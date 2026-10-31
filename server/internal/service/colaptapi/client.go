package colaptapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultJSONTimeout 是读接口的单次超时。
	defaultJSONTimeout = 45 * time.Second
	// defaultDownloadTimeout 是下载 .torrent 的单次超时。
	defaultDownloadTimeout = 90 * time.Second
	// maxJSONBody 限制读接口响应体大小，避免异常站点把内存打满。
	maxJSONBody = 8 << 20
	// maxTorrentBody 限制 .torrent 响应体大小。
	maxTorrentBody = 32 << 20
)

// APIError 表示站点返回的业务错误（非传输错误）。
// 说明：可乐站错误体统一为 {"error":{"code","message","fields","request_id"}}。
type APIError struct {
	Stage     string
	Status    int
	Code      string
	Message   string
	RequestID string
}

// Error 输出可直接给人看的错误信息。
func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	parts := []string{fmt.Sprintf("可乐站%s失败：HTTP %d", strings.TrimSpace(e.Stage), e.Status)}
	if e.Code != "" {
		parts = append(parts, "code="+e.Code)
	}
	if e.Message != "" {
		parts = append(parts, "message="+e.Message)
	}
	if e.RequestID != "" {
		parts = append(parts, "request_id="+e.RequestID)
	}
	return strings.Join(parts, " ")
}

// Torrent 表示站点种子详情（对应契约 TorrentDetail）。
// 字段说明：source 是站点口径的「片源类型」（等价于其它站的媒介），不是产地。
type Torrent struct {
	ID int64 `json:"id"`
	// MediaID 是站点的作品 ID，形如 "rc17"（带前缀的字符串，不是纯数字）。
	MediaID              string         `json:"media_id"`
	ReleaseName          string         `json:"release_name"`
	Subtitle             string         `json:"subtitle"`
	ReleaseGroup         string         `json:"release_group"`
	Description          string         `json:"description"`
	MediaInfo            string         `json:"media_info"`
	Screenshots          []string       `json:"screenshots"`
	Tags                 []string       `json:"tags"`
	SizeBytes            string         `json:"size_bytes"`
	InfoHash             string         `json:"info_hash"`
	Resolution           string         `json:"resolution"`
	Source               string         `json:"source"`
	VideoCodec           string         `json:"video_codec"`
	HDR                  string         `json:"hdr"`
	Audio                []string       `json:"audio"`
	Subtitles            []string       `json:"subtitles"`
	ReleaseSeasons       string         `json:"release_seasons"`
	ReleaseScope         map[string]any `json:"release_scope"`
	Status               string         `json:"status"`
	PublicationStage     string         `json:"publication_stage"`
	DownloadAllowed      bool           `json:"download_allowed"`
	DownloadDeniedReason string         `json:"download_denied_reason"`
	Anonymous            bool           `json:"anonymous"`
}

// SizeBytesValue 把站点以字符串返回的字节数转成整数，解析失败返回 0。
func (t Torrent) SizeBytesValue() int64 {
	trimmed := strings.TrimSpace(t.SizeBytes)
	if trimmed == "" {
		return 0
	}
	value, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

// ScopeCategory 返回发布范围里的作品分类（movie/tv/anime/variety/other），可能为空。
func (t Torrent) ScopeCategory() string {
	if t.ReleaseScope == nil {
		return ""
	}
	if value, ok := t.ReleaseScope["category"].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

// Media 表示站点作品信息（对应契约 Media）。
// 只保留抓取侧需要的字段：外链 ID、产地、海报、简介、分类。
type Media struct {
	MediaID       string   `json:"media_id"`
	Title         string   `json:"title"`
	OriginalTitle string   `json:"original_title"`
	Year          int      `json:"year"`
	Category      string   `json:"category"`
	Genres        []string `json:"genres"`
	Synopsis      string   `json:"synopsis"`
	PosterURL     string   `json:"poster_url"`
	DoubanID      string   `json:"douban_id"`
	IMDbID        string   `json:"imdb_id"`
	TMDbURL       string   `json:"tmdb_url"`
	Countries     []string `json:"countries"`
	Languages     []string `json:"languages"`
	Duration      string   `json:"duration"`
	DoubanRating  string   `json:"douban_rating"`
	IMDbRating    string   `json:"imdb_rating"`
}

// Client 是可乐PT 的 JSON API 客户端。
//
// 两种鉴权模式：
//   - API Key 模式：请求头 X-API-Key，走 /automation/* 一套（需要 Key 勾了 torrent.upload）。
//   - 会话模式：浏览器 Cookie，走网页版 /torrents/* 一套；写操作另外需要 X-CSRF-Token，
//     该令牌由 GET /me 的 session.csrf_token 下发。
type Client struct {
	APIBase      string
	APIKey       string
	APIKeyHeader string
	// Cookie 为会话模式下的浏览器 Cookie（"k=v; k2=v2"）。
	Cookie string
	// SiteOrigin 是会话模式写操作所需的 Origin 头（站点要求与请求方同源）。
	SiteOrigin string
	// HTTPClient 为底层客户端；为零值时按请求类型设默认超时。
	HTTPClient *http.Client

	// csrfToken 懒加载缓存，仅会话模式使用。
	csrfToken string
}

// AuthMode 返回当前鉴权模式："key" 或 "session"。
// 说明：两种模式互斥，API Key 优先（更稳定、不随浏览器退登失效）。
func (c *Client) AuthMode() string {
	if strings.TrimSpace(c.APIKey) != "" {
		return "key"
	}
	if strings.TrimSpace(c.Cookie) != "" {
		return "session"
	}
	return ""
}

// NewClient 按站点根地址与 API Key 构造客户端。
// 参数/返回：baseURL 可为空（回退官方地址）；apiKey 为空时返回 error，因为站点所有读接口都要鉴权。
// 失败场景：缺少 API Key。
// 副作用：无。
func NewClient(baseURL, apiKey string) (*Client, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return nil, fmt.Errorf("可乐站缺少 API Key：请在站点设置的 Passkey 栏填写站点生成的 API 密钥（需 torrent.upload 权限）")
	}
	base := APIBase(baseURL)
	return &Client{
		APIBase:      base,
		APIKey:       trimmedKey,
		APIKeyHeader: DefaultAPIKeyHeader,
		SiteOrigin:   strings.TrimSuffix(base, APIPrefix),
		HTTPClient:   &http.Client{Timeout: defaultJSONTimeout},
	}, nil
}

// NewSessionClient 用浏览器 Cookie 构造会话模式客户端。
// 参数/返回：baseURL 可为空；cookie 为站点登录态 Cookie，为空时返回 error。
// 失败场景：缺少 Cookie。
// 副作用：无。CSRF 令牌在首次写操作时通过 GET /me 懒加载。
func NewSessionClient(baseURL, cookie string) (*Client, error) {
	trimmedCookie := strings.TrimSpace(cookie)
	if trimmedCookie == "" {
		return nil, fmt.Errorf("可乐站缺少登录 Cookie：请在站点设置的 Cookie 栏粘贴浏览器里 /api/v1 请求的完整 Cookie")
	}
	base := APIBase(baseURL)
	return &Client{
		APIBase:    base,
		Cookie:     trimmedCookie,
		SiteOrigin: strings.TrimSuffix(base, APIPrefix),
		HTTPClient: &http.Client{Timeout: defaultJSONTimeout},
	}, nil
}

// NewClientWithAuth 按「Cookie 会话优先、API Key 兜底」构造只读客户端。
//
// 为什么 Cookie 优先：站点的 API Key 只覆盖 /automation/*（契约里 ApiKey.scopes 的枚举
// 只有 torrent.upload 与 mtorrent.compat，没有任何读权限），未开自动化授权的 Key
// 连 GET /automation/torrents/{id} 都会 403 api_key_scope_denied；
// 而浏览器 Cookie 走网页版 /torrents/* 一套，只要账号是站点成员就能读。
//
// 参数/返回：两者都为空时返回 error 并说明两种填法。
// 失败场景：缺少任何可用凭据。
// 副作用：无。
func NewClientWithAuth(baseURL, apiKey, cookie string) (*Client, error) {
	if strings.TrimSpace(cookie) != "" {
		return NewSessionClient(baseURL, cookie)
	}
	if strings.TrimSpace(apiKey) != "" {
		return NewClient(baseURL, apiKey)
	}
	return nil, fmt.Errorf("可乐站缺少凭据：请在站点 Cookie 栏粘贴浏览器登录后的 /api/v1 请求 Cookie（推荐），或在 Passkey 栏填站点 API 密钥")
}

// torrentPath 返回读种子详情的接口路径。
// 说明：Key 模式走 /automation/*，会话模式走网页版同义接口，两者返回同一个 TorrentDetail。
func (c *Client) torrentPath(torrentID string) string {
	if c.AuthMode() == "session" {
		return "/torrents/" + torrentID
	}
	return "/automation/torrents/" + torrentID
}

// mediaPath 返回读作品信息的接口路径。
// 说明：作品 ID 是带前缀的字符串（如 "rc17"），不能当整数处理。
func (c *Client) mediaPath(mediaID string) string {
	id := url.PathEscape(strings.TrimSpace(mediaID))
	if c.AuthMode() == "session" {
		return "/media/" + id
	}
	return "/automation/media/" + id
}

// Torrent 读取种子详情。
// 参数/返回：id 为种子 ID；返回详情结构。
// 失败场景：网络错误、鉴权失败、站点 4xx/5xx。
// 副作用：发起一次 HTTP 请求。
func (c *Client) Torrent(id string) (Torrent, error) {
	var torrent Torrent
	torrentID := ExtractTorrentID(id)
	if torrentID == "" {
		return torrent, fmt.Errorf("可乐站种子 ID 无效：%s", strings.TrimSpace(id))
	}
	body, err := c.getJSON(c.torrentPath(torrentID), "读取种子详情")
	if err != nil {
		return torrent, err
	}
	if err := json.Unmarshal(body, &torrent); err != nil {
		return torrent, fmt.Errorf("可乐站种子详情解析失败: %w", err)
	}
	if strings.TrimSpace(torrent.ReleaseName) == "" {
		return torrent, fmt.Errorf("可乐站种子详情缺少 release_name（种子可能已被删除或未通过审核）")
	}
	return torrent, nil
}

// Media 读取作品信息。
// 参数/返回：mediaID 为作品 ID（如 "rc17"）；返回作品结构。
// 失败场景：网络错误、鉴权失败、站点 4xx/5xx。
// 副作用：发起一次 HTTP 请求。
func (c *Client) Media(mediaID string) (Media, error) {
	var media Media
	trimmedID := strings.TrimSpace(mediaID)
	if trimmedID == "" {
		return media, fmt.Errorf("可乐站作品 ID 为空")
	}
	body, err := c.getJSON(c.mediaPath(trimmedID), "读取作品信息")
	if err != nil {
		return media, err
	}
	if err := json.Unmarshal(body, &media); err != nil {
		return media, fmt.Errorf("可乐站作品信息解析失败: %w", err)
	}
	if strings.TrimSpace(media.MediaID) == "" {
		media.MediaID = trimmedID
	}
	return media, nil
}

// IssueDownloadAuthorization 为指定种子申请下载授权。
// 参数/返回：id 为种子 ID；replacesID > 0 时表示顶掉该旧授权；返回授权 ID。
// 失败场景：网络错误、鉴权失败、站点拒绝下载（download_allowed=false 时站点返回 403）。
// 副作用：发起一次 HTTP 请求，站点侧会记录一次授权。
func (c *Client) IssueDownloadAuthorization(id string, replacesID int64) (int64, error) {
	torrentID := ExtractTorrentID(id)
	if torrentID == "" {
		return 0, fmt.Errorf("可乐站种子 ID 无效：%s", strings.TrimSpace(id))
	}
	payload := map[string]any{}
	if replacesID > 0 {
		payload["replaces_id"] = replacesID
	}
	body, err := c.issueAuthorization(torrentID, payload)
	if err != nil {
		return 0, err
	}
	var authorization struct {
		ID     int64 `json:"id"`
		Usable bool  `json:"usable"`
	}
	if err := json.Unmarshal(body, &authorization); err != nil {
		return 0, fmt.Errorf("可乐站下载授权解析失败: %w", err)
	}
	if authorization.ID <= 0 {
		return 0, fmt.Errorf("可乐站未返回下载授权 ID：%s", snippet(body))
	}
	return authorization.ID, nil
}

// issueAuthorization 按鉴权模式申请授权。
// 说明：Key 模式无需 CSRF；会话模式的写操作必须带 X-CSRF-Token、Origin 与 Idempotency-Key，
// CSRF 失效（退登/令牌轮换）时刷新一次再重试。
func (c *Client) issueAuthorization(torrentID string, payload map[string]any) ([]byte, error) {
	if c.AuthMode() != "session" {
		return c.doJSON(http.MethodPost, "/automation/torrents/"+torrentID+"/authorizations", payload, "申请下载授权")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("构造可乐站请求体失败: %w", err)
	}
	path := "/torrents/" + torrentID + "/authorizations"
	body, status, err := c.sessionPost(path, encoded)
	if err != nil {
		return nil, fmt.Errorf("可乐站申请下载授权失败: %w", err)
	}
	if status < 400 {
		return body, nil
	}
	// CSRF 令牌可能已过期（浏览器重登过），刷新后重试一次。
	if status == http.StatusForbidden {
		body, status, err = c.sessionPost(path, encoded)
		if err != nil {
			return nil, fmt.Errorf("可乐站申请下载授权失败: %w", err)
		}
		if status < 400 {
			return body, nil
		}
	}
	return nil, errorFromBody("申请下载授权", status, body)
}

// sessionPost 用会话凭据发出一次写请求（自动带上 CSRF 与幂等键）。
func (c *Client) sessionPost(path string, encoded []byte) ([]byte, int, error) {
	token, err := c.ensureCSRF(false)
	if err != nil {
		return nil, 0, err
	}
	status, body, err := c.sessionPostWithToken(path, encoded, token)
	if err != nil {
		return nil, status, err
	}
	if status == http.StatusForbidden {
		// 令牌确实失效时刷新：清缓存后由调用方重试。
		c.csrfToken = ""
	}
	return body, status, nil
}

// sessionPostWithToken 是 sessionPost 的底层实现，便于测试注入令牌。
func (c *Client) sessionPostWithToken(path string, encoded []byte, token string) (int, []byte, error) {
	extra := http.Header{}
	extra.Set("X-CSRF-Token", token)
	extra.Set("Idempotency-Key", idempotencyKey())
	if origin := strings.TrimSpace(c.SiteOrigin); origin != "" {
		extra.Set("Origin", origin)
	}
	return c.doWith(http.MethodPost, path, bytes.NewReader(encoded), "application/json", defaultJSONTimeout, extra)
}

// ensureCSRF 读取并缓存会话 CSRF 令牌（来自 GET /me 的 session.csrf_token）。
// 参数/返回：force 为 true 时忽略缓存；返回令牌。
// 失败场景：会话失效（401）、响应缺少 csrf_token。
// 副作用：发起一次 HTTP 请求（仅首次或强制刷新时）。
func (c *Client) ensureCSRF(force bool) (string, error) {
	if !force && strings.TrimSpace(c.csrfToken) != "" {
		return c.csrfToken, nil
	}
	status, body, err := c.doWith(http.MethodGet, "/me", nil, "application/json", defaultJSONTimeout, nil)
	if err != nil {
		return "", fmt.Errorf("读取可乐站会话信息失败: %w", err)
	}
	if status >= 400 {
		return "", errorFromBody("读取会话信息", status, body)
	}
	var payload struct {
		Session struct {
			CSRFToken string `json:"csrf_token"`
		} `json:"session"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("可乐站会话信息解析失败: %w", err)
	}
	token := strings.TrimSpace(payload.Session.CSRFToken)
	if token == "" {
		return "", fmt.Errorf("可乐站会话未下发 csrf_token：Cookie 可能已失效")
	}
	c.csrfToken = token
	return token, nil
}

// DownloadTorrent 下载种子文件。
// 参数/返回：id 为种子 ID；返回 .torrent 原始二进制。
// 失败场景：授权申请失败、下载被拒、响应不是种子。
// 副作用：发起两次 HTTP 请求（申请授权 + 下载），必要时重试一次新授权。
func (c *Client) DownloadTorrent(id string) ([]byte, error) {
	torrentID := ExtractTorrentID(id)
	if torrentID == "" {
		return nil, fmt.Errorf("可乐站种子 ID 无效：%s", strings.TrimSpace(id))
	}

	authorizationID, err := c.IssueDownloadAuthorization(torrentID, 0)
	if err != nil {
		return nil, err
	}

	body, downloadErr := c.download(torrentID, authorizationID)
	if downloadErr == nil {
		return body, nil
	}
	// 授权是一次性的：GET 失败后旧授权多半已作废，用 replaces_id 换一张再试一次。
	replacementID, issueErr := c.IssueDownloadAuthorization(torrentID, authorizationID)
	if issueErr != nil {
		return nil, fmt.Errorf("%v；重新申请下载授权也失败: %w", downloadErr, issueErr)
	}
	return c.download(torrentID, replacementID)
}

// download 用指定授权拉取 .torrent。
func (c *Client) download(torrentID string, authorizationID int64) ([]byte, error) {
	prefix := "/automation/torrents/"
	if c.AuthMode() == "session" {
		prefix = "/torrents/"
	}
	path := prefix + torrentID + "/download?authorization_id=" + strconv.FormatInt(authorizationID, 10)
	status, body, err := c.do(http.MethodGet, path, nil, "application/x-bittorrent", defaultDownloadTimeout)
	if err != nil {
		return nil, fmt.Errorf("可乐站下载种子失败: %w", err)
	}
	if status >= 400 {
		return nil, errorFromBody("下载种子", status, body)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("可乐站下载种子失败：响应为空")
	}
	return body, nil
}

// getJSON 读取 JSON 接口并校验状态码。
func (c *Client) getJSON(path, stage string) ([]byte, error) {
	return c.doJSON(http.MethodGet, path, nil, stage)
}

// doJSON 发送 JSON 请求并返回响应体。
func (c *Client) doJSON(method, path string, payload any, stage string) ([]byte, error) {
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("构造可乐站请求体失败: %w", err)
		}
		body = bytes.NewReader(encoded)
	}
	accept := "application/json"
	status, respBody, err := c.do(method, path, body, accept, defaultJSONTimeout)
	if err != nil {
		return nil, fmt.Errorf("可乐站%s失败: %w", stage, err)
	}
	if status >= 400 {
		return nil, errorFromBody(stage, status, respBody)
	}
	return respBody, nil
}

// do 执行一次 HTTP 请求，4xx/5xx 不当作传输错误返回。
func (c *Client) do(method, path string, body io.Reader, accept string, timeout time.Duration) (int, []byte, error) {
	return c.doWith(method, path, body, accept, timeout, nil)
}

// doWith 在 do 的基础上附加自定义请求头（会话模式的 CSRF、Origin、幂等键）。
func (c *Client) doWith(method, path string, body io.Reader, accept string, timeout time.Duration, extra http.Header) (int, []byte, error) {
	target := strings.TrimRight(c.APIBase, "/") + path
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		return 0, nil, fmt.Errorf("构造请求失败（%s %s）: %w", method, target, err)
	}
	// 会话模式用 Cookie 鉴权；Key 模式用请求头。
	if cookie := strings.TrimSpace(c.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	if key := strings.TrimSpace(c.APIKey); key != "" {
		header := strings.TrimSpace(c.APIKeyHeader)
		if header == "" {
			header = DefaultAPIKeyHeader
		}
		req.Header.Set(header, key)
	}
	for name, values := range extra {
		for _, value := range values {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", UserAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	cloned := *client
	if cloned.Timeout <= 0 || timeout < cloned.Timeout {
		cloned.Timeout = timeout
	}
	resp, err := cloned.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("请求失败（%s %s）: %w", method, target, redactURL(err))
	}
	defer resp.Body.Close()

	limit := int64(maxJSONBody)
	if accept == "application/x-bittorrent" {
		limit = maxTorrentBody
	}
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit))
	if readErr != nil {
		return resp.StatusCode, nil, fmt.Errorf("读取响应失败（%s %s）: %w", method, target, readErr)
	}
	return resp.StatusCode, raw, nil
}

// errorFromBody 把站点错误体转成 *APIError。
func errorFromBody(stage string, status int, body []byte) error {
	apiErr := &APIError{Stage: stage, Status: status}
	var payload struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil {
		apiErr.Code = strings.TrimSpace(payload.Error.Code)
		apiErr.Message = strings.TrimSpace(payload.Error.Message)
		apiErr.RequestID = strings.TrimSpace(payload.Error.RequestID)
	}
	if apiErr.Code == "" {
		apiErr.Message = snippet(body)
	}
	return apiErr
}

// redactURL 去掉错误信息里可能带出的查询串（授权 ID 不敏感，但统一处理更稳妥）。
func redactURL(err error) error {
	if err == nil {
		return nil
	}
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	cleaned := *urlErr
	if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil {
		parsed.RawQuery = ""
		cleaned.URL = parsed.String()
	}
	return &cleaned
}

// idempotencyKey 生成站点要求的幂等键（16~128 个可见 ASCII 字符）。
// 说明：站点用它区分「重放」与「新操作」，每次写请求都必须是新值。
func idempotencyKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时退回时间戳，仍满足长度与字符集要求。
		return strconv.FormatInt(time.Now().UnixNano(), 36) + "-ptnexus"
	}
	return hex.EncodeToString(buf)
}

// snippet 截取响应体摘要用于错误提示。
func snippet(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "<空响应>"
	}
	if len(text) > 200 {
		return text[:200] + "…"
	}
	return text
}
