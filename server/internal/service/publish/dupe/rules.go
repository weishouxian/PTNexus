package dupe

import (
	"encoding/json"
	"strings"
)

// dupe 判定维度常量。
//
// 这些取值会写进 sites.dupe_rules 的 JSON，前后端共用，改名必须同步前端
// （webui/src/components/settings/SitesSettings.vue 的 DUPE_DIMENSIONS）。
//
// 实现方式分两类：
//   - 客户端比对（size / team）：拿候选条目的体积、标题与待发布种子做比对；
//   - 站点筛选（medium / resolution / video_codec / audio_codec）：换算成站点搜索参数，
//     由站点用自己的元数据筛掉不一致的条目。
//
// 之所以把筛选维度也算作「判定维度」：站点检索结果行只有标题与体积，
// 拿不到媒介/分辨率/编码，因此这些维度只能靠站点筛选实现（用户已确认此口径）。
const (
	// DimensionSize 文件大小：候选体积与待发布体积之差需落在站点容差之内。
	DimensionSize = "size"
	// DimensionTeam 制作组：两侧标题尾部的制作组需归一化为同一个 team.* 键。
	DimensionTeam = "team"
	// DimensionMedium 媒介：作为站点检索筛选条件。
	DimensionMedium = "medium"
	// DimensionResolution 分辨率：作为站点检索筛选条件。
	DimensionResolution = "resolution"
	// DimensionVideoCodec 视频编码：作为站点检索筛选条件。
	DimensionVideoCodec = "video_codec"
	// DimensionAudioCodec 音频编码：作为站点检索筛选条件。
	DimensionAudioCodec = "audio_codec"
)

// DupeFallbackMedium 是「兜底规则」在规则表里的保留键。
//
// 站点设置里可单独开启一条兜底规则：凡是**没有单独配置规则**的媒介都走它
// （包括媒介取不到 / 取到站点映射里没有的键这些情况）。
// 关闭兜底时该键不存在，未配置的媒介即不执行校验。
//
// 用保留键而不是再加一个字段，是为了保持「sites.dupe_rules 是该站点规则的唯一来源」。
const DupeFallbackMedium = "*"

// dupeDimensionOrder 为维度的规范顺序：日志与界面展示均按它排列，保证输出稳定可比对。
var dupeDimensionOrder = []string{
	DimensionSize,
	DimensionTeam,
	DimensionMedium,
	DimensionResolution,
	DimensionVideoCodec,
	DimensionAudioCodec,
}

// dupeDimensionLabels 为维度中文名，用于发布日志与选项接口。
var dupeDimensionLabels = map[string]string{
	DimensionSize:       "文件大小",
	DimensionTeam:       "制作组",
	DimensionMedium:     "媒介",
	DimensionResolution: "分辨率",
	DimensionVideoCodec: "视频编码",
	DimensionAudioCodec: "音频编码",
}

// dupeFilterDimensions 为「通过站点检索筛选实现」的维度集合，其余维度由客户端比对。
var dupeFilterDimensions = map[string]struct{}{
	DimensionMedium:     {},
	DimensionResolution: {},
	DimensionVideoCodec: {},
	DimensionAudioCodec: {},
}

// DupeDimensionLabel 返回维度的中文名（未知维度原样返回）。
// 参数/返回：dimension 为维度常量；返回中文名。
// 副作用：无。
func DupeDimensionLabel(dimension string) string {
	key := strings.TrimSpace(dimension)
	if label, ok := dupeDimensionLabels[key]; ok {
		return label
	}
	return key
}

// IsFilterDimension 判断维度是否通过站点检索筛选实现。
// 参数/返回：dimension 为维度常量；返回是否属于筛选维度。
// 副作用：无。
func IsFilterDimension(dimension string) bool {
	_, ok := dupeFilterDimensions[strings.TrimSpace(dimension)]
	return ok
}

// NormalizeDupeDimensions 归一化维度集合：剔除未知项与重复项，并按规范顺序排列。
// 参数/返回：raw 为原始维度集合；返回归一化结果（可能为空）。
// 副作用：无。
func NormalizeDupeDimensions(raw []string) []string {
	set := map[string]struct{}{}
	for _, item := range raw {
		key := strings.TrimSpace(item)
		if _, known := dupeDimensionLabels[key]; !known {
			continue
		}
		set[key] = struct{}{}
	}
	if len(set) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(set))
	for _, dimension := range dupeDimensionOrder {
		if _, ok := set[dimension]; ok {
			normalized = append(normalized, dimension)
		}
	}
	return normalized
}

// DupeDimensionLabels 把维度集合翻译成中文名列表（按规范顺序）。
// 参数/返回：dims 为维度集合；返回中文名列表，用于拼接日志。
// 副作用：无。
func DupeDimensionLabels(dims []string) []string {
	normalized := NormalizeDupeDimensions(dims)
	labels := make([]string, 0, len(normalized))
	for _, dimension := range normalized {
		labels = append(labels, DupeDimensionLabel(dimension))
	}
	return labels
}

// FilterDimensions 从维度集合中取出所有「站点筛选」维度。
// 参数/返回：dims 为维度集合；返回筛选维度子集（按规范顺序）。
// 副作用：无。
func FilterDimensions(dims []string) []string {
	filtered := make([]string, 0, len(dims))
	for _, dimension := range NormalizeDupeDimensions(dims) {
		if IsFilterDimension(dimension) {
			filtered = append(filtered, dimension)
		}
	}
	return filtered
}

// ClientDimensions 返回客户端比对维度。
// 参数/返回：dims 为维度集合；返回是否需要比对体积、是否需要比对制作组。
// 副作用：无。
func ClientDimensions(dims []string) (matchSize bool, matchTeam bool) {
	for _, dimension := range NormalizeDupeDimensions(dims) {
		switch dimension {
		case DimensionSize:
			matchSize = true
		case DimensionTeam:
			matchTeam = true
		}
	}
	return matchSize, matchTeam
}

// SiteSettings.MatchRule 取指定媒介**实际生效**的判定维度。
// 参数/返回：medium 为标准媒介键；返回维度集合、命中的规则键与是否命中。
//
// 匹配顺序：先精确命中该媒介；没有再退化到兜底规则（DupeFallbackMedium）。
// 命中兜底时会把规则键返回为 DupeFallbackMedium，调用方据此在日志里说明「走的是兜底」。
//
// ⚠️ 媒介规则（非兜底）**恒含「媒介」维度**：规则本身就是按媒介区分的，
// 因此检索范围必须锁在这个媒介上。这里统一补一次（`ensureMediumDimension`），
// 使历史数据或手工改过的配置也照样生效；界面上该维度是「已勾选且不可取消」。
//
// ⚠️ 未开启兜底、且该媒介没有单独配置时返回 ok=false，调用方必须跳过校验而不是放行。
// 副作用：无。
func (s SiteSettings) MatchRule(medium string) (dims []string, ruleKey string, ok bool) {
	if key := strings.TrimSpace(medium); key != "" {
		if stored, hit := s.Rules[key]; hit {
			return ensureMediumDimension(stored), key, true
		}
	}
	if fallback, hit := s.FallbackRule(); hit {
		return fallback, DupeFallbackMedium, true
	}
	return nil, "", false
}

// ensureMediumDimension 保证维度集合包含「媒介」（去重并按规范顺序归一化）。
// 参数/返回：dims 为原始维度集合；返回补上「媒介」后的结果。
// 副作用：无。
func ensureMediumDimension(dims []string) []string {
	merged := make([]string, 0, len(dims)+1)
	merged = append(merged, dims...)
	merged = append(merged, DimensionMedium)
	return NormalizeDupeDimensions(merged)
}

// SiteSettings.FallbackRule 返回兜底规则（未开启时 ok=false）。
// 参数/返回：返回兜底维度集合与是否开启。
// 副作用：无。
func (s SiteSettings) FallbackRule() ([]string, bool) {
	if len(s.Rules) == 0 {
		return nil, false
	}
	dims, ok := s.Rules[DupeFallbackMedium]
	if !ok || len(dims) == 0 {
		return nil, false
	}
	return dims, true
}

// ParseDupeRules 解析 sites.dupe_rules 字段为「媒介 → 判定维度」规则表。
//
// 参数/返回：raw 为字段原始值（兼容 JSON 字符串 / []byte / map 三种形态，因不同驱动扫描结果不同）；
//
//	返回规则表；解析不出有效规则时返回 nil。
//
// 说明：维度会经 NormalizeDupeDimensions 归一化 —— 未知维度被剔除；**维度为空的规则整条丢弃**，
//
//	因为「一个维度都不比」没有可执行的语义，保留它只会让「配了规则」与「没配规则」两种状态混淆。
//
// 副作用：无。
func ParseDupeRules(raw any) map[string][]string {
	switch typed := raw.(type) {
	case nil:
		return nil
	case string:
		return parseDupeRulesJSON(typed)
	case []byte:
		return parseDupeRulesJSON(string(typed))
	case map[string][]string:
		return normalizeDupeRulesMap(toAnyValueMap(typed))
	case map[string]any:
		return normalizeDupeRulesMap(typed)
	default:
		return nil
	}
}

// parseDupeRulesJSON 解析 JSON 文本形态的规则表。
func parseDupeRulesJSON(text string) map[string][]string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil
	}
	return normalizeDupeRulesMap(decoded)
}

// normalizeDupeRulesMap 归一化 map 形态的规则表（值为数组或逗号分隔字符串）。
func normalizeDupeRulesMap(raw map[string]any) map[string][]string {
	rules := make(map[string][]string, len(raw))
	for medium, value := range raw {
		key := strings.TrimSpace(medium)
		if key == "" {
			continue
		}
		dims := NormalizeDupeDimensions(toStringSlice(value))
		if len(dims) == 0 {
			continue
		}
		rules[key] = dims
	}
	if len(rules) == 0 {
		return nil
	}
	return rules
}

// toStringSlice 把任意形态的维度值转成字符串切片。
// 支持 []any（JSON 数组）、[]string、逗号分隔字符串。
func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case string:
		parts := strings.Split(typed, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

// toAnyValueMap 把 map[string][]string 转成 map[string]any，复用同一套归一化逻辑。
func toAnyValueMap(raw map[string][]string) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

// SeedMediumFromPayload 从发布 payload 中读取待发布种子的标准媒介键。
// 参数/返回：uploadData 为发布参数；返回标准媒介键（如 medium.remux），取不到时返回空串。
// 说明：标准值由前端放在 standardized_params 中随 upload_data 一并提交，
//
//	与发布映射（ResolveBasicPublishMappings）取的是同一份数据，故两侧口径天然一致。
//
// 副作用：无。
func SeedMediumFromPayload(uploadData map[string]any) string {
	if uploadData == nil {
		return ""
	}
	standardized, ok := uploadData["standardized_params"].(map[string]any)
	if !ok || standardized == nil {
		// 兼容前端偶发传字符串 JSON 的情况。
		if text, isText := uploadData["standardized_params"].(string); isText {
			decoded := map[string]any{}
			if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err == nil {
				standardized = decoded
			}
		}
	}
	if standardized == nil {
		return ""
	}
	return strings.TrimSpace(toStringValue(standardized["medium"]))
}

// toStringValue 把任意标量转成字符串（仅用于读取 standardized_params 里的值）。
func toStringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}
