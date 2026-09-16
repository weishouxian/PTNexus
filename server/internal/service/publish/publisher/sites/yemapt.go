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
	"strconv"
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

// yemaptDefaultConfig 为 configs/yemapt.yaml 缺失或解析失败时的兜底字典。
// 取值来自站点权威选项表（GET /api/torrent/fetchUploadOptions，2026-09-17 拉取），
// 完整字典与选项表以 server/configs/yemapt.yaml 为准，改动请同步两处。
var yemaptDefaultConfig = yemaptConfig{
	APIPath: "/api/torrent/addTorrent",
	// 分类：标准化值形如 category.movie / category.tv_series（见 configs/global_mappings.yaml），
	// 键写去前缀后的形态。
	Category: map[string]string{
		"movie": "4", "tv_series": "5", "drama": "5", "tv_shows": "13",
		"animation": "14", "cartoon": "14",
		"documentary": "15", "documentaries": "15", "document": "15",
		"sports": "17", "playlet": "6", "mv": "16",
		"music": "8", "audiobook": "9", "books": "12", "ebook": "12",
		"game": "10", "software": "3", "os": "3", "stage": "22", "other": "22",
	},
	// 媒介：标准化值形如 medium.remux / medium.uhd_bluray。
	// 注意 4 是 Remux、3 才是 Blu-ray UHD 原盘（早期版本把 uhd_bluray 写成 4 是错的）。
	Medium: map[string]string{
		"webdl": "1", "webrip": "1",
		"bluray": "2", "uhd_bluray": "3", "uhd_diy": "3",
		"remux":  "4",
		"encode": "5", "bdrip": "5", "minibd": "5", "minisd": "5",
		"hdtv": "6", "uhdtv": "6", "tv": "6", "tvrip": "6",
		"dvdr": "7", "cd": "8", "sacd": "8", "vinyl": "8", "track": "8",
		"dvd": "9", "hddvd": "999", "vcd": "999", "other": "999",
	},
	// 分辨率：标准化值形如 resolution.r2160p，前缀 resolution.r 会先被去掉。
	Standard: map[string]string{
		"720i": "1", "720p": "2", "1080i": "3", "1080p": "4",
		"sd": "5", "1440p": "6", "2160p": "7", "4320p": "8", "other": "999",
	},
	// 视频编码：标准化值为 video.h265 / video.h264，去前缀后缀是 h265/h264，
	// 因此必须按 h265/h264 建键（只写 x265/hevc 会命中失败）。
	Codec: map[string]string{
		"h264": "1", "x264": "1", "h265": "2", "x265": "2",
		"vc1": "3", "mpeg2": "6", "xvid": "7", "av1": "8", "vp9": "9", "h266": "10",
		"h261": "999", "mpeg1": "999", "mpeg4": "999", "other": "999",
	},
	// 音频编码：标准化值为 audio.ac3 / audio.dts_hd_ma 等。
	// 4 是 DTS-HD MA 而非通用兜底值，DTS→3、TrueHD→7、AC3→2（曾把三者都写成 4）。
	Audio: map[string]string{
		"aac": "1", "m4a": "1", "ac3": "2", "dts": "3",
		"dts_hd": "4", "dts_hd_hr": "4", "dts_hd_ma": "4", "dtsx": "4",
		"ddp": "5", "ddp_atmos": "6", "truehd": "7", "truehd_atmos": "8",
		"lpcm": "9", "pcm": "9", "wav": "9", "flac": "10", "alac": "10", "ape": "11",
		"mp3": "12", "ogg": "13", "opus": "14",
		"dsd": "999", "tta": "999", "taa": "999", "other": "999",
	},
	// 地区：标准化值形如 source.china / source.japan（见 configs/global_mappings.yaml），
	// 键必须同时覆盖「完整值」与「去前缀后缀」两种写法，否则命中失败会回落到默认地区。
	Region: map[string]string{
		"source.china": "1", "source.hongkong": "2", "source.hongkong_taiwan": "2",
		"source.taiwan": "3", "source.western": "4", "source.canada": "4",
		"source.uk": "5", "source.europe": "5", "source.france": "5", "source.germany": "5",
		"source.italy": "5", "source.spain": "5", "source.sweden": "5", "source.denmark": "5",
		"source.russia": "5", "source.japan": "6", "source.korea": "7",
		"source.australia": "999", "source.brazil": "999", "source.india": "999",
		"source.malaysia": "999", "source.singapore": "999", "source.thailand": "999",
		"source.other": "999",
		"china":        "1", "hongkong": "2", "hongkong_taiwan": "2", "taiwan": "3",
		"western": "4", "canada": "4", "uk": "5", "europe": "5", "france": "5", "germany": "5",
		"japan": "6", "korea": "7", "other": "999",
	},
	// 制作小组：站点只提供 10 个小组，其余一律是 Other(999)。
	Team: map[string]string{
		"ourbits": "1", "btshd": "2", "btstv": "3", "hdchina": "4", "cmct": "5",
		"hhweb": "6", "frds": "7", "mteam": "8", "qhstudio": "9", "ubits": "10",
		"none": "999", "other": "999",
	},
	// 标签：键为 PTNexus 标签名（比较时统一转小写），未配置的标签会被忽略。
	Tags: map[string]string{
		"禁转": "1", "禁止转载": "1", "首发": "2", "官组": "3", "diy": "4",
		"国语": "5", "中字": "6", "粤语": "7", "英字": "8",
		"hdr10": "9", "杜比视界": "10", "dolby vision": "10", "dv": "10",
		"连载中": "11", "完结": "12", "多国字幕": "13", "hdr10+": "14",
		"杜比全景声": "15", "atmos": "15", "dolby atmos": "15",
		"dts-x": "16", "dtsx": "16", "5.1声道": "17", "7.1声道": "17",
		"完结全集": "18", "sp": "19", "剧场版": "19", "ova": "19",
	},
	// 兜底值：站点对 medium/standard/codec/audiocodec/region/team 都提供 Other(999)，
	// 未知项一律发 999，而不是假装是某个具体规格（例如把未知分辨率写成 2160p）。
	Defaults: yemaptDefaults{
		Category: "4",
		Medium:   "999",
		Standard: "999",
		Codec:    "999",
		Audio:    "999",
		Region:   "999",
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
			PublishURL:       strings.TrimRight(baseURL, "/") + "/#/torrent/detail/999999999",
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

	poster := stripYemaPTImageTags(resolveUploadSection(input.UploadData, "poster"))
	// imdb 参数必须是纯数字：yemapt 的 imdb 字段不接受 tt 前缀、也不接受完整链接
	// （站点详情实测返回 "imdb":"0206013"，对应 https://www.imdb.com/title/tt0206013/）。
	imdb := resolveYemaPTIMDbParam(input, std)
	douban := extractYemaPTDoubanID(strings.TrimSpace(input.DoubanLink))
	anonymousEnabled := publisher.ResolveAnonymousUploadEnabled(input.RootConfig)

	// 各维度统一走 pickYemaPTValueEx：标准化值形如 source.china / category.tv_series / video.h265，
	// 一旦与 configs/yemapt.yaml 的字典键不一致就会静默回落到 defaults，因此未命中的维度必须记录并告警。
	fallbackNotes := make([]string, 0, 4)
	pick := func(label string, mapping map[string]string, raw, fallback string) string {
		value, matched := pickYemaPTValueEx(mapping, raw, fallback)
		if !matched && strings.TrimSpace(raw) != "" {
			fallbackNotes = append(fallbackNotes, fmt.Sprintf(
				"⚠️ %s 映射未命中：标准化值 %q 不在 configs/yemapt.yaml 对应字典中，已回落默认值 %s。请核对发种页表单后补充映射。",
				label, strings.TrimSpace(raw), value,
			))
		}
		return value
	}

	textFields := map[string]string{
		"showName":   title,
		"shortDesc":  strings.TrimSpace(input.Subtitle),
		"longDesc":   normalizeYemaPTDescription(strings.TrimSpace(input.Description)),
		"mediaInfo":  strings.TrimSpace(input.MediaInfo),
		"categoryId": pick("分类(categoryId)", cfg.Category, toStringAny(std["type"], ""), cfg.Defaults.Category),
		"medium":     pick("媒介(medium)", cfg.Medium, normalizeYemaPTParam(toStringAny(std["medium"], ""), "medium."), cfg.Defaults.Medium),
		"standard":   pick("分辨率(standard)", cfg.Standard, normalizeYemaPTParam(toStringAny(std["resolution"], ""), "resolution.r"), cfg.Defaults.Standard),
		"codec":      pick("视频编码(codec)", cfg.Codec, strings.ToLower(strings.TrimSpace(toStringAny(std["video_codec"], ""))), cfg.Defaults.Codec),
		"audiocodec": pick("音频编码(audiocodec)", cfg.Audio, strings.ToLower(strings.TrimSpace(toStringAny(std["audio_codec"], ""))), cfg.Defaults.Audio),
		"regionList": pick("地区(regionList)", cfg.Region, strings.ToLower(strings.TrimSpace(toStringAny(std["source"], ""))), cfg.Defaults.Region),
		"team":       pickYemaPTTeam(cfg.Team, strings.ToLower(strings.TrimSpace(toStringAny(std["team"], ""))), cfg.Defaults.Team),
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
		fmt.Sprintf("字段摘要: showName=%q picture=%q categoryId=%s medium=%s standard=%s codec=%s audiocodec=%s regionList=%s team=%s tags=%v anonymous=%s",
			title, textFields["picture"], textFields["categoryId"], textFields["medium"], textFields["standard"], textFields["codec"], textFields["audiocodec"], textFields["regionList"], textFields["team"], tagIDs, textFields["uploadUserAnonymous"]),
		fmt.Sprintf("外链字段: imdb=%q douban=%q（imdb 只发纯数字，已去掉 tt 前缀）", imdb, douban),
	}
	logLines = append(logLines, fallbackNotes...)

	publishURL, downloadURL, attemptDetail, publishErr := postYemaPTTorrent(uploadURL, baseURL, cookie, textFields, tagIDs, torrentBytes, filepath.Base(torrentPath))
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
		PublishURL:        publishURL,
		DirectDownloadURL: downloadURL,
		AttemptDetailLog:  strings.Join(logLines, "\n"),
	}, nil
}

// postYemaPTTorrent 以 multipart/form-data 提交发种请求到 yemapt 接口。
func postYemaPTTorrent(uploadURL, baseURL, cookie string, textFields map[string]string, tagIDs []string, torrentBytes []byte, torrentName string) (string, string, string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// 文本字段（shortDesc/picture 等可能为空，yemapt 允许则照发；为空时不加）
	for key, value := range textFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return "", "", "", fmt.Errorf("构造表单字段失败 %s: %w", key, err)
		}
	}
	// 标签重复字段
	for _, id := range tagIDs {
		if strings.TrimSpace(id) == "" {
			continue
		}
		if err := writer.WriteField("tagList", id); err != nil {
			return "", "", "", fmt.Errorf("构造 tagList 字段失败: %w", err)
		}
	}
	// 种子文件
	part, err := writer.CreateFormFile("file", torrentName)
	if err != nil {
		return "", "", "", fmt.Errorf("构造文件字段失败: %w", err)
	}
	if _, err := part.Write(torrentBytes); err != nil {
		return "", "", "", fmt.Errorf("写入种子文件失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", "", "", fmt.Errorf("结束表单写入失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, body)
	if err != nil {
		return "", "", "", fmt.Errorf("构造请求失败: %w", err)
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
		return "", "", "", fmt.Errorf("请求 yemapt 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", fmt.Errorf("读取响应失败: %w", err)
	}
	raw := strings.TrimSpace(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", fmt.Sprintf("HTTP %d 响应: %s", resp.StatusCode, summarizeResponseBody(raw)), fmt.Errorf("yemapt 返回 HTTP %d", resp.StatusCode)
	}

	var parsed yemaptAPIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		// 非 JSON（罕见）：把原始响应返回，方便排查
		return "", "", fmt.Sprintf("响应(非 JSON): %s", summarizeResponseBody(raw)), fmt.Errorf("yemapt 响应解析失败: %w", err)
	}
	if !parsed.Success {
		msg := strings.TrimSpace(parsed.Message)
		if msg == "" {
			msg = strings.TrimSpace(parsed.Msg)
		}
		if msg == "" {
			msg = summarizeResponseBody(raw)
		}
		return "", "", fmt.Sprintf("接口返回失败: %s", msg), fmt.Errorf("yemapt 接口返回 success=false: %s", msg)
	}

	torrentID, detailURL, downloadURL := resolveYemaPTTorrentResult(parsed.Data, baseURL)
	return detailURL, downloadURL, fmt.Sprintf("接口返回成功: data=%d %s", torrentID, summarizeResponseBody(raw)), nil
}

type yemaptAPIResponse struct {
	Success  bool            `json:"success"`
	ShowType int             `json:"showType"`
	Message  string          `json:"message"`
	Msg      string          `json:"msg"`
	Data     json.RawMessage `json:"data"`
}

// yemaptTorrentData 为发种成功时 data 字段（对象形态）的字段定义。
// 实测 yemapt 的 data 为裸整数（即种子 ID），此处同时兼容对象形态（含 detailUrl/downloadUrl 等）。
type yemaptTorrentData struct {
	ID          int    `json:"id"`
	TorrentID   int    `json:"torrentId"`
	DetailURL   string `json:"detailUrl"`
	DownloadURL string `json:"downloadUrl"`
}

// resolveYemaPTTorrentResult 从响应 data 中提取种子 ID 与详情/下载链接。
// data 可能是裸整数（最常见，即种子 ID），也可能是对象。返回 torrentID、详情页 URL、直链下载 URL。
// 详情页采用站点 hash 路由（/#/torrent/detail/{id}），与发种页 /#/torrent/add 一致。
func resolveYemaPTTorrentResult(rawData json.RawMessage, baseURL string) (int, string, string) {
	trimmed := strings.TrimSpace(string(rawData))
	if trimmed == "" || trimmed == "null" {
		return 0, "", ""
	}
	// 裸整数：直接作为种子 ID
	if n, err := strconv.Atoi(strings.Trim(trimmed, `"`)); err == nil {
		detail := strings.TrimRight(baseURL, "/") + "/#/torrent/detail/" + strconv.Itoa(n)
		return n, detail, ""
	}
	// 对象形态：尝试解析 detailUrl/downloadUrl 等字段
	var obj yemaptTorrentData
	if err := json.Unmarshal(rawData, &obj); err == nil {
		id := obj.ID
		if id == 0 {
			id = obj.TorrentID
		}
		detail := strings.TrimSpace(obj.DetailURL)
		if detail == "" && id != 0 {
			detail = strings.TrimRight(baseURL, "/") + "/#/torrent/detail/" + strconv.Itoa(id)
		}
		return id, detail, strings.TrimSpace(obj.DownloadURL)
	}
	return 0, "", ""
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

// pickYemaPTValueEx 在映射表中按标准化值查找 yemapt 数字 ID，并返回是否命中映射。
// 参数/返回：mapping 为数字字典，standardized 为 PTNexus 标准化值（如 source.china、video.h265），
// fallback 为兜底值；返回解析结果与「是否命中」，未命中等价于回落到兜底值。
// 失败场景：不适用。
// 副作用：无。
func pickYemaPTValueEx(mapping map[string]string, standardized, fallback string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(standardized))
	if key == "" {
		return strings.TrimSpace(fallback), false
	}
	if mapped, ok := mapping[key]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped), true
	}
	// 标准化值可能带 source./medium./resolution.r 等前缀，去掉后再试一次
	if idx := strings.Index(key, "."); idx >= 0 {
		suffix := key[idx+1:]
		if mapped, ok := mapping[suffix]; ok && strings.TrimSpace(mapped) != "" {
			return strings.TrimSpace(mapped), true
		}
	}
	return strings.TrimSpace(fallback), false
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
	// IMDb ID：tt + 数字（大小写不敏感），只取数字部分，用于剥掉 tt 前缀。
	reYemaPTIMDb   = regexp.MustCompile(`(?i)tt(\d+)`)
	reYemaPTDouban = regexp.MustCompile(`subject/(\d+)`)
	reYemaPTImgTag = regexp.MustCompile(`(?i)\[img(?:\=[^\]]*)?\]([\s\S]*?)\[/img\]`)
	reYemaPTURL    = regexp.MustCompile(`https?://[^\s\)\]]+`)
)

// stripYemaPTImageTags 取出预览图的纯 URL。
// 发种站点的 poster 常以 BBCode 包裹（如 [img]https://...[/img]），而 yemapt 的 picture 字段只接受纯 URL，
// 因此需剥掉 [img] 标签；已经是纯 URL 则原样返回，找不到标签时退回提取首个 http(s) 链接。
func stripYemaPTImageTags(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	if m := reYemaPTImgTag.FindStringSubmatch(trimmed); m != nil {
		inner := strings.TrimSpace(m[1])
		if inner != "" {
			return inner
		}
	}
	if m := reYemaPTURL.FindString(trimmed); m != "" {
		return m
	}
	return trimmed
}

// normalizeYemaPTDescription 将简介（longDesc）中的 BBCode 图片标签 [img]url[/img] 转为 yemapt 接受的
// Markdown 图片语法 ![_](url)，避免 BBCode 泄漏到 Markdown 字段；Markdown 原生 ![alt](url) 保持不变。
// 其它 BBCode（如 [u]/[b]/[color]）为源站自带标记，yemapt 可原样呈现，不做处理。
func normalizeYemaPTDescription(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	return reYemaPTImgTag.ReplaceAllStringFunc(trimmed, func(match string) string {
		sub := reYemaPTImgTag.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		url := strings.TrimSpace(sub[1])
		if url == "" {
			return ""
		}
		return fmt.Sprintf("![_](%s)", url)
	})
}

// resolveYemaPTIMDbParam 解析 yemapt 发种接口的 imdb 参数，**始终返回纯数字**（去掉 tt 前缀）。
//
// yemapt 的 imdb 字段只接受数字 ID：站点详情接口实测返回 "imdb":"0206013"，
// 对应 https://www.imdb.com/title/tt0206013/ —— 若把 tt 一起发过去，站点会存成非法 ID。
// 因此不论上游给的是完整链接、tt 前缀 ID，还是转种面板里的 imdb_id，
// 统一在这里剥掉 tt，只提交数字串。
//
// 候选来源按优先级：发布输入的外部链接（已聚合 uploadData/standardized 的 imdb_link）
// → 标准化参数 imdb / imdb_id → uploadData 原始字段（转种面板直接提交时用）。
func resolveYemaPTIMDbParam(input publisher.PublishInput, std map[string]any) string {
	candidates := []string{
		input.IMDbLink,
		toStringAny(std["imdb_link"], ""),
		toStringAny(std["imdb"], ""),
		toStringAny(std["imdb_id"], ""),
		toStringAny(input.UploadData["imdb_link"], ""),
		toStringAny(input.UploadData["imdb"], ""),
		toStringAny(input.UploadData["imdb_id"], ""),
	}
	for _, candidate := range candidates {
		if id := extractYemaPTIMDbID(candidate); id != "" {
			return id
		}
	}
	return ""
}

// extractYemaPTIMDbID 从 IMDb 链接或 ID 中提取纯数字部分（保留前导零，例如 tt0089893 -> 0089893）。
// 已去掉 tt 前缀或本身就是数字串时原样返回；解析不出数字则返回空串（调用方会跳过 imdb 字段）。
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
