package siteprofile

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

// 本文件实现 NexusPHP 站点个人资料页（userdetails.php）的解析。
//
// 解析口径参考 PT-Plugin-Plus / MoviePilot 的 NexusPHP 实现：
//  1. userdetails.php 的资料表是「<td class="rowhead">标签</td><td class="rowfollow">值</td>」的平铺键值对，
//     按 td 出现顺序配对，比写死整段表格正则更能扛住各站皮肤差异；
//  2. 上传量/下载量文本里可能夹 <br />、<span title>、&nbsp;，必须先转纯文本再解析数值；
//  3. 做种数优先取资料页的「在做种」行，缺失时由调用方退回
//     getusertorrentlistajax.php?userid=X&type=seeding 计数。

var (
	reTD        = regexp.MustCompile(`(?is)<td\b([^>]*)>(.*?)</td>`)
	reUserLevel = regexp.MustCompile(`(?is)<(?:img|span|a)\b[^>]*class\s*=\s*["'][^"']*userlevel[^"']*["'][^>]*>`)
	// reTag 剥 HTML 标签时必须认引号内的 `>`：梓喵（azusa）页头把排名信息塞进
	// `onmouseover="domTT_activate(this, event, 'content', '综合排名: <b style=\'color:orangered;\'>1264</b>…')"`，
	// 属性值里本身带 `>`，用 `<[^>]+>` 会在第一个 `>` 处断开，
	// 剩下的 `, 'trail', true, …);">` 会被当成正文，把用户名污染成一大串 JS 参数。
	reTag         = regexp.MustCompile(`(?is)<(?:[^>"']|"[^"]*"|'[^']*')+>`)
	reScriptStyle = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
	reFloat       = regexp.MustCompile(`-?\d+(?:[.,]\d+)*`)
	reSizeValue   = regexp.MustCompile(`(?i)(\d[\d.,]*)\s*([KMGTPE]?i?B)\b`)
	reDigits      = regexp.MustCompile(`\d+`)
)

// 资料页标签别名表。匹配按「包含」进行，顺序即优先级：
// 必须先放更具体的别名（如「上传量」），避免被「上传」这种短词提前命中。
// labelBonus 末尾的「积分」是给 TTG 这类把魔力值直接叫「积分」的站点兜底
// （页头 `积分 : 356630.30`）：pick 先做全等匹配，只有没有「魔力值/魔力/bonus」
// 这类标签时才轮到它，而且「做种积分」已被 labelSeedingBonus 提前取走。
var (
	labelUploaded     = []string{"上传量", "上传", "uploaded", "upload"}
	labelDownloaded   = []string{"下载量", "下载", "downloaded", "download"}
	labelUsername     = []string{"用户名", "username", "user name"}
	labelRatio        = []string{"分享率", "分享", "ratio"}
	labelBonus        = []string{"魔力值", "茉莉", "魔力", "bonus", "karma", "积分"}
	labelClass        = []string{"用户等级", "等级", "user class", "class", "rank"}
	labelSeeding      = []string{"在做种", "当前做种", "做种数", "seeding"}
	labelLeeching     = []string{"在下载", "当前下载", "下载数", "leeching"}
	labelInvites      = []string{"邀请名额", "邀请数", "邀请", "invites"}
	labelJoinDate     = []string{"加入日期", "注册日期", "注册时间", "join date", "joined"}
	labelSeedingBonus = []string{"做种积分", "做种得分", "做种分数", "做种分值", "seed bonus", "seedbonus", "seeding bonus", "seed points", "seedpoints"}
)

// labeledCell 是一个「标签 → 值」单元格。
// 用有序切片而不是 map：同一页常出现「上传量」与「上传量(本月)」这类近似标签，
// map 遍历顺序随机会让同一个站两次刷新取到不同的值。
type labeledCell struct {
	Label string // 归一化后的标签（去空白/冒号、小写）
	Value string // 原始值片段（表格里是 HTML 片段，文本/属性来源里是纯文本）
}

// cellSet 汇总多个来源的键值对，并提供「取走即标记」的按别名取值。
type cellSet struct {
	items []labeledCell
	used  []bool
}

// newCellSet 按来源优先级合并多组键值对（靠前的组优先命中）。
func newCellSet(groups ...[]labeledCell) *cellSet {
	total := 0
	for _, group := range groups {
		total += len(group)
	}
	set := &cellSet{items: make([]labeledCell, 0, total), used: make([]bool, 0, total)}
	for _, group := range groups {
		for _, item := range group {
			set.items = append(set.items, item)
			set.used = append(set.used, false)
		}
	}
	return set
}

// size 返回键值对总数（含未使用的），用于判断页面是否解析出了任何结构化内容。
func (c *cellSet) size() int {
	if c == nil {
		return 0
	}
	return len(c.items)
}

// cellLabels 返回前若干个标签，用于「解析不到流量」时的排错日志。
func cellLabels(cells *cellSet) string {
	if cells == nil || len(cells.items) == 0 {
		return "(空)"
	}
	limit := len(cells.items)
	if limit > 16 {
		limit = 16
	}
	labels := make([]string, 0, limit)
	for _, item := range cells.items[:limit] {
		labels = append(labels, item.Label)
	}
	return strings.Join(labels, ",")
}

// pick 按别名优先级取值：先整标签相等，再退化为包含匹配。
// 参数/返回：aliases 为别名优先级列表；返回命中的标签与值（未命中均为空串）。
// 失败场景：无。
// 副作用：命中后把该条目标记为已用 —— 「Seed Bonus」既含 bonus 又含 seeding，
// 不标记就会被魔力值/做种数二次取走。
func (c *cellSet) pick(aliases []string) (string, string) {
	if c == nil || len(c.items) == 0 {
		return "", ""
	}
	for _, alias := range aliases {
		key := normalizeLabel(alias)
		if key == "" {
			continue
		}
		for index, item := range c.items {
			if c.used[index] || item.Label != key {
				continue
			}
			c.used[index] = true
			return item.Label, item.Value
		}
	}
	for _, alias := range aliases {
		key := normalizeLabel(alias)
		if key == "" {
			continue
		}
		for index, item := range c.items {
			if c.used[index] || !strings.Contains(item.Label, key) {
				continue
			}
			c.used[index] = true
			return item.Label, item.Value
		}
	}
	return "", ""
}

// extractLabeledCells 把 userdetails 资料表解析成有序的「标签 → 值(HTML 片段)」列表。
// 参数/返回：doc 为整页 HTML；返回按标签在页面出现顺序排列的键值对（同名标签只留第一处）。
// 失败场景：无（页面结构不符时返回空切片）。
// 副作用：无。
func extractLabeledCells(doc string) []labeledCell {
	cells := make([]labeledCell, 0, 32)
	seen := map[string]bool{}
	matches := reTD.FindAllStringSubmatch(doc, -1)
	pendingLabel := ""
	for _, match := range matches {
		attr := strings.ToLower(match[1])
		value := match[2]
		if strings.Contains(attr, "rowhead") {
			pendingLabel = normalizeLabel(value)
			continue
		}
		if pendingLabel == "" {
			continue
		}
		// 值单元格：标准皮肤是 class="rowfollow"，老皮肤（TTG）只写 `<td align=left>` ——
		// 强制要求 rowfollow 会让这类站的资料表只剩标签、一个值都取不到（等级因此缺失）。
		if !strings.Contains(attr, "rowfollow") && !isBareValueCell(attr) {
			continue
		}
		// 皮肤常用 `<td>&nbsp;</td>` 占位，遇到空值继续等下一格，不要把标签丢掉。
		if plainText(value) == "" && imageLabelName(value) == "" {
			continue
		}
		if !seen[pendingLabel] {
			seen[pendingLabel] = true
			cells = append(cells, labeledCell{Label: pendingLabel, Value: value})
		}
		pendingLabel = ""
	}
	return cells
}

// isBareValueCell 判断一个 td 是否是「没写 class、但明显是紧跟 rowhead 的值格」。
// 老皮肤（TTG）写成 `<td align=left>BrontoByte</td>`：只带对齐属性、没有 class。
// 带 colspan/rowspan 的多格合并单元格不算 —— 那是分组标题或嵌套表格。
func isBareValueCell(attr string) bool {
	if strings.Contains(attr, "colspan") || strings.Contains(attr, "rowspan") {
		return false
	}
	return strings.Contains(attr, "align")
}

// secondaryLabelKeywords 是「非账号总量」的统计口径修饰词。
// 页头/侧栏常同屏展示「本月上传量」「今日下载量」，一旦被当成总量会把数值整体带偏。
var secondaryLabelKeywords = []string{"本月", "上月", "今日", "今天", "昨日", "昨天", "本周", "上周", "本年", "今年", "平均", "期间", "过去", "最近"}

// isSecondaryLabel 判断标签前方是否带「本月/今日」这类统计口径修饰。
// 参数/返回：before 为标签之前的上下文文本；命中修饰词返回 true。
// 失败场景：无。
// 副作用：无。
func isSecondaryLabel(before string) bool {
	runes := []rune(strings.TrimSpace(before))
	if len(runes) > 8 {
		runes = runes[len(runes)-8:]
	}
	tail := string(runes)
	for _, keyword := range secondaryLabelKeywords {
		if strings.Contains(tail, keyword) {
			return true
		}
	}
	return false
}

var (
	reColonMark = regexp.MustCompile(`[:：]`)
	reSizeUnit  = regexp.MustCompile(`(?i)^[kmgtpe]i?b$|^[kmgtp]b$`)
)

// extractInlinePairs 从页面纯文本里提取「标签：值」对。
//
// 为什么需要它：现代 NexusPHP 皮肤（以及大量自研皮肤）已经把上传/下载/分享率从资料表里
// 挪到了页头状态条或自定义卡片，写法变成
//   - 页头：<font class="color_ratio">分享率：</font> 17.889 上传量： 160.389 TB
//   - 资料表「传输」行：一个格子里塞「上传量 : 21.374 TB / 下载量 : 0.00 KB」
//   - Tailwind 皮肤：<span>上传量：</span><span>28.329 TB</span>
//
// 这三种都不在 <td class="rowhead"> 里，只做 rowhead→rowfollow 配对会整段漏掉。
//
// 参数/返回：doc 为整页 HTML；返回按出现顺序排列的键值对。
// 失败场景：无。
// 副作用：无。
func extractInlinePairs(doc string) []labeledCell {
	lines := strings.Split(htmlToTextWithBreaks(doc), "\n")
	cells := make([]labeledCell, 0, 48)
	for index, line := range lines {
		for _, loc := range reColonMark.FindAllStringIndex(line, -1) {
			context := strings.TrimRight(line[:loc[0]], " \t")
			// 这里要检查「原始上下文」：去除括号片段后「上传量(本月)」会退化成「上传量」，
			// 只剩后面那次检查的话会把本月增量当成账号总量。
			if isSecondaryLabel(context) {
				continue
			}
			label, labelStart := lastLabelToken(context)
			if label == "" {
				continue
			}
			// 标签前面若是「本月 / 今日」这类修饰词，说明不是账号总量，直接跳过。
			if isSecondaryLabel(context[:labelStart]) {
				continue
			}
			value := firstValueTokens(line[loc[1]:])
			if value == "" && index+1 < len(lines) {
				// 标签与值被块级标签分到两行的情况（如 label 独占一行）。
				value = firstValueTokens(lines[index+1])
			}
			if value == "" {
				continue
			}
			cells = append(cells, labeledCell{Label: normalizeLabel(label), Value: value})
		}
	}
	return cells
}

var (
	reWord = regexp.MustCompile(`[^\s]+`)
	// reBracketSegment 匹配夹在标签与冒号之间的操作入口：[使用] / （详情） / 【更多】。
	reBracketSegment = regexp.MustCompile(`\[[^\]]{0,30}\]|\([^)]{0,30}\)|（[^）]{0,30}）|【[^】]{0,30}】|〔[^〕]{0,30}〕`)
)

// lastLabelToken 取冒号前最后一个「标签」词，忽略 [使用] 这类夹在中间的操作链接。
// 参数/返回：context 为冒号前的文本；返回标签词及其起始字节位置（用于取前缀判断「本月」等修饰）。
// 失败场景：无可用词时返回空串。
// 副作用：无。
//
// 例：`魔力值 [使用]: 1,593,841.1` 里冒号前是「魔力值 [使用]」，
// 真正的标签是「魔力值」；HTML 剥标签会把链接文本变成独立词，所以先整体抹掉括号片段。
// 抹除时用等长空格占位，保证返回的位置仍能索引原文本。
func lastLabelToken(context string) (string, int) {
	cleaned := reBracketSegment.ReplaceAllStringFunc(context, func(segment string) string {
		return strings.Repeat(" ", len(segment))
	})
	locs := reWord.FindAllStringIndex(cleaned, -1)
	if len(locs) == 0 {
		return "", 0
	}
	last := locs[len(locs)-1]
	return cleaned[last[0]:last[1]], last[0]
}

// firstValueTokens 返回冒号后的值：第一个片段，若第二个片段是体积单位则一起带上。
// 这样「160.389 TB」能整体交给 parseSizeBytes，「17.889」也能交给 parseRatio。
func firstValueTokens(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 1 && reSizeUnit.MatchString(fields[1]) {
		return fields[0] + " " + fields[1]
	}
	return fields[0]
}

// reAttrStatsPair 匹配「"label":"上传量：","value":"101.552 TB"」这类写在属性里的统计对。
var reAttrStatsPair = regexp.MustCompile(`(?is)["']?(?:label|name|title|text)["']?\s*:\s*["']([^"'<>]{1,16})["']\s*,\s*["']?(?:value|val|amount|count|num)["']?\s*:\s*["']([^"'<>]{1,32})["']`)

var (
	// reTitleAttr 匹配 title="…" 属性值（值里可能带中文冒号，如「茉莉: 329,518.8」）。
	reTitleAttr = regexp.MustCompile(`(?is)\btitle\s*=\s*["']([^"'<]{1,80})["']`)
	// reTitleLabelValue 认「标签: 数值」整体写在属性里的写法。
	reTitleLabelValue = regexp.MustCompile(`^(.{1,12}?)\s*[:：]\s*(-?\d[\d.,]*)$`)
	// reTitlePlainNumber 认属性里只有数字的写法（不可说：<span title="606,714.6">606.7K</span>）。
	reTitlePlainNumber = regexp.MustCompile(`^-?\d[\d.,]*$`)
)

// extractTitleNumberPairs 提取「精确值写在 title 属性里、正文只显示缩写」的统计项。
//
// 为什么需要：不可说（ssd）页头把做种积分/茉莉缩写成三个字符显示，精确值放在 title 里：
//
//	<a title="茉莉: 329,518.8">茉莉: 329.5K</a>
//	<font class="color_bonus">做种积分:</font> <span title="606,714.6">606.7K</span>
//
// 只按正文解析会拿到 329.5 / 606.7，比真实值小三个数量级，
// 导致等级弹窗的做种积分进度、当日增量统计全部偏小。
//
// 参数/返回：doc 为整页 HTML；返回 label 已归一化的键值对（值为纯数字文本）。
// 失败场景：无（没有这类属性时返回空切片）。
// 副作用：无。
func extractTitleNumberPairs(doc string) []labeledCell {
	cells := make([]labeledCell, 0, 4)
	seen := map[string]bool{}
	for _, loc := range reTitleAttr.FindAllStringSubmatchIndex(doc, -1) {
		raw := strings.TrimSpace(doc[loc[2]:loc[3]])
		if raw == "" {
			continue
		}
		label, value := "", ""
		if sub := reTitleLabelValue.FindStringSubmatch(raw); sub != nil {
			label, value = sub[1], sub[2]
		} else if reTitlePlainNumber.MatchString(raw) && countDigits(raw) >= 4 {
			// 纯数字属性：标签只能从属性前面的正文里找（「做种积分:」）。
			label, value = labelBeforeAttribute(doc, loc[0]), raw
		}
		if label == "" || value == "" {
			continue
		}
		key := normalizeLabel(label)
		if key == "" || seen[key] || isSecondaryLabel(label) {
			continue
		}
		seen[key] = true
		cells = append(cells, labeledCell{Label: key, Value: value})
	}
	return cells
}

// labelBeforeAttribute 从属性位置往前找最近的「标签:」，返回标签词。
// 参数/返回：doc 为整页 HTML，pos 为属性起始位置；返回标签（找不到返回空串）。
// 失败场景：窗口内没有冒号时返回空串。
// 副作用：无。
func labelBeforeAttribute(doc string, pos int) string {
	start := max(0, pos-240)
	window := plainText(doc[start:pos])
	locs := reColonMark.FindAllStringIndex(window, -1)
	if len(locs) == 0 {
		return ""
	}
	label, _ := lastLabelToken(window[:locs[len(locs)-1][0]])
	return label
}

// countDigits 统计字符串里的数字字符个数，用于判断属性值是「精确数值」还是顺带的小数字。
func countDigits(raw string) int {
	count := 0
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			count++
		}
	}
	return count
}

// extractAttributePairs 提取 data-* 属性内嵌 JSON 的统计对。
//
// 人人（audiences）这类皮肤把数字只放在 data-uploader-stats 属性里，
// 正文文本中根本没有数值，纯文本扫描拿不到，只能对反转义后的 HTML 再做一次配对。
//
// 参数/返回：doc 为整页 HTML；返回按出现顺序排列的键值对。
// 失败场景：无。
// 副作用：无。
func extractAttributePairs(doc string) []labeledCell {
	unescaped := html.UnescapeString(doc)
	matches := reAttrStatsPair.FindAllStringSubmatch(unescaped, -1)
	cells := make([]labeledCell, 0, len(matches))
	for _, match := range matches {
		label := normalizeLabel(match[1])
		if label == "" || isSecondaryLabel(match[1]) {
			continue
		}
		cells = append(cells, labeledCell{Label: label, Value: strings.TrimSpace(match[2])})
	}
	return cells
}

// parsedDetail 是资料页解析结果（不含需要额外请求才能拿到的兜底数据）。
type parsedDetail struct {
	Profile Profile
	// SeedingParsed / LeechingParsed 表示做种/下载中数量是否真的从页面解析出了数字。
	// 很多皮肤这两行是 [显示/隐藏] 折叠开关，取到文本却没数字，需要继续走种子列表计数兜底。
	SeedingParsed  bool
	LeechingParsed bool
	Cells          *cellSet
}

// parseDetailDoc 把 userdetails.php 的 HTML 解析成账号数据。
//
// 三层来源按优先级合并：
//  1. 资料表 rowhead→rowfollow 键值格（老皮肤）；
//  2. 页面文本里的「标签：值」（页头状态条 / 「传输」行 / 自定义卡片皮肤）;
//  3. data-* 属性内嵌 JSON 的 label/value 对（人人这类把数字只写在属性里的皮肤）。
//
// 现代 NexusPHP 皮肤普遍把上传/下载/分享率移出资料表，只做第 1 层会整段漏掉。
// 参数/返回：doc 为整页 HTML；返回解析结果（需网络兜底的部分由调用方补齐）。
// 失败场景：无（解析不到时各项为零值）。
// 副作用：无。
func parseDetailDoc(doc string) parsedDetail {
	cells := newCellSet(extractTitleNumberPairs(doc), extractLabeledCells(doc), extractInlinePairs(doc), extractAttributePairs(doc))
	result := parsedDetail{Profile: Profile{Source: "nexusphp"}, Cells: cells}
	profile := &result.Profile

	// 做种积分必须先解析并把命中的标签标记为已用：
	// 「Seed Bonus / Seeding Bonus」这类标签既是做种积分，又包含 bonus（魔力值别名）与 seeding（做种数别名），
	// 留在候选里会被后面两个字段误取。中文标签「做种积分」不含「在做种/做种数」，不受影响。
	if _, value := cells.pick(labelSeedingBonus); value != "" {
		if bonus, ok := parseLooseFloat(plainText(value)); ok {
			profile.SeedBonus = bonus
		}
	}
	if _, value := cells.pick(labelUploaded); value != "" {
		if bytes, ok := parseSizeBytes(value); ok {
			profile.Uploaded = bytes
		}
	}
	if _, value := cells.pick(labelDownloaded); value != "" {
		if bytes, ok := parseSizeBytes(value); ok {
			profile.Downloaded = bytes
		}
	}
	if _, value := cells.pick(labelRatio); value != "" {
		if ratio, ok := parseRatio(value); ok {
			profile.Ratio = ratio
		}
	}
	if _, value := cells.pick(labelBonus); value != "" {
		if bonus, ok := parseLooseFloat(value); ok {
			profile.Bonus = bonus
		}
	}
	// 只有解析出大于 0 的数字才算「确认取到」：页面里带 seeding/leeching 字样的行很多
	// （Seeding Time、Leeching Time 这类时长行），命中它们只会得到 0，
	// 这种情况仍要让上层走种子列表计数兜底。
	if _, value := cells.pick(labelSeeding); value != "" {
		if count, ok := parseInteger(value); ok && count > 0 {
			profile.Seeding = count
			result.SeedingParsed = true
		}
	}
	if _, value := cells.pick(labelLeeching); value != "" {
		if count, ok := parseInteger(value); ok && count > 0 {
			profile.Leeching = count
			result.LeechingParsed = true
		}
	}
	if _, value := cells.pick(labelInvites); value != "" {
		if count, ok := parseInteger(value); ok {
			profile.Invites = count
		}
	}
	if _, value := cells.pick(labelJoinDate); value != "" {
		profile.JoinDate = plainText(value)
	}
	// 用户名只作兜底：首页通常已经拿到，这里补的是「资料页可脱 id 打开」的站点（如城市），
	// 以及已知 uid 时不再请求首页、只能从页头取名的情形（梓喵：资料表没有「用户名」行）。
	if _, value := cells.pick(labelUsername); value != "" {
		profile.Username = plainText(value)
	}
	if profile.Username == "" {
		profile.Username = headerUserName(doc)
	}
	profile.UserClass = extractUserLevel(doc, cells)

	// 页头的「当前做种 / 当前下载」多数是「图标(alt/title 是标签) + 紧跟数字」的形式，
	// 能直接取到就省掉后面两次种子列表计数请求。
	if !result.SeedingParsed {
		if count, ok := extractActivityCount(doc, "seeding"); ok && count > 0 {
			profile.Seeding = count
			result.SeedingParsed = true
		}
	}
	if !result.LeechingParsed {
		if count, ok := extractActivityCount(doc, "leeching"); ok && count > 0 {
			profile.Leeching = count
			result.LeechingParsed = true
		}
	}

	// 分享率缺失时按上传/下载推算；下载为 0 且有上传量时站点显示 ∞，用 -1 表示。
	if profile.Ratio == 0 {
		switch {
		case profile.Downloaded > 0:
			profile.Ratio = float64(profile.Uploaded) / float64(profile.Downloaded)
		case profile.Uploaded > 0:
			profile.Ratio = -1
		}
	}
	return result
}

// reActivityCount 匹配「<img alt='Torrents seeding' title='当前做种' .../>340」这类图标计数。
var reActivityCount = regexp.MustCompile(`(?is)(?:alt|title)\s*=\s*["']([^"']{1,40})["'][^>]*>\s*(\d+)`)

// reActivityCountNested 是上面那条的宽松版：允许图标与数字之间夹「&nbsp; + 内联样式标签」。
// TTG 的页头写 `<img alt="做种中" title="做种中" src="/pic/arrowup.gif">&nbsp;<font color=black><span class="smallfont">291</span></font>`，
// 严格版要求 `>` 后面直接是数字，于是整站做种数永远是 0。
// 只放行 font/span/b/strong/i/em/a 这些内联标签，遇到 td/tr/div/table 就停，避免把别处的数字算进来。
var reActivityCountNested = regexp.MustCompile(`(?is)(?:alt|title)\s*=\s*["']([^"']{1,40})["'][^>]*>(?:(?:&nbsp;|\s)|</?(?:font|span|b|strong|i|em|a)\b[^>]*>)*(\d+)`)

// extractActivityCount 从页头图标后的数字里取做种/下载中数量。
// 参数/返回：doc 为整页 HTML；kind 为 seeding 或 leeching；返回数量与是否解析成功。
// 失败场景：页面没有该图标或后面不是纯数字时返回 false。
// 副作用：无。
func extractActivityCount(doc, kind string) (int64, bool) {
	keywords := []string{"seeding", "做种"}
	if kind == "leeching" {
		keywords = []string{"leeching", "downloading", "下载"}
	}
	for _, match := range reActivityCount.FindAllStringSubmatch(doc, -1) {
		// 走 normalizeLabel：繁体皮肤的图标 title 是「當前做種」，直接比简体关键词会漏。
		label := normalizeLabel(match[1])
		matched := false
		for _, keyword := range keywords {
			if strings.Contains(label, strings.ToLower(keyword)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		value, err := strconv.ParseInt(match[2], 10, 64)
		if err == nil {
			return value, true
		}
	}
	// 严格版一条都没命中时再试宽松版（数字被 font/span 包起来的老皮肤，如 TTG）。
	// 放在兜底位置是为了不动已经能正常取到数值的站点。
	for _, match := range reActivityCountNested.FindAllStringSubmatch(doc, -1) {
		label := normalizeLabel(match[1])
		matched := false
		for _, keyword := range keywords {
			if strings.Contains(label, strings.ToLower(keyword)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		value, err := strconv.ParseInt(match[2], 10, 64)
		if err == nil {
			return value, true
		}
	}
	return 0, false
}

// hasAccountData 判断解析结果里是否有任何一项账号数据。
// 一个字段都没有，说明拿到的是登录页或错误页；只要有一项就算成功 ——
// 只缺做种数这类小差异不该把整站判失败。
func (p parsedDetail) hasAccountData() bool {
	profile := p.Profile
	return profile.Uploaded > 0 || profile.Downloaded > 0 || profile.Bonus > 0 ||
		profile.SeedBonus > 0 || profile.Seeding > 0 || profile.Leeching > 0 ||
		profile.UserClass != "" || profile.JoinDate != ""
}

// tradToSimpLabel 是「繁体标签 → 简体」的单字映射表。
//
// 为什么需要：港台皮肤（蝶粉 discfan、PTZone 等）的资料表与页头用繁体写标签，
// 「上傳量 / 下載量 / 做種積分」跟别名表里的「上传量 / 下载量 / 做种积分」一个字都对不上，
// 结果这几站的流量与做种积分永远是 0，而分享率（繁简同形）、魔力值、做种数却能正常取到。
//
// 只做字符级简化、不做词组匹配：标签本身很短，逐字替换不会产生歧义，
// 而且能一次性覆盖「上傳/下載/做種積分/等級/邀請/加入日期/註冊時間」等所有别名。
// 规则只管标签，值仍走原始 HTML（体积单位、数字不受影响）。
var tradToSimpLabel = strings.NewReplacer(
	"傳", "传", "載", "载", "種", "种", "積", "积", "級", "级", "戶", "户",
	"請", "请", "註", "注", "冊", "册", "時", "时", "間", "间", "數", "数",
	"個", "个", "總", "总", "發", "发", "佈", "布", "幣", "币", "獎", "奖",
	"額", "额", "費", "费", "帳", "帐", "賬", "账", "號", "号", "稱", "称",
	"顯", "显", "隱", "隐", "檢", "检", "視", "视", "統", "统", "計", "计",
	"連", "连", "續", "续", "會", "会", "員", "员", "類", "类", "別", "别",
	"輸", "输", "網", "网", "頁", "页", "點", "点", "擊", "击", "開", "开",
	"關", "关", "閉", "闭", "無", "无", "為", "为", "後", "后", "實", "实",
	"際", "际", "錄", "录", "歷", "历", "當", "当", "現", "现", "狀", "状",
	"態", "态", "動", "动", "價", "价", "論", "论", "壇", "坛", "籤", "签",
	"優", "优", "質", "质", "選", "选", "擇", "择", "對", "对", "應", "应",
	"處", "处", "區", "区", "圍", "围", "場", "场", "環", "环", "體", "体",
	"簡", "简", "單", "单", "雙", "双", "贈", "赠", "賺", "赚", "錢", "钱",
	"於", "于", "與", "与", "這", "这", "們", "们", "樣", "样", "麼", "么",
	"試", "试", "證", "证", "驗", "验", "詳", "详", "細", "细", "訊", "讯",
)

// normalizeLabel 归一化标签文本：去标签、繁转简、去空白与中英文冒号、转小写。
func normalizeLabel(value string) string {
	value = plainText(value)
	value = tradToSimpLabel.Replace(value)
	replacer := strings.NewReplacer(" ", "", "\t", "", "\n", "", "\r", "", "：", "", ":", "", "　", "")
	return strings.ToLower(replacer.Replace(value))
}

// plainText 把 HTML 片段转成单行纯文本（去脚本样式、去标签、解实体、压缩空白）。
func plainText(fragment string) string {
	text := reScriptStyle.ReplaceAllString(fragment, " ")
	text = reTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")
	text = stripIconGlyphs(text)
	return strings.Join(strings.Fields(text), " ")
}

// stripIconGlyphs 去掉图标字体的私有区码位（`&#xe902;` 这类）。
//
// 剥掉 `<i class="icon-rank">` 之后，里面的私有区字符仍是文本，
// 会把用户名污染成 `englaile \ue902`（梓喵页头就挂了排名图标）。
// 私有区码位不含任何可读信息，直接丢弃。
func stripIconGlyphs(value string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 0xE000 && r <= 0xF8FF) || (r >= 0xF0000 && r <= 0x10FFFD) {
			return -1
		}
		return r
	}, value)
}

// 块级标签与表格单元格标签：前者换行、后者换空格，保证 FAQ/rules 的段落结构不被压扁。
var (
	reBlockBreak = regexp.MustCompile(`(?is)<\s*/?\s*(?:br|p|div|tr|li|ul|ol|h[1-6]|table|tbody|dl|dt|dd)\b[^>]*>`)
	reCellSpace  = regexp.MustCompile(`(?is)<\s*/?\s*(?:td|th)\b[^>]*>`)
)

// htmlToTextWithBreaks 把整页 HTML 转成保留换行的纯文本。
// 参数/返回：fragment 为 HTML；返回按块级标签分行、行内空白压缩后的文本。
// 失败场景：无。
// 副作用：无。
//
// 用途：FAQ / rules 的等级说明靠「一行一级」的结构来切段，不能像资料表那样压成单行。
func htmlToTextWithBreaks(fragment string) string {
	text := reScriptStyle.ReplaceAllString(fragment, " ")
	text = reBlockBreak.ReplaceAllString(text, "\n")
	text = reCellSpace.ReplaceAllString(text, " ")
	text = reTag.ReplaceAllString(text, " ")
	text = html.UnescapeString(text)
	text = strings.ReplaceAll(text, "\u00a0", " ")

	lines := make([]string, 0, 128)
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.Join(strings.Fields(line), " ")
		if trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	return strings.Join(lines, "\n")
}

// parseSizeBytes 解析「1.234 TB」「22.53<br />GB」「1,234.5 GiB」这类体积文本为字节数。
// 参数/返回：text 为纯文本或含标签的片段；返回字节数与是否解析成功。
// 失败场景：无单位或无数字时返回 (0,false)。
// 副作用：无。
func parseSizeBytes(text string) (int64, bool) {
	clean := plainText(text)
	if clean == "" {
		return 0, false
	}
	lower := strings.ToLower(clean)
	if strings.Contains(lower, "unlimited") || strings.Contains(clean, "无限") {
		return 0, true
	}
	match := reSizeValue.FindStringSubmatch(clean)
	if len(match) < 3 {
		return 0, false
	}
	number, ok := parseLooseFloat(match[1])
	if !ok {
		return 0, false
	}
	unit := strings.ToUpper(strings.ReplaceAll(match[2], "i", ""))
	multiplier := float64(1)
	switch unit {
	case "KB":
		multiplier = 1024
	case "MB":
		multiplier = 1024 * 1024
	case "GB":
		multiplier = 1024 * 1024 * 1024
	case "TB":
		multiplier = 1024 * 1024 * 1024 * 1024
	case "PB":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024
	case "EB":
		multiplier = 1024 * 1024 * 1024 * 1024 * 1024 * 1024
	}
	return int64(number * multiplier), true
}

// abbreviationFactors 是大数字缩写后缀的倍数（站点把 606,714.6 显示成「606.7K」）。
// 只在「纯数字 + 单个后缀字符」时生效：像「160.389 TB」这种带单位的文本，
// 去掉 B 后还剩 T，不满足纯数字形态，不会被误当成倍数。
var abbreviationFactors = map[string]float64{
	"k": 1e3, "m": 1e6, "b": 1e9,
	"千": 1e3, "万": 1e4, "萬": 1e4, "亿": 1e8, "億": 1e8,
}

// reNumericBody 匹配纯数字串（允许千分位逗号与小数点）。
var reNumericBody = regexp.MustCompile(`^-?\d[\d.,]*$`)

// stripAbbreviationSuffix 拆出「606.7K」里的数字与倍数。
// 参数/返回：raw 为去空格后的文本；返回数字部分、倍数与是否命中。
// 失败场景：后缀不认识或数字部分不是纯数字时返回 (raw, 1, false)。
// 副作用：无。
func stripAbbreviationSuffix(raw string) (string, float64, bool) {
	runes := []rune(raw)
	if len(runes) < 2 {
		return raw, 1, false
	}
	factor, ok := abbreviationFactors[strings.ToLower(string(runes[len(runes)-1]))]
	if !ok {
		return raw, 1, false
	}
	head := string(runes[:len(runes)-1])
	if !reNumericBody.MatchString(head) {
		return raw, 1, false
	}
	return head, factor, true
}

// parseLooseFloat 解析可能带千分位逗号的数字（1,234.5 / 1.234 / 3,4），
// 也认「606.7K / 1.2M / 3.4万」这类缩写（乘回倍数）。
func parseLooseFloat(text string) (float64, bool) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return 0, false
	}
	raw = strings.ReplaceAll(raw, " ", "")
	multiplier := 1.0
	if stripped, factor, hit := stripAbbreviationSuffix(raw); hit {
		raw, multiplier = stripped, factor
	}
	if strings.Contains(raw, ",") && strings.Contains(raw, ".") {
		raw = strings.ReplaceAll(raw, ",", "")
	} else if strings.Contains(raw, ",") {
		parts := strings.Split(raw, ",")
		if len(parts) > 1 && len(parts[len(parts)-1]) == 3 && len(raw) > 4 {
			raw = strings.Join(parts, "")
		} else {
			raw = strings.ReplaceAll(raw, ",", ".")
		}
	}
	match := reFloat.FindString(raw)
	if match == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match, ",", ""), 64)
	if err != nil {
		return 0, false
	}
	return value * multiplier, true
}

// parseRatio 解析分享率：支持 ∞ / Unlimited（返回 -1 表示无限）与普通小数。
func parseRatio(text string) (float64, bool) {
	clean := plainText(text)
	lower := strings.ToLower(clean)
	if strings.Contains(lower, "unlimited") || strings.Contains(clean, "∞") || strings.Contains(clean, "无限") {
		return -1, true
	}
	return parseLooseFloat(clean)
}

// parseInteger 解析做种数/邀请数这类整数，容忍「12 个」「(12)」「1,234」。
func parseInteger(text string) (int64, bool) {
	clean := plainText(text)
	match := reDigits.FindString(strings.ReplaceAll(clean, ",", ""))
	if match == "" {
		return 0, false
	}
	value, err := strconv.ParseInt(match, 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// extractUserLevel 从页面里提取用户等级。
// 取用户等级图标的 title/alt（NexusPHP 标准皮肤），退化到 class="User_Xxx" 文本，
// 最后退回资料表里的「等级」行。
func extractUserLevel(doc string, cells *cellSet) string {
	if tag := reUserLevel.FindString(doc); tag != "" {
		if value := attrValue(tag, "title"); value != "" && looksLikeLevelName(value) {
			return value
		}
		if value := attrValue(tag, "alt"); value != "" && looksLikeLevelName(value) {
			return value
		}
	}
	if match := reUserLevel.FindStringSubmatch(doc); len(match) > 0 {
		if class := attrValue(match[0], "class"); class != "" {
			trimmed := strings.TrimSpace(strings.ReplaceAll(class, "userlevel", ""))
			if looksLikeLevelName(trimmed) {
				return trimmed
			}
		}
	}
	if _, value := cells.pick(labelClass); value != "" {
		// 等级格里常只有一张图标：`<img alt="贵宾" title="贵宾" src="pic/vip.gif">`（天空、城市等），
		// 剥完标签是空字符串，必须先读 alt/title，否则整站等级都是空的。
		if name := imageLabelName(value); name != "" {
			return name
		}
		if text := plainText(value); looksLikeLevelName(text) {
			return text
		}
	}
	// 有些皮肤的等级根本不成行，只体现在「用户名按等级着色」的 class 上
	// （蝶粉 / 杜比 / PTzone / 萝莉 都是 `class="VeteranUser_Name"`），资料表里没有「等级」标签。
	if name := userLevelFromNameClass(doc); name != "" {
		return name
	}
	// 等级徽章：人人把当前等级写成 `<span class="ud-class"><b class="VeteranUser_Name">`。
	if name := userLevelFromBadgeTag(doc); name != "" {
		return name
	}
	// 页头的等级小图标（城市 `pic/class/5.gif` 只有 alt="InsaneAngel"）。
	if name := userLevelFromClassIcon(doc); name != "" {
		return name
	}
	// 人人这类卡片皮肤把等级写在属性里：`data-uploader-badge="(杰出公民)Veteran User"`。
	if name := userLevelFromBadgeAttr(doc); name != "" {
		return name
	}
	return ""
}

// reLevelNameClass 匹配 class 串里的等级标记（`VeteranUser_Name` 取 `VeteranUser`）。
//
// 必须大小写敏感：页面里还有 `class="...site-userbar__name"` 这类全小写的布局类名。
var reLevelNameClass = regexp.MustCompile(`\b([A-Z][A-Za-z]{1,24})_Name\b`)

// reAnchorTag 匹配一个完整的 <a ...> 开标签（用于读 href/class，不关心闭合）。
var reAnchorTag = regexp.MustCompile(`(?is)<a\b[^>]*>`)

// reBoldClassTag 匹配带 class 的 <b> 开标签（等级徽章：`<b class="VeteranUser_Name">`）。
var reBoldClassTag = regexp.MustCompile(`(?is)<b\b[^>]*class\s*=\s*["'][^"']*["'][^>]*>`)

// levelNameClassIgnored 是长得像等级却不是等级的 class 前缀。
// Rainbow 是彩虹用户名的配色类（Audiences 等站用 `Rainbow_Name` 给用户名上彩虹色）。
var levelNameClassIgnored = []string{"Rainbow"}

// levelNameFromClassName 从 class 串里取出等级名（`VeteranUser_Name` → `Veteran User`）。
func levelNameFromClassName(class string) string {
	for _, match := range reLevelNameClass.FindAllStringSubmatch(class, -1) {
		raw := strings.TrimSpace(match[1])
		if raw == "" || isIgnoredLevelClass(raw) {
			continue
		}
		if name := splitLevelCamelCase(raw); looksLikeLevelName(name) {
			return name
		}
	}
	return ""
}

// isIgnoredLevelClass 判断 class 后缀是否是「不是等级」的别名（彩虹 / 版务身份等）。
func isIgnoredLevelClass(raw string) bool {
	for _, prefix := range levelNameClassIgnored {
		if strings.EqualFold(raw, prefix) {
			return true
		}
	}
	return false
}

// userLevelFromNameClass 从页头「当前用户」链接的等级 class 反推等级名。
// 参数/返回：doc 为整页 HTML；返回拆分后的等级名（`VeteranUser` → `Veteran User`），无命中返回空串。
// 失败场景：无。
// 副作用：无。
//
// ⚠️ 只看**第一个** userdetails 链接（页头「欢迎回来，<a class="VeteranUser_Name">」就是它），
// 拿到就停：页面里后面的 userdetails 链接是评论者、邀请人、感谢者，取他们的 class 会把
// 等级标成别人的（城市 hdcity 的 `<font class="InsaneAngel_Name">mss</font>` 就是邀请人）。
func userLevelFromNameClass(doc string) string {
	for _, tag := range reAnchorTag.FindAllString(doc, -1) {
		if !strings.Contains(attrValue(tag, "href"), "userdetails.php") {
			continue
		}
		// 第一个「当前用户」链接就定了：它的 class 不是等级（彩虹名 / citizen 之类）也不往后找，
		// 交给徽章 / 图标那几条兜底。
		return levelNameFromClassName(attrValue(tag, "class"))
	}
	return ""
}

// userLevelFromBadgeTag 从等级徽章的 `<b class="VeteranUser_Name">` 取等级。
// 参数/返回：doc 为整页 HTML；返回等级名，无命中返回空串。
// 失败场景：无。
// 副作用：无。
//
// 人人（Audiences）的资料页把当前等级画成 `<span class="ud-class"><b class="VeteranUser_Name">`，
// 它既不是资料表的「等级」行，也不在用户名链接上。
func userLevelFromBadgeTag(doc string) string {
	for _, tag := range reBoldClassTag.FindAllString(doc, -1) {
		if name := levelNameFromClassName(attrValue(tag, "class")); name != "" {
			return name
		}
	}
	return ""
}

// reClassIcon 匹配 NexusPHP 的等级小图标（`<img src="pic/class/5.gif" alt="InsaneAngel">`）。
var reClassIcon = regexp.MustCompile(`(?is)<img\b[^>]*src\s*=\s*["'][^"']*/class/[^"']*["'][^>]*>`)

// userLevelFromClassIcon 从页头等级图标取等级名。
// 参数/返回：doc 为整页 HTML；返回图标 alt/title 里的等级名，无命中返回空串。
// 失败场景：无。
// 副作用：无。
//
// 城市（hdcity）的资料页没有「等级」行、也没有用户名 class，只有这张 `pic/class/N.gif`
// 小图标，alt 就是等级（InsaneAngel）。
func userLevelFromClassIcon(doc string) string {
	for _, tag := range reClassIcon.FindAllString(doc, -1) {
		for _, attr := range []string{"alt", "title"} {
			value := attrValue(tag, attr)
			if value == "" || !looksLikeLevelName(value) || isLevelIconNoise(value) {
				continue
			}
			return splitLevelCamelCase(value)
		}
	}
	return ""
}

// splitLevelCamelCase 把 `VeteranUser` / `NexusMaster` / `VeteranAngel` 还原成带空格的等级名。
// 参数/返回：value 为驼峰串；返回拆好空格的文本（已是普通词时原样返回）。
// 失败场景：无。
// 副作用：无。
func splitLevelCamelCase(value string) string {
	var builder strings.Builder
	for index, r := range value {
		if index > 0 && r >= 'A' && r <= 'Z' {
			builder.WriteByte(' ')
		}
		builder.WriteRune(r)
	}
	return strings.TrimSpace(builder.String())
}

// reUserBadgeAttr 匹配卡片皮肤把等级写进属性的写法（Audiences：`data-uploader-badge="(杰出公民)Veteran User"`）。
var reUserBadgeAttr = regexp.MustCompile(`(?is)data-(?:uploader|user|member)-(?:badge|class|level|rank)\s*=\s*["']([^"']{1,48})["']`)

// userLevelFromBadgeAttr 从 data-* 属性里取等级名。
// 参数/返回：doc 为整页 HTML；返回属性里的等级名，无命中返回空串。
// 失败场景：无。
// 副作用：无。
func userLevelFromBadgeAttr(doc string) string {
	for _, match := range reUserBadgeAttr.FindAllStringSubmatch(doc, -1) {
		value := strings.TrimSpace(html.UnescapeString(match[1]))
		if value != "" && looksLikeLevelName(value) {
			return value
		}
	}
	return ""
}

// reImageTag 匹配资料页里的小图标（等级、勋章等）。
var reImageTag = regexp.MustCompile(`(?is)<img\b([^>]*)>`)

// levelIconNoise 是等级格里混进来的其它小图标（性别、在线状态、勋章、赞助等）。
var levelIconNoise = []string{
	"男", "女", "在线", "离线", "勋章", "奖章", "邮箱", "短信", "pm",
	"赞助", "捐赠", "注册", "最后访问", "分享率", "积分", "上传", "下载",
}

// imageLabelName 从一段 HTML 片段里的 <img> 取 alt/title 作为等级名。
// 天空/城市的等级格只有一张图片（`<img alt="贵宾" title="贵宾" src="pic/vip.gif">`），
// 同一格里还常有性别、在线状态等图标，按 levelIconNoise 排除掉。
func imageLabelName(fragment string) string {
	fallback := ""
	for _, match := range reImageTag.FindAllStringSubmatch(fragment, -1) {
		for _, attr := range []string{"alt", "title"} {
			value := attrValue(match[0], attr)
			if value == "" || !looksLikeLevelName(value) {
				continue
			}
			if isLevelIconNoise(value) {
				continue
			}
			// 等级图标一般带 src=.../vip.gif、userlevel_xxx.png 这类特征，优先认它。
			if strings.Contains(strings.ToLower(attrValue(match[0], "src")), "level") {
				return value
			}
			if fallback == "" {
				fallback = value
			}
		}
	}
	return fallback
}

// isLevelIconNoise 判断图标文案是否属于性别/状态/勋章等非等级信息。
func isLevelIconNoise(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, noise := range levelIconNoise {
		if strings.Contains(lower, noise) {
			return true
		}
	}
	return false
}

// levelNameNoise 是「等级」格里混进来的操作入口/说明文案。
var levelNameNoise = []string{"详情", "提升", "查看", "申请", "说明", "升级", "降级", "权限", "结束时间", "有效期"}

// looksLikeLevelName 过滤掉「等级详情/提升等级」「贵宾资格结束时间: 2025-11-27」这类 UI 文本，
// 它们出现在等级图标 title 或等级格里，但不是等级名。
func looksLikeLevelName(value string) bool {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return false
	}
	for _, noise := range levelNameNoise {
		if strings.Contains(clean, noise) {
			return false
		}
	}
	return !strings.ContainsAny(clean, ":：")
}

// attrValue 从单个标签字符串里取属性值（兼容单双引号、带空格的值与无引号写法）。
func attrValue(tag, name string) string {
	pattern := regexp.MustCompile(`(?is)\b` + regexp.QuoteMeta(name) + `\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))`)
	match := pattern.FindStringSubmatch(tag)
	if len(match) < 2 {
		return ""
	}
	for _, candidate := range match[1:] {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(html.UnescapeString(candidate))
		}
	}
	return ""
}

// reUserLink 匹配 NexusPHP 页头用户链接，用于在资料页缺失时兜底取用户 ID/用户名。
var reUserLink = regexp.MustCompile(`(?is)<a[^>]*\bhref\s*=\s*["'][^"']*userdetails\.php\?[^"']*\bid=(\d+)[^"']*["'][^>]*>(.*?)</a>`)

// 页头「自己的用户名」链接：`<a href="userdetails.php?id=10820" class='User_Name'>名字</a>`。
// class 与 href 的前后顺序各站不同，两种顺序都试。
var (
	reHeaderUserAfter  = regexp.MustCompile(`(?is)<a\b[^>]*userdetails\.php\?[^>]*_Name[^>]*>(.*?)</a>`)
	reHeaderUserBefore = regexp.MustCompile(`(?is)<a\b[^>]*_Name[^>]*userdetails\.php\?[^>]*>(.*?)</a>`)
)

// headerUserName 从页头用户名链接里取自己的显示名。
// 参数/返回：doc 为整页 HTML；返回折叠后的用户名（取不到返回空串）。
// 失败场景：页头没有带等级 class 的用户链接时返回空串。
// 副作用：无。
//
// 用途：已知 uid 时不再请求 index.php，而部分站（梓喵）资料表里没有「用户名」行，
// 只能从页头 `欢迎回来 / 用户名` 链接倒推。
func headerUserName(doc string) string {
	for _, pattern := range []*regexp.Regexp{reHeaderUserAfter, reHeaderUserBefore} {
		match := pattern.FindStringSubmatch(doc)
		if len(match) < 2 {
			continue
		}
		if name := plainText(match[1]); name != "" {
			return name
		}
	}
	return ""
}

// extractUserFromIndex 从站点首页 HTML 中提取当前登录用户的 ID 与用户名。
// 参数/返回：doc 为首页 HTML；返回用户 ID 与用户名（可能为空）。
// 失败场景：未登录/页面结构不符时返回空串。
// 副作用：无。
func extractUserFromIndex(doc string) (userID, username string) {
	if match := reUserLink.FindStringSubmatch(doc); len(match) > 2 {
		return strings.TrimSpace(match[1]), plainText(match[2])
	}
	return "", ""
}

// countTorrentRows 统计种子列表 HTML 里的种子行数。
// NexusPHP 的 getusertorrentlistajax.php 每行一个 details.php?id= 链接，按出现次数计数。
func countTorrentRows(doc string) int64 {
	return int64(strings.Count(strings.ToLower(doc), "details.php?id="))
}

// reTorrentListSummary 匹配「<b>100</b> 条记录」这类种子列表摘要（繁简都要认，港台皮肤写「筆記錄」）。
var reTorrentListSummary = regexp.MustCompile(`(\d+)\s*(?:条|條|笔|筆)\s*(?:记|記)\s*(?:录|錄)`)

// parseTorrentListSummary 从列表摘要里取记录数。
// 参数/返回：text 为接口返回的 HTML 片段；返回记录数与是否命中。
// 失败场景：不是摘要格式（完整种子列表或错误页）时返回 false。
// 副作用：无。
//
// 梓喵（azusa）的 getusertorrentlist_ajax.php 只回一行摘要
// （`<b>100</b> 条记录 | 总大小：300.19 GB <a>查看列表</a>`），没有种子行可数。
func parseTorrentListSummary(text string) (int64, bool) {
	match := reTorrentListSummary.FindStringSubmatch(plainText(text))
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
