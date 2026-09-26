package extract

import (
	"regexp"
	"strings"
)

var (
	reDescriptionYearLine = regexp.MustCompile(`(?im)^[◎❁]\s*年\s*代\s*[:：]?\s*(.+?)(?:\r?\n|$)`)
	reFourDigitYearToken  = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
)

// InferYearFromDescription 从简介文本的“年代”行提取 4 位年份。
// 参数/返回：description 为声明与正文拼接文本；成功返回如 "2022"；无“年代”行或行内无 4 位年份时返回空字符串。
// 失败场景：description 为空、无“年代”行或行内不含 4 位年份。
// 副作用：无。
//
// 支持写法：◎年　　代　2022（全角空格）/ ◎年 代 2022 / ◎年代：2022-02-14(意大利) / ◎年　　代　2022(重映)。
// 站点简介的“年代”行是发行年份的权威来源，用于纠正标题缺失年份或标题年份与发行年份不一致的情况。
func InferYearFromDescription(description string) string {
	text := strings.TrimSpace(description)
	if text == "" {
		return ""
	}

	// 兼容“◎年　　代　2022”里的全角空格（U+3000）。
	normalized := strings.ReplaceAll(text, "\u3000", " ")
	match := reDescriptionYearLine.FindStringSubmatch(normalized)
	if len(match) < 2 {
		return ""
	}
	return reFourDigitYearToken.FindString(strings.TrimSpace(match[1]))
}
