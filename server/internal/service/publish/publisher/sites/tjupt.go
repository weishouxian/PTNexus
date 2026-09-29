package sites

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/config"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	"github.com/pt-nexus/server/internal/service/processing/tagging"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
	publishuploader "github.com/pt-nexus/server/internal/service/publish/uploader"
	"gopkg.in/yaml.v3"
)

// tjuptConfig 定义北洋园（tjupt.org）发种表单的专属字典。
//
// 北洋园不是标准 NexusPHP 六维度站点：upload.php 上只有一级分类 type 一个分类 select，
// 其余分类信息由「二级分类 specificcat + 一批文本字段」承担，且各一级分类的字段集与取值语义都不同。
// 通用 SitePublishConfig（form_fields + mappings）无法表达「按分类切换字段名与值域」，
// 因此本站走专属适配器，字典也使用独立结构。
type tjuptConfig struct {
	SiteName         string                       `yaml:"site_name"`
	UploadFileField  string                       `yaml:"upload_file_field"`
	UploadPagePath   string                       `yaml:"upload_page_path"`
	UploadSubmitPath string                       `yaml:"upload_submit_path"`
	Category         map[string]string            `yaml:"category"`
	District         map[string]map[string]string `yaml:"district"`
	SpecificCat      map[string]map[string]string `yaml:"specific_cat"`
	Format           map[string]map[string]string `yaml:"format"`
	Platform         map[string]map[string]string `yaml:"platform"`
	Defaults         tjuptDefaults                `yaml:"defaults"`
}

type tjuptDefaults struct {
	Category    string `yaml:"category"`
	District    string `yaml:"district"`
	SpecificCat string `yaml:"specific_cat"`
	Format      string `yaml:"format"`
	Platform    string `yaml:"platform"`
	Artist      string `yaml:"artist"`
	Substeam    string `yaml:"substeam"`
	Animenum    string `yaml:"animenum"`
}

// tjuptDefaultConfig 为 configs/tjupt.yaml 缺失或解析失败时的兜底配置。
// 完整字典以 server/configs/tjupt.yaml 为准；此处只保留「没有它就跑不起来」的最小集合
// （枚举值不在这里重复维护，避免两处漂移）。
var tjuptDefaultConfig = tjuptConfig{
	SiteName:         "北洋园",
	UploadFileField:  "file",
	UploadPagePath:   "/upload.php",
	UploadSubmitPath: "/takeupload.php",
	Category: map[string]string{
		"category.movie":         "401",
		"category.movie_3d":      "401",
		"category.tv_series":     "402",
		"category.tv_pack":       "402",
		"category.tv_shows":      "403",
		"category.animation":     "405",
		"category.music":         "406",
		"category.mv":            "406",
		"category.sports":        "407",
		"category.software":      "408",
		"category.game":          "409",
		"category.other":         "410",
		"category.documentaries": "411",
		"default":                "401",
	},
	Defaults: tjuptDefaults{
		Category:    "401",
		District:    "其他",
		SpecificCat: "其他",
		Format:      "MKV",
		Platform:    "其他",
		Artist:      "群星",
		Substeam:    "未知",
		Animenum:    "1",
	},
}

// tjupt 站点常量：一级分类 ID 与表单字段名（实测自 2026-09-29 的 upload.php / showcategorydetail.php）。
const (
	tjuptCatMovie         = "401"
	tjuptCatTV            = "402"
	tjuptCatShow          = "403"
	tjuptCatMaterial      = "404"
	tjuptCatAnime         = "405"
	tjuptCatMusic         = "406"
	tjuptCatSports        = "407"
	tjuptCatSoftware      = "408"
	tjuptCatGame          = "409"
	tjuptCatOther         = "410"
	tjuptCatDocumentary   = "411"
	tjuptCatMobileVideo   = "412"
	tjuptFieldCategory    = "type"
	tjuptFieldCName       = "cname"
	tjuptFieldEName       = "ename"
	tjuptFieldDistrict    = "district"
	tjuptFieldSpecificCat = "specificcat"
	tjuptFieldFormat      = "format"
	tjuptFieldPlatform    = "platform"
	tjuptFieldIssueDate   = "issuedate"
	tjuptFieldAnimeNum    = "animenum"
	tjuptFieldSubsteam    = "substeam"
	tjuptFieldHQName      = "hqname"
	tjuptFieldArtist      = "artist"
	tjuptFieldResolution  = "resolution"
)

// 标题处理相关正则。
var (
	// 简介里的译名行，兼容全角空格（◎译　　名）。
	tjuptAliasLinePattern = regexp.MustCompile(`(?m)◎[　\s]*译[　\s]*名[　\s:：]*(.+)`)
	// 文件名末尾的常见媒体扩展名（站点前端校验要求 cname/ename 不得带扩展名）。
	tjuptFileExtPattern = regexp.MustCompile(`(?i)\.(mkv|mp4|iso|avi|mov|wmv|flv|m4v|ts|m2ts|rmvb|rm|asf|zip|rar|7z|pdf|azw3|epub|mobi|txt|nfo|doc|docx|xls|xlsx|ppt|pptx)$`)
	// 中文名里不允许出现集数描述（站点要求集数写进副标题）。
	tjuptEpisodeDescPattern = regexp.MustCompile(`(共|全|第)[0-9一二三四五六七八九十百]+[期集季部]`)
	// 标题中的年份与月（用于 issuedate）。
	tjuptYearPattern  = regexp.MustCompile(`(19\d{2}|20\d{2})`)
	tjuptMonthPattern = regexp.MustCompile(`(?:19\d{2}|20\d{2})[.\-年](\d{1,2})[月.\-]`)
	// 动漫话数：01-12 / 01~12 / 全12集 / S01 / 第01话
	tjuptAnimeNumPattern = regexp.MustCompile(`(?i)(?:S(\d{1,2})\b|\b(\d{1,3})[-~](\d{1,3})\b|全(\d{1,3})[集话]|第(\d{1,3})[话集])`)
	// 标题尾部的 release group（-GROUP）。
	tjuptReleaseGroupPattern = regexp.MustCompile(`-([A-Za-z0-9][A-Za-z0-9._-]{0,30})$`)
	// 描述中的中文字幕线索（用于决定是否勾选 chinese）。
	tjuptChineseSubtitlePattern = regexp.MustCompile(`(?i)中字|中文字幕|简繁|简体|繁体|CHS|CHT|Chinese|zh-cn|zh-tw|Text/zh`)
	// 动漫等分类的英文名里不应出现的视频编码标记（站点漫版规则；仅用于日志提示，不改写原始 0Day 名）。
	tjuptAnimeCodecHintPattern = regexp.MustCompile(`(?i)[hx]26[45]|avc|hevc`)
)

// 无对应语义来源的二级分类，按标题关键词启发式推断。
// key 为一级分类 ID，内层为「标准值 → 关键词列表」，按声明顺序命中即止。
var tjuptSecondaryKeywords = map[string][]struct {
	Value    string
	Keywords []string
}{
	tjuptCatMaterial: {
		{"考试相关", []string{"四级", "六级", "考研", "雅思", "托福", "GRE", "公务员", "中考", "高考"}},
		{"信息技术", []string{"编程", "python", "java", "c++", "数据结构", "数据库", "算法", "linux", "前端", "后端", "人工智能", "机器学习"}},
		{"外语学习", []string{"英语", "日语", "韩语", "法语", "德语", "俄语", "口语", "单词"}},
		{"经济管理", []string{"经济", "金融", "管理", "会计", "营销", "投资"}},
		{"科学技术", []string{"数学", "物理", "化学", "生物", "工程", "科学"}},
		{"健康养生", []string{"健身", "养生", "医学", "中医", "营养"}},
		{"教学课件", []string{"课件", "教案", "讲义", "教学", "课程"}},
		{"精彩图集", []string{"图集", "写真", "画册", "壁纸"}},
		{"论文期刊", []string{"论文", "期刊", "文献", "学报"}},
		{"文学艺术", []string{"小说", "文学", "艺术", "音乐", "绘画", "摄影"}},
		{"历史哲学", []string{"历史", "哲学", "宗教", "考古"}},
	},
	tjuptCatAnime: {
		{"剧场", []string{"剧场版", "劇場版", "Movie"}},
		{"OVA", []string{"OVA"}},
		{"OAD", []string{"OAD"}},
		{"漫画", []string{"漫画", "连载漫画", "Comic"}},
		{"周边", []string{"周边", "手办", "原画集"}},
		{"音乐", []string{"OST", "Original Soundtrack", "角色歌"}},
	},
	tjuptCatSports: {
		{"足球", []string{"足球", "英超", "西甲", "意甲", "德甲", "中超", "欧冠", "世界杯"}},
		{"篮球", []string{"篮球", "NBA", "CBA", "季后赛"}},
		{"F1", []string{"F1", "Formule", "Grand Prix"}},
		{"网球", []string{"网球", "ATP", "WTA", "大满贯"}},
		{"羽毛球", []string{"羽毛球", "汤杯", "尤杯"}},
		{"乒乓球", []string{"乒乓球", "世乒赛"}},
		{"棒球", []string{"棒球", "MLB"}},
		{"高尔夫", []string{"高尔夫", "Golf"}},
		{"摔角", []string{"摔角", "WWE", "UFC"}},
		{"台球", []string{"台球", "斯诺克"}},
	},
	tjuptCatDocumentary: {
		{"Netflix", []string{"Netflix", "NF"}},
		{"BBC", []string{"BBC"}},
		{"CCTV", []string{"CCTV", "央视"}},
		{"Disney+", []string{"Disney"}},
		{"National Geographic", []string{"国家地理", "National Geographic"}},
		{"Discovery", []string{"探索频道", "Discovery"}},
		{"History", []string{"历史频道", "History Channel"}},
		{"NHK", []string{"NHK"}},
		{"KBS", []string{"KBS"}},
		{"PBS", []string{"PBS"}},
		{"HBO", []string{"HBO"}},
		{"Amazon", []string{"Amazon", "AMZN"}},
		{"AppleTV", []string{"Apple TV", "AppleTV", "ATVP"}},
		{"IMAX", []string{"IMAX"}},
	},
}

// 运行平台关键词（408 软件 / 409 游戏）。
var tjuptPlatformKeywords = []struct {
	Value    string
	Keywords []string
}{
	{"Windows", []string{"windows", "win32", "win64", "win10", "win11", "pc版"}},
	{"macOS", []string{"macos", "mac os", "mac版", "osx"}},
	{"Linux", []string{"linux", "ubuntu", "debian", "centos"}},
	{"Android", []string{"android", "安卓", "apk"}},
	{"iOS", []string{"ios", "iphone", "ipad"}},
}

// 分辨率标准化值 → 站点 resolution 文本（405 动漫 / 407 体育 可选字段）。
var tjuptResolutionTexts = map[string]string{
	"resolution.r2160p": "2160p",
	"resolution.r1080p": "1080p",
	"resolution.r1080i": "1080i",
	"resolution.r720p":  "720p",
	"resolution.r720i":  "720i",
	"resolution.r480p":  "480p",
	"resolution.sd":     "480p",
}

// resolveTJUPTResolution 把分辨率标准化值转成站点文本，兼容完整/去前缀两种上游形态。
func resolveTJUPTResolution(key string) string {
	trimmed := strings.ToLower(strings.TrimSpace(key))
	if trimmed == "" {
		return ""
	}
	if value, ok := tjuptResolutionTexts[trimmed]; ok {
		return value
	}
	if idx := strings.Index(trimmed, "."); idx > 0 && idx < len(trimmed)-1 {
		if value, ok := tjuptResolutionTexts["resolution."+trimmed[idx+1:]]; ok {
			return value
		}
	}
	return ""
}

// resolveTJUPTAnimeRestriction 判断当前发布参数是否属于北洋园禁止发布的动漫资源。
// 参数/返回：cfg 为站点字典，input 为统一发布输入；命中时返回拒绝原因，未命中返回空串。
// 判定口径（两条，取自站点规则「本站不接收动漫资源」）：
//  1. 一级分类映射结果为 405 动漫（含标准化 type 直接映射为 category.animation 的资源）；
//  2. 发布标签命中动漫/动画（站点用标签表达动画属性，标准化 type 未必是动漫，与 mteam / ourbits / ptskit 同口径）。
//
// 说明：本站对动漫是「整类不接收」而不是「归到某个分类」，所以命中即拒绝，
// 不再做「按标签改写一级分类后继续发布」的兜底（原动画标签强制归 405 的逻辑已移除）。
func resolveTJUPTAnimeRestriction(cfg tjuptConfig, input publisher.PublishInput) string {
	categoryKey := ""
	if std, ok := input.UploadData["standardized_params"].(map[string]any); ok && std != nil {
		categoryKey = strings.TrimSpace(toStringAny(std["type"], ""))
	}
	if pickTJUPTValue(cfg.Category, categoryKey, cfg.Defaults.Category) == tjuptCatAnime {
		return "北洋园不接收动漫资源：当前一级分类映射为 405 动漫，已停止发布"
	}
	if hasAnimationTag(input.UploadData) {
		return "北洋园不接收动漫资源：发布标签命中动漫/动画，已停止发布"
	}
	return ""
}

// PublishTJUPT 将种子发布到北洋园（tjupt.org，非标准 NexusPHP 表单）。
// 参数/返回：input 为统一发布输入；返回发布结果（详情页链接、是否已存在、过程日志）。
// 失败场景：缺少 base_url / cookie / 标题 / 种子文件时返回 error；动漫资源返回 *publisher.PreCheckError；
// 站点返回校验错误时同样返回 error。
// 副作用：读取本地种子文件并向 tjupt.org 发起 multipart 上传请求。
func PublishTJUPT(input publisher.PublishInput) (publisher.PublishResult, error) {
	baseURL := normalizeBaseURL(input.BaseURL)
	if baseURL == "" {
		return publisher.PublishResult{}, fmt.Errorf("北洋园发种缺少 base_url")
	}
	cookie := strings.TrimSpace(input.Cookie)
	if cookie == "" {
		return publisher.PublishResult{}, fmt.Errorf("北洋园发种缺少 cookie：请在 webui 站点配置的 Cookie 栏填写登录态（含 access_token）")
	}

	cfg := loadTJUPTConfig()

	// 站点硬性规则：北洋园不接收动漫资源，命中即拒绝，不做分类改写后强行发布。
	// 这是发布前校验（在读取种子与任何网络请求之前），返回 *publisher.PreCheckError，
	// 上层会按「预检查限制」确定性跳过，而不是失败重试。
	if reason := resolveTJUPTAnimeRestriction(cfg, input); reason != "" {
		return publisher.PublishResult{AttemptDetailLog: "发布前校验失败: " + reason}, publisher.NewPreCheckError(reason)
	}

	uploadURL := baseURL + cfg.UploadSubmitPath

	// 对齐其它发布器：UPLOAD_TEST_MODE=true 时跳过真实发种。
	if os.Getenv("UPLOAD_TEST_MODE") == "true" {
		return publisher.PublishResult{
			PublishURL:       baseURL + "/details.php?id=999999999",
			AttemptDetailLog: fmt.Sprintf("--- [北洋园] 测试模式：跳过实际发种（目标 %s）---", strings.TrimSpace(input.TargetName)),
		}, nil
	}

	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return publisher.PublishResult{}, fmt.Errorf("北洋园发种缺少种子文件路径")
	}
	torrentBytes, err := os.ReadFile(torrentPath)
	if err != nil {
		return publisher.PublishResult{}, fmt.Errorf("读取种子文件失败: %w", err)
	}

	std := map[string]any{}
	if raw, ok := input.UploadData["standardized_params"].(map[string]any); ok && raw != nil {
		std = raw
	}

	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(torrentPath), ".torrent")
	}
	if title == "" {
		return publisher.PublishResult{}, fmt.Errorf("北洋园发种缺少标题")
	}
	title = strings.TrimSpace(tjuptFileExtPattern.ReplaceAllString(title, ""))

	categoryKey := strings.TrimSpace(toStringAny(std["type"], ""))
	mediumKey := strings.TrimSpace(toStringAny(std["medium"], ""))
	sourceKey := strings.TrimSpace(toStringAny(std["source"], ""))
	audioKey := strings.TrimSpace(toStringAny(std["audio_codec"], ""))
	resolutionKey := strings.TrimSpace(toStringAny(std["resolution"], ""))
	tags := collectTJUPTTags(input.UploadData, std)

	categoryID := pickTJUPTValue(cfg.Category, categoryKey, cfg.Defaults.Category)
	if strings.TrimSpace(categoryID) == "" {
		categoryID = cfg.Defaults.Category
	}

	notes := make([]string, 0, 8)
	fields := map[string]string{
		tjuptFieldCategory: categoryID,
	}

	// 主标题：站点没有 name 字段，主标题由服务端从种子文件读取；
	// 中文名(cname)/英文名(ename) 分开填写，0Day 名进 ename。
	ename := title
	if ename != "" {
		fields[tjuptFieldEName] = ename
	}
	cname := resolveTJUPTChineseName(input.Description, input.Subtitle, title)
	if cname != "" {
		fields[tjuptFieldCName] = cname
	}
	if subtitle := strings.TrimSpace(input.Subtitle); subtitle != "" {
		fields["small_descr"] = subtitle
	}
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = title
	}
	// 北洋园没有独立的 mediainfo 输入框（upload.php 只有 descr 一个 textarea），
	// 站点要求把 MediaInfo/BDInfo 一并写进简介正文，并用 [mediainfo][/mediainfo] 包裹
	// （页面上 name=mediainfo 的 BBCode 快捷按钮生成的正是这对标签），
	// 且顺序固定为「简介详情 → MediaInfo → 截图」，不能简单追加到简介末尾。
	mediaText := resolveTJUPTMediaInfo(input)
	descr := description
	if mediaText != "" && !isTJUPTMediaInfoWrapped(descr) {
		descr = insertTJUPTMediaInfo(descr, resolveTJUPTMediaInfoAnchor(input), mediaText)
	}
	fields["descr"] = descr
	if imdb := strings.TrimSpace(input.IMDbLink); imdb != "" {
		fields["url"] = imdb
	}

	switch categoryID {
	case tjuptCatMovie, tjuptCatMobileVideo:
		fields[tjuptFieldDistrict] = pickTJUPTValue(cfg.District[categoryID], sourceKey, cfg.Defaults.District)
	case tjuptCatShow:
		fields[tjuptFieldDistrict] = pickTJUPTValue(cfg.District[categoryID], sourceKey, cfg.Defaults.District)
		if issued := resolveTJUPTIssueDate(title, false); issued != "" {
			fields[tjuptFieldIssueDate] = issued
		}
	case tjuptCatTV:
		fields[tjuptFieldSpecificCat] = pickTJUPTValue(cfg.SpecificCat[categoryID], sourceKey, cfg.Defaults.SpecificCat)
	case tjuptCatMaterial:
		fields[tjuptFieldSpecificCat] = resolveTJUPTKeywordCategory(cfg, categoryID, title)
		fields[tjuptFieldFormat] = pickTJUPTValue(cfg.Format[categoryID], mediumKey, cfg.Defaults.Format)
	case tjuptCatAnime:
		// 保留分支以便将来放开动漫：当前入口处已由 resolveTJUPTAnimeRestriction 拒绝，正常不可达。
		fields[tjuptFieldSpecificCat] = resolveTJUPTKeywordCategory(cfg, categoryID, title)
		fields[tjuptFieldFormat] = pickTJUPTValue(cfg.Format[categoryID], mediumKey, cfg.Defaults.Format)
		fields[tjuptFieldDistrict] = pickTJUPTValue(cfg.District[categoryID], sourceKey, cfg.Defaults.District)
		if issued := resolveTJUPTIssueDate(title, true); issued != "" {
			fields[tjuptFieldIssueDate] = issued
		}
		fields[tjuptFieldAnimeNum] = resolveTJUPTAnimeNum(title, cfg.Defaults.Animenum)
		fields[tjuptFieldSubsteam] = resolveTJUPTSubsteam(title, cfg.Defaults.Substeam)
		if res := resolveTJUPTResolution(resolutionKey); res != "" {
			fields[tjuptFieldResolution] = res
		}
	case tjuptCatMusic:
		fields[tjuptFieldSpecificCat] = pickTJUPTValue(cfg.SpecificCat[categoryID], mediumKey, cfg.Defaults.SpecificCat)
		// 先看载体形态（BD 原盘 → BDMV），再看音轨编码（FLAC/MP3…），都没有才落兜底。
		fields[tjuptFieldFormat] = pickFirstTJUPTValue(
			[]map[string]string{cfg.Format[categoryID]},
			[]string{mediumKey, audioKey},
			cfg.Defaults.Format,
		)
		fields[tjuptFieldHQName] = title
		fields[tjuptFieldArtist] = cfg.Defaults.Artist
	case tjuptCatSports:
		fields[tjuptFieldSpecificCat] = resolveTJUPTKeywordCategory(cfg, categoryID, title)
		fields[tjuptFieldFormat] = pickTJUPTValue(cfg.Format[categoryID], mediumKey, cfg.Defaults.Format)
		if res := resolveTJUPTResolution(resolutionKey); res != "" {
			fields[tjuptFieldResolution] = res
		}
	case tjuptCatSoftware, tjuptCatGame:
		fields[tjuptFieldSpecificCat] = pickTJUPTValue(cfg.SpecificCat[categoryID], "", cfg.Defaults.SpecificCat)
		fields[tjuptFieldFormat] = pickTJUPTValue(cfg.Format[categoryID], mediumKey, cfg.Defaults.Format)
		fields[tjuptFieldPlatform] = resolveTJUPTPlatform(cfg, categoryID, title)
	case tjuptCatOther:
		fields[tjuptFieldSpecificCat] = pickTJUPTValue(cfg.SpecificCat[categoryID], "", cfg.Defaults.SpecificCat)
		fields[tjuptFieldFormat] = pickTJUPTValue(cfg.Format[categoryID], mediumKey, cfg.Defaults.Format)
	case tjuptCatDocumentary:
		// 411 的 specificcat 语义是「来源平台 / 制片地区」：先按标题匹配平台名，未命中再落地区。
		spec := resolveTJUPTKeywordCategory(cfg, categoryID, title)
		if strings.TrimSpace(spec) == "" || spec == cfg.Defaults.SpecificCat {
			if mapped, ok := pickTJUPTValueEx(cfg.District[tjuptCatMovie], sourceKey, ""); ok {
				spec = mapped
			}
		}
		fields[tjuptFieldSpecificCat] = spec
	}

	// 独立 checkbox 型特性字段（站点 value=yes，未命中即不提交）。
	// 中文字幕：站点原文「中文字幕(华语影视不勾选)」——
	// 先判是否华语作品（华语影视一律不勾），再判是否带中字。
	chineseSubtitle := false
	if _, isVideoCategory := tjuptVideoCategoryIDs[categoryID]; isVideoCategory &&
		!isTJUPTChineseWork(categoryID, fields, sourceKey) &&
		hasTJUPTChineseSubtitle(tags, description, input.Subtitle, mediaText) {
		fields["chinese"] = "yes"
		chineseSubtitle = true
	}
	for _, tag := range tags {
		switch strings.TrimSpace(strings.TrimPrefix(strings.ToLower(tag), "tag.")) {
		case "应求":
			fields["response"] = "yes"
		case "禁转":
			fields["exclusive"] = "yes"
		}
	}
	// TJUPT 小组作品：非动漫看 ename 是否以 -TJUPT 结尾，动漫看字幕组是否为 TJUPT。
	substeam := fields[tjuptFieldSubsteam]
	if categoryID == tjuptCatAnime {
		if strings.HasSuffix(strings.ToLower(strings.TrimSpace(substeam)), "tjupt") {
			fields["tjuptrip"] = "yes"
		}
	} else if strings.HasSuffix(strings.ToLower(ename), "-tjupt") {
		fields["tjuptrip"] = "yes"
	}

	// 无来源字段的兜底填写属于「凑齐必填」，站点可能判为信息不完整，明确写进日志便于事后核对。
	switch categoryID {
	case tjuptCatAnime:
		notes = append(notes, fmt.Sprintf(
			"动漫话数(animenum)=%s、字幕组(substeam)=%s 为站点必填但 PTNexus 侧无来源，按标题启发式/兜底填充，请人工复核",
			emptyDash(fields[tjuptFieldAnimeNum]), emptyDash(fields[tjuptFieldSubsteam]),
		))
		if tjuptAnimeCodecHintPattern.MatchString(ename) {
			notes = append(notes, "英文名包含视频编码标记，北洋园漫版规则要求去除（未自动改写以避免篡改 0Day 名，请人工确认）")
		}
	case tjuptCatMusic:
		notes = append(notes, fmt.Sprintf(
			"音乐艺术家(artist)=%s、专辑名(hqname)=主标题 为站点必填但 PTNexus 侧无来源，请人工复核",
			emptyDash(fields[tjuptFieldArtist]),
		))
	case tjuptCatSoftware, tjuptCatGame:
		notes = append(notes, fmt.Sprintf("运行平台(platform)=%s 为站点必填，按标题关键词/兜底填充，请人工复核",
			emptyDash(fields[tjuptFieldPlatform])))
	case tjuptCatMaterial, tjuptCatSports:
		notes = append(notes, fmt.Sprintf("二级分类(specificcat)=%s 为站点必填，按标题关键词/兜底填充，请人工复核",
			emptyDash(fields[tjuptFieldSpecificCat])))
	}

	logLines := []string{
		fmt.Sprintf("北洋园字段装配: 一级分类=%s 中文名=%s 英文名=%s 二级分类=%s 地区=%s 格式=%s",
			categoryID,
			emptyDash(cname),
			emptyDash(ename),
			emptyDash(firstNonEmpty(fields[tjuptFieldSpecificCat], "-")),
			emptyDash(firstNonEmpty(fields[tjuptFieldDistrict], "-")),
			emptyDash(firstNonEmpty(fields[tjuptFieldFormat], "-")),
		),
		fmt.Sprintf("北洋园简介装配: MediaInfo=%s 中文字幕=%s",
			boolLabel(mediaText != "" && isTJUPTMediaInfoWrapped(descr), "已内嵌[mediainfo](位于截图之前)", "无"),
			boolLabel(chineseSubtitle, "勾选", "不勾选"),
		),
	}
	for _, note := range notes {
		logLines = append(logLines, "提示: "+note)
	}

	torrentName := filepath.Base(torrentPath)
	publishURL, existing, attemptDetail, attemptErr := publishuploader.TryUploadTorrent(
		uploadURL,
		baseURL,
		cookie,
		cfg.UploadFileField,
		torrentBytes,
		torrentName,
		fields,
	)
	if strings.TrimSpace(attemptDetail) != "" {
		logLines = append(logLines, attemptDetail)
	}

	result := publisher.PublishResult{
		PublishURL:        publishURL,
		IsExistingTorrent: existing,
		UploadFormFields:  fields,
		AttemptDetailLog:  strings.Join(logLines, "\n"),
	}
	if attemptErr != nil {
		return result, attemptErr
	}
	return result, nil
}

// loadTJUPTConfig 读取 configs/tjupt.yaml 并与内置兜底配置合并。
// 参数/返回：无入参；返回合并后的配置（每次返回独立副本，避免污染全局兜底配置）。
// 失败场景：文件缺失或解析失败时返回内置兜底配置。
// 副作用：读取磁盘文件。
func loadTJUPTConfig() tjuptConfig {
	cfg := tjuptConfig{
		SiteName:         tjuptDefaultConfig.SiteName,
		UploadFileField:  tjuptDefaultConfig.UploadFileField,
		UploadPagePath:   tjuptDefaultConfig.UploadPagePath,
		UploadSubmitPath: tjuptDefaultConfig.UploadSubmitPath,
		Category:         cloneTJUPTMap(tjuptDefaultConfig.Category),
		District:         map[string]map[string]string{},
		SpecificCat:      map[string]map[string]string{},
		Format:           map[string]map[string]string{},
		Platform:         map[string]map[string]string{},
		Defaults:         tjuptDefaultConfig.Defaults,
	}

	paths := config.ResolveRuntimePaths()
	data, err := os.ReadFile(filepath.Join(paths.BaseDir, "configs", "tjupt.yaml"))
	if err != nil {
		return cfg
	}
	var override tjuptConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return cfg
	}

	if v := strings.TrimSpace(override.SiteName); v != "" {
		cfg.SiteName = v
	}
	if v := strings.TrimSpace(override.UploadFileField); v != "" {
		cfg.UploadFileField = v
	}
	if v := strings.TrimSpace(override.UploadPagePath); v != "" {
		cfg.UploadPagePath = v
	}
	if v := strings.TrimSpace(override.UploadSubmitPath); v != "" {
		cfg.UploadSubmitPath = v
	}
	mergeTJUPTMap(cfg.Category, override.Category)
	cfg.District = mergeTJUPTNestedMap(cfg.District, override.District)
	cfg.SpecificCat = mergeTJUPTNestedMap(cfg.SpecificCat, override.SpecificCat)
	cfg.Format = mergeTJUPTNestedMap(cfg.Format, override.Format)
	cfg.Platform = mergeTJUPTNestedMap(cfg.Platform, override.Platform)
	if v := strings.TrimSpace(override.Defaults.Category); v != "" {
		cfg.Defaults.Category = v
	}
	if v := strings.TrimSpace(override.Defaults.District); v != "" {
		cfg.Defaults.District = v
	}
	if v := strings.TrimSpace(override.Defaults.SpecificCat); v != "" {
		cfg.Defaults.SpecificCat = v
	}
	if v := strings.TrimSpace(override.Defaults.Format); v != "" {
		cfg.Defaults.Format = v
	}
	if v := strings.TrimSpace(override.Defaults.Platform); v != "" {
		cfg.Defaults.Platform = v
	}
	if v := strings.TrimSpace(override.Defaults.Artist); v != "" {
		cfg.Defaults.Artist = v
	}
	if v := strings.TrimSpace(override.Defaults.Substeam); v != "" {
		cfg.Defaults.Substeam = v
	}
	if v := strings.TrimSpace(override.Defaults.Animenum); v != "" {
		cfg.Defaults.Animenum = v
	}
	return cfg
}

func cloneTJUPTMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func mergeTJUPTMap(base, override map[string]string) {
	for key, value := range override {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		base[trimmedKey] = strings.TrimSpace(value)
	}
}

func mergeTJUPTNestedMap(base, override map[string]map[string]string) map[string]map[string]string {
	merged := make(map[string]map[string]string, len(base)+len(override))
	for key, inner := range base {
		merged[key] = cloneTJUPTMap(inner)
	}
	for outerKey, inner := range override {
		trimmedOuter := strings.TrimSpace(outerKey)
		if trimmedOuter == "" {
			continue
		}
		target, exists := merged[trimmedOuter]
		if !exists || target == nil {
			target = map[string]string{}
			merged[trimmedOuter] = target
		}
		mergeTJUPTMap(target, inner)
	}
	return merged
}

// pickTJUPTValue 在映射表中按标准化值查找站点取值，未命中返回 fallback。
// 参数/返回：mapping 为字典；standardized 为 PTNexus 标准化值（如 source.china）；fallback 为兜底值。
// 失败场景：mapping 为空或未命中时返回 fallback。
// 副作用：无。
func pickTJUPTValue(mapping map[string]string, standardized, fallback string) string {
	value, _ := pickTJUPTValueEx(mapping, standardized, fallback)
	return value
}

// pickTJUPTExact 只做「标准化值 → 站点取值」的精确查找（含去前缀兜底），未命中返回空串。
// 说明：北洋园的字典按一级分类分表，每张表自带 default；default 属于「表内兜底」，
// 不能和调用方的 fallback 混在一起，否则「先试 A 再试 B」的多值查找会被表的 default 提前截断。
func pickTJUPTExact(mapping map[string]string, standardized string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(standardized))
	if key == "" || len(mapping) == 0 {
		return "", false
	}
	if mapped, ok := pickIgnoreCase(mapping, key); ok {
		return mapped, true
	}
	if idx := strings.Index(key, "."); idx > 0 && idx < len(key)-1 {
		if mapped, ok := pickIgnoreCase(mapping, key[idx+1:]); ok {
			return mapped, true
		}
	}
	return "", false
}

// pickTJUPTValueEx 在映射表中查找站点取值：先按标准化值查，未命中再落该表的 default，最后才用 fallback。
func pickTJUPTValueEx(mapping map[string]string, standardized, fallback string) (string, bool) {
	if value, ok := pickTJUPTExact(mapping, standardized); ok {
		return value, true
	}
	if value, ok := pickIgnoreCase(mapping, "default"); ok {
		return value, true
	}
	return strings.TrimSpace(fallback), false
}

func pickIgnoreCase(mapping map[string]string, key string) (string, bool) {
	if len(mapping) == 0 {
		return "", false
	}
	if value, ok := mapping[key]; ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value), true
	}
	for candidate, value := range mapping {
		if strings.EqualFold(strings.TrimSpace(candidate), key) && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), true
		}
	}
	return "", false
}

// pickFirstTJUPTValue 依次用多个标准化值尝试若干字典（只看精确命中），全未命中再落字典 default / fallback。
func pickFirstTJUPTValue(mappings []map[string]string, standards []string, fallback string) string {
	for _, mapping := range mappings {
		for _, standard := range standards {
			if value, ok := pickTJUPTExact(mapping, standard); ok {
				return value
			}
		}
	}
	for _, mapping := range mappings {
		if value, ok := pickIgnoreCase(mapping, "default"); ok {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

// resolveTJUPTChineseName 提取「中文名」。
// 参数/返回：description 为简介（NexusPHP 形如「◎译　　名 xxx」）；subtitle 为副标题；title 为 0Day 主标题。
// 返回清理后的单一中文片名；全部来源都拿不到时回退到 title。
// 失败场景：无。
// 副作用：无。
func resolveTJUPTChineseName(description, subtitle, title string) string {
	candidates := make([]string, 0, 4)
	if match := tjuptAliasLinePattern.FindStringSubmatch(description); len(match) > 1 {
		candidates = append(candidates, match[1])
	}
	if trimmed := strings.TrimSpace(subtitle); trimmed != "" {
		candidates = append(candidates, trimmed)
	}
	for _, candidate := range candidates {
		if cleaned := cleanTJUPTChineseName(candidate); cleaned != "" {
			return cleaned
		}
	}
	// 兜底：标题本身含中文时直接使用，否则仍返回标题（站点前端会给出「中文名中没有中文」提示）。
	return strings.TrimSpace(title)
}

// cleanTJUPTChineseName 按站点规则清理中文名：只取第一个片名、去掉集数描述与扩展名。
func cleanTJUPTChineseName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	// 译名行常见「大陆译名/台译名/港译名」并列，站点要求只填一个大陆译名。
	for _, separator := range []string{"/", "|", "、", "；", ";"} {
		if idx := strings.Index(trimmed, separator); idx > 0 {
			trimmed = strings.TrimSpace(trimmed[:idx])
		}
	}
	trimmed = strings.TrimSpace(tjuptFileExtPattern.ReplaceAllString(trimmed, ""))
	if loc := tjuptEpisodeDescPattern.FindStringIndex(trimmed); loc != nil {
		trimmed = strings.TrimSpace(trimmed[:loc[0]])
	}
	trimmed = strings.TrimSpace(strings.Trim(trimmed, "《》【】（）()[] "))
	if trimmed == "" || !containsCJK(trimmed) {
		return ""
	}
	return trimmed
}

func containsCJK(text string) bool {
	for _, r := range text {
		if r >= 0x4e00 && r <= 0x9fa5 {
			return true
		}
	}
	return false
}

// resolveTJUPTKeywordCategory 按标题关键词推断无来源的二级分类，未命中返回字典 default。
func resolveTJUPTKeywordCategory(cfg tjuptConfig, categoryID, title string) string {
	lower := strings.ToLower(title)
	for _, rule := range tjuptSecondaryKeywords[categoryID] {
		for _, keyword := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				return rule.Value
			}
		}
	}
	return pickTJUPTValue(cfg.SpecificCat[categoryID], "", cfg.Defaults.SpecificCat)
}

// resolveTJUPTIssueDate 从标题推断发行时间。
// 参数/返回：title 为 0Day 名；animeMonth 为 true 时输出「YYYY年MM月」（405 动漫要求），否则输出「YYYYMMDD」。
// 失败场景：标题中找不到年份时返回空串（由调用方决定是否提交）。
// 副作用：无。
func resolveTJUPTIssueDate(title string, animeMonth bool) string {
	year := ""
	if match := tjuptYearPattern.FindString(title); match != "" {
		year = match
	}
	if year == "" {
		return ""
	}
	if !animeMonth {
		// 综艺允许 YYYY 或 YYYYMMDD，缺月份时按 YYYY0101 补齐，避免只填年份被判定格式错误。
		if match := tjuptMonthPattern.FindStringSubmatch(title); len(match) > 1 {
			month := match[1]
			if len(month) == 1 {
				month = "0" + month
			}
			return fmt.Sprintf("%s%s01", year, month)
		}
		return year + "0101"
	}
	month := "01"
	if match := tjuptMonthPattern.FindStringSubmatch(title); len(match) > 1 {
		digits := match[1]
		if len(digits) == 1 {
			digits = "0" + digits
		}
		month = digits
	}
	return fmt.Sprintf("%s年%s月", year, month)
}

// resolveTJUPTAnimeNum 从标题推断动漫话数，无法判断时返回 fallback。
func resolveTJUPTAnimeNum(title, fallback string) string {
	match := tjuptAnimeNumPattern.FindStringSubmatch(title)
	if len(match) == 0 {
		return strings.TrimSpace(fallback)
	}
	// 捕获组顺序：1=季号 2=区间起始 3=区间结束 4=全集数 5=第N话。
	// 站点要的是「总话数」，故区间取结束值、全集数优先于季号，区间起始值不使用。
	for _, idx := range []int{3, 4, 5, 1} {
		if idx >= len(match) {
			continue
		}
		if value := trimTJUPTLeadingZeros(match[idx]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fallback)
}

// trimTJUPTLeadingZeros 去掉话数的前导零（站点习惯写法为 1、12 而不是 01、012）。
func trimTJUPTLeadingZeros(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimLeft(trimmed, "0")
	if trimmed == "" {
		return "0"
	}
	return trimmed
}

// resolveTJUPTSubsteam 从 0Day 名尾部推断字幕组/制作组，无法判断时返回 fallback。
func resolveTJUPTSubsteam(title, fallback string) string {
	match := tjuptReleaseGroupPattern.FindStringSubmatch(strings.TrimSpace(title))
	if len(match) > 1 {
		if group := strings.TrimSpace(match[1]); group != "" {
			return group
		}
	}
	return strings.TrimSpace(fallback)
}

// resolveTJUPTPlatform 按标题关键词推断运行平台（408 软件 / 409 游戏 必填），未命中返回字典 default。
func resolveTJUPTPlatform(cfg tjuptConfig, categoryID, title string) string {
	lower := strings.ToLower(title)
	for _, rule := range tjuptPlatformKeywords {
		for _, keyword := range rule.Keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				return rule.Value
			}
		}
	}
	return pickTJUPTValue(cfg.Platform[categoryID], "", cfg.Defaults.Platform)
}

// collectTJUPTTags 汇总发布数据里的标签，供 checkbox 型特性字段使用。
func collectTJUPTTags(uploadData, standardized map[string]any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 16)
	appendTags := func(value any) {
		for _, tag := range parseStringArray(value) {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			if _, exists := seen[trimmed]; exists {
				continue
			}
			seen[trimmed] = struct{}{}
			result = append(result, trimmed)
		}
	}
	if standardized != nil {
		appendTags(standardized["tags"])
	}
	if uploadData != nil {
		appendTags(uploadData["tags"])
		if sourceParams, ok := uploadData["source_params"].(map[string]any); ok && sourceParams != nil {
			appendTags(sourceParams["标签"])
		}
	}
	return result
}

// tjuptVideoCategoryIDs 需要判定「中文字幕」的影视/视频类一级分类。
// 站点该 checkbox 对全部分类可见，但中文字幕只对影视、视频类资源有意义（音乐/软件/游戏等不勾）。
var tjuptVideoCategoryIDs = map[string]struct{}{
	tjuptCatMovie:       {},
	tjuptCatTV:          {},
	tjuptCatShow:        {},
	tjuptCatAnime:       {},
	tjuptCatSports:      {},
	tjuptCatDocumentary: {},
	tjuptCatMobileVideo: {},
}

// tjuptChineseSubtitleTags 中文字幕标签归一集合（去 tag. 前缀、小写）。
// 与 tagging 包 extractRawTagsFromSubtitle / applySubtitleTagsFromTextSection 的产出词汇保持一致。
var tjuptChineseSubtitleTags = map[string]struct{}{
	"中字": {}, "中文字幕": {}, "简繁": {}, "简体": {}, "繁体": {},
	"简繁字幕": {}, "中英": {}, "双语字幕": {}, "官译": {},
	"chs": {}, "cht": {}, "chinese": {}, "zh-cn": {}, "zh-tw": {},
}

// resolveTJUPTMediaInfo 取发布数据里的 MediaInfo / BDInfo 原文。
// 参数/返回：input 为统一发布输入；返回剔除首尾空白后的媒体文本，取不到时为空串。
// 失败场景：uploadData 缺失时退到 input.MediaInfo，仍为空则返回空串。
// 副作用：无。
func resolveTJUPTMediaInfo(input publisher.PublishInput) string {
	for _, key := range []string{"mediainfo", "media_info", "mediainfo_text"} {
		if value := strings.TrimSpace(toStringAny(input.UploadData[key], "")); value != "" {
			return value
		}
	}
	return strings.TrimSpace(input.MediaInfo)
}

// isTJUPTMediaInfoWrapped 判断简介正文里是否已经带了 MediaInfo / BDInfo 标签，避免重复追加。
func isTJUPTMediaInfoWrapped(description string) bool {
	lower := strings.ToLower(description)
	return strings.Contains(lower, "[mediainfo]") || strings.Contains(lower, "[bdinfo]")
}

// resolveTJUPTMediaInfoAnchor 取简介里的截图段文本，用于把 MediaInfo 锚定到截图之前。
// 参数/返回：input 为统一发布输入；优先取 uploadData 顶层的 screenshots，再取 intro.screenshots；都没有返回空串。
// 说明：取值键与 uploader.BuildUploadDescription 保持一致（顶层优先、intro 兜底），
// 这样锚点文本一定会出现在已拼好的简介里。
func resolveTJUPTMediaInfoAnchor(input publisher.PublishInput) string {
	if value := strings.TrimSpace(toStringAny(input.UploadData["screenshots"], "")); value != "" {
		return value
	}
	if intro, ok := input.UploadData["intro"].(map[string]any); ok && intro != nil {
		return strings.TrimSpace(toStringAny(intro["screenshots"], ""))
	}
	return ""
}

// insertTJUPTMediaInfo 按站点要求的顺序插入 MediaInfo 块：简介详情 → MediaInfo → 截图。
// 参数/返回：description 为已拼好的简介；anchor 为截图段文本；mediaText 为 MediaInfo/BDInfo 原文。
// 失败场景：mediaText 为空时原样返回简介。
// 副作用：无。
// 说明：站点 upload.php 没有独立的 mediainfo 字段，MediaInfo 只能并进 descr，
// 位置必须落在截图之前——截图段通常是简介的最后一段，因此在简介中定位该段文本并插到它前面；
// 定位不到（调用方没带截图段）时退化为追加到正文末尾。
func insertTJUPTMediaInfo(description, anchor, mediaText string) string {
	trimmedMedia := strings.TrimSpace(mediaText)
	trimmedDesc := strings.TrimSpace(description)
	if trimmedMedia == "" {
		return trimmedDesc
	}

	block := "[mediainfo]" + trimmedMedia + "[/mediainfo]"
	if trimmedAnchor := strings.TrimSpace(anchor); trimmedAnchor != "" {
		if idx := strings.LastIndex(trimmedDesc, trimmedAnchor); idx >= 0 {
			head := strings.TrimSpace(trimmedDesc[:idx])
			tail := strings.TrimSpace(trimmedDesc[idx:])
			if head == "" {
				return block + "\n\n" + tail
			}
			return head + "\n\n" + block + "\n\n" + tail
		}
	}
	if trimmedDesc == "" {
		return block
	}
	return trimmedDesc + "\n\n" + block
}

// isTJUPTChineseSubtitleTag 判断单个标签是否为中文字幕标签（兼容 tag. 前缀与大小写）。
func isTJUPTChineseSubtitleTag(tag string) bool {
	bare := strings.ToLower(strings.TrimSpace(tag))
	bare = strings.TrimSpace(strings.TrimPrefix(bare, "tag."))
	if bare == "" {
		return false
	}
	_, ok := tjuptChineseSubtitleTags[bare]
	return ok
}

// hasTJUPTChineseSubtitle 判断资源是否带中文字幕。
// 参数/返回：tags 为发布标签；description/subtitle 为简介与副标题；mediaText 为 MediaInfo/BDInfo 原文。
// 判定来源按可信度排序：发布标签 → 媒体文本字幕段（MediaInfo 的 Text 段 / BDInfo 的 SUBTITLES 段）→ 简介与副标题文本。
// 失败场景：三处都无线索时返回 false（宁可不勾，避免给纯外文资源错打中字）。
// 副作用：无。
func hasTJUPTChineseSubtitle(tags []string, description, subtitle, mediaText string) bool {
	for _, tag := range tags {
		if isTJUPTChineseSubtitleTag(tag) {
			return true
		}
	}
	if trimmed := strings.TrimSpace(mediaText); trimmed != "" {
		_, isBDInfo, _ := processingmedia.ValidateMediaInfoFormat(trimmed)
		for _, tag := range tagging.ExtractRawTagsFromMediaText(trimmed, isBDInfo) {
			if isTJUPTChineseSubtitleTag(tag) {
				return true
			}
		}
	}
	return tjuptChineseSubtitlePattern.MatchString(description) || tjuptChineseSubtitlePattern.MatchString(subtitle)
}

// isTJUPTChineseWork 判断是否为华语影视（华语影视不勾选「中文字幕」）。
// 判定来源：站点地区字段 → 标准化产地（source.*）。
// 说明：各一级分类承载「地区」语义的字段不同，必须按分类取，不能把两个字段一锅端——
// 例如 405 动漫的 specificcat 是动画形态（TV/剧场/OVA），和 district（日漫/国产）一起判会互相干扰。
func isTJUPTChineseWork(categoryID string, fields map[string]string, sourceKey string) bool {
	var regionValues []string
	switch categoryID {
	case tjuptCatMovie, tjuptCatShow, tjuptCatAnime, tjuptCatMobileVideo:
		// district = 制片国家/地区
		regionValues = append(regionValues, fields[tjuptFieldDistrict])
	case tjuptCatTV, tjuptCatDocumentary:
		// 402 specificcat = 大陆/港台/美剧/日剧…；411 specificcat = 来源平台或制片地区
		regionValues = append(regionValues, fields[tjuptFieldSpecificCat])
	default:
		regionValues = append(regionValues, fields[tjuptFieldDistrict], fields[tjuptFieldSpecificCat])
	}
	if isTJUPTChineseRegion(regionValues...) {
		return true
	}
	key := strings.ToLower(strings.TrimSpace(sourceKey))
	if idx := strings.Index(key, "."); idx >= 0 && idx+1 < len(key) {
		key = key[idx+1:]
	}
	switch key {
	case "china", "hongkong", "taiwan", "hongkong_taiwan", "macau":
		return true
	}
	return false
}

// isTJUPTChineseRegion 判断地区/二级分类取值是否全部属于华语地区（华语影视不勾选中文字幕）。
func isTJUPTChineseRegion(values ...string) bool {
	hasValue := false
	for _, raw := range values {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		for _, part := range strings.Split(trimmed, "/") {
			item := strings.TrimSpace(part)
			if item == "" {
				continue
			}
			hasValue = true
			switch item {
			case "大陆", "中国大陆", "香港", "中国香港", "澳门", "中国澳门", "台湾", "中国台湾", "港台", "国产":
			default:
				return false
			}
		}
	}
	return hasValue
}

func emptyDash(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "-"
	}
	return trimmed
}

// boolLabel 按条件返回可读文案，用于发布日志。
func boolLabel(condition bool, whenTrue, whenFalse string) string {
	if condition {
		return whenTrue
	}
	return whenFalse
}
