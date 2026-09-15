// Package descclean 提供简介（description）文本的通用清洗能力，供抓取归一化与发种组装两处复用。
// 说明：独立叶子包，仅依赖标准库，避免 extract/publish 之间的循环依赖。
package descclean

import (
	"regexp"
	"strings"
)

// reMovieParamsSectionStart 匹配源站简介中【影片参数】参数段落的起始标记（含 color/b 开启标签），用于发种前截断冗余 mediainfo 段。
// 兼容两种常见包裹顺序：[color...][b]【影片参数】 与 [b][color...]【影片参数】，从首个开启标签起截断，避免留下悬空标签。
var reMovieParamsSectionStart = regexp.MustCompile(`(?i)(?:\[color[^\]]*\]\s*\[b\]|\[b\]\s*\[color[^\]]*\])\s*【影片参数】`)

// TrimDescriptionAtMovieParams 若简介包含【影片参数】参数段落标记，则删除该标记及其之后的全部内容（含标记本身），仅保留其前面的影片介绍。
// 说明：源站简介常以 [color=blue][b]【影片参数】[/b][/color] 包裹一段 mediainfo 参数，发种/展示时该段冗余，需截断。
// 参数/返回：desc 为原始简介；未命中标记时原样返回。
func TrimDescriptionAtMovieParams(desc string) string {
	trimmed := strings.TrimSpace(desc)
	if trimmed == "" {
		return desc
	}
	if loc := reMovieParamsSectionStart.FindStringIndex(trimmed); loc != nil {
		return strings.TrimSpace(trimmed[:loc[0]])
	}
	if idx := strings.Index(trimmed, "【影片参数】"); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	return desc
}
