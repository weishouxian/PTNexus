package extract

import (
	"regexp"
	"strings"
)

var (
	reDescriptionSourceLine  = regexp.MustCompile(`(?im)^[◎❁]\s*产\s*地\s*[:：]?\s*(.+?)(?:\r?\n|$)`)
	reDescriptionCountryLine = regexp.MustCompile(`(?im)^[◎❁]\s*(?:国\s*家|地\s*区)\s*[:：]?\s*(.+?)(?:\r?\n|$)`)
	reRegionSourceLine       = regexp.MustCompile(`(?im)^\s*(?:制片国家/地区|制片國家/地區)\s*[:：]\s*(.+?)(?:\r?\n|$)`)
)

// InferSourceFromDescription 从简介文本中提取“产地/制片国家/地区”并映射为标准 source.* 键。
// 参数/返回：description 为声明与正文拼接文本；成功返回 source.china/source.japan 等；未命中或无法识别返回空字符串。
// 失败场景：description 为空或不存在产地行时返回空字符串。
// 副作用：无。
func InferSourceFromDescription(description string) string {
	text := strings.TrimSpace(description)
	if text == "" {
		return ""
	}

	// 兼容“◎产　　地　日本”里的全角空格（U+3000）。
	normalized := strings.ReplaceAll(text, "\u3000", " ")

	sourceText := ""
	if matches := reDescriptionSourceLine.FindStringSubmatch(normalized); len(matches) >= 2 {
		sourceText = strings.TrimSpace(matches[1])
	} else if matches := reDescriptionCountryLine.FindStringSubmatch(normalized); len(matches) >= 2 {
		sourceText = strings.TrimSpace(matches[1])
	} else if matches := reRegionSourceLine.FindStringSubmatch(normalized); len(matches) >= 2 {
		sourceText = strings.TrimSpace(matches[1])
	}
	if sourceText == "" {
		return ""
	}

	// 常见形式：日本 / 韩国、美国, 英国、China/Hong Kong 等。
	// 归一口径统一在 sourceKeyAliasGroups（见 source_key.go），此处不再单独维护分支。
	return NormalizeSourceKeyFromText(sourceText)
}
