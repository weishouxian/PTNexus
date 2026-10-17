package persist

import (
	"strings"

	"github.com/pt-nexus/server/internal/config"
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
// 「禁转/限转」命中即短路 —— 这类资源不会发布，没必要再花时间做抓取修复。
// 「分集」是否短路取决于全局总开关 ALLOW_EPISODE_PUBLISH：
//   - 关闭（默认）⇒ 同样短路，与既有行为一致；
//   - 开启 ⇒ 不短路，让分集种子正常走修复流程。否则自动发种虽然放行了下载，
//     下游却因修复被跳过而拿不到完整信息，发布时仍然是残缺数据。
//
// 判据只认全局开关，不看具体站点 —— 这里发生在抓取阶段，尚无目标站概念；
// 目标站级别的开关留到发布时由 publish_entry 裁决。
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

	allowEpisode := config.AllowsEpisodePublish()

	restricted := processingtagging.DetectRestrictedTags(draft.RawTags)
	if allowEpisode {
		// 分集开关打开时，把「分集」从短路名单里剔除；禁转/限转照旧拦截。
		restricted = filterOutEpisodeTag(restricted)
	}
	if len(restricted) > 0 {
		return FetchRestrictionPrecheckResult{
			Matched:        true,
			SkipRepairs:    true,
			RestrictedTags: restricted,
			Reason:         "源站显式标签命中受限规则",
		}
	}
	if allowEpisode {
		// 分集标签已被放行：不再做自动分集检测短路，直接进入正常修复流程。
		_ = siteIdentifier
		return FetchRestrictionPrecheckResult{}
	}

	description := strings.TrimSpace(strings.Join([]string{draft.Statement, draft.Body}, "\n"))
	completion := processingtagging.CheckCompletionStatusWithDownloaderContext(
		draft.Title,
		draft.Subtitle,
		description,
		processingtagging.CompletionCheckContext{
			SavePath:     savePath,
			TorrentName:  torrentNameForPath,
			ContentName:  draft.Title,
			DownloaderID: downloaderID,
			RootConfig:   rootConfig,
		},
	)
	episodeTagResult := processingtagging.DetectEpisodeTag(processingtagging.EpisodeTagInput{
		Title:            draft.Title,
		Subtitle:         draft.Subtitle,
		TorrentName:      torrentNameForPath,
		Type:             draft.Type,
		ExistingTags:     draft.RawTags,
		TorrentFileNames: draft.TorrentFileNames,
		Completion:       completion,
	})
	if episodeTagResult.Matched {
		return FetchRestrictionPrecheckResult{
			Matched:        true,
			SkipRepairs:    true,
			RestrictedTags: []string{processingtagging.StandardEpisodeTag},
			Reason:         episodeTagResult.Reason,
		}
	}

	_ = siteIdentifier
	return FetchRestrictionPrecheckResult{}
}

// filterOutEpisodeTag 从标准受限标签列表中剔除「分集」，保留禁转/限转。
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
