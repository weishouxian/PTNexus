package iyuu

import (
	"strconv"
	"strings"
)

// toStringValue 把任意值转为去空格字符串，失败时返回默认值。
func toStringValue(value any, defaultValue string) string {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return defaultValue
		}
		return trimmed
	case nil:
		return defaultValue
	default:
		text := strings.TrimSpace(strconv.FormatFloat(toFloatValue(value), 'f', -1, 64))
		if text == "" {
			return defaultValue
		}
		return text
	}
}

// toIntValue 把任意值转为 int，失败时返回默认值。
func toIntValue(value any, defaultValue int) int {
	return int(toInt64Value(value, int64(defaultValue)))
}

// toInt64Value 把任意值转为 int64，失败时返回默认值。
func toInt64Value(value any, defaultValue int64) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return defaultValue
		}
		if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return parsed
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int64(parsed)
		}
		return defaultValue
	case nil:
		return defaultValue
	default:
		return defaultValue
	}
}

// toFloatValue 把任意值转为 float64，失败时返回 0。
func toFloatValue(value any) float64 {
	switch typed := value.(type) {
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case float64:
		return typed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}
