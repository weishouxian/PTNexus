package extract

import (
	"regexp"
	"strings"
)

// PromotionInfo 表示从详情页判定出的优惠信息（目前只关心「是否免费」）。
//
// Known 用于区分「确认不是免费」与「页面上根本没识别到优惠信息」——
// 两者对「仅下载免费种」的策略含义不同：前者应跳过，后者属于无法确认，同样应跳过但要给出不同提示，
// 否则用户会误以为是站点没有免费种，而不是解析没覆盖到。
type PromotionInfo struct {
	Known bool   // 是否成功识别到优惠状态
	Free  bool   // 是否免费
	Label string // 命中的原始文案，便于日志排查（截断后使用）
}

var (
	// 免费关键词：中文「免费」与英文 free/freeleech。
	// ⚠️ 只在标题原文与「优惠/促销」标签附近匹配，绝不全页扫描 ——
	// 简介正文里常见「禁止免费转载」「免费注册」等表述，全页扫描会大量误判。
	rePromotionFreeWord = regexp.MustCompile(`(?i)免费|freeleech|\bfree\b`)

	// 顶部标题里的徽标原文，如 [免费]、[Free]、[免费][优惠]。
	rePromotionBadge = regexp.MustCompile(`(?i)\[[^\]]*(?:免费|free|优惠|折扣|促销|2x|50%|30%)[^\]]*\]`)

	// 「优惠/促销」标签节点：`优惠</td><td>…免费…</td>` 或 `优惠：免费` 这类结构。
	// 取标签后最多 200 字（跨标签），足够覆盖一个单元格内容，又不会把正文吃进来。
	rePromotionLabeledCell = regexp.MustCompile(`(?is)(?:优惠|促销|promotion)\s*(?:</?[^>]+>\s*)*[:：]?\s*(?:</?[^>]+>\s*){0,6}(.{0,200})`)
)

// DetectPromotionFromDetail 从详情页 HTML 判定优惠信息。
//
// 判定顺序（前者命中即返回）：
//  1. 顶部标题原文里的徽标（[免费] / [Free] / [优惠]）—— NexusPHP 详情页最通用的位置，
//     且标题徽标正是标题清洗逻辑 cleanTopTitleText 会剥掉的部分，误判率最低；
//  2. 「优惠 / 促销 / promotion」标签后的单元格文本，命中免费关键词即视为免费。
//
// 参数/返回：pageHTML 为详情页 HTML 原文；返回判定结果（无法判定时 Known=false）。
// 说明：只做「是否免费」的粗判，不区分 2X / 50% 等具体折扣类型；
// 免费以外的折扣不算免费（下载量仍然 > 0）。
// 副作用：无。
func DetectPromotionFromDetail(pageHTML string) PromotionInfo {
	page := strings.TrimSpace(pageHTML)
	if page == "" {
		return PromotionInfo{}
	}

	// 1. 顶部标题徽标
	if match := reTopTitle.FindStringSubmatch(page); len(match) >= 2 {
		titleHTML := match[1]
		if badge := rePromotionBadge.FindString(titleHTML); badge != "" {
			free := rePromotionFreeWord.MatchString(badge)
			return PromotionInfo{Known: true, Free: free, Label: compactPromotionLabel(badge)}
		}
	}

	// 2. 优惠标签后的单元格
	if match := rePromotionLabeledCell.FindStringSubmatch(page); len(match) >= 2 {
		cell := sanitizeHTMLText(match[1], true)
		cell = strings.Join(strings.Fields(cell), " ")
		if cell != "" {
			free := rePromotionFreeWord.MatchString(cell)
			return PromotionInfo{Known: true, Free: free, Label: compactPromotionLabel(cell)}
		}
	}

	return PromotionInfo{}
}

// compactPromotionLabel 截断命中文案，避免把整段 HTML 文本写进日志。
func compactPromotionLabel(text string) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	const maxRunes = 40
	runes := []rune(trimmed)
	if len(runes) <= maxRunes {
		return trimmed
	}
	return string(runes[:maxRunes]) + "…"
}
