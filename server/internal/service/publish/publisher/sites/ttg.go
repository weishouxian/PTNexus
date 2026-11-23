package sites

import (
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// 定义 TTG 站点在公共表单发布流程上的差异步骤。
type ttgPublisher struct {
	publicSiteDefaults
}

// PublishTTG 执行 TTG 站点特殊发布流程（合并 type 下拉框、提取 IMDB/Douban ID）。
// 参数/返回：input 提供站点信息、发布数据与通用字段；返回发布详情页 URL、是否疑似"种子已存在"、用于自动编辑的表单字段，以及发布过程日志。
// 失败场景：同 Public 发布器。
// 副作用：读取本地种子文件并向目标站点发起上传请求；可选写入 data/tmp/torrents 参数落盘。
func PublishTTG(input publisher.PublishInput) (publisher.PublishResult, error) {
	return publishWithPublicSite(input, ttgPublisher{})
}

// PublishTTGOffer 执行 TTG 候选（申请上传）发布流程。
// TTG 普通账号没有直发权限时只能提交候选：候选页 offer.php → takeoffer.php 只收
// 名称/海报/简介/分类/做种条件，不含种子文件；管理员审核通过后才允许到 upload.php 正式发种。
// 参数/返回：input 由 workflow 统一构建；返回候选链接（offers.php?id=N）与过程日志。
// 失败场景：同 PublishOffer。
// 副作用：向 TTG 发起候选表单提交请求。
func PublishTTGOffer(input publisher.PublishInput) (publisher.PublishResult, error) {
	return publisher.PublishOffer(input, adjustTTGOfferFields)
}

// adjustTTGOfferFields 按 TTG 候选页规则修正字段：分类仍是单一 type 下拉框，
// 由 分类 + 媒介 + 分辨率 合并计算（与正式发种同一套规则）。
func adjustTTGOfferFields(input publisher.PublishInput, formFields map[string]string) {
	if formFields == nil {
		return
	}
	standardized := extractTTGStandardizedParams(input.UploadData)
	category := strings.TrimSpace(toStringAny(standardized["type"], ""))
	medium := strings.TrimSpace(toStringAny(standardized["medium"], ""))
	resolution := strings.TrimSpace(toStringAny(standardized["resolution"], ""))
	if ttgType := resolveTTGType(category, medium, resolution, hasAnimationTag(input.UploadData)); ttgType != "" {
		formFields["type"] = ttgType
	}
}

func (ttgPublisher) LogModule() string {
	return "发布-TTG"
}

func (ttgPublisher) AdjustFormFields(input publisher.PublishInput, formFields map[string]string) {
	if formFields == nil {
		return
	}

	standardized := extractTTGStandardizedParams(input.UploadData)

	// TTG 使用单一 type 下拉框，合并 category + medium + resolution
	category := strings.TrimSpace(toStringAny(standardized["type"], ""))
	medium := strings.TrimSpace(toStringAny(standardized["medium"], ""))
	resolution := strings.TrimSpace(toStringAny(standardized["resolution"], ""))

	if ttgType := resolveTTGType(category, medium, resolution, hasAnimationTag(input.UploadData)); ttgType != "" {
		formFields["type"] = ttgType
	}

	// 移除 medium/resolution 等可能残留的无效字段（TTG 无此表单字段）
	delete(formFields, "medium")
	delete(formFields, "codec")
	delete(formFields, "audiocodec")
	delete(formFields, "standard")

	// 提取 IMDB ID（TTG 的 imdb_c 字段期望纯 ID，如 tt0000000）
	if imdbURL := strings.TrimSpace(formFields["imdb_c"]); imdbURL != "" && strings.Contains(imdbURL, "imdb") {
		if id := extractTTGIMDBID(imdbURL); id != "" {
			formFields["imdb_c"] = id
		}
	}

	// 提取 Douban ID（TTG 的 douban_id 字段期望纯数字 ID）
	if doubanURL := strings.TrimSpace(formFields["douban_id"]); doubanURL != "" && strings.Contains(doubanURL, "douban") {
		if id := extractTTGDoubanID(doubanURL); id != "" {
			formFields["douban_id"] = id
		}
	}

	// 匿名发布（anonymity）由全局设置 upload_settings.anonymous_upload 控制，
	// 交由公共发种器的 ApplyAnonymousFormFields 统一注入（详见 anonymous.go / public.go），此处不再硬编码。
	//
	// 禁转（nodistr）是 yes/no 下拉，跟随标准标签 tag.禁转；控件默认值即 no。
	if hasTTGNoRedistributeTag(input.UploadData) {
		formFields["nodistr"] = "yes"
	} else if _, exists := formFields["nodistr"]; !exists {
		formFields["nodistr"] = "no"
	}
	if _, exists := formFields["hr"]; !exists {
		formFields["hr"] = "no"
	}

	// TTG 发种标题规范：主标题原样、副标题独立成栏（详见 adjustTTGTitle）
	adjustTTGTitle(formFields)
}

// resolveTTGType 根据分类、媒介和分辨率的组合，返回 TTG 的 type 值。
func resolveTTGType(category, medium, resolution string, isAnimation bool) string {
	isBluRay := medium == "medium.bluray" || medium == "medium.uhd_bluray" ||
		medium == "medium.bluray_diy" || medium == "medium.uhd_diy"
	isUHD := medium == "medium.uhd_bluray" || medium == "medium.uhd_diy" ||
		resolution == "resolution.r2160p"
	if isAnimation {
		if isBluRay {
			return "111" // 动漫原盘
		}
		return "58" // 高清动漫
	}

	switch category {
	case "category.movie":
		if isBluRay && isUHD {
			return "109" // UHD原盘
		}
		if isBluRay {
			return "54" // BluRay原盘
		}
		switch resolution {
		case "resolution.r2160p":
			return "108" // 影视2160p
		case "resolution.r720p":
			return "52" // 电影720p
		default:
			return "53" // 电影1080i/p
		}

	// 标准类型键存在两种写法：全局映射产出 category.documentaries，
	// 早期站点配置里写作 category.documentary，两种都要认，否则会落到默认 type。
	case "category.documentary", "category.documentaries":
		if isBluRay {
			return "67" // 纪录片BluRay原盘
		}
		switch resolution {
		case "resolution.r720p":
			return "62" // 纪录片720p
		default:
			return "63" // 纪录片1080i/p
		}

	case "category.tv_series":
		// 需要更细粒度的地区区分，但标准化参数通常不包含地区信息
		// 默认使用大陆港台剧（最常见的中文 PT 场景）
		switch resolution {
		case "resolution.r720p":
			return "76" // 大陆港台剧720p(单集)
		default:
			return "75" // 大陆港台剧1080i/p(单集)
		}

	case "category.tv_shows":
		return "90" // 华语剧包(全集)

	case "category.mv":
		return "59" // MV&演唱会

	case "category.music":
		return "83" // 无损音乐FLAC&APE

	case "category.sports":
		return "57" // 高清体育节目

	case "category.other":
		return "91" // MiniVideo
	}

	return ""
}

func extractTTGStandardizedParams(uploadData map[string]any) map[string]any {
	if uploadData == nil {
		return nil
	}
	if item, ok := uploadData["standardized_params"].(map[string]any); ok && item != nil {
		return item
	}
	return nil
}

var reTTGIMDBID = regexp.MustCompile(`(tt\d{7,})`)

func extractTTGIMDBID(value string) string {
	if matches := reTTGIMDBID.FindStringSubmatch(value); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

var reTTGDoubanID = regexp.MustCompile(`douban\.com/(?:movie|subject)/(\d+)`)

func extractTTGDoubanID(value string) string {
	if matches := reTTGDoubanID.FindStringSubmatch(value); len(matches) > 1 {
		return matches[1]
	}
	// 如果已经是纯数字，直接返回
	trimmed := strings.TrimSpace(value)
	if trimmed != "" && regexp.MustCompile(`^\d+$`).MatchString(trimmed) {
		return trimmed
	}
	return ""
}

// adjustTTGTitle 按 TTG 新版 upload.php 表单规范处理标题相关字段。
//
// 新版表单已拆成三栏：
//   - name：主标题，按 0day 命名原样填写（点号保留，站点现有种子形如
//     "The Odyssey 2026 1080p USA Blu-ray AVC DTS-HD MA 5.1-DIY@TTG"）；
//   - subtitle（maxlength=55）：副标题，页面明确「此处不再需要加 '[' 和 ']' 来换行」；
//   - highlight：副标题右侧的加粗标签（如 "*内封简繁字幕"），非必填，本项目暂不生成。
//
// 因此这里只做两件事：
//  1. 把副标题从 NexusPHP 默认的 small_descr 搬到 subtitle 字段，剥掉 [] 并按 55 字符截断；
//  2. 主标题保持原样 —— 旧版的 "." → "{@}" 替换与「副标题以 [] 包裹后拼进主标题」均已废弃，
//     那套规则会毁标题（"5.1" → "5{@}1"）、让副标题不再显示在独立位置，还超出 55 字限制。
func adjustTTGTitle(formFields map[string]string) {
	// 副标题可能落在 small_descr（NexusPHP 默认）或 subtitle（TTG 配置），兼容读取
	subtitle := strings.TrimSpace(formFields["small_descr"])
	if subtitle == "" {
		subtitle = strings.TrimSpace(formFields["subtitle"])
	}
	// small_descr 不是 TTG 表单字段，避免把无效字段提交给站点
	delete(formFields, "small_descr")

	if subtitle == "" {
		delete(formFields, "subtitle")
		return
	}

	// 剥掉 [] 字符：新版表单自己会换行，再包一层会被站点当作正文
	subtitle = strings.NewReplacer("[", "", "]", "").Replace(subtitle)
	subtitle = strings.Join(strings.Fields(subtitle), " ")

	// 站点 maxlength=55，按 rune 截断，避免把多字节汉字截成半个
	if runes := []rune(subtitle); len(runes) > 55 {
		subtitle = string(runes[:55])
	}
	formFields["subtitle"] = subtitle
}

// hasTTGNoRedistributeTag 判断发布数据里是否带「禁转」标准标签（tag.禁转 / 独占）。
// 参数/返回：uploadData 为发布 payload；命中返回 true。
// 失败场景：无标签或标签不是禁转时返回 false。
// 副作用：无。
func hasTTGNoRedistributeTag(uploadData map[string]any) bool {
	for _, tag := range collectTTGTags(uploadData) {
		normalized := strings.TrimSpace(tag)
		switch normalized {
		case "tag.禁转", "禁转", "独占", "tag.独占":
			return true
		}
	}
	return false
}

// collectTTGTags 汇总标签来源：标准化标签、uploadData.tags、源站「标签」参数。
// 参数/返回：uploadData 为发布 payload；返回标签切片（可能为空）。
// 失败场景：uploadData 为空或标签字段缺失时返回空切片。
// 副作用：无。
func collectTTGTags(uploadData map[string]any) []string {
	if uploadData == nil {
		return nil
	}
	tags := make([]string, 0, 8)
	appendFrom := func(value any) {
		switch v := value.(type) {
		case []string:
			for _, item := range v {
				if trimmed := strings.TrimSpace(item); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		case []any:
			for _, item := range v {
				if trimmed := strings.TrimSpace(toStringAny(item, "")); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		case string:
			for _, item := range strings.Split(v, ",") {
				if trimmed := strings.TrimSpace(item); trimmed != "" {
					tags = append(tags, trimmed)
				}
			}
		}
	}

	if standardized, ok := uploadData["standardized_params"].(map[string]any); ok && standardized != nil {
		appendFrom(standardized["tags"])
	}
	appendFrom(uploadData["tags"])
	if sourceParams, ok := uploadData["source_params"].(map[string]any); ok && sourceParams != nil {
		appendFrom(sourceParams["标签"])
		appendFrom(sourceParams["tags"])
	}
	return tags
}
