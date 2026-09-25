package bangumi

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

// 番组条目匹配：把种子侧的标题/译名/TMDB ID 匹配到库内 bangumi_items，生成 bgm.tv 详情链接。
// 仅做本地库匹配，不访问外网；未命中时返回空串，由前端手动填写。

// SubjectLinkPrefix bgm.tv 条目详情页前缀。
const SubjectLinkPrefix = "https://bgm.tv/subject/"

// 标题清洗相关模式（仅用于生成匹配候选，不改变原始标题）。
var (
	subjectIDPattern = regexp.MustCompile(`bgm\.tv/subject/(\d+)`)
	bareIDPattern    = regexp.MustCompile(`^\d{2,}$`)
	// bracketPattern 匹配方括号/圆括号包裹的发布组、画质、字幕等信息。
	bracketPattern = regexp.MustCompile(`[\[\(【（][^\[\]\(\)【】（）]*[\]\)】）]`)
	// trailingEpisodePattern 匹配标题结尾的集数标记：第 01 集 / EP01 / - 01 / 01v2 等。
	trailingEpisodePattern = regexp.MustCompile(`(?i)(?:\s*[-–—~]\s*)?(?:第\s*\d{1,4}\s*[集话話期]|(?:EP|E|Episode)\s*\d{1,4}|\d{1,4})(?:\s*v\d)?\s*$`)
	// trailingSeasonPattern 匹配标题结尾的季号：第 2 季 / S02 / 2nd Season。
	trailingSeasonPattern = regexp.MustCompile(`(?i)(?:\s*第\s*[0-9０-９一二三四五六七八九十]+\s*[季期部]|\s*[\(\[【]?\s*(?:S|Season)\s*\d{1,2}\s*[\)\]】]?|\s*(?:1st|2nd|3rd|\d+th)\s+season)\s*$`)
	// cjkRunPattern / latinRunPattern 分别提取中文（含日文假名）片段与拉丁字母片段，
	// 用于处理“中文名 原名”连写在同一段（无分隔符）的常见标题形式。
	cjkRunPattern   = regexp.MustCompile(`[\p{Han}\p{Hiragana}\p{Katakana}]+`)
	latinRunPattern = regexp.MustCompile(`[A-Za-z][A-Za-z0-9'’&!.:,\-]*`)

	// titleSegmentSeparators 标题分段符：PT 标题常用“中文名 / 原名”形式。
	titleSegmentSeparators = []rune{'/', '／', '|', '｜', '\\'}
	// titleTrimCutset 候选标题首尾需要剔除的标点。
	titleTrimCutset = " \t-–—~·・_.,;:：；、!！?？\"'“”‘’()（）[]【】"
)

// ItemLookup 定义番组条目检索所需的最小仓储能力。
type ItemLookup interface {
	FindBangumiItemsByTmdbID(tmdbID string) ([]repository.BangumiItem, error)
	FindBangumiItemsByTitles(titles []string) ([]repository.BangumiItem, error)
}

// MatchInput 描述一次番组条目匹配的输入。
type MatchInput struct {
	// Titles 种子侧可用标题集合（发布标题、种子名、副标题、译名等），由调用方按可信度降序排列。
	Titles []string
	// TmdbID 种子的 TMDB 数字 ID（可为空）。
	TmdbID string
	// Year 资源年份（可为空），用于同系列多季条目消歧。
	Year string
}

// ResolveSubjectLink 按 TMDB ID 与标题匹配番组条目，返回 bgm.tv 详情链接与条目 ID。
// 参数/返回：lookup 为条目检索能力；input 为匹配输入；命中返回链接与 ID，未命中返回两个空串。
// 失败场景：检索失败仅记录日志并按未命中处理，不返回错误。
// 副作用：仅读取 bangumi_items 表。
func ResolveSubjectLink(lookup ItemLookup, input MatchInput) (string, string) {
	if lookup == nil {
		return "", ""
	}
	candidates := BuildTitleCandidates(input.Titles...)
	year := strings.TrimSpace(input.Year)

	items := make([]repository.BangumiItem, 0, 16)
	if tmdbID := strings.TrimSpace(input.TmdbID); tmdbID != "" {
		rows, err := lookup.FindBangumiItemsByTmdbID(tmdbID)
		if err != nil {
			logx.Warnf(logModule, "按 TMDB ID 检索番组条目失败 tmdb_id=%s err=%v", tmdbID, err)
		} else {
			items = append(items, rows...)
		}
	}
	if len(candidates) > 0 {
		rows, err := lookup.FindBangumiItemsByTitles(candidates)
		if err != nil {
			logx.Warnf(logModule, "按标题检索番组条目失败 titles=%v err=%v", candidates, err)
		} else {
			items = append(items, rows...)
		}
	}

	best := PickBestItem(items, candidates, year, strings.TrimSpace(input.TmdbID))
	if best == nil {
		return "", ""
	}
	return SubjectLink(best.BangumiID), strings.TrimSpace(best.BangumiID)
}

// SubjectLink 由番组条目 ID 生成 bgm.tv 详情链接；ID 为空时返回空串。
// 参数/返回：bangumiID 为 bgm.tv subject ID；返回规范链接。
// 失败场景：无。
// 副作用：无。
func SubjectLink(bangumiID string) string {
	id := strings.TrimSpace(bangumiID)
	if id == "" {
		return ""
	}
	return SubjectLinkPrefix + id
}

// ExtractSubjectID 从 bgm.tv 链接或纯数字文本中提取番组条目 ID。
// 参数/返回：text 为链接或裸 ID；未识别返回空串。
// 失败场景：无。
// 副作用：无。
func ExtractSubjectID(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if match := subjectIDPattern.FindStringSubmatch(trimmed); len(match) > 1 {
		return match[1]
	}
	if bareIDPattern.MatchString(trimmed) {
		return trimmed
	}
	return ""
}

// NormalizeSubjectLink 归一化用户填写的番组链接：识别 bgm.tv 链接或裸 ID 后统一为规范链接。
// 参数/返回：raw 为用户输入；无法识别为 bgm.tv 条目时原样返回（保留用户自定义内容）。
// 失败场景：无。
// 副作用：无。
func NormalizeSubjectLink(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if id := ExtractSubjectID(trimmed); id != "" {
		return SubjectLink(id)
	}
	return trimmed
}

// BuildTitleCandidates 从种子侧标题集合生成匹配候选标题（保持传入顺序，即可信度降序）。
// 处理内容：剔除方括号包裹信息、按分隔符拆分中英标题、剔除结尾集数与季号、拆出中英片段、去重。
// 参数/返回：raws 为原始标题（允许为空或含空串）；返回候选标题切片（可能为空）。
// 失败场景：全部原始标题为空或无有效候选时返回空切片。
// 副作用：无。
func BuildTitleCandidates(raws ...string) []string {
	result := make([]string, 0, len(raws)*3)
	seen := make(map[string]struct{}, len(raws)*3)
	appendCandidate := func(value string) {
		cleaned := strings.Trim(strings.Join(strings.Fields(value), " "), titleTrimCutset)
		cleaned = strings.TrimSpace(cleaned)
		if cleaned == "" {
			return
		}
		length := utf8.RuneCountInString(cleaned)
		if length < 2 || length > 120 {
			return
		}
		// 纯数字/纯符号片段（如集数残留）无匹配意义。
		if !cjkRunPattern.MatchString(cleaned) && !latinRunPattern.MatchString(cleaned) {
			return
		}
		key := repository.NormalizeBangumiMatchTitle(cleaned)
		if key == "" {
			return
		}
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		result = append(result, cleaned)
	}

	for _, raw := range raws {
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		for _, segment := range strings.FieldsFunc(text, func(r rune) bool {
			for _, separator := range titleSegmentSeparators {
				if r == separator {
					return true
				}
			}
			return false
		}) {
			sanitized := bracketPattern.ReplaceAllString(segment, " ")
			appendCandidate(sanitized)
			episodeStripped := strings.TrimSpace(trailingEpisodePattern.ReplaceAllString(sanitized, ""))
			appendCandidate(episodeStripped)
			appendCandidate(strings.TrimSpace(trailingSeasonPattern.ReplaceAllString(episodeStripped, "")))
			// 中英连写（如“葬送的芙莉莲 Sousou no Frieren”）时拆出中文片段优先参与匹配。
			for _, run := range cjkRunPattern.FindAllString(episodeStripped, 2) {
				appendCandidate(run)
			}
			if latinRuns := latinRunPattern.FindAllString(episodeStripped, 2); len(latinRuns) > 0 {
				appendCandidate(latinRuns[0])
			}
		}
		// 整体标题兜底参与匹配（库内标题与种子标题完全一致的场景）。
		appendCandidate(bracketPattern.ReplaceAllString(text, " "))
	}
	return result
}

// PickBestItem 从候选条目中挑选最匹配的一条；条目缺少 bgm.tv ID 时视为不可用。
// 打分优先级：标题精确命中（原名/中文名）> 译名表命中 > 其它检索命中（TMDB / 译名子串），
// 同档位下按 TMDB 命中形态、类型（动画多为 tv）、年份接近度与播出时间消歧。
// 参数/返回：items 为候选条目；candidates 为候选标题（可信度降序）；year 为资源年份（可空）；
// tmdbID 为种子的 TMDB 数字 ID（可空，用于在多季条目间消歧）。
// 失败场景：无可选条目时返回 nil。
// 副作用：无。
func PickBestItem(items []repository.BangumiItem, candidates []string, year, tmdbID string) *repository.BangumiItem {
	if len(items) == 0 {
		return nil
	}
	index := make(map[string]int, len(candidates))
	for i, candidate := range candidates {
		key := repository.NormalizeBangumiMatchTitle(candidate)
		if key == "" {
			continue
		}
		if _, exists := index[key]; !exists {
			index[key] = i
		}
	}
	yearValue, _ := strconv.Atoi(strings.TrimSpace(year))

	var best *repository.BangumiItem
	bestScore := 0
	for i := range items {
		item := items[i]
		if strings.TrimSpace(item.BangumiID) == "" {
			continue
		}
		score := scoreBangumiItem(item, index, candidates, yearValue, strings.TrimSpace(tmdbID))
		if score <= 0 {
			continue
		}
		if best == nil || score > bestScore || (score == bestScore && item.BeginTimestamp > best.BeginTimestamp) {
			selected := item
			best = &selected
			bestScore = score
		}
	}
	return best
}

// scoreBangumiItem 计算单个候选条目的匹配得分，分值越高越可信，<=0 表示不可用。
func scoreBangumiItem(item repository.BangumiItem, index map[string]int, candidates []string, year int, tmdbID string) int {
	score := 0
	titleIndex := -1
	for _, title := range []string{item.Title, item.TitleZH} {
		key := repository.NormalizeBangumiMatchTitle(title)
		if key == "" {
			continue
		}
		if idx, ok := index[key]; ok && (titleIndex < 0 || idx < titleIndex) {
			titleIndex = idx
		}
	}
	switch {
	case titleIndex >= 0:
		score = 10000 - titleIndex*100
	default:
		if idx := translateCandidateIndex(item, candidates); idx >= 0 {
			score = 5000 - idx*100
		} else {
			score = 1000
		}
	}

	// TMDB 精确形态（tv/209867）比带季集后缀（tv/209867/season/1/episode/29）更可信。
	if tmdbID != "" {
		switch itemTmdbID := strings.TrimSpace(item.TmdbID); {
		case itemTmdbID == tmdbID || strings.HasSuffix(itemTmdbID, "/"+tmdbID):
			score += 2000
		case strings.Contains(itemTmdbID, "/"+tmdbID+"/"):
			score += 200
		}
	}

	// 动画条目在番组数据中以 tv 为主，同分时优先选择 tv。
	if strings.EqualFold(strings.TrimSpace(item.ItemType), "tv") {
		score += 60
	}
	if year > 0 && item.BeginTimestamp > 0 {
		diff := time.Unix(item.BeginTimestamp, 0).UTC().Year() - year
		if diff < 0 {
			diff = -diff
		}
		switch {
		case diff == 0:
			score += 300
		case diff == 1:
			score += 120
		case diff > 3:
			score -= 300
		}
	}
	return score
}

// translateCandidateIndex 在条目译名表（titleTranslate）中查找候选标题，返回命中的最小候选下标；未命中返回 -1。
// 先做归一化精确匹配，再做双向包含匹配，避免长译名被短别名抢先命中。
func translateCandidateIndex(item repository.BangumiItem, candidates []string) int {
	translations := repository.ParseBangumiTitleTranslate(item.TitleTransJSON)
	if len(translations) == 0 || len(candidates) == 0 {
		return -1
	}
	exactIndex, containsIndex := -1, -1
	for idx, candidate := range candidates {
		key := repository.NormalizeBangumiMatchTitle(candidate)
		if key == "" {
			continue
		}
		for _, values := range translations {
			for _, value := range values {
				translated := repository.NormalizeBangumiMatchTitle(value)
				if translated == "" {
					continue
				}
				if translated == key {
					if exactIndex < 0 || idx < exactIndex {
						exactIndex = idx
					}
					continue
				}
				if strings.Contains(translated, key) || strings.Contains(key, translated) {
					if containsIndex < 0 || idx < containsIndex {
						containsIndex = idx
					}
				}
			}
		}
	}
	if exactIndex >= 0 {
		return exactIndex
	}
	return containsIndex
}
