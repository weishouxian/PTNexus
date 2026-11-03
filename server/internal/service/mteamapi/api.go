package mteamapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultAPIBase 为 M-Team 的 API 根地址。站点 Web 域名与 API 域名相互独立。
const DefaultAPIBase = "https://api.m-team.cc"

// WebUserAgent 为站点要求的浏览器 UA。
// 站点 nginx 对无 UA 的请求会 302 跳转，所有接口调用都必须带上。
const WebUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36"

// Response 为 M-Team 通用接口响应。
// code 在不同接口可能是字符串 "0" 也可能是数字 0，故统一用 RawMessage 承载。
type Response struct {
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// CodeString 返回规范化后的 code 文本（统一去掉 JSON 引号）。
func (r Response) CodeString() string {
	return strings.Trim(strings.TrimSpace(string(r.Code)), `"`)
}

// IsSuccess 判断响应是否为成功（code 为 0 或缺失）。
func (r Response) IsSuccess() bool {
	code := r.CodeString()
	return code == "" || code == "0"
}

// DataString 在 data 为字符串时返回其内容，否则返回空串。
func (r Response) DataString() string {
	trimmed := strings.TrimSpace(string(r.Data))
	if trimmed == "" || trimmed == "null" || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return ""
	}
	return strings.Trim(trimmed, `"`)
}

// APIBaseFor 推导 M-Team 的 API 根地址。
//
// 优先级：环境变量 MTEAM_API_BASE > 给定地址中已是 api 域名的那个 > DefaultAPIBase。
// 允许环境变量覆盖，是为了站点更换域名时无需重新编译即可切换。
func APIBaseFor(candidates ...string) string {
	if override := strings.TrimSpace(os.Getenv("MTEAM_API_BASE")); override != "" {
		return strings.TrimRight(override, "/")
	}
	for _, raw := range candidates {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		lower := strings.ToLower(trimmed)
		if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
			trimmed = "https://" + trimmed
		}
		parsed, err := url.Parse(trimmed)
		if err != nil {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if strings.HasPrefix(host, "api.") {
			return strings.TrimRight(parsed.Scheme+"://"+parsed.Host, "/")
		}
	}
	return DefaultAPIBase
}

// GenDlToken 调用 /api/torrent/genDlToken 换取种子下载直链。
// 参数/返回：apiBase 为 API 根地址，apiKey 为存取令牌，torrentID 为数字种子 ID；返回可下载的直链。
// 失败场景：缺少令牌/ID、网络错误、HTTP 非 2xx、响应非 JSON、接口返回 code!=0、未返回直链。
// 副作用：发起网络请求（站点对请求频率有限制，同一顆種子每天最多下载 10 次）。
//
// 注意：接口超限时仍会返回 HTTP 200，错误信息在响应体里，因此必须解析响应体而不能只看状态码。
func GenDlToken(apiBase, apiKey, torrentID string, timeout time.Duration) (string, error) {
	root := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if root == "" {
		root = DefaultAPIBase
	}
	token := strings.TrimSpace(apiKey)
	if token == "" {
		return "", errors.New("缺少 API 令牌")
	}
	id := strings.TrimSpace(torrentID)
	if id == "" {
		return "", errors.New("缺少种子 ID")
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("id", id); err != nil {
		return "", fmt.Errorf("构造表单字段失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("结束表单写入失败: %w", err)
	}

	endpoint := root + "/api/torrent/genDlToken"
	request, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	request.Header.Set("User-Agent", WebUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("x-api-key", token)

	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("请求 m-team 失败: %w", err)
	}
	defer response.Body.Close()

	respBody, err := io.ReadAll(response.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("m-team 返回 HTTP %d: %s", response.StatusCode, BodySnippet(respBody))
	}

	var parsed Response
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("m-team 响应解析失败: %s", BodySnippet(respBody))
	}
	if !parsed.IsSuccess() {
		message := strings.TrimSpace(parsed.Message)
		if message == "" {
			message = BodySnippet(respBody)
		}
		return "", fmt.Errorf("m-team 接口返回失败(code=%s): %s", parsed.CodeString(), message)
	}

	link := strings.TrimSpace(parsed.DataString())
	if !strings.HasPrefix(strings.ToLower(link), "http") {
		return "", fmt.Errorf("m-team 接口未返回下载直链: %s", BodySnippet(respBody))
	}
	return link, nil
}

// DetailData 表示 M-Team 种子详情接口 /api/torrent/detail 返回的 data 字段。
// 只保留抓取/提取侧需要的字段，其余字段不解析（接口返回的完整 JSON 远多于此）。
type DetailData struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	SmallDescr   string   `json:"smallDescr"`
	Descr        string   `json:"descr"`
	MediaInfo    string   `json:"mediainfo"`
	ImageList    []string `json:"imageList"`
	Category     string   `json:"category"`
	Standard     string   `json:"standard"`
	VideoCodec   string   `json:"videoCodec"`
	AudioCodec   string   `json:"audioCodec"`
	Source       string   `json:"source"`
	Processing   string   `json:"processing"`
	Medium       string   `json:"medium"`
	Team         string   `json:"team"`
	Countries    []string `json:"countries"`
	LabelsNew    []string `json:"labelsNew"`
	IMDb         string   `json:"imdb"`
	Douban       string   `json:"douban"`
	DoubanRating string   `json:"doubanRating"`
	IMDbRating   string   `json:"imdbRating"`
	Bangumi      string   `json:"bangumi"`
}

// Detail 调用 /api/torrent/detail 读取种子详情。
// 参数/返回：apiBase 为 API 根地址，apiKey 为存取令牌，torrentID 为数字种子 ID；返回详情结构。
// 失败场景：缺少令牌/ID、网络错误、HTTP 非 2xx、响应非 JSON、接口返回 code!=0。
// 副作用：发起一次 HTTP 请求。
//
// 注意：该接口与发种的 createOredit、下载的 genDlToken 同属 x-api-key 鉴权体系，
// 用控制台「存取令牌」即可调通（已实测 code=0），无需网页端的 authorization JWT 与 _sgin 签名。
func Detail(apiBase, apiKey, torrentID string, timeout time.Duration) (DetailData, error) {
	var detail DetailData
	root := strings.TrimRight(strings.TrimSpace(apiBase), "/")
	if root == "" {
		root = DefaultAPIBase
	}
	token := strings.TrimSpace(apiKey)
	if token == "" {
		return detail, errors.New("缺少 API 令牌")
	}
	id := strings.TrimSpace(torrentID)
	if id == "" {
		return detail, errors.New("缺少种子 ID")
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.WriteField("id", id); err != nil {
		return detail, fmt.Errorf("构造表单字段失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return detail, fmt.Errorf("结束表单写入失败: %w", err)
	}

	endpoint := root + "/api/torrent/detail"
	request, err := http.NewRequest(http.MethodPost, endpoint, body)
	if err != nil {
		return detail, fmt.Errorf("构造请求失败: %w", err)
	}
	request.Header.Set("User-Agent", WebUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("x-api-key", token)

	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return detail, fmt.Errorf("请求 m-team 失败: %w", err)
	}
	defer response.Body.Close()

	respBody, err := io.ReadAll(response.Body)
	if err != nil {
		return detail, fmt.Errorf("读取响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return detail, fmt.Errorf("m-team 返回 HTTP %d: %s", response.StatusCode, BodySnippet(respBody))
	}

	var parsed Response
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return detail, fmt.Errorf("m-team 响应解析失败: %s", BodySnippet(respBody))
	}
	if !parsed.IsSuccess() {
		message := strings.TrimSpace(parsed.Message)
		if message == "" {
			message = BodySnippet(respBody)
		}
		return detail, fmt.Errorf("m-team 接口返回失败(code=%s): %s", parsed.CodeString(), message)
	}

	if err := json.Unmarshal(parsed.Data, &detail); err != nil {
		return detail, fmt.Errorf("m-team 种子详情解析失败: %w", err)
	}
	if strings.TrimSpace(detail.Name) == "" {
		return detail, fmt.Errorf("m-team 种子详情缺少 name（种子可能已被删除或未通过审核）")
	}
	return detail, nil
}

// MemberProfile 表示 M-Team /api/member/profile 返回的用户信息。
// 只保留用户 ID 与昵称/账号名，其余字段不解析。
type MemberProfile struct {
	ID       string `json:"id"`
	Nickname string `json:"nickname"`
	Account  string `json:"account"`
	Username string `json:"username"`
}

// DisplayName 返回用户的可读名称，优先昵称，其次账号名，再次 username。
func (p MemberProfile) DisplayName() string {
	for _, name := range []string{p.Nickname, p.Account, p.Username} {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Profile 调用 /api/member/profile 读取当前令牌对应用户的信息。
// 参数/返回：apiBase 为 API 根地址，apiKey 为存取令牌；返回用户 ID 与昵称。
// 失败场景：缺少令牌、网络错误、HTTP 非 2xx、响应非 JSON、接口返回 code!=0。
// 副作用：发起一次 HTTP 请求。
//
// 说明：该端点在 M-Team 官方「允许第三方调用」清单内（/member/profile），
// 用 x-api-key 鉴权即可调通；响应既可能是 {result:{...}} 也可能是 {data:{...}}，
// 故解析时两种包裹都尝试。
//
// 注意：M-Team 官方 API **只支持 POST**，GET 会返回 code=1「Request method 'GET'
// is not supported」，故此处用 POST（空 form body）。
func Profile(apiBase, apiKey string, timeout time.Duration) (MemberProfile, error) {
	var profile MemberProfile
	// apiBase 可能是网页域名（如 kp.m-team.cc）或 API 域名；用 APIBaseFor 统一推导到 API 根。
	root := APIBaseFor(apiBase)
	token := strings.TrimSpace(apiKey)
	if token == "" {
		return profile, errors.New("缺少 API 令牌")
	}
	if timeout <= 0 {
		timeout = 45 * time.Second
	}

	endpoint := root + "/api/member/profile"
	request, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return profile, fmt.Errorf("构造请求失败: %w", err)
	}
	request.Header.Set("User-Agent", WebUserAgent)
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("x-api-key", token)

	client := &http.Client{Timeout: timeout}
	response, err := client.Do(request)
	if err != nil {
		return profile, fmt.Errorf("请求 m-team 失败: %w", err)
	}
	defer response.Body.Close()

	respBody, err := io.ReadAll(response.Body)
	if err != nil {
		return profile, fmt.Errorf("读取响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return profile, fmt.Errorf("m-team 返回 HTTP %d: %s", response.StatusCode, BodySnippet(respBody))
	}

	// 先按通用响应解析 code/message，判断业务是否成功。
	var parsed Response
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return profile, fmt.Errorf("m-team 响应解析失败: %s", BodySnippet(respBody))
	}
	if !parsed.IsSuccess() {
		message := strings.TrimSpace(parsed.Message)
		if message == "" {
			message = BodySnippet(respBody)
		}
		return profile, fmt.Errorf("m-team 接口返回失败(code=%s): %s", parsed.CodeString(), message)
	}

	// 用户信息可能被 result 或 data 字段包裹（不同接口/版本字段名不同），也可能平铺在顶层。
	// 用 RawMessage 兜底，按 result > data > 顶层 顺序取第一个非空对象。
	var wrapper map[string]json.RawMessage
	_ = json.Unmarshal(respBody, &wrapper)
	payload := respBody
	for _, key := range []string{"result", "data"} {
		if raw, ok := wrapper[key]; ok && len(raw) > 0 && raw[0] == '{' {
			payload = raw
			break
		}
	}
	if err := json.Unmarshal(payload, &profile); err != nil {
		return profile, fmt.Errorf("m-team 用户信息解析失败: %s", BodySnippet(respBody))
	}
	if strings.TrimSpace(profile.ID) == "" && strings.TrimSpace(profile.DisplayName()) == "" {
		return profile, fmt.Errorf("m-team 用户信息缺少 id/nickname（令牌可能无效）")
	}
	return profile, nil
}

// BodySnippet 把响应体裁剪成单行短文本，用于日志与错误信息，避免刷屏。
func BodySnippet(body []byte) string {
	const limit = 200
	text := strings.Join(strings.Fields(string(body)), " ")
	if text == "" {
		return "(空响应)"
	}
	runes := []rune(text)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return text
}
