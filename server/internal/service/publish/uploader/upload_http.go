package uploader

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	neturl "net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/netproxy"
)

const (
	uploadRequestTimeout       = 180 * time.Second
	uploadTLSHandshakeTimeout  = 45 * time.Second
	uploadNetworkRetryAttempts = 3
	uploadResponseSummaryLimit = 600
)

var reHTMLTitle = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var reUploadFormTag = regexp.MustCompile(`(?is)<form\b[^>]*>.*?</form>`)
var reHiddenInputTag = regexp.MustCompile(`(?is)<input\b[^>]*>`)
var reSelectTag = regexp.MustCompile(`(?is)<select\b[^>]*>.*?</select>`)
var reOptionTag = regexp.MustCompile(`(?is)<option\b[^>]*>.*?</option>`)
var reStripHTMLTags = regexp.MustCompile(`(?is)<[^>]+>`)
var reHTMLAttr = regexp.MustCompile(`(?is)\s([a-zA-Z_:][-a-zA-Z0-9_:.]*)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>` + "`" + `]+))`)
var reUploadNoticeHeading = regexp.MustCompile(`(?is)<h[1-4][^>]*>(.*?)</h[1-4]>`)
var reUploadNoticeParagraph = regexp.MustCompile(`(?is)<p[^>]*>(.*?)</p>`)

// reUploadNoticeStdMsgOpen 匹配 NexusPHP 标准提示块的起始标签（如 <td class="text" id='stdmsg'>）。
// 站点在 h1/p 之外的拒绝场景（如资质不足、无发布权限）统一把提示正文写进 id='stdmsg' 的单元格。
var reUploadNoticeStdMsgOpen = regexp.MustCompile(`(?is)<\s*(td|th|p|div|span|font|dd|li)\b[^>]*\bid\s*=\s*['"]?stdmsg['"]?[^>]*>`)

// uploadNoticeStdMsgCloseTags 预编译各标签的闭合标签正则，避免每次解析 stdmsg 块时重复编译。
var uploadNoticeStdMsgCloseTags = map[string]*regexp.Regexp{
	"td":   regexp.MustCompile(`(?i)</td\s*>`),
	"th":   regexp.MustCompile(`(?i)</th\s*>`),
	"p":    regexp.MustCompile(`(?i)</p\s*>`),
	"div":  regexp.MustCompile(`(?i)</div\s*>`),
	"span": regexp.MustCompile(`(?i)</span\s*>`),
	"font": regexp.MustCompile(`(?i)</font\s*>`),
	"dd":   regexp.MustCompile(`(?i)</dd\s*>`),
	"li":   regexp.MustCompile(`(?i)</li\s*>`),
}

// uploadNoticeErrorKeywords 用于判定 h1 标题是否为站点失败/错误提示（NexusPHP 错误页形如 <h1>上传失败！</h1><p>原因</p>）。
var uploadNoticeErrorKeywords = []string{
	"失败", "错误", "出错", "异常", "禁止", "不允许", "无效",
	"error", "failed", "failure", "invalid", "denied",
}

// uploadNoticeDenialKeywords 用于判定 id='stdmsg' 提示块是否为「被站点拒绝」的原因说明。
// stdmsg 是 NexusPHP 的通用提示框，成功/中性文案也会用到，因此不能只看有没有 stdmsg，
// 必须要求正文含「权限/条件不足/被拒绝」这类语义，否则不当作错误提示。
var uploadNoticeDenialKeywords = []string{
	"无权", "权限", "不允许", "禁止", "限制", "不足", "拒绝", "条件", "必须",
	"至少", "只有", "无法", "不能", "需要先", "请先", "缺少", "过期", "封禁",
	"denied", "not allowed", "permission", "insufficient", "forbidden",
}

// uploadForbiddenHint 站点以 401/403 拒绝上传时给出的统一排查建议。
// 单凭状态码分不清是登录态失效（Cookie 过期、被 WAF/人机校验拦截）还是账号本身没有发种权限，
// 所以两种可能都必须写进提示，避免用户只去重登却忽略了权限问题。
const uploadForbiddenHint = "请检查站点 Cookie 是否已失效，或当前账号是否具备发种权限"

// rePublishDetailLink 命中即认为响应是发布成功后的详情页，此时页面里的 stdmsg 属中性提示而非失败原因。
// 前导分隔符不可省略：用户资料页 userdetails.php?id=xxx 同样含 "details.php?id="，不设边界会被误判成发布成功。
var rePublishDetailLink = regexp.MustCompile(`(?i)(^|[\s"'/=?&])details\.php\?id=\d+`)

type uploadFormFields struct {
	Hidden  map[string]string
	Selects map[string][]string
}

// TryUploadTorrent 执行单次上传尝试，支持重定向 Location 与响应正文解析详情页链接。
// 参数/返回：uploadURL/baseURL/cookie/fileField 为站点上传所需信息；torrentFile 为种子字节；formFields 为表单字段；
// 返回发布详情页 URL、是否疑似“种子已存在”、本次尝试日志，以及错误。
// 失败场景：HTTP 请求失败、服务端返回错误等；若站点仅提示“种子已存在”但未返回详情页，将按已存在成功处理。
// HTTP 401/403 视为“站点拒绝/无权访问”，提示统一收敛为「检查 Cookie 是否失效或是否有发种权限」，不再倾倒整页 HTML。
// 副作用：发起 HTTP 请求并读取响应正文。
func TryUploadTorrent(uploadURL, baseURL, cookie, fileField string, torrentFile []byte, fileName string, formFields map[string]string) (string, bool, string, error) {
	detailLines := []string{
		fmt.Sprintf("正在上传种子文件..."),
		fmt.Sprintf("上传端点: %s (文件字段: %s)", strings.TrimSpace(uploadURL), strings.TrimSpace(fileField)),
	}
	buildDetail := func() string {
		return strings.Join(detailLines, "\n")
	}

	client := newUploadHTTPClient()
	mergedFields := mergeUploadFormHiddenFields(client, baseURL, cookie, formFields, &detailLines)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	for key, value := range mergedFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		_ = writer.WriteField(key, value)
	}
	part, createFileErr := writer.CreateFormFile(fileField, fileName)
	if createFileErr != nil {
		detailLines = append(detailLines, fmt.Sprintf("构建上传表单失败: %v", createFileErr))
		return "", false, buildDetail(), createFileErr
	}
	if _, err := part.Write(torrentFile); err != nil {
		detailLines = append(detailLines, fmt.Sprintf("写入种子内容失败: %v", err))
		return "", false, buildDetail(), err
	}
	if err := writer.Close(); err != nil {
		detailLines = append(detailLines, fmt.Sprintf("封装上传请求失败: %v", err))
		return "", false, buildDetail(), err
	}

	contentType := writer.FormDataContentType()
	payloadBytes := append([]byte(nil), body.Bytes()...)

	resp := (*http.Response)(nil)
	var err error
	for attempt := 1; attempt <= uploadNetworkRetryAttempts; attempt++ {
		requestBody := bytes.NewReader(payloadBytes)
		req, buildErr := http.NewRequest(http.MethodPost, uploadURL, requestBody)
		if buildErr != nil {
			detailLines = append(detailLines, fmt.Sprintf("创建 HTTP 请求失败: %v", buildErr))
			return "", false, buildDetail(), buildErr
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Cookie", cookie)
		req.Header.Set("Referer", strings.TrimRight(baseURL, "/")+"/upload.php")

		resp, err = client.Do(req)
		if err == nil {
			break
		}

		detailLines = append(detailLines, fmt.Sprintf("请求失败 (第 %d/%d 次): %v", attempt, uploadNetworkRetryAttempts, err))
		if !ShouldRetryUploadNetworkError(err) || attempt >= uploadNetworkRetryAttempts {
			return "", false, buildDetail(), err
		}

		backoff := time.Duration(attempt) * 2 * time.Second
		detailLines = append(detailLines, fmt.Sprintf("检测到网络波动，%.0f 秒后重试...", backoff.Seconds()))
		time.Sleep(backoff)
	}
	if err != nil {
		return "", false, buildDetail(), err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	bodyText := string(respBody)

	isExisting := looksLikeExistingTorrent(bodyText)
	uploadNotice := extractUploadNotice(bodyText)
	responseDetail := summarizeResponseBody(bodyText)
	detailLines = append(detailLines, fmt.Sprintf("响应状态: %d %s", resp.StatusCode, strings.TrimSpace(resp.Status)))

	// 检查 Location URL 中是否包含 existed=1 / exist=1 参数
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location != "" {
		detailLines = append(detailLines, fmt.Sprintf("Location: %s", redactURLQuery(location)))
	}
	if responseDetail != "" {
		detailLines = append(detailLines, fmt.Sprintf("站点响应摘要: %s", responseDetail))
	}
	if hasExistingFlagInLocation(location) {
		isExisting = true
	}
	if isExisting {
		detailLines = append(detailLines, "✓ 种子已存在（站点已有相同种子）")
	}

	if location != "" {
		if publishURL := NormalizePublishURLWithOfferSupport(baseURL, location); publishURL != "" && isValidPublishedDetailURL(publishURL) {
			detailLines = append(detailLines, fmt.Sprintf("解析详情页: %s", publishURL))
			return publishURL, isExisting, buildDetail(), nil
		}
	}
	if publishURL := ExtractPublishURLFromText(baseURL, bodyText); publishURL != "" && isValidPublishedDetailURL(publishURL) {
		detailLines = append(detailLines, fmt.Sprintf("解析详情页: %s", publishURL))
		return publishURL, isExisting, buildDetail(), nil
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		if looksLikeUploadLoginResult(location, respBody) {
			err = errLikelyLoginPage
			appendUploadRawResponse(&detailLines, bodyText)
			detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
			return "", isExisting, buildDetail(), err
		}
		if strings.Contains(bodyText, "已存在") || strings.Contains(bodyText, "已经上传") || strings.Contains(strings.ToLower(bodyText), "already exists") {
			if publishURL := ExtractPublishURLFromText(baseURL, bodyText); publishURL != "" && isValidPublishedDetailURL(publishURL) {
				detailLines = append(detailLines, fmt.Sprintf("解析详情页: %s", publishURL))
				return publishURL, true, buildDetail(), nil
			}
			detailLines = append(detailLines, "尝试结论: 目标站点提示种子已存在，但未返回详情链接，按已存在处理")
			return "", true, buildDetail(), nil
		}
		if looksLikeUploadFormPage(bodyText) {
			if uploadNotice != "" {
				// 站点已经给出明确原因（如 stdmsg 里的字段校验提示），提示本身比“返回了表单页”更有价值。
				err = fmt.Errorf("站点返回上传表单页面: %s", uploadNotice)
			} else {
				err = fmt.Errorf("站点返回上传表单页面，可能是字段缺失、校验未通过或会话状态异常")
				appendUploadRawResponse(&detailLines, bodyText)
			}
			detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
			return "", isExisting, buildDetail(), err
		}
	}

	if isUploadForbiddenStatus(resp.StatusCode) {
		// 401/403 是「站点拒绝本次请求」，此时正文往往是 Cloudflare 人机校验页或 403 错误页，
		// 整页 HTML 既无排查价值又会淹没真正的结论，因此只输出可执行的排查建议。
		err = buildUploadForbiddenError(resp.StatusCode, bodyText, uploadNotice)
		if !looksLikeCloudflareChallenge(bodyText) {
			appendUploadRawResponse(&detailLines, bodyText)
		}
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", isExisting, buildDetail(), err
	}

	if responseDetail == "" {
		responseDetail = "<empty response body>"
	}
	if uploadNotice != "" {
		// 站点已给出明确失败提示，提示本身即全部有效信息，无需再倾倒整页 HTML。
		err = fmt.Errorf("HTTP %d 上传失败: %s", resp.StatusCode, uploadNotice)
	} else {
		err = fmt.Errorf("HTTP %d 上传失败: %s", resp.StatusCode, responseDetail)
		appendUploadRawResponse(&detailLines, bodyText)
	}
	detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
	return "", isExisting, buildDetail(), err
}

// ShouldRetryUploadNetworkError 判断上传请求错误是否属于可重试的网络异常。
// 参数/返回：err 为请求返回错误；当错误可能由瞬时网络抖动导致时返回 true。
// 失败场景：无失败场景，未知错误按不可重试处理。
// 副作用：无。
func ShouldRetryUploadNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	for _, keyword := range []string{
		"tls handshake timeout",
		"i/o timeout",
		"context deadline exceeded",
		"timeout awaiting response headers",
		"connection reset by peer",
		"connection refused",
		"unexpected eof",
		"temporary failure",
		"no route to host",
	} {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

// mergeUploadFormHiddenFields 读取上传页 takeupload 表单中的隐藏字段并与发布字段合并。
func mergeUploadFormHiddenFields(client *http.Client, baseURL string, cookie string, formFields map[string]string, detailLines *[]string) map[string]string {
	merged := map[string]string{}
	appendField := func(key string, value string) {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			return
		}
		merged[trimmedKey] = strings.TrimSpace(value)
	}

	formMeta, err := fetchUploadFormFields(client, baseURL, cookie)
	if err != nil {
		if detailLines != nil {
			*detailLines = append(*detailLines, fmt.Sprintf("读取上传页隐藏字段失败，继续直接提交: %v", err))
		}
	} else if len(formMeta.Hidden) > 0 {
		for key, value := range formMeta.Hidden {
			appendField(key, value)
		}
		if detailLines != nil {
			*detailLines = append(*detailLines, fmt.Sprintf("已合并上传页隐藏字段: %d 个", len(formMeta.Hidden)))
		}
	}

	for key, value := range formFields {
		appendField(key, value)
	}
	resolveUploadSelectIndexMarkers(merged, formMeta.Selects, detailLines)
	return merged
}

func fetchUploadFormFields(client *http.Client, baseURL string, cookie string) (uploadFormFields, error) {
	if client == nil {
		client = newUploadHTTPClient()
	}
	uploadPageURL := strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/upload.php"
	req, err := http.NewRequest(http.MethodGet, uploadPageURL, nil)
	if err != nil {
		return uploadFormFields{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		return uploadFormFields{}, err
	}
	defer resp.Body.Close()
	if isUploadForbiddenStatus(resp.StatusCode) {
		// 读上传页就 403/401，说明登录态或发种权限有问题，直接给出排查方向而不是干巴巴的状态码。
		return uploadFormFields{}, fmt.Errorf("上传页响应状态异常: %d（%s）", resp.StatusCode, uploadForbiddenHint)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return uploadFormFields{}, fmt.Errorf("上传页响应状态异常: %d", resp.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return uploadFormFields{}, err
	}
	return extractUploadFormFields(string(content)), nil
}

// extractUploadFormFields 从上传页 HTML 中提取 takeupload 表单隐藏字段与下拉选项。
func extractUploadFormFields(pageHTML string) uploadFormFields {
	result := uploadFormFields{
		Hidden:  map[string]string{},
		Selects: map[string][]string{},
	}
	for _, form := range reUploadFormTag.FindAllString(pageHTML, -1) {
		formLower := strings.ToLower(form)
		if !strings.Contains(formLower, "takeupload.php") {
			continue
		}
		for _, input := range reHiddenInputTag.FindAllString(form, -1) {
			attrs := parseHTMLAttributes(input)
			if !strings.EqualFold(strings.TrimSpace(attrs["type"]), "hidden") {
				continue
			}
			name := strings.TrimSpace(attrs["name"])
			if name == "" {
				continue
			}
			result.Hidden[name] = strings.TrimSpace(attrs["value"])
		}
		for _, selectTag := range reSelectTag.FindAllString(form, -1) {
			attrs := parseHTMLAttributes(selectTag)
			name := strings.TrimSpace(attrs["name"])
			if name == "" {
				continue
			}
			values := make([]string, 0, 16)
			for _, optionTag := range reOptionTag.FindAllString(selectTag, -1) {
				optionAttrs := parseHTMLAttributes(optionTag)
				value, exists := optionAttrs["value"]
				if !exists {
					value = optionLabelText(optionTag)
				}
				values = append(values, strings.TrimSpace(value))
			}
			if len(values) > 0 {
				result.Selects[name] = values
			}
		}
	}
	return result
}

func resolveUploadSelectIndexMarkers(formFields map[string]string, selects map[string][]string, detailLines *[]string) {
	for key, value := range formFields {
		idx, ok := parseUploadIndexMarker(value)
		if !ok {
			continue
		}
		options := selects[strings.TrimSpace(key)]
		if idx < 0 || idx >= len(options) {
			formFields[key] = fmt.Sprintf("%d", idx)
			continue
		}
		resolved := strings.TrimSpace(options[idx])
		if resolved == "" {
			formFields[key] = fmt.Sprintf("%d", idx)
			continue
		}
		formFields[key] = resolved
		if detailLines != nil {
			*detailLines = append(*detailLines, fmt.Sprintf("已按上传页选项索引解析字段: %s index=%d", key, idx))
		}
	}
}

func parseUploadIndexMarker(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "@index:") {
		return 0, false
	}
	idxText := strings.TrimSpace(strings.TrimPrefix(trimmed, "@index:"))
	idx, err := strconv.Atoi(idxText)
	if err != nil {
		return 0, false
	}
	return idx, true
}

func optionLabelText(optionTag string) string {
	text := reStripHTMLTags.ReplaceAllString(optionTag, "")
	return html.UnescapeString(strings.Join(strings.Fields(strings.TrimSpace(text)), " "))
}

// parseHTMLAttributes 解析单个 HTML 标签上的属性键值。
func parseHTMLAttributes(tag string) map[string]string {
	result := map[string]string{}
	for _, match := range reHTMLAttr.FindAllStringSubmatch(tag, -1) {
		if len(match) < 5 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(match[1]))
		value := firstNonEmptyUploadAttr(match[2], match[3], match[4])
		if key != "" {
			result[key] = html.UnescapeString(value)
		}
	}
	return result
}

func firstNonEmptyUploadAttr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func looksLikeExistingTorrent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(trimmed, "种子已存在") ||
		strings.Contains(trimmed, "该种子已存在") ||
		strings.Contains(trimmed, "已存在") ||
		strings.Contains(trimmed, "已经上传") ||
		strings.Contains(lower, "already exists")
}

func summarizeResponseBody(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if notice := extractUploadNotice(trimmed); notice != "" {
		// 站点返回失败提示页（如 NexusPHP 的 <h1>上传失败！</h1><p>原因</p>）时，
		// 只保留标题与原因正文，避免整页 CSS/JS 把关键信息淹没。
		return truncateLogText("站点错误提示: "+notice, uploadResponseSummaryLimit)
	}
	normalized := strings.Join(strings.Fields(trimmed), " ")
	title := extractUploadHTMLTitle(trimmed)
	if looksLikeUploadFormPage(trimmed) {
		return truncateLogText(fmt.Sprintf("返回上传表单页面 (title=%s): %s", title, normalized), uploadResponseSummaryLimit)
	}
	if looksLikeHTMLResponse(trimmed) {
		if title != "" {
			return truncateLogText(fmt.Sprintf("返回 HTML 页面 (title=%s): %s", title, normalized), uploadResponseSummaryLimit)
		}
		return truncateLogText("返回 HTML 页面: "+normalized, uploadResponseSummaryLimit)
	}
	return truncateLogText(normalized, uploadResponseSummaryLimit)
}

// extractUploadNotice 从站点返回的 HTML 页面中提取失败/错误提示正文。
// 参数/返回：text 为响应正文；命中 NexusPHP 风格错误页时返回“标题 + 紧随其后的说明段落”，
// 或命中 id='stdmsg' 提示块时返回“提示标题 + 正文”，否则返回空串。
// 失败场景：两种页面结构都不命中时返回空串，由调用方回退到原始摘要逻辑。
// 副作用：无。
//
// 背景一：Audiences（人人）等 NexusPHP 站点发种校验失败时返回 HTTP 200 + 错误页，形如
// <h1>上传失败！</h1><p>非完结剧集需在主标题内标明集数 SxxExx ，已完结剧集需勾选完结标签，请重新检查并修正。</p>
// 页面里广告位等元素也含 <p>，因此必须以 h1 的位置为锚点向后取段落。
//
// 背景二：部分站点（如 OurBits/我堡）在上传前做资格校验，被拒时返回 HTTP 200 + 提示页，形如
// <h2>资质不足</h2><table ...><tr><td class="text" id='stdmsg'>只有已经成功通过审核10个种子…</td></tr></table>
// 这种页面既没有错误关键词标题，也没有 <p> 正文，只能以 id='stdmsg' 为锚点提取。
func extractUploadNotice(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}

	if notice := extractUploadHeadingNotice(trimmed); notice != "" {
		return notice
	}
	return extractUploadStdMsgNotice(trimmed)
}

// extractUploadHeadingNotice 处理「错误关键词标题 + 紧随段落」形态的提示页。
func extractUploadHeadingNotice(trimmed string) string {
	heading := ""
	headingEnd := -1
	for _, match := range reUploadNoticeHeading.FindAllStringSubmatchIndex(trimmed, -1) {
		if len(match) < 4 {
			continue
		}
		candidate := cleanUploadNoticeText(trimmed[match[2]:match[3]])
		if candidate == "" || !containsUploadNoticeErrorKeyword(candidate) {
			continue
		}
		heading = candidate
		headingEnd = match[1]
		break
	}
	if heading == "" || headingEnd < 0 || headingEnd >= len(trimmed) {
		return ""
	}

	body := ""
	if match := reUploadNoticeParagraph.FindStringSubmatch(trimmed[headingEnd:]); len(match) >= 2 {
		body = cleanUploadNoticeText(match[1])
	}
	if body == "" {
		return truncateLogText(heading, 300)
	}
	return truncateLogText(heading+body, 300)
}

// extractUploadStdMsgNotice 处理 NexusPHP 通用提示块 id='stdmsg'（如「资质不足」页）。
// 参数/返回：text 为响应正文；命中且正文语义为“被拒绝/条件不足”时返回“标题 + 正文”，否则返回空串。
// 失败场景：无 stdmsg 标签、正文为空、正文不含拒绝语义、或响应其实是发布成功的详情页时返回空串。
// 副作用：无。
func extractUploadStdMsgNotice(trimmed string) string {
	loc := reUploadNoticeStdMsgOpen.FindStringSubmatchIndex(trimmed)
	if len(loc) < 4 || loc[1] >= len(trimmed) {
		return ""
	}
	// 发布成功后的详情页里也会带 stdmsg（中性提示），此时不应把页面内容当成失败原因。
	if rePublishDetailLink.MatchString(trimmed) {
		return ""
	}

	tagName := strings.ToLower(trimmed[loc[2]:loc[3]])
	closeTag := uploadNoticeStdMsgCloseTags[tagName]
	if closeTag == nil {
		return ""
	}
	closeLoc := closeTag.FindStringIndex(trimmed[loc[1]:])
	if closeLoc == nil {
		return ""
	}
	body := cleanUploadNoticeText(trimmed[loc[1] : loc[1]+closeLoc[0]])
	if body == "" || !containsAnyKeyword(body, uploadNoticeDenialKeywords) {
		return ""
	}

	// 取 stdmsg 之前最近的小标题（如「资质不足」），过长则视为站点通用标题而丢弃。
	prefix := trimmed[:loc[0]]
	heading := ""
	for _, match := range reUploadNoticeHeading.FindAllStringSubmatchIndex(prefix, -1) {
		if len(match) < 4 {
			continue
		}
		candidate := cleanUploadNoticeText(prefix[match[2]:match[3]])
		if candidate != "" && len([]rune(candidate)) <= 40 {
			heading = candidate
		}
	}
	if heading == "" {
		return truncateLogText(body, 300)
	}
	return truncateLogText(heading+"："+body, 300)
}

// cleanUploadNoticeText 去除 HTML 标签并归一化空白，用于把提示片段转成可读单行文本。
func cleanUploadNoticeText(fragment string) string {
	cleaned := reStripHTMLTags.ReplaceAllString(fragment, " ")
	cleaned = html.UnescapeString(cleaned)
	return strings.Join(strings.Fields(strings.TrimSpace(cleaned)), " ")
}

// containsUploadNoticeErrorKeyword 判断提示标题是否包含失败/错误语义关键词。
func containsUploadNoticeErrorKeyword(text string) bool {
	return containsAnyKeyword(text, uploadNoticeErrorKeywords)
}

// containsAnyKeyword 判断文本（小写化后）是否包含任一给定关键词。
func containsAnyKeyword(text string, keywords []string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	for _, keyword := range keywords {
		if strings.Contains(lower, keyword) {
			return true
		}
	}
	return false
}

func appendUploadRawResponse(detailLines *[]string, bodyText string) {
	if detailLines == nil {
		return
	}
	if strings.TrimSpace(bodyText) == "" {
		return
	}
	*detailLines = append(*detailLines, "--- [站点原始响应] ---")
	*detailLines = append(*detailLines, bodyText)
}

func newUploadHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   uploadTLSHandshakeTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	netproxy.ConfigureTransport(transport)
	return &http.Client{
		Timeout:   uploadRequestTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func looksLikeUploadFormPage(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return false
	}
	if !strings.Contains(lower, "<form") {
		return false
	}
	if strings.Contains(lower, "action=\"takeupload.php\"") || strings.Contains(lower, "action='takeupload.php'") {
		return true
	}
	return strings.Contains(lower, "name=\"upload\"") || strings.Contains(lower, "发布 - powered by nexusphp")
}

func looksLikeHTMLResponse(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "<!doctype html") || strings.Contains(lower, "<html")
}

// isUploadForbiddenStatus 判断上传响应状态码是否属于“被站点拒绝/无权访问”。
// 参数/返回：statusCode 为 HTTP 状态码；401（未登录）与 403（无权限）返回 true。
// 失败场景：无。
// 副作用：无。
func isUploadForbiddenStatus(statusCode int) bool {
	return statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden
}

// looksLikeCloudflareChallenge 判断响应正文是否为 Cloudflare 人机校验/拦截页。
// 参数/返回：text 为响应正文；命中 Cloudflare challenge 特征时返回 true。
// 失败场景：正文为空或为正常站点页面时返回 false。
// 副作用：无。
//
// 背景：UBits 等站前置 Cloudflare，Cookie 失效或请求被判定为自动化时，upload.php 会直接返回 403 +
// <title>Just a moment...</title> 的挑战页（正文是几十 KB 的混淆脚本）。这类页面必须单独识别，
// 否则既会顶替掉「Cookie/权限」这个真实结论，也会把整页脚本写进发布日志。
func looksLikeCloudflareChallenge(text string) bool {
	sample := text
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	lower := strings.ToLower(sample)
	if strings.TrimSpace(lower) == "" {
		return false
	}
	for _, marker := range []string{
		"challenges.cloudflare.com",
		"/cdn-cgi/challenge-platform",
		"cf_chl_opt",
		"cf-chl-",
		"just a moment",
		"checking your browser",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// buildUploadForbiddenError 生成 401/403 上传失败的提示文本。
// 参数/返回：statusCode 为站点状态码；bodyText 为响应正文（用于识别人机校验页）；uploadNotice 为站点给出的失败原因（可为空）。
// 失败场景：无，任何输入都会返回可读错误。
// 副作用：无。
func buildUploadForbiddenError(statusCode int, bodyText string, uploadNotice string) error {
	if notice := strings.TrimSpace(uploadNotice); notice != "" {
		// 站点已经写明了拒绝原因（如「你没有发布权限」），原样保留并附上排查建议。
		return fmt.Errorf("HTTP %d 上传失败: %s（%s）", statusCode, notice, uploadForbiddenHint)
	}
	if looksLikeCloudflareChallenge(bodyText) {
		return fmt.Errorf("HTTP %d 上传失败: 站点返回 Cloudflare 人机校验页面（Cookie 失效或请求被拦截时会出现），%s", statusCode, uploadForbiddenHint)
	}
	return fmt.Errorf("HTTP %d 上传失败: 站点拒绝了本次上传请求，%s", statusCode, uploadForbiddenHint)
}

func looksLikeUploadLoginResult(location string, body []byte) bool {
	if isUploadLoginURL(location) {
		return true
	}
	return looksLikeUploadLoginHTML(body)
}

func isUploadLoginURL(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		lower := strings.ToLower(trimmed)
		return strings.Contains(lower, "login.php") && strings.Contains(lower, "returnto=")
	}
	path := strings.ToLower(strings.TrimSpace(parsed.Path))
	if strings.HasSuffix(path, "/login.php") || path == "login.php" {
		return true
	}
	query := strings.ToLower(parsed.RawQuery)
	return strings.Contains(path, "login.php") && strings.Contains(query, "returnto=")
}

func looksLikeUploadLoginHTML(content []byte) bool {
	if len(content) == 0 {
		return false
	}
	limit := len(content)
	if limit > 4096 {
		limit = 4096
	}
	sample := strings.ToLower(string(content[:limit]))
	trimmed := strings.TrimSpace(sample)
	if !strings.HasPrefix(trimmed, "<!doctype") && !strings.HasPrefix(trimmed, "<html") {
		return false
	}
	if strings.Contains(sample, "login.php") && strings.Contains(sample, "returnto=") {
		return true
	}
	if strings.Contains(sample, "name=\"username\"") && strings.Contains(sample, "name=\"password\"") {
		return true
	}
	return strings.Contains(sample, "action=\"login.php\"")
}

func isValidPublishedDetailURL(raw string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return false
	}
	if isUploadLoginURL(trimmed) {
		return false
	}
	return strings.Contains(trimmed, "details.php?") ||
		strings.Contains(trimmed, "offers.php?") ||
		strings.Contains(trimmed, "/torrent/") ||
		strings.Contains(trimmed, "/torrent/info/") ||
		isTTGDownloadURL(trimmed)
}

func extractUploadHTMLTitle(text string) string {
	match := reHTMLTitle.FindStringSubmatch(text)
	if len(match) < 2 {
		return ""
	}
	return truncateLogText(strings.Join(strings.Fields(strings.TrimSpace(match[1])), " "), 120)
}

func truncateLogText(text string, maxLen int) string {
	if maxLen <= 0 || len(text) <= maxLen {
		return text
	}
	if maxLen <= 3 {
		return text[:maxLen]
	}
	return text[:maxLen-3] + "..."
}

func hasExistingFlagInLocation(location string) bool {
	trimmed := strings.TrimSpace(location)
	if trimmed == "" {
		return false
	}

	// 兼容部分站点返回非标准 URL 的场景。
	if strings.Contains(trimmed, "existed=1") || strings.Contains(trimmed, "exist=1") {
		return true
	}

	parsed, err := neturl.Parse(trimmed)
	if err != nil {
		return false
	}
	return queryContainsOne(parsed.Query(), "existed") || queryContainsOne(parsed.Query(), "exist")
}

func queryContainsOne(values neturl.Values, key string) bool {
	for _, value := range values[key] {
		if strings.TrimSpace(value) == "1" {
			return true
		}
	}
	return false
}
