package tagging

import (
	"strings"

	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
)

// RecomputeStandardTags 根据当前种子文本信息重新计算标准化 tags。
// 参数/返回：siteCode 用于读取站点 tag 映射；title/subtitle/statement/body/mediainfo/titleComponents 为种子信息；savePath/torrentNameForPath/downloaderID/rootConfig 用于完结检测；
// existingTags 会被当作候选来源之一（避免丢失已存在的有效标准标签）。
// 失败场景：映射配置缺失或媒体文本为空时，会输出更少的标签，但不会返回错误。
func RecomputeStandardTags(
	siteCode string,
	title string,
	subtitle string,
	statement string,
	body string,
	mediainfo string,
	titleComponents []any,
	savePath string,
	torrentNameForPath string,
	downloaderID string,
	rootConfig map[string]any,
	existingTags []string,
) ([]string, string, []string) {
	description := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(statement), strings.TrimSpace(body)}, "\n"))

	rawTagCandidates := make([]string, 0, len(existingTags)+24)
	rawTagCandidates = append(rawTagCandidates, existingTags...)
	rawTagCandidates = append(rawTagCandidates, ExtractRawTagsFromTitleComponents(AnyTitleComponentsToMaps(titleComponents))...)
	rawTagCandidates = append(rawTagCandidates, ExtractRawTagsFromSubtitle(subtitle)...)
	rawTagCandidates = append(rawTagCandidates, ExtractTagsFromDescriptionCategory(description)...)
	rawTagCandidates = append(rawTagCandidates, ExtractTagsFromDescriptionScore(description)...)

	_, isBDInfo, _ := processingmedia.ValidateMediaInfoFormat(strings.TrimSpace(mediainfo))
	rawTagCandidates = append(rawTagCandidates, ExtractRawTagsFromMediaText(mediainfo, isBDInfo)...)

	// 以 MediaInfo/BDInfo 的实际音轨语种为准，剔除源站误标的中文语种标签。
	// 典型场景：源站详情页「标签」字段挂了「国语」，但文件只有一条 English 音轨
	//（屌丝站 Until We Meet Again 2026 S01 实例）。媒体文本无音轨语种线索时不改动。
	rawTagCandidates = ReconcileAudioLanguageTagsWithMediaText(rawTagCandidates, mediainfo, isBDInfo)

	contentName := strings.TrimSpace(title)
	if contentName == "" {
		contentName = strings.TrimSpace(torrentNameForPath)
	}
	completion := CheckCompletionStatusWithDownloaderContext(
		title,
		subtitle,
		description,
		CompletionCheckContext{
			SavePath:     savePath,
			TorrentName:  torrentNameForPath,
			ContentName:  contentName,
			DownloaderID: downloaderID,
			RootConfig:   rootConfig,
		},
	)
	if ShouldAddCompletionTag(rawTagCandidates, completion) {
		rawTagCandidates = append(rawTagCandidates, "完结")
	}

	mappedTags, unmappedTags := MapTagsToStandard(rawTagCandidates, siteCode)
	// 简介「类别」行写“纪录/纪录片”时同步纠正类型，保证重算链路与抓取链路口径一致
	// （未审核种子重算时会回写 seed_parameters.type）。
	typeOverride := parser.InferTypeFromDescriptionCategory(description)
	return mappedTags, typeOverride, unmappedTags
}

// AnyTitleComponentsToMaps 将任意数组过滤为有效 title_components 结构切片。
func AnyTitleComponentsToMaps(items []any) []map[string]any {
	if len(items) == 0 {
		return []map[string]any{}
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		component, ok := item.(map[string]any)
		if !ok {
			continue
		}
		key := strings.TrimSpace(toStringAny(component["key"], ""))
		if key == "" {
			continue
		}
		result = append(result, component)
	}
	return result
}

// TagSample 返回标签采样切片，避免日志过长。
func TagSample(tags []string, limit int) []string {
	if limit <= 0 {
		limit = 8
	}
	if len(tags) <= limit {
		return tags
	}
	return tags[:limit]
}
