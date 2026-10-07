package dupe

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	"github.com/pt-nexus/server/internal/platform/logx"
)

// nexusPHPResultMarker 为 NexusPHP 结果表的识别标记。
// 实测 hdhome / pterclub / ourbits / chdbits 四站一致：结果表都带 class="torrents"
// （hdhome、ourbits、pterclub 另有 id="torrenttable"，chdbits 没有，故只用 class 判定）。
const nexusPHPResultMarker = `class="torrents"`

var (
	// nexusPHPTorrentIDPattern 从详情页链接中取种子 ID。
	// 覆盖 details.php?id=N 与游戏区的 detailsgame.php?id=N（pterclub 有游戏种子）。
	nexusPHPTorrentIDPattern = regexp.MustCompile(`(?i)details(?:game)?\.php\?id=(\d+)`)
	// nexusPHPSizeTextPattern 匹配体积单元格的完整文本（如 "22.53GB"）。
	// 用首尾锚定避免把做种数、评论数等纯数字误当体积。
	nexusPHPSizeTextPattern = regexp.MustCompile(`(?i)^(\d+(?:[.,]\d+)?)\s*(B|KB|MB|GB|TB|KIB|MIB|GIB|TIB)$`)
)

// parseNexusPHPCandidates 解析 NexusPHP 结果页，返回候选种子列表。
//
// 参数/返回：pageHTML 为搜索页 HTML；返回候选列表（无候选时返回空切片）。
// 说明：四站行结构一致——标题在 `<a title="...">`（href 指向 details.php?id=N），
// 体积在 `<td class="rowfollow">22.53<br />GB</td>`。行内嵌套了 torrentname 表格，
// 因此用真正的 DOM 解析而不是正则切行，避免嵌套 `<tr>` 导致错位。
// 副作用：无。
func parseNexusPHPCandidates(pageHTML string) []Candidate {
	root, err := xhtml.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	table := findTableByClassToken(root, "torrents")
	if table == nil {
		return nil
	}

	candidates := make([]Candidate, 0, 32)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type == xhtml.ElementNode && strings.EqualFold(child.Data, "tr") {
				if candidate, ok := candidateFromRow(child); ok {
					candidates = append(candidates, candidate)
				}
			}
			walk(child)
		}
	}
	walk(table)
	return candidates
}

// findTableByClassToken 深度优先查找带指定 class token 的 table 节点。
func findTableByClassToken(root *xhtml.Node, token string) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if found != nil {
			return
		}
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "table") && hasClassToken(node, token) {
			found = node
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return found
}

// hasClassToken 判断节点 class 属性是否包含指定 token（按空白分词，避免前缀误匹配）。
func hasClassToken(node *xhtml.Node, token string) bool {
	for _, attr := range node.Attr {
		if !strings.EqualFold(attr.Key, "class") {
			continue
		}
		for _, field := range strings.Fields(attr.Val) {
			if field == token {
				return true
			}
		}
	}
	return false
}

// candidateFromRow 从一行结果中提取候选。
// 参数/返回：row 为 tr 节点；返回候选与是否成功。
// 失败场景：缺少详情页链接、标题或体积时返回 false（该行不是种子行，如页头/表头）。
// 副作用：无。
func candidateFromRow(row *xhtml.Node) (Candidate, bool) {
	var torrentID string
	var title string
	var sizeText string

	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "a") && torrentID == "" {
			if match := nexusPHPTorrentIDPattern.FindStringSubmatch(attrValue(node, "href")); len(match) > 1 {
				torrentID = strings.TrimSpace(match[1])
				title = strings.TrimSpace(attrValue(node, "title"))
				if title == "" {
					title = nodeText(node)
				}
			}
		}
		// 体积单元格只在「结果行的直接子 td」里找：行内嵌套的 torrentname 表格
		// 同样含 td，但其文本不是体积，靠严格的整格文本匹配天然排除。
		if sizeText == "" && node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "td") {
			if text := normalizeNodeText(node); nexusPHPSizeTextPattern.MatchString(text) {
				sizeText = text
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(row)

	if torrentID == "" || strings.TrimSpace(title) == "" || sizeText == "" {
		return Candidate{}, false
	}
	size, ok := parseNexusPHPSizeText(sizeText)
	if !ok {
		return Candidate{}, false
	}
	return Candidate{
		TorrentID: torrentID,
		Title:     strings.TrimSpace(title),
		SizeBytes: size,
	}, true
}

// attrValue 读取节点属性值（大小写不敏感）。
func attrValue(node *xhtml.Node, key string) string {
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, key) {
			return attr.Val
		}
	}
	return ""
}

// nodeText 递归拼接节点的文本内容（不做空白压缩）。
func nodeText(node *xhtml.Node) string {
	var builder strings.Builder
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.TextNode {
			builder.WriteString(n.Data)
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.TrimSpace(builder.String())
}

// normalizeNodeText 归一节点文本的空白（体积单元格会被 <br /> 拆成 "22.53" 与 "GB"）。
func normalizeNodeText(node *xhtml.Node) string {
	return strings.Join(strings.Fields(nodeText(node)), "")
}

// parseNexusPHPSizeText 把 "22.53GB" 这类展示文本换算为字节。
//
// ⚠️ 精度说明：这四个站的列表页只给展示文本、没有精确字节属性。
// 与幸运站同源（NexusPHP 的 GB 即 GiB 口径，实测同一资源在两站显示同一数值且
// 幸运站详情页同时标注 23.15 GB / 23.2 GiB），故按 1024³ 换算；
// 误差来自文本保留 2 位小数，约 ±0.005 GiB（5 MB 级），远小于默认 1 GiB 容差。
//
// 参数/返回：text 为归一后的整格文本；返回字节数与是否解析成功。
// 副作用：无。
func parseNexusPHPSizeText(text string) (int64, bool) {
	match := nexusPHPSizeTextPattern.FindStringSubmatch(strings.TrimSpace(text))
	if len(match) < 3 {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	multiplier, ok := nexusPHPSizeUnitMultiplier(match[2])
	if !ok {
		return 0, false
	}
	return int64(value * multiplier), true
}

// nexusPHPSizeUnitMultiplier 返回体积单位对应的字节倍数。
func nexusPHPSizeUnitMultiplier(unit string) (float64, bool) {
	switch strings.ToUpper(strings.TrimSpace(unit)) {
	case "B":
		return 1, true
	case "KB", "KIB":
		return 1 << 10, true
	case "MB", "MIB":
		return 1 << 20, true
	case "GB", "GIB":
		return 1 << 30, true
	case "TB", "TIB":
		return 1 << 40, true
	default:
		return 0, false
	}
}

// fetchNexusPHPCandidates 请求搜索页并解析候选列表。
// 参数/返回：query 为检索输入；searchURL 为完整 URL；返回候选与错误。
// 失败场景：请求失败、被限流或返回非结果页时返回错误（调用方按可重试处理）。
// 副作用：向目标站点发起一次 GET 请求。
func fetchNexusPHPCandidates(query Query, searchURL string) ([]Candidate, error) {
	body, err := httpGetText(query, searchURL)
	if err != nil {
		return nil, err
	}
	if rejectErr := rejectUnexpectedPage(body, nexusPHPResultMarker, nil); rejectErr != nil {
		return nil, rejectErr
	}
	candidates := parseNexusPHPCandidates(body)
	if len(candidates) == 0 && !strings.Contains(body, nexusPHPResultMarker) {
		return nil, fmt.Errorf("dupe 检索返回的页面不含结果列表，无法确认是否重复（可能是站点返回了非预期页面）")
	}
	return candidates, nil
}

// checkNexusPHPSiteDupe 为「标准 NexusPHP 结果页」站点的通用 dupe 校验实现。
//
// 站点差异全部收敛在 siteCode 对应的 YAML dupe_check 配置里：
//   - search_areas 决定可用哪些检索范围（豆瓣 / IMDb / 标题），进而决定检索分几段；
//   - param_templates 决定筛选参数的命名格式（平铺名或数组名，各站不同）。
//
// 参数/返回：siteCode 为站点标识；query 为检索输入；logModule 为日志模块名。
// 返回命中结论、过程日志与错误。
// 失败场景：检索失败时返回 error（调用方走重试），绝不按「无重复」放行。
// 副作用：向目标站点发起 1~3 次 GET 请求。
func checkNexusPHPSiteDupe(siteCode string, query Query, logModule string) (Result, string, error) {
	plans := buildNexusPHPSearchPlans(siteCode, query)
	outcome := runSearchPlans(siteCode, query, plans, fetchNexusPHPCandidates, logModule)
	if outcome.Detail != "" {
		logx.Infof(logModule, "site=%s\n%s", strings.TrimSpace(siteCode), outcome.Detail)
	}
	return outcome.Result, outcome.Detail, outcome.Err
}
