package tagging

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 副标题季集补全相关正则。
// 目标形态：给电视剧/动漫的「整季合集」副标题补写「第x季 全y集」，单集种子与已含「全X集」的副标题不动。
var (
	// reSubtitleTotalEpisodeToken 判断副标题是否已含「全12集 / 全12話 / 全12期」，命中即视为已补齐。
	reSubtitleTotalEpisodeToken = regexp.MustCompile(`(?i)全\s*[0-9０-９]{1,4}\s*[集话話期]`)

	// reSeasonEpisodeSignal 剧集特征（严格版）：Sxx / SxxExx / 第X季。
	// 刻意不用 hasEpisodeToken 的宽口径（含「第N期」），避免综艺误判为剧集。
	reSeasonEpisodeSignal = regexp.MustCompile(`(?i)\bS[0-9]{1,2}(?:E[0-9]{1,3})?\b|第\s*[0-9０-９一二三四五六七八九十]{1,3}\s*季`)

	// reSubtitleSeasonToken 判断副标题内部是否已带季号（第X季 / Sxx），命中时只补「全y集」。
	reSubtitleSeasonToken = regexp.MustCompile(`(?i)第\s*[0-9０-９一二三四五六七八九十]{1,3}\s*季|\bS[0-9]{1,2}\b`)

	// 季号解析：第3季 / 第三季 / S03(E05) / Season 3。
	reSeasonNumberArabic = regexp.MustCompile(`第\s*([0-9０-９]{1,2})\s*季`)
	reSeasonNumberCN     = regexp.MustCompile(`第\s*([一二三四五六七八九十]{1,3})\s*季`)
	reSeasonNumberSxx    = regexp.MustCompile(`(?i)\bS([0-9]{1,2})(?:E[0-9]{1,3})?\b`)
	reSeasonNumberEN     = regexp.MustCompile(`(?i)\bSeason\s*([0-9]{1,2})\b`)

	// 简介「◎季　　数: 3」行（与 extractTotalEpisodesFromDescription 同构）。
	reSeasonCountInDescription = []*regexp.Regexp{
		regexp.MustCompile(`(?i)[◎❁][\s　]*季[\s　]*数[\s　]*[:：]?[\s　]*([0-9]{1,2})`),
		regexp.MustCompile(`(?i)季[\s　]*数[\s　]*[:：][\s　]*([0-9]{1,2})`),
		regexp.MustCompile(`(?i)Seasons?[\s　]*[:：][\s　]*([0-9]{1,2})`),
	}
)

// ChineseSeasonDigits 中文数字（用于「第三季」形态的季号解析）。
var chineseSeasonDigits = map[rune]int{
	'一': 1, '二': 2, '三': 3, '四': 4, '五': 5,
	'六': 6, '七': 7, '八': 8, '九': 9,
}

// SubtitleSeasonEpisodeInput 定义副标题季集补全所需输入。
type SubtitleSeasonEpisodeInput struct {
	Subtitle string

	// Title 为主标题/种子名（draft.Title 即 torrent 名）。
	Title string
	// SeasonEpisode 为标题组件「季集」值（形如 S03 / S03E05 / S03E01-E12），可为空。
	SeasonEpisode string
	// Description 为简介全文（statement + body），用于「季　　数」「集　　数」兜底。
	Description string
	// Type 为标准化分类键（category.tv_series 等）。
	Type string
	// Tags 为当前候选标签，用于识别动漫。
	Tags []string
	// TorrentFileNames 为种子内文件名列表，用于集数统计与整季包判定。
	TorrentFileNames []string
	// Completion 为完结判定结果，提供总集数与本地集数。
	Completion CompletionStatus
}

// EnrichSubtitleWithSeasonEpisode 为电视剧/动漫的整季合集副标题补写「第x季 全y集」。
// 返回加工后的副标题与变更原因；未发生变更时原样返回副标题并返回空原因。
// 规则：
//  1. 副标题已含「全X集」→ 原样返回（幂等）；
//  2. 非电视剧/动漫（分类、动漫标签、季集特征三者任一命中即算）→ 原样返回；
//  3. 非整季合集（集数未覆盖整季）→ 原样返回；
//  4. 季号 x：标题组件「季集」→ 主标题/种子名 → 简介「季数」；均取不到则只写「全y集」；
//  5. 集数 y：简介「集　　数」优先，本地/种子文件集数兜底；
//  6. 拼装：插入到副标题第一个空格之后；副标题无空格或为空则追加到末尾。
func EnrichSubtitleWithSeasonEpisode(input SubtitleSeasonEpisodeInput) (string, string) {
	subtitle := strings.TrimSpace(input.Subtitle)
	if reSubtitleTotalEpisodeToken.MatchString(subtitle) {
		return subtitle, ""
	}
	if !looksLikeTVSeriesContent(input) {
		return subtitle, ""
	}

	total := resolveSeasonEpisodeTotal(input)
	if total == nil || *total <= 0 {
		return subtitle, ""
	}
	if !isSeasonPackForSeasonEpisode(input, *total) {
		return subtitle, ""
	}

	season, hasSeason := resolveSeasonEpisodeNumber(input)
	hasSeasonInSubtitle := reSubtitleSeasonToken.MatchString(subtitle)

	fragment := ""
	if hasSeason && !hasSeasonInSubtitle {
		fragment = fmt.Sprintf("第%d季 全%d集", season, *total)
	} else {
		fragment = fmt.Sprintf("全%d集", *total)
	}

	enriched := insertSeasonEpisodeFragment(subtitle, fragment)
	if enriched == subtitle {
		return subtitle, ""
	}
	return enriched, "副标题补写「" + fragment + "」"
}

// looksLikeTVSeriesContent 判断当前资源是否属于「电视剧/动漫」。
// 判定顺序：标准化分类 → 动漫标签 → 标题/季集组件/副标题的季集特征，任一命中即算。
func looksLikeTVSeriesContent(input SubtitleSeasonEpisodeInput) bool {
	switch strings.TrimSpace(input.Type) {
	case "category.tv_series", "category.tv_shows", "category.tv", "category.animation",
		"剧集", "电视剧", "动漫", "动画":
		return true
	}
	if HasAnimationTag(input.Tags) {
		return true
	}
	for _, text := range []string{input.Title, input.SeasonEpisode, input.Subtitle} {
		if reSeasonEpisodeSignal.MatchString(strings.TrimSpace(text)) {
			return true
		}
	}
	return false
}

// resolveSeasonEpisodeTotal 解析「全y集」的 y。
// 顺序：简介「集　　数」→ 完结判定的总集数 → 本地集数（需 >1，避免单集被当作全集数）→ 种子文件集数（需 >1）。
func resolveSeasonEpisodeTotal(input SubtitleSeasonEpisodeInput) *int {
	if total := extractTotalEpisodesFromDescription(input.Description); total != nil {
		return total
	}
	if input.Completion.TotalEpisodes != nil && *input.Completion.TotalEpisodes > 0 {
		return input.Completion.TotalEpisodes
	}
	if input.Completion.LocalEpisodes != nil && *input.Completion.LocalEpisodes > 1 {
		return input.Completion.LocalEpisodes
	}
	if stats := collectEpisodeFileStats(input.TorrentFileNames); stats.UniqueEpisodes > 1 {
		count := stats.UniqueEpisodes
		return &count
	}
	return nil
}

// isSeasonPackForSeasonEpisode 判断是否为「整季包/合集」：识别到的集数覆盖整季。
// 单集种子（本地与种子文件均只识别到 1 集）会被排除。
func isSeasonPackForSeasonEpisode(input SubtitleSeasonEpisodeInput, total int) bool {
	if local := input.Completion.LocalEpisodes; local != nil && *local > 1 && *local >= total {
		return true
	}
	if stats := collectEpisodeFileStats(input.TorrentFileNames); stats.UniqueEpisodes > 1 && stats.UniqueEpisodes >= total {
		return true
	}
	return false
}

// resolveSeasonEpisodeNumber 解析季号 x：标题组件「季集」→ 主标题/种子名 → 简介「季数」。
func resolveSeasonEpisodeNumber(input SubtitleSeasonEpisodeInput) (int, bool) {
	if season, ok := parseSeasonNumber(input.SeasonEpisode); ok {
		return season, true
	}
	if season, ok := parseSeasonNumber(input.Title); ok {
		return season, true
	}
	for _, re := range reSeasonCountInDescription {
		match := re.FindStringSubmatch(input.Description)
		if len(match) < 2 {
			continue
		}
		if value, err := strconv.Atoi(strings.TrimSpace(match[1])); err == nil && value > 0 {
			return value, true
		}
	}
	return 0, false
}

// parseSeasonNumber 从任意文本中解析季号，支持 第3季 / 第三季 / S03(E05) / Season 3 四种形态。
func parseSeasonNumber(text string) (int, bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, false
	}
	if match := reSeasonNumberArabic.FindStringSubmatch(trimmed); len(match) >= 2 {
		if value, err := strconv.Atoi(normalizeFullWidthDigits(match[1])); err == nil && value > 0 {
			return value, true
		}
	}
	if match := reSeasonNumberCN.FindStringSubmatch(trimmed); len(match) >= 2 {
		if value, ok := chineseSeasonNumberToInt(match[1]); ok {
			return value, true
		}
	}
	if match := reSeasonNumberSxx.FindStringSubmatch(trimmed); len(match) >= 2 {
		if value, err := strconv.Atoi(match[1]); err == nil && value > 0 {
			return value, true
		}
	}
	if match := reSeasonNumberEN.FindStringSubmatch(trimmed); len(match) >= 2 {
		if value, err := strconv.Atoi(match[1]); err == nil && value > 0 {
			return value, true
		}
	}
	return 0, false
}

// chineseSeasonNumberToInt 将「三 / 十 / 十二 / 二十三」等中文数字转为整数（仅支持 1-99）。
func chineseSeasonNumberToInt(text string) (int, bool) {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return 0, false
	}
	total := 0
	if runes[0] == '十' {
		total = 10
		runes = runes[1:]
	}
	for _, r := range runes {
		if r == '十' {
			if total == 0 {
				total = 1
			}
			total *= 10
			continue
		}
		digit, ok := chineseSeasonDigits[r]
		if !ok {
			return 0, false
		}
		total += digit
	}
	if total <= 0 {
		return 0, false
	}
	return total, true
}

// insertSeasonEpisodeFragment 将季集片段插入副标题：优先插到第一个空格（含全角）之后，无空格则追加到末尾。
func insertSeasonEpisodeFragment(subtitle, fragment string) string {
	sub := strings.TrimSpace(subtitle)
	if sub == "" {
		return fragment
	}
	idx := strings.IndexAny(sub, " \u3000")
	if idx <= 0 {
		return strings.TrimSpace(sub + " " + fragment)
	}
	head := strings.TrimSpace(sub[:idx])
	tail := strings.TrimSpace(sub[idx+1:])
	switch {
	case head == "":
		return strings.TrimSpace(fragment + " " + tail)
	case tail == "":
		return strings.TrimSpace(head + " " + fragment)
	default:
		return strings.TrimSpace(head + " " + fragment + " " + tail)
	}
}

// normalizeFullWidthDigits 将全角数字（０-９）归一为半角，便于 strconv 解析。
func normalizeFullWidthDigits(text string) string {
	var builder strings.Builder
	for _, r := range text {
		if r >= '０' && r <= '９' {
			builder.WriteRune(r - '０' + '0')
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}
