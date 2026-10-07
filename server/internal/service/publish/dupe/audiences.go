package dupe

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
)

const audiencesDupeLogModule = "发布-dupe校验-人人"

// audiencesDupeSearchArea 为人人站搜索范围枚举。
// 实测该值对豆瓣数字 ID 与 tt 开头的 IMDb ID 都能命中（同一批结果），
// 因此豆瓣 / IMDb 任一可用即足以检索；纯数字 TMDb ID 在该范围下不命中，故不参与检索。
const audiencesDupeSearchArea = "2"

var (
	// audiencesDupeRowPattern 匹配一条搜索结果行。行内携带 data-torrent-id 与
	// data-size-bytes 两个属性，可直接取到种子 ID 与精确体积，无需解析 "57.07 GB" 这类展示文本。
	audiencesDupeRowPattern = regexp.MustCompile(`(?is)<tr[^>]*\bdata-torrent-id="(\d+)"[^>]*>(.*?)</tr>`)
	// audiencesDupeSizePattern 从行属性中取精确体积。
	audiencesDupeSizePattern = regexp.MustCompile(`\bdata-size-bytes="(\d+)"`)
	// audiencesDupeTitlePattern 取站点展示的主标题（title 属性优先，回退到 <b> 文本）。
	audiencesDupeTitlePattern         = regexp.MustCompile(`(?is)class="[^"]*torrent-title-link[^"]*"[^>]*\btitle="([^"]*)"`)
	audiencesDupeTitleTextPattern     = regexp.MustCompile(`(?is)class="[^"]*torrent-title-link[^"]*"[^>]*>(.*?)</a>`)
	audiencesDupeTitleTextBoldPattern = regexp.MustCompile(`(?is)<b>(.*?)</b>`)
	audiencesDupeTagPattern           = regexp.MustCompile(`(?is)<[^>]+>`)
	audiencesDupeSpacePattern         = regexp.MustCompile(`\s+`)
	audiencesDupeAmpPattern           = regexp.MustCompile(`&amp;`)
	audiencesDupeNBSPPattern          = regexp.MustCompile("\u00a0")
	// audiencesDupeResultTablePattern 为正常结果页的结构标记（结果表头）。
	audiencesDupeResultTablePattern = regexp.MustCompile(`(?i)id="torrenttable`)
)

// CheckAudiencesDupe 在人人站检索 dupe 候选，并按「制作组相同 + 体积差在容差内」判定。
// 参数/返回：query 为检索输入；返回命中结论、过程日志与错误。
// 失败场景：检索请求失败或响应异常时返回 error（调用方须自行决定是否放行）；
// 检索成功但无候选 / 无命中时返回 IsDupe 为 false 的 Result 与 nil error。
// 副作用：向目标站点发起一次 GET 请求。
func CheckAudiencesDupe(query Query) (Result, string, error) {
	search := firstAudiencesSearchID(query)
	plans := []searchPlan{{
		Label:      "IMDb/豆瓣 ID 检索",
		Search:     search,
		SearchArea: audiencesDupeSearchArea,
		Filters:    query.Filters,
	}}
	outcome := runSearchPlans("audiences", query, plans, fetchAudiencesDupeCandidates, audiencesDupeLogModule)
	if outcome.Detail != "" {
		logx.Infof(audiencesDupeLogModule, "site=audiences\n%s", outcome.Detail)
	}
	return outcome.Result, outcome.Detail, outcome.Err
}

// firstAudiencesSearchID 选择用于检索的外部条目 ID。
// 参数/返回：query 为检索输入；返回搜索关键字，取不到时返回空串。
// 说明：豆瓣数字 ID 与 tt 开头的 IMDb ID 在 search_area=2 下都能命中，优先取豆瓣、其次 IMDb；
// 纯数字 TMDb ID 不参与检索（该范围下不命中）。
// 副作用：无。
func firstAudiencesSearchID(query Query) string {
	if id := ExtractDoubanID(query.DoubanID); id != "" {
		return id
	}
	return ExtractIMDbID(query.IMDbID)
}

var (
	audiencesDoubanIDPattern = regexp.MustCompile(`(\d{5,})`)
	audiencesIMDbIDPattern   = regexp.MustCompile(`(?i)(tt\d{6,10})`)
)

// ExtractDoubanID 从豆瓣链接或裸 ID 中提取数字 ID。
func ExtractDoubanID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	match := audiencesDoubanIDPattern.FindStringSubmatch(trimmed)
	if len(match) > 1 {
		return match[1]
	}
	return ""
}

// ExtractIMDbID 从 IMDb 链接或裸 ID 中提取 tt 编号（统一小写）。
func ExtractIMDbID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	match := audiencesIMDbIDPattern.FindStringSubmatch(trimmed)
	if len(match) > 1 {
		return strings.ToLower(match[1])
	}
	return ""
}

// fetchAudiencesDupeCandidates 请求搜索页并解析候选列表。
func fetchAudiencesDupeCandidates(query Query, searchURL string) ([]Candidate, error) {
	body, err := httpGetText(query, searchURL)
	if err != nil {
		return nil, err
	}
	if rejectErr := rejectUnexpectedPage(body, `id="torrenttable`, nil); rejectErr != nil {
		return nil, rejectErr
	}
	candidates := parseAudiencesCandidates(body)
	// 结构兜底：正常结果页（无论有无命中）都会渲染结果表头；
	// 若既无结果行也无结果表，说明拿到的是非预期页面，按失败处理而不是「无重复」。
	if len(candidates) == 0 && !audiencesDupeResultTablePattern.MatchString(body) {
		return nil, fmt.Errorf("dupe 检索返回的页面不含结果列表，无法确认是否重复（可能是站点返回了非预期页面）")
	}
	return candidates, nil
}

// parseAudiencesCandidates 从搜索页 HTML 解析候选列表。
// 参数/返回：pageHTML 为搜索页 HTML；返回候选种子列表（无候选时返回空切片）。
// 失败场景：页面无结果行时返回空切片，不视为错误（表示没有重复）。
// 副作用：无。
func parseAudiencesCandidates(pageHTML string) []Candidate {
	matches := audiencesDupeRowPattern.FindAllStringSubmatch(pageHTML, -1)
	candidates := make([]Candidate, 0, len(matches))
	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		torrentID := strings.TrimSpace(match[1])
		rowHTML := match[2]
		title := extractAudiencesRowTitle(rowHTML)
		if torrentID == "" || title == "" {
			continue
		}
		size, sizeOK := extractAudiencesRowSize(match[0], rowHTML)
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

// extractAudiencesRowSize 读取行内的精确体积（字节）。
func extractAudiencesRowSize(fullRow string, rowHTML string) (int64, bool) {
	for _, source := range []string{fullRow, rowHTML} {
		if match := audiencesDupeSizePattern.FindStringSubmatch(source); len(match) > 1 {
			if parsed, err := strconv.ParseInt(strings.TrimSpace(match[1]), 10, 64); err == nil && parsed > 0 {
				return parsed, true
			}
		}
	}
	return 0, false
}

// extractAudiencesRowTitle 提取行内主标题。
func extractAudiencesRowTitle(rowHTML string) string {
	if match := audiencesDupeTitlePattern.FindStringSubmatch(rowHTML); len(match) > 1 {
		if title := cleanHTMLText(match[1]); title != "" {
			return title
		}
	}
	if match := audiencesDupeTitleTextPattern.FindStringSubmatch(rowHTML); len(match) > 1 {
		inner := match[1]
		if boldMatch := audiencesDupeTitleTextBoldPattern.FindStringSubmatch(inner); len(boldMatch) > 1 {
			inner = boldMatch[1]
		}
		return cleanHTMLText(inner)
	}
	return ""
}

// cleanHTMLText 还原 HTML 实体并归一空白。
func cleanHTMLText(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	stripped := audiencesDupeTagPattern.ReplaceAllString(trimmed, "")
	decoded := audiencesDupeAmpPattern.ReplaceAllString(stripped, "&")
	decoded = audiencesDupeNBSPPattern.ReplaceAllString(decoded, " ")
	decoded = audiencesDupeSpacePattern.ReplaceAllString(decoded, " ")
	decoded = html.UnescapeString(decoded)
	return strings.TrimSpace(decoded)
}
