package sites

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/config"
	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// 肉丝（rousi.pro）2026-08 起升级为 PeerGo 平台，旧的 API v1（Bearer passkey + JSON）契约全部作废。
//
// 新契约（2026-11 实测）：
//   - 认证：只认浏览器会话 Cookie（__Host-peergo_session），站点没有 API Key 入口
//   - 写请求：必须带同源 Origin/Referer，否则 403 origin_not_allowed；还需 X-CSRF-Token
//     （GET /api/v1/session 返回 csrf_token）
//   - 提交：POST /api/v1/torrents，multipart/form-data
//     category_id / title / subtitle / description / media_info / anonymous
//     imdb_id / tmdb_id / douban_id（可选）
//     facet_selections（可重复，每次一个 JSON 文件 part：{"facet_id":..,"option_keys":[..]}）
//     screenshots（可重复，单张 ≤ 2 MiB，JPEG/PNG/WebP，至少一张）
//     torrent_file（原始私有 v1 .torrent）
//   - 成功：201 {id, info_hash_v1, state(pending_review|published), content_name, ...}
//     详情页为 /torrents/{id}（不再是 /torrent/{uuid}）
//
// facet 维度由站点按分类返回（GET /api/v1/categories/{id}/facets），其中 required 必须给值，
// 同 requirement_group 的多个 facet（source-medium / release-type）至少要有一个有值。
var (
	reRousiSourceUHDBluRayRip = regexp.MustCompile(`(?i)UHD[\s._-]*(?:BLU-?RAY|BLURAY)`)
	reRousiSourceBluRayRip    = regexp.MustCompile(`(?i)\b(?:BLU-?RAY|BLURAY|BDMV|BDRIP|BD-?RIP)\b`)
	reRousiSourceRemux        = regexp.MustCompile(`(?i)\bREMUX\b`)
	reRousiSourceFullDisc     = regexp.MustCompile(`(?i)\b(?:FULL[\s._-]*DISC|COMPLETE[\s._-]*BLU-?RAY|ISO)\b`)
	reRousiSourceWebDL        = regexp.MustCompile(`(?i)\bWEB[\s._-]*DL\b`)
	reRousiSourceWebRip       = regexp.MustCompile(`(?i)\bWEB[\s._-]*RIP\b`)
	reRousiSourceHDTV         = regexp.MustCompile(`(?i)\b(?:UHDTV|HDTV|TV[\s._-]*RIP)\b`)
	reRousiSourceDVD          = regexp.MustCompile(`(?i)\b(?:DVD[\s._-]*RIP|DVDRIP|DVD(?:5|9)?)\b`)
	reRousiSourceBroadcast    = regexp.MustCompile(`(?i)\b(?:HDTV|UHDTV|BROADCAST)\b`)
	reRousiSourceCam          = regexp.MustCompile(`(?i)\b(?:CAM|HDRIP|HDCAM|TS)\b`)

	reRousiUUID             = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
	reRousiBBCodeImage      = regexp.MustCompile(`(?is)\[img[^\]]*\].*?\[/img\]`)
	reRousiHTMLImage        = regexp.MustCompile(`(?is)<img[^>]*>`)
	reRousiMarkdownImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]+\)`)
	reRousiOnlyImageURLLine = regexp.MustCompile(`(?i)^https?://\S+\.(?:png|jpe?g|gif|webp)\S*$`)
	reRousiBBCodeQuote      = regexp.MustCompile(`(?is)\[quote\](.*?)\[/quote\]`)
	reRousiBBCodeCode       = regexp.MustCompile(`(?is)\[code\](.*?)\[/code\]`)
	reRousiMalformedURL     = regexp.MustCompile(`(?is)\[url=\[\s*https?://[^\]\s]+\\?\]`)
	reRousiBBCodeURLWithArg = regexp.MustCompile(`(?is)\[url=(.*?)\](.*?)\[/url\]`)
	reRousiBBCodeURL        = regexp.MustCompile(`(?is)\[url\](.*?)\[/url\]`)
	reRousiBBCodeColor      = regexp.MustCompile(`(?is)\[color=[^\]]+\](.*?)\[/color\]`)
	reRousiBBCodeSize       = regexp.MustCompile(`(?is)\[size=[^\]]+\](.*?)\[/size\]`)
	reRousiBBCodeBold       = regexp.MustCompile(`(?is)\[b\](.*?)\[/b\]`)
	reRousiBBCodeItalic     = regexp.MustCompile(`(?is)\[i\](.*?)\[/i\]`)
	reRousiBBCodeUnderline  = regexp.MustCompile(`(?is)\[u\](.*?)\[/u\]`)
	reRousiManyNewlines     = regexp.MustCompile(`\n{3,}`)
	reRousiBBCodeURLImg     = regexp.MustCompile(`(?is)\[url=([^\]]+)\]\s*\[img[^\]]*\](.*?)\[/img\]\s*\[/url\]`)
	reRousiBBCodeImgSource  = regexp.MustCompile(`(?is)\[img[^\]]*\](.*?)\[/img\]`)
	reRousiMDImgSource      = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
	reRousiHTMLImgSource    = regexp.MustCompile(`(?is)<img[^>]+src=["']([^"']+)["']`)
	reRousiImageExt         = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)(\?.*)?$`)
	reRousiPixhostThumb     = regexp.MustCompile(`(?i)^https?://t(\d+)\.pixhost\.(?:to|cc)/thumbs/(.+)$`)
	reRousiTorrentFilename  = regexp.MustCompile(`^([^-]+)-(\d+)-`)

	reRousiIMDbID   = regexp.MustCompile(`(?i)(tt\d{7,10})`)
	reRousiDoubanID = regexp.MustCompile(`(?i)subject/(\d{5,12})`)
	reRousiTmdbID   = regexp.MustCompile(`(?i)(?:movie|tv)/(\d{1,10})`)
)

const (
	rousiSessionCookieName = "__Host-peergo_session"
	// 站点限制单张截图 ≤ 2 MiB，这里留 8KB 余量给 multipart 边界与头部。
	rousiScreenshotMaxBytes = 2*1024*1024 - 8*1024
	rousiScreenshotMaxCount = 6
	rousiScreenshotTotalMax = 12 * 1024 * 1024
)

type rousiImageSource struct {
	URL     string
	Referer string
}

// rousiScreenshot 是待上传的截图文件内容。
type rousiScreenshot struct {
	Name        string
	ContentType string
	Data        []byte
}

// rousiFacetOption 是站点 facet 选项（key 为提交值，label 为展示值）。
type rousiFacetOption struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// rousiFacet 是站点按分类返回的填表维度定义。
type rousiFacet struct {
	ID               string             `json:"id"`
	Name             string             `json:"name"`
	Required         bool               `json:"required"`
	SelectionMode    string             `json:"selection_mode"`
	RequirementGroup string             `json:"requirement_group"`
	Options          []rousiFacetOption `json:"options"`
}

// rousiFacetSelection 是提交给站点的 facet_selections 分片内容。
type rousiFacetSelection struct {
	FacetID    string   `json:"facet_id"`
	OptionKeys []string `json:"option_keys"`
}

// rousiSubmitResult 是 POST /api/v1/torrents 成功（201）的响应体。
type rousiSubmitResult struct {
	ID             int64  `json:"id"`
	InfoHashV1     string `json:"info_hash_v1"`
	State          string `json:"state"`
	ContentName    string `json:"content_name"`
	TotalSizeBytes int64  `json:"total_size_bytes"`
	FileCount      int    `json:"file_count"`
	SubmittedAt    string `json:"submitted_at"`
}

// PublishRousi 执行肉丝站点（PeerGo 平台）发布流程。
// 参数/返回：input 提供站点信息、发布数据与通用字段；返回发布详情页 URL、是否疑似“种子已存在”，以及发布过程日志。
// 失败场景：Cookie 缺失/失效、CSRF 获取失败、必填 facet 缺失、截图缺失、上传失败等返回 error。
// 副作用：读取本地种子文件与截图、向目标站点发起多次请求（session / facets / 上传）。
func PublishRousi(input publisher.PublishInput) (publisher.PublishResult, error) {
	targetName := strings.TrimSpace(input.TargetName)
	if targetName == "" {
		targetName = "目标站点"
	}

	logLines := []string{
		"检测到肉丝站点：启用 PeerGo 发布流程（会话 Cookie + CSRF + multipart）",
	}
	appendLog := func(text string) {
		trimmed := strings.TrimSpace(text)
		if trimmed == "" {
			return
		}
		logLines = append(logLines, trimmed)
	}
	buildDetail := func() string { return strings.Join(logLines, "\n") }

	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	if baseURL == "" {
		err := fmt.Errorf("目标站点缺少 base_url")
		appendLog(fmt.Sprintf("参数校验失败: %v", err))
		return publisher.PublishResult{AttemptDetailLog: buildDetail()}, err
	}

	// 对齐 Python：UPLOAD_TEST_MODE=true 时跳过真实发布，返回模拟的详情页链接。
	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		appendLog("测试模式：跳过实际发布，模拟成功响应")
		return publisher.PublishResult{
			PublishURL:       baseURL + "/torrents/99999999",
			AttemptDetailLog: buildDetail(),
		}, nil
	}

	torrentPath := strings.TrimSpace(input.TorrentPath)
	torrentFile, err := os.ReadFile(torrentPath)
	if err != nil {
		wrappedErr := fmt.Errorf("读取种子文件失败: %w", err)
		appendLog(fmt.Sprintf("读取种子失败: %v", wrappedErr))
		return publisher.PublishResult{AttemptDetailLog: buildDetail()}, wrappedErr
	}

	publishURL, existing, attemptDetail, attemptErr := tryUploadTorrentRousiPeerGo(
		baseURL,
		targetName,
		torrentPath,
		input.TargetInfo,
		input.UploadData,
		torrentFile,
		strings.TrimSpace(input.Cookie),
		strings.TrimSpace(input.Title),
		strings.TrimSpace(input.Subtitle),
		strings.TrimSpace(input.Description),
		strings.TrimSpace(input.MediaInfo),
		strings.TrimSpace(input.IMDbLink),
		strings.TrimSpace(input.DoubanLink),
		input.RootConfig,
	)
	appendLog(attemptDetail)
	if attemptErr != nil {
		return publisher.PublishResult{
			IsExistingTorrent: existing,
			AttemptDetailLog:  buildDetail(),
		}, attemptErr
	}

	return publisher.PublishResult{
		PublishURL:        publishURL,
		IsExistingTorrent: existing,
		AttemptDetailLog:  buildDetail(),
	}, nil
}

// isRousiSite 判断站点 code 是否为肉丝。
func isRousiSite(siteCode string) bool {
	return strings.EqualFold(strings.TrimSpace(siteCode), "rousi")
}

// rousiResolveCookieHeader 把站点 passkey 归一成 Cookie 请求头。
// 说明：肉丝已经取消 API Key，站点配置里建议直接粘贴浏览器 Cookie 值；
// 兼容两种写法：完整 Cookie 串（含 `=` 或 `;`）或只填 __Host-peergo_session 的值。
func rousiResolveCookieHeader(passkey string) string {
	trimmed := strings.TrimSpace(passkey)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	trimmed = regexp.MustCompile(`(?i)^cookie:\s*`).ReplaceAllString(trimmed, "")
	if strings.Contains(lower, "peergo_session=") {
		return trimmed
	}
	if strings.Contains(trimmed, "=") || strings.Contains(trimmed, ";") {
		return trimmed
	}
	return rousiSessionCookieName + "=" + trimmed
}

// rousiFetchSession 读取当前会话，返回 CSRF token 与用户名。
// 失败场景：Cookie 缺失/过期时站点返回 401，这里给出可读错误。
func rousiFetchSession(client *http.Client, baseURL string, cookie string, userAgent string) (string, string, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/session", nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", baseURL+"/upload")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求会话接口失败: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", "", fmt.Errorf("会话 Cookie 无效或已过期（HTTP %d），请到浏览器重新复制 %s", resp.StatusCode, rousiSessionCookieName)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Errorf("会话接口返回 HTTP %d: %s", resp.StatusCode, summarizeRousiResponseBody(string(body)))
	}

	payload := map[string]any{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", "", fmt.Errorf("会话接口返回非 JSON: %s", summarizeRousiResponseBody(string(body)))
	}
	csrf := strings.TrimSpace(toStringLoose(payload["csrf_token"]))
	if csrf == "" {
		return "", "", fmt.Errorf("会话接口未返回 csrf_token")
	}
	username := ""
	if user, ok := payload["user"].(map[string]any); ok {
		username = strings.TrimSpace(toStringLoose(user["username"]))
	}
	return csrf, username, nil
}

// rousiFetchFacets 读取指定分类的填表维度定义。
func rousiFetchFacets(client *http.Client, baseURL string, cookie string, userAgent string, categoryID string) ([]rousiFacet, error) {
	endpoint := baseURL + "/api/v1/categories/" + neturl.PathEscape(categoryID) + "/facets"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", baseURL+"/upload")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, summarizeRousiResponseBody(string(body)))
	}
	facets := []rousiFacet{}
	if err := json.Unmarshal(body, &facets); err != nil {
		return nil, fmt.Errorf("响应非 JSON: %s", summarizeRousiResponseBody(string(body)))
	}
	return facets, nil
}

// tryUploadTorrentRousiPeerGo 走 PeerGo 契约完成一次发种。
// 参数/返回：见 PublishRousi；返回详情页 URL、是否“已存在”、过程日志与错误。
func tryUploadTorrentRousiPeerGo(
	baseURL string,
	targetName string,
	torrentPath string,
	targetInfo map[string]any,
	uploadData map[string]any,
	torrentFile []byte,
	siteCookie string,
	title string,
	subtitle string,
	description string,
	mediaInfo string,
	imdbLink string,
	doubanLink string,
	rootConfig map[string]any,
) (string, bool, string, error) {
	detailLines := []string{
		fmt.Sprintf("请求地址: %s/api/v1/torrents", baseURL),
		"上传方式: PeerGo multipart（Cookie 会话 + X-CSRF-Token）",
	}
	buildDetail := func() string { return strings.Join(detailLines, "\n") }

	cookie := rousiResolveCookieHeader(firstNonEmpty(strings.TrimSpace(siteCookie), toStringAny(targetInfo["passkey"], "")))
	if cookie == "" {
		err := fmt.Errorf("目标站点缺少 Cookie（请同步浏览器 Cookie，或在站点配置的 passkey 里填入 %s）", rousiSessionCookieName)
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", false, buildDetail(), err
	}

	userAgent := strings.TrimSpace(toStringAny(targetInfo["user_agent"], ""))
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}

	client := &http.Client{Timeout: 180 * time.Second}

	csrf, username, err := rousiFetchSession(client, baseURL, cookie, userAgent)
	if err != nil {
		detailLines = append(detailLines, fmt.Sprintf("会话校验失败: %v", err))
		return "", false, buildDetail(), err
	}
	if username != "" {
		detailLines = append(detailLines, fmt.Sprintf("会话有效: 用户=%s", username))
	}

	siteCode := strings.TrimSpace(toStringAny(targetInfo["site"], "rousi"))
	siteCfg, _ := publishmapping.LoadSitePublishConfig(siteCode)

	if uploadData == nil {
		uploadData = map[string]any{}
	}
	standardized := mapStringAny(uploadData["standardized_params"])

	categoryID := resolveRousiCategoryID(uploadData, standardized, siteCfg)
	detailLines = append(detailLines, fmt.Sprintf("分类: category_id=%s", categoryID))

	facets, facetsErr := rousiFetchFacets(client, baseURL, cookie, userAgent, categoryID)
	if facetsErr != nil {
		detailLines = append(detailLines, fmt.Sprintf("分类维度读取失败（降级为本地映射）: %v", facetsErr))
		facets = nil
	}

	selections, selectionSummary, selErr := buildRousiFacetSelections(categoryID, facets, uploadData, standardized, siteCfg, title)
	if selectionSummary != "" {
		detailLines = append(detailLines, selectionSummary)
	}
	if selErr != nil {
		detailLines = append(detailLines, fmt.Sprintf("维度校验失败: %v", selErr))
		return "", false, buildDetail(), selErr
	}

	resolvedSubtitle := strings.TrimSpace(subtitle)
	if resolvedSubtitle == "" {
		resolvedSubtitle = title
	}
	if resolvedSubtitle == "" {
		err := fmt.Errorf("标题为空")
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", false, buildDetail(), err
	}

	resolvedDescription := strings.TrimSpace(buildRousiDescriptionMarkdown(uploadData, description))
	resolvedMediaInfo := firstNonEmpty(
		strings.TrimSpace(mediaInfo),
		strings.TrimSpace(toStringLoose(uploadData["mediaInfo"])),
		strings.TrimSpace(toStringLoose(uploadData["media_info"])),
		strings.TrimSpace(toStringLoose(uploadData["mediainfo"])),
		strings.TrimSpace(toStringLoose(uploadData["mediainfo_text"])),
		strings.TrimSpace(toStringLoose(uploadData["mediainfo_str"])),
	)

	uploadTargetName := strings.TrimSpace(targetName)
	if uploadTargetName == "" {
		uploadTargetName = strings.TrimSpace(toStringAny(targetInfo["nickname"], siteCode))
	}

	screenshots, cleanup, shotSummary, shotErr := collectRousiScreenshots(baseURL, cookie, userAgent, torrentPath, uploadData)
	if shotSummary != "" {
		detailLines = append(detailLines, shotSummary)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if shotErr != nil {
		detailLines = append(detailLines, fmt.Sprintf("截图处理失败: %v", shotErr))
	}
	if len(screenshots) == 0 {
		err := fmt.Errorf("站点要求至少上传一张截图，但本地没有可用截图（需 JPEG/PNG/WebP，单张 ≤ 2 MiB）")
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", false, buildDetail(), err
	}

	anonymous := publisher.ResolveAnonymousUploadEnabled(rootConfig)
	if value, exists := uploadData["anonymous"]; exists {
		anonymous = boolFromAny(value)
	}

	ids := resolveRousiExternalIDs(uploadData, standardized, imdbLink, doubanLink)
	if len(ids) > 0 {
		parts := make([]string, 0, len(ids))
		for key, value := range ids {
			parts = append(parts, key+"="+value)
		}
		detailLines = append(detailLines, fmt.Sprintf("外链 ID: %s", strings.Join(parts, ", ")))
	}

	body, contentType, err := buildRousiMultipartBody(rousiMultipartInput{
		CategoryID:   categoryID,
		Title:        title,
		Subtitle:     resolvedSubtitle,
		Description:  resolvedDescription,
		MediaInfo:    resolvedMediaInfo,
		Anonymous:    anonymous,
		ExternalIDs:  ids,
		Selections:   selections,
		Screenshots:  screenshots,
		TorrentName:  filepath.Base(torrentPath),
		TorrentBytes: torrentFile,
	})
	if err != nil {
		detailLines = append(detailLines, fmt.Sprintf("构建上传参数失败: %v", err))
		return "", false, buildDetail(), err
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/v1/torrents", body)
	if err != nil {
		detailLines = append(detailLines, fmt.Sprintf("创建 HTTP 请求失败: %v", err))
		return "", false, buildDetail(), err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json, application/problem+json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Origin", baseURL)
	req.Header.Set("Referer", baseURL+"/upload")
	req.Header.Set("X-CSRF-Token", csrf)
	req.Header.Set("Idempotency-Key", rousiNewUUID())

	resp, err := client.Do(req)
	if err != nil {
		detailLines = append(detailLines, fmt.Sprintf("请求失败: %v", err))
		return "", false, buildDetail(), err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	bodyText := string(respBody)
	responseDetail := summarizeRousiResponseBody(bodyText)
	detailLines = append(detailLines, fmt.Sprintf("响应状态: %s", strings.TrimSpace(resp.Status)))
	if responseDetail != "" {
		detailLines = append(detailLines, fmt.Sprintf("站点响应: %s", responseDetail))
	}

	isExisting := looksLikeExistingTorrentForRousi(bodyText)
	problem := parseRousiProblem(bodyText)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := rousiProblemMessage(problem, resp.StatusCode)
		if problem.Code != "" {
			isExisting = isExisting || rousiLooksLikeDuplicateCode(problem.Code)
		}
		err = fmt.Errorf("HTTP %d 上传失败: %s", resp.StatusCode, message)
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", isExisting, buildDetail(), err
	}

	result := rousiSubmitResult{}
	if err := json.Unmarshal(respBody, &result); err != nil || result.ID <= 0 {
		err = fmt.Errorf("API 返回无法识别的提交结果: %s", responseDetail)
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return "", isExisting, buildDetail(), err
	}

	if result.State != "" {
		detailLines = append(detailLines, fmt.Sprintf("发布状态: %s", result.State))
	}
	if result.InfoHashV1 != "" {
		detailLines = append(detailLines, fmt.Sprintf("infohash: %s", result.InfoHashV1))
	}

	publishURL := fmt.Sprintf("%s/torrents/%d", baseURL, result.ID)
	detailLines = append(detailLines, fmt.Sprintf("解析详情页: %s", publishURL))
	if strings.EqualFold(result.State, "pending_review") {
		detailLines = append(detailLines, "站点状态为待审核：种子已提交，需站点审核通过后才会公开展示")
	}
	return publishURL, isExisting, buildDetail(), nil
}

type rousiProblemBody struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Title  string `json:"title"`
	Status int    `json:"status"`
}

func parseRousiProblem(body string) rousiProblemBody {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" || !strings.HasPrefix(trimmed, "{") {
		return rousiProblemBody{}
	}
	parsed := rousiProblemBody{}
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return rousiProblemBody{}
	}
	return parsed
}

// rousiProblemMessage 把站点的 problem+json 错误翻译成可读信息。
func rousiProblemMessage(problem rousiProblemBody, statusCode int) string {
	detail := strings.TrimSpace(problem.Detail)
	title := strings.TrimSpace(problem.Title)
	switch strings.TrimSpace(problem.Code) {
	case "invalid_torrent_submission":
		return "种子提交内容无效：站点要求「原始私有 v1 .torrent」，请检查分类/标题/种子文件"
	case "web_session_required", "session_required":
		return "会话 Cookie 无效或已过期，请重新复制 " + rousiSessionCookieName
	case "origin_not_allowed":
		return "请求来源校验失败（Origin/Referer 必须为站点自身域名）"
	case "csrf_token_invalid", "csrf_token_missing":
		return "CSRF token 失效，重试即可"
	case "contract_validation_failed":
		if detail != "" {
			return detail
		}
		return "请求不符合站点 API 契约"
	}
	switch {
	case detail != "" && title != "":
		return title + "：" + detail
	case detail != "":
		return detail
	case title != "":
		return title
	default:
		return fmt.Sprintf("HTTP %d", statusCode)
	}
}

func rousiLooksLikeDuplicateCode(code string) bool {
	lower := strings.ToLower(strings.TrimSpace(code))
	return strings.Contains(lower, "duplicate") || strings.Contains(lower, "already_exists") || strings.Contains(lower, "exists")
}

// resolveRousiCategoryID 把标准化类型换算成站点分类 id（movies/tv/anime/...）。
func resolveRousiCategoryID(uploadData map[string]any, standardized map[string]any, siteCfg *publishmapping.SitePublishConfig) string {
	rawCategory := firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["category"])),
		strings.TrimSpace(toStringLoose(uploadData["type"])),
		strings.TrimSpace(toStringLoose(standardized["category"])),
		strings.TrimSpace(toStringLoose(standardized["type"])),
	)
	return resolveRousiCategory(rawCategory, siteCfg)
}

func resolveRousiCategory(rawCategory string, siteCfg *publishmapping.SitePublishConfig) string {
	candidate := strings.TrimSpace(rawCategory)
	if siteCfg != nil && candidate != "" {
		candidate = strings.TrimSpace(publishmapping.PickMappedValueWithFallback("type", siteCfg.Mappings["type"], candidate))
	}
	candidate = normalizeRousiToken(candidate)
	lower := strings.ToLower(candidate)

	switch {
	case strings.Contains(lower, "movie") || strings.Contains(candidate, "电影"):
		return "movies"
	case strings.Contains(lower, "tv_shows") || strings.Contains(lower, "variety") || strings.Contains(candidate, "综艺"):
		return "variety"
	case strings.Contains(lower, "tv_series") || lower == "tv" || strings.Contains(candidate, "电视剧") || strings.Contains(candidate, "剧集"):
		return "tv"
	case strings.Contains(lower, "animation") || strings.Contains(lower, "anime") || strings.Contains(candidate, "动漫") || strings.Contains(candidate, "动画"):
		return "anime"
	case strings.Contains(lower, "documentar") || strings.Contains(candidate, "纪录"):
		return "documentary"
	case strings.Contains(lower, "sports") || strings.Contains(candidate, "体育"):
		return "sports"
	case strings.Contains(lower, "music") || strings.Contains(candidate, "音乐"):
		return "music"
	case strings.Contains(lower, "software") || strings.Contains(candidate, "软件"):
		return "software"
	case strings.Contains(lower, "ebook") || strings.Contains(lower, "book") || strings.Contains(candidate, "电子书"):
		return "ebooks"
	case strings.Contains(lower, "9kg") || strings.Contains(candidate, "9kg"):
		return "9kg"
	case strings.Contains(lower, "game") || strings.Contains(candidate, "游戏"):
		return "other"
	case strings.TrimSpace(candidate) == "":
		return "other"
	default:
		return candidate
	}
}

// buildRousiFacetSelections 依据站点维度定义与本地上传数据构造 facet_selections。
// 参数/返回：facets 为空表示维度接口不可用，此时降级用本地映射兜底。
// 失败场景：站点声明 required 的维度仍无值（且没有可用的兜底项）时返回 error。
func buildRousiFacetSelections(
	categoryID string,
	facets []rousiFacet,
	uploadData map[string]any,
	standardized map[string]any,
	siteCfg *publishmapping.SitePublishConfig,
	title string,
) ([]rousiFacetSelection, string, error) {
	if len(facets) == 0 {
		selections := buildRousiFallbackSelections(categoryID, uploadData, standardized, siteCfg, title)
		summary := fmt.Sprintf("维度来源: 本地映射（facet 接口不可用），selections=%d", len(selections))
		return selections, summary, nil
	}

	genreCandidates := collectRousiGenreCandidates(uploadData, standardized)
	regionCandidates := rousiRegionCandidates(uploadData, standardized, siteCfg)
	resolutionCandidates := rousiResolutionCandidates(uploadData, standardized, siteCfg)
	releaseSelections := inferRousiReleaseFacetSelections(facets, title)

	selections := make([]rousiFacetSelection, 0, len(facets))
	picked := make([]string, 0, len(facets))

	for _, facet := range facets {
		var optionKeys []string
		switch facet.ID {
		case "genre", "themes", "behaviors":
			optionKeys = rousiMatchOptions(facet, genreCandidates, !strings.EqualFold(facet.SelectionMode, "single_option"), 6)
		case "region":
			optionKeys = rousiMatchOptions(facet, regionCandidates, false, 1)
		case "resolution":
			optionKeys = rousiMatchOptions(facet, resolutionCandidates, false, 1)
		default:
			optionKeys = rousiMatchOptions(facet, releaseSelections[facet.ID], false, 1)
		}

		if len(optionKeys) == 0 && facet.Required {
			if fallback := rousiFacetFallbackOption(facet); fallback != "" {
				optionKeys = []string{fallback}
			}
		}
		if len(optionKeys) == 0 {
			continue
		}
		selections = append(selections, rousiFacetSelection{FacetID: facet.ID, OptionKeys: optionKeys})
		picked = append(picked, facet.ID+"="+strings.Join(optionKeys, "|"))
	}

	// requirement_group：同组至少要有一个维度有值（如「来源」组里的 source-medium / release-type）。
	groupHasValue := map[string]bool{}
	for _, selection := range selections {
		for _, facet := range facets {
			if facet.ID == selection.FacetID && facet.RequirementGroup != "" {
				groupHasValue[facet.RequirementGroup] = true
			}
		}
	}
	for _, facet := range facets {
		if facet.RequirementGroup == "" || groupHasValue[facet.RequirementGroup] {
			continue
		}
		fallback := rousiFacetFallbackOption(facet)
		if fallback == "" {
			continue
		}
		selections = append(selections, rousiFacetSelection{FacetID: facet.ID, OptionKeys: []string{fallback}})
		picked = append(picked, facet.ID+"="+fallback)
		groupHasValue[facet.RequirementGroup] = true
	}

	// required 维度兜底后仍为空即为硬性失败，避免提交后被站点拒绝。
	missing := []string{}
	for _, facet := range facets {
		if !facet.Required {
			continue
		}
		found := false
		for _, selection := range selections {
			if selection.FacetID == facet.ID && len(selection.OptionKeys) > 0 {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, facet.Name+"("+facet.ID+")")
		}
	}
	if len(missing) > 0 {
		return nil, "", fmt.Errorf("站点必填维度缺失且无可兜底选项: %s", strings.Join(missing, "、"))
	}

	summary := fmt.Sprintf("维度映射: %s", strings.Join(picked, "; "))
	return selections, summary, nil
}

// buildRousiFallbackSelections 在维度接口不可用时用本地映射兜底。
func buildRousiFallbackSelections(
	categoryID string,
	uploadData map[string]any,
	standardized map[string]any,
	siteCfg *publishmapping.SitePublishConfig,
	title string,
) []rousiFacetSelection {
	selections := make([]rousiFacetSelection, 0, 5)

	if siteCfg != nil {
		if genres := resolveRousiGenres(categoryID, uploadData, standardized, siteCfg); len(genres) > 0 {
			selections = append(selections, rousiFacetSelection{FacetID: "genre", OptionKeys: genres})
		}
	}
	if region := resolveRousiMappedValue(siteCfg, "region", firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["region"])),
		strings.TrimSpace(toStringLoose(standardized["region"])),
		strings.TrimSpace(toStringLoose(standardized["source"])),
	)); region != "" {
		selections = append(selections, rousiFacetSelection{FacetID: "region", OptionKeys: []string{region}})
	}
	if resolution := resolveRousiMappedValue(siteCfg, "resolution", firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["resolution"])),
		strings.TrimSpace(toStringLoose(standardized["resolution"])),
	)); resolution != "" {
		selections = append(selections, rousiFacetSelection{FacetID: "resolution", OptionKeys: []string{resolution}})
	}

	for facetID, keys := range inferRousiReleaseFacetSelections(nil, title) {
		if len(keys) == 0 {
			continue
		}
		selections = append(selections, rousiFacetSelection{FacetID: facetID, OptionKeys: []string{keys[0]}})
	}
	return selections
}

// rousiRegionCandidates 汇总产地候选（标准值 → 站点映射值）。
func rousiRegionCandidates(uploadData map[string]any, standardized map[string]any, siteCfg *publishmapping.SitePublishConfig) []string {
	raw := firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["region"])),
		strings.TrimSpace(toStringLoose(standardized["region"])),
		strings.TrimSpace(toStringLoose(uploadData["source"])),
		strings.TrimSpace(toStringLoose(standardized["source"])),
	)
	candidates := []string{}
	if mapped := resolveRousiMappedValue(siteCfg, "region", raw); mapped != "" {
		candidates = append(candidates, mapped)
	}
	if raw != "" {
		candidates = append(candidates, normalizeRousiToken(raw))
	}
	return candidates
}

// rousiResolutionCandidates 汇总分辨率候选（标准值 → 站点映射值）。
func rousiResolutionCandidates(uploadData map[string]any, standardized map[string]any, siteCfg *publishmapping.SitePublishConfig) []string {
	raw := firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["resolution"])),
		strings.TrimSpace(toStringLoose(standardized["resolution"])),
	)
	candidates := []string{}
	if mapped := resolveRousiMappedValue(siteCfg, "resolution", raw); mapped != "" {
		candidates = append(candidates, mapped)
	}
	if raw != "" {
		candidates = append(candidates, normalizeRousiToken(raw))
	}
	return candidates
}

// rousiMatchOptions 在站点选项里按候选值匹配（key / label 均参与，忽略大小写）。
func rousiMatchOptions(facet rousiFacet, candidates []string, multi bool, limit int) []string {
	if len(candidates) == 0 || len(facet.Options) == 0 {
		return nil
	}
	matches := make([]string, 0, limit)
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		target := strings.TrimSpace(candidate)
		if target == "" {
			continue
		}
		for _, option := range facet.Options {
			if !strings.EqualFold(strings.TrimSpace(option.Key), target) && !strings.EqualFold(strings.TrimSpace(option.Label), target) {
				continue
			}
			if _, exists := seen[option.Key]; exists {
				break
			}
			seen[option.Key] = struct{}{}
			matches = append(matches, option.Key)
			break
		}
		if !multi && len(matches) > 0 {
			break
		}
		if limit > 0 && len(matches) >= limit {
			break
		}
	}
	return matches
}

// rousiFacetFallbackOption 返回维度的兜底选项（优先 other / 其它）。
func rousiFacetFallbackOption(facet rousiFacet) string {
	for _, option := range facet.Options {
		key := strings.ToLower(strings.TrimSpace(option.Key))
		label := strings.TrimSpace(option.Label)
		if key == "other" || label == "其它" || label == "其他" {
			return option.Key
		}
	}
	return ""
}

// inferRousiReleaseFacetSelections 从标题推断「来源 / 发布类型」两组维度。
// 说明：facets 为 nil 时返回不受站点选项约束的推断结果，仅在维度接口降级时使用。
func inferRousiReleaseFacetSelections(facets []rousiFacet, title string) map[string][]string {
	text := strings.TrimSpace(title)
	result := map[string][]string{}
	if text == "" {
		return result
	}

	allowed := func(facetID string, key string) bool {
		if len(facets) == 0 {
			return true
		}
		for _, facet := range facets {
			if facet.ID != facetID {
				continue
			}
			for _, option := range facet.Options {
				if strings.EqualFold(strings.TrimSpace(option.Key), key) {
					return true
				}
			}
			return false
		}
		return false
	}
	push := func(facetID string, key string) {
		if key == "" || !allowed(facetID, key) {
			return
		}
		if len(result[facetID]) == 0 {
			result[facetID] = []string{key}
		}
	}

	if reRousiSourceFullDisc.MatchString(text) {
		push("release-type", "full-disc")
	}
	if reRousiSourceRemux.MatchString(text) {
		push("release-type", "remux")
	}
	if reRousiSourceUHDBluRayRip.MatchString(text) {
		push("source-medium", "uhd-blu-ray")
	} else if reRousiSourceBluRayRip.MatchString(text) {
		push("source-medium", "blu-ray")
	}
	if reRousiSourceWebDL.MatchString(text) {
		push("release-type", "web-dl")
		push("source-medium", "web")
	}
	if reRousiSourceWebRip.MatchString(text) {
		push("release-type", "webrip")
		push("source-medium", "web")
	}
	if reRousiSourceDVD.MatchString(text) {
		push("release-type", "dvdrip")
		push("source-medium", "dvd")
	}
	if reRousiSourceHDTV.MatchString(text) {
		push("release-type", "hdtv")
	}
	if reRousiSourceBroadcast.MatchString(text) {
		push("source-medium", "broadcast")
	}
	if reRousiSourceCam.MatchString(text) {
		push("release-type", "cam")
	}
	return result
}

// resolveRousiExternalIDs 解析 imdb / tmdb / douban 的纯 ID（站点只收 ID，不收链接）。
func resolveRousiExternalIDs(uploadData map[string]any, standardized map[string]any, imdbLink string, doubanLink string) map[string]string {
	result := map[string]string{}

	imdbRaw := firstNonEmpty(
		imdbLink,
		strings.TrimSpace(toStringLoose(uploadData["imdb"])),
		strings.TrimSpace(toStringLoose(uploadData["imdb_link"])),
		strings.TrimSpace(toStringLoose(standardized["imdb"])),
		strings.TrimSpace(toStringLoose(standardized["imdb_link"])),
	)
	if match := reRousiIMDbID.FindStringSubmatch(imdbRaw); len(match) >= 2 {
		result["imdb_id"] = strings.ToLower(strings.TrimSpace(match[1]))
	}

	tmdbRaw := firstNonEmpty(
		strings.TrimSpace(toStringLoose(uploadData["tmdb"])),
		strings.TrimSpace(toStringLoose(uploadData["tmdb_link"])),
		strings.TrimSpace(toStringLoose(standardized["tmdb"])),
		strings.TrimSpace(toStringLoose(standardized["tmdb_link"])),
	)
	if tmdbRaw != "" {
		if match := reRousiTmdbID.FindStringSubmatch(tmdbRaw); len(match) >= 2 {
			result["tmdb_id"] = strings.TrimSpace(match[1])
		} else if digits := strings.TrimSpace(regexp.MustCompile(`^\d{1,10}$`).FindString(tmdbRaw)); digits != "" {
			result["tmdb_id"] = digits
		}
	}

	doubanRaw := firstNonEmpty(
		doubanLink,
		strings.TrimSpace(toStringLoose(uploadData["douban"])),
		strings.TrimSpace(toStringLoose(uploadData["douban_link"])),
		strings.TrimSpace(toStringLoose(standardized["douban"])),
		strings.TrimSpace(toStringLoose(standardized["douban_link"])),
	)
	if doubanRaw != "" {
		if match := reRousiDoubanID.FindStringSubmatch(doubanRaw); len(match) >= 2 {
			result["douban_id"] = strings.TrimSpace(match[1])
		} else if digits := strings.TrimSpace(regexp.MustCompile(`^\d{5,12}$`).FindString(doubanRaw)); digits != "" {
			result["douban_id"] = digits
		}
	}

	return result
}

// rousiMultipartInput 是构造 multipart 表单所需的全部内容。
type rousiMultipartInput struct {
	CategoryID   string
	Title        string
	Subtitle     string
	Description  string
	MediaInfo    string
	Anonymous    bool
	ExternalIDs  map[string]string
	Selections   []rousiFacetSelection
	Screenshots  []rousiScreenshot
	TorrentName  string
	TorrentBytes []byte
}

// buildRousiMultipartBody 按站点契约拼装 multipart 请求体。
func buildRousiMultipartBody(input rousiMultipartInput) (*bytes.Buffer, string, error) {
	if strings.TrimSpace(input.Title) == "" {
		return nil, "", fmt.Errorf("标题为空")
	}
	if len(input.TorrentBytes) == 0 {
		return nil, "", fmt.Errorf("种子文件为空")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	writeField := func(name string, value string) error {
		return writer.WriteField(name, value)
	}

	if err := writeField("category_id", input.CategoryID); err != nil {
		return nil, "", err
	}
	if err := writeField("title", input.Title); err != nil {
		return nil, "", err
	}
	if err := writeField("subtitle", input.Subtitle); err != nil {
		return nil, "", err
	}
	if err := writeField("description", input.Description); err != nil {
		return nil, "", err
	}
	if err := writeField("media_info", input.MediaInfo); err != nil {
		return nil, "", err
	}
	if err := writeField("anonymous", strconv.FormatBool(input.Anonymous)); err != nil {
		return nil, "", err
	}
	for _, key := range []string{"imdb_id", "tmdb_id", "douban_id"} {
		if value := strings.TrimSpace(input.ExternalIDs[key]); value != "" {
			if err := writeField(key, value); err != nil {
				return nil, "", err
			}
		}
	}

	for index, selection := range input.Selections {
		payload, err := json.Marshal(selection)
		if err != nil {
			return nil, "", err
		}
		if err := rousiWriteBinaryPart(writer, "facet_selections", fmt.Sprintf("facet-selection-%d.json", index+1), "application/json", payload); err != nil {
			return nil, "", err
		}
	}

	for index, shot := range input.Screenshots {
		name := strings.TrimSpace(shot.Name)
		if name == "" {
			name = fmt.Sprintf("screenshot-%d.jpg", index+1)
		}
		contentType := strings.TrimSpace(shot.ContentType)
		if contentType == "" {
			contentType = "image/jpeg"
		}
		if err := rousiWriteBinaryPart(writer, "screenshots", name, contentType, shot.Data); err != nil {
			return nil, "", err
		}
	}

	torrentName := strings.TrimSpace(input.TorrentName)
	if torrentName == "" {
		torrentName = "torrent.torrent"
	}
	if err := rousiWriteBinaryPart(writer, "torrent_file", torrentName, "application/x-bittorrent", input.TorrentBytes); err != nil {
		return nil, "", err
	}

	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return body, writer.FormDataContentType(), nil
}

// rousiWriteBinaryPart 写入一个带文件名的 multipart part（保留前端使用的 filename 形式）。
func rousiWriteBinaryPart(writer *multipart.Writer, field string, filename string, contentType string, data []byte) error {
	safeName := strings.ReplaceAll(strings.ReplaceAll(filename, `"`, "'"), "\r", "")
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, field, safeName))
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	_, err = part.Write(data)
	return err
}

// rousiNewUUID 生成 v4 UUID 作为 Idempotency-Key。
func rousiNewUUID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", buf[0:4], buf[4:6], buf[6:8], buf[8:10], buf[10:16])
}

// collectRousiScreenshots 把发布数据里的图片转成站点可接受的文件（JPEG/PNG/WebP、单张 ≤ 2 MiB）。
// 上传顺序：封面（海报）第一，截图随后 —— 站点把截图区第一张当作封面。
// 返回：图片列表、清理函数、过程摘要、错误（无可用图片时返回空列表而非错误）。
func collectRousiScreenshots(baseURL string, cookie string, userAgent string, torrentPath string, uploadData map[string]any) ([]rousiScreenshot, func(), string, error) {
	grouped := collectRousiImageSources(uploadData)
	sources := flattenRousiImageSources(grouped)
	if len(sources) == 0 {
		return nil, nil, "截图处理: 未找到可用图片来源", nil
	}

	paths := config.ResolveRuntimePaths()
	imagesRoot := filepath.Join(paths.DataDir, "tmp", "torrents", "images")
	if err := os.MkdirAll(imagesRoot, 0o755); err != nil {
		return nil, nil, "", err
	}

	sourceSiteCode := "unknown"
	torrentID := "unknown"
	if base := strings.TrimSpace(filepath.Base(torrentPath)); base != "" {
		if match := reRousiTorrentFilename.FindStringSubmatch(base); len(match) >= 3 {
			sourceSiteCode = strings.TrimSpace(match[1])
			torrentID = strings.TrimSpace(match[2])
		}
	}

	timestamp := time.Now().Format("2006-01-02-15:04:05")
	workDirName := fmt.Sprintf("%s-%s-rousi-%s", sanitizeRousiTempDirPart(sourceSiteCode), sanitizeRousiTempDirPart(torrentID), sanitizeRousiTempDirPart(timestamp))
	workDir := filepath.Join(imagesRoot, workDirName)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, nil, "", err
	}
	cleanup := func() { safeRemoveWorkDir(imagesRoot, workDir) }

	shots := make([]rousiScreenshot, 0, rousiScreenshotMaxCount)
	totalUsed := int64(0)
	skipped := 0
	firstFromCover := false

	for index, item := range sources {
		if len(shots) >= rousiScreenshotMaxCount {
			break
		}
		sourceURL := strings.TrimSpace(item.URL)
		if sourceURL == "" {
			continue
		}

		originalPath := ""
		if strings.HasPrefix(sourceURL, "data:image/") {
			_, ext, raw, ok := parseRousiDataURL(sourceURL)
			if !ok {
				skipped++
				continue
			}
			originalPath = filepath.Join(workDir, fmt.Sprintf("%02d.original.%s", index, ext))
			if err := os.WriteFile(originalPath, raw, 0o644); err != nil {
				skipped++
				continue
			}
		} else if strings.HasPrefix(sourceURL, "http://") || strings.HasPrefix(sourceURL, "https://") {
			raw, err := downloadRousiImageToBytes(baseURL, cookie, userAgent, sourceURL, item.Referer)
			if err != nil || len(raw) == 0 {
				skipped++
				continue
			}
			originalPath = filepath.Join(workDir, fmt.Sprintf("%02d.downloaded", index))
			if err := os.WriteFile(originalPath, raw, 0o644); err != nil {
				skipped++
				continue
			}
		} else {
			skipped++
			continue
		}

		jpegPath := filepath.Join(workDir, fmt.Sprintf("%02d.final.jpg", index))
		data, ok := rousiPrepareScreenshotFile(originalPath, jpegPath, rousiScreenshotMaxBytes)
		if !ok {
			skipped++
			continue
		}
		if totalUsed+int64(len(data)) > rousiScreenshotTotalMax {
			break
		}
		totalUsed += int64(len(data))
		ext, contentType := rousiDetectImageType(data)
		// 第一张即站点封面；后面的按截图命名（站点按顺序展示，名字仅作可读标识）。
		fileName := fmt.Sprintf("screenshot-%d%s", len(shots)+1, ext)
		if len(shots) == 0 {
			fileName = "cover" + ext
			firstFromCover = index < len(grouped.Cover)
		}
		shots = append(shots, rousiScreenshot{
			Name:        fileName,
			ContentType: contentType,
			Data:        data,
		})
	}

	firstLabel := "none"
	if len(shots) > 0 {
		firstLabel = "screenshot"
		if firstFromCover {
			firstLabel = "poster"
		}
	}
	summary := fmt.Sprintf("截图处理: cover=%d screenshots=%d uploaded=%d first=%s skipped=%d total_bytes=%d",
		len(grouped.Cover), len(grouped.Screenshots), len(shots), firstLabel, skipped, totalUsed)
	if len(shots) > 0 && len(grouped.Screenshots) == 0 {
		summary += "；警告：简介里没有截图，本次只上传封面"
	}
	return shots, cleanup, summary, nil
}

// rousiPrepareScreenshotFile 把本地图片转成 ≤ maxBytes 且站点允许的格式。
// 顺序：原图合规且不超限直接透传（不重编码、不掉画质）→ ffmpeg 压缩 → 纯 Go 压缩兜底。
func rousiPrepareScreenshotFile(inputPath string, outputPath string, maxBytes int64) ([]byte, bool) {
	if info, err := os.Stat(inputPath); err == nil && !info.IsDir() && info.Size() > 0 && info.Size() <= maxBytes {
		if data, err := os.ReadFile(inputPath); err == nil && looksLikeRousiUploadableImage(data) {
			return data, true
		}
	}
	if ffmpegConvertToJpegUnderBytesLimit(inputPath, outputPath, maxBytes) {
		if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 && int64(len(data)) <= maxBytes {
			return data, true
		}
	}
	if compressRousiImageToJPEG(inputPath, outputPath, maxBytes) {
		if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 && int64(len(data)) <= maxBytes {
			return data, true
		}
	}
	return nil, false
}

// looksLikeRousiUploadableImage 通过文件头判断是否为站点允许的图片格式（JPEG/PNG/WebP）。
func looksLikeRousiUploadableImage(data []byte) bool {
	ext, _ := rousiDetectImageType(data)
	return ext != ""
}

// rousiDetectImageType 依据文件头返回扩展名与 MIME（站点只接受 JPEG/PNG/WebP）。
func rousiDetectImageType(data []byte) (string, string) {
	if len(data) < 12 {
		return "", ""
	}
	if data[0] == 0xFF && data[1] == 0xD8 {
		return ".jpg", "image/jpeg"
	}
	if data[0] == 0x89 && data[1] == 0x50 && data[2] == 0x4E && data[3] == 0x47 {
		return ".png", "image/png"
	}
	if string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return ".webp", "image/webp"
	}
	return "", ""
}

// ffmpegConvertToJpegUnderBytesLimit 用 ffmpeg 把图片压成不超过 maxBytes 的 JPEG。
func ffmpegConvertToJpegUnderBytesLimit(inputPath string, outputPath string, maxBytes int64) bool {
	scalesPrimary := []int{0, 1920, 1600, 1280, 1024, 800}
	qValuesPrimary := []int{3, 5, 7, 9, 11, 13, 15, 17, 20, 24, 28, 32}
	scalesSecondary := []int{720, 640, 576, 512, 480}
	qValuesSecondary := []int{28, 36, 40, 45, 50}

	try := func(scales []int, qValues []int) bool {
		for _, scale := range scales {
			for _, qv := range qValues {
				vfArg := "null"
				if scale > 0 {
					vfArg = fmt.Sprintf("scale='min(%d,iw)':-2:flags=lanczos", scale)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				cmd := exec.CommandContext(
					ctx,
					"ffmpeg",
					"-y", "-hide_banner", "-loglevel", "error",
					"-i", inputPath,
					"-an", "-sn", "-dn",
					"-frames:v", "1",
					"-vf", vfArg,
					"-pix_fmt", "yuvj420p",
					"-q:v", strconv.Itoa(qv),
					outputPath,
				)
				cmd.Stdout = io.Discard
				cmd.Stderr = io.Discard
				_ = cmd.Run()
				cancel()

				if info, err := os.Stat(outputPath); err == nil && !info.IsDir() && info.Size() > 0 && info.Size() <= maxBytes {
					return true
				}
			}
		}
		return false
	}

	return try(scalesPrimary, qValuesPrimary) || try(scalesSecondary, qValuesSecondary)
}

// downloadRousiImageToBytes 下载截图（同域请求自动带会话 Cookie）。
func downloadRousiImageToBytes(baseURL string, cookie string, userAgent string, targetURL string, referer string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}

	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")

	chosenReferer := strings.TrimSpace(referer)
	if !(strings.HasPrefix(chosenReferer, "http://") || strings.HasPrefix(chosenReferer, "https://")) {
		chosenReferer = ""
	}
	if chosenReferer == "" {
		chosenReferer = defaultRousiRefererForURL(targetURL)
	}
	if chosenReferer != "" {
		req.Header.Set("Referer", chosenReferer)
	}

	// 站点自身的私有截图（如站点附件）需要会话 Cookie，外链图床不需要也不该带。
	if shouldAttachRousiCookie(baseURL, targetURL) && strings.TrimSpace(cookie) != "" {
		req.Header.Set("Cookie", cookie)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
}

func defaultRousiRefererForURL(targetURL string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(targetURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host + "/"
}

func shouldAttachRousiCookie(baseURL string, targetURL string) bool {
	baseParsed, err := neturl.Parse(strings.TrimSpace(baseURL))
	if err != nil || baseParsed.Host == "" {
		return false
	}
	targetParsed, err := neturl.Parse(strings.TrimSpace(targetURL))
	if err != nil || targetParsed.Host == "" {
		return false
	}
	return strings.EqualFold(baseParsed.Host, targetParsed.Host)
}

// extractRousiImageSourcesFromText 从文本中提取图片地址（BBCode / Markdown / HTML / 裸 URL）。
func extractRousiImageSourcesFromText(text string) []rousiImageSource {
	source := strings.TrimSpace(text)
	if source == "" {
		return nil
	}

	items := make([]rousiImageSource, 0, 16)
	appendURL := func(url string, referer string) {
		trimmed := strings.TrimSpace(url)
		if trimmed == "" {
			return
		}
		items = append(items, rousiImageSource{
			URL:     trimmed,
			Referer: strings.TrimSpace(referer),
		})
	}

	for _, match := range reRousiBBCodeURLImg.FindAllStringSubmatch(source, -1) {
		if len(match) < 3 {
			continue
		}
		pageURL := strings.TrimSpace(match[1])
		imgURL := strings.TrimSpace(match[2])
		if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
			appendURL(imgURL, pageURL)
		}
	}
	for _, match := range reRousiBBCodeImgSource.FindAllStringSubmatch(source, -1) {
		if len(match) < 2 {
			continue
		}
		imgURL := strings.TrimSpace(match[1])
		if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
			appendURL(imgURL, "")
		}
	}
	for _, match := range reRousiMDImgSource.FindAllStringSubmatch(source, -1) {
		if len(match) < 2 {
			continue
		}
		imgURL := strings.TrimSpace(match[1])
		if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
			appendURL(imgURL, "")
		}
	}
	for _, match := range reRousiHTMLImgSource.FindAllStringSubmatch(source, -1) {
		if len(match) < 2 {
			continue
		}
		imgURL := strings.TrimSpace(match[1])
		if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
			appendURL(imgURL, "")
		}
	}

	seen := map[string]struct{}{}
	result := make([]rousiImageSource, 0, len(items))
	for _, item := range items {
		url := strings.TrimSpace(item.URL)
		if url == "" {
			continue
		}

		if isRousiPixhostURL(url) && strings.Contains(url, "/thumbs/") {
			if sub := reRousiPixhostThumb.FindStringSubmatch(url); len(sub) >= 3 {
				url = "https://img" + strings.TrimSpace(sub[1]) + ".pixhost.cc/images/" + strings.TrimSpace(sub[2])
			}
		}

		if !isProbableRousiImageURL(url) {
			continue
		}

		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		result = append(result, rousiImageSource{URL: url, Referer: strings.TrimSpace(item.Referer)})
	}

	return result
}

func isProbableRousiImageURL(url string) bool {
	lower := strings.ToLower(strings.TrimSpace(url))
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return false
	}
	if reRousiImageExt.MatchString(lower) {
		return true
	}
	if isRousiPixhostURL(lower) && (strings.Contains(lower, "/images/") || strings.Contains(lower, "/thumbs/")) {
		return true
	}
	return false
}

func isRousiPixhostURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.Contains(lower, "pixhost.to") || strings.Contains(lower, "pixhost.cc")
}

// rousiImageSources 是按站点语义分好组的待上传图片：封面（海报）与截图分开。
// 站点规则：截图区「第一张即封面」，所以海报必须排在最前，其余按顺序作为截图。
type rousiImageSources struct {
	Cover       []rousiImageSource
	Screenshots []rousiImageSource
}

// collectRousiImageSources 汇总上传图片：封面只取简介海报的第一张，截图取本地 images 与简介截图。
func collectRousiImageSources(uploadData map[string]any) rousiImageSources {
	if uploadData == nil {
		return rousiImageSources{}
	}

	var grouped rousiImageSources
	intro := mapStringAny(uploadData["intro"])

	// 封面（海报）：站点把第一张图当封面，所以海报排在截图之前，且最多取一张。
	for _, item := range extractRousiImageSourcesFromText(toStringLoose(intro["poster"])) {
		if url := strings.TrimSpace(item.URL); url != "" {
			grouped.Cover = append(grouped.Cover, rousiImageSource{URL: url, Referer: strings.TrimSpace(item.Referer)})
			break
		}
	}

	// 截图：本地 data URL（发布时刚生成/改过）在前，简介里的截图在后。
	for _, item := range extractStringList(uploadData["images"]) {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "data:image/") || isProbableRousiImageURL(trimmed) {
			grouped.Screenshots = append(grouped.Screenshots, rousiImageSource{URL: trimmed})
		}
	}
	for _, item := range extractRousiImageSourcesFromText(toStringLoose(intro["screenshots"])) {
		if url := strings.TrimSpace(item.URL); url != "" {
			grouped.Screenshots = append(grouped.Screenshots, rousiImageSource{URL: url, Referer: strings.TrimSpace(item.Referer)})
		}
	}

	return grouped
}

// flattenRousiImageSources 把封面与截图拼成上传顺序（封面第一），并按 URL 去重。
func flattenRousiImageSources(grouped rousiImageSources) []rousiImageSource {
	merged := make([]rousiImageSource, 0, len(grouped.Cover)+len(grouped.Screenshots))
	merged = append(merged, grouped.Cover...)
	merged = append(merged, grouped.Screenshots...)

	seen := map[string]struct{}{}
	result := make([]rousiImageSource, 0, len(merged))
	for _, item := range merged {
		url := strings.TrimSpace(item.URL)
		if url == "" {
			continue
		}
		if _, exists := seen[url]; exists {
			continue
		}
		seen[url] = struct{}{}
		result = append(result, rousiImageSource{URL: url, Referer: strings.TrimSpace(item.Referer)})
	}
	return result
}

func extractStringList(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(toStringLoose(item)); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(typed); trimmed != "" {
			return []string{trimmed}
		}
		return nil
	default:
		return nil
	}
}

func parseRousiDataURL(dataURL string) (string, string, []byte, bool) {
	if !strings.HasPrefix(dataURL, "data:image/") {
		return "", "", nil, false
	}
	idx := strings.Index(dataURL, ";base64,")
	if idx < 0 {
		return "", "", nil, false
	}
	header := dataURL[:idx]
	b64 := dataURL[idx+len(";base64,"):]
	mime := strings.TrimSpace(strings.TrimPrefix(header, "data:"))
	ext := "bin"
	if parts := strings.SplitN(mime, "/", 2); len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		ext = strings.TrimSpace(parts[1])
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return mime, ext, nil, false
	}
	return mime, ext, raw, true
}

func sanitizeRousiTempDirPart(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "unknown"
	}
	trimmed = strings.ReplaceAll(trimmed, "/", "_")
	trimmed = strings.ReplaceAll(trimmed, "\\", "_")
	trimmed = strings.ReplaceAll(trimmed, ":", "_")
	trimmed = strings.ReplaceAll(trimmed, "..", "_")
	trimmed = strings.ReplaceAll(trimmed, " ", "_")
	return trimmed
}

func safeRemoveWorkDir(rootDir string, workDir string) {
	if strings.TrimSpace(rootDir) == "" || strings.TrimSpace(workDir) == "" {
		return
	}

	rootAbs, err := filepath.Abs(rootDir)
	if err != nil {
		return
	}
	workAbs, err := filepath.Abs(workDir)
	if err != nil {
		return
	}
	rootAbs = filepath.Clean(rootAbs)
	workAbs = filepath.Clean(workAbs)
	if !strings.HasPrefix(workAbs, rootAbs+string(os.PathSeparator)) {
		return
	}
	_ = os.RemoveAll(workAbs)
}

// resolveRousiMappedValue 按 yaml mappings 换算站点值（如 source.china → mainland-china）。
func resolveRousiMappedValue(siteCfg *publishmapping.SitePublishConfig, mappingKey string, rawValue string) string {
	candidate := strings.TrimSpace(rawValue)
	if candidate == "" {
		return ""
	}
	if siteCfg != nil {
		candidate = strings.TrimSpace(publishmapping.PickMappedValueWithFallback(mappingKey, siteCfg.Mappings[mappingKey], candidate))
	}
	return normalizeRousiToken(candidate)
}

// resolveRousiGenres 取「本地标签 ∩ 站点分类可用题材」，供 facet 接口降级时使用。
func resolveRousiGenres(category string, uploadData map[string]any, standardized map[string]any, siteCfg *publishmapping.SitePublishConfig) []string {
	if siteCfg == nil || len(siteCfg.GenreOptionsByType) == 0 {
		return nil
	}

	normalizedCategory := normalizeRousiToken(category)
	if normalizedCategory == "" {
		return nil
	}

	allowedGenres := siteCfg.GenreOptionsByType[normalizedCategory]
	if len(allowedGenres) == 0 {
		return nil
	}

	candidates := collectRousiGenreCandidates(uploadData, standardized)
	if len(candidates) == 0 {
		return nil
	}

	genreSet := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		normalized := normalizeRousiGenreToken(candidate)
		if normalized == "" {
			continue
		}
		genreSet[normalized] = struct{}{}
	}

	result := make([]string, 0, len(allowedGenres))
	for _, item := range allowedGenres {
		normalized := normalizeRousiGenreToken(item)
		if normalized == "" {
			continue
		}
		if _, ok := genreSet[normalized]; ok {
			result = append(result, normalized)
		}
	}
	return result
}

func collectRousiGenreCandidates(uploadData map[string]any, standardized map[string]any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 16)
	appendValue := func(value string) {
		normalized := normalizeRousiGenreToken(value)
		if normalized == "" {
			return
		}
		if _, exists := seen[normalized]; exists {
			return
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	appendTags := func(value any) {
		for _, item := range toStringSliceLoose(value) {
			appendValue(item)
		}
	}

	appendTags(standardized["tags"])
	appendTags(uploadData["tags"])
	if sourceParams := mapStringAny(uploadData["source_params"]); len(sourceParams) > 0 {
		appendTags(sourceParams["标签"])
	}
	return result
}

func normalizeRousiGenreToken(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "tag.") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "tag."))
	}
	if trimmed == "其他" {
		return "其它"
	}
	return trimmed
}

func normalizeRousiToken(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, ".") {
		parts := strings.SplitN(trimmed, ".", 2)
		prefix := strings.ToLower(strings.TrimSpace(parts[0]))
		if prefix == "source" || prefix == "resolution" || prefix == "medium" || prefix == "category" || prefix == "type" {
			trimmed = strings.TrimSpace(parts[1])
		}
	}
	return trimmed
}

// buildRousiDescriptionMarkdown 组装简介：BBCode 转 Markdown 并剔除图片（截图走独立字段）。
func buildRousiDescriptionMarkdown(uploadData map[string]any, fallback string) string {
	intro := mapStringAny(uploadData["intro"])

	posterScreenshotURLs := []string{}
	if len(intro) > 0 {
		for _, item := range extractRousiImageSourcesFromText(toStringLoose(intro["poster"])) {
			if trimmed := strings.TrimSpace(item.URL); trimmed != "" {
				posterScreenshotURLs = append(posterScreenshotURLs, trimmed)
			}
		}
		for _, item := range extractRousiImageSourcesFromText(toStringLoose(intro["screenshots"])) {
			if trimmed := strings.TrimSpace(item.URL); trimmed != "" {
				posterScreenshotURLs = append(posterScreenshotURLs, trimmed)
			}
		}
	}

	rawText := strings.TrimSpace(toStringLoose(uploadData["description"]))
	if rawText == "" && len(intro) > 0 {
		statement := strings.TrimSpace(toStringLoose(intro["statement"]))
		body := strings.TrimSpace(toStringLoose(intro["body"]))
		parts := make([]string, 0, 2)
		if statement != "" {
			parts = append(parts, statement)
		}
		if body != "" {
			parts = append(parts, body)
		}
		rawText = strings.Join(parts, "\n\n")
	}
	if rawText == "" {
		rawText = strings.TrimSpace(fallback)
	}
	if rawText == "" {
		return ""
	}

	converted := bbcodeToMarkdown(rawText)
	sanitized := sanitizeMarkdownNoImages(converted)
	return removeExplicitImageURLs(sanitized, posterScreenshotURLs)
}

func sanitizeMarkdownNoImages(text string) string {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" {
		return ""
	}
	cleaned = reRousiBBCodeImage.ReplaceAllString(cleaned, "")
	cleaned = reRousiHTMLImage.ReplaceAllString(cleaned, "")
	cleaned = reRousiMarkdownImage.ReplaceAllString(cleaned, "")

	lines := strings.Split(cleaned, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && reRousiOnlyImageURLLine.MatchString(trimmed) {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.TrimSpace(strings.Join(filtered, "\n"))
}

func bbcodeToMarkdown(text string) string {
	source := strings.TrimSpace(text)
	if source == "" {
		return ""
	}
	source = strings.ReplaceAll(source, "\r\n", "\n")
	source = strings.ReplaceAll(source, "\r", "\n")

	convertInline := func(s string) string {
		out := s

		// 兼容源站声明中被重复包裹的损坏 URL 标签，保留其内部 Markdown 链接文本。
		out = reRousiMalformedURL.ReplaceAllString(out, "[")

		out = reRousiBBCodeCode.ReplaceAllStringFunc(out, func(match string) string {
			sub := reRousiBBCodeCode.FindStringSubmatch(match)
			body := ""
			if len(sub) >= 2 {
				body = strings.Trim(sub[1], "\n")
			}
			if body == "" {
				return ""
			}
			return "\n```\n" + body + "\n```\n"
		})

		out = reRousiBBCodeURLWithArg.ReplaceAllStringFunc(out, func(match string) string {
			sub := reRousiBBCodeURLWithArg.FindStringSubmatch(match)
			if len(sub) < 3 {
				return match
			}
			url := strings.TrimSpace(sub[1])
			label := strings.TrimSpace(sub[2])
			if url == "" || label == "" {
				return label
			}
			return "[" + label + "](" + url + ")"
		})
		out = reRousiBBCodeURL.ReplaceAllStringFunc(out, func(match string) string {
			sub := reRousiBBCodeURL.FindStringSubmatch(match)
			if len(sub) < 2 {
				return match
			}
			url := strings.TrimSpace(sub[1])
			if url == "" {
				return ""
			}
			return "[" + url + "](" + url + ")"
		})

		out = reRousiBBCodeColor.ReplaceAllString(out, "$1")
		out = reRousiBBCodeSize.ReplaceAllString(out, "$1")
		out = reRousiBBCodeBold.ReplaceAllString(out, "**$1**")
		out = reRousiBBCodeItalic.ReplaceAllString(out, "*$1*")
		out = reRousiBBCodeUnderline.ReplaceAllString(out, "$1")

		out = strings.ReplaceAll(out, "[list]", "")
		out = strings.ReplaceAll(out, "[/list]", "")
		out = strings.ReplaceAll(out, "[*]", "- ")

		out = stripRemainingBBCodeTags(out)
		return out
	}

	parts := []string{}
	lastEnd := 0
	matches := reRousiBBCodeQuote.FindAllStringSubmatchIndex(source, -1)
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		before := source[lastEnd:match[0]]
		if strings.TrimSpace(before) != "" {
			parts = append(parts, strings.TrimSpace(convertInline(before)))
		}

		quoteBody := ""
		if match[2] >= 0 && match[3] >= 0 {
			quoteBody = strings.Trim(source[match[2]:match[3]], "\n")
		}
		quoteConverted := strings.TrimSpace(convertInline(quoteBody))
		if quoteConverted != "" {
			lines := strings.Split(quoteConverted, "\n")
			for i, line := range lines {
				lines[i] = strings.TrimRight("> "+line, " ")
			}
			parts = append(parts, strings.TrimRight(strings.Join(lines, "\n"), "\n"))
		}
		lastEnd = match[1]
	}

	tail := source[lastEnd:]
	if strings.TrimSpace(tail) != "" {
		parts = append(parts, strings.TrimSpace(convertInline(tail)))
	}

	out := strings.Join(parts, "\n\n")
	out = reRousiManyNewlines.ReplaceAllString(out, "\n\n")
	return strings.TrimSpace(out)
}

func stripRemainingBBCodeTags(text string) string {
	if text == "" {
		return ""
	}

	var b strings.Builder
	b.Grow(len(text))

	for i := 0; i < len(text); {
		if text[i] != '[' {
			b.WriteByte(text[i])
			i++
			continue
		}
		end := strings.IndexByte(text[i:], ']')
		if end < 0 {
			b.WriteByte(text[i])
			i++
			continue
		}
		end = i + end
		tagContent := text[i+1 : end]
		nextPos := end + 1
		if nextPos < len(text) && text[nextPos] == '(' {
			b.WriteString(text[i : end+1])
			i = end + 1
			continue
		}
		if isProbableBBCodeTag(tagContent) {
			i = end + 1
			continue
		}
		b.WriteString(text[i : end+1])
		i = end + 1
	}

	return b.String()
}

func isProbableBBCodeTag(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	trimmed = strings.TrimPrefix(trimmed, "/")
	if trimmed == "" {
		return false
	}

	nameEnd := 0
	for nameEnd < len(trimmed) {
		ch := trimmed[nameEnd]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			nameEnd++
			continue
		}
		break
	}
	if nameEnd == 0 {
		return false
	}
	name := trimmed[:nameEnd]
	for i := 0; i < len(name); i++ {
		ch := name[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') {
			return true
		}
	}
	return false
}

func removeExplicitImageURLs(text string, imageURLs []string) string {
	cleaned := strings.TrimSpace(text)
	if cleaned == "" || len(imageURLs) == 0 {
		return cleaned
	}

	for _, url := range imageURLs {
		trimmed := strings.TrimSpace(url)
		if trimmed == "" {
			continue
		}
		cleaned = strings.ReplaceAll(cleaned, trimmed, "")
	}

	lines := strings.Split(cleaned, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " \t\r")
	}

	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

func looksLikeExistingTorrentForRousi(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(trimmed, "种子已存在") ||
		strings.Contains(trimmed, "该种子已存在") ||
		strings.Contains(trimmed, "已存在") ||
		strings.Contains(lower, "already exists") ||
		strings.Contains(lower, "duplicate")
}

func summarizeRousiResponseBody(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	summary := strings.Join(strings.Fields(trimmed), " ")
	if len(summary) > 300 {
		return summary[:300] + "..."
	}
	return summary
}

func extractUUID(text string) string {
	match := reRousiUUID.FindStringSubmatch(text)
	if len(match) >= 2 {
		return strings.ToLower(strings.TrimSpace(match[1]))
	}
	return ""
}

func mapStringAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok && typed != nil {
		return typed
	}
	if typed, ok := value.(map[string]interface{}); ok && typed != nil {
		copied := make(map[string]any, len(typed))
		for key, item := range typed {
			copied[key] = item
		}
		return copied
	}
	return map[string]any{}
}

func toStringLoose(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int8:
		return fmt.Sprintf("%d", typed)
	case int16:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case uint:
		return fmt.Sprintf("%d", typed)
	case uint8:
		return fmt.Sprintf("%d", typed)
	case uint16:
		return fmt.Sprintf("%d", typed)
	case uint32:
		return fmt.Sprintf("%d", typed)
	case uint64:
		return fmt.Sprintf("%d", typed)
	case float32:
		return fmt.Sprintf("%v", typed)
	case float64:
		return fmt.Sprintf("%v", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", value)
	}
}

func toStringSliceLoose(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(toStringLoose(item)); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		if strings.Contains(trimmed, ",") {
			parts := strings.Split(trimmed, ",")
			result := make([]string, 0, len(parts))
			for _, part := range parts {
				if item := strings.TrimSpace(part); item != "" {
					result = append(result, item)
				}
			}
			return result
		}
		return []string{trimmed}
	default:
		if trimmed := strings.TrimSpace(toStringLoose(value)); trimmed != "" {
			return []string{trimmed}
		}
		return nil
	}
}
