package siteprofile

import (
	"encoding/json"
	"html"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// 本文件实现「站点用户等级体系」的获取：每个等级有哪些权限、升级条件是什么、哪一级是保号线。
//
// 数据来源与优先级（参考 PTPP 的 levelName + 站点扩展信息的做法）：
//  1. 站点 faq.php —— NexusPHP 标准 FAQ 里就有「不同的用户等级代表了什么含义」整段说明，
//     逐级列出该等级的权限（这也是各站自定义等级后唯一能拿到的权威文案）；
//  2. 站点 rules.php —— 「用户升级和降级规则」段落，逐级列出注册时长/做种积分/下载量/分享率门槛；
//  3. 内置 NexusPHP 标准等级表 —— 站点抓不到时兜底，同时提供标准权限文案与默认保号线（Elite User）。
//
// 保号线（keep_alive）的判定：以 FAQ「权限」列写的「X 及以上用户会永远保留账号」为准，X 即保号线；
// 某级权限直接写「保留帐号 / 账号永久保留」也算该级。「Y 及以上用户封存账号后不会被删除 / 不会被禁用」
// 是封存豁免（不封存照样清理），不算保号。站点没写就不标，不用 NexusPHP 的 Elite User 标准值兜底。

// UserLevel 表示站点的一个用户等级及其权限/门槛。
type UserLevel struct {
	Name         string `json:"name"`
	Order        int    `json:"order"`
	Requirements string `json:"requirements"`
	Privileges   string `json:"privileges"`
	// KeepAlive 标记「保号等级」——起点取「X 及以上用户会永远保留账号」的 X，
	// 或该级权限直接写「保留帐号 / 账号永久保留」的那一级，前端用蓝色标出。
	KeepAlive bool `json:"keep_alive"`
}

// levelCanonicalNames 是 NexusPHP 标准等级序列（含特殊/管理组之前的部分）。
// 顺序即数值等级（0..9），站点抓取结果按出现顺序与之对齐。
var levelCanonicalNames = []string{
	"Peasant",
	"User",
	"Power User",
	"Elite User",
	"Crazy User",
	"Insane User",
	"Veteran User",
	"Extreme User",
	"Ultimate User",
	"Nexus Master",
}

// levelNameTokens 是 NexusPHP 全部等级/用户组名（含 VIP、发布员、版主等特殊组），
// 用于识别「没有 _Name class」的等级表格：憨憨这类自研皮肤把等级写成 `<td>脸憨皮厚 Peasant</td>`，
// 只能靠名字里出现的英文等级名判断这一行是不是等级行。长名在前，避免 `User` 抢先命中。
var levelNameTokens = []string{
	"Peasant",
	"Power User",
	"Elite User",
	"Crazy User",
	"Insane User",
	"Veteran User",
	"Extreme User",
	"Ultimate User",
	"Nexus Master",
	"Staff Leader",
	"Administrator",
	"Moderator",
	"Retiree",
	"Uploader",
	"Honorary",
	"Sysop",
	"VIP",
	"User",
}

// defaultUserLevels 返回 NexusPHP 标准等级表（升级条件为官方默认值，多数站点会自行调整）。
// 参数/返回：无参数；返回按 order 升序的等级列表。
// 失败场景：无。
// 副作用：无（每次返回新切片）。
func defaultUserLevels() []UserLevel {
	return []UserLevel{
		{
			Name:         "Peasant",
			Order:        0,
			Requirements: "被降级用户；须在规定时间内提升分享率，否则会被封禁",
			Privileges:   "不能发表趣味盒内容、不能申请友情链接、不能上传字幕",
		},
		{
			Name:         "User",
			Order:        1,
			Requirements: "注册后的默认等级",
			Privileges:   "可以上传字幕；可参与常规下载与做种",
		},
		{
			Name:         "Power User",
			Order:        2,
			Requirements: "注册 ≥ 4 周，做种积分 ≥ 14888，下载量 ≥ 50GB，分享率 ≥ 1.05",
			Privileges:   "查看 NFO 文件、请求续种、购买/发送邀请、删除自己上传的字幕、申请友情链接、查看 Top 10 与他人种子历史",
		},
		{
			Name:         "Elite User",
			Order:        3,
			Requirements: "注册 ≥ 8 周，做种积分 ≥ 29888，下载量 ≥ 120GB，分享率 ≥ 1.55",
			Privileges:   "查看种子结构、更新外部信息",
		},
		{
			Name:         "Crazy User",
			Order:        4,
			Requirements: "注册 ≥ 15 周，做种积分 ≥ 59888，下载量 ≥ 300GB，分享率 ≥ 2.05",
			Privileges:   "做种/下载/发布时可选择匿名；首次到达获得 2 个邀请名额",
		},
		{
			Name:         "Insane User",
			Order:        5,
			Requirements: "注册 ≥ 25 周，做种积分 ≥ 118888，下载量 ≥ 500GB，分享率 ≥ 2.55",
			Privileges:   "查看排行榜、查看常规日志",
		},
		{
			Name:         "Veteran User",
			Order:        6,
			Requirements: "注册 ≥ 40 周，做种积分 ≥ 236888，下载量 ≥ 750GB，分享率 ≥ 3.05",
			Privileges:   "查看他人种子历史、评论与论坛发帖记录；首次到达获得 3 个邀请名额",
		},
		{
			Name:         "Extreme User",
			Order:        7,
			Requirements: "注册 ≥ 60 周，做种积分 ≥ 468888，下载量 ≥ 1024GB，分享率 ≥ 3.55",
			Privileges:   "更新过期的外部信息、访问 Extreme User 专属版块",
		},
		{
			Name:         "Ultimate User",
			Order:        8,
			Requirements: "注册 ≥ 80 周，做种积分 ≥ 668888，下载量 ≥ 1536GB，分享率 ≥ 4.05",
			Privileges:   "首次到达获得 5 个邀请名额",
		},
		{
			Name:         "Nexus Master",
			Order:        9,
			Requirements: "注册 ≥ 100 周，做种积分 ≥ 1000000，下载量 ≥ 3TB，分享率 ≥ 4.55",
			Privileges:   "获得 10 个邀请名额；为站点捐款的用户；账号永久保留",
		},
	}
}

var (
	// FAQ 里等级说明段落的标题（中英文常见写法）。
	reFAQLevelHeading = regexp.MustCompile(`(?i)(?:what are the different user classes|different user classes|用户等级代表了什么|不同的用户等级|用户等级的含义|用戶等級)`)
	// rules.php 里「升级至 XXX 等级」的门槛说明（英文名与中文别名两种写法都有站点在用）。
	reRulePromote = regexp.MustCompile(`升级至\s*([A-Za-z][A-Za-z ]{1,24}?|[^\s，,。:：；;]{1,20})\s*等级(?:别名\s*[:：]\s*([^\s,，。]+))?\s*[:：]?\s*([^\n]{0,260})`)
	// 保号线索：权限列里「X 及以上用户会永远保留账号」的 X，或某级权限直接写「保留帐号 / 账号永久保留」。
	//
	// ⚠️ 只有「账号永久保留」（永远保留账号 / 账号永久保留 / 永久保留账号）才算保号等级。
	// 「Y 及以上用户封存账号后不会被删除 / 不会被禁用」
	// 是「封存之后才不清理」，不封存照样会被定期清理，不能当保号等级 —— 混判会把保号线压低。
	//
	// ⚠️ 「及以上」必须紧跟在等级名之后（中间只允许空白）。早期版本给等级名配 320 字节窗口，
	// 结果低等级名会把**后面几行**属于更高等级的「及以上…永远保留」算到自己头上，
	// 再取最低命中 ⇒ 保号线整体压低 1~3 档（天空判成 Veteran、彩虹岛判成 Extreme、1ptba 判成 Elite）。
	reAndAbove = regexp.MustCompile(`(?i)^\s*(?:及以上|或以上|以上|and\s+above|and\s+higher|or\s+above)`)
	// 文本里任意位置出现「及以上」句式（用于区分「单级直说保留」与「X 及以上保留」）。
	reAndAboveAnywhere = regexp.MustCompile(`(?i)及以上|或以上|and\s+above|and\s+higher|or\s+above`)
	// 账号永久保留的措辞：站点有两种写法，①「X 及以上用户会永远保留账号」；②该级权限直接写
	// 「保留帐号 / 保留账号 / 账号永久保留 / 永久保留账号」—— 语序两种都算，等价于永久保留。
	reRetainForever = regexp.MustCompile(`(?i)(?:永远保留|永久保留|永遠保留|永久性保留|保留帐号|保留账号|保留帳號|保留賬號|永久保号|永不被删除|永远不被删除|永不删除|永遠不被刪除|永不刪除|never\s+be\s+(?:deleted|removed)|never\s+(?:deleted|removed)|permanently\s+retained|retained\s+forever)`)
	// 否定写法（「不保留 / 不会永久保留 / 不予保留」）——不是保号，是惩罚条款。
	reRetainNegated = regexp.MustCompile(`(?i)(?:不|未|非|勿|沒|没)(?:会|會|予|再|能|得|被)?\s*(?:永久?\s*)?(?:保留|保留帐号|保留账号|保留帳號|保留賬號)|(?:not|never)\s+(?:be\s+)?(?:retained|kept)`)
	// 封存/挂起：这句讲的是「封存之后才不清理」，与「账号永久保留」是两回事。
	reParked = regexp.MustCompile(`(?i)封存|掛起|挂起|(?:if|once|when|after|while)\s+(?:the\s+)?(?:account\s+is\s+|being\s+)?park`)
	// 自指写法「此级别及以上用户会永远保留账号」（ptcafe 这类不重复等级名的站点）。
	reThisLevelAndAbove = regexp.MustCompile(`(?i)(?:此|该|該|本|这|這)(?:级别|級別|等级|等級)\s*(?:及以上|或以上|以上)|(?:this|that)\s+level\s+and\s+above`)
	// 需要清理的条件文本尾部（「的用户，将升级为 XXX。」）。
	reRuleTail = regexp.MustCompile(`(?i)(?:的用户.*|,?\s*将升级为.*)$`)
)

// buildUserLevels 合并内置标准表、FAQ 权限说明与 rules 升级条件，产出该站的等级体系。
// 参数/返回：faqText / rulesText 为已转纯文本的站点页面（可为空）；返回按 order 升序的等级列表。
// 失败场景：无（任何解析失败都退回内置表）。
// 副作用：无。
func buildUserLevels(faqText, rulesText string) []UserLevel {
	levels := defaultUserLevels()

	// 1) FAQ：按等级名锚点切段，拿到每级的权限描述。
	parsed := parseFAQLevelDescriptions(faqText)
	if len(parsed) >= 3 {
		for index := range levels {
			if description, ok := parsed[normalizeLevelName(levels[index].Name)]; ok && description != "" {
				levels[index].Privileges = truncateText(description, 300)
			}
		}
	}

	// 2) rules：逐级覆盖升级门槛（站点实际值优先于标准值）。
	applyRuleRequirements(levels, rulesText)

	// 3) 保号线：站点写「X 及以上用户会永远保留账号」取 X；某级权限直接写
	// 「保留帐号 / 账号永久保留 / 永久保留账号」也取该级（NexusPHP 的 Nexus Master 就是这种写法）。
	// 内置标准表没有 FAQ 时，行内权限仍是官方默认文案，其中 NM 的「账号永久保留」在
	// NexusPHP 里是真实承诺，所以这里照样逐行判定。
	markKeepAliveLevelsFromRows(levels, faqText, rulesText)
	return levels
}

var (
	// FAQ 权限表标题。只匹配标题文字：FAQ 顶部目录里也有同样的标题
	// （`<a href="#id22">不同的用户等级代表了什么含义？</a>`），由 parseLevelTable
	// 判断「标题后不远处是否有能解析出 ≥3 个等级的表格」来排除目录命中。
	// 「有哪些不同的用户类别」是 TTG 的写法（等级名用硬盘容量单位 Byte/KiloByte/…），
	// 只认完整短语而不是裸「用户类别」：裸词会命中其它站点「捐赠用户类别」之类的附属表格。
	reFAQPrivilegeHeading = regexp.MustCompile(`(?is)(?:what are the different user classes|different user classes|用户等级代表了什么|不同的用户等级|用户等级的含义|用户等级规则|账号等级规则|等級規則|账号等级|用戶等級|等级规则|有哪些不同的等级|有哪些等级|用户等级有哪些|用戶等級有哪些|有哪些不同的用户类别|有哪些不同的用戶類別|有哪些用户类别|有哪些用戶類別)`)
	// FAQ 升级条件表标题（第二张表，逐级写「必须注册至少 N 周…」，是站点真实门槛）。
	reFAQPromotionHeading = regexp.MustCompile(`(?is)(?:how do i get promoted|promotion and demotion|提升和降级|升级和降级|如何升级|等級的升降|升(?:级|級)(?:与|和)降(?:级|級))`)
	// 等级表格的一行。
	reTableRow = regexp.MustCompile(`(?is)<tr\b[^>]*>(.*?)</tr>`)
	// 等级名：NexusPHP 标准 FAQ 给每级一个 `<b class="PowerUser_Name">跑龙套(Power User)</b>`。
	// 用 `_Name` 后缀而不是固定名单，站点加了 VIP / 发布员 / 版主等特殊等级也能认出来。
	reLevelBadge = regexp.MustCompile(`(?is)<b\b[^>]*class\s*=\s*["'][^"']*_name[^"']*["'][^>]*>(.*?)</b>`)
	// 等级名里的拉丁字母部分（`(冰人)Peasant` → `Peasant`），用于和 rules.php 的升级条件对齐。
	reLatinName = regexp.MustCompile(`[A-Za-z][A-Za-z ]{1,30}`)
	// 名字里括号包裹的别名（`贵宾(VIP)` 需要额外生成「贵宾」这个键）。
	reBracketPart = regexp.MustCompile(`[（(\[【][^）)\]】]*[）)\]】]`)
	// 等级表里的「分组说明」行（TTG 写 `以下为工作组：`）：这一行之后是工作组名单，
	// 不是用户等级，遇到就结束本表，避免把 WiKi/Admin 这些组别收进等级列表。
	reLevelTableSectionBreak = regexp.MustCompile(`以下(?:为|為)|以下是`)
	// 自研皮肤把等级名放在第一列且只加粗（TTG：`<td …><strong>BrontoByte</strong></td>`），
	// 没有 `_Name` class 也没有标准等级名，需要靠「首列以 strong/b 开头 + 短文本」识别。
	reStrongFirstCell = regexp.MustCompile(`(?is)^\s*<(?:strong|b)\b`)
	// 等级描述里的行内升级条件：TTG 把门槛写在权限后面，`（升级条件：注册满 5 周，…）`。
	reInlineUpgradeCondition = regexp.MustCompile(`[（(]\s*(?:升级|升級)(?:条件|條件)\s*[:：]\s*([^）)]*)[）)]`)
)

// faqLevelSectionBytes 是从 FAQ 标题往后截取的字节数：一张等级表通常几百字节到几万字节，
// 取 14KB 既够覆盖 20 个等级，也不至于把后面的其它 FAQ 段落（如「为什么我连不上」）扫进来。
const faqLevelSectionBytes = 14000

// faqLevelHeadingGap 是「标题文字」到「等级表格」之间允许的最大字节数。
// 400 字节只够跨过 `</b></span><br />`，遇到双语站在标题与表格之间插一段说明
// （不可说 ssd：「提升和降级又是怎样被执行的呢？」后面还有两百多字的中文说明）就找不到表，
// 结果一路跳到英文段落，等级条件全变成英文。900 字节仍远小于相邻表格间距（TOC 到正文表有 9KB），
// 而且取到表后还要解析出 ≥3 个等级行才算命中，不会把别的表当成等级表。
const faqLevelHeadingGap = 900

// levelRow 是 FAQ 等级表里的一行：等级名 + 其余列（已按顺序去掉空单元格）。
// 不同皮肤列数不一样：NexusPHP 是三列（等级 / 空 / 权限），憨憨这类自研皮肤是四列
// （等级 / 升级条件 / 降级条件 / 权限），所以保留全部列，由调用方按用途取。
type levelRow struct {
	Name  string
	Cells []string
}

// lastCell 返回该行最后一个非空列（权限描述一般在最后）。
func (r levelRow) lastCell() string {
	if len(r.Cells) == 0 {
		return ""
	}
	return r.Cells[len(r.Cells)-1]
}

// middleCell 返回除最后一列外的第一个非空列（四列表格里的「升级条件」）。
func (r levelRow) middleCell() string {
	for index := 0; index < len(r.Cells)-1; index++ {
		if strings.TrimSpace(r.Cells[index]) != "" {
			return r.Cells[index]
		}
	}
	return ""
}

// buildUserLevelsFromFAQ 用 FAQ 页面 HTML 里的两张等级表构造站点真实等级体系：
//   - 「不同的用户等级代表了什么含义？」→ 等级列表与权限；
//   - 「提升和降级又是怎样被执行的呢？」→ 逐级升级门槛。
//
// 为什么要读 HTML 而不是纯文本：NexusPHP 标准 FAQ 用三列表格逐级列出等级，
// 纯文本会把十几个等级压成一段，只能靠内置的 10 个标准名去猜，于是 VIP、贵宾、发布员、
// 版主、荣誉会员这些站点自定义等级全部丢失（用户反馈「很多等级没展示出来」）。
// 解析不到表格时（非标准皮肤）退回原来的纯文本 + 内置标准表逻辑。
func buildUserLevelsFromFAQ(faqHTML, rulesText string) []UserLevel {
	faqText := htmlToTextWithBreaks(faqHTML)

	privileges := parseLevelTable(faqHTML, reFAQPrivilegeHeading)
	if len(privileges) < 3 {
		// Markdown 皮肤（梓喵 azusa）：FAQ 正文是被 editormd 隐藏起来的 markdown 原文
		// （`<div id="markdown-xxx-content" style="display:none"># 用户等级\n| 等级 | 权限说明 |…`），
		// 页面上的表格是 JS 渲染出来的，HTML 源码里没有骨架根本没有 <table>，
		// 只能从纯文本里按 markdown 表格的 `| a | b |` 结构解析。
		privileges = parseMarkdownLevelTable(faqText, false)
	}
	if len(privileges) < 3 {
		return buildUserLevels(faqText, rulesText)
	}

	// 四列表格（憨憨）把升级条件放在第 2 列，和权限在同一张表里；
	// 三列标准皮肤这一列为空，升级条件留给下面的「提升和降级」表。
	levels := make([]UserLevel, 0, len(privileges))
	for _, row := range privileges {
		requirement := row.middleCell()
		privilege := row.lastCell()
		// TTG 这类站点没有单独的「提升和降级」表，把门槛写在权限列的行内括号里，
		// 拆出来单独展示，否则整段说明会挤在权限一栏。
		if requirement == "" {
			if inline, remained := splitInlineRequirement(privilege); inline != "" {
				requirement = inline
				privilege = remained
			}
		}
		levels = append(levels, UserLevel{
			Name:         row.Name,
			Order:        len(levels),
			Requirements: truncateText(cleanRequirement(requirement), 260),
			Privileges:   truncateText(privilege, 300),
		})
	}
	// 站点真实门槛优先于内置标准值；rules.php 的「升级至 X 等级」只在没有表格时才是来源。
	promotion := parseLevelTable(faqHTML, reFAQPromotionHeading)
	if len(promotion) == 0 {
		promotion = parseMarkdownLevelTable(faqText, true)
	}
	applyRowRequirements(levels, promotion)
	applyRuleRequirements(levels, rulesText)

	markKeepAliveLevelsFromRows(levels, faqText, rulesText)
	return levels
}

// parseLevelTable 在某个标题后面找第一张解析出 ≥3 行的等级表。
// 参数/返回：htmlText 为 FAQ 原始 HTML，heading 为段落标题正则；解析不到返回 nil。
// 失败场景：无（由调用方兜底）。
// 副作用：无。
func parseLevelTable(htmlText string, heading *regexp.Regexp) []levelRow {
	if strings.TrimSpace(htmlText) == "" || heading == nil {
		return nil
	}
	lowerHTML := strings.ToLower(htmlText)
	for _, loc := range heading.FindAllStringIndex(htmlText, -1) {
		// 标题到表格之间会夹 </font>、<br />、<p> 等，限制在 400 字节内找第一张表；
		// 目录里的那条标题后面紧跟的是目录表格本身，解析不出等级行，会自然跳到正文那条。
		windowEnd := min(loc[1]+faqLevelHeadingGap, len(htmlText))
		tableOffset := strings.Index(lowerHTML[loc[1]:windowEnd], "<table")
		if tableOffset < 0 {
			continue
		}
		tableStart := loc[1] + tableOffset
		sectionEnd := min(tableStart+faqLevelSectionBytes, len(htmlText))
		// 只取这张表本身：FAQ 里等级表和「提升和降级」表紧挨着，
		// 扫过头会把两张表的行混在一起（彩虹岛 Elite/Veteran 行少一个 </td>，
		// 于是被挤到第二张表的位置、排到等级列表最后）。
		if closeAt := strings.Index(lowerHTML[tableStart:sectionEnd], "</table>"); closeAt >= 0 {
			sectionEnd = tableStart + closeAt + len("</table>")
		}
		if rows := parseLevelRowsInSection(htmlText[tableStart:sectionEnd]); len(rows) >= 3 {
			return rows
		}
	}
	return nil
}

// markdownTable 是一张从纯文本里还原出来的 markdown 表格。
type markdownTable struct {
	Header []string
	Rows   [][]string
}

var (
	// reMarkdownTableSeparator 匹配 markdown 表格的表头分隔行：`|------|:---:|---|`。
	reMarkdownTableSeparator = regexp.MustCompile(`^\s*\|(?:\s*:?-{2,}:?\s*\|)+\s*$`)
	// markdownCellDecorators 去掉 markdown 的行内修饰：**粗体**、`代码`、~~删除线~~。
	markdownCellDecorators = strings.NewReplacer("**", "", "__", "", "~~", "", "`", "", "*", "")
)

// cleanMarkdownCell 把 markdown 单元格清洗成纯文本。
// 参数/返回：raw 为 `| a | b |` 拆出来的单格；返回去修饰、反转义、折叠空白后的文本。
// 失败场景：无。副作用：无。
func cleanMarkdownCell(raw string) string {
	value := html.UnescapeString(strings.TrimSpace(raw))
	value = markdownCellDecorators.Replace(value)
	return strings.Join(strings.Fields(value), " ")
}

// isMarkdownTableRow 判断一行是否属于 markdown 表格（以 `|` 开头且至少两列）。
func isMarkdownTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "|") && strings.Count(trimmed, "|") >= 2
}

// splitMarkdownTableRow 把 `| a | b |` 拆成 ["a", "b"]（保留空列，列位置对等级表很关键）。
func splitMarkdownTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, cleanMarkdownCell(part))
	}
	return cells
}

// parseMarkdownTables 从纯文本里提取所有 markdown 表格。
// 参数/返回：text 为已转纯文本的页面；返回按出现顺序排列的表格列表。
// 失败场景：无（没有表格时返回空切片）。
// 副作用：无。
//
// 只认「表头行 + 分隔行」的组合，段落正文里偶尔出现的 `|` 竖线不会被误当成表格。
func parseMarkdownTables(text string) []markdownTable {
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	tables := make([]markdownTable, 0, 4)
	for index := 0; index+1 < len(lines); index++ {
		if !isMarkdownTableRow(lines[index]) || !reMarkdownTableSeparator.MatchString(lines[index+1]) {
			continue
		}
		table := markdownTable{Header: splitMarkdownTableRow(lines[index])}
		cursor := index + 2
		for cursor < len(lines) && isMarkdownTableRow(lines[cursor]) {
			table.Rows = append(table.Rows, splitMarkdownTableRow(lines[cursor]))
			cursor++
		}
		tables = append(tables, table)
		index = cursor - 1
	}
	return tables
}

// markdownHeaderHas 判断表头里是否含某个关键词（已做繁简/去空白归一化）。
func markdownHeaderHas(header []string, keywords ...string) bool {
	for _, cell := range header {
		normalized := normalizeLabel(cell)
		for _, keyword := range keywords {
			key := normalizeLabel(keyword)
			if key != "" && strings.Contains(normalized, key) {
				return true
			}
		}
	}
	return false
}

// parseMarkdownLevelTable 从 markdown 表格里还原等级表。
// 参数/返回：text 为纯文本；wantRequirement=false 取「等级 | 权限说明」表，
// true 取「等级 | 注册时间 | 做种积分 | …」升级条件表；解析不到 ≥3 行返回 nil。
// 失败场景：无（由调用方兜底）。
// 副作用：无。
//
// 与 HTML 版 parseLevelTable 的语义保持一致：返回的 levelRow.Cells 只装「说明列」，
// 权限表给最后一列（权限说明），条件表把各列拼成「注册时间 至少 4 周；做种积分 >40000…」。
func parseMarkdownLevelTable(text string, wantRequirement bool) []levelRow {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	for _, table := range parseMarkdownTables(text) {
		if len(table.Header) < 2 || len(table.Rows) < 3 {
			continue
		}
		// 第一列必须是「等级」列：站点用表格同时列出等级、捐赠说明、统计口径，
		// 只靠列数无法区分，表头是唯一可靠的锚点。
		firstHeader := normalizeLabel(table.Header[0])
		if !strings.Contains(firstHeader, "等级") && !strings.Contains(firstHeader, "class") {
			continue
		}
		isPrivilegeTable := markdownHeaderHas(table.Header, "权限", "權限", "说明", "說明", "privilege")
		if isPrivilegeTable == wantRequirement {
			continue
		}
		rows := make([]levelRow, 0, len(table.Rows))
		seen := map[string]struct{}{}
		for _, cells := range table.Rows {
			if len(cells) == 0 {
				continue
			}
			name := cells[0]
			if name == "" || isLevelPlaceholder(name) {
				continue
			}
			// 表头已声明这是等级列，所以「黄星」「贵宾(VIP)」这类站点自定义等级不再靠英文名单过滤；
			// 只挡掉太长、明显是说明文字的行（某些站把备注也写成表格行）。
			if len([]rune(name)) > 40 {
				continue
			}
			key := normalizeLevelName(name)
			if _, duplicated := seen[key]; duplicated {
				continue
			}
			seen[key] = struct{}{}

			rest := make([]string, 0, len(cells))
			if wantRequirement {
				conditions := make([]string, 0, len(cells))
				for col := 1; col < len(cells); col++ {
					value := cells[col]
					if value == "" || isCellPlaceholder(value) {
						continue
					}
					label := ""
					if col < len(table.Header) {
						label = table.Header[col]
					}
					if label == "" || label == "-" {
						conditions = append(conditions, value)
						continue
					}
					conditions = append(conditions, label+" "+value)
				}
				if len(conditions) == 0 {
					continue
				}
				rest = append(rest, strings.Join(conditions, "；"))
			} else {
				for col := 1; col < len(cells); col++ {
					if cells[col] == "" || isCellPlaceholder(cells[col]) {
						continue
					}
					rest = append(rest, cells[col])
				}
			}
			rows = append(rows, levelRow{Name: name, Cells: rest})
		}
		if len(rows) >= 3 {
			return rows
		}
	}
	return nil
}

// parseLevelRowsInSection 在标题后的片段里逐行提取「等级名 + 该行说明」。
func parseLevelRowsInSection(section string) []levelRow {
	rows := make([]levelRow, 0, 12)
	seen := map[string]struct{}{}
	// 双语站（不可说 ssd）把中文等级表和英文等级表放进同一个 <table>，
	// 同一等级会出现两行（「精英(Elite)」和「Elite」）。按名字里的拉丁部分建索引去重，
	// 只保留带中文的那行（中文行还有站点的中文权限说明）。
	latinIndex := map[string]int{}
	for _, match := range reTableRow.FindAllStringSubmatch(section, -1) {
		cells := rowCellTexts(match[1])
		badge := reLevelBadge.FindStringSubmatch(match[1])
		// 分组说明行（TTG：`以下为工作组：`）之后整块是工作组名单，不属于用户等级。
		if len(cells) > 0 && reLevelTableSectionBreak.MatchString(cells[0]) {
			break
		}
		// 标准皮肤的等级名在 badge 里，即使该行少了描述单元格（td 未闭合）也要保留；
		// 自研皮肤没有 badge，必须至少有「等级名 + 一列说明」两个单元格才认。
		if badge == nil && len(cells) < 2 {
			continue
		}
		name := ""
		switch {
		case badge != nil:
			name = cellText(badge[1])
		case looksLikeLevelRowName(cells[0]):
			// 自研皮肤（憨憨 / PeerGo 规则页）没有 _Name class，等级名写在第一列
			// 形如「脸憨皮厚 Peasant」，靠名字里的英文等级名识别。
			name = cells[0]
		case firstCellStartsBold(match[1]) && looksLikeBoldLevelName(cells[0]):
			// TTG：等级名是第一列加粗的短文本（Byte / KiloByte / … / 资深组 / VIP），
			// 既没有 _Name class 也不含标准等级名，只能靠「加粗 + 短 + 无标点」判断。
			name = cells[0]
		}
		if name == "" || isLevelPlaceholder(name) {
			continue
		}
		key := normalizeLevelName(name)
		if _, duplicated := seen[key]; duplicated {
			continue
		}
		// 去掉第一列（等级名）与占位符，剩下的按原顺序留给权限/升级条件。
		rest := make([]string, 0, len(cells))
		for _, cell := range cells[1:] {
			if isCellPlaceholder(cell) {
				continue
			}
			rest = append(rest, cell)
		}
		row := levelRow{Name: name, Cells: rest}
		latin := latinLevelKey(name)
		if latin != "" {
			if index, existed := latinIndex[latin]; existed {
				existingCJK, newCJK := hasHanText(rows[index].Name), hasHanText(name)
				switch {
				case existingCJK && !newCJK:
					// 中文在前、英文在后：丢掉英文重复行。
					continue
				case !existingCJK && newCJK:
					// 英文在前、中文在后：用中文行就地替换，等级顺序不变。
					delete(seen, normalizeLevelName(rows[index].Name))
					seen[key] = struct{}{}
					rows[index] = row
					continue
				}
			}
			latinIndex[latin] = len(rows)
		}
		seen[key] = struct{}{}
		rows = append(rows, row)
	}
	return rows
}

// latinLevelKey 取等级名里的拉丁部分做去重键（`精英(Elite)` → `elite`）；没有拉丁字母时返回空。
func latinLevelKey(name string) string {
	match := reLatinName.FindString(name)
	if match == "" {
		return ""
	}
	return normalizeLevelName(match)
}

// hasHanText 判断文本里是否含汉字（用来在双语等级表里优先保留中文行）。
func hasHanText(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// applyRowRequirements 用 FAQ 升级条件表覆盖每一级的门槛文本。
func applyRowRequirements(levels []UserLevel, rows []levelRow) {
	if len(rows) == 0 {
		return
	}
	byKey := indexLevelsByKey(levels)
	for _, row := range rows {
		// 标准皮肤的条件表只有一列说明；四列表格优先取「升级条件」那一列。
		condition := cleanRequirement(row.middleCell())
		if condition == "" {
			condition = cleanRequirement(row.lastCell())
		}
		if condition == "" {
			continue
		}
		if index, ok := lookupLevelIndex(byKey, row.Name); ok {
			levels[index].Requirements = truncateText(condition, 260)
		}
	}
}

// cleanRequirement 清掉升级条件里的模板句与占位符（`-` 表示该级没有升级条件）。
func cleanRequirement(raw string) string {
	value := strings.ReplaceAll(raw, "当条件符合时将被自动提升。", "")
	value = strings.TrimSpace(value)
	switch value {
	case "", "-", "—", "–", "无", "/":
		return ""
	}
	return strings.Trim(value, " ：:，,。;；")
}

// rowCellTexts 取一行里所有单元格的文本（跳过空单元格）。
func rowCellTexts(rowHTML string) []string {
	cells := reTD.FindAllStringSubmatch(rowHTML, -1)
	texts := make([]string, 0, len(cells))
	// reTD 的第 1 个分组是属性、第 2 个才是单元格内容。
	for _, cell := range cells {
		if len(cell) < 3 {
			continue
		}
		if text := cellText(cell[2]); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

// looksLikeLevelRowName 判断第一列文本是否是等级名（自研皮肤没有 _Name class 时用）。
// 判定口径：长度不超过 40 字，且包含 NexusPHP 的某个等级名（`脸憨皮厚 Peasant`、`贵宾(VIP)`）。
func looksLikeLevelRowName(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len([]rune(trimmed)) > 40 {
		return false
	}
	lower := strings.ToLower(trimmed)
	for _, token := range levelNameTokens {
		if strings.Contains(lower, strings.ToLower(token)) {
			return true
		}
	}
	return false
}

// firstCellStartsBold 判断该行的第一个单元格是否以 <strong>/<b> 开头（自研皮肤的加粗等级名）。
func firstCellStartsBold(rowHTML string) bool {
	match := reTD.FindStringSubmatch(rowHTML)
	if len(match) < 3 {
		return false
	}
	return reStrongFirstCell.MatchString(match[2])
}

// looksLikeBoldLevelName 判断加粗的首列文本能否当等级名。
// 判定口径：不超过 20 字、不含句读标点、不是分组说明行（`以下为工作组`）。
// 加粗只说明「是表头/强调」，说明性长句仍要挡掉，避免把备注行收成等级。
func looksLikeBoldLevelName(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || len([]rune(trimmed)) > 20 {
		return false
	}
	if strings.ContainsAny(trimmed, "。，；：、,.;:！？!?") {
		return false
	}
	return !reLevelTableSectionBreak.MatchString(trimmed)
}

// splitInlineRequirement 把权限描述里的行内升级条件拆出来。
// 参数/返回：text 是等级表最后列的文本；返回 (升级条件, 去掉该括号后的权限文本)。
// 例：`此等级才为正式会员。（升级条件：注册满 5 周，下载量在60G以上）` →
// (`注册满 5 周，下载量在60G以上`, `此等级才为正式会员。`)。
// 失败场景：没有行内条件时返回 ("", 原文)。
// 副作用：无。
func splitInlineRequirement(text string) (string, string) {
	match := reInlineUpgradeCondition.FindStringSubmatchIndex(text)
	if match == nil {
		return "", text
	}
	condition := strings.TrimSpace(text[match[2]:match[3]])
	remaining := strings.TrimSpace(text[:match[0]] + text[match[1]:])
	return condition, strings.TrimSpace(remaining)
}

// isCellPlaceholder 判断单元格是否是占位符（`-` 表示该格没有内容）。
func isCellPlaceholder(text string) bool {
	switch strings.TrimSpace(text) {
	case "-", "—", "–", "/":
		return true
	}
	return false
}

// cellText 把一段 HTML 片段转成紧凑纯文本（去标签、解实体、折叠空白）。
func cellText(fragment string) string {
	text := reScriptStyle.ReplaceAllString(fragment, " ")
	text = html.UnescapeString(reTag.ReplaceAllString(text, " "))
	text = strings.ReplaceAll(text, "\u00a0", " ")
	return strings.Join(strings.Fields(stripIconGlyphs(text)), " ")
}

// isLevelPlaceholder 过滤 FAQ 表格里的占位行（`其它`/`自定义等级` 不是真正的等级）。
func isLevelPlaceholder(name string) bool {
	switch strings.TrimSpace(name) {
	case "其它", "其他", "自定义等级", "自定義等級":
		return true
	}
	return false
}

// levelMatchKeys 生成一个等级名可用于匹配的若干归一化键：
// 整个名字、名字里的拉丁字母部分、去掉括号别名后的部分。
// 例：`(冰人)Peasant` → `冰人)peasant` / `peasant`；`贵宾(VIP)` → `贵宾vip` / `vip` / `贵宾`。
// 这样 rules.php 里写「升级至 Ultimate User 等级」也能对上有中文别名的等级行。
func levelMatchKeys(name string) []string {
	keys := make([]string, 0, 3)
	add := func(value string) {
		key := normalizeLevelName(value)
		if key == "" {
			return
		}
		for _, existing := range keys {
			if existing == key {
				return
			}
		}
		keys = append(keys, key)
	}
	add(name)
	for _, part := range reLatinName.FindAllString(name, -1) {
		add(part)
	}
	if stripped := strings.TrimSpace(reBracketPart.ReplaceAllString(name, " ")); stripped != "" && stripped != name {
		add(stripped)
	}
	return keys
}

// indexLevelsByKey 建立「归一化键 → 等级下标」索引，同键只保留第一个（顺序即等级顺序）。
func indexLevelsByKey(levels []UserLevel) map[string]int {
	byKey := make(map[string]int, len(levels)*3)
	for index, level := range levels {
		for _, key := range levelMatchKeys(level.Name) {
			if _, exists := byKey[key]; !exists {
				byKey[key] = index
			}
		}
	}
	return byKey
}

// lookupLevelIndex 用等级名（可能带别名）在索引里查下标。
func lookupLevelIndex(byKey map[string]int, name string) (int, bool) {
	for _, key := range levelMatchKeys(name) {
		if index, ok := byKey[key]; ok {
			return index, true
		}
	}
	return 0, false
}

// markKeepAliveLevelsFromRows 逐行看等级表的权限 / 升级条件，再用整页文本兜底，判定保号线。
//
// 两种落库路径（FAQ 表格解析、内置 10 级标准表兜底）都走这里：两种路径的行内文案都可能
// 是站点原文，「账号永久保留」这类单级写法（NexusPHP 的 Nexus Master）本身就等于保号承诺。
func markKeepAliveLevelsFromRows(levels []UserLevel, faqText, rulesText string) {
	order, found := detectKeepAliveOrder(levels, faqText, rulesText)
	if !found {
		order = -1
	}
	applyKeepAliveOrder(levels, order)
}

// applyKeepAliveOrder 把保号标记落到 order 对应的那一级（order < 0 表示全部清空）。
func applyKeepAliveOrder(levels []UserLevel, order int) {
	for index := range levels {
		levels[index].KeepAlive = order >= 0 && levels[index].Order == order
	}
}

// detectKeepAliveOrder 返回保号等级的 order（多句命中时取最低档）。
//
// 先逐行看权限 / 升级条件单元格（站点把保留规则写在等级表里的情况，绝大多数站点如此），
// 再用整页文本兜底（保留规则写在表格外段落、或权限列被截断的站点）。两条路径都要求
// 「X 及以上」紧邻等级名，避免低等级名抢走高等级那行的句子。
func detectKeepAliveOrder(levels []UserLevel, faqText, rulesText string) (int, bool) {
	best := -1
	record := func(order int) {
		if order >= 0 && (best < 0 || order < best) {
			best = order
		}
	}
	for _, level := range levels {
		for _, field := range []string{level.Privileges, level.Requirements} {
			if order, ok := keepAliveOrderInLine(levels, field, level.Order); ok {
				record(order)
			}
		}
	}
	for _, text := range []string{faqText, rulesText} {
		if order, ok := keepAliveOrderInText(levels, text); ok {
			record(order)
		}
	}
	return best, best >= 0
}

// keepAliveOrderInLine 解析单个等级行的权限/条件文案，返回该行能证明的保号 order。
//
// 判断顺序：
//  1. 「此级别及以上…永远保留」→ 本行；
//  2. 「X 及以上…永远保留」→ X 那一级（X 可能不是本行，站点常把规则写在高一级）；
//  3. 本行不含「及以上」却直接写「保留帐号 / 账号永久保留」→ 本行。
func keepAliveOrderInLine(levels []UserLevel, text string, selfOrder int) (int, bool) {
	if strings.TrimSpace(text) == "" {
		return 0, false
	}
	if loc := reThisLevelAndAbove.FindStringIndex(text); loc != nil {
		if isRetainForeverSentence(keepAliveRetainWindow(text, loc[1])) {
			return selfOrder, true
		}
	}
	if order, ok := keepAliveOrderInText(levels, text); ok {
		return order, true
	}
	// 单级直说「保留帐号」才算本行保号；含「及以上」的行交给上面两条，否则「Ultimate 及以上…」
	// 会被低等级行当成本行声明，把保号线拉到最低档。
	if !reAndAboveAnywhere.MatchString(text) && isRetainForeverSentence(text) {
		return selfOrder, true
	}
	return 0, false
}

// keepAliveOrderInText 在整段文本里找「等级名 + 及以上 + 永远保留」，返回最低命中 order。
//
// 长名优先并占用区间：`User` 是 `Power User`/`Veteran User` 的子串，
// 若不占用，"Veteran User 及以上" 会被 `User` 抢先认领，保号线直接掉到 User。
func keepAliveOrderInText(levels []UserLevel, text string) (int, bool) {
	if strings.TrimSpace(text) == "" {
		return 0, false
	}
	type candidate struct {
		name  string
		order int
	}
	candidates := make([]candidate, 0, len(levels)*2)
	seen := map[string]struct{}{}
	for _, level := range levels {
		for _, name := range rawMatchNames(level.Name) {
			key := strings.ToLower(name)
			if _, duplicated := seen[key]; duplicated {
				continue
			}
			seen[key] = struct{}{}
			candidates = append(candidates, candidate{name: name, order: level.Order})
		}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return len(candidates[i].name) > len(candidates[j].name)
	})

	best := -1
	occupied := make([][2]int, 0, len(candidates))
	lowerText := strings.ToLower(text)
	for _, item := range candidates {
		nameLower := strings.ToLower(item.name)
		from := 0
		for from < len(lowerText) {
			offset := strings.Index(lowerText[from:], nameLower)
			if offset < 0 {
				break
			}
			start := from + offset
			end := start + len(nameLower)
			from = end
			if !isWordBoundary(text, start, end) || overlapsAny([2]int{start, end}, occupied) {
				continue
			}
			occupied = append(occupied, [2]int{start, end})
			// 必须紧邻「及以上」：中间夹别的字就说明这句不是给这个名字的。
			rest := text[end:]
			match := reAndAbove.FindStringIndex(rest)
			if match == nil || match[0] != 0 {
				continue
			}
			if !isRetainForeverSentence(keepAliveRetainWindow(rest, match[1])) {
				continue
			}
			if best < 0 || item.order < best {
				best = item.order
			}
		}
	}
	return best, best >= 0
}

// keepAliveRetainWindow 截取锚点之后到句末的文本（最多 80 字），用于判断这句是不是「永远保留账号」。
func keepAliveRetainWindow(text string, start int) string {
	if start >= len(text) {
		return ""
	}
	window := text[start:]
	if idx := strings.IndexAny(window, "。；;\n"); idx >= 0 {
		window = window[:idx]
	}
	if idx := strings.Index(window, ". "); idx >= 0 {
		window = window[:idx]
	}
	runes := []rune(window)
	if len(runes) > 80 {
		window = string(runes[:80])
	}
	return window
}

// isRetainForeverSentence 判断一句文案是否声明「账号永久保留」。
// 含封存/挂起（「封存后不删除」）或否定写法（「不保留」）都不算。
func isRetainForeverSentence(sentence string) bool {
	if strings.TrimSpace(sentence) == "" {
		return false
	}
	if reParked.MatchString(sentence) {
		return false
	}
	if reRetainNegated.MatchString(sentence) {
		return false
	}
	return reRetainForever.MatchString(sentence)
}

// parseFAQLevelDescriptions 从 FAQ 文本中按等级名切段，返回「归一化等级名 → 描述」。
// 参数/返回：text 为 FAQ 纯文本；返回映射（解析不到时为空）。
// 失败场景：无。
// 副作用：无。
//
// 实现要点：英文等级名可能是中文等级名的前缀（User ⊂ Power User），
// 因此按「长度降序」找锚点，并要求命中顺序对应的 order 递增，避免把描述里提到的其它等级当成新锚点。
func parseFAQLevelDescriptions(text string) map[string]string {
	result := map[string]string{}
	if strings.TrimSpace(text) == "" {
		return result
	}
	section := cutFAQSection(text)
	if section == "" {
		return result
	}

	type anchor struct {
		start int
		end   int
		name  string
		order int
	}
	// 先按名字长度降序匹配，「长名优先占位」——否则 "Power User" 里的 "User" 会被当成 User 等级锚点，
	// 导致 User 的描述被切在 "Power " 上。
	type candidate struct {
		name  string
		order int
	}
	candidates := make([]candidate, 0, len(levelCanonicalNames))
	for order, name := range levelCanonicalNames {
		candidates = append(candidates, candidate{name: name, order: order})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return len(candidates[i].name) > len(candidates[j].name)
	})

	anchors := make([]anchor, 0, len(candidates))
	occupied := make([][2]int, 0, len(candidates))
	lowerSection := strings.ToLower(section)
	for _, item := range candidates {
		nameLower := strings.ToLower(item.name)
		from := 0
		for from < len(lowerSection) {
			offset := strings.Index(lowerSection[from:], nameLower)
			if offset < 0 {
				break
			}
			start := from + offset
			end := start + len(nameLower)
			from = end
			if !isWordBoundary(section, start, end) || overlapsAny([2]int{start, end}, occupied) {
				continue
			}
			occupied = append(occupied, [2]int{start, end})
			anchors = append(anchors, anchor{start: start, end: end, name: item.name, order: item.order})
			break
		}
	}
	if len(anchors) < 3 {
		return result
	}
	// 按出现位置排序后，只保留 order 递增的锚点（避免描述里的其它等级名干扰）。
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].start < anchors[j].start })
	ordered := make([]anchor, 0, len(anchors))
	lastOrder := -1
	for _, item := range anchors {
		if item.order <= lastOrder {
			continue
		}
		ordered = append(ordered, item)
		lastOrder = item.order
	}

	for index, item := range ordered {
		end := len(section)
		if index+1 < len(ordered) {
			end = ordered[index+1].start
		}
		if item.end > end {
			continue
		}
		description := strings.TrimSpace(section[item.end:end])
		description = strings.Trim(description, " ：:。;；\n")
		if description != "" {
			result[normalizeLevelName(item.name)] = description
		}
	}
	return result
}

// isWordBoundary 判断 [start,end) 处的匹配是否自成词（前后不能紧邻英文字母/数字）。
// 这样 "Power User" 里的 "User" 不会单独成为一个等级锚点。
//
// 边界只认 ASCII 字母与数字：中文不属于「英文单词的一部分」。
// 早期版本用 unicode.IsLetter 把中文也算进词内，导致「Veteran User及以上用户…」被判成
// 非词边界而漏掉保号线，中文 FAQ 的等级锚点也会整段识别不出来。
func isWordBoundary(text string, start, end int) bool {
	if start > 0 {
		prev, _ := utf8.DecodeLastRuneInString(text[:start])
		if isWordRune(prev) {
			return false
		}
	}
	if end < len(text) {
		next, _ := utf8.DecodeRuneInString(text[end:])
		if isWordRune(next) {
			return false
		}
	}
	return true
}

// isWordRune 判断字符是否属于英文单词（ASCII 字母或数字）。
func isWordRune(value rune) bool {
	if value > unicode.MaxASCII {
		return false
	}
	return unicode.IsLetter(value) || unicode.IsDigit(value)
}

// overlapsAny 判断区间是否与已占用区间重叠。
func overlapsAny(target [2]int, used [][2]int) bool {
	for _, item := range used {
		if target[0] < item[1] && item[0] < target[1] {
			return true
		}
	}
	return false
}

// cutFAQSection 截取 FAQ 里「用户等级说明」那一段（标题后 6000 字符内）。
func cutFAQSection(text string) string {
	loc := reFAQLevelHeading.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	section := text[loc[0]:]
	runes := []rune(section)
	if len(runes) > 6000 {
		section = string(runes[:6000])
	}
	return section
}

// applyRuleRequirements 用 rules.php 解析出的门槛文本覆盖标准值。
//
// 等级名可能带站点别名（`(冰人)Peasant`、`贵宾(VIP)`），而 rules 里写的是英文名，
// 因此按 levelMatchKeys 生成的多组键做匹配，而不是拿整名直接比。
func applyRuleRequirements(levels []UserLevel, rulesText string) {
	if strings.TrimSpace(rulesText) == "" {
		return
	}
	byKey := indexLevelsByKey(levels)
	for _, match := range reRulePromote.FindAllStringSubmatch(rulesText, -1) {
		condition := strings.TrimSpace(reRuleTail.ReplaceAllString(strings.TrimSpace(match[3]), ""))
		condition = strings.Trim(condition, " ：:，,。;；")
		if condition == "" {
			continue
		}
		if index, ok := lookupLevelIndex(byKey, match[1]); ok {
			levels[index].Requirements = truncateText(condition, 260)
		}
	}
}

// rawMatchNames 返回等级名在原始文本里可能出现的几种写法（保留大小写与空格，供全文扫描用）：
// 整名、拉丁字母片段、去掉括号别名后的部分。
// 例：`(高烧)Veteran User` → `(高烧)Veteran User` / `Veteran User` / `(高烧)` 之外的部分。
func rawMatchNames(name string) []string {
	names := make([]string, 0, 3)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		for _, existing := range names {
			if strings.EqualFold(existing, value) {
				return
			}
		}
		names = append(names, value)
	}
	add(name)
	for _, part := range reLatinName.FindAllString(name, -1) {
		add(part)
	}
	if stripped := strings.TrimSpace(reBracketPart.ReplaceAllString(name, " ")); stripped != "" && stripped != name {
		add(stripped)
	}
	return names
}

// normalizeLevelName 归一化等级名：小写、去空格与常见修饰（(新)/[已封存] 等）。
func normalizeLevelName(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	trimmed = strings.NewReplacer(" ", "", "　", "", "-", "", "_", "").Replace(trimmed)
	return strings.Trim(trimmed, "()（）[]【】")
}

// truncateText 按字符数截断文本（超长时补省略号）。
func truncateText(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "…"
}

// levelsSchemaVersion 是等级体系缓存的结构/口径版本。
//
// 版本 2 起改成「站点真实等级表」（FAQ 表格逐行解析，含 VIP/发布员/版主等特殊等级），
// 版本 1 的缓存是内置的 10 级标准表 —— 两者混用会让用户刷新后仍看到缺失等级的旧结果。
// 版本 3 起保号等级只认「X 及以上用户会永远保留账号」（此前把「封存账号后不会被删除」
// 也算进来，保号线被压低一级），旧缓存里蓝色标记会停在错误等级，必须重抓。
// 版本 4 起改按「权限行内紧邻的 X 及以上 / 单级保留帐号」判定：旧实现的 320 字节窗口会把
// 高等级那行的句子算到低等级头上（天空 明星→影帝、彩虹岛 Extreme→Ultimate、1ptba Elite→Veteran），
// 所有旧缓存的保号线都偏低，必须再重抓一次。
// 版本 5 起「账号永久保留」也算保号等级（内置标准表兜底路径同样逐行判定），
// 此前刻意跳过行内权限，导致只写「账号永久保留」的站点（Nexus Master 行）保号列为空。
// 版本 6 起 FAQ 等级表标题多了「有哪些不同的等级」等变体：城市 hdcity 的 FAQ 用的是
// 「有哪些不同的等级？」，旧正则认不出，整站退回内置 10 级（实际 18 级天使体系），
// 不升版本旧缓存不会重抓。
// 版本 7 起支持 markdown 表格：梓喵 azusa 的 FAQ 正文是 editormd 隐藏起来的 markdown 原文
// （`| 等级 | 权限说明 |`），HTML 里没有 <table>，此前只能拿到内置 10 级。
// 版本 8 起支持「第一列加粗即等级名」的自研皮肤（TTG 的 Byte/KiloByte/…/DoggaByte，
// FAQ 标题是「TTG有哪些不同的用户类别？」），并把行内「（升级条件：…）」拆到升级条件列。
// 版本 9 起处理双语 FAQ：中文表与英文表在同一个 <table> 里时按拉丁名去重（保留中文行，
// 不可说 ssd 由 32 行变 16 行），标题到表格的窗口 400→900 字节（ssd 的升级条件从英文表
// 改回中文表）。改任何解析口径都要升这个号。
// 版本 10 起支持「站点自己抓不到等级表」的内置等级体系（青蛙 qingwapt 的 faq.php 已 302 到
// Nuxt Wiki，正文由前端组件渲染）。旧缓存是内置 10 级标准表且把 Nexus Master 标成了保号，
// 不升版本不会重抓。
// 版本 11 起优先解析独立的 classes.php 等级页（北洋园 tjupt）：FAQ 里只有它自己标注的
// 「之前的规则」且表格不带升级条件，旧缓存 requirements 全空，不升版本不会重抓。
// 版本 12 起朱雀（zhuque，自研接口站）改用内置 8 级等级表（筑基…真仙）；旧缓存只有一条
// 「仅同步当前等级」的占位行，不升版本不会重抓。
// 版本 13 起 YemaPT（裸数字等级）改用内置 12 级表（Level 0 ~ Level 11），同理由占位行升级。
// 版本 14 起 PTFans 的升级门槛改用内置值补齐：FAQ 等级表能解析出 17 个等级，
// 但「提升和降级」正文只写「看置顶种子的 PU 升级计划」，requirements 全空。
// 版本 15 起 末日 agsv 同口径补齐：FAQ 还是 NexusPHP 老文案（4 周/50G/1.05），
// 站点实际生效的是自定义「熊」等级体系（做种积分 + 抬高后的周数/分享率）。
// 版本 16 起 杜比 hddolby 同口径补齐：FAQ 的做种积分/注册周数是旧值（且把「做种积分」
// 写成「种积分」，前端匹配不到），按站点当前规则表覆盖。
const levelsSchemaVersion = 16

// levelsCache 是落库的等级体系 JSON 结构。
type levelsCache struct {
	Version int         `json:"version"`
	Levels  []UserLevel `json:"levels"`
}

// encodeLevelsCache 把等级列表编码成带版本号的 JSON（空列表返回空串，不写库）。
func encodeLevelsCache(levels []UserLevel) string {
	if len(levels) == 0 {
		return ""
	}
	data, err := json.Marshal(levelsCache{Version: levelsSchemaVersion, Levels: levels})
	if err != nil {
		return ""
	}
	return string(data)
}

// decodeLevelsCache 解析等级体系 JSON，返回等级列表与版本号。
// 兼容旧格式（版本 1 是裸数组 [{name,order,...}]，版本号返回 0）。
func decodeLevelsCache(raw string) ([]UserLevel, int) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, 0
	}
	var cache levelsCache
	if err := json.Unmarshal([]byte(trimmed), &cache); err == nil && len(cache.Levels) > 0 {
		return cache.Levels, cache.Version
	}
	var legacy []UserLevel
	if err := json.Unmarshal([]byte(trimmed), &legacy); err == nil {
		return legacy, 0
	}
	return nil, 0
}

// singleLevelFallback 给没有等级体系来源的站点补一条「当前等级」。
//
// 非 NexusPHP 的接口站（可乐 / 朱雀 / 好学 / 肉丝 / YemaPT）没有 faq.php 等级表，
// 页面上「等级」点开就是空的，连自己是哪一级都看不到；用接口返回的当前等级兜底，
// 并明确标注这不是完整等级体系。
func singleLevelFallback(userClass string) []UserLevel {
	trimmed := strings.TrimSpace(userClass)
	if trimmed == "" {
		return nil
	}
	return []UserLevel{{
		Name:       trimmed,
		Order:      0,
		Privileges: "该站点未提供等级体系接口，仅同步当前等级",
	}}
}
