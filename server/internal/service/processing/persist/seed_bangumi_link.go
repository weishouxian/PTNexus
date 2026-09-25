package persist

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service/bangumi"
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
)

const bangumiLinkLogModule = "迁移-番组链接"

// AttachBangumiLinkToRow 在种子含动漫标签且尚无番组链接时，按库内番组数据匹配并写入 bgm.tv 详情链接。
// 参数/返回：lookup 为番组条目检索能力；normalized 为 get_db_seed_info 归一化数据；
// 命中时写入 normalized["bangumi_link"]，未命中或非动漫种子时不写入。
// 失败场景：检索失败仅记录日志，不影响主响应；库内已有链接（含人工修改结果）不覆盖。
// 副作用：仅读取 bangumi_items 表，可能原地写入 normalized 的 bangumi_link 键。
func AttachBangumiLinkToRow(lookup bangumi.ItemLookup, normalized map[string]any) {
	if lookup == nil || normalized == nil {
		return
	}
	if existing := strings.TrimSpace(toStringSimple(normalized["bangumi_link"])); existing != "" {
		// 库内已有链接：仅做一次规范化（裸 ID / 非规范链接 → 规范链接），不做重新匹配。
		normalized["bangumi_link"] = bangumi.NormalizeSubjectLink(existing)
		return
	}
	// 简介正文已带 bgm.tv 链接时优先采用（站点方给出，可信度最高），不再做库内匹配。
	if id := bangumi.ExtractSubjectID(toStringSimple(normalized["body"])); id != "" {
		normalized["bangumi_link"] = bangumi.SubjectLink(id)
		logx.Infof(bangumiLinkLogModule, "从简介正文提取番组链接 torrent_id=%s bangumi_id=%s", toStringSimple(normalized["torrent_id"]), id)
		return
	}
	// 动漫标签才参与匹配，避免给普通影视资源写入无关番组链接。
	if !seedHasAnimationTag(normalized) {
		return
	}

	titles := seedBangumiTitleCandidates(normalized)
	tmdbID := ExtractTmdbID(toStringSimple(normalized["tmdb_link"]))
	year := ExtractYearFromText(toStringSimple(normalized["title"]))
	if year == "" {
		year = ExtractYearFromText(toStringSimple(normalized["name"]))
	}
	link, bangumiID := bangumi.ResolveSubjectLink(lookup, bangumi.MatchInput{
		Titles: titles,
		TmdbID: tmdbID,
		Year:   year,
	})
	if link == "" {
		logx.Infof(bangumiLinkLogModule, "未匹配到番组条目 torrent_id=%s titles=%v tmdb_id=%s", toStringSimple(normalized["torrent_id"]), titles, tmdbID)
		return
	}
	normalized["bangumi_link"] = link
	logx.Infof(bangumiLinkLogModule, "匹配到番组条目 torrent_id=%s bangumi_id=%s link=%s", toStringSimple(normalized["torrent_id"]), bangumiID, link)
}

// seedHasAnimationTag 判断种子标签中是否含动漫/动画标记。
// 参数/返回：normalized 为归一化种子数据；命中返回 true。
// 失败场景：标签缺失或无法解析时返回 false。
// 副作用：无。
func seedHasAnimationTag(normalized map[string]any) bool {
	tags := ParseStringArray(normalized["tags"])
	if standardized, ok := normalized["standardized_params"].(map[string]any); ok && standardized != nil {
		tags = append(tags, ParseStringArray(standardized["tags"])...)
	}
	if sourceParams, ok := normalized["source_params"].(map[string]any); ok && sourceParams != nil {
		tags = append(tags, splitSeedTagText(toStringSimple(sourceParams["标签"]))...)
	}
	return processingtagging.HasAnimationTag(tags)
}

// splitSeedTagText 将站点原始标签文本拆分为标签切片（兼容 JSON 数组 / 逗号 / 顿号 / 空白分隔）。
func splitSeedTagText(raw string) []string {
	text := strings.TrimSpace(raw)
	if text == "" {
		return []string{}
	}
	// 仅当形如 JSON 数组时才按数组解析，避免把“动画 完结”这类空格分隔文本当成单个标签。
	if strings.HasPrefix(text, "[") {
		if parsed := ParseStringArray(text); len(parsed) > 0 {
			return parsed
		}
	}
	return strings.FieldsFunc(text, func(r rune) bool {
		switch r {
		case ',', '，', '、', ';', '；', '|', ' ', '\t', '\n', '[', ']', '"', '\'':
			return true
		default:
			return false
		}
	})
}

// seedBangumiTitleCandidates 汇总种子侧可用于番组匹配的标题，按可信度从高到低排列。
// 顺序：简介“译名”行 > 资源信息库标题 > 标题组件「主标题/副标题」 > 发布标题 > 种子名 > 副标题。
// 参数/返回：normalized 为归一化种子数据；返回去空后的标题切片。
// 失败场景：无。
// 副作用：无。
func seedBangumiTitleCandidates(normalized map[string]any) []string {
	candidates := make([]string, 0, 8)
	appendValue := func(value string) {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			candidates = append(candidates, trimmed)
		}
	}

	appendValue(ExtractTranslatedName(toStringSimple(normalized["body"])))
	if resourceInfo, ok := normalized["resource_info"].(map[string]any); ok && resourceInfo != nil {
		appendValue(toStringSimple(resourceInfo["title"]))
	}
	for _, component := range ParseAnyArray(normalized["title_components"]) {
		item, ok := component.(map[string]any)
		if !ok || item == nil {
			continue
		}
		switch strings.TrimSpace(toStringSimple(item["key"])) {
		case "主标题", "副标题":
			appendValue(toStringSimple(item["value"]))
		}
	}
	appendValue(toStringSimple(normalized["title"]))
	appendValue(toStringSimple(normalized["name"]))
	appendValue(toStringSimple(normalized["subtitle"]))
	return candidates
}
