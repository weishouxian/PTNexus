package sites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
	"gopkg.in/yaml.v3"
)

// 好学（HXPT，www.hxpt.org）发种实现的默认端点。
// 站点 2026-11 起已从 NexusPHP 迁到 Next.js 前端 + JSON API，发种不再是表单提交：
//
//	POST /api/v1/torrents（multipart），必须带 Cookie + 请求头 x-csrf-token（值取自 flux_csrf cookie）。
//
// 缺 x-csrf-token 时站点直接 403 {"code":2003,"message":"无权限"}，与 Cookie 是否有效无关，
// 这一点很容易被误判成登录态失效，日志里要区分开。
const (
	hxptDefaultBase            = "https://www.hxpt.org"
	hxptDefaultUploadPath      = "/api/v1/torrents"
	hxptDefaultProfilePath     = "/api/v1/site-profile"
	hxptDefaultSectionDictPath = "/api/v1/section-dict"
	hxptDefaultTagsDictPath    = "/api/v1/tags-dict"
	hxptDefaultDetailPath      = "/torrent/{id}"
	// 站点只允许凭会话 Cookie 下载 .torrent（待审核的种子本人也能下），没有 NexusPHP 的 download.php。
	hxptDefaultDownloadPath = "/api/v1/torrents/{id}/download"
	hxptDefaultCSRFCookie   = "flux_csrf"
	hxptDefaultCSRFHeader   = "x-csrf-token"
	hxptDefaultUserAgent    = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

	hxptRequestTimeout = 120 * time.Second
	hxptResponseLimit  = 4 << 20
)

// hxptConfig 对应 server/configs/hxpt.yaml 的 api 与 mappings 两段。
// ⚠️ mappings 的值是「站点字典名称」而不是字典 id：站点字典 id 会随运维改动漂移，
// 发布时按名称去 section-dict 里解析 id，改字典不用改配置。
type hxptConfig struct {
	SiteName string                       `yaml:"site_name"`
	API      hxptAPIConfig                `yaml:"api"`
	Mappings map[string]map[string]string `yaml:"mappings"`
	// InfoSourceTag 非空时会在上传前把 source=该值 写进种子 info 字典，
	// 让好学种子与源站种子拥有不同的 infohash（否则下载器里已有源站同 hash 任务，加种会被判重复）。
	InfoSourceTag string `yaml:"info_source_tag"`
}

// hxptAPIConfig 定义接口地址与鉴权相关配置。
type hxptAPIConfig struct {
	Base            string `yaml:"base"`
	UploadPath      string `yaml:"upload_path"`
	ProfilePath     string `yaml:"profile_path"`
	SectionDictPath string `yaml:"section_dict_path"`
	TagsDictPath    string `yaml:"tags_dict_path"`
	DetailPath      string `yaml:"detail_path"`
	DownloadPath    string `yaml:"download_path"`
	CSRFCookie      string `yaml:"csrf_cookie"`
	CSRFHeader      string `yaml:"csrf_header"`
	UserAgent       string `yaml:"user_agent"`
}

// hxptEnvelope 是站点统一的 JSON 外壳：code=0 表示成功，非 0 时 message 是给人看的原因。
type hxptEnvelope struct {
	Code      int             `json:"code"`
	Message   string          `json:"message"`
	Data      json.RawMessage `json:"data"`
	RequestID string          `json:"request_id"`
}

// hxptSession 保存一次发布的连接参数，避免每个辅助函数都重复传 cookie/csrf。
type hxptSession struct {
	client     *http.Client
	base       string
	cookie     string
	csrf       string
	csrfHeader string
	ua         string
}

// PublishHXPT 将种子发布到好学（HXPT，www.hxpt.org）。
// 参数/返回：input 提供站点信息、Cookie、种子路径与统一发布字段；返回详情页 URL 与过程日志。
// 失败场景：缺少 Cookie / Cookie 里没有 flux_csrf / 类型没有对应分类时返回 *publisher.PreCheckError；
// 站点返回 401/403、参数校验失败、种子无效时返回 error。
// 副作用：读取本地种子文件并向站点发起上传请求；成功后站点上新增一条种子（可能进入审核队列）。
func PublishHXPT(input publisher.PublishInput) (publisher.PublishResult, error) {
	cfg := loadHXPTConfig()
	base := normalizeBaseURL(firstNonEmpty(input.BaseURL, cfg.API.Base, hxptDefaultBase))
	logs := []string{fmt.Sprintf("--- [好学] JSON API 发种：POST %s%s ---", base, cfg.API.UploadPath)}
	buildLog := func() string { return strings.Join(logs, "\n") }

	cookie := strings.TrimSpace(input.Cookie)
	if cookie == "" {
		return publisher.PublishResult{}, fmt.Errorf("好学发种缺少 Cookie：请粘贴登录后浏览器里的完整 Cookie（必须包含 %s）", cfg.API.CSRFCookie)
	}
	csrf := hxptCookieValue(cookie, cfg.API.CSRFCookie)
	if csrf == "" {
		// 站点把「没有 csrf 头」和「Cookie 失效」都回 403/2003，这里提前拦住并说清缺哪一项，
		// 否则用户会一直去重登而问题其实在复制的 Cookie 不完整。
		return publisher.PublishResult{}, publisher.NewPreCheckError(fmt.Sprintf(
			"好学 Cookie 里缺少 %s：上传接口必须带 %s 请求头（缺了站点固定回 403 无权限）。请重新登录后复制完整 Cookie",
			cfg.API.CSRFCookie, cfg.API.CSRFHeader))
	}

	std := hxptStandardized(input.UploadData)
	typeKey := strings.TrimSpace(toStringAny(std["type"], ""))
	categoryName := hxptMapValue(cfg, "type", typeKey)
	if categoryName == "" {
		actual := typeKey
		if actual == "" {
			actual = "（未识别）"
		}
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, publisher.NewPreCheckError(fmt.Sprintf(
			"好学没有与资源类型对应的分类：标准类型=%s。可发布分类为电影/电视剧/综艺/纪录片/动漫/体育/音乐，请先在发布参数里确认类型", actual))
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return publisher.PublishResult{}, fmt.Errorf("好学发种缺少标题")
	}
	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return publisher.PublishResult{}, fmt.Errorf("好学发种缺少种子文件路径")
	}
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("读取种子文件失败: %w", err)
	}

	// 站点会把上传的 .torrent 原样保存、只替换 announce（写入本站 passkey），
	// 因此不隔离 infohash 时，好学种子与源站种子在下载器里是同一个任务：
	// 源站任务还在，添加就会「任务已存在」失败（表现为删掉源站任务才加得上）。
	// info 里写入 source 后 infohash 改变，pieces/files 原样不动，下载器指向同一批文件即可直接校验做种。
	if rewritten, note := isolateInfoHashByTag(torrentBytes, cfg.InfoSourceTag); note != "" {
		torrentBytes = rewritten
		logs = append(logs, note)
	}

	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		logs = append(logs, "--- [好学] 测试模式：跳过实际发种 ---")
		return publisher.PublishResult{
			PublishURL:       strings.TrimRight(base, "/") + strings.ReplaceAll(cfg.API.DetailPath, "{id}", "999999999"),
			AttemptDetailLog: buildLog(),
		}, nil
	}

	session := hxptSession{
		client:     &http.Client{Timeout: hxptRequestTimeout},
		base:       base,
		cookie:     cookie,
		csrf:       csrf,
		csrfHeader: firstNonEmpty(cfg.API.CSRFHeader, hxptDefaultCSRFHeader),
		ua:         firstNonEmpty(cfg.API.UserAgent, hxptDefaultUserAgent),
	}

	// 步骤 1：分类名称 → category_id（站点分类表由 site-profile 下发，不写死在配置里）。
	profile, err := session.getJSON(firstNonEmpty(cfg.API.ProfilePath, hxptDefaultProfilePath))
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, fmt.Errorf("读取好学站点分类失败: %w", err)
	}
	categoryID := hxptDictIDByName(profile["categories"], categoryName)
	if categoryID <= 0 {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, fmt.Errorf(
			"好学分类表里找不到「%s」：站点分类可能已调整，请检查 configs/hxpt.yaml 的 mappings.type", categoryName)
	}
	logs = append(logs, fmt.Sprintf("分类: %s (category_id=%d)", categoryName, categoryID))

	// 步骤 2：拉该分类的字段字典，把配置里的名称解析成字典 id。
	// 分类字典读不到时降级为「只发必填字段」，不因为可选字段失败而挡住整次发种。
	sections := map[string]any{}
	dict, err := session.getJSON(fmt.Sprintf("%s?category_id=%d", firstNonEmpty(cfg.API.SectionDictPath, hxptDefaultSectionDictPath), categoryID))
	if err != nil {
		logs = append(logs, fmt.Sprintf("⚠️ 读取分区字典失败，跳过可选字段: %v", err))
	} else {
		mediaName := hxptMapValue(cfg, "media_by_type", typeKey)
		codecName := hxptMapValue(cfg, "video_codec", toStringAny(std["video_codec"], ""))
		sourceName := hxptMapValue(cfg, "source", toStringAny(std["medium"], ""))
		teamName := hxptMapValue(cfg, "team", toStringAny(std["team"], ""))
		hxptSetSection(sections, dict, "media", mediaName)
		hxptSetSection(sections, dict, "codec", codecName)
		hxptSetSection(sections, dict, "source", sourceName)
		hxptSetSection(sections, dict, "team", teamName)
		logs = append(logs, fmt.Sprintf("分区字段: media=%s codec=%s source=%s team=%s",
			hxptDescribeSection(sections, "media", mediaName),
			hxptDescribeSection(sections, "codec", codecName),
			hxptDescribeSection(sections, "source", sourceName),
			hxptDescribeSection(sections, "team", teamName)))
	}

	// 步骤 3：标签名称 → 标签 id（站点只有固定 9 个标签，未命中的标准标签直接丢弃）。
	tagIDs := []int{}
	if tagsDict, tagErr := session.getJSON(firstNonEmpty(cfg.API.TagsDictPath, hxptDefaultTagsDictPath)); tagErr != nil {
		logs = append(logs, fmt.Sprintf("⚠️ 读取标签字典失败，本次不带标签: %v", tagErr))
	} else {
		tagIDs = hxptResolveTagIDs(cfg, tagsDict, input.UploadData)
		if len(tagIDs) > 0 {
			logs = append(logs, fmt.Sprintf("标签: %v", tagIDs))
		}
	}

	// 步骤 4：组装 multipart 并提交。
	body, contentType, err := hxptBuildUploadForm(hxptUploadFormInput{
		Config:       cfg,
		Title:        title,
		Subtitle:     strings.TrimSpace(input.Subtitle),
		Description:  input.Description,
		MediaInfo:    input.MediaInfo,
		IMDbLink:     strings.TrimSpace(input.IMDbLink),
		DoubanLink:   strings.TrimSpace(input.DoubanLink),
		TMDbLink:     strings.TrimSpace(firstUploadDataString(input.UploadData, []string{"tmdb_link", "tmdbLink", "tmdb"})),
		Poster:       colaptPoster(input.UploadData),
		CategoryID:   int(categoryID),
		Sections:     sections,
		TagIDs:       tagIDs,
		Anonymous:    publisher.ResolveAnonymousUploadEnabled(input.RootConfig),
		TorrentBytes: torrentBytes,
		TorrentName:  filepath.Base(torrentPath),
	})
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, err
	}

	envelope, status, err := session.postMultipart(firstNonEmpty(cfg.API.UploadPath, hxptDefaultUploadPath), body, contentType)
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, err
	}
	logs = append(logs, fmt.Sprintf("响应状态: %d code=%d request_id=%s", status, envelope.Code, envelope.RequestID))
	if err := hxptUploadError(status, envelope); err != nil {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, err
	}

	torrentID := hxptResponseTorrentID(envelope.Data)
	if torrentID <= 0 {
		return publisher.PublishResult{AttemptDetailLog: buildLog()}, fmt.Errorf(
			"好学未返回种子 ID：%s", summarizeResponseBody(string(envelope.Data)))
	}
	publishURL := strings.TrimRight(base, "/") + strings.ReplaceAll(
		firstNonEmpty(cfg.API.DetailPath, hxptDefaultDetailPath), "{id}", strconv.FormatInt(torrentID, 10))
	logs = append(logs, fmt.Sprintf("发布成功: id=%d", torrentID))
	if hint := hxptGroupSuggestion(envelope.Data); hint != "" {
		logs = append(logs, "站点提示: "+hint)
	}
	logs = append(logs, "⚠️ 该站上传后通常进入审核，详情页可访问不代表已上架")
	logs = append(logs, "详情页: "+publishURL)

	// 直链：/api/v1/torrents/{id}/download（会话 Cookie 鉴权）。
	// 发布后「自动添加到下载器」用的是这个地址，缺了它就只能退回详情页去猜下载链接（SPA 页里没有）。
	downloadURL := strings.TrimRight(base, "/") + strings.ReplaceAll(
		firstNonEmpty(cfg.API.DownloadPath, hxptDefaultDownloadPath), "{id}", strconv.FormatInt(torrentID, 10))

	return publisher.PublishResult{
		PublishURL:        publishURL,
		DirectDownloadURL: downloadURL,
		AttemptDetailLog:  buildLog(),
	}, nil
}

// hxptUploadFormInput 汇总一次上传的字段，避免 build 函数参数列表过长。
type hxptUploadFormInput struct {
	Config       hxptConfig
	Title        string
	Subtitle     string
	Description  string
	MediaInfo    string
	IMDbLink     string
	DoubanLink   string
	TMDbLink     string
	Poster       string
	CategoryID   int
	Sections     map[string]any
	TagIDs       []int
	Anonymous    bool
	TorrentBytes []byte
	TorrentName  string
}

// hxptBuildUploadForm 组装 multipart 请求体，字段与站点上传页一次提交的字段完全一致。
// 参数/返回：入参见结构体定义；返回请求体字节、Content-Type 与错误。
// 失败场景：multipart 构造失败时返回 error。
// 副作用：分配内存缓冲区。
func hxptBuildUploadForm(input hxptUploadFormInput) ([]byte, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	part, err := writer.CreateFormFile("file", input.TorrentName)
	if err != nil {
		return nil, "", fmt.Errorf("构造种子文件字段失败: %w", err)
	}
	if _, err := part.Write(input.TorrentBytes); err != nil {
		return nil, "", fmt.Errorf("写入种子内容失败: %w", err)
	}

	fields := [][2]string{
		{"category_id", strconv.Itoa(input.CategoryID)},
		{"anonymous", strconv.FormatBool(input.Anonymous)},
		{"name", input.Title},
		{"small_descr", input.Subtitle},
		{"descr", input.Description},
		{"imdb", input.IMDbLink},
		{"douban", input.DoubanLink},
		{"tmdb", input.TMDbLink},
		{"poster", input.Poster},
		{"mediainfo", input.MediaInfo},
	}
	for _, field := range fields {
		// 与站点前端一致：空值不提交，避免把空串当有效值覆盖站点默认。
		if strings.TrimSpace(field[1]) == "" {
			continue
		}
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return nil, "", fmt.Errorf("写入字段 %s 失败: %w", field[0], err)
		}
	}
	if len(input.Sections) > 0 {
		encoded, err := json.Marshal(input.Sections)
		if err != nil {
			return nil, "", fmt.Errorf("序列化 sections 失败: %w", err)
		}
		if err := writer.WriteField("sections", string(encoded)); err != nil {
			return nil, "", fmt.Errorf("写入 sections 失败: %w", err)
		}
	}
	if len(input.TagIDs) > 0 {
		encoded, err := json.Marshal(input.TagIDs)
		if err != nil {
			return nil, "", fmt.Errorf("序列化 tags 失败: %w", err)
		}
		if err := writer.WriteField("tags", string(encoded)); err != nil {
			return nil, "", fmt.Errorf("写入 tags 失败: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("封装上传请求失败: %w", err)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

// getJSON 发起带会话的 GET 请求并返回 data 段。
// 失败场景：网络错误、HTTP 4xx/5xx、code!=0 时返回 error。
func (s hxptSession) getJSON(path string) (map[string]any, error) {
	endpoint := path
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		endpoint = strings.TrimRight(s.base, "/") + path
	}
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	s.applyHeaders(req)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, hxptResponseLimit))

	envelope := hxptEnvelope{}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("HTTP %d 响应不是合法 JSON: %s", resp.StatusCode, summarizeResponseBody(string(body)))
	}
	if err := hxptUploadError(resp.StatusCode, envelope); err != nil {
		return nil, err
	}
	data := map[string]any{}
	if len(envelope.Data) > 0 {
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			return nil, fmt.Errorf("解析响应 data 失败: %w", err)
		}
	}
	return data, nil
}

// postMultipart 提交 multipart 上传请求，返回解析后的 JSON 外壳与状态码。
// 失败场景：网络错误或响应不是合法 JSON 时返回 error（业务错误由调用方按 code 处理）。
func (s hxptSession) postMultipart(path string, body []byte, contentType string) (hxptEnvelope, int, error) {
	endpoint := strings.TrimRight(s.base, "/") + path
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return hxptEnvelope{}, 0, err
	}
	s.applyHeaders(req)
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return hxptEnvelope{}, 0, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, hxptResponseLimit))

	envelope := hxptEnvelope{}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return hxptEnvelope{}, resp.StatusCode, fmt.Errorf(
			"HTTP %d 响应不是合法 JSON: %s", resp.StatusCode, summarizeResponseBody(string(respBody)))
	}
	return envelope, resp.StatusCode, nil
}

// applyHeaders 补齐会话 Cookie、CSRF 头与同源头，模拟站点前端的一次 API 调用。
func (s hxptSession) applyHeaders(req *http.Request) {
	header := strings.TrimSpace(s.csrfHeader)
	if header == "" {
		header = hxptDefaultCSRFHeader
	}
	req.Header.Set("User-Agent", s.ua)
	req.Header.Set("Cookie", s.cookie)
	req.Header.Set(header, s.csrf)
	req.Header.Set("Accept-Language", "zh-CN")
	if origin := hxptOrigin(s.base); origin != "" {
		req.Header.Set("Origin", origin)
		req.Header.Set("Referer", origin+"/upload")
	}
}

// hxptUploadError 把站点 code 与 HTTP 状态码翻译成可执行的错误提示。
// 参数/返回：status 为 HTTP 状态码，envelope 为响应外壳；code=0 且状态正常时返回 nil。
// 失败场景：无。
// 副作用：无。
func hxptUploadError(status int, envelope hxptEnvelope) error {
	// 2003 = 站点「无权限」：既可能是 Cookie 过期，也可能是缺/错 x-csrf-token。
	if status == http.StatusUnauthorized || status == http.StatusForbidden || envelope.Code == 2003 {
		return fmt.Errorf("好学拒绝了本次请求（HTTP %d code=%d）：Cookie 可能已失效，或 Cookie 里缺少有效的 %s",
			status, envelope.Code, hxptDefaultCSRFCookie)
	}
	if envelope.Code == 0 && status < 400 {
		return nil
	}
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = "站点未返回原因"
	}
	// 1002 = 参数校验失败：data 是 [{field,error}] 数组，拼进日志才看得出是哪个字段。
	if envelope.Code == 1002 {
		if detail := hxptFieldErrors(envelope.Data); detail != "" {
			return fmt.Errorf("好学参数校验失败（HTTP %d）：%s（%s）", status, message, detail)
		}
	}
	return fmt.Errorf("好学返回失败（HTTP %d code=%d）：%s", status, envelope.Code, message)
}

// hxptFieldErrors 把 1002 的字段错误数组拼成 "字段: 原因" 形式。
func hxptFieldErrors(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	items := []map[string]any{}
	if err := json.Unmarshal(raw, &items); err != nil {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		field := strings.TrimSpace(toStringAny(item["field"], ""))
		reason := strings.TrimSpace(toStringAny(item["error"], ""))
		if field == "" && reason == "" {
			continue
		}
		parts = append(parts, field+"="+reason)
	}
	return strings.Join(parts, "; ")
}

// hxptResponseTorrentID 从 data 里取新种子 id（站点主字段为 id，兼容 torrent_id）。
func hxptResponseTorrentID(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	item := map[string]any{}
	if err := json.Unmarshal(raw, &item); err != nil {
		return 0
	}
	for _, key := range []string{"id", "torrent_id"} {
		if parsed := int64FromAny(item[key], 0); parsed > 0 {
			return parsed
		}
	}
	return 0
}

// hxptGroupSuggestion 提取站点对「同作品已有档位」的提示（存在时不视为失败，只写日志）。
func hxptGroupSuggestion(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	item := map[string]any{}
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	suggest, ok := item["group_suggest"].(map[string]any)
	if !ok || suggest == nil {
		return ""
	}
	name := strings.TrimSpace(toStringAny(suggest["name"], ""))
	if name == "" {
		return ""
	}
	id := int64FromAny(suggest["torrent_id"], 0)
	return fmt.Sprintf("同作品已有种子 id=%d name=%s", id, name)
}

// hxptStandardized 读取标准化参数字典（缺省返回空 map，便于直接索引）。
func hxptStandardized(uploadData map[string]any) map[string]any {
	std := map[string]any{}
	if uploadData == nil {
		return std
	}
	if raw, ok := uploadData["standardized_params"].(map[string]any); ok && raw != nil {
		for key, value := range raw {
			std[key] = value
		}
	}
	return std
}

// hxptMapValue 按「维度 + 标准键」查配置映射，未命中时回退该维度的 default。
// default 配成空串表示「不允许兜底」，调用方据此拒绝发布。
func hxptMapValue(cfg hxptConfig, dimension, key string) string {
	mapping := cfg.Mappings[dimension]
	if mapping == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(key); trimmed != "" {
		if value := strings.TrimSpace(mapping[trimmed]); value != "" {
			return value
		}
		// 标准键可能带大小写差异（如 tag.DIY / tag.diy），做一次忽略大小写的兜底匹配。
		for candidate, value := range mapping {
			if strings.EqualFold(candidate, trimmed) && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return strings.TrimSpace(mapping["default"])
}

// hxptCookieValue 从 Cookie 串里取指定键的值（站点把 CSRF token 放在 flux_csrf 里）。
func hxptCookieValue(cookie, name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return ""
	}
	for _, segment := range strings.Split(cookie, ";") {
		key, value, found := strings.Cut(strings.TrimSpace(segment), "=")
		if !found || !strings.EqualFold(strings.TrimSpace(key), trimmed) {
			continue
		}
		decoded := strings.TrimSpace(value)
		if unescaped, err := url.QueryUnescape(decoded); err == nil {
			decoded = unescaped
		}
		return strings.TrimSpace(decoded)
	}
	return ""
}

// hxptOrigin 返回站点同源地址（写请求带 Origin/Referer，与浏览器行为一致）。
func hxptOrigin(base string) string {
	parsed, err := url.Parse(strings.TrimSpace(base))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// hxptDictIDByName 在 [{id,name}] 形态的字典里按名称取 id（忽略大小写与首尾空格）。
func hxptDictIDByName(raw any, name string) int64 {
	items, ok := raw.([]any)
	if !ok {
		return 0
	}
	target := strings.ToLower(strings.TrimSpace(name))
	if target == "" {
		return 0
	}
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.ToLower(strings.TrimSpace(toStringAny(entry["name"], ""))) != target {
			continue
		}
		if id := int64FromAny(entry["id"], 0); id > 0 {
			return id
		}
	}
	return 0
}

// hxptSetSection 把「配置里的字典名称」解析成字典 id 写进 sections。
// 站点该分类没有这个 kind、或名称在字典里不存在时静默跳过（这些字段在站点侧都非必填）。
func hxptSetSection(sections map[string]any, dict map[string]any, kind, name string) {
	if sections == nil || strings.TrimSpace(name) == "" {
		return
	}
	options, exists := dict[kind]
	if !exists {
		return
	}
	if id := hxptDictIDByName(options, name); id > 0 {
		sections[kind] = id
	}
}

// hxptDescribeSection 生成日志里「名称→id」的可读描述。
func hxptDescribeSection(sections map[string]any, kind, name string) string {
	if strings.TrimSpace(name) == "" {
		return "-"
	}
	if id := int64FromAny(sections[kind], 0); id > 0 {
		return fmt.Sprintf("%s(%d)", name, id)
	}
	return name + "(跳过)"
}

// hxptResolveTagIDs 把标准标签解析成站点标签 id。
// 站点标签是固定清单（首发/禁转/国语/中字/DIY/官种/合集/完结/独家），未命中的标准标签直接丢弃。
func hxptResolveTagIDs(cfg hxptConfig, tagsDict map[string]any, uploadData map[string]any) []int {
	mapping := cfg.Mappings["tag"]
	if len(mapping) == 0 {
		return nil
	}
	combined := resolveSiteCombinedTags(uploadData)
	if len(combined) == 0 {
		return nil
	}

	// tags-dict: data.tags = [[id, name, kind], ...]
	nameToID := map[string]int{}
	if raw, ok := tagsDict["tags"].([]any); ok {
		for _, item := range raw {
			entry, ok := item.([]any)
			if !ok || len(entry) < 2 {
				continue
			}
			id := int(int64FromAny(entry[0], 0))
			name := strings.ToLower(strings.TrimSpace(toStringAny(entry[1], "")))
			if id > 0 && name != "" {
				nameToID[name] = id
			}
		}
	}
	if len(nameToID) == 0 {
		return nil
	}

	ids := make([]int, 0, len(combined))
	seen := map[int]struct{}{}
	for stdTag := range combined {
		// 标签在 uploadData 里可能已是标准键（tag.禁转），也可能还是站内原文（禁转），两种都试。
		siteName := strings.ToLower(strings.TrimSpace(hxptMapValue(cfg, "tag", stdTag)))
		if siteName == "" {
			siteName = strings.ToLower(strings.TrimSpace(hxptMapValue(cfg, "tag", "tag."+stdTag)))
		}
		if siteName == "" {
			continue
		}
		id, ok := nameToID[siteName]
		if !ok {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sortInts(ids)
	return ids
}

// sortInts 升序排序（标签 id 顺序不影响站点，只为日志稳定可比）。
func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// loadHXPTConfig 读取 server/configs/hxpt.yaml；文件缺失或解析失败时返回内置端点默认值。
func loadHXPTConfig() hxptConfig {
	cfg := hxptConfig{
		SiteName: "好学",
		API: hxptAPIConfig{
			Base:            hxptDefaultBase,
			UploadPath:      hxptDefaultUploadPath,
			ProfilePath:     hxptDefaultProfilePath,
			SectionDictPath: hxptDefaultSectionDictPath,
			TagsDictPath:    hxptDefaultTagsDictPath,
			DetailPath:      hxptDefaultDetailPath,
			DownloadPath:    hxptDefaultDownloadPath,
			CSRFCookie:      hxptDefaultCSRFCookie,
			CSRFHeader:      hxptDefaultCSRFHeader,
			UserAgent:       hxptDefaultUserAgent,
		},
		Mappings: map[string]map[string]string{},
	}

	paths := config.ResolveRuntimePaths()
	data, err := os.ReadFile(filepath.Join(paths.BaseDir, "configs", "hxpt.yaml"))
	if err != nil {
		return cfg
	}
	var override hxptConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return cfg
	}

	if value := strings.TrimSpace(override.SiteName); value != "" {
		cfg.SiteName = value
	}
	api := override.API
	if value := strings.TrimSpace(api.Base); value != "" {
		cfg.API.Base = value
	}
	if value := strings.TrimSpace(api.UploadPath); value != "" {
		cfg.API.UploadPath = value
	}
	if value := strings.TrimSpace(api.ProfilePath); value != "" {
		cfg.API.ProfilePath = value
	}
	if value := strings.TrimSpace(api.SectionDictPath); value != "" {
		cfg.API.SectionDictPath = value
	}
	if value := strings.TrimSpace(api.TagsDictPath); value != "" {
		cfg.API.TagsDictPath = value
	}
	if value := strings.TrimSpace(api.DetailPath); value != "" {
		cfg.API.DetailPath = value
	}
	if value := strings.TrimSpace(api.DownloadPath); value != "" {
		cfg.API.DownloadPath = value
	}
	if value := strings.TrimSpace(api.CSRFCookie); value != "" {
		cfg.API.CSRFCookie = value
	}
	if value := strings.TrimSpace(api.CSRFHeader); value != "" {
		cfg.API.CSRFHeader = value
	}
	if value := strings.TrimSpace(api.UserAgent); value != "" {
		cfg.API.UserAgent = value
	}
	if len(override.Mappings) > 0 {
		cfg.Mappings = override.Mappings
	}
	if value := strings.TrimSpace(override.InfoSourceTag); value != "" {
		cfg.InfoSourceTag = value
	}
	return cfg
}
