package dupe

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

// SiteSettings 描述单个站点的 dupe 校验开关配置（来自 sites 表）。
type SiteSettings struct {
	// Enabled 为站点是否开启 dupe 校验。
	Enabled bool
	// SizeToleranceBytes 为体积容差（字节），<= 0 时回退到默认 1 GiB。
	SizeToleranceBytes int64
}

// DefaultSizeToleranceBytes 为未配置容差时的默认值（1 GiB）。
const DefaultSizeToleranceBytes int64 = 1073741824

// CheckBySite 按站点标识分发到对应的 dupe 检索实现。
// 参数/返回：siteCode 为站点标识；query 为检索输入；返回命中结论、过程日志与错误。
// 失败场景：站点未实现 dupe 检索时返回错误（调用方应先经 SiteSupportsDupe 判定）。
// 副作用：向目标站点发起 1~2 次 GET 请求。
func CheckBySite(siteCode string, query Query) (Result, string, error) {
	switch strings.ToLower(strings.TrimSpace(siteCode)) {
	case "audiences":
		return CheckAudiencesDupe(query)
	case "luckpt":
		return CheckLuckPTDupe(query)
	case "hdhome":
		return CheckHDHomeDupe(query)
	case "pterclub":
		return CheckPterclubDupe(query)
	case "ourbits":
		return CheckOurbitsDupe(query)
	case "chdbits":
		return CheckChdbitsDupe(query)
	default:
		return Result{}, "", fmt.Errorf("站点 %s 未实现 dupe 检索", strings.TrimSpace(siteCode))
	}
}

// ResolveSiteSettings 从 sites 表行（map 形式）读取 dupe 校验配置。
// 参数/返回：targetInfo 为 sites 表记录；返回解析后的配置。
// 副作用：无。
func ResolveSiteSettings(targetInfo map[string]any) SiteSettings {
	settings := SiteSettings{}
	if targetInfo == nil {
		return settings
	}
	settings.Enabled = toBool(targetInfo["dupe_check_enabled"])
	tolerance := toInt64(targetInfo["dupe_size_tolerance_bytes"])
	if tolerance <= 0 {
		tolerance = DefaultSizeToleranceBytes
	}
	settings.SizeToleranceBytes = tolerance
	return settings
}

// SiteSupportsDupe 判断站点 YAML 是否声明了 dupe 校验能力。
// 参数/返回：siteCode 为站点标识；返回是否支持。
// 说明：这是「站点是否实现了 dupe 校验」的判据，与站点设置里的开关相互独立——
// 开关是用户意愿，本项是站点能力。未声明能力的站点即使开了开关也不会执行校验。
// 副作用：无。
func SiteSupportsDupe(siteCode string) bool {
	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil {
		return false
	}
	return siteCfg.DupeCheck.Enabled
}

// BuildSearchFilters 依据站点 dupe 配置，把最终表单字段换算为搜索页筛选参数。
// 参数/返回：siteCode 为站点标识；formFields 为已完成映射与站点修正的最终表单字段。
// 返回搜索参数（键为参数名、值为参数值）；站点未声明 dupe 配置时返回 nil。
// 副作用：无。
//
// 说明：维度取值直接取自 formFields —— 也就是 ResolveBasicPublishMappings + AdjustFormFields
// 之后的站点取值，避免在 dupe 侧重复维护一份映射。
func BuildSearchFilters(siteCode string, formFields map[string]string) map[string]string {
	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil || !siteCfg.DupeCheck.Enabled {
		return nil
	}
	templates := siteCfg.DupeCheck.ParamTemplates
	if len(templates) == 0 {
		return nil
	}

	// 维度 → 表单字段名。取自 siteCfg.FormFields，缺失时回退到 NexusPHP 常见字段名。
	fieldForDimension := func(dimension string, fallback string) string {
		if siteCfg != nil {
			if resolved := strings.TrimSpace(siteCfg.FormFields[dimension]); resolved != "" {
				return resolved
			}
		}
		return fallback
	}

	filters := map[string]string{}
	addFilter := func(dimension string, fallbackField string) {
		template := strings.TrimSpace(templates[dimension])
		if template == "" {
			return
		}
		fieldName := fieldForDimension(dimension, fallbackField)
		if fieldName == "" {
			return
		}
		value := strings.TrimSpace(formFields[fieldName])
		if value == "" {
			return
		}
		// ⚠️ 跳过尚未解析的 select 索引占位符（如 hdhome 的 "@index:1"）。
		// 这类值要到「抓取上传页」阶段才会由 uploader 换成真实选项值，而 dupe 校验发生在
		// 上传页抓取之前，此刻拿到的是占位符。若把它当成维度值拼进检索参数，
		// 会筛到错误类别（甚至 0 结果）→ 把「查不到」误判成「不重复」而漏检。
		// 宁可不筛（候选多一些无妨，最终判定靠制作组 + 体积容差），也不能筛错。
		if strings.HasPrefix(value, "@index:") {
			return
		}
		name := strings.TrimSpace(template)
		if name == "" {
			return
		}
		// 两种参数形态：
		//  - 名字里带取值占位符（如 medium{value} → medium12=1）：取值嵌进参数名，参数值固定为 1；
		//  - 名字不带占位符（如 codec[] → codec[]=6）：参数名固定，取值作为参数值。
		if strings.Contains(name, "{value}") {
			filters[strings.ReplaceAll(name, "{value}", value)] = "1"
			return
		}
		filters[name] = value
	}

	addFilter("type", "type")
	addFilter("medium", "medium")
	addFilter("resolution", "standard")
	addFilter("video_codec", "codec")
	addFilter("audio_codec", "audiocodec")

	return filters
}

// TorrentTotalSizeFromPayload 尽力从发布 payload 中取待发布种子的总体积。
// 参数/返回：uploadData 为发布参数；返回体积（字节），取不到时返回 0。
// 副作用：无。
//
// 注意：站点展示的体积是种子内全部文件的总大小，因此这里必须取「总体积」而不是
// 「视频文件体积」——后者对多文件合集、附赠花絮等场景会明显偏小，导致容差判定失真。
func TorrentTotalSizeFromPayload(uploadData map[string]any) int64 {
	if uploadData == nil {
		return 0
	}
	for _, key := range []string{"total_size", "torrent_size", "size"} {
		if value := toInt64(uploadData[key]); value > 0 {
			return value
		}
	}
	if standardized, ok := uploadData["standardized_params"].(map[string]any); ok && standardized != nil {
		for _, key := range []string{"total_size", "torrent_size", "size"} {
			if value := toInt64(standardized[key]); value > 0 {
				return value
			}
		}
	}
	return 0
}

// SortedFilterKeys 返回筛选参数的排序键，便于日志稳定输出。
func SortedFilterKeys(filters map[string]string) []string {
	keys := make([]string, 0, len(filters))
	for key := range filters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// toBool 把任意来源的「开关值」解释为布尔。
//
// ⚠️ 必须覆盖全部整型宽度：MySQL 的 TINYINT(1)（如 dupe_check_enabled）经驱动与 GORM
// 扫描后会以 int8 返回，SQLite/PostgreSQL 则可能给 int64/int32。
// 早期版本只处理 int/int64/float64/string，导致开了开关也被读成 false、dupe 校验整体失效。
func toBool(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	case string:
		return isTruthyText(typed)
	case []byte:
		return isTruthyText(string(typed))
	default:
		return false
	}
}

func isTruthyText(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// toInt64 把任意来源的数值解释为 int64，覆盖全部整型宽度与字节串（口径同 toBool）。
func toInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case bool:
		if typed {
			return 1
		}
		return 0
	case string:
		return parseIntText(typed)
	case []byte:
		return parseIntText(string(typed))
	default:
		return 0
	}
}

func parseIntText(raw string) int64 {
	parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// DupeSearchAreas 返回站点配置的检索范围表（键：douban / imdb / title）。
// 参数/返回：siteCode 为站点标识；返回范围取值表（可能为空）。
// 说明：兼容旧的单一 SearchAreaValue——旧配置只有一个范围时按「IMDb」处理，
// 因为人人站与幸运站的历史配置都是 IMDb / 豆瓣共用一个范围值。
// 副作用：无。
func DupeSearchAreas(siteCode string) map[string]string {
	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil || !siteCfg.DupeCheck.Enabled {
		return nil
	}
	if len(siteCfg.DupeCheck.SearchAreas) > 0 {
		return siteCfg.DupeCheck.SearchAreas
	}
	if legacy := strings.TrimSpace(siteCfg.DupeCheck.SearchAreaValue); legacy != "" {
		return map[string]string{"imdb": legacy, "douban": legacy}
	}
	return nil
}

// buildNexusPHPSearchPlans 按站点配置组装 NexusPHP 站点检索计划。
//
// 顺序与理由：
//  1. 豆瓣 ID 检索（站点声明了 douban 范围且有豆瓣 ID 时）——精确度最高；
//  2. IMDb 检索（站点声明了 imdb 范围且有 IMDb ID 时）；
//  3. 标题检索兜底（站点声明了 title 范围时）——上面的 ID 都缺失或都未命中时使用。
//
// 之所以「未命中就继续下一段」而不是只查一段：各站对 ID 的收录范围不同，
// 多查一段只是多一次请求，漏查则可能把重复种子放行。
//
// 参数/返回：siteCode 为站点标识；query 为检索输入；返回检索计划（可能为空，表示无可检索关键字）。
// 副作用：加载站点配置（有缓存）。
func buildNexusPHPSearchPlans(siteCode string, query Query) []searchPlan {
	areas := DupeSearchAreas(siteCode)
	plans := make([]searchPlan, 0, 3)

	if area := strings.TrimSpace(areas["douban"]); area != "" {
		if id := ExtractDoubanID(query.DoubanID); id != "" {
			plans = append(plans, searchPlan{
				Label:      "豆瓣 ID 检索",
				Search:     id,
				SearchArea: area,
				Filters:    query.Filters,
			})
		}
	}
	if area := strings.TrimSpace(areas["imdb"]); area != "" {
		if id := ExtractIMDbID(query.IMDbID); id != "" {
			plans = append(plans, searchPlan{
				Label:      "IMDb ID 检索",
				Search:     id,
				SearchArea: area,
				Filters:    query.Filters,
			})
		}
	}
	if area := strings.TrimSpace(areas["title"]); area != "" {
		if keyword := DupeTitleSearchKeyword(query.Title); keyword != "" {
			plans = append(plans, searchPlan{
				Label:      "标题检索兜底",
				Search:     keyword,
				SearchArea: area,
				Filters:    query.Filters,
			})
		}
	}
	return plans
}
