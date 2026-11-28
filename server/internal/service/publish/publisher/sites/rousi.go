package sites

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/netproxy"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
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
//   - 失败：problem+json {code, title, detail, request_id}；5xx 多为瞬时故障，
//     官方提示「请稍后重试」，这里自动重试（沿用同一 Idempotency-Key），失败时把 request_id 带进报错
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
	reRousiRawImageURL      = regexp.MustCompile(`(?i)https?://[^\s\[\]<>"']+\.(?:png|jpe?g|gif|webp)(?:\?[^\s\[\]<>"']*)?`)
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
	// 截图往往是几 MB 的外链大图（如 pixhost 的 4K PNG），串行下载在慢线路上会一路超时，
	// 表现成「只传了封面、截图全被跳过」，因此并发预取并放宽单张超时。
	rousiImageFetchConcurrency = 4
	rousiImageDownloadTimeout  = 180 * time.Second
	rousiImageDownloadMaxBytes = 32 * 1024 * 1024
	// 站点只要求「至少一张截图」，但慢图床（pixhost 整站 ~50 KB/s、单张 2-3 MB）会把整站转种
	// 拖到几分钟。除封面外只抓 3 张候选：够一张就能过审，多抓的也用不上。
	rousiScreenshotCandidateLimit = 3
	// 截图整段预算：到点且手里已有可用图片就不再等剩下的慢图床（已拿到的照常压缩上传）。
	// 留下的时间给压缩与提交，避免一侧图床卡死把整站发布顶到超时。
	rousiImageStageBudget = 200 * time.Second
	// 部分图床（如阿里云 ESA 加速的 img.seedvault.cn、境外的 pixhost.to）整段传输到几十 KB
	// 就被对端断流（Go 侧表现为 stream error INTERNAL_ERROR，curl 侧是 transfer closed），
	// 但对带 Range 的分片请求能正常返回 ⇒ 整段下载失败时改用 1 MiB 分片续传。
	rousiImageChunkSize     = 1 << 20
	rousiImageChunkTimeout  = 60 * time.Second
	rousiImageChunkAttempts = 5
	// 站点提交偶发 5xx（官方提示「请稍后重试」），自动重试两次；重试沿用同一 Idempotency-Key。
	rousiUploadMaxAttempts = 3
	// 5xx 后按 infohash 回查时，从提交前的公开最大 id 往后扫多少个 id。
	rousiConfirmScanSpan = 20
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
	// 站点把「未登录」的响应从 401 改成了 204 空响应（前端 session query 同样按 204 ⇒ 无会话处理），
	// 继续走 JSON 解析只会抛出「会话接口返回非 JSON: 」这种无从下手的错误，这里归一成 Cookie 失效。
	if resp.StatusCode == http.StatusNoContent || len(bytes.TrimSpace(body)) == 0 {
		return "", "", fmt.Errorf("会话 Cookie 无效或已过期（HTTP %d，站点未返回会话内容），请到浏览器重新复制 %s", resp.StatusCode, rousiSessionCookieName)
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

// rousiTorrentDetail 是详情接口 GET /api/v1/torrents/{id} 里回查需要的字段。
type rousiTorrentDetail struct {
	ID         int64  `json:"id"`
	InfoHashV1 string `json:"info_hash_v1"`
	State      string `json:"state"`
}

// rousiMaxPublicTorrentID 取公开列表里最大的种子 id，作为提交前后的 id 基线。
// 参数/返回：读取失败返回 0（调用方据此跳过回查）。
// 副作用：一次只读 GET。
func rousiMaxPublicTorrentID(client *http.Client, baseURL string, cookie string, userAgent string) int64 {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/v1/torrents?limit=20&offset=0", nil)
	if err != nil {
		return 0
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", baseURL+"/torrents")

	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return 0
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	payload := struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0
	}
	maxID := int64(0)
	for _, item := range payload.Items {
		if item.ID > maxID {
			maxID = item.ID
		}
	}
	return maxID
}

// rousiFetchTorrentDetail 读取单个种子详情（含 info_hash_v1）。
// 参数/返回：非 2xx 或非 JSON 时 ok=false。
// 副作用：一次只读 GET。
func rousiFetchTorrentDetail(client *http.Client, baseURL string, cookie string, userAgent string, id int64) (rousiTorrentDetail, bool) {
	url := fmt.Sprintf("%s/api/v1/torrents/%d", baseURL, id)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return rousiTorrentDetail{}, false
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", url)

	resp, err := client.Do(req)
	if err != nil {
		return rousiTorrentDetail{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return rousiTorrentDetail{}, false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 128*1024))
	detail := rousiTorrentDetail{}
	if err := json.Unmarshal(body, &detail); err != nil {
		return rousiTorrentDetail{}, false
	}
	return detail, true
}

// rousiConfirmUploaded 在站点返回 5xx 后按 infohash 回查「是否其实已经落库」。
// 站点在部分场景会「写库成功但响应 500」，此时用本次提交的 infohash 反查最近的种子即可确认。
//
// 参数/返回：baselineID 为提交前取到的公开最大 id；返回种子 id、状态与是否确认。
// 失败场景：找不到（说明确实未落库）或接口不可用，返回 false 由调用方继续重试。
// 副作用：最多发起 rousiConfirmScanSpan+4 次只读 GET。
func rousiConfirmUploaded(client *http.Client, baseURL string, cookie string, userAgent string, infoHash string, baselineID int64) (int64, string, bool) {
	infoHash = strings.ToLower(strings.TrimSpace(infoHash))
	if infoHash == "" || baselineID <= 0 {
		return 0, "", false
	}
	probe := &http.Client{Timeout: 10 * time.Second}
	// 公开列表可能略有滞后，向后多探几个 id；新分配的 id 一定在 baseline 之后。
	startID := baselineID - 3
	if startID < 1 {
		startID = 1
	}
	for id := startID; id <= baselineID+rousiConfirmScanSpan; id++ {
		if id == baselineID {
			// 基线本身是提交前就存在的种子，跳过以省一次请求
			continue
		}
		detail, ok := rousiFetchTorrentDetail(probe, baseURL, cookie, userAgent, id)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(detail.InfoHashV1), infoHash) {
			return detail.ID, detail.State, true
		}
	}
	return 0, "", false
}

// tryUploadTorrentRousiPeerGo 走 PeerGo 契约完成一次发种。// 参数/返回：见 PublishRousi；返回详情页 URL、是否“已存在”、过程日志与错误。
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

	// 让肉丝种子与源站种子成为「两个种子」：在 info 字典内写入 source 标记，
	// infohash 随之改变（下载器里独立成任务），文件列表与 piece 数据保持不动。
	if rewritten, note := rousiIsolateInfoHash(torrentFile, siteCfg); note != "" {
		torrentFile = rewritten
		detailLines = append(detailLines, note)
	}

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

	bodyBytes := body.Bytes()
	// 提交摘要：站点报 5xx 时用于对照 request_id 自查（不打印正文，避免日志爆炸）。
	detailLines = append(detailLines, fmt.Sprintf(
		"提交摘要: category_id=%s facets=%d screenshots=%d torrent_bytes=%d description_chars=%d media_info_chars=%d",
		categoryID, len(selections), len(screenshots), len(torrentFile), len([]rune(resolvedDescription)), len([]rune(resolvedMediaInfo)),
	))

	// 站点 5xx 多为「上游/图片处理瞬时失败」，官方提示就是「请稍后重试」，
	// 这里自动重试几次；重试沿用同一个 Idempotency-Key，站点若已落库会返回同一结果，不会重复发种。
	idempotencyKey := rousiNewUUID()
	// 本站在多次 500 场景下其实是「写库成功、响应报错」，因此 5xx 后按 infohash 回查确认。
	ourInfoHash, _ := acquirefetch.TorrentInfoHash(torrentFile)
	// 提交前的公开最大 id：5xx 回查时以它为基线扫描新分配的 id。
	baselineTorrentID := rousiMaxPublicTorrentID(&http.Client{Timeout: 10 * time.Second}, baseURL, cookie, userAgent)
	var (
		respBody       []byte
		requestID      string
		responseDetail string
		statusCode     int
		statusText     string
		confirmedID    int64
		confirmedState string
	)
	problem := rousiProblemBody{}

	for attempt := 1; attempt <= rousiUploadMaxAttempts; attempt++ {
		req, errReq := http.NewRequest(http.MethodPost, baseURL+"/api/v1/torrents", bytes.NewReader(bodyBytes))
		if errReq != nil {
			detailLines = append(detailLines, fmt.Sprintf("创建 HTTP 请求失败: %v", errReq))
			return "", false, buildDetail(), errReq
		}
		req.Header.Set("Content-Type", contentType)
		req.Header.Set("Accept", "application/json, application/problem+json")
		req.Header.Set("Cookie", cookie)
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Origin", baseURL)
		req.Header.Set("Referer", baseURL+"/upload")
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", idempotencyKey)

		resp, errDo := client.Do(req)
		if errDo != nil {
			detailLines = append(detailLines, fmt.Sprintf("第 %d 次提交请求失败: %v", attempt, errDo))
			if attempt < rousiUploadMaxAttempts {
				time.Sleep(rousiUploadRetryDelay(attempt))
				continue
			}
			return "", false, buildDetail(), errDo
		}

		respBody, _ = io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		resp.Body.Close()
		statusCode = resp.StatusCode
		statusText = strings.TrimSpace(resp.Status)
		bodyText := string(respBody)
		responseDetail = summarizeRousiResponseBody(bodyText)
		problem = parseRousiProblem(bodyText)
		requestID = rousiExtractRequestID(problem.RequestID, bodyText)

		label := "响应状态"
		if attempt > 1 {
			label = fmt.Sprintf("第 %d 次提交响应状态", attempt)
		}
		detailLines = append(detailLines, fmt.Sprintf("%s: %s", label, statusText))
		if responseDetail != "" {
			detailLines = append(detailLines, fmt.Sprintf("站点响应: %s", responseDetail))
		}

		if statusCode >= 200 && statusCode < 300 {
			break
		}
		if statusCode >= 500 && (attempt == 1 || attempt == rousiUploadMaxAttempts) {
			// 站点在这个场景下经常「已写库但响应 500」：按 infohash 回查一次，命中就直接按成功返回。
			// 只在首次和末次尝试后回查，避免每轮都扫一遍 id 段。
			if id, state, ok := rousiConfirmUploaded(client, baseURL, cookie, userAgent, ourInfoHash, baselineTorrentID); ok {
				confirmedID, confirmedState = id, state
				detailLines = append(detailLines, fmt.Sprintf(
					"站点返回 %d，但已回查确认种子落库: %s/torrents/%d（state=%s）", statusCode, baseURL, id, state))
				break
			}
		}
		if statusCode >= 500 && attempt < rousiUploadMaxAttempts {
			delay := rousiUploadRetryDelay(attempt)
			detailLines = append(detailLines, fmt.Sprintf(
				"站点返回 %d，%s 后自动重试（沿用同一 Idempotency-Key=%s）", statusCode, delay, idempotencyKey))
			time.Sleep(delay)
			continue
		}
		break
	}

	bodyText := string(respBody)
	isExisting := looksLikeExistingTorrentForRousi(bodyText)

	if confirmedID <= 0 && (statusCode < 200 || statusCode >= 300) {
		message := rousiProblemMessage(problem, statusCode)
		if problem.Code != "" {
			isExisting = isExisting || rousiLooksLikeDuplicateCode(problem.Code)
		}
		if requestID != "" {
			message += fmt.Sprintf("（request_id=%s，可在向站点反馈时提供）", requestID)
		}
		err := fmt.Errorf("HTTP %d 上传失败: %s", statusCode, message)
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		if isExisting {
			// 待审核的种子不会出现在公开搜索/列表里，只有站点的「我的发布」能看到。
			detailLines = append(detailLines, "站点判定种子已存在：本次提交可能已落库（含待审核），请到站点「我的发布」核对详情页")
		} else if statusCode >= 500 {
			detailLines = append(detailLines, "站点内部错误：该次提交有可能已落库但响应失败，重发前建议先到站点「我的发布」核对，避免重复投稿")
		}
		return "", isExisting, buildDetail(), err
	}

	result := rousiSubmitResult{}
	if confirmedID > 0 {
		// 站点响应 500 但已回查确认落库：用查到的详情拼装结果，按成功处理。
		result = rousiSubmitResult{ID: confirmedID, InfoHashV1: ourInfoHash, State: confirmedState}
	} else if err := json.Unmarshal(respBody, &result); err != nil || result.ID <= 0 {
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
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	RequestID string `json:"request_id"`
}

// rousiUploadRetryDelay 返回第 attempt 次失败后的等待时间（3s、8s）。
func rousiUploadRetryDelay(attempt int) time.Duration {
	if attempt <= 1 {
		return 3 * time.Second
	}
	return 8 * time.Second
}

// rousiExtractRequestID 取站点返回的 request_id：优先 problem+json 的字段，
// 其次在响应体里找任意 UUID（站点的反馈通道只认这个 id）。
func rousiExtractRequestID(fromProblem string, body string) string {
	if id := strings.TrimSpace(fromProblem); id != "" {
		return id
	}
	return extractUUID(body)
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

// rousiIsolateInfoHash 按站点配置 info_source_tag 改写种子 info 字典（写入 source），
// 让上传到肉丝的种子拥有与源站不同的 infohash —— 两个站就是两个独立任务，
// 下载器里的站点标签、分类、上传量统计互不覆盖。
//
// 只动 info 里的普通键，pieces/files 原始字节不变：下载器指向同一份文件即可直接校验做种，不用重新下载。
// 参数/返回：torrentFile 为源站种子字节；siteCfg 为站点发布配置（info_source_tag 为空则原样返回），
// 返回（可能被改写的）种子字节与一行日志说明。
// 失败场景：种子结构非法时保持源站字节不变，日志里给出原因（不阻断发布）。
// 副作用：无。
func rousiIsolateInfoHash(torrentFile []byte, siteCfg *publishmapping.SitePublishConfig) ([]byte, string) {
	tag := ""
	if siteCfg != nil {
		tag = strings.TrimSpace(siteCfg.InfoSourceTag)
	}
	return isolateInfoHashByTag(torrentFile, tag)
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
	allSources := flattenRousiImageSources(grouped)
	if len(allSources) == 0 {
		return nil, nil, "截图处理: 未找到可用图片来源", nil
	}
	coverCount := 0
	if len(grouped.Cover) > 0 {
		coverCount = 1
	}
	// 封面 + 前 3 张截图：站点只要一张就够，候选多抓的只会把等待时间拉长。
	sources := limitRousiImageCandidates(allSources, coverCount)
	candidateNote := ""
	if len(sources) < len(allSources) {
		candidateNote = fmt.Sprintf("；候选 %d/%d 张（除封面外最多 %d 张）",
			len(sources), len(allSources), rousiScreenshotCandidateLimit)
	}
	// 图床按来源防盗链时，Referer 要像「从真实页面点开」：优先用简介里的包裹页，
	// 其次是源站详情页（由种子文件名反推），最后才退回图片自身域名。见 rousiRefererCandidates。
	sourceReferer := rousiSourceRefererForTorrent(torrentPath)

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
	failures := make([]string, 0, 4)

	// 先把候选并发落到本地，再按原顺序压缩：
	// 截图常是几 MB 的外链大图（pixhost 的 4K PNG 就有 7MB+），串行下载会逐个超时，
	// 最终表现成「只上传了封面，截图全被跳过」。
	fetched, budgetNote := fetchRousiImagesConcurrently(context.Background(), baseURL, cookie, userAgent, workDir, sources, sourceReferer)
	if budgetNote != "" {
		candidateNote += "；" + budgetNote
	}

	for index, item := range sources {
		if len(shots) >= rousiScreenshotMaxCount {
			break
		}
		sourceURL := strings.TrimSpace(item.URL)
		if sourceURL == "" {
			continue
		}

		var fetchErr error
		if index >= len(fetched) {
			fetchErr = errors.New("预取结果缺失")
		} else {
			fetchErr = fetched[index].err
		}
		if fetchErr != nil {
			skipped++
			failures = append(failures, describeRousiImageFailure(index, sourceURL, fetchErr))
			continue
		}

		jpegPath := filepath.Join(workDir, fmt.Sprintf("%02d.final.jpg", index))
		data, ok := rousiPrepareScreenshotFile(fetched[index].path, jpegPath, rousiScreenshotMaxBytes)
		if !ok {
			skipped++
			failures = append(failures, fmt.Sprintf("#%d 压缩未达标（原图 >2 MiB）", index+1))
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
	summary += candidateNote
	if len(failures) > 0 {
		shown := failures
		suffix := ""
		if len(shown) > 3 {
			shown = shown[:3]
			suffix = fmt.Sprintf(" 等 %d 张", len(failures))
		}
		summary += "；失败: " + strings.Join(shown, " / ") + suffix
	}
	if len(shots) > 0 && len(grouped.Screenshots) == 0 {
		summary += "；警告：简介里没有截图，本次只上传封面"
	}
	return shots, cleanup, summary, nil
}

// rousiFetchedImage 是并发预取的单张结果：path 为本地原图路径，err 为失败原因。
type rousiFetchedImage struct {
	path string
	err  error
}

// limitRousiImageCandidates 把候选裁到「封面 + 前 N 张截图」。
// 参数/返回：sources 为按上传顺序（封面在前）排好的图片；coverCount 为其中封面的数量（0/1）；
// 返回截断后的切片（不复制原切片元素，仅调整长度）。
// 说明：站点只要求至少一张截图，候选多抓的只会让最慢那台图床把整站发布拖到超时。
// 副作用：无。
func limitRousiImageCandidates(sources []rousiImageSource, coverCount int) []rousiImageSource {
	if coverCount < 0 {
		coverCount = 0
	}
	if coverCount > len(sources) {
		coverCount = len(sources)
	}
	limit := coverCount + rousiScreenshotCandidateLimit
	if len(sources) <= limit {
		return sources
	}
	return sources[:limit]
}

// fetchRousiImagesConcurrently 把候选图片来源并发落到 workDir，并在整段预算用尽时停止等待。
// 参数/返回：ctx 为上游取消信号（可为 context.Background()），baseURL/cookie/userAgent 用于外链下载鉴权，
// workDir 为本次发布的工作目录，sources 为待处理图片（含 data URL 与外链），
// sourceReferer 为源站详情页地址（图床防盗链时作 Referer 候选）；
// 返回与 sources 等长、下标一一对应的结果，以及一句「预算用尽」的说明（未触发时为空串）。
// 失败场景：单张失败只影响该下标（err 非空），不影响其它图片。
// 副作用：写本地临时文件并发起网络请求；预算用尽会取消在飞请求（已下载完成的文件保留）。
func fetchRousiImagesConcurrently(ctx context.Context, baseURL string, cookie string, userAgent string, workDir string, sources []rousiImageSource, sourceReferer string) ([]rousiFetchedImage, string) {
	result := make([]rousiFetchedImage, len(sources))
	if len(sources) == 0 {
		return result, ""
	}

	stageCtx, stageCancel := context.WithTimeout(ctx, rousiImageStageBudget)
	defer stageCancel()

	sem := make(chan struct{}, rousiImageFetchConcurrency)
	var wg sync.WaitGroup

	for index, item := range sources {
		sourceURL := strings.TrimSpace(item.URL)
		if sourceURL == "" {
			result[index] = rousiFetchedImage{err: errors.New("图片地址为空")}
			continue
		}

		if strings.HasPrefix(sourceURL, "data:image/") {
			_, ext, raw, ok := parseRousiDataURL(sourceURL)
			if !ok {
				result[index] = rousiFetchedImage{err: errors.New("data URL 解析失败")}
				continue
			}
			originalPath := filepath.Join(workDir, fmt.Sprintf("%02d.original.%s", index, ext))
			if err := os.WriteFile(originalPath, raw, 0o644); err != nil {
				result[index] = rousiFetchedImage{err: err}
				continue
			}
			result[index] = rousiFetchedImage{path: originalPath}
			continue
		}

		if !strings.HasPrefix(sourceURL, "http://") && !strings.HasPrefix(sourceURL, "https://") {
			result[index] = rousiFetchedImage{err: fmt.Errorf("不支持的图片地址: %s", sourceURL)}
			continue
		}

		wg.Add(1)
		go func(idx int, url string, referer string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-stageCtx.Done():
				result[idx] = rousiFetchedImage{err: stageCtx.Err()}
				return
			}
			defer func() { <-sem }()

			raw, err := downloadRousiImageWithReferers(stageCtx, baseURL, cookie, userAgent, url, referer, sourceReferer)
			if err != nil {
				result[idx] = rousiFetchedImage{err: err}
				return
			}
			if len(raw) == 0 {
				result[idx] = rousiFetchedImage{err: errors.New("图片内容为空")}
				return
			}
			originalPath := filepath.Join(workDir, fmt.Sprintf("%02d.downloaded", idx))
			if err := os.WriteFile(originalPath, raw, 0o644); err != nil {
				result[idx] = rousiFetchedImage{err: err}
				return
			}
			result[idx] = rousiFetchedImage{path: originalPath}
		}(index, sourceURL, strings.TrimSpace(item.Referer))
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return result, ""
	case <-stageCtx.Done():
		// 预算到点：取消在飞请求后收工，手里已下载完的照常参与压缩上传。
		stageCancel()
		<-done
		return result, fmt.Sprintf("预取达到 %d 秒预算，未完成的请求已放弃（已拿到的照常上传）",
			int(rousiImageStageBudget.Seconds()))
	}
}

// describeRousiImageFailure 生成「#序号(域名): 原因」形式的失败摘要，供发布日志排查。
// 参数/返回：index 为图片下标，sourceURL 为原始地址，err 为失败原因；返回单行摘要。
// 失败场景：不适用。
// 副作用：无。
func describeRousiImageFailure(index int, sourceURL string, err error) string {
	host := "本地"
	if parsed, parseErr := neturl.Parse(strings.TrimSpace(sourceURL)); parseErr == nil && parsed.Host != "" {
		host = parsed.Host
	}
	reason := "未知原因"
	if err != nil {
		reason = strings.TrimSpace(err.Error())
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		reason = "截图预取预算用尽，已放弃"
	}
	if reason == "" {
		reason = "未知原因"
	}
	if len(reason) > 90 {
		reason = reason[:90] + "…"
	}
	return fmt.Sprintf("#%d(%s): %s", index+1, host, reason)
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

var (
	rousiImageClientOnce sync.Once
	rousiImageClient     *http.Client
)

// rousiImageHTTPClient 返回图片下载专用客户端（强制 HTTP/1.1）。
// 参数/返回：无入参；返回惰性初始化的共享客户端。
// 失败场景：不适用。
// 副作用：首次调用时创建 transport 并接入全局网络代理配置。
//
// 说明：img.seedvault.cn（阿里云 ESA）等图床的 HTTP/2 不回 WINDOW_UPDATE，
// 流在 64 KiB（h2 默认流控窗口）处被对端 RST，Go 侧表现为
// `stream error: stream ID n; INTERNAL_ERROR; received from peer`；
// 同一地址在 HTTP/1.1 下（配合 Range 分片）能正常返回，因此图片下载固定走 h1.1。
func rousiImageHTTPClient() *http.Client {
	rousiImageClientOnce.Do(func() {
		transport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     false,
			TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}},
			TLSHandshakeTimeout:   15 * time.Second,
			MaxIdleConns:          8,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		rousiImageClient = &http.Client{Transport: netproxy.ConfigureTransport(transport)}
	})
	return rousiImageClient
}

// downloadRousiImageToBytes 下载截图（同域请求自动带会话 Cookie）。
// 说明：Referer 依次尝试「简介包裹页 → 源站详情页 → 图片自身域名 → 不发」，见 rousiRefererCandidates；
// 每次都先整段下载，被对端断流时回退 Range 分片续传。
// 副作用：发起 HTTP GET。
func downloadRousiImageToBytes(baseURL string, cookie string, userAgent string, targetURL string, referer string) ([]byte, error) {
	return downloadRousiImageWithReferers(context.Background(), baseURL, cookie, userAgent, targetURL, referer, "")
}

// downloadRousiImageWithReferers 按 Referer 候选逐个尝试下载图片。
// 参数/返回：ctx 为整段截图预算的取消信号；referer 为简介里的包裹页地址（可空）；
// sourceReferer 为源站详情页地址（可空）；返回完整图片字节。
// 失败场景：候选全部失败时返回最后一次错误。只有 401/403/451 这类「来源被拒」才换下一个候选，
// 404/5xx/超时说明换 Referer 也没用，直接返回。
// 副作用：最多发起 len(候选) 轮请求。
func downloadRousiImageWithReferers(ctx context.Context, baseURL string, cookie string, userAgent string, targetURL string, referer string, sourceReferer string) ([]byte, error) {
	candidates := rousiRefererCandidates(targetURL, referer, sourceReferer)
	var lastErr error
	for _, chosen := range candidates {
		data, err := downloadRousiImageOnce(ctx, baseURL, cookie, userAgent, targetURL, chosen)
		if err == nil {
			return data, nil
		}
		lastErr = err
		if !isRousiRefererRejected(err) {
			return nil, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("图片下载失败")
	}
	return nil, lastErr
}

// rousiRefererCandidates 生成 Referer 尝试顺序（去重；空串表示「不发 Referer」）。
// 参数/返回：targetURL 为图片地址；wrapper 为简介里 [url=X][img]Y[/img][/url] 的包裹页；
// sourceReferer 为源站详情页地址；返回按优先级排好的候选。
// 说明：图床按来源防盗链时，最像「真从页面点开」的是包裹页；源站详情页只在该图确实挂在源站域名下才有意义；
// 图片自身域名根是实测能过的兜底；最后留一档不发 Referer（部分 CDN 反而只放行空来源）。
// 副作用：无。
func rousiRefererCandidates(targetURL string, wrapper string, sourceReferer string) []string {
	candidates := make([]string, 0, 4)
	appendURL := func(value string) {
		trimmed := strings.TrimSpace(value)
		if !(strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://")) {
			return
		}
		for _, existing := range candidates {
			if existing == trimmed {
				return
			}
		}
		candidates = append(candidates, trimmed)
	}

	appendURL(wrapper)
	if rousiSameHost(sourceReferer, targetURL) {
		appendURL(sourceReferer)
	}
	appendURL(defaultRousiRefererForURL(targetURL))
	// 最后一档不发 Referer：部分 CDN 只放行空来源，只有前面都被 401/403 拒绝才会走到这里。
	candidates = append(candidates, "")
	return candidates
}

// rousiSameHost 判断页面地址与图片地址是否同域（允许一方是另一方的子域）。
// 参数/返回：pageURL 为页面地址，targetURL 为图片地址；任一解析失败返回 false。
// 副作用：无。
func rousiSameHost(pageURL string, targetURL string) bool {
	pageHost := rousiURLHost(pageURL)
	targetHost := rousiURLHost(targetURL)
	if pageHost == "" || targetHost == "" {
		return false
	}
	return pageHost == targetHost ||
		strings.HasSuffix(pageHost, "."+targetHost) || strings.HasSuffix(targetHost, "."+pageHost)
}

// rousiURLHost 取 URL 的小写主机名（含端口），解析失败返回空串。
// 副作用：无。
func rousiURLHost(value string) string {
	parsed, err := neturl.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return ""
	}
	return strings.ToLower(parsed.Host)
}

// rousiImageStatusError 是图床返回明确 HTTP 状态码的错误。
// 单列类型是为了区分「该换 Referer 重试」（401/403/451）与其它失败（404/超时/断流）。
type rousiImageStatusError struct {
	Code    int
	Preview string
}

func (e *rousiImageStatusError) Error() string {
	if e.Preview == "" {
		return fmt.Sprintf("HTTP %d", e.Code)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Code, e.Preview)
}

// isRousiRefererRejected 判断失败是否可能由 Referer 引起（401/403/451），可换下一个候选重试。
// 副作用：无。
func isRousiRefererRejected(err error) bool {
	var statusErr *rousiImageStatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusUnavailableForLegalReasons:
			return true
		}
	}
	return false
}

// rousiSourceRefererForTorrent 由种子文件名反推源站详情页地址，作为图床防盗链时的 Referer 候选。
// 参数/返回：torrentPath 形如 <site>-<torrentID>-<timestamp>.torrent（上传时按此约定命名）；
// 站点域名取自 sites_data.json 的 base_url；解析不出站点或域名时返回空串。
// 说明：发布 payload 里不带源站详情页链接，只能按文件名约定反推；
// 路径按 NexusPHP 惯例给 /details.php?id=，图床只校验 Referer 的 host，路径不参与判定。
// 副作用：读一次 sites_data.json。
func rousiSourceRefererForTorrent(torrentPath string) string {
	base := strings.TrimSpace(filepath.Base(strings.TrimSpace(torrentPath)))
	match := reRousiTorrentFilename.FindStringSubmatch(base)
	if len(match) < 3 {
		return ""
	}
	siteCode := strings.TrimSpace(match[1])
	torrentID := strings.TrimSpace(match[2])
	if siteCode == "" || torrentID == "" {
		return ""
	}
	host := strings.TrimSpace(config.SiteBaseURL(siteCode))
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	if host == "" {
		return ""
	}
	return "https://" + host + "/details.php?id=" + torrentID
}

// downloadRousiImageOnce 用指定 Referer 下载一次图片：整段优先，被断流时回退 Range 分片续传。
// 参数/返回：referer 为空表示不发 Referer 头；返回完整图片字节。
// 失败场景：4xx 返回 *rousiImageStatusError（供上层判断是否换 Referer），其它返回原始错误。
// 副作用：发起 HTTP 请求；ctx 取消时立即中止。
func downloadRousiImageOnce(ctx context.Context, baseURL string, cookie string, userAgent string, targetURL string, referer string) ([]byte, error) {
	buildRequest := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
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
		if chosenReferer != "" {
			req.Header.Set("Referer", chosenReferer)
		}

		// 站点自身的私有截图（如站点附件）需要会话 Cookie，外链图床不需要也不该带。
		if shouldAttachRousiCookie(baseURL, targetURL) && strings.TrimSpace(cookie) != "" {
			req.Header.Set("Cookie", cookie)
		}
		return req, nil
	}

	// 快路径：一次整段下载，应付正常图床。
	req, err := buildRequest()
	if err != nil {
		return nil, err
	}
	fastCtx, fastCancel := context.WithTimeout(ctx, rousiImageDownloadTimeout)
	resp, err := rousiImageHTTPClient().Do(req.WithContext(fastCtx))
	if err == nil {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, rousiImageDownloadMaxBytes))
		statusCode := resp.StatusCode
		resp.Body.Close()
		fastCancel()
		if statusCode >= 400 && statusCode < 500 {
			preview := body
			if len(preview) > 1024 {
				preview = preview[:1024]
			}
			return nil, &rousiImageStatusError{Code: statusCode, Preview: strings.TrimSpace(string(preview))}
		}
		if statusCode >= 200 && statusCode < 300 && readErr == nil && len(body) > 0 {
			return body, nil
		}
		if statusCode < 200 || statusCode >= 300 {
			err = fmt.Errorf("HTTP %d", statusCode)
		} else if readErr != nil {
			err = readErr
		}
	} else {
		fastCancel()
		// 预取预算到点（上游取消）就别再走分片续传，直接把取消原因抛回去。
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}

	// 慢路径：分片续传。整段被提前断流时，Range 单片仍可正常返回。
	data, rangeErr := downloadRousiImageByRange(buildRequest, targetURL)
	if rangeErr == nil {
		return data, nil
	}
	return nil, fmt.Errorf("整段下载失败(%v)；分片续传失败(%v)", err, rangeErr)
}

// downloadRousiImageByRange 用 Range 分片把图片拉全，单片失败重试若干次并从断点继续。
// 参数/返回：buildRequest 构造带鉴权头的请求；返回完整图片字节。
// 失败场景：站点不支持 Range（非 206）、单片重试耗尽、超过整体大小上限时返回 error。
// 副作用：按分片发起多次 HTTP GET。
func downloadRousiImageByRange(buildRequest func() (*http.Request, error), targetURL string) ([]byte, error) {
	var buffer bytes.Buffer
	total := int64(-1)
	maxChunks := int(rousiImageDownloadMaxBytes/rousiImageChunkSize) + 8

	for chunkIndex := 0; chunkIndex < maxChunks; chunkIndex++ {
		offset := int64(buffer.Len())
		if total >= 0 && offset >= total {
			break
		}
		if offset >= rousiImageDownloadMaxBytes {
			return nil, fmt.Errorf("图片超过下载上限 %d 字节", rousiImageDownloadMaxBytes)
		}

		end := offset + rousiImageChunkSize - 1
		if total > 0 && end >= total {
			end = total - 1
		}

		var (
			chunk     []byte
			chunkErr  error
			lastError error
		)
		for attempt := 0; attempt < rousiImageChunkAttempts; attempt++ {
			// 图床在连发请求时会间歇性断流，重试之间留一点间隔，避免连撞限流。
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * 300 * time.Millisecond)
			}
			req, err := buildRequest()
			if err != nil {
				return nil, err
			}
			// 预取预算到点就立刻收手，别在取消之后再空转重试。
			if reqErr := req.Context().Err(); reqErr != nil {
				return nil, reqErr
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))

			client := rousiImageHTTPClient()
			chunkCtx, chunkCancel := context.WithTimeout(req.Context(), rousiImageChunkTimeout)
			resp, err := client.Do(req.WithContext(chunkCtx))
			if err != nil {
				chunkCancel()
				lastError = err
				continue
			}
			body, readErr := io.ReadAll(io.LimitReader(resp.Body, end-offset+1))
			statusCode := resp.StatusCode
			contentRange := resp.Header.Get("Content-Range")
			resp.Body.Close()
			chunkCancel()

			if statusCode != http.StatusPartialContent {
				// 服务器忽略 Range（回 200）或越界（416）：换分片也没意义，直接退出。
				if statusCode == http.StatusOK {
					return nil, fmt.Errorf("图床不支持 Range 分片下载: HTTP %d %s", statusCode, targetURL)
				}
				return nil, fmt.Errorf("分片下载失败: HTTP %d %s", statusCode, targetURL)
			}
			if total < 0 {
				if parsed := parseRousiContentRangeTotal(contentRange); parsed > 0 {
					total = parsed
				}
			}
			if len(body) > 0 {
				chunk = body
				chunkErr = nil
				break
			}
			chunkErr = readErr
			if chunkErr == nil {
				chunkErr = errors.New("分片返回空数据")
			}
			lastError = chunkErr
		}

		if len(chunk) == 0 {
			if lastError != nil {
				return nil, fmt.Errorf("偏移 %d 处取分片失败: %v", offset, lastError)
			}
			return nil, fmt.Errorf("偏移 %d 处取分片失败", offset)
		}
		buffer.Write(chunk)

		// 没有 Content-Range 总长时，用「取到的字节数不足一片」判断已到结尾。
		if total < 0 && int64(len(chunk)) < end-offset+1 {
			break
		}
	}

	if buffer.Len() == 0 {
		return nil, errors.New("分片下载未取到任何数据")
	}
	return buffer.Bytes(), nil
}

// parseRousiContentRangeTotal 从 Content-Range 头（bytes 0-1048575/3955182）解析总长度。
// 参数/返回：header 为响应头原文；解析不出时返回 -1。
// 失败场景：格式不符（如 bytes 0-1023/*）时返回 -1。
// 副作用：无。
func parseRousiContentRangeTotal(header string) int64 {
	value := strings.TrimSpace(header)
	if value == "" {
		return -1
	}
	slash := strings.LastIndex(value, "/")
	if slash < 0 || slash == len(value)-1 {
		return -1
	}
	total, err := strconv.ParseInt(strings.TrimSpace(value[slash+1:]), 10, 64)
	if err != nil || total <= 0 {
		return -1
	}
	return total
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
	// 纯文本 URL（简介里常常一行一个、没有 [img] 包裹）也要收，否则会漏掉全部截图。
	for _, raw := range reRousiRawImageURL.FindAllString(source, -1) {
		if imgURL := strings.TrimSpace(raw); imgURL != "" {
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
	posterText := strings.TrimSpace(toStringLoose(intro["poster"]))
	if posterText == "" {
		posterText = strings.TrimSpace(toStringLoose(uploadData["poster"]))
	}
	for _, item := range extractRousiImageSourcesFromText(posterText) {
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
	// 截图正文既可能在 uploadData.screenshots，也可能在 intro.screenshots，统一用同一套语义取。
	for _, item := range extractRousiImageSourcesFromText(resolveUploadSection(uploadData, "screenshots")) {
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
