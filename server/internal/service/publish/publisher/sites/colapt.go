package sites

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/config"
	acquireextract "github.com/pt-nexus/server/internal/service/acquire/extract"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
	"gopkg.in/yaml.v3"
)

// colaptConfigFile 之外的正则：作品标识归一用。
// 站点 MediaReference 只认三种写法：rc<n>（站内作品）、豆瓣 subject URL、IMDb tt 号。
var (
	colaptDoubanBareIDPattern  = regexp.MustCompile(`^[0-9]{1,20}$`)
	colaptDoubanSubjectPattern = regexp.MustCompile(`subject/([0-9]{1,20})`)
	colaptIMDbPattern          = regexp.MustCompile(`tt([0-9]{7,10})`)
)

// colaptConfig 定义可乐PT（www.colapt.com）发种接口的专属字典。
//
// 可乐PT 不是 NexusPHP 站点，发种走自研 JSON API（站点契约 17.55.0 / openapi 3.4.0）：
//   - 鉴权：请求头 X-API-Key（账号级永久 Key，需带 torrent.upload scope，且账号必须已获得「自动发种」资格）
//   - 三段式：上传种子 → 写元数据 → 提交（提交时才关联作品 media）
//   - 分类挂在作品上、音轨是自由文本、字幕只有 zh/en/ja/other、标签由站点派生不可写
//
// 因此这里不复用 NexusPHP 系的 SitePublishConfig，使用独立结构。
// 字典真源为站点上传页前端字典（2026-10-22 抓取），完整取值以 server/configs/colapt.yaml 为准。
type colaptConfig struct {
	SiteName     string `yaml:"site_name"`
	APIBase      string `yaml:"api_base"`
	APIKeyHeader string `yaml:"api_key_header"`
	UploadPath   string `yaml:"upload_path"`
	TorrentPath  string `yaml:"torrent_path"`
	MetadataPath string `yaml:"metadata_path"`
	SubmitPath   string `yaml:"submit_path"`
	PublishPage  string `yaml:"publish_page"`
	Anonymous    bool   `yaml:"anonymous"`

	Medium     map[string]string `yaml:"medium"`
	Resolution map[string]string `yaml:"resolution"`
	VideoCodec map[string]string `yaml:"video_codec"`
	HDR        map[string]string `yaml:"hdr"`
	Audio      map[string]string `yaml:"audio"`
	Category   map[string]string `yaml:"category"`
	Subtitles  map[string]string `yaml:"subtitles"`

	Defaults colaptDefaults `yaml:"defaults"`
}

// colaptDefaults 定义各维度未命中时的兜底值（站点下拉都有「未知」，兜底一律未知）。
type colaptDefaults struct {
	Medium     string `yaml:"medium"`
	Resolution string `yaml:"resolution"`
	VideoCodec string `yaml:"video_codec"`
	HDR        string `yaml:"hdr"`
	Audio      string `yaml:"audio"`
	Category   string `yaml:"category"`
}

// colaptDefaultConfig 为 configs/colapt.yaml 缺失或解析失败时的兜底配置。
// 只保留「没有它就跑不起来」的端点与字典最小集；完整字典以 configs/colapt.yaml 为准，改动请同步两处。
var colaptDefaultConfig = colaptConfig{
	SiteName:     "ColaPT",
	APIBase:      "https://www.colapt.com",
	APIKeyHeader: "X-API-Key",
	UploadPath:   "/api/v1/automation/torrents",
	TorrentPath:  "/api/v1/automation/torrents/{id}",
	MetadataPath: "/api/v1/automation/torrents/{id}/metadata",
	SubmitPath:   "/api/v1/automation/torrents/{id}/submit",
	PublishPage:  "/torrents/{id}",
	Medium: map[string]string{
		"bluray": "Blu-ray Disc", "uhd_bluray": "Blu-ray Disc", "uhd_diy": "DIY原盘",
		"remux": "Remux", "uhd_remux": "Remux", "bdrip": "BDRip", "webdl": "WEB-DL",
		"webrip": "WEBRip", "hdtv": "HDTV", "tvrip": "TVRip", "dvd": "DVD", "dvdrip": "DVDRip",
	},
	Resolution: map[string]string{
		"r2160p": "2160p", "r1080p": "1080p", "r1080i": "1080i", "r720p": "720p",
		"r576p": "576p", "r576i": "576i", "r480p": "480p", "r480i": "480i",
	},
	VideoCodec: map[string]string{
		"h265": "HEVC / H.265", "x265": "HEVC / H.265", "h264": "AVC / H.264",
		"x264": "AVC / H.264", "av1": "AV1", "vp9": "VP9", "vc1": "VC-1", "mpeg2": "MPEG-2",
	},
	HDR: map[string]string{
		"dv+hdr10+": "Dolby Vision + HDR10+", "dv+hdr10": "Dolby Vision + HDR10",
		"dv": "Dolby Vision", "hdr10+": "HDR10+", "hdr10": "HDR10",
		"vivid": "HDR Vivid", "hlg": "HLG", "hdr": "HDR",
	},
	Audio: map[string]string{
		"aac": "AAC", "ac3": "AC-3", "ddp": "E-AC-3", "truehd": "TrueHD",
		"dts": "DTS", "dts_hd_ma": "DTS-HD MA", "dts_hd_hr": "DTS-HD HR", "dtsx": "DTS:X",
		"lpcm": "LPCM", "flac": "FLAC",
	},
	Category: map[string]string{
		"category.movie": "movie", "category.tv_series": "tv", "category.tv_shows": "variety",
		"category.animation": "anime", "category.documentaries": "movie",
	},
	Subtitles: map[string]string{"zh": "zh", "en": "en", "ja": "ja"},
	Defaults: colaptDefaults{
		Medium: "未知", Resolution: "未知", VideoCodec: "未知",
		HDR: "未知", Audio: "未知", Category: "other",
	},
}

// colaptHTTPResult 保存一次接口调用结果，便于按阶段拼日志与错误提示。
type colaptHTTPResult struct {
	Status int
	Body   []byte
}

// colaptAuth 描述一次发布使用的凭据。
//
// 站点有两条互不相通的写通道（2026-10-31 实测）：
//   - API Key：请求头 X-API-Key，路径 /api/v1/automation/*；需账号先获得「自动发种」资格，
//     否则 scopes 为空，上传直接 403 api_key_scope_denied。
//   - 浏览器会话：Cookie 里的 reelcore_session，路径去掉 /automation 段走网页版，
//     写操作额外要 X-CSRF-Token（GET /api/v1/me 的 session.csrf_token）与幂等键。
//
// 会话通道只要账号有 torrent.upload 权限就能用，因此 Key 被拒时自动降级到它。
type colaptAuth struct {
	APIKey  string
	Cookie  string
	Session bool
	// Origin 是会话模式写操作要求的同源头。
	Origin string
	// csrfToken 懒加载缓存，仅会话模式使用。
	csrfToken string
}

// authRejection 表示「凭据不被站点接受」（Key 无权限、会话失效等），上层可据此切换通道重试。
// 与普通业务失败区分开：这类失败在切换凭据后通常能成功，不该直接判为发布失败。
type authRejection struct {
	detail string
}

// Error 输出可直接给人看的原因。
func (e *authRejection) Error() string { return e.detail }

// colaptRequest 描述一次待发送的请求。
// 保留原始字节而不是 io.Reader：CSRF 失效需要原样重放一次。
type colaptRequest struct {
	Method      string
	URL         string
	Body        []byte
	ContentType string
	Accept      string
}

// PublishColapt 执行可乐PT 站点发布流程（上传种子 → 写元数据 → 提交）。
// 参数/返回：input 提供站点信息、发布数据与通用字段；返回发布详情页 URL 与发布过程日志。
// 失败场景：缺少 API Key、读取种子失败、站点鉴权/资格不足、字段映射后仍被站点拒绝（含站点侧查重拒绝）。
// 副作用：读取本地种子文件并向站点发起上传请求；成功后站点上会新增一条种子（可能进入审核队列）。
func PublishColapt(input publisher.PublishInput) (publisher.PublishResult, error) {
	cfg := loadColaptConfig()
	base := normalizeBaseURL(firstNonEmpty(input.BaseURL, cfg.APIBase, colaptDefaultConfig.APIBase))
	if base == "" {
		return publisher.PublishResult{}, fmt.Errorf("可乐站发种缺少 base_url")
	}

	auth, source, authErr := resolveColaptAuth(input, base)
	if authErr != nil {
		return publisher.PublishResult{}, authErr
	}

	result, err := runColaptFlow(input, cfg, base, auth, source)
	if err == nil {
		return result, nil
	}

	// Key 通道被站点拒（多为账号还没有「自动发种」资格，scopes 为空）时自动改用浏览器会话重走一遍。
	// 此时站点侧还没建任何草稿，重试是安全的。
	var rejection *authRejection
	if !auth.Session && errors.As(err, &rejection) {
		if cookie := strings.TrimSpace(input.Cookie); cookie != "" {
			retryAuth := &colaptAuth{Cookie: cookie, Session: true, Origin: colaptSiteOrigin(base)}
			retryResult, retryErr := runColaptFlow(input, cfg, base, retryAuth,
				"Cookie 会话（API Key 无权限，已自动降级）")
			if retryErr == nil {
				return retryResult, nil
			}
			return publisher.PublishResult{}, fmt.Errorf(
				"API Key 通道失败：%s；改用 Cookie 会话上传也失败：%w", rejection.detail, retryErr)
		}
	}
	return result, err
}

// runColaptFlow 用给定凭据执行一次完整发种流程（上传种子 → 写元数据 → 提交）。
// 参数/返回：auth 决定走 /automation/* 还是网页版 /torrents/*；source 只用于日志标注凭据来源。
// 失败场景：读取种子失败、站点鉴权被拒、字段被站点拒绝。
// 副作用：向站点发起上传请求，成功后站点上新增一条种子（可能进入审核队列）。
func runColaptFlow(input publisher.PublishInput, cfg colaptConfig, base string, auth *colaptAuth, source string) (publisher.PublishResult, error) {
	logs := []string{
		fmt.Sprintf("--- [可乐PT] 使用 %s 鉴权（指纹 %s）---", source, colaptFingerprint(auth.fingerprintValue())),
	}

	// 对齐 PublishPublic：UPLOAD_TEST_MODE=true 时跳过真实发种，返回模拟成功。
	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		logs = append(logs, "--- [可乐PT] 测试模式：跳过实际发种 ---")
		return publisher.PublishResult{
			PublishURL:       strings.TrimRight(base, "/") + strings.ReplaceAll(cfg.PublishPage, "{id}", "999999999"),
			AttemptDetailLog: strings.Join(logs, "\n"),
		}, nil
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return publisher.PublishResult{}, fmt.Errorf("可乐站发种缺少标题")
	}
	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return publisher.PublishResult{}, fmt.Errorf("可乐站发种缺少种子文件路径")
	}
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("读取种子文件失败: %w", err)
	}

	client := &http.Client{Timeout: 180 * time.Second}

	// 步骤 1：上传种子，得到草稿 id 与 revision。
	uploadURL := strings.TrimRight(base, "/") + auth.resolvePath(cfg.UploadPath)
	created, err := colaptUploadTorrent(client, uploadURL, cfg, auth, torrentBytes)
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")}, err
	}
	torrentID := int64FromAny(created["id"], 0)
	if torrentID <= 0 {
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")},
			fmt.Errorf("可乐站未返回种子 ID：%s", summarizeResponseBody(colaptEncodeJSON(created)))
	}
	revision := int64FromAny(created["revision"], 1)
	logs = append(logs, fmt.Sprintf("步骤1 上传种子成功：id=%d revision=%d info_hash=%s",
		torrentID, revision, toStringAny(created["info_hash"], "")))

	// 步骤 2：写元数据（字段级映射）。
	metadata, metaLogs := buildColaptMetadata(input, cfg)
	logs = append(logs, metaLogs...)
	metadata["expected_revision"] = revision

	metadataURL := strings.ReplaceAll(strings.TrimRight(base, "/")+auth.resolvePath(cfg.MetadataPath), "{id}", strconv.FormatInt(torrentID, 10))
	result, err := colaptDoJSON(client, http.MethodPut, metadataURL, cfg, auth, metadata)
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")}, err
	}
	if result.Status >= 400 {
		logs = append(logs, fmt.Sprintf("步骤2 写元数据失败：%s", summarizeResponseBody(string(result.Body))))
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")},
			colaptErrorFromResponse(result, "写元数据")
	}
	logs = append(logs, "步骤2 写元数据成功")

	// 写元数据会推高 revision，提交前重新读一次，避免乐观锁冲突。
	latest, err := colaptFetchTorrent(client, base, cfg, auth, torrentID)
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")}, err
	}
	revision = int64FromAny(latest["revision"], revision)
	// 站点侧可能在此时已判定同作品同档位（warning 不拒绝），把提示带进日志便于事后核对。
	for _, warning := range colaptWarnings(latest) {
		logs = append(logs, "站点提示: "+warning)
	}

	// 步骤 3：提交（关联作品）。
	submitURL := strings.ReplaceAll(strings.TrimRight(base, "/")+auth.resolvePath(cfg.SubmitPath), "{id}", strconv.FormatInt(torrentID, 10))
	submission, submitLogs, err := colaptSubmit(client, submitURL, cfg, auth, torrentID, revision, input)
	logs = append(logs, submitLogs...)
	if err != nil {
		return publisher.PublishResult{AttemptDetailLog: strings.Join(logs, "\n")}, err
	}

	publishURL := strings.TrimRight(base, "/") + strings.ReplaceAll(cfg.PublishPage, "{id}", strconv.FormatInt(torrentID, 10))
	stage := toStringAny(submission["publication_stage"], "")
	status := toStringAny(submission["status"], "")
	if stage != "" || status != "" {
		logs = append(logs, fmt.Sprintf("站点回传阶段：status=%s publication_stage=%s", status, stage))
	}
	if stage != "" && !strings.EqualFold(stage, "published") {
		logs = append(logs, "⚠️ 该站提交后不一定立即上架，当前阶段不是 published，请到站点查看是否需要审核/补充资料")
	}
	logs = append(logs, "详情页: "+publishURL)

	return publisher.PublishResult{
		PublishURL:       publishURL,
		AttemptDetailLog: strings.Join(logs, "\n"),
	}, nil
}

// colaptUploadTorrent 上传种子文件（body 直接是种子二进制），返回站点回传的草稿对象。
func colaptUploadTorrent(client *http.Client, url string, cfg colaptConfig, auth *colaptAuth, torrentBytes []byte) (map[string]any, error) {
	result, err := colaptSend(client, cfg, auth, colaptRequest{
		Method:      http.MethodPost,
		URL:         url,
		Body:        torrentBytes,
		ContentType: "application/x-bittorrent",
		Accept:      "application/json",
	})
	if err != nil {
		return nil, err
	}
	if result.Status >= 400 {
		return nil, colaptErrorFromResponse(result, "上传种子")
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(result.Body, &parsed); err != nil {
		return nil, fmt.Errorf("可乐站上传响应不是合法 JSON: %s", summarizeResponseBody(string(result.Body)))
	}
	return parsed, nil
}

// colaptFetchTorrent 读取种子当前状态（用于取最新 revision 与站点提示）。
func colaptFetchTorrent(client *http.Client, base string, cfg colaptConfig, auth *colaptAuth, torrentID int64) (map[string]any, error) {
	url := strings.ReplaceAll(strings.TrimRight(base, "/")+auth.resolvePath(cfg.TorrentPath), "{id}", strconv.FormatInt(torrentID, 10))
	result, err := colaptDoJSON(client, http.MethodGet, url, cfg, auth, nil)
	if err != nil {
		return nil, err
	}
	if result.Status >= 400 {
		return nil, colaptErrorFromResponse(result, "读取种子状态")
	}
	parsed := map[string]any{}
	if err := json.Unmarshal(result.Body, &parsed); err != nil {
		return nil, fmt.Errorf("可乐站种子状态响应不是合法 JSON: %s", summarizeResponseBody(string(result.Body)))
	}
	return parsed, nil
}

// colaptSubmit 提交种子：先按外链关联作品（media_id），站点没有该作品时改用 new_media 现场建档。
// 参数/返回：revision 为提交前的乐观锁版本号；返回提交响应、过程日志与错误。
func colaptSubmit(
	client *http.Client,
	url string,
	cfg colaptConfig,
	auth *colaptAuth,
	torrentID int64,
	revision int64,
	input publisher.PublishInput,
) (map[string]any, []string, error) {
	logs := []string{}
	std := colaptStandardized(input.UploadData)

	body := map[string]any{"expected_revision": revision}
	reference := colaptMediaReference(input)
	if reference != "" {
		body["media_id"] = reference
		logs = append(logs, "步骤3 提交：按作品标识关联已建档作品 "+reference)
	} else {
		body["new_media"] = colaptNewMedia(input, std, cfg)
		logs = append(logs, "步骤3 提交：没有可用的作品标识，随提交新建作品档案")
	}

	result, err := colaptDoJSON(client, http.MethodPost, url, cfg, auth, body)
	if err != nil {
		return nil, logs, err
	}
	if result.Status == 404 && reference != "" && colaptErrorCode(result) == "media_not_found" {
		// 站点不自动建档：外链在站内没有对应作品时改用 new_media 重提一次。
		logs = append(logs, "站点内没有该作品档案（media_not_found），改用 new_media 建档后重新提交")
		body = map[string]any{
			"expected_revision": revision,
			"new_media":         colaptNewMedia(input, std, cfg),
		}
		result, err = colaptDoJSON(client, http.MethodPost, url, cfg, auth, body)
		if err != nil {
			return nil, logs, err
		}
	}
	if result.Status >= 400 {
		logs = append(logs, fmt.Sprintf("步骤3 提交失败：%s", summarizeResponseBody(string(result.Body))))
		return nil, logs, colaptErrorFromResponse(result, "提交种子")
	}

	parsed := map[string]any{}
	if err := json.Unmarshal(result.Body, &parsed); err != nil {
		// 提交已经成功（HTTP 2xx），响应解析失败不应改判为发布失败。
		logs = append(logs, "步骤3 提交成功，但响应解析失败："+summarizeResponseBody(string(result.Body)))
		return map[string]any{}, logs, nil
	}
	logs = append(logs, fmt.Sprintf("步骤3 提交成功：id=%d", torrentID))
	for _, warning := range colaptWarnings(parsed) {
		logs = append(logs, "站点提示: "+warning)
	}
	return parsed, logs, nil
}

// buildColaptMetadata 把 PTNexus 的发布数据映射成站点元数据字段。
// 参数/返回：input 为发布输入；返回元数据 map 与映射过程日志（含未命中告警）。
// 说明：未命中站点字典的维度一律回落「未知」并记录告警，绝不提交站点不认的值。
func buildColaptMetadata(input publisher.PublishInput, cfg colaptConfig) (map[string]any, []string) {
	logs := []string{}
	std := colaptStandardized(input.UploadData)
	tags := resolveSiteCombinedTags(input.UploadData)

	pick := func(label string, mapping map[string]string, raw, prefix, fallback string) string {
		value, matched := colaptPick(mapping, raw, prefix, fallback)
		if !matched && strings.TrimSpace(raw) != "" {
			logs = append(logs, fmt.Sprintf(
				"⚠️ %s 映射未命中：标准化值 %q 不在 configs/colapt.yaml 对应字典中，已回落 %q。请核对站点选项后补充映射。",
				label, strings.TrimSpace(raw), value,
			))
		}
		return value
	}

	audioLabels := buildColaptAudio(input, std, cfg)
	metadata := map[string]any{
		"release_name": strings.TrimSpace(input.Title),
		// 站点把「媒介」叫「片源类型」，API 字段是 source。
		"source":      pick("片源类型", cfg.Medium, toStringAny(std["medium"], ""), "medium.", cfg.Defaults.Medium),
		"resolution":  pick("分辨率", cfg.Resolution, toStringAny(std["resolution"], ""), "resolution.", cfg.Defaults.Resolution),
		"video_codec": pick("视频编码", cfg.VideoCodec, toStringAny(std["video_codec"], ""), "video.", cfg.Defaults.VideoCodec),
		"hdr":         buildColaptHDR(tags, cfg),
		"audio":       audioLabels,
	}

	if subtitle := strings.TrimSpace(input.Subtitle); subtitle != "" {
		metadata["subtitle"] = subtitle
	}
	if group := colaptReleaseGroup(input, std); group != "" {
		metadata["release_group"] = group
	}
	if description := strings.TrimSpace(input.Description); description != "" {
		metadata["description"] = colaptTruncateUTF8(description, 65000)
	}
	if mediaInfo := strings.TrimSpace(input.MediaInfo); mediaInfo != "" {
		metadata["media_info"] = colaptTruncateUTF8(mediaInfo, 1000000)
	}
	if screenshots := colaptScreenshots(input.UploadData); len(screenshots) > 0 {
		metadata["screenshots"] = screenshots
	}
	if subtitles := buildColaptSubtitles(tags, cfg); len(subtitles) > 0 {
		metadata["subtitles"] = subtitles
	}
	if cfg.Anonymous {
		metadata["anonymous"] = true
	}

	category := buildColaptCategory(input.UploadData, std, tags, cfg)
	scope, seasons := buildColaptReleaseScope(input.UploadData, std, tags, category)
	if scope != nil {
		metadata["release_scope"] = scope
	}
	if seasons != "" {
		metadata["release_seasons"] = seasons
	}
	if scope == nil && strings.TrimSpace(seasons) == "" && (category == "tv" || category == "anime" || category == "variety") {
		logs = append(logs, fmt.Sprintf(
			"⚠️ 分类 %s 需要「季与集数」范围，但标题/季集字段里没解析出季集，已留空提交；站点可能要求补填。",
			category,
		))
	}

	logs = append(logs, fmt.Sprintf("字段映射结果：分类=%s 片源类型=%s 分辨率=%s 视频编码=%s HDR=%s 音轨=%s",
		category,
		toStringAny(metadata["source"], ""),
		toStringAny(metadata["resolution"], ""),
		toStringAny(metadata["video_codec"], ""),
		toStringAny(metadata["hdr"], ""),
		strings.Join(audioLabels, ", "),
	))
	return metadata, logs
}

// buildColaptCategory 决定作品分类（movie / tv / anime / variety / other）。
// 说明：动画标签优先于类型字段（与仓库既有口径一致），综艺归 variety。
func buildColaptCategory(uploadData map[string]any, std map[string]any, tags map[string]struct{}, cfg colaptConfig) string {
	if hasAnimationTag(uploadData) {
		return "anime"
	}
	raw := strings.TrimSpace(toStringAny(std["type"], ""))
	if raw == "" {
		return cfg.Defaults.Category
	}
	if value, ok := colaptPick(cfg.Category, raw, "category.", cfg.Defaults.Category); ok {
		return value
	}
	return cfg.Defaults.Category
}

// buildColaptReleaseScope 构造站点的发布范围（release_scope）与「季」（release_seasons）。
//
// 站点规则（契约 17.55.0）：
//   - tv / anime / variety 用 release_scope；movie / other 的 release_scope 必须为 null，季写在 release_seasons。
//   - anime 额外要 anime_type；anime_type=movie/ova/special 时 season 必须为 null，movie 还要求 episodes 为 null。
//   - scope=complete 时 episodes 可为 null；scope=single/multiple 时 episodes 必填。
//
// 因此这里只做能确证的判断：命中合集/完结 → complete；能解析出单集号 → single；其余一律 complete 且不带集号。
// variety 的发布范围需要「播出年份/期号/播出日期/期类型」四项，PTNexus 侧没有对应数据，留空由站点侧提示补填。
func buildColaptReleaseScope(uploadData map[string]any, std map[string]any, tags map[string]struct{}, category string) (map[string]any, string) {
	if category == "movie" || category == "other" {
		season, _ := colaptSeasonEpisode(uploadData, std)
		return nil, colaptNormalizeSeasonNumber(season)
	}
	if category == "variety" {
		return nil, ""
	}

	season, episode := colaptSeasonEpisode(uploadData, std)
	scope := "complete"
	episodes := any(nil)
	if !hasAnySiteTagLower(tags, "tag.合集", "tag.完结", "合集", "完结") {
		if episodeNumber, ok := colaptEpisodeNumber(episode); ok {
			scope = "single"
			episodes = strconv.Itoa(episodeNumber)
		}
	}

	normalizedSeason := colaptNormalizeSeasonNumber(season)
	scopeValue := map[string]any{
		"category": category,
		"season":   nil,
		"scope":    scope,
		"episodes": episodes,
	}
	if normalizedSeason != "" {
		scopeValue["season"] = normalizedSeason
	}

	if category == "anime" {
		animeType := colaptAnimeType(uploadData, std)
		scopeValue["anime_type"] = animeType
		switch animeType {
		case "movie":
			scopeValue["season"] = nil
			scopeValue["episodes"] = nil
			scopeValue["scope"] = "complete"
		case "ova", "ona", "special":
			scopeValue["season"] = nil
		}
	}
	return scopeValue, ""
}

// colaptAnimeType 推断动漫子类型（tv / movie / ova / ona / special）。
func colaptAnimeType(uploadData map[string]any, std map[string]any) string {
	haystack := strings.ToLower(strings.Join([]string{
		strings.TrimSpace(toStringAny(std["title"], "")),
		strings.TrimSpace(toStringAny(uploadData["title"], "")),
		strings.TrimSpace(toStringAny(uploadData["name"], "")),
	}, " "))
	switch {
	case strings.Contains(haystack, "剧场版"), strings.Contains(haystack, "劇場版"):
		return "movie"
	case strings.Contains(haystack, "ova"):
		return "ova"
	case strings.Contains(haystack, "ona"):
		return "ona"
	case strings.Contains(haystack, "特别篇"), strings.Contains(haystack, "特別篇"):
		return "special"
	default:
		return "tv"
	}
}

// colaptSeasonEpisode 取季集文本（优先标题组件里的「季集」，其次 season_episode 字段）。
func colaptSeasonEpisode(uploadData map[string]any, std map[string]any) (string, string) {
	raw := ""
	if uploadData != nil {
		raw = strings.TrimSpace(findTitleComponentValue(parseTitleComponentsLocal(uploadData["title_components"]), "季集"))
		if raw == "" {
			raw = strings.TrimSpace(toStringAny(uploadData["season_episode"], ""))
		}
	}
	if raw == "" && std != nil {
		raw = strings.TrimSpace(toStringAny(std["season_episode"], ""))
	}
	if raw == "" {
		return "", ""
	}
	return parseHaidanSeasonEpisode(raw)
}

// colaptNormalizeSeasonNumber 把站点要求的「季」归一为不带前导零的整数串（"01" → "1"）；无法解析返回空串。
func colaptNormalizeSeasonNumber(season string) string {
	trimmed := strings.TrimSpace(season)
	if trimmed == "" {
		return ""
	}
	value, err := strconv.Atoi(strings.TrimLeft(trimmed, "0"))
	if err != nil {
		return ""
	}
	if value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

// colaptEpisodeNumber 把集号归一为整数；"0"/空/无法解析时返回 false。
func colaptEpisodeNumber(episode string) (int, bool) {
	trimmed := strings.TrimSpace(episode)
	if trimmed == "" {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimLeft(trimmed, "0"))
	if err != nil || value <= 0 {
		return 0, false
	}
	return value, true
}

// buildColaptHDR 由标签组合推断 HDR 类型（站点是单选，Dolby Vision 与 HDR10 系列可组合）。
func buildColaptHDR(tags map[string]struct{}, cfg colaptConfig) string {
	dv := hasAnySiteTagLower(tags, "tag.杜比", "tag.dv", "tag.dovi", "杜比", "dovi", "dolbyvision")
	hdr10plus := hasAnySiteTagLower(tags, "tag.hdr10+", "hdr10+")
	hdr10 := hasAnySiteTagLower(tags, "tag.hdr10", "hdr10")
	vivid := hasAnySiteTagLower(tags, "tag.菁彩hdr", "tag.hdrvivid", "菁彩hdr", "hdrvivid")
	hlg := hasAnySiteTagLower(tags, "tag.hlg", "hlg")
	hdr := hasAnySiteTagLower(tags, "tag.hdr", "hdr")

	key := ""
	switch {
	case dv && hdr10plus:
		key = "dv+hdr10+"
	case dv && hdr10:
		key = "dv+hdr10"
	case dv:
		key = "dv"
	case hdr10plus:
		key = "hdr10+"
	case hdr10:
		key = "hdr10"
	case vivid:
		key = "vivid"
	case hlg:
		key = "hlg"
	case hdr:
		key = "hdr"
	}
	if key == "" {
		return cfg.Defaults.HDR
	}
	if value := strings.TrimSpace(cfg.HDR[key]); value != "" {
		return value
	}
	return cfg.Defaults.HDR
}

// buildColaptSubtitles 由标签推断字幕语言码（zh/en/ja，站点最多 4 个）。
func buildColaptSubtitles(tags map[string]struct{}, cfg colaptConfig) []string {
	result := make([]string, 0, 3)
	if hasAnySiteTagLower(tags, "tag.中字", "tag.简中", "tag.繁中", "中字", "简中", "繁中", "中英") {
		result = append(result, "zh")
	}
	if hasAnySiteTagLower(tags, "tag.英字", "英字") {
		result = append(result, "en")
	}
	if hasAnySiteTagLower(tags, "tag.日字", "tag.日文", "日字", "日文") {
		result = append(result, "ja")
	}
	// 只保留站点字典里认得的码，顺序按 zh/en/ja 固定，避免触发站点 uniqueItems 之外的排序差异。
	filtered := make([]string, 0, len(result))
	for _, code := range result {
		if mapped, ok := cfg.Subtitles[code]; ok && strings.TrimSpace(mapped) != "" {
			filtered = append(filtered, mapped)
		}
	}
	return filtered
}

// buildColaptAudio 构造音轨自由文本：优先用 MediaInfo 音轨（编码+声道，形如 "DTS-HD MA 5.1"），
// 取不到音轨时用标准化音频编码的显示名兜底。
func buildColaptAudio(input publisher.PublishInput, std map[string]any, cfg colaptConfig) []string {
	tracks := acquireextract.ParseAudioTracksFromMediainfo(input.MediaInfo)
	labels := make([]string, 0, len(tracks))
	for _, track := range tracks {
		name := colaptAudioDisplay(track.CodecKey, cfg)
		if name == "" || name == "未知" {
			continue
		}
		if channels := colaptChannelLabel(track.Channels); channels != "" {
			name = name + " " + channels
		}
		labels = appendUniqueString(labels, name)
	}
	if len(labels) > 0 {
		if len(labels) > 20 {
			labels = labels[:20]
		}
		return labels
	}

	key := strings.TrimSpace(toStringAny(std["audio_codec"], ""))
	if label := colaptAudioDisplay(key, cfg); label != "" {
		return []string{label}
	}
	return []string{cfg.Defaults.Audio}
}

// colaptAudioDisplay 把标准化音频编码键（audio.dts_hd_ma）转成站点习惯写法（DTS-HD MA）。
func colaptAudioDisplay(codecKey string, cfg colaptConfig) string {
	normalized := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(codecKey), "audio."))
	if normalized == "" {
		return ""
	}
	if value, ok := cfg.Audio[normalized]; ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if value, ok := cfg.Audio["audio."+normalized]; ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

// colaptChannelLabel 把声道数转成站点习惯的声道布局写法。
func colaptChannelLabel(channels int) string {
	switch channels {
	case 1:
		return "1.0"
	case 2:
		return "2.0"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	case 0:
		return ""
	default:
		return strconv.Itoa(channels) + "ch"
	}
}

// colaptScreenshots 提取截图 URL 列表（站点最多 20 张、必须 http(s) 且不超过 2048 字符）。
func colaptScreenshots(uploadData map[string]any) []string {
	raw := resolveUploadSection(uploadData, "screenshots")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	result := make([]string, 0, 8)
	for _, url := range extractImageURLsFromText(raw) {
		trimmed := strings.TrimSpace(url)
		if len(trimmed) == 0 || len(trimmed) > 2048 {
			continue
		}
		if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
			continue
		}
		result = appendUniqueString(result, trimmed)
		if len(result) >= 20 {
			break
		}
	}
	return result
}

// colaptReleaseGroup 取制作组（站点为自由文本，未知留空）。
func colaptReleaseGroup(input publisher.PublishInput, std map[string]any) string {
	raw := strings.TrimSpace(toStringAny(input.UploadData["team"], ""))
	if raw == "" {
		raw = strings.TrimSpace(toStringAny(std["team"], ""))
	}
	raw = strings.TrimPrefix(raw, "team.")
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "other") || raw == "其他" {
		return ""
	}
	if len([]rune(raw)) > 64 {
		return string([]rune(raw)[:64])
	}
	return raw
}

// colaptMediaReference 从外链推导作品标识（MediaReference：豆瓣条目 URL 或 IMDb tt 号）。
// 站点用「一个命名空间一种写法」的规则：豆瓣必须是 https://movie.douban.com/subject/<id>/，IMDb 必须是 tt<digits>。
// 站内自有作品编号（rc<n>）PTNexus 侧没有，故不产生。
func colaptMediaReference(input publisher.PublishInput) string {
	if douban := colaptDoubanReference(firstNonEmpty(input.DoubanLink, toStringAny(input.UploadData["douban_link"], ""))); douban != "" {
		return douban
	}
	return colaptIMDbReference(firstNonEmpty(input.IMDbLink, toStringAny(input.UploadData["imdb_link"], "")))
}

// colaptDoubanReference 把豆瓣链接/ID 归一为站点要求的 subject URL。
// 站点 MediaReference 只认这一种写法，裸数字（除作为豆瓣 ID 外）会被判定 invalid_media_id。
func colaptDoubanReference(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if colaptDoubanBareIDPattern.MatchString(trimmed) {
		return "https://movie.douban.com/subject/" + trimmed + "/"
	}
	if match := colaptDoubanSubjectPattern.FindStringSubmatch(trimmed); len(match) >= 2 {
		return "https://movie.douban.com/subject/" + match[1] + "/"
	}
	return ""
}

// colaptIMDbReference 把 IMDb 链接/ID 归一为 tt<digits>。
// 说明：不能用 strings.Index 找 "tt"，`https` 里就含 "tt"，必须用带数字边界的模式匹配。
func colaptIMDbReference(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if match := colaptIMDbPattern.FindStringSubmatch(strings.ToLower(trimmed)); len(match) >= 2 {
		return "tt" + match[1]
	}
	return ""
}

// colaptTrailingDigits 取字符串里最后一段连续数字（豆瓣 subject ID）。
func colaptTrailingDigits(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	digits := ""
	for index := len(trimmed) - 1; index >= 0; index-- {
		char := trimmed[index]
		if char < '0' || char > '9' {
			break
		}
		digits = string(char) + digits
	}
	if len(digits) == 0 || len(digits) > 20 {
		return ""
	}
	return digits
}

// colaptNewMedia 构造「随提交新建作品档案」的请求体（站点只强制 category，其余按现有数据尽量补全）。
func colaptNewMedia(input publisher.PublishInput, std map[string]any, cfg colaptConfig) map[string]any {
	category := buildColaptCategory(input.UploadData, std, resolveSiteCombinedTags(input.UploadData), cfg)
	body := map[string]any{"category": category}

	if title := strings.TrimSpace(firstNonEmpty(toStringAny(input.UploadData["title"], ""), toStringAny(input.UploadData["name"], ""))); title != "" {
		if len([]rune(title)) <= 255 {
			body["title"] = title
		}
	}
	if year := colaptYear(input.UploadData, std); year > 0 {
		body["year"] = year
	}
	if douban := colaptDoubanReference(firstNonEmpty(input.DoubanLink, toStringAny(input.UploadData["douban_link"], ""))); douban != "" {
		if id := colaptTrailingDigits(douban); id != "" {
			body["douban_id"] = id
		}
	}
	if imdb := colaptIMDbReference(firstNonEmpty(input.IMDbLink, toStringAny(input.UploadData["imdb_link"], ""))); imdb != "" {
		body["imdb_id"] = imdb
	}
	// 海报必须是 https（站点 pattern ^https://），http 或空直接不传。
	if poster := colaptPoster(input.UploadData); poster != "" {
		body["poster_url"] = poster
	}
	if synopsis := strings.TrimSpace(resolveUploadSection(input.UploadData, "synopsis")); synopsis != "" {
		body["synopsis"] = colaptTruncateUTF8(synopsis, 16000)
	}
	return body
}

// colaptYear 取年份（4 位数字）。
func colaptYear(uploadData map[string]any, std map[string]any) int {
	raw := strings.TrimSpace(toStringAny(uploadData["year"], ""))
	if raw == "" {
		raw = strings.TrimSpace(toStringAny(std["year"], ""))
	}
	if raw == "" {
		raw = extractFourDigitYear(toStringAny(uploadData["title"], ""))
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1800 || value > 2200 {
		return 0
	}
	return value
}

// colaptPoster 取海报 URL（只接受 https，站点 pattern 要求）。
func colaptPoster(uploadData map[string]any) string {
	for _, key := range []string{"poster", "cover"} {
		section := resolveUploadSection(uploadData, key)
		if strings.TrimSpace(section) == "" {
			continue
		}
		for _, url := range extractImageURLsFromText(section) {
			trimmed := strings.TrimSpace(url)
			if strings.HasPrefix(trimmed, "https://") && len(trimmed) <= 2048 {
				return trimmed
			}
		}
	}
	return ""
}

// colaptStandardized 取发布数据里的标准化参数字典（缺省返回空 map，绝不返回 nil 供调用方直接索引）。
func colaptStandardized(uploadData map[string]any) map[string]any {
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

// resolveColaptAuth 决定本次发布用哪条写通道。
//
// 规则：配了 API Key（站点配置 api_key 或 Passkey 栏）→ 先走 Key 通道（站点官方自动化入口，
// 拿到「自动发种」资格后最稳）；只配了 Cookie → 直接走网页会话通道。
// 返回值第二项是凭据来源描述，仅用于日志核对。
func resolveColaptAuth(input publisher.PublishInput, base string) (*colaptAuth, string, error) {
	candidates := []struct {
		Source string
		Value  string
	}{
		{Source: "站点配置 api_key", Value: toStringAny(input.TargetInfo["api_key"], "")},
		{Source: "Passkey 栏 API Key", Value: toStringAny(input.TargetInfo["passkey"], "")},
	}
	for _, candidate := range candidates {
		if value := strings.TrimSpace(candidate.Value); value != "" {
			return &colaptAuth{APIKey: value}, candidate.Source, nil
		}
	}
	if cookie := strings.TrimSpace(input.Cookie); cookie != "" {
		return &colaptAuth{Cookie: cookie, Session: true, Origin: colaptSiteOrigin(base)}, "Cookie 会话", nil
	}
	return nil, "", fmt.Errorf(
		"可乐站发种缺少凭据：请在站点 Cookie 栏粘贴浏览器登录后的 /api/v1 请求 Cookie" +
			"（含 reelcore_session=，可直接发种）；或填 API Key（Passkey 栏，需账号已获「自动发种」资格）")
}

// colaptSiteOrigin 返回站点同源地址（会话模式写操作必须带 Origin 头）。
func colaptSiteOrigin(base string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(base), "/")
	if index := strings.Index(trimmed, "/api/v1"); index > 0 {
		return trimmed[:index]
	}
	return trimmed
}

// resolvePath 按通道改写接口路径：会话通道去掉 /automation 段，走网页版同名接口。
func (a *colaptAuth) resolvePath(path string) string {
	if a == nil || !a.Session {
		return path
	}
	return strings.Replace(path, "/automation", "", 1)
}

// fingerprintValue 返回用于日志脱敏展示的凭据值。
func (a *colaptAuth) fingerprintValue() string {
	if a == nil {
		return ""
	}
	if a.Session {
		return a.Cookie
	}
	return a.APIKey
}

// applyHeaders 写入公共请求头；会话模式额外带 Cookie，写操作带 CSRF 与幂等键。
func (a *colaptAuth) applyHeaders(req *http.Request, cfg colaptConfig, write bool) {
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "PTNexus/1.0 (+https://github.com/weishouxian/PTNexus)")
	if a == nil {
		return
	}
	if a.Session {
		req.Header.Set("Cookie", a.Cookie)
		if origin := strings.TrimSpace(a.Origin); origin != "" {
			req.Header.Set("Origin", origin)
		}
		if write {
			req.Header.Set("X-CSRF-Token", a.csrfToken)
			req.Header.Set("Idempotency-Key", colaptIdempotencyKey())
		}
		return
	}
	header := strings.TrimSpace(cfg.APIKeyHeader)
	if header == "" {
		header = "X-API-Key"
	}
	req.Header.Set(header, a.APIKey)
}

// ensureCSRF 读取并缓存会话 CSRF 令牌（GET /api/v1/me 的 session.csrf_token）。
// 参数/返回：client 为底层客户端；url 为站点 /api/v1/me 地址；force 为 true 时忽略缓存。
// 失败场景：会话失效（401/403）、响应缺少 csrf_token。
// 副作用：发起一次 HTTP 请求（仅首次或强制刷新时）。
func (a *colaptAuth) ensureCSRF(client *http.Client, cfg colaptConfig, base string, force bool) error {
	if a == nil || !a.Session {
		return nil
	}
	if !force && strings.TrimSpace(a.csrfToken) != "" {
		return nil
	}
	result, err := colaptSend(client, cfg, &colaptAuth{Cookie: a.Cookie, Session: true, Origin: a.Origin}, colaptRequest{
		Method: http.MethodGet,
		URL:    strings.TrimRight(strings.TrimSpace(base), "/") + "/api/v1/me",
		Accept: "application/json",
	})
	if err != nil {
		return fmt.Errorf("读取可乐站会话信息失败: %w", err)
	}
	if result.Status >= 400 {
		return fmt.Errorf("可乐站会话不可用（HTTP %d）：Cookie 可能已过期，请重新登录站点后复制 Cookie", result.Status)
	}
	payload := map[string]any{}
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		return fmt.Errorf("可乐站会话信息解析失败: %s", summarizeResponseBody(string(result.Body)))
	}
	session, _ := payload["session"].(map[string]any)
	token := strings.TrimSpace(toStringAny(session["csrf_token"], ""))
	if token == "" {
		return fmt.Errorf("可乐站会话未下发 csrf_token：Cookie 可能已失效")
	}
	a.csrfToken = token
	return nil
}

// colaptSend 发送一次请求并返回状态码与响应体（4xx/5xx 不当作传输错误）。
//
// 会话模式下写操作依赖 CSRF：令牌在浏览器重登后会轮换，站点回 403 时刷新一次并原样重放，
// 避免把「令牌过期」误报成发布失败。
func colaptSend(client *http.Client, cfg colaptConfig, auth *colaptAuth, spec colaptRequest) (colaptHTTPResult, error) {
	// 会话模式的写操作必须先拿到 CSRF 令牌（只在首次或刷新时发一次 /me）。
	if auth != nil && auth.Session && spec.Method != http.MethodGet {
		if err := auth.ensureCSRF(client, cfg, colaptOriginFromURL(spec.URL), false); err != nil {
			return colaptHTTPResult{}, err
		}
	}
	result, err := colaptSendOnce(client, cfg, auth, spec)
	if err != nil {
		return result, err
	}
	if auth == nil || !auth.Session || spec.Method == http.MethodGet || result.Status != http.StatusForbidden {
		return result, nil
	}
	if refreshErr := auth.ensureCSRF(client, cfg, colaptOriginFromURL(spec.URL), true); refreshErr != nil {
		return result, nil
	}
	return colaptSendOnce(client, cfg, auth, spec)
}

// colaptSendOnce 是 colaptSend 的单次实现（构造请求 → 写入头部 → 读响应）。
func colaptSendOnce(client *http.Client, cfg colaptConfig, auth *colaptAuth, spec colaptRequest) (colaptHTTPResult, error) {
	var body io.Reader
	if len(spec.Body) > 0 {
		body = bytes.NewReader(spec.Body)
	}
	req, err := http.NewRequest(spec.Method, spec.URL, body)
	if err != nil {
		return colaptHTTPResult{}, fmt.Errorf("构造可乐站请求失败: %w", err)
	}
	auth.applyHeaders(req, cfg, spec.Method != http.MethodGet)
	if spec.ContentType != "" {
		req.Header.Set("Content-Type", spec.ContentType)
	}
	if spec.Accept != "" {
		req.Header.Set("Accept", spec.Accept)
	}

	resp, err := client.Do(req)
	if err != nil {
		return colaptHTTPResult{}, fmt.Errorf("可乐站请求失败（%s %s）: %w", spec.Method, spec.URL, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return colaptHTTPResult{Status: resp.StatusCode, Body: raw}, nil
}

// colaptOriginFromURL 从接口地址反推站点同源地址（用于刷新 CSRF 时复用同一鉴权）。
func colaptOriginFromURL(rawURL string) string {
	if index := strings.Index(rawURL, "/api/v1"); index > 0 {
		return rawURL[:index]
	}
	return rawURL
}

// colaptDoJSON 发起一次 JSON 请求并返回原始状态码与响应体（4xx/5xx 不当作传输错误）。
func colaptDoJSON(client *http.Client, method string, url string, cfg colaptConfig, auth *colaptAuth, payload any) (colaptHTTPResult, error) {
	var encoded []byte
	if payload != nil {
		marshaled, err := json.Marshal(payload)
		if err != nil {
			return colaptHTTPResult{}, fmt.Errorf("构造可乐站请求体失败: %w", err)
		}
		encoded = marshaled
	}
	// 会话模式的写操作由 colaptSend 统一补 CSRF 令牌。
	request := colaptRequest{Method: method, URL: url, Body: encoded, Accept: "application/json"}
	if len(encoded) > 0 {
		request.ContentType = "application/json"
	}
	return colaptSend(client, cfg, auth, request)
}

// colaptErrorFromResponse 把站点错误体转成可读错误。
// 站点硬性拒绝（查重降质/重复）包装成 *publisher.PreCheckError，让上层判为「跳过」而不是「失败重试」。
func colaptErrorFromResponse(result colaptHTTPResult, stage string) error {
	code := colaptErrorCode(result)
	message := colaptErrorMessage(result)
	detail := fmt.Sprintf("可乐站%s失败：HTTP %d code=%s %s", stage, result.Status, code, message)
	if fields := colaptErrorFields(result); fields != "" {
		detail += " fields=" + fields
	}

	switch code {
	case "dupe_lower_quality", "dupe_same_quality", "dupe_lower_resolution":
		return publisher.NewPreCheckErrorWithDetail("可乐站查重拒绝："+message, detail)
	case "api_key_required", "api_key_scope_denied", "authentication_required", "permission_denied", "scope_not_granted":
		// 凭据被拒：上层会据此尝试换通道（Key → Cookie 会话）。
		return &authRejection{detail: detail + "（凭据不被站点接受：API Key 需账号先获得「自动发种」资格，网页 Cookie 需包含 reelcore_session）"}
	default:
		return fmt.Errorf("%s", detail)
	}
}

// colaptErrorCode 取站点错误体的 code 字段。
func colaptErrorCode(result colaptHTTPResult) string {
	payload := colaptErrorPayload(result.Body)
	return toStringAny(payload["code"], "")
}

// colaptErrorMessage 取站点错误体的 message 字段。
func colaptErrorMessage(result colaptHTTPResult) string {
	payload := colaptErrorPayload(result.Body)
	return toStringAny(payload["message"], summarizeResponseBody(string(result.Body)))
}

// colaptErrorFields 把站点错误体的 fields（字段 → 原因数组）压成一行文本。
func colaptErrorFields(result colaptHTTPResult) string {
	payload := colaptErrorPayload(result.Body)
	fields, ok := payload["fields"].(map[string]any)
	if !ok || len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for name, value := range fields {
		if reasons, ok := value.([]any); ok {
			texts := make([]string, 0, len(reasons))
			for _, reason := range reasons {
				texts = append(texts, toStringAny(reason, ""))
			}
			parts = append(parts, fmt.Sprintf("%s[%s]", name, strings.Join(texts, "; ")))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s[%s]", name, toStringAny(value, "")))
	}
	return strings.Join(parts, " ")
}

// colaptErrorPayload 解析站点错误体里的 error 对象；解析失败返回空 map。
func colaptErrorPayload(body []byte) map[string]any {
	parsed := map[string]any{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return map[string]any{}
	}
	errorPayload, ok := parsed["error"].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return errorPayload
}

// colaptWarnings 取响应里的站点提示（同档位 hint / 发布告警），转成文本列表。
func colaptWarnings(payload map[string]any) []string {
	if payload == nil {
		return nil
	}
	raw, ok := payload["warnings"].([]any)
	if !ok || len(raw) == 0 {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		warning, ok := item.(map[string]any)
		if !ok {
			result = append(result, toStringAny(item, ""))
			continue
		}
		code := toStringAny(warning["code"], "")
		message := toStringAny(warning["message"], "")
		if strings.TrimSpace(message) == "" {
			message = code
		} else if strings.TrimSpace(code) != "" {
			message = code + "：" + message
		}
		if strings.TrimSpace(message) != "" {
			result = append(result, message)
		}
	}
	return result
}

// colaptEncodeJSON 把响应对象回写成 JSON 文本，仅用于错误提示。
func colaptEncodeJSON(payload map[string]any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

// colaptPick 按站点字典取值：先按完整键（medium.bluray）查，再按去前缀键（bluray）查。
// 返回值与是否命中；未命中时返回 fallback。
func colaptPick(mapping map[string]string, raw string, prefix string, fallback string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		return fallback, false
	}
	if value, ok := mapping[normalized]; ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	stripped := strings.TrimPrefix(normalized, strings.ToLower(strings.TrimSpace(prefix)))
	if stripped != normalized {
		if value, ok := mapping[stripped]; ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return fallback, false
}

// colaptIdempotencyKey 生成站点会话写操作要求的幂等键（16~128 个可见 ASCII 字符）。
// 说明：站点用它区分「重放」与「新操作」，每次写请求都必须是新值。
func colaptIdempotencyKey() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// 随机源不可用时退回时间戳，仍满足长度与字符集要求。
		return strconv.FormatInt(time.Now().UnixNano(), 36) + "-ptnexus"
	}
	return hex.EncodeToString(buf)
}

// colaptFingerprint 生成 API Key 的脱敏指纹（只暴露前 6 位与长度），用于日志核对。
func colaptFingerprint(apiKey string) string {
	trimmed := strings.TrimSpace(apiKey)
	if trimmed == "" {
		return "(空)"
	}
	if len(trimmed) <= 6 {
		return fmt.Sprintf("%s***(len=%d)", trimmed[:len(trimmed)/2], len(trimmed))
	}
	return fmt.Sprintf("%s***(len=%d)", trimmed[:6], len(trimmed))
}

// colaptTruncateUTF8 按 UTF-8 字节上限截断，且不切断多字节字符。
func colaptTruncateUTF8(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	truncated := text[:limit]
	for len(truncated) > 0 && !isValidUTF8Tail(truncated) {
		truncated = truncated[:len(truncated)-1]
	}
	return truncated
}

// isValidUTF8Tail 判断截断点是否落在完整字符边界上。
func isValidUTF8Tail(text string) bool {
	return strings.ToValidUTF8(text, "") == text
}

// appendUniqueString 追加去重字符串。
func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// int64FromAny 把接口返回的数字字段转成 int64（兼容 float64/json.Number/字符串）。
func int64FromAny(value any, fallback int64) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return parsed
		}
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}

// loadColaptConfig 读取 server/configs/colapt.yaml 并合并到内置兜底配置之上。
func loadColaptConfig() colaptConfig {
	cfg := colaptDefaultConfig
	// 兜底配置里的 map 是引用类型，直接合并会污染全局，先逐张拷贝。
	cfg.Medium = cloneColaptMap(cfg.Medium)
	cfg.Resolution = cloneColaptMap(cfg.Resolution)
	cfg.VideoCodec = cloneColaptMap(cfg.VideoCodec)
	cfg.HDR = cloneColaptMap(cfg.HDR)
	cfg.Audio = cloneColaptMap(cfg.Audio)
	cfg.Category = cloneColaptMap(cfg.Category)
	cfg.Subtitles = cloneColaptMap(cfg.Subtitles)

	paths := config.ResolveRuntimePaths()
	data, err := os.ReadFile(filepath.Join(paths.BaseDir, "configs", "colapt.yaml"))
	if err != nil {
		return cfg
	}
	var override colaptConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return cfg
	}

	if value := strings.TrimSpace(override.SiteName); value != "" {
		cfg.SiteName = value
	}
	if value := strings.TrimSpace(override.APIBase); value != "" {
		cfg.APIBase = value
	}
	if value := strings.TrimSpace(override.APIKeyHeader); value != "" {
		cfg.APIKeyHeader = value
	}
	if value := strings.TrimSpace(override.UploadPath); value != "" {
		cfg.UploadPath = value
	}
	if value := strings.TrimSpace(override.TorrentPath); value != "" {
		cfg.TorrentPath = value
	}
	if value := strings.TrimSpace(override.MetadataPath); value != "" {
		cfg.MetadataPath = value
	}
	if value := strings.TrimSpace(override.SubmitPath); value != "" {
		cfg.SubmitPath = value
	}
	if value := strings.TrimSpace(override.PublishPage); value != "" {
		cfg.PublishPage = value
	}
	cfg.Anonymous = override.Anonymous

	mergeColaptMap(cfg.Medium, override.Medium)
	mergeColaptMap(cfg.Resolution, override.Resolution)
	mergeColaptMap(cfg.VideoCodec, override.VideoCodec)
	mergeColaptMap(cfg.HDR, override.HDR)
	mergeColaptMap(cfg.Audio, override.Audio)
	mergeColaptMap(cfg.Category, override.Category)
	mergeColaptMap(cfg.Subtitles, override.Subtitles)

	if value := strings.TrimSpace(override.Defaults.Medium); value != "" {
		cfg.Defaults.Medium = value
	}
	if value := strings.TrimSpace(override.Defaults.Resolution); value != "" {
		cfg.Defaults.Resolution = value
	}
	if value := strings.TrimSpace(override.Defaults.VideoCodec); value != "" {
		cfg.Defaults.VideoCodec = value
	}
	if value := strings.TrimSpace(override.Defaults.HDR); value != "" {
		cfg.Defaults.HDR = value
	}
	if value := strings.TrimSpace(override.Defaults.Audio); value != "" {
		cfg.Defaults.Audio = value
	}
	if value := strings.TrimSpace(override.Defaults.Category); value != "" {
		cfg.Defaults.Category = value
	}
	return cfg
}

// cloneColaptMap 复制字典，避免合并污染全局兜底配置。
func cloneColaptMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// mergeColaptMap 把 override 合并进 target（空值不覆盖）。
func mergeColaptMap(target map[string]string, override map[string]string) {
	for key, value := range override {
		if strings.TrimSpace(value) == "" {
			continue
		}
		target[key] = value
	}
}
