package mapping

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ResolveBasicPublishMappings 根据标准化参数生成目标站的基础表单字段映射。
func ResolveBasicPublishMappings(siteCode string, uploadData map[string]any) map[string]string {
	mapped := map[string]string{}
	standardized, _ := uploadData["standardized_params"].(map[string]any)
	if standardized == nil {
		standardized = map[string]any{}
	}

	siteCfg, _ := LoadSitePublishConfig(siteCode)
	apply := func(mappingKey string, formKey string, value string, fallbackField string, requireConfiguredField bool) {
		if strings.TrimSpace(value) == "" {
			return
		}
		fieldName := fallbackField
		mappedValue := value
		if siteCfg != nil {
			if resolvedField := strings.TrimSpace(siteCfg.FormFields[formKey]); resolvedField != "" {
				fieldName = resolvedField
			} else if requireConfiguredField {
				return
			}
			mappedValue = strings.TrimSpace(PickMappedValueWithFallback(mappingKey, siteCfg.Mappings[mappingKey], value))
		} else if requireConfiguredField {
			return
		}
		if fieldName == "" || mappedValue == "" {
			return
		}
		mapped[fieldName] = mappedValue
	}

	apply("type", "category", strings.TrimSpace(toStringAnyBasic(standardized["type"], "")), "type", false)
	applyTypeByTag(siteCfg, mapped, uploadData, standardized)
	apply("medium", "medium", strings.TrimSpace(toStringAnyBasic(standardized["medium"], "")), "medium", false)
	apply("video_codec", "video_codec", strings.TrimSpace(toStringAnyBasic(standardized["video_codec"], "")), "codec", false)
	apply("audio_codec", "audio_codec", strings.TrimSpace(toStringAnyBasic(standardized["audio_codec"], "")), "audiocodec", false)
	apply("resolution", "resolution", strings.TrimSpace(toStringAnyBasic(standardized["resolution"], "")), "standard", false)
	applySourceOrProcessing(siteCfg, mapped, strings.TrimSpace(toStringAnyBasic(standardized["source"], "")))
	apply("team", "team", strings.TrimSpace(toStringAnyBasic(standardized["team"], "")), "team", false)
	apply("region", "region", strings.TrimSpace(toStringAnyBasic(standardized["source"], "")), "region_sel", true)

	applyTags("tag", siteCfg, mapped, uploadData, standardized)
	applyCheckboxTags(siteCfg, mapped, uploadData, standardized)
	applyStaticFields(siteCfg, mapped)
	return mapped
}

// applyTypeByTag 按"标签决定分类"规则覆盖站点的分类字段。
// 参数/返回：siteCfg 提供 type_by_tag 表（标准标签 → 标准类型值）；mapped 为待提交字段；uploadData/standardized 提供标签来源；无返回。
// 说明：部分站点硬性要求带特定标签的种子必须归入对应分类（如 AGSV 带"动画"标签时必须选"动漫"），
// 该规则优先于按来源文本解析出的类型；命中后直接覆盖分类字段。标准类型值经站点 mappings.type 换算为站点取值（不走 default 兜底，避免误覆盖）。
// 标签匹配同时兼容带/不带 tag. 前缀两种写法，且忽略大小写；匹配基于种子自身标签，不做 tag_requires 联动扩展。
// 失败场景：站点未配 type_by_tag、标签集合为空、标准类型在站点 mappings.type 中无对应取值时跳过。
// 副作用：直接改写 mapped 中的分类字段。
func applyTypeByTag(siteCfg *SitePublishConfig, mapped map[string]string, uploadData map[string]any, standardized map[string]any) {
	if mapped == nil || siteCfg == nil || len(siteCfg.TypeByTag) == 0 {
		return
	}
	allTags := collectAllTags(uploadData, standardized)
	if len(allTags) == 0 {
		return
	}

	field := "type"
	if resolved := strings.TrimSpace(siteCfg.FormFields["category"]); resolved != "" {
		field = resolved
	}
	typeMapping := siteCfg.Mappings["type"]

	for _, tag := range allTags {
		standardType := ""
		for _, candidate := range tagKeyCandidates(tag) {
			if value, ok := pickMappingValueIgnoreCase(siteCfg.TypeByTag, candidate); ok {
				standardType = value
				break
			}
		}
		if standardType == "" {
			continue
		}
		if siteValue, ok := pickMappingValueIgnoreCase(typeMapping, standardType); ok {
			mapped[field] = siteValue
			return
		}
	}
}

// tagKeyCandidates 返回标签在映射表中的候选键，兼容带/不带 tag. 前缀两种写法。
// 参数/返回：tag 为标签文本；返回候选键列表，空标签返回 nil。
// 失败场景：无。
// 副作用：无。
func tagKeyCandidates(tag string) []string {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return nil
	}
	if strings.HasPrefix(trimmed, "tag.") {
		return []string{trimmed, strings.TrimPrefix(trimmed, "tag.")}
	}
	return []string{trimmed, "tag." + trimmed}
}

func applySourceOrProcessing(siteCfg *SitePublishConfig, mapped map[string]string, sourceValue string) {
	trimmed := strings.TrimSpace(sourceValue)
	if trimmed == "" || mapped == nil {
		return
	}

	if siteCfg == nil {
		mapped["source"] = trimmed
		return
	}

	if fieldName, mappedValue := resolveMappedField(siteCfg, "source", "source", trimmed, "source", false); fieldName != "" && mappedValue != "" {
		mapped[fieldName] = mappedValue
		return
	}

	if fieldName, mappedValue := resolveMappedField(siteCfg, "processing", "processing", trimmed, "processing", true); fieldName != "" && mappedValue != "" {
		mapped[fieldName] = mappedValue
	}
}

func resolveMappedField(siteCfg *SitePublishConfig, mappingKey string, formKey string, value string, fallbackField string, requireConfiguredField bool) (string, string) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", ""
	}

	fieldName := fallbackField
	mappedValue := trimmedValue
	if siteCfg != nil {
		if resolvedField := strings.TrimSpace(siteCfg.FormFields[formKey]); resolvedField != "" {
			fieldName = resolvedField
		} else if requireConfiguredField {
			return "", ""
		}
		mappedValue = strings.TrimSpace(PickMappedValueWithFallback(mappingKey, siteCfg.Mappings[mappingKey], trimmedValue))
	} else if requireConfiguredField {
		return "", ""
	}

	if strings.TrimSpace(fieldName) == "" || strings.TrimSpace(mappedValue) == "" {
		return "", ""
	}
	return strings.TrimSpace(fieldName), strings.TrimSpace(mappedValue)
}

func applyTags(mappingKey string, siteCfg *SitePublishConfig, mapped map[string]string, uploadData map[string]any, standardized map[string]any) {
	if mapped == nil || siteCfg == nil {
		return
	}
	tagMapping := siteCfg.Mappings[mappingKey]
	if len(tagMapping) == 0 {
		return
	}

	allTags := collectAllTags(uploadData, standardized)
	if len(allTags) == 0 {
		return
	}
	allTags = expandRequiredTags(allTags, siteCfg.TagRequires)

	tagIDs := make([]string, 0, len(allTags))
	seen := map[string]struct{}{}
	for _, tag := range allTags {
		candidates := tagKeyCandidates(tag)
		mappedID := ""
		for _, candidate := range candidates {
			if mappedValue := pickMappedValueWithFallback(mappingKey, tagMapping, candidate, false, false); strings.TrimSpace(mappedValue) != "" {
				mappedID = strings.TrimSpace(mappedValue)
				break
			}
		}
		if mappedID == "" {
			if fallback, ok := tagMapping["default"]; ok {
				mappedID = strings.TrimSpace(fallback)
			}
		}
		if mappedID == "" {
			continue
		}
		if _, exists := seen[mappedID]; exists {
			continue
		}
		seen[mappedID] = struct{}{}
		tagIDs = append(tagIDs, mappedID)
	}
	if len(tagIDs) == 0 {
		return
	}
	sort.Strings(tagIDs)

	base := resolveTagFieldBase(siteCfg)
	if base == "" {
		base = "tags[4]"
	}
	for idx, id := range tagIDs {
		mapped[fmt.Sprintf("%s[%d]", base, idx)] = id
	}
}

// applyCheckboxTags 把标准化标签映射成站点上"独立命名"的 checkbox 字段。
// 参数/返回：siteCfg 提供 checkbox_tags 表；mapped 为待提交字段；uploadData/standardized 提供标签来源；无返回。
// 说明：部分老 NexusPHP 站点（如 CHDBits）每个标签是一个独立 checkbox（name=first/oneself/... 且 value=yes），
// 而非其它站点的 tags[] 数组，applyTags 生成的 tags[N]=id 对这种站点无效，故单独处理。
// 失败场景：站点未配 checkbox_tags、标签集合为空时直接返回。
// 副作用：直接改写 mapped（命中即写入 <字段名>=<checkbox_tag_value>，重复命中同一字段时幂等）。
func applyCheckboxTags(siteCfg *SitePublishConfig, mapped map[string]string, uploadData map[string]any, standardized map[string]any) {
	if mapped == nil || siteCfg == nil || len(siteCfg.CheckboxTags) == 0 {
		return
	}

	allTags := collectAllTags(uploadData, standardized)
	if len(allTags) == 0 {
		return
	}
	allTags = expandRequiredTags(allTags, siteCfg.TagRequires)

	value := resolveCheckboxTagValue(siteCfg)
	for _, tag := range allTags {
		for _, candidate := range tagKeyCandidates(tag) {
			field, ok := pickMappingValueIgnoreCase(siteCfg.CheckboxTags, candidate)
			if !ok {
				continue
			}
			mapped[field] = value
			break
		}
	}
}

// applyStaticFields 写入站点固定字段（无语义映射来源的字段默认值）。
// 参数/返回：siteCfg 提供 static_fields 表；mapped 为待提交字段；无返回。
// 失败场景：站点未配 static_fields 时直接返回。
// 副作用：直接改写 mapped；已被其它映射命中的字段不会被覆盖。
func applyStaticFields(siteCfg *SitePublishConfig, mapped map[string]string) {
	if mapped == nil || siteCfg == nil || len(siteCfg.StaticFields) == 0 {
		return
	}
	for key, value := range siteCfg.StaticFields {
		field := strings.TrimSpace(key)
		if field == "" {
			continue
		}
		if _, exists := mapped[field]; exists {
			continue
		}
		mapped[field] = strings.TrimSpace(value)
	}
}

// resolveCheckboxTagValue 返回独立 checkbox 标签命中时提交的值。
// 参数/返回：siteCfg 提供 checkbox_tag_value；返回提交值，未配置时返回 NexusPHP 默认值 yes。
// 失败场景：无。
// 副作用：无。
func resolveCheckboxTagValue(siteCfg *SitePublishConfig) string {
	if siteCfg != nil {
		if value := strings.TrimSpace(siteCfg.CheckboxTagValue); value != "" {
			return value
		}
	}
	return "yes"
}

// expandRequiredTags 按站点配置的标签联动规则补齐必须同时勾选的标签。
// 参数/返回：tags 为待映射的标签集合；requires 为“命中某个标签时必须同时包含的标签”规则表；返回补齐后的标签集合。
// 失败场景：规则为空或标签集合为空时原样返回；规则出现环时按规则条数限制展开轮次，避免死循环。
// 副作用：无。
func expandRequiredTags(tags []string, requires map[string][]string) []string {
	if len(tags) == 0 || len(requires) == 0 {
		return tags
	}
	result := append([]string{}, tags...)
	for round := 0; round <= len(requires); round++ {
		added := false
		for _, tag := range result {
			normalized := normalizeTagKeyForRequire(tag)
			if normalized == "" {
				continue
			}
			for requireKey, requiredTags := range requires {
				if normalizeTagKeyForRequire(requireKey) != normalized {
					continue
				}
				for _, required := range requiredTags {
					target := strings.TrimSpace(required)
					if target == "" || containsTagFold(result, target) {
						continue
					}
					result = append(result, target)
					added = true
				}
			}
		}
		if !added {
			break
		}
	}
	return result
}

func normalizeTagKeyForRequire(tag string) string {
	trimmed := strings.ToLower(strings.TrimSpace(tag))
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "tag."))
	return trimmed
}

func containsTagFold(tags []string, target string) bool {
	normalizedTarget := normalizeTagKeyForRequire(target)
	for _, tag := range tags {
		if normalizeTagKeyForRequire(tag) == normalizedTarget {
			return true
		}
	}
	return false
}

func resolveTagFieldBase(siteCfg *SitePublishConfig) string {
	if siteCfg == nil {
		return ""
	}
	if raw := strings.TrimSpace(siteCfg.FormFields["tags[]"]); raw != "" {
		return strings.TrimSuffix(raw, "[]")
	}
	if raw := strings.TrimSpace(siteCfg.FormFields["tag_list[]"]); raw != "" {
		return strings.TrimSuffix(raw, "[]")
	}
	// 一些站点的配置使用 tags[4][] 作为 key，本质上就是默认字段名。
	if _, exists := siteCfg.FormFields["tags[4][]"]; exists {
		return strings.TrimSuffix("tags[4][]", "[]")
	}
	return ""
}

func collectAllTags(uploadData map[string]any, standardized map[string]any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 16)

	appendTags := func(value any) {
		for _, tag := range parseStringSlice(value) {
			if tag == "" {
				continue
			}
			if _, exists := seen[tag]; exists {
				continue
			}
			seen[tag] = struct{}{}
			result = append(result, tag)
		}
	}

	appendTags(standardized["tags"])
	if uploadData != nil {
		appendTags(uploadData["tags"])
		if sourceParams, ok := uploadData["source_params"].(map[string]any); ok && sourceParams != nil {
			appendTags(sourceParams["标签"])
		}
	}
	return result
}

func parseStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return []string{}
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(toStringAnyBasic(item, ""))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return []string{}
		}
		if strings.HasPrefix(text, "[") {
			parsed := []string{}
			if err := json.Unmarshal([]byte(text), &parsed); err == nil {
				return parseStringSlice(parsed)
			}
			parsedAny := []any{}
			if err := json.Unmarshal([]byte(text), &parsedAny); err == nil {
				return parseStringSlice(parsedAny)
			}
		}
		if strings.Contains(text, ",") {
			parts := strings.Split(text, ",")
			out := make([]string, 0, len(parts))
			for _, part := range parts {
				trimmed := strings.TrimSpace(part)
				if trimmed != "" {
					out = append(out, trimmed)
				}
			}
			return out
		}
		return []string{text}
	default:
		return []string{strings.TrimSpace(toStringAnyBasic(typed, ""))}
	}
}

func toStringAnyBasic(value any, fallback string) string {
	switch typed := value.(type) {
	case nil:
		return fallback
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
		return fallback
	}
}
