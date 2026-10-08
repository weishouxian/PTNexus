package dupe

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
)

const luckptDupeLogModule = "发布-dupe校验-幸运"

// 幸运站搜索范围枚举（实测取自 torrents.php 搜索表单的 search_area 选项）。
const (
	// luckptSearchAreaIMDb 用 IMDb 链接检索（search_area=4）。实测 tt 编号与纯数字均可命中。
	luckptSearchAreaIMDb = "4"
	// luckptSearchAreaTitle 按标题检索（search_area=0），作为无 IMDb 时的兜底。
	luckptSearchAreaTitle = "0"
)

var (
	// luckptRowStartPattern 匹配结果行「标题」单元格的起始标签。
	// 幸运站与人人的行属性完全不同：没有 data-torrent-id / data-size-bytes，
	// 而是用 data-label 标记每个单元格。Go 的 RE2 不支持前瞻 (?=)，
	// 因此用 FindAllStringIndex 取各起始位置，再按下一个起始位置切片得到整行。
	luckptRowStartPattern = regexp.MustCompile(`(?is)<td[^>]*data-label="标题"`)
	// luckptIDPattern 从行内取种子 ID。
	luckptIDPattern = regexp.MustCompile(`(?i)details\.php\?id=(\d+)`)
	// luckptTitlePattern 取主标题（<a title="...">）。
	luckptTitlePattern     = regexp.MustCompile(`(?is)<a[^>]*\btitle="([^"]*)"[^>]*>(?:.*?)</a>`)
	luckptTitleBoldPattern = regexp.MustCompile(`(?is)<b>(.*?)</b>`)
	// luckptSizePattern 取体积单元格文本，形如 "23.15<br />GB"。
	luckptSizePattern = regexp.MustCompile(`(?is)data-col="size"[^>]*>(.*?)</td>`)
	// luckptSizeNumberPattern 从体积文本里取数字与单位。
	luckptSizeNumberPattern = regexp.MustCompile(`(?i)(\d+(?:[.,]\d+)?)\s*(B|KB|MB|GB|TB|KiB|MiB|GiB|TiB)`)
)

// CheckLuckPTDupe 在幸运站检索 dupe 候选，并按「制作组相同 + 体积差在容差内」判定。
//
// 检索策略（与人人站不同，故分两段）：
//  1. 有 IMDb ID 时按 IMDb 链接检索（search_area=4）——最精确，优先使用；
//  2. 没有 IMDb ID 时退回标题检索（search_area=0）——该站 search_area 没有豆瓣/TMDb 范围，
//     只能靠标题宽搜，再由「制作组相同 + 体积容差」收敛。
//
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 失败场景：检索失败时返回 error（调用方走重试），绝不按「无重复」放行。
// 副作用：向目标站点发起 1~2 次 GET 请求。
func CheckLuckPTDupe(query Query) (Result, string, error) {
	plans := make([]searchPlan, 0, 2)
	if imdbID := ExtractIMDbID(query.IMDbID); imdbID != "" {
		plans = append(plans, searchPlan{
			Label:      "IMDb ID 检索",
			Search:     imdbID,
			SearchArea: luckptSearchAreaIMDb,
			Filters:    query.Filters,
		})
	}
	// 标题兜底：IMDb 缺失时用；IMDb 存在但没命中时也追加（不同范围可能收录不同条目）。
	if title := DupeTitleSearchKeyword(query.Title); title != "" {
		plans = append(plans, searchPlan{
			Label:      "标题检索",
			Search:     title,
			SearchArea: luckptSearchAreaTitle,
			Filters:    query.Filters,
		})
	}

	outcome := runSearchPlans("luckpt", query, plans, fetchLuckPTDupeCandidates, luckptDupeLogModule)
	if outcome.Detail != "" {
		logx.Infof(luckptDupeLogModule, "site=luckpt\n%s", outcome.Detail)
	}
	return outcome.Result, outcome.Detail, outcome.Err
}

// DupeTitleSearchKeyword 从主标题提取检索关键字（各站共用）。
// 参数/返回：title 为待发布种子主标题；返回检索关键字，取不到时返回空串。
// 说明：幸运站标题检索是子串匹配，整条标题过长且含大量标点噪声。这里截取「年份之前」的片名主体：
// 支持 `Dune 2021 ...`（空格分隔）与 `Dune.Part.Two.2024...`（点号分隔）两种常见形态，
// 命中面更收敛、减少无关候选。
// 副作用：无。
func DupeTitleSearchKeyword(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	for idx := 0; idx+4 <= len(trimmed); idx++ {
		if !isAllDigits(trimmed[idx : idx+4]) {
			continue
		}
		// 年份前应是分隔符（空格或点号）或字符串开头，避免从单词中间切错。
		if idx > 0 {
			prev := trimmed[idx-1]
			if prev != ' ' && prev != '.' && prev != '_' {
				continue
			}
		}
		keyword := strings.Trim(strings.TrimSpace(trimmed[:idx]), " ._")
		if keyword != "" {
			return keyword
		}
		break
	}
	// 没有年份时取前若干个词，够站点做子串匹配即可。
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return ""
	}
	if len(fields) > 6 {
		fields = fields[:6]
	}
	return strings.Join(fields, " ")
}

func isAllDigits(text string) bool {
	for _, ch := range text {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return len(text) > 0
}

// fetchLuckPTDupeCandidates 请求搜索页并解析候选列表。
func fetchLuckPTDupeCandidates(query Query, searchURL string) ([]Candidate, error) {
	body, err := httpGetText(query, searchURL)
	if err != nil {
		return nil, err
	}
	if pageErr := ensureSearchResultPage(body, []string{`<table class="torrents"`}); pageErr != nil {
		return nil, pageErr
	}
	return parseLuckPTCandidates(body), nil
}

// parseLuckPTCandidates 从搜索页 HTML 解析候选列表。
// 参数/返回：pageHTML 为搜索页 HTML；返回候选种子列表（无候选时返回空切片）。
// 副作用：无。
func parseLuckPTCandidates(pageHTML string) []Candidate {
	starts := luckptRowStartPattern.FindAllStringIndex(pageHTML, -1)
	candidates := make([]Candidate, 0, len(starts))
	for idx, start := range starts {
		// 行片段 = 从本行标题单元格起点，到下一行标题单元格起点（或页面结尾）。
		end := len(pageHTML)
		if idx+1 < len(starts) {
			end = starts[idx+1][0]
		}
		row := pageHTML[start[0]:end]

		idMatch := luckptIDPattern.FindStringSubmatch(row)
		if len(idMatch) < 2 {
			continue
		}
		torrentID := strings.TrimSpace(idMatch[1])
		title := extractLuckPTRowTitle(row)
		if torrentID == "" || title == "" {
			continue
		}
		size, sizeOK := extractLuckPTRowSize(row)
		if !sizeOK {
			continue
		}
		candidates = append(candidates, Candidate{
			TorrentID: torrentID,
			Title:     title,
			SizeBytes: size,
		})
	}
	return candidates
}

// extractLuckPTRowTitle 提取行内主标题。
func extractLuckPTRowTitle(row string) string {
	if match := luckptTitlePattern.FindStringSubmatch(row); len(match) > 1 {
		inner := match[1]
		if boldMatch := luckptTitleBoldPattern.FindStringSubmatch(inner); len(boldMatch) > 1 {
			inner = boldMatch[1]
		}
		if title := cleanHTMLText(inner); title != "" {
			return title
		}
	}
	return ""
}

// extractLuckPTRowSize 读取行内体积并换算为字节。
// 参数/返回：row 为行 HTML；返回体积（字节）与是否解析成功。
// 失败场景：体积单元格缺失或数值无法解析时返回 false。
// 副作用：无。
//
// ⚠️ 精度说明：幸运站列表页只给「23.15GB」这类展示文本，没有精确字节属性。
// 实测详情页同时显示 `23.15 GB` 与 `23.2 GiB`，说明该站 GB 就是 GiB 口径（1024³）。
// 因此按 1024³ 换算，误差仅为文本保留 2 位小数带来的 ±0.005 GiB（约 5 MB），
// 远小于默认 1 GiB 容差，不影响判定。
func extractLuckPTRowSize(row string) (int64, bool) {
	match := luckptSizePattern.FindStringSubmatch(row)
	if len(match) < 2 {
		return 0, false
	}
	text := cleanHTMLText(match[1])
	if text == "" {
		return 0, false
	}
	numMatch := luckptSizeNumberPattern.FindStringSubmatch(text)
	if len(numMatch) < 3 {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(numMatch[1], ",", ""), 64)
	if err != nil || value <= 0 {
		return 0, false
	}
	multiplier, ok := luckptSizeUnitMultiplier(numMatch[2])
	if !ok {
		return 0, false
	}
	return int64(value * multiplier), true
}

// luckptSizeUnitMultiplier 返回体积单位对应的字节倍数（该站 GB = GiB = 1024³）。
func luckptSizeUnitMultiplier(unit string) (float64, bool) {
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
