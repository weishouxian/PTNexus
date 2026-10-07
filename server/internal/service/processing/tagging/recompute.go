package tagging

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
)

// RecomputeStandardTags 根据当前种子文本信息重新计算标准化 tags。
// 参数/返回：siteCode 用于读取站点 tag 映射；title/subtitle/statement/body/mediainfo/titleComponents 为种子信息；medium 为当前标准媒介（用于校正媒介类标签）；
// savePath/torrentNameForPath/downloaderID/rootConfig 用于完结检测；
// existingTags 会被当作候选来源之一（避免丢失已存在的有效标准标签）。
// 失败场景：映射配置缺失或媒体文本为空时，会输出更少的标签，但不会返回错误。
func RecomputeStandardTags(
	siteCode string,
	title string,
	subtitle string,
	statement string,
	body string,
	mediainfo string,
	medium string,
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

	// 本体已落到原盘档且 BDInfo 碟指纹提示 DIY 时补 DIY 原始标签，
	// 交由 MapTagsToStandard 按站点过滤（站点没配 tag.DIY 映射时自然被丢弃）。
	if shouldAddDIY, reason := ShouldAddDiscDIYRawTag(medium, mediainfo, torrentNameForPath); shouldAddDIY {
		rawTagCandidates = append(rawTagCandidates, "DIY")
		logx.Infof(tagCompletionLogModule, "按碟指纹补 DIY 标签 torrent_name=%s 依据=%s", strings.TrimSpace(torrentNameForPath), reason)
	}

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
	mappedTags = ReconcileMediumTagsWithStandardMedium(mappedTags, medium)
	// 简介「类别」行写“纪录/纪录片”时同步纠正类型，保证重算链路与抓取链路口径一致
	// （未审核种子重算时会回写 seed_parameters.type）。
	typeOverride := parser.InferTypeFromDescriptionCategory(description)
	return mappedTags, typeOverride, unmappedTags
}

// ShouldAddDiscDIYRawTag 判断是否应补 DIY 原始标签，并返回命中的碟指纹证据（供日志追溯）。
// 参数/返回：medium 为当前标准媒介；mediaText 为种子本体媒体文本；releaseName 为发布名；返回是否补标签与证据说明。
// 失败场景：媒介不在原盘档、文本非 BDInfo 或无指纹命中时返回 false 与空说明。
// 副作用：无。
//
// 前置门禁是「媒介属蓝光原盘档」：媒介落到原盘档意味着碟结构已被物理证据确认过
// （抓取期文件列表含 .iso/BDMV，或刷新期确认本体跑出了 BDInfo）。没有这道门禁，
// 「源站详情页贴源盘 BDInfo」的 Remux 种子（媒介仍是 medium.remux）会被整批误标 DIY。
func ShouldAddDiscDIYRawTag(medium, mediaText, releaseName string) (bool, string) {
	if !processingmedia.IsBlurayDiscMedium(medium) {
		return false, ""
	}
	return processingmedia.IsDIYDiscByBDInfo(mediaText, releaseName)
}

// ReconcileMediumTagsWithStandardMedium 以最终标准媒介为准校正媒介类标签：剔除已失效的 Remux 标签。
// 参数/返回：tags 为已映射的标准标签（tag.*）；medium 为当前标准媒介；返回校正后的标签列表。
// 失败场景：媒介为空或仍含 Remux 时原样返回，不做删减。
// 副作用：无。
//
// 背景：标题写 “…BluRay.Remux…” 但本体是 ISO/BDMV 原盘的转种很常见，媒介已被碟结构纠偏
// 收敛为 medium.bluray，而标签是从标题组件推的，仍留着 tag.Remux，发到站点会同时勾上原盘与 Remux。
func ReconcileMediumTagsWithStandardMedium(tags []string, medium string) []string {
	if len(tags) == 0 || !processingmedia.ShouldDropRemuxTag(medium) {
		return tags
	}
	result := make([]string, 0, len(tags))
	for _, tag := range tags {
		if strings.EqualFold(strings.TrimSpace(tag), "tag.Remux") {
			continue
		}
		result = append(result, tag)
	}
	return result
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
