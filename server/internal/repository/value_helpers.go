package repository

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func toString(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func toInt64(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int64:
		return typed, nil
	case int32:
		return int64(typed), nil
	case int16:
		return int64(typed), nil
	case int8:
		return int64(typed), nil
	case uint:
		return int64(typed), nil
	case uint64:
		return int64(typed), nil
	case uint32:
		return int64(typed), nil
	case uint16:
		return int64(typed), nil
	case uint8:
		return int64(typed), nil
	case float64:
		return int64(typed), nil
	case float32:
		return int64(typed), nil
	case bool:
		if typed {
			return 1, nil
		}
		return 0, nil
	case string:
		text := strings.TrimSpace(typed)
		if text == "" {
			return 0, fmt.Errorf("empty string")
		}
		return strconv.ParseInt(text, 10, 64)
	case []byte:
		text := strings.TrimSpace(string(typed))
		if text == "" {
			return 0, fmt.Errorf("empty bytes")
		}
		return strconv.ParseInt(text, 10, 64)
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func toIntWithDefault(value any, fallback int) int {
	parsed, err := toInt64(value)
	if err != nil {
		return fallback
	}
	return int(parsed)
}

// DefaultDupeSizeToleranceBytes 是 dupe 校验的默认体积容差（字节）。
// 对应站点管理页展示的「体积容差 1024 MB」（1 MB = 1024² 字节，即 1 GiB）。
// 站点未显式配置 dupe_size_tolerance_bytes 时使用该值；0 为合法配置（要求体积完全一致）。
const DefaultDupeSizeToleranceBytes int64 = 1073741824

// siteDupeRulesFromAny 把 sites.dupe_rules 的原始值解析成「媒介 → 判定维度」表。
// 参数/返回：value 为数据库返回值（不同驱动可能是 string / []byte / 已是 map）；返回规则表。
// 说明：这里只负责 JSON 形状的解析（去掉空条目），维度白名单校验在 dupe 服务侧做
// （repository 不依赖发布服务，避免分层倒置）。解析失败时返回空表而不是报错 ——
// 一条脏数据不该让整个站点列表接口失败。
// 副作用：无。
func siteDupeRulesFromAny(value any) map[string][]string {
	switch typed := value.(type) {
	case nil:
		return map[string][]string{}
	case map[string][]string:
		return normalizeRuleShape(toAnyRuleMap(typed))
	case map[string]any:
		return normalizeRuleShape(typed)
	case []byte:
		return parseSiteDupeRulesJSON(string(typed))
	case string:
		return parseSiteDupeRulesJSON(typed)
	default:
		return map[string][]string{}
	}
}

func parseSiteDupeRulesJSON(text string) map[string][]string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || trimmed == "null" {
		return map[string][]string{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return map[string][]string{}
	}
	return normalizeRuleShape(decoded)
}

// normalizeRuleShape 把规则表的每个值统一成去空的字符串切片，并丢弃空条目。
func normalizeRuleShape(raw map[string]any) map[string][]string {
	rules := make(map[string][]string, len(raw))
	for key, value := range raw {
		medium := strings.TrimSpace(key)
		if medium == "" {
			continue
		}
		dims := make([]string, 0, 4)
		switch typed := value.(type) {
		case []string:
			dims = append(dims, typed...)
		case []any:
			for _, item := range typed {
				if text, ok := item.(string); ok {
					dims = append(dims, text)
				}
			}
		case string:
			dims = append(dims, strings.Split(typed, ",")...)
		}
		cleaned := make([]string, 0, len(dims))
		for _, dim := range dims {
			if trimmed := strings.TrimSpace(dim); trimmed != "" {
				cleaned = append(cleaned, trimmed)
			}
		}
		if len(cleaned) == 0 {
			continue
		}
		rules[medium] = cleaned
	}
	return rules
}

func toAnyRuleMap(raw map[string][]string) map[string]any {
	out := make(map[string]any, len(raw))
	for key, value := range raw {
		out[key] = value
	}
	return out
}

// encodeSiteDupeRules 把前端提交的规则表编码为写库用的 JSON 文本。
// 参数/返回：value 为请求体中的 dupe_rules；返回 JSON 文本（无有效规则时返回空串）。
// 说明：空串表示「未配置规则」，与「配置了但为空表」等价，读回时同样解析为空表。
// 副作用：无。
func encodeSiteDupeRules(value any) string {
	rules := siteDupeRulesFromAny(value)
	if len(rules) == 0 {
		return ""
	}
	encoded, err := json.Marshal(rules)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func toInt64WithDefault(value any, fallback int64) int64 {
	parsed, err := toInt64(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func toFloat64WithDefault(value any, fallback float64) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		if typed == "" {
			return fallback
		}
		parsed, err := strconv.ParseFloat(typed, 64)
		if err != nil {
			return fallback
		}
		return parsed
	default:
		return fallback
	}
}

func siteStringListFromAny(value any) []string {
	result := make([]string, 0)
	appendValue := func(item any) {
		text := strings.TrimSpace(toString(item, ""))
		if text != "" {
			result = append(result, text)
		}
	}
	switch typed := value.(type) {
	case nil:
		return []string{}
	case []string:
		for _, item := range typed {
			appendValue(item)
		}
	case []any:
		for _, item := range typed {
			appendValue(item)
		}
	case []byte:
		return siteStringListFromAny(string(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []string{}
		}
		parsedString := []string{}
		if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &parsedString) == nil {
			for _, item := range parsedString {
				appendValue(item)
			}
			return dedupeSiteStringList(result)
		}
		parsedAny := []any{}
		if strings.HasPrefix(trimmed, "[") && json.Unmarshal([]byte(trimmed), &parsedAny) == nil {
			for _, item := range parsedAny {
				appendValue(item)
			}
			return dedupeSiteStringList(result)
		}
		for _, item := range strings.Split(trimmed, ",") {
			appendValue(item)
		}
	default:
		appendValue(typed)
	}
	return dedupeSiteStringList(result)
}

func encodeSiteStringList(value any) string {
	items := siteStringListFromAny(value)
	if len(items) == 0 {
		return "[]"
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func dedupeSiteStringList(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
