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
	"github.com/pt-nexus/server/internal/service/mteamapi"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
	"gopkg.in/yaml.v3"
)

// mteamConfig 定义 M-Team（馒头）发种接口的专属字典。
//
// M-Team 不是 NexusPHP 站点，发种走自研 REST API：
//   - 端点：POST {api_base}{api_path}（默认 https://api.m-team.cc/api/torrent/createOredit）
//   - 鉴权：请求头 x-api-key（API Access Token，控制台 → 实验室 → 存取令牌）
//   - 编码：multipart/form-data，种子文件为 file 字段
//   - 注意：必须带 User-Agent，否则 nginx 会 302 到 google
//
// 因此无法复用 configs 下 NexusPHP 系的 SitePublishConfig，这里使用独立配置结构。
type mteamConfig struct {
	SiteName   string            `yaml:"site_name"`
	APIBase    string            `yaml:"api_base"`
	APIPath    string            `yaml:"api_path"`
	Category   map[string]string `yaml:"category"`
	Source     map[string]string `yaml:"source"`
	Standard   map[string]string `yaml:"standard"`
	Codec      map[string]string `yaml:"video_codec"`
	Audio      map[string]string `yaml:"audio_codec"`
	Team       map[string]string `yaml:"team"`
	Processing map[string]string `yaml:"processing"`
	Country    map[string]string `yaml:"country"`
	Tags       map[string]string `yaml:"tags"`
	Defaults   mteamDefaults     `yaml:"defaults"`
}

type mteamDefaults struct {
	Category   string `yaml:"category"`
	Source     string `yaml:"source"`
	Standard   string `yaml:"standard"`
	Codec      string `yaml:"video_codec"`
	Audio      string `yaml:"audio_codec"`
	Processing string `yaml:"processing"`
}

// mteamDefaultConfig 为 configs/mteam.yaml 缺失或解析失败时的兜底字典。
// 取值来自站点权威选项表（2026-09-21 用真实 token 拉取
// /api/torrent/{categoryList,sourceList,standardList,videoCodecList,audioCodecList,teamList,processingList}
// 与 /api/system/{countryList,getConf}），完整字典以 server/configs/mteam.yaml 为准，改动请同步两处。
var mteamDefaultConfig = mteamConfig{
	SiteName: "M-Team",
	APIBase:  "https://api.m-team.cc",
	APIPath:  "/api/torrent/createOredit",
	// 分类按「类型 + 介质」二维划分，这里只存各细分 ID，落点由 resolveMTeamCategory 计算。
	Category: map[string]string{
		"movie": "419", "movie_sd": "401", "movie_dvd": "420", "movie_bluray": "421", "movie_remux": "439",
		"tv": "402", "tv_sd": "403", "tv_dvd": "435", "tv_bluray": "438",
		"anime": "405", "anime_bluray": "453",
		"documentary": "404", "music": "434", "mv": "406", "sports": "407",
		"game": "423", "software": "422", "ebook": "427", "audiobook": "442",
		"study": "451", "other": "409", "default": "409",
	},
	// 来源：1=Bluray 4=Remux 5=HDTV/TV 3=DVD 8=Web-DL 10=CD 6=Other
	Source: map[string]string{
		"medium.bluray": "1", "medium.uhd_bluray": "1", "medium.uhd_diy": "1",
		"medium.bdrip": "1", "medium.encode": "1", "medium.minibd": "1",
		"medium.minisd": "1", "medium.hddvd": "1",
		"medium.remux": "4",
		"medium.webdl": "8", "medium.webrip": "8", "medium.feed": "8",
		"medium.hdtv": "5", "medium.tv": "5", "medium.tvrip": "5", "medium.uhdtv": "5",
		"medium.dvd": "3", "medium.dvdr": "3", "medium.vcd": "3",
		"medium.cd_dvd": "3", "medium.cd_vcd": "3",
		"medium.cd": "10", "medium.sacd": "10", "medium.vinyl": "10", "medium.track": "10",
		"medium.other": "6",
		"bluray":       "1", "uhd_bluray": "1", "uhd_diy": "1", "bdrip": "1", "encode": "1",
		"minibd": "1", "minisd": "1", "hddvd": "1",
		"remux": "4",
		"webdl": "8", "webrip": "8", "feed": "8",
		"hdtv": "5", "tv": "5", "tvrip": "5", "uhdtv": "5",
		"dvd": "3", "dvdr": "3", "vcd": "3", "cd_dvd": "3", "cd_vcd": "3",
		"cd": "10", "sacd": "10", "vinyl": "10", "track": "10",
		"other": "6",
	},
	// 分辨率：1=1080p 2=1080i 3=720p 5=SD 6=4K 7=8K（站点无 Other）
	Standard: map[string]string{
		"resolution.r1080p": "1", "resolution.r1080i": "2", "resolution.r720p": "3",
		"resolution.sd": "5", "resolution.r480p": "5", "resolution.r540p": "5",
		"resolution.other": "5", "resolution.ipad": "5",
		"resolution.r1440p": "1", "resolution.r2160p": "6", "resolution.r4320p": "7",
	},
	// 视频编码：1=H.264 16=H.265 2=VC-1 4=MPEG-2 3=Xvid 19=AV1 21=VP8/9 22=AVS（站点无 Other）
	Codec: map[string]string{
		"video.h264": "1", "video.x264": "1", "video.h265": "16", "video.x265": "16",
		"video.vc1": "2", "video.mpeg2": "4", "video.xvid": "3", "video.av1": "19",
		"video.vp9": "21", "video.h261": "1", "video.mpeg1": "1", "video.mpeg4": "1",
		"video.h266": "1", "video.other": "1",
	},
	// 音频编码：6=AAC 8=AC3 3=DTS 11=DTS-HD MA 12=E-AC3 13=E-AC3 Atmos
	// 9=TrueHD 10=TrueHD Atmos 14=LPCM/PCM 15=WAV 1=FLAC 2=APE 4=MP2/3 5=OGG 7=Other
	Audio: map[string]string{
		"audio.aac": "6", "audio.m4a": "6", "audio.ac3": "8", "audio.dts": "3",
		"audio.dts_hd": "11", "audio.dts_hd_hr": "11", "audio.dts_hd_ma": "11", "audio.dtsx": "11",
		"audio.ddp": "12", "audio.ddp_atmos": "13",
		"audio.truehd": "9", "audio.truehd_atmos": "10",
		"audio.lpcm": "14", "audio.pcm": "14", "audio.wav": "15",
		"audio.flac": "1", "audio.ape": "2", "audio.mp3": "4", "audio.mpeg": "4",
		"audio.ogg": "5", "audio.opus": "5", "audio.alac": "7", "audio.other": "7",
	},
	// 制作组：站点仅提供这些小组，其余留空不提交。
	Team: map[string]string{
		"team.mteam": "9", "team.mweb": "44", "team.bmdru": "6", "team.catedu": "25",
		"team.jkct": "31", "team.7acg": "30", "team.pack": "8", "team.dstudio": "64",
		"team.starfall": "59", "team.starfallweb": "59", "team.other": "",
	},
	// 地区：1=CN 2=US/EU 3=HK/TW 4=JP 5=KR 6=OT
	Processing: map[string]string{
		"source.china": "1", "source.hongkong": "3", "source.hongkong_taiwan": "3",
		"source.taiwan": "3", "source.western": "2", "source.canada": "2",
		"source.uk": "2", "source.france": "2", "source.germany": "2", "source.italy": "2",
		"source.spain": "2", "source.sweden": "2", "source.denmark": "2",
		"source.europe": "2", "source.russia": "2",
		"source.japan": "4", "source.korea": "5",
		"source.australia": "6", "source.brazil": "6", "source.india": "6",
		"source.malaysia": "6", "source.singapore": "6", "source.thailand": "6",
		"source.other": "6",
	},
	// 国家：站点国家表里中国为 8，香港/台湾不单列（同属中国）。未命中则不提交该字段。
	Country: map[string]string{
		"source.china": "8", "source.hongkong": "8", "source.hongkong_taiwan": "8",
		"source.taiwan": "8", "source.western": "2", "source.canada": "5",
		"source.uk": "12", "source.france": "6", "source.germany": "7", "source.italy": "9",
		"source.spain": "23", "source.sweden": "1", "source.denmark": "10",
		"source.russia": "3", "source.japan": "17", "source.korea": "30",
		"source.australia": "20", "source.brazil": "18", "source.india": "70",
		"source.singapore": "26", "source.malaysia": "40", "source.thailand": "93",
	},
	// 标签：取自站点 /api/system/getConf 的 TORRENT_LABEL_CONFIG
	Tags: map[string]string{
		"4k": "4k", "8k": "8k", "hdr": "hdr", "hdr10": "hdr10", "hdr10+": "hdr10+",
		"hlg": "hlg", "dv": "DoVi", "杜比": "DoVi", "菁彩hdr": "HDRVi",
		"中字": "中字", "国语": "中配", "中配": "中配",
	},
	Defaults: mteamDefaults{
		Category:   "409",
		Source:     "6",
		Standard:   "1",
		Codec:      "1",
		Audio:      "7",
		Processing: "6",
	},
}

// PublishMTeam 将种子发布到 M-Team（馒头，自研 API 站点）。
// 参数/返回：input 为统一发布输入；返回发布结果（详情页链接、过程日志等）。
// 失败场景：缺少 base_url / API token / 种子文件，或接口返回失败时返回 error。
// 副作用：读取本地种子文件并向 M-Team 发起 multipart 上传请求。
func PublishMTeam(input publisher.PublishInput) (publisher.PublishResult, error) {
	webBase := normalizeBaseURL(input.BaseURL)
	if webBase == "" {
		return publisher.PublishResult{}, fmt.Errorf("m-team 发种缺少 base_url")
	}

	token := resolveMTeamToken(input)
	if token.Value == "" {
		return publisher.PublishResult{}, fmt.Errorf("m-team 发种缺少 API Token：请在 webui 站点配置的 Passkey 或 Cookie 栏填写控制台生成的存取令牌（形如 57b1fa6c-4444-3333-2222-1b1111111111）。若该栏填的是网页 Cookie，令牌不会被识别")
	}
	apiKey := token.Value

	cfg := loadMTeamConfig()
	apiBase := firstNonEmpty(normalizeBaseURL(cfg.APIBase), "https://api.m-team.cc")
	uploadURL := strings.TrimRight(apiBase, "/") + cfg.APIPath

	// 对齐 PublishPublic：UPLOAD_TEST_MODE=true 时跳过真实发种，返回模拟成功。
	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		return publisher.PublishResult{
			PublishURL:       strings.TrimRight(webBase, "/") + "/detail/999999999",
			AttemptDetailLog: fmt.Sprintf("--- [m-team] 测试模式：跳过实际发种（目标 %s）---", strings.TrimSpace(input.TargetName)),
		}, nil
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		return publisher.PublishResult{}, fmt.Errorf("m-team 发种缺少标题")
	}

	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return publisher.PublishResult{}, fmt.Errorf("m-team 发种缺少种子文件路径")
	}
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("读取种子文件失败: %w", err)
	}

	std := map[string]any{}
	if s, ok := input.UploadData["standardized_params"].(map[string]any); ok && s != nil {
		std = s
	}

	// 标准化值保留完整形态（如 resolution.r1080p）供字典查找；去前缀形态（r1080p）
	// 仅用于分类落点判断。字典查找必须吃完整值，否则 pickMTeamValueEx 的去前缀兜底会走空。
	categoryKey := toStringAny(std["type"], "")
	mediumRaw := toStringAny(std["medium"], "")
	resolutionRaw := toStringAny(std["resolution"], "")
	codecRaw := toStringAny(std["video_codec"], "")
	audioRaw := toStringAny(std["audio_codec"], "")
	sourceKey := toStringAny(std["source"], "")
	teamKey := toStringAny(std["team"], "")

	mediumKey := normalizeMTeamToken(mediumRaw, "medium.")
	resolutionToken := normalizeMTeamToken(resolutionRaw, "resolution.")

	// 未命中即回落到 defaults，故每一维度都记录告警，便于事后核对站点字典。
	fallbackNotes := make([]string, 0, 6)
	pick := func(label string, mapping map[string]string, raw, fallback string) string {
		value, matched := pickMTeamValueEx(mapping, raw, fallback)
		if !matched && strings.TrimSpace(raw) != "" {
			fallbackNotes = append(fallbackNotes, fmt.Sprintf(
				"⚠️ %s 映射未命中：标准化值 %q 不在 configs/mteam.yaml 对应字典中，已回落默认值 %s。请核对站点选项后补充映射。",
				label, strings.TrimSpace(raw), value,
			))
		}
		return value
	}

	textFields := map[string]string{
		"name":       title,
		"smallDescr": strings.TrimSpace(input.Subtitle),
		"descr":      buildMTeamDescription(input.Description),
		"category":   resolveMTeamCategory(cfg, categoryKey, mediumKey, resolutionToken),
		"source":     pick("来源(source)", cfg.Source, mediumRaw, cfg.Defaults.Source),
		"standard":   pick("分辨率(standard)", cfg.Standard, resolutionRaw, cfg.Defaults.Standard),
		"videoCodec": pick("视频编码(videoCodec)", cfg.Codec, codecRaw, cfg.Defaults.Codec),
		"audioCodec": pick("音频编码(audioCodec)", cfg.Audio, audioRaw, cfg.Defaults.Audio),
		"processing": pick("地区(processing)", cfg.Processing, sourceKey, cfg.Defaults.Processing),
	}

	if teamID, ok := cfg.Team[strings.ToLower(strings.TrimSpace(teamKey))]; ok && strings.TrimSpace(teamID) != "" {
		textFields["team"] = strings.TrimSpace(teamID)
	}

	// countries 为必填（站点按分类决定是否展示）；映射不到具体国家时不提交，交由站点提示。
	if countryID, ok := cfg.Country[strings.ToLower(strings.TrimSpace(sourceKey))]; ok && strings.TrimSpace(countryID) != "" {
		textFields["countries"] = strings.TrimSpace(countryID)
	}

	// 外链：站点存的是完整链接（实测 https://www.imdb.com/title/tt0468569/）。
	if imdb := resolveMTeamIMDbLink(input, std); imdb != "" {
		textFields["imdb"] = imdb
	}
	if douban := resolveMTeamDoubanLink(input, std); douban != "" {
		textFields["douban"] = douban
	}
	if mediaInfo := strings.TrimSpace(input.MediaInfo); mediaInfo != "" {
		textFields["mediainfo"] = mediaInfo
	}

	// 标签：站点前端同样以逗号分隔字符串提交（FormData 对数组会做 String() 展开）。
	tagIds := resolveMTeamTagIDs(cfg, input.UploadData)
	if len(tagIds) > 0 {
		textFields["labelsNew"] = strings.Join(tagIds, ",")
	}

	// 匿名：站点为布尔字段。
	if publisher.ResolveAnonymousUploadEnabled(input.RootConfig) {
		textFields["anonymous"] = "true"
	} else {
		textFields["anonymous"] = "false"
	}

	if strings.TrimSpace(textFields["smallDescr"]) == "" {
		textFields["smallDescr"] = title
	}
	if len([]rune(textFields["smallDescr"])) > 255 {
		textFields["smallDescr"] = string([]rune(textFields["smallDescr"])[:255])
	}
	if strings.TrimSpace(textFields["descr"]) == "" {
		// descr 为站点必填项，缺失时用标题兜底，避免因空简介被拒。
		textFields["descr"] = title
	}

	logLines := []string{
		fmt.Sprintf("--- [m-team] 开始发布到 %s ---", strings.TrimSpace(input.TargetName)),
		fmt.Sprintf("上传地址: %s", uploadURL),
		fmt.Sprintf("API Token: %s（来源 %s）", mteamTokenFingerprint(apiKey), token.Source),
		fmt.Sprintf("字段摘要: name=%q category=%s source=%s standard=%s videoCodec=%s audioCodec=%s processing=%s team=%q countries=%q labelsNew=%q anonymous=%s",
			title, textFields["category"], textFields["source"], textFields["standard"],
			textFields["videoCodec"], textFields["audioCodec"], textFields["processing"],
			textFields["team"], textFields["countries"], textFields["labelsNew"], textFields["anonymous"]),
		fmt.Sprintf("外链字段: imdb=%q douban=%q", textFields["imdb"], textFields["douban"]),
	}
	logLines = append(logLines, fallbackNotes...)

	publishURL, attemptDetail, existing, publishErr := postMTeamTorrent(uploadURL, webBase, apiKey, textFields, torrentBytes, filepath.Base(torrentPath))
	if strings.TrimSpace(attemptDetail) != "" {
		logLines = append(logLines, attemptDetail)
	}

	if publishErr != nil {
		logLines = append(logLines, fmt.Sprintf("发布结果：发布到 m-team 失败: %v", publishErr))
		return publisher.PublishResult{
			IsExistingTorrent: existing,
			AttemptDetailLog:  strings.Join(logLines, "\n"),
		}, publishErr
	}

	logLines = append(logLines, "发布结果：成功发布到 m-team")
	return publisher.PublishResult{
		PublishURL:       publishURL,
		AttemptDetailLog: strings.Join(logLines, "\n"),
	}, nil
}

// postMTeamTorrent 以 multipart/form-data 提交发种请求到 M-Team。
// 返回详情页链接、过程日志、是否为「种子已存在」以及错误。
func postMTeamTorrent(uploadURL, webBase, apiKey string, textFields map[string]string, torrentBytes []byte, torrentName string) (string, string, bool, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	for key, value := range textFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return "", "", false, fmt.Errorf("构造表单字段失败 %s: %w", key, err)
		}
	}

	part, err := writer.CreateFormFile("file", torrentName)
	if err != nil {
		return "", "", false, fmt.Errorf("构造文件字段失败: %w", err)
	}
	if _, err := part.Write(torrentBytes); err != nil {
		return "", "", false, fmt.Errorf("写入种子文件失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", "", false, fmt.Errorf("结束表单写入失败: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, uploadURL, body)
	if err != nil {
		return "", "", false, fmt.Errorf("构造请求失败: %w", err)
	}
	// 站点 nginx 对无 UA 的请求会 302 到 google，UA 必须设置。
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Origin", strings.TrimRight(webBase, "/"))
	req.Header.Set("Referer", strings.TrimRight(webBase, "/")+"/upload")

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", false, fmt.Errorf("请求 m-team 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", false, fmt.Errorf("读取响应失败: %w", err)
	}
	raw := strings.TrimSpace(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Sprintf("HTTP %d 响应: %s", resp.StatusCode, summarizeResponseBody(raw)), false,
			fmt.Errorf("m-team 返回 HTTP %d", resp.StatusCode)
	}

	var parsed mteamapi.Response
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Sprintf("响应(非 JSON): %s", summarizeResponseBody(raw)), false,
			fmt.Errorf("m-team 响应解析失败: %w", err)
	}

	code := parsed.CodeString()
	message := strings.TrimSpace(parsed.Message)
	if !parsed.IsSuccess() {
		existing := isMTeamDuplicateMessage(message)
		if message == "" {
			message = summarizeResponseBody(raw)
		}
		return "", fmt.Sprintf("接口返回失败: code=%s message=%s", code, message), existing,
			fmt.Errorf("m-team 接口返回失败(code=%s): %s", code, message)
	}

	torrentID := resolveMTeamTorrentID(parsed.Data)
	detail := ""
	if torrentID != "" {
		detail = strings.TrimRight(webBase, "/") + "/detail/" + torrentID
	}
	return detail, fmt.Sprintf("接口返回成功: %s", summarizeResponseBody(raw)), false, nil
}

// resolveMTeamTorrentID 从响应 data 中提取种子 ID。
// data 可能是对象（含 id），也可能是裸 ID。
func resolveMTeamTorrentID(rawData json.RawMessage) string {
	trimmed := strings.TrimSpace(string(rawData))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") {
		var obj map[string]any
		if err := json.Unmarshal(rawData, &obj); err == nil {
			for _, key := range []string{"id", "torrentId", "torrent"} {
				if value := strings.TrimSpace(toStringAny(obj[key], "")); value != "" && value != "0" {
					return value
				}
			}
		}
		return ""
	}
	return strings.Trim(trimmed, `"`)
}

// isMTeamDuplicateMessage 判断接口报错是否为「种子已存在」，用于让上层标记重复而非硬失败。
func isMTeamDuplicateMessage(message string) bool {
	lower := strings.ToLower(strings.TrimSpace(message))
	if lower == "" {
		return false
	}
	for _, keyword := range []string{"已存在", "已存在該", "重複", "重复", "已發佈", "已发布", "已收藏", "exists", "duplicate", "existed"} {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// loadMTeamConfig 读取 server/configs/mteam.yaml 并将其中字典合并到默认配置之上。
func loadMTeamConfig() mteamConfig {
	cfg := mteamDefaultConfig
	// mteamDefaultConfig 中的 map 是引用类型，直接合并会污染全局兜底配置，
	// 并发发种时还会产生 data race，因此先逐张拷贝。
	cfg.Category = cloneMTeamMap(cfg.Category)
	cfg.Source = cloneMTeamMap(cfg.Source)
	cfg.Standard = cloneMTeamMap(cfg.Standard)
	cfg.Codec = cloneMTeamMap(cfg.Codec)
	cfg.Audio = cloneMTeamMap(cfg.Audio)
	cfg.Team = cloneMTeamMap(cfg.Team)
	cfg.Processing = cloneMTeamMap(cfg.Processing)
	cfg.Country = cloneMTeamMap(cfg.Country)
	cfg.Tags = cloneMTeamMap(cfg.Tags)

	paths := config.ResolveRuntimePaths()
	data, err := os.ReadFile(filepath.Join(paths.BaseDir, "configs", "mteam.yaml"))
	if err != nil {
		expandMTeamAliases(&cfg)
		return cfg
	}
	var override mteamConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		expandMTeamAliases(&cfg)
		return cfg
	}
	if strings.TrimSpace(override.SiteName) != "" {
		cfg.SiteName = strings.TrimSpace(override.SiteName)
	}
	if strings.TrimSpace(override.APIBase) != "" {
		cfg.APIBase = strings.TrimSpace(override.APIBase)
	}
	if strings.TrimSpace(override.APIPath) != "" {
		cfg.APIPath = strings.TrimSpace(override.APIPath)
	}
	mergeMTeamMap(cfg.Category, override.Category)
	mergeMTeamMap(cfg.Source, override.Source)
	mergeMTeamMap(cfg.Standard, override.Standard)
	mergeMTeamMap(cfg.Codec, override.Codec)
	mergeMTeamMap(cfg.Audio, override.Audio)
	mergeMTeamMap(cfg.Team, override.Team)
	mergeMTeamMap(cfg.Processing, override.Processing)
	mergeMTeamMap(cfg.Country, override.Country)
	mergeMTeamMap(cfg.Tags, override.Tags)
	if v := strings.TrimSpace(override.Defaults.Category); v != "" {
		cfg.Defaults.Category = v
	}
	if v := strings.TrimSpace(override.Defaults.Source); v != "" {
		cfg.Defaults.Source = v
	}
	if v := strings.TrimSpace(override.Defaults.Standard); v != "" {
		cfg.Defaults.Standard = v
	}
	if v := strings.TrimSpace(override.Defaults.Codec); v != "" {
		cfg.Defaults.Codec = v
	}
	if v := strings.TrimSpace(override.Defaults.Audio); v != "" {
		cfg.Defaults.Audio = v
	}
	if v := strings.TrimSpace(override.Defaults.Processing); v != "" {
		cfg.Defaults.Processing = v
	}
	expandMTeamAliases(&cfg)
	return cfg
}

// cloneMTeamMap 复制一张字典，避免合并 yaml 覆盖时写坏全局兜底配置。
func cloneMTeamMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

// expandMTeamAliases 为带点号的标准化键补充去前缀别名。
//
// 上游标准化值可能是 resolution.r1080p（完整形态），也可能已去前缀为 r1080p，
// 字典只维护完整形态，这里派生别名后两种形态都能命中，避免同一份字典写两遍后相互漂移。
func expandMTeamAliases(cfg *mteamConfig) {
	for _, mapping := range []map[string]string{
		cfg.Category, cfg.Source, cfg.Standard, cfg.Codec,
		cfg.Audio, cfg.Team, cfg.Processing, cfg.Country,
	} {
		for key, value := range mapping {
			idx := strings.Index(key, ".")
			if idx <= 0 || idx >= len(key)-1 {
				continue
			}
			alias := key[idx+1:]
			if _, exists := mapping[alias]; !exists {
				mapping[alias] = value
			}
		}
	}
}

func mergeMTeamMap(base, override map[string]string) {
	if len(override) == 0 {
		return
	}
	for key, value := range override {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		base[trimmedKey] = strings.TrimSpace(value)
	}
}

// pickMTeamValueEx 在映射表中按标准化值查找 M-Team 数字 ID，并返回是否命中。
// 参数/返回：mapping 为数字字典，standardized 为 PTNexus 标准化值（如 source.china、video.h265），
// fallback 为兜底值；返回解析结果与「是否命中」。
// 失败场景：不适用。
// 副作用：无。
func pickMTeamValueEx(mapping map[string]string, standardized, fallback string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(standardized))
	if key == "" {
		return strings.TrimSpace(fallback), false
	}
	if mapped, ok := mapping[key]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped), true
	}
	// 标准化值可能带完整的 source./medium./video. 前缀，去掉后再试一次
	if idx := strings.Index(key, "."); idx >= 0 {
		if mapped, ok := mapping[key[idx+1:]]; ok && strings.TrimSpace(mapped) != "" {
			return strings.TrimSpace(mapped), true
		}
	}
	return strings.TrimSpace(fallback), false
}

// normalizeMTeamToken 去掉 PTNexus 标准化值的前缀并转小写，例如 medium.uhd_bluray -> uhd_bluray。
func normalizeMTeamToken(raw, prefix string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return ""
	}
	if prefix != "" && strings.HasPrefix(trimmed, strings.ToLower(prefix)) {
		trimmed = trimmed[len(prefix):]
	}
	return trimmed
}

// mteamCategoryGroup 把 PTNexus 分类归并到站点的分类大类。
func mteamCategoryGroup(category string) string {
	key := normalizeMTeamToken(category, "category.")
	switch key {
	case "movie", "blu_ray", "bluray":
		return "movie"
	case "tv_series", "tv_shows", "drama", "playlet", "tvshow", "tv":
		return "tv"
	case "animation", "cartoon", "anime":
		return "anime"
	case "document", "documentaries", "documentary":
		return "documentary"
	case "music", "audio":
		return "music"
	case "audiobook":
		return "audiobook"
	case "mv", "live":
		return "mv"
	case "sports":
		return "sports"
	case "game":
		return "game"
	case "software", "os":
		return "software"
	case "ebook", "books":
		return "ebook"
	case "study", "education":
		return "study"
	default:
		return ""
	}
}

// resolveMTeamCategory 计算站点分类 ID。
//
// 站点的电影/影剧分类按「介质 + 分辨率」二维细分（SD / HD / DVDiSo / BluRay / Remux），
// 因此先用 PTNexus 的 category 定大类，再用 medium / resolution 细分；
// 无法细分的类型（纪录、音乐、游戏等）直接取单值分类。
func resolveMTeamCategory(cfg mteamConfig, category, medium, resolution string) string {
	pickKey := func(keys ...string) string {
		for _, key := range keys {
			if value := strings.TrimSpace(cfg.Category[key]); value != "" {
				return value
			}
		}
		return strings.TrimSpace(cfg.Defaults.Category)
	}

	remux := medium == "remux"
	dvd := medium == "dvd" || medium == "dvdr" || medium == "vcd" || medium == "cd_dvd" || medium == "cd_vcd"
	bluray := strings.Contains(medium, "bluray") || medium == "uhd_diy" || medium == "minibd" ||
		medium == "minisd" || medium == "bdrip" || medium == "hddvd"
	sd := resolution == "sd" || resolution == "r480p" || resolution == "r540p"

	switch mteamCategoryGroup(category) {
	case "movie":
		switch {
		case remux:
			return pickKey("movie_remux", "movie")
		case dvd:
			return pickKey("movie_dvd", "movie")
		case bluray:
			return pickKey("movie_bluray", "movie")
		case sd:
			return pickKey("movie_sd", "movie")
		default:
			return pickKey("movie")
		}
	case "tv":
		switch {
		case remux || bluray:
			return pickKey("tv_bluray", "tv")
		case dvd:
			return pickKey("tv_dvd", "tv")
		case sd:
			return pickKey("tv_sd", "tv")
		default:
			return pickKey("tv")
		}
	case "anime":
		if remux || bluray {
			return pickKey("anime_bluray", "anime")
		}
		return pickKey("anime")
	case "documentary":
		return pickKey("documentary")
	case "music":
		return pickKey("music")
	case "audiobook":
		return pickKey("audiobook")
	case "mv":
		return pickKey("mv")
	case "sports":
		return pickKey("sports")
	case "game":
		return pickKey("game")
	case "software":
		return pickKey("software")
	case "ebook":
		return pickKey("ebook")
	case "study":
		return pickKey("study")
	default:
		return pickKey("other", "default")
	}
}

// resolveMTeamTagIDs 把 PTNexus 标签映射到站点标签（labelsNew）；未配置的标签忽略。
func resolveMTeamTagIDs(cfg mteamConfig, uploadData map[string]any) []string {
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
	reMTeamIMDb   = regexp.MustCompile(`(?i)tt(\d+)`)
	reMTeamDouban = regexp.MustCompile(`subject/(\d+)`)
	reMTeamImgTag = regexp.MustCompile(`(?i)\[img(?:\=[^\]]*)?\]([\s\S]*?)\[/img\]`)
)

// resolveMTeamIMDbLink 解析站点 imdb 字段，返回完整 IMDb 链接（站点实测存的就是完整链接）。
func resolveMTeamIMDbLink(input publisher.PublishInput, std map[string]any) string {
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
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if match := reMTeamIMDb.FindStringSubmatch(trimmed); match != nil {
			return fmt.Sprintf("https://www.imdb.com/title/tt%s/", match[1])
		}
		if strings.Contains(strings.ToLower(trimmed), "imdb.com") {
			return trimmed
		}
	}
	return ""
}

// resolveMTeamDoubanLink 解析站点 douban 字段，返回完整豆瓣链接。
func resolveMTeamDoubanLink(input publisher.PublishInput, std map[string]any) string {
	candidates := []string{
		input.DoubanLink,
		toStringAny(std["douban"], ""),
		toStringAny(std["douban_id"], ""),
		toStringAny(input.UploadData["douban_link"], ""),
		toStringAny(input.UploadData["douban"], ""),
		toStringAny(input.UploadData["douban_id"], ""),
	}
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		if match := reMTeamDouban.FindStringSubmatch(trimmed); match != nil {
			return fmt.Sprintf("https://movie.douban.com/subject/%s/", match[1])
		}
		if strings.Contains(strings.ToLower(trimmed), "douban.com") {
			return trimmed
		}
	}
	return ""
}

// buildMTeamDescription 把简介中的 BBCode 图片标签转为 Markdown（站点简介为 Markdown 编辑器）。
func buildMTeamDescription(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	return reMTeamImgTag.ReplaceAllStringFunc(trimmed, func(match string) string {
		sub := reMTeamImgTag.FindStringSubmatch(match)
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

// mteamToken 描述解析出的存取令牌及其来源，来源用于日志诊断（便于发现填错栏位）。
type mteamToken = mteamapi.Credential

// resolveMTeamToken 解析 M-Team 的 API Access Token。
//
// 站点鉴权不走 cookie 而是 x-api-key，但 PTNexus 的站点配置只有 Cookie / Passkey 两栏，
// 前端还把 m-team 列入「需要手动配 Passkey」的站点，因此两栏都要能取到令牌。按序尝试：
//   - 站点配置显式提供的 api_key
//   - Passkey 栏
//   - Cookie 栏
//
// 每一栏都做容错解析（见 mteamapi.ExtractToken），避免把误粘贴的网页 Cookie 当成令牌发出。
func resolveMTeamToken(input publisher.PublishInput) mteamToken {
	return mteamapi.ResolveToken([]mteamapi.Credential{
		{Source: "站点配置 api_key", Value: toStringAny(input.TargetInfo["api_key"], "")},
		{Source: "Passkey 栏", Value: toStringAny(input.TargetInfo["passkey"], "")},
		{Source: "Cookie 栏", Value: input.Cookie},
	})
}

// mteamTokenFingerprint 生成令牌的脱敏指纹，用于日志核对（只暴露前 8 位与长度）。
func mteamTokenFingerprint(token string) string {
	return mteamapi.Fingerprint(token)
}
