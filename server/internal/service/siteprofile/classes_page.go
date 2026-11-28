package siteprofile

import (
	"html"
	"regexp"
	"sort"
	"strings"
)

// classes.php 是较新版本 NexusPHP 的独立「等级」页（北洋园 tjupt 的 /classes.php#6，
// 页头「等级」链接就指向这里）。和 FAQ 的「提升和降级」段落相比它有两个关键差别：
//
//  1. FAQ 里那段已经标注「以下为之前的规则，新的升级规则已开始测试运行」，是**旧门槛**；
//     FAQ 表本身也不带升级条件（三列只剩权限），于是解析出来 requirements 全空。
//  2. classes.php 把每一级的门槛逐条列成「当前值/目标值」对（`注册周数：59/48`、
//     `上传量（GiB）：8585.97/5000`），并直接用 `color: red/green` 标出是否达成，
//     还带 `永久保留账号` 这类保号标记 —— 比任何文本推断都准。
//
// 所以只要 classes.php 能解出 ≥3 个等级，就用它替换 FAQ 的结果。
var (
	// reClassesRow 匹配一个等级行：`<tr id="6" class='active_class'>…</tr>`。
	// 注意「当前等级以上」的那些行没有 class 属性（只写 `<tr id="7">`），
	// 所以不能要求 class 存在，否则高级别全被丢掉。
	reClassesRow = regexp.MustCompile(`(?is)<tr\b[^>]*\bid=["']?(\d+)["']?[^>]*>(.*?)</tr>`)
	// reClassesName 取等级行第一列里的名称 span（`<span class='VeteranUser_Name'>威震一方</span>`）。
	reClassesName = regexp.MustCompile(`(?is)<span\b[^>]*>([^<]{1,60})</span>`)
	// reClassesItem 取要求/权限列里的列表项。
	reClassesItem = regexp.MustCompile(`(?is)<li\b[^>]*>(.*?)</li>`)
	// reClassesMember 取「目前人数：2810人」。
	reClassesMember = regexp.MustCompile(`目前人数[：:]\s*(\d+)\s*人`)
)

// classesRow 是 classes.php 里的一行（一级）。
type classesRow struct {
	id        int
	name      string
	require   []string
	privilege []string
	status    string
	members   string
}

// parseClassesPage 解析 classes.php 的等级表；结构不符（非该页面/权限不足）时返回 nil。
// 参数/返回：html 为 classes.php 的原始 HTML；返回按等级从低到高排序的等级列表。
// 失败场景：解析出的等级不足 3 个时返回 nil，由调用方退回 FAQ 解析。
// 副作用：无。
func parseClassesPage(html string) []UserLevel {
	if strings.TrimSpace(html) == "" {
		return nil
	}
	matches := reClassesRow.FindAllStringSubmatch(html, -1)
	if len(matches) < 3 {
		return nil
	}
	rows := make([]classesRow, 0, len(matches))
	for _, match := range matches {
		body := match[2]
		cells := reTD.FindAllStringSubmatch(body, -1)
		if len(cells) < 3 {
			continue
		}
		// reTD 的第 1 个分组是属性，第 2 个是单元格内容。
		cellTexts := make([]string, 0, len(cells))
		for _, cell := range cells {
			if len(cell) < 3 {
				continue
			}
			cellTexts = append(cellTexts, cell[2])
		}
		if len(cellTexts) < 3 {
			continue
		}
		// 等级行的第一列一定有「目前人数：N人」（皮肤改名时退化为 `_Name` 的 span class），
		// 其它带 id 的表格行没有这两个特征，直接跳过。
		if !reClassesMember.MatchString(cellTexts[0]) && !strings.Contains(cellTexts[0], "_Name") {
			continue
		}
		name, members := parseClassesName(cellTexts[0])
		if name == "" {
			continue
		}
		row := classesRow{
			id:        atoiSafe(match[1]),
			name:      name,
			require:   listItems(cellTexts[1]),
			privilege: listItems(cellTexts[2]),
			members:   members,
		}
		if len(cellTexts) > 3 {
			row.status = plainText(cellTexts[3])
		}
		// 没有列表项时（「初始等级，欢迎来到北洋园PT」这类整段说明）退化为整列文本。
		if len(row.require) == 0 {
			if text := plainText(cellTexts[1]); text != "" {
				row.require = []string{text}
			}
		}
		if len(row.privilege) == 0 {
			if text := plainText(cellTexts[2]); text != "" {
				row.privilege = []string{text}
			}
		}
		rows = append(rows, row)
	}
	if len(rows) < 3 {
		return nil
	}
	// 表格从最高等级往下排（id 越大等级越高），按 id 升序还原成「由低到高」。
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].id < rows[j].id })

	levels := make([]UserLevel, 0, len(rows))
	for index, row := range rows {
		level := UserLevel{
			Name:         row.name,
			Order:        index,
			Requirements: truncateText(cleanRequirement(strings.Join(row.require, "\n")), 420),
			Privileges:   truncateText(strings.Join(row.privilege, "\n"), 300),
			// 保号只认这一级权限里明确写的「永久保留账号」（`持剑下山` 的
			// 「封存账号后不会被删除」是封存豁免线，不是保号线）。
			KeepAlive: hasKeepAliveMark(row.privilege),
		}
		if row.members != "" {
			level.Privileges = strings.TrimSpace(level.Privileges + "\n目前人数：" + row.members + "人")
		}
		levels = append(levels, level)
	}
	return levels
}

// parseClassesName 从等级行第一列取出「中文名(英文名)」与人数。
// 第一列结构：图标 `<img alt="VeteranUser">` + 中文名 span + 英文名 span + 「目前人数：2810人」。
func parseClassesName(cell string) (string, string) {
	members := ""
	if match := reClassesMember.FindStringSubmatch(cell); len(match) > 1 {
		members = match[1]
	}
	cn := ""
	en := ""
	for _, match := range reClassesName.FindAllStringSubmatch(cell, -1) {
		text := strings.TrimSpace(html.UnescapeString(match[1]))
		if text == "" || strings.Contains(text, "目前人数") {
			continue
		}
		if looksLatinName(text) {
			if en == "" {
				en = text
			}
			continue
		}
		if cn == "" {
			cn = text
		}
	}
	switch {
	case cn != "" && en != "":
		return cn + "(" + en + ")", members
	case cn != "":
		return cn, members
	case en != "":
		return en, members
	}
	return "", members
}

// looksLatinName 判断名称是否以拉丁字母开头（英文等级名，如 `Veteran User`）。
func looksLatinName(text string) bool {
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
			return true
		case r == ' ' || r == '(' || r == '（':
			continue
		default:
			return false
		}
	}
	return false
}

// listItems 取一段 HTML 里所有 `<li>` 的纯文本（去掉删除线的「已获得」条目）。
func listItems(fragment string) []string {
	items := make([]string, 0, 8)
	for _, match := range reClassesItem.FindAllStringSubmatch(fragment, -1) {
		body := match[1]
		// `<s>首次升级至此等级时将获得1个永久邀请</s>` 是「已拿过」的划线项，不算要求/权限。
		if strings.Contains(strings.ToLower(body), "<s>") || strings.Contains(strings.ToLower(body), "color: gray") {
			continue
		}
		text := plainText(body)
		if text == "" {
			continue
		}
		items = append(items, text)
	}
	return items
}

// hasKeepAliveMark 判断权限列表里是否有保号标记。
func hasKeepAliveMark(privileges []string) bool {
	for _, item := range privileges {
		if strings.Contains(item, "永久保留") || strings.Contains(item, "永久保留账号") ||
			strings.Contains(item, "永久保留帳號") || strings.Contains(item, "永远保留") {
			return true
		}
	}
	return false
}

// atoiSafe 把纯数字字符串转成 int，失败返回 0。
func atoiSafe(raw string) int {
	value := 0
	for _, r := range strings.TrimSpace(raw) {
		if r < '0' || r > '9' {
			return 0
		}
		value = value*10 + int(r-'0')
	}
	return value
}
