package extract

import (
	"regexp"
	"strings"
)

var (
	// reDescriptionCategoryLine 匹配简介正文里的「◎类　　别　xxx」行（匹配前需先把全角空格 U+3000 归一为半角）。
	reDescriptionCategoryLine = regexp.MustCompile(`(?im)^[◎❁]\s*类\s*别\s*[:：]?\s*(.+?)(?:\r?\n|$)`)
)

// categoryDocumentaryKeywords 判定「类别」行是否属于纪录片的关键词。
// "纪录" 已覆盖 "纪录片"；"记录片" 为常见同音写法；英文类别写作 Documentary。
var categoryDocumentaryKeywords = []string{"纪录", "记录片", "documentary"}

// ExtractDescriptionCategoryText 从简介文本中提取「类别」字段原文。
// 参数/返回：description 为简介正文文本；返回类别原文与是否命中。
// 失败场景：文本为空、不存在「◎类　　别」行或类别值为空时返回 false。
// 副作用：无。
func ExtractDescriptionCategoryText(description string) (string, bool) {
	text := strings.TrimSpace(description)
	if text == "" {
		return "", false
	}

	// 兼容“◎类　　别　剧情 / 爱情”里的全角空格（U+3000）。
	normalized := strings.ReplaceAll(text, "\u3000", " ")
	matches := reDescriptionCategoryLine.FindStringSubmatch(normalized)
	if len(matches) < 2 {
		return "", false
	}

	categoryText := strings.TrimSpace(matches[1])
	if categoryText == "" {
		return "", false
	}
	return categoryText, true
}

// InferTypeFromDescriptionCategory 按简介「类别」行推断标准类型键。
// 说明：类别写「纪录 / 纪录片 / 记录片 / Documentary」时统一归为 category.documentaries；
// 其余类别不在此处判断，返回空字符串表示不干预既有类型推断链路。
// 参数/返回：description 为简介正文文本；命中返回 category.documentaries，否则返回空字符串。
// 失败场景：文本为空或不存在类别行时返回空字符串。
// 副作用：无。
func InferTypeFromDescriptionCategory(description string) string {
	categoryText, ok := ExtractDescriptionCategoryText(description)
	if !ok {
		return ""
	}

	lowered := strings.ToLower(categoryText)
	for _, keyword := range categoryDocumentaryKeywords {
		if strings.Contains(lowered, keyword) {
			return "category.documentaries"
		}
	}
	return ""
}
