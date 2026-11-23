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

// reScreenshotsSectionStart 匹配源站简介中「更多视频截图」截图段落的起始标记（形如 [quote][size=3][color=royalblue][b]★★★★★ 更多视频截图 ★★★★★[/b][/color][/size][/quote]）。
// 匹配「实心/空心星号 + 更多视频截图」标题，并向前吞掉紧邻的 BBCode 开启标签（[quote]/[size]/[color]/[b] 等），避免截断后残留悬空开启标签。
// 开启标签一律以字母开头（[b] / [size=3]），闭合标签以 / 开头（[/b]），因此向前吞标签不会误吞上一段的闭合标签。
var reScreenshotsSectionStart = regexp.MustCompile(`(?s)(?:\[[a-zA-Z][^\]]{0,64}\]\s*)*[★☆]+\s*更多视频截图\s*[★☆]*`)

// reBBCodeTag 用于判断某段文本是否只由 BBCode 标签构成（配合 fallback 行首回退使用）。
var reBBCodeTag = regexp.MustCompile(`\[[^\]]*\]`)

// reGeneralInfoSectionStart 匹配源站简介里的 release info 段标题「Quote:★★★★★ General Information ★★★★★」。
// TTG 等站把它接在◎简　　介之后，后面跟 RELEASE.NAME / VIDEO.CODEC / UPLOADER 等发布参数，属于冗余；
// 允许前后吞掉紧邻的 BBCode 开启标签（[b]/[quote]/[size] 等），并对 `Quote:` 之后紧跟的闭合标签
// （典型形态 [b]Quote:[/b][b]★★★★★…）一并吞掉，避免从标签内部切开留下悬空 [b]。
// 判定必须前后都有星号标题装饰：正文里普通提到的「General Information」不会命中，避免误截断。
var reGeneralInfoSectionStart = regexp.MustCompile(`(?is)(?:\[[a-zA-Z][^\]]{0,64}\]\s*)*(?:quote\s*[:：]\s*(?:\[\/[a-zA-Z][^\]]{0,64}\]\s*)*)?(?:\[[a-zA-Z][^\]]{0,64}\]\s*)*[★☆]+\s*general\s+information\s*[★☆]+`)

// reQuoteSectionStart 匹配正文里的「Quote:」段落标记（常见于 TTG 等站的 release info 段起头）。
// 需求口径：正文一旦出现 Quote:（含全角冒号），该标记及其后的全部内容都删掉——
// 后面通常跟着 RELEASE.NAME / VIDEO.CODEC / UPLOADER 之类发布参数，属于冗余。
// 向前吞掉紧邻的 BBCode 开启标签（[b]/[quote]/[size] 等），向后吞掉 Quote: 紧跟的闭合标签
// （典型形态 [b]Quote:[/b][b]★★★★★…），避免截断后残留悬空标签。
// 注意：只认带冒号的 `quote:`，BBCode 引用块 [quote]/[quote=作者] 不会命中（正文里的引用不能误删）。
var reQuoteSectionStart = regexp.MustCompile(`(?is)(?:\[[a-zA-Z][^\]]{0,64}\]\s*)*\bquote\s*[:：]\s*(?:\[\/[a-zA-Z][^\]]{0,64}\]\s*)*`)

// descCutMarker 描述一处需在简介中「从标记处截断到结尾」的冗余段落起始标记。
// re 负责带 BBCode 的精确起始定位；fallback 为纯文本回退关键词（空串表示不做纯文本回退）。
type descCutMarker struct {
	re       *regexp.Regexp
	fallback string
}

// descCutMarkers 汇总所有冗余段落标记。顺序无关：
// 统一入口会取最早出现的匹配位置，保证从最靠前的标记处连同其后全部内容一起删除。
var descCutMarkers = []descCutMarker{
	{re: reMovieParamsSectionStart, fallback: "【影片参数】"},
	{re: reScreenshotsSectionStart, fallback: "更多视频截图"},
	// 「Quote:」起头的 release info 段：命中即截到结尾。
	{re: reQuoteSectionStart},
	// release info 段的无 Quote 变体（[quote][size=3][b]★★★★★ General Information ★★★★★）：
	// 星号是它区别于正文叙述的关键特征，丢了星号宁可不动，避免把正文后半段误删。
	{re: reGeneralInfoSectionStart},
}

// TrimDescription 截断简介中的冗余段落：命中 descCutMarkers 中任一标记时，
// 删除该标记及其之后的全部内容（含标记本身），仅保留其前面的影片介绍。
// 参数/返回：desc 为原始简介；未命中任何标记时原样返回。
func TrimDescription(desc string) string {
	trimmed := strings.TrimSpace(desc)
	if trimmed == "" {
		return desc
	}

	cut := -1
	for _, marker := range descCutMarkers {
		idx := -1
		if loc := marker.re.FindStringIndex(trimmed); loc != nil {
			idx = loc[0]
		} else if marker.fallback != "" {
			if pos := strings.Index(trimmed, marker.fallback); pos >= 0 {
				idx = marker.startIndex(trimmed, pos)
			}
		}
		if idx >= 0 && (cut < 0 || idx < cut) {
			cut = idx
		}
	}
	if cut < 0 {
		return desc
	}
	return strings.TrimSpace(trimmed[:cut])
}

// TrimDescriptionAtMovieParams 保留原有入口名（语义等价于 TrimDescription），供既有调用点继续使用。
// 说明：包内标记表已扩展（新增「更多视频截图」段落），因此本函数同时承担新旧两类截断。
func TrimDescriptionAtMovieParams(desc string) string {
	return TrimDescription(desc)
}

// EnsureStatementQuote 保证声明被一对 [quote]…[/quote] 包裹。
//
// 背景：部分源站（TTG 等）的简述只给纯文本，如「Audiences官组作品，感谢原制作者发布。」，
// 而各目标站的声明区块都按 [quote] 渲染；缺包裹时声明会掉格式，或被正文的段落判定吞并。
// 判定口径：声明以 [quote] / [quote=…] 开头即视为已包裹（含多段 quote 并列的情形），原样返回；
// 因此本函数幂等，可在抓取归一与发种组装两侧重复调用。
// 参数/返回：statement 为声明文本；返回包裹后的声明，空串仍返回空串。
// 副作用：无。
func EnsureStatementQuote(statement string) string {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "[quote") {
		return trimmed
	}
	return "[quote]" + trimmed + "[/quote]"
}

// startIndex 根据纯文本关键词命中位置推算真正的截断起点。
// 若该关键词所在行、自行首至关键词之间的内容只由 BBCode 标签与空白构成，
// 则回退到行首（把同行残留的开启标签一并删除，避免截断后出现悬空标签）；否则保留行内关键词之前的内容。
func (m descCutMarker) startIndex(text string, pos int) int {
	lineStart := lineStartIndex(text, pos)
	if lineStart < pos && bbcodeOrSpaceOnly(text[lineStart:pos]) {
		return lineStart
	}
	return pos
}

// lineStartIndex 返回 text 中 pos 所在行的行首下标。
func lineStartIndex(text string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if idx := strings.LastIndexByte(text[:pos], '\n'); idx >= 0 {
		return idx + 1
	}
	return 0
}

// bbcodeOrSpaceOnly 判断 s 去掉全部 BBCode 标签后是否只剩空白字符。
func bbcodeOrSpaceOnly(s string) bool {
	if s == "" {
		return true
	}
	return strings.TrimSpace(reBBCodeTag.ReplaceAllString(s, "")) == ""
}
