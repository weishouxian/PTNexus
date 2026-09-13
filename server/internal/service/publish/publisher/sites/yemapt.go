package sites

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
	"gopkg.in/yaml.v3"
)

// yemaptConfig 定义 yemapt 发种接口的专属字典映射。
// 说明：yemapt 不是 NexusPHP 站点，发种走自研 REST API（/api/torrent/addTorrent），
// 因此无法复用 configs 的 SitePublishConfig，这里使用独立的配置结构。
type yemaptConfig struct {
	APIPath  string            `yaml:"api_path"`
	Category map[string]string `yaml:"category"`
	Medium   map[string]string `yaml:"medium"`
	Standard map[string]string `yaml:"standard"`
	Codec    map[string]string `yaml:"codec"`
	Audio    map[string]string `yaml:"audiocodec"`
	Region   map[string]string `yaml:"region"`
	Team     map[string]string `yaml:"team"`
	Tags     map[string]string `yaml:"tags"`
	Defaults yemaptDefaults    `yaml:"defaults"`
}

type yemaptDefaults struct {
	Category string `yaml:"category"`
	Medium   string `yaml:"medium"`
	Standard string `yaml:"standard"`
	Codec    string `yaml:"codec"`
	Audio    string `yaml:"audiocodec"`
	Region   string `yaml:"region"`
	Team     string `yaml:"team"`
}

// yemaptDefaultConfig 为「4K UHD BluRay Remux 电影」实测值，作为无法直接命中映射时的兜底默认值。
// 发布其它类型前，请对照发种页表单在 server/configs/yemapt.yaml 中补充数字字典。
var yemaptDefaultConfig = yemaptConfig{
	APIPath:  "/api/torrent/addTorrent",
	Category: map[string]string{"movie": "4"},
	Medium:   map[string]string{"uhd_bluray": "4", "remux": "4", "bluray": "4"},
	Standard: map[string]string{"2160p": "7"},
	Codec:    map[string]string{"x265": "2", "hevc": "2"},
	Audio:    map[string]string{"dts_hd_ma": "4", "dts": "4", "truehd": "4"},
	Region:   map[string]string{"us": "4", "usa": "4", "gb": "4", "uk": "4"},
	Team:     map[string]string{"none": "999"},
	Tags:     map[string]string{},
	Defaults: yemaptDefaults{
		Category: "4",
		Medium:   "4",
		Standard: "7",
		Codec:    "2",
		Audio:    "4",
		Region:   "4",
		Team:     "999",
	},
}

// PublishYemaPT 将种子发布到 yemapt（自研 API 站点）。
// 参数/返回：input 为统一发布输入；返回发布结果（详情页链接、过程日志等）。
// 失败场景：缺少 base_url/cookie/torrent、读取种子失败、接口返回 success=false 时返回 error。
// 副作用：读取本地种子文件并向 yemapt 发起 multipart 上传请求。
func PublishYemaPT(input publisher.PublishInput) (publisher.PublishResult, error) {
	baseURL := normalizeBaseURL(input.BaseURL)
	cookie := strings.TrimSpace(input.Cookie)

	if baseURL == "" {
		return publisher.PublishResult{}, fmt.Errorf("yemapt 发种缺少 base_url")
	}
	if cookie == "" {
		return publisher.PublishResult{}, fmt.Errorf("yemapt 发种缺少 cookie（请先在 webui 填写 yemapt 的 auth/b_auth cookie）")
	}

	cfg := loadYemaPTConfig()

	// 对齐 PublishPublic：UPLOAD_TEST_MODE=true 时跳过真实发种，返回模拟成功。
	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		return publisher.PublishResult{
			PublishURL:       strings.TrimRight(baseURL, "/") + "/torrent/999999999",
			AttemptDetailLog: fmt.Sprintf("--- [yemapt] 测试模式：跳过实际发种（目标 %s）---", strings.TrimSpace(input.TargetName)),
		}, nil
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return publisher.PublishResult{}, fmt.Errorf("yemapt 发种缺少标题")
	}

	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return publisher.PublishResult{}, fmt.Errorf("yemapt 发种缺少种子文件路径")
	}
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("读取种子文件失败: %w", err)
	}

	std := map[string]any{}
	if s, ok := input.UploadData["standardized_params"].(map[string]any); ok && s != nil {
		std = s
	}

	poster := strings.TrimSpace(resolveUploadSection(input.UploadData, "poster"))
	imdb := extractYemaPTIMDbID(strings.TrimSpace(input.IMDbLink))
	douban := extractYemaPTDoubanID(strings.TrimSpace(input.DoubanLink))
	anonymousEnabled := publisher.ResolveAnonymousUploadEnabled(input.RootConfig)

	textFields := map[string]string{
		"showName": title,
		"shortDesc": strings.TrimSpace(input.Subtitle),
		"longDesc": strings.TrimSpace(input.Description),
		"mediaInfo": strings.TrimSpace(input.MediaInfo),
		"categoryId": pickYemaPTValue(cfg.Category, toStringAny(std["type"], ""), cfg.Defaults.Category),
		"medium": pickYemaPTValue(cfg.Medium, normalizeYemaPTParam(toStringAny(std["medium"], ""), "medium."), cfg.Defaults.Medium),
		"standard": pickYemaPTValue(cfg.Standard, normalizeYemaPTParam(toStringAny(std["resolution"], ""), "resolution.r"), cfg.Defaults.Standard),
		"codec": pickYemaPTValue(cfg.Codec, strings.ToLower(strings.TrimSpace(toStringAny(std["video_codec"], ""))), cfg.Defaults.Codec),
		"audiocodec": pickYemaPTValue(cfg.Audio, strings.ToLower(strings.TrimSpace(toStringAny(std["audio_codec"], ""))), cfg.Defaults.Audio),
		"regionList": pickYemaPTValue(cfg.Region, strings.ToLower(strings.TrimSpace(toStringAny(std["source"], ""))), cfg.Defaults.Region),
		"team": pickYemaPTTeam(cfg.Team, strings.ToLower(strings.TrimSpace(toStringAny(std["team"], ""))), cfg.Defaults.Team),
	}
	if poster != "" {
		textFields["picture"] = poster
	}
	if imdb != "" {
		textFields["imdb"] = imdb
	}
	if douban != "" {
		textFields["douban"] = douban
	}
	if anonymousEnabled {
		textFields["uploadUserAnonymous"] = "y"
	} else {
		textFields["uploadUserAnonymous"] = "n"
	}

	// 标签：重复字段 tagList
	tagIDs := resolveYemaPTTagIDs(cfg, input.UploadData)

	uploadURL := strings.TrimRight(baseURL, "/") + cfg.APIPath
	logLines := []string{
		fmt.Sprintf("--- [yemapt] 开始发布到 %s ---", strings.TrimSpace(input.TargetName)),
		fmt.Sprintf("上传地址: %s", uploadURL),
		fmt.Sprintf("字段摘要: showName=%q categoryId=%s medium=%s standard=%s codec=%s audiocodec=%s regionList=%s team=%s tags=%v anonymous=%s",
			title, textFields["categoryId"], textFields["medium"], textFields["standard"], textFields["codec"], textFields["audiocodec"], textFields["regionList"], textFields["team"], tagIDs, textFields["uploadUserAnonymous"]),
	}

	publishURL, attemptDetail, publishErr := postYemaPTTorrent(uploadURL, baseURL, cookie, textFields, tagIDs, torrentBytes, filepath.Base(torrentPath))
	if strings.TrimSpace(attemptDetail) != "" {
		logLines = append(logLines, attemptDetail)
	}

	if publishErr != nil {
		logLines = append(logLines, fmt.Sprintf("发布结果：发布到 yemapt 失败: %v", publishErr))
		return publisher.PublishResult{
			AttemptDetailLog: strings.Join(logLines, "\n"),
		}, publishErr
	}

	logLines = append(logLines, "发布结果：成功发布到 yemapt")
	return publisher.PublishResult{
		PublishURL:       publishURL,
		AttemptDetailLog: strings.Join(logLines, "\n"),
	}, nil
}

// postYemaPTTorrent 以 multipart/form-data 提交发种请求到 yemapt 接口。
func postYemaPTTorrent(uploadURL, baseURL, cookie string, textFields map[string]string, tagIDs []string, torrentBytes []byte, torrentName string) (string, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 文本字段（shortDesc/picture 等可能为空，yemapt 允许则照发；为空时不加）
	for key, value := range textFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return "", "", fmt.Errorf("构造表单字段失败 %s: %w", key, err)
		}
	}
	// 标签重复字段
	for _, id := range tagIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := writer.WriteField("tagList", id); err != nil {
			return "", "", fmt.Errorf("构造 tagList 字段失败: %w", err)
		}
	}
	// 种子文件
	part, err := writer.CreateFormFile("file", torrentName)
	if err != nil {
		return "", "", fmt.Errorf("构造文件字段失败: %w", err)
	}
	if _, err := part.Write(torrentBytes); err != nil {
		return "", "", fmt.Errorf("写入种子文件失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", "", fmt.Errorf("结束表单写入失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, body)
	if err != nil {
		return "", "", fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Origin", strings.TrimRight(baseURL, "/"))
	req.Header.Set("Referer", strings.TrimRight(baseURL, "/")+"/")
	req.Header.Set("Cookie", cookie)

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("请求 yemapt 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("读取响应失败: %w", err)
	}
	raw := strings.TrimSpace(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Sprintf("HTTP %d 响应: %s", resp.StatusCode, summarizeResponseBody(raw)), fmt.Errorf("yemapt 返回 HTTP %d", resp.StatusCode)
	}

	var parsed yemaptAPIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// 非 JSON（罕见）：把原始响应返回，方便排查
		return "", fmt.Sprintf("响应(非 JSON): %s", summarizeResponseBody(raw)), fmt.Errorf("yemapt 响应解析失败: %w", err)
	}
	if !parsed.Success {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(parsed.Msg)
		}
		if msg == "" {
			msg = summarizeResponseBody(raw)
		}
		return "", fmt.Sprintf("接口返回失败: %s", msg), fmt.Errorf("yemapt 接口返回 success=false: %s", msg)
	}

	publishURL := ""
	if parsed.Data.ID != 0 {
		publishURL = strings.TrimRight(baseURL, "/") + "/torrent/" + fmt.Sprintf("%d", parsed.Data.ID)
	} else if parsed.Data.TorrentID != 0 {
		publishURL = strings.TrimRight(baseURL, "/") + "/torrent/" + fmt.Sprintf("%d", parsed.Data.TorrentID)
	}
	return publishURL, fmt.Sprintf("接口返回成功: %s", summarizeResponseBody(raw)), nil
}

type yemaptAPIResponse struct {
	Success  bool   `json:"success"`
	ShowType int    `json:"showType"`
	Message  string `json:"message"`
	Msg      string `json:"msg"`
	Data     struct {
		ID        int    `json:"id"`
		TorrentID int    `json:"torrentId"`
		DetailURL string `json:"detailUrl"`
	} `json:"data"`
}

// loadYemaPTConfig 读取 server/configs/yemapt.yaml 并将其中字典合并到默认配置之上。
func loadYemaPTConfig() yemaptConfig {
	cfg := yemaptDefaultConfig
	paths := config.ResolveRuntimePaths()
	data, err := os.ReadFile(filepath.Join(paths.BaseDir, "configs", "yemapt.yaml"))
	if err != nil {
		return cfg
	}
	var override yemaptConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return cfg
	}
	if strings.TrimSpace(override.APIPath) != "" {
		cfg.APIPath = override.APIPath
	}
	mergeYemaPTMap(cfg.Category, override.Category)
	mergeYemaPTMap(cfg.Medium, override.Medium)
	mergeYemaPTMap(cfg.Standard, override.Standard)
	mergeYemaPTMap(cfg.Codec, override.Codec)
	mergeYemaPTMap(cfg.Audio, override.Audio)
	mergeYemaPTMap(cfg.Region, override.Region)
	mergeYemaPTMap(cfg.Team, override.Team)
	mergeYemaPTMap(cfg.Tags, override.Tags)
	if override.Defaults.Category != "" {
		cfg.Defaults.Category = override.Defaults.Category
	}
	if override.Defaults.Medium != "" {
		cfg.Defaults.Medium = override.Defaults.Medium
	}
	if override.Defaults.Standard != "" {
		cfg.Defaults.Standard = override.Defaults.Standard
	}
	if override.Defaults.Codec != "" {
		cfg.Defaults.Codec = override.Defaults.Codec
	}
	if override.Defaults.Audio != "" {
		cfg.Defaults.Audio = override.Defaults.Audio
	}
	if override.Defaults.Region != "" {
		cfg.Defaults.Region = override.Defaults.Region
	}
	if override.Defaults.Team != "" {
		cfg.Defaults.Team = override.Defaults.Team
	}
	return cfg
}

func mergeYemaPTMap(base, override map[string]string) {
	if len(override) == 0 {
		return
	}
	for key, value := range override {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		base[trimmedKey] = trimmedValue
	}
}

// pickYemaPTValue 在映射表中按标准化值查找 yemapt 数字 ID，未命中返回兜底默认值。
func pickYemaPTValue(mapping map[string]string, standardized, fallback string) string {
	key := strings.ToLower(strings.TrimSpace(standardized))
	if key == "" {
		return strings.TrimSpace(fallback)
	}
	if mapped, ok := mapping[key]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped)
	}
	// 去掉常见前缀再试一次
	if idx := strings.Index(key, "."); idx >= 0 {
		suffix := key[idx+1:]
		if mapped, ok := mapping[suffix]; ok && strings.TrimSpace(mapped) != "" {
			return strings.TrimSpace(mapped)
		}
	}
	return strings.TrimSpace(fallback)
}

// pickYemaPTTeam 解析制作小组：为空时回退到「无小组」默认值。
func pickYemaPTTeam(mapping map[string]string, team, fallback string) string {
	if strings.TrimSpace(team) == "" {
		return strings.TrimSpace(fallback)
	}
	if mapped, ok := mapping[team]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped)
	}
	return strings.TrimSpace(fallback)
}

// normalizeYemaPTParam 去掉 PTNexus 标准化值的前缀（如 medium. / resolution.r）。
func normalizeYemaPTParam(raw, prefix string) string {
	trimmed := strings.TrimSpace(raw)
	if prefix != "" && strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(prefix)) {
		return trimmed[len(prefix):]
	}
	return trimmed
}

// resolveYemaPTTagIDs 把 PTNexus 标签映射为 yemapt 标签 ID（未在配置中配置的标签会被忽略）。
func resolveYemaPTTagIDs(cfg yemaptConfig, uploadData map[string]any) []string {
	tags := resolveSiteCombinedTags(uploadData)
	if len(tags) == 0 {
		return nil
	}
	ids := make([]string, 0, len(tags))
	seen := map[string]struct{}{}
	for tag := range tags {
		key := strings.ToLower(strings.TrimSpace(tag))
		key = strings.TrimPrefix(key, "tag.")
		if key == "" {
			continue
		}
		id, ok := cfg.Tags[key]
		if !ok || strings.TrimSpace(id) == "" {
			continue
		}
		normID := strings.TrimSpace(id)
		if _, exists := seen[normID]; exists {
			continue
		}
		seen[normID] = struct{}{}
		ids = append(ids, normID)
	}
	return ids
}

var (
	reYemaPTIMDb = regexp.MustCompile(`tt(\d+)`)
	reYemaPTDouban = regexp.MustCompile(`subject/(\d+)`)
)

// extractYemaPTIMDbID 从 IMDb 链接或 ID 中提取纯数字部分（保留前导零，例如 tt0089893 -> 0089893）。
func extractYemaPTIMDbID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := reYemaPTIMDb.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	if isAllDigits(trimmed) {
		return trimmed
	}
	return ""
}

// extractYemaPTDoubanID 从豆瓣链接或数字中提取 subject 数字 ID。
func extractYemaPTDoubanID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if m := reYemaPTDouban.FindStringSubmatch(trimmed); m != nil {
		return m[1]
	}
	if isAllDigits(trimmed) {
		return trimmed
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
