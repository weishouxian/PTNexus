package dupe

import (
	neturl "net/url"
	"sort"
	"strings"
)

// appendQuery 把参数按 key 排序后拼到 URL 上。
// 参数/返回：base 为不含查询串的 URL；values 为参数表；返回带查询串的 URL。
// 说明：排序保证同一组参数生成稳定可比的 URL，便于在日志里核对与复现。
// 副作用：无。
func appendQuery(base string, values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		if strings.TrimSpace(key) != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	encoded := neturl.Values{}
	for _, key := range keys {
		encoded.Set(key, values[key])
	}
	return base + "?" + encoded.Encode()
}

// firstNonEmptyValue 返回首个去除空白后非空的字符串。
func firstNonEmptyValue(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
