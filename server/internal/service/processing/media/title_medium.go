package media

import (
	"regexp"
	"strings"
)

var (
	// reRemuxMediumToken 命中标题里的 Remux 媒介标记（不区分大小写）。
	reRemuxMediumToken = regexp.MustCompile(`(?i)\bremux\b`)
	// reRemuxTokenWithLeadingSeparator 连同前置分隔符一起摘除，避免留下 “Blu-ray  1080p” 这类空洞。
	reRemuxTokenWithLeadingSeparator = regexp.MustCompile(`(?i)[\s._-]+remux\b`)
	// reRemuxTokenAtHead 处理标题以 Remux 开头（前置没有分隔符）的形态。
	reRemuxTokenAtHead = regexp.MustCompile(`(?i)^remux[\s._-]+`)
	// reRepeatedDots 摘除后可能留下连续点号（如 “BluRay..1080p”）。
	reRepeatedDots = regexp.MustCompile(`\.{2,}`)
)

// StripRemuxMediumToken 从标题文本中摘除“Remux”媒介标记。
// 参数/返回：title 为标题原文；返回摘除后的标题，未命中 Remux 时原样返回。
// 失败场景：不返回错误；空标题直接返回空串。
// 副作用：无。
//
// 背景：标题组件「媒介」由标题文本推导，标准媒介被判定为非 Remux 档时
// （碟结构纠偏为原盘、或 MediaInfo/BDInfo 判为 encode 等），标题里残留的 “Remux”
// 会让面板「媒介」一直停在 “Blu-ray Remux”，与标准 medium 矛盾，并随发种标题提交给站点。
func StripRemuxMediumToken(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" || !reRemuxMediumToken.MatchString(trimmed) {
		return trimmed
	}

	result := trimmed
	// 标题里 Remux 理论上只出现一次，这里循环几次纯粹作为兜底，避免正则不收敛导致死循环。
	for i := 0; i < 4; i++ {
		next := reRemuxTokenWithLeadingSeparator.ReplaceAllString(result, "")
		next = reRemuxTokenAtHead.ReplaceAllString(next, "")
		if next == result {
			break
		}
		result = next
	}

	result = reRepeatedDots.ReplaceAllString(strings.Join(strings.Fields(result), " "), ".")
	return strings.TrimSpace(strings.Trim(result, ".-_"))
}
