package persist

import (
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
)

// FetchRestrictionPrecheckResult 表示抓取修复前的受限标签预检结果。
type FetchRestrictionPrecheckResult struct {
	Matched        bool
	SkipRepairs    bool
	RestrictedTags []string
	Reason         string
}

// DetectFetchRestrictionPrecheck 在抓取修复前预检受限标签，命中时用于短路后续修复。
//
// 「禁转」命中即短路 —— 禁转资源永久不可转发，没必要再花时间做抓取修复。
// 「限转」不短路 —— 限转是限时约束（如发布后 N 天内禁转），到期可正常转发，提前备好截图/简介更合理。
// 「分集」也不短路 —— 能否发布只由目标站的 allow_episode_publish 决定，
// 抓取阶段先按正常流程把截图/简介备齐，避免站点开启分集后发布时数据残缺。
func DetectFetchRestrictionPrecheck(
	draft *SeedDraft,
	siteIdentifier string,
	savePath string,
	torrentNameForPath string,
	downloaderID string,
	rootConfig map[string]any,
) FetchRestrictionPrecheckResult {
	if draft == nil {
		return FetchRestrictionPrecheckResult{}
	}

	restricted := processingtagging.DetectRestrictedTags(draft.RawTags)
	// 「限转」不短路修复：限转通常为限时约束（如发布后 N 天内禁转），到期后可正常转发，
	// 提前把截图/简介备好比等到限转期过了再补更合理。仅「禁转」参与短路判定。
	restricted = filterOutLimitedTransferTag(restricted)
	// 「分集」同样从短路名单剔除：分集发布只看目标站配置，抓取阶段不拦。
	restricted = filterOutEpisodeTag(restricted)
	if len(restricted) > 0 {
		return FetchRestrictionPrecheckResult{
			Matched:        true,
			SkipRepairs:    true,
			RestrictedTags: restricted,
			Reason:         "源站显式标签命中受限规则",
		}
	}

	_ = siteIdentifier
	_ = savePath
	_ = torrentNameForPath
	_ = downloaderID
	_ = rootConfig
	return FetchRestrictionPrecheckResult{}
}

// filterOutEpisodeTag 从标准受限标签列表中剔除「分集」，保留禁转（限转已在上游剔除）。
// 参数/返回：tags 为 DetectRestrictedTags 的结果；返回不含分集的新切片。
// 副作用：无。
func filterOutEpisodeTag(tags []string) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if processingtagging.IsEpisodeRestrictedTag(tag) {
			continue
		}
		result = append(result, tag)
	}
	return result
}

// filterOutLimitedTransferTag 从标准受限标签列表中剔除「限转」，保留禁转/分集。
// 参数/返回：tags 为 DetectRestrictedTags 的结果；返回不含限转的新切片。
// 副作用：无。
func filterOutLimitedTransferTag(tags []string) []string {
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if processingtagging.IsLimitedTransferTag(tag) {
			continue
		}
		result = append(result, tag)
	}
	return result
}
