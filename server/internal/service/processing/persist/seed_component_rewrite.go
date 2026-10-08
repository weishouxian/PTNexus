package persist

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
)

const seedComponentRewriteLogModule = "媒体信息刷新"

// SeedParameterUpdater 定义媒体信息刷新时需要的最小写库接口。
type SeedParameterUpdater interface {
	UpdateSeedParameterByKey(hash, torrentID, siteName string, updates map[string]any) error
}

// RewriteSeedTitleComponentsByMediaInfo 使用媒体文本重建标题组件并回写数据库（仅调用方允许时执行）。
// 参数/返回：row 为 seed_parameters 当前行；repo 为写库接口；mediaInfoText 为本次刷新得到的媒体文本；
// discStructure 表示调用方已物理确认种子本体是碟结构（ISO/BDMV）；返回是否执行了更新与是否命中媒体格式。
// 失败场景：标题为空、媒体格式未命中、序列化失败或写库失败时返回 false。
// 副作用：可能写入 seed_parameters.title_components 与 seed_parameters.medium。
func RewriteSeedTitleComponentsByMediaInfo(
	logModule string,
	repo SeedParameterUpdater,
	hash string,
	torrentID string,
	siteName string,
	now time.Time,
	row map[string]any,
	mediaInfoText string,
	discStructure bool,
) (bool, bool, bool) {
	if repo == nil || row == nil {
		return false, false, false
	}
	if strings.TrimSpace(logModule) == "" {
		logModule = seedComponentRewriteLogModule
	}

	title := strings.TrimSpace(toStringSimple(row["title"]))
	if title == "" {
		title = strings.TrimSpace(toStringSimple(row["name"]))
	}

	// 媒体文本类型先判一次：它同时决定「标题 Blu-ray 写法」与「媒介纠偏」是否成立。
	isMediainfo, isBDInfo, formatReason := processingmedia.ValidateMediaInfoFormat(strings.TrimSpace(mediaInfoText))
	if !(isMediainfo || isBDInfo) {
		logx.Warnf(logModule, "标题组件回写跳过：seed_id=%s_%s_%s 媒体格式未命中 reason=%s", hash, torrentID, siteName, formatReason)
		return false, false, false
	}

	// 年份与产地同口径以简介为准：媒体文本刷新会按标题重建组件，若不回填会把简介年份退回标题年份。
	description := strings.TrimSpace(strings.Join([]string{toStringSimple(row["statement"]), toStringSimple(row["body"])}, "\n"))

	// 媒介必须先算：标题组件「媒介」由标题文本推导，标题里的 Remux 声明是否仍有效取决于标准媒介。
	mediumBefore := strings.TrimSpace(toStringSimple(row["medium"]))
	mediumBefore = processingtitle.PreferExplicitTitleMedium(mediumBefore, title, mediaInfoText)
	mediumAfter := processingmedia.NormalizeMediumByMediaType(mediumBefore, isMediainfo, isBDInfo)

	// 碟结构纠偏：本体已被物理确认是 ISO/BDMV 原盘（刷新链路由「对本体成功跑出 BDInfo」确认）。
	// NormalizeMediumByMediaType 的 isBDInfo 分支刻意不覆盖 medium.remux（防「源站详情页贴源盘 BDInfo」的误判），
	// 而此处拿到的是本体自身的物理证据，允许越过该保守保留，把 medium.remux 收敛回原盘档。
	if discStructure && isBDInfo {
		resolution := discStructureResolutionForRow(row, title, mediaInfoText, description)
		if converged := processingmedia.ConvergeMediumByConfirmedDiscStructure(mediumAfter, resolution); converged != mediumAfter {
			logx.Infof(
				logModule,
				"媒介碟结构纠偏：seed_id=%s_%s_%s medium_before=%s medium_after=%s resolution=%s",
				hash, torrentID, siteName, mediumAfter, converged, resolution,
			)
			mediumAfter = converged
		}
	}

	// 媒介已明确不是 Remux（碟结构收敛为原盘、或被判为 encode 等）时，标题里的 Remux 媒介标记已失效：
	// 必须先把标题摘干净再造组件，否则面板「媒介」会停在 “Blu-ray Remux”，与标准 medium 矛盾。
	finalTitle := title
	if processingmedia.ShouldDropRemuxTag(mediumAfter) {
		if stripped := processingmedia.StripRemuxMediumToken(title); stripped != "" && stripped != title {
			logx.Infof(logModule, "标题 Remux 标记摘除：seed_id=%s_%s_%s medium=%s before=%q after=%q",
				hash, torrentID, siteName, mediumAfter, title, stripped)
			finalTitle = stripped
		}
	}

	// 音频编码同样以媒体文本为准：标题音频 token 与首条音轨矛盾时替换（如标题 DDP2.0、首音轨 AAC），
	// 否则重建出的组件「音频编码」会继续停留在标题声明上。须在组件重建之前替换 finalTitle。
	if inferred := parser.InferStandardizedValues(finalTitle, mediaInfoText, description); inferred != nil {
		if standard := strings.TrimSpace(inferred["audio_codec"]); strings.HasPrefix(standard, "audio.") {
			if replaced := processingmedia.ReplaceTitleAudioCodecToken(finalTitle, standard); replaced != "" && replaced != finalTitle {
				logx.Infof(logModule, "标题音频标记替换：seed_id=%s_%s_%s audio_codec=%s before=%q after=%q",
					hash, torrentID, siteName, standard, finalTitle, replaced)
				finalTitle = replaced
			}
		}
	}

	result := processingtitle.BuildTitleComponentsForStorage(finalTitle, mediaInfoText, processingtitle.BuildSimpleTitleComponentsWithMediaInfo)
	if !(result.IsMediainfo || result.IsBDInfo) {
		logx.Warnf(logModule, "标题组件回写跳过：seed_id=%s_%s_%s 媒体格式未命中 reason=%s", hash, torrentID, siteName, result.Reason)
		return false, false, false
	}
	if len(result.Components) == 0 {
		logx.Warnf(logModule, "标题组件回写跳过：seed_id=%s_%s_%s 解析结果为空", hash, torrentID, siteName)
		return false, true, false
	}

	if year := strings.TrimSpace(parser.InferYearFromDescription(description)); year != "" {
		if before := titleComponentValue(result.Components, "年份"); before != year {
			result.Components = processingtitle.OverrideTitleComponentValue(result.Components, "年份", year)
			logx.Infof(logModule, "标题组件年份纠偏：seed_id=%s_%s_%s before=%s after=%s", hash, torrentID, siteName, before, year)
		}
	}

	encoded, err := json.Marshal(result.Components)
	if err != nil {
		logx.Warnf(logModule, "标题组件回写失败：seed_id=%s_%s_%s 序列化失败 err=%v", hash, torrentID, siteName, err)
		return false, true, false
	}

	nowText := now.Format("2006-01-02 15:04:05")
	logx.Infof(logModule, "标题组件回写开始：seed_id=%s_%s_%s is_mediainfo=%t is_bdinfo=%t reason=%s", hash, torrentID, siteName, result.IsMediainfo, result.IsBDInfo, result.Reason)
	titleUpdates := map[string]any{
		"title_components": string(encoded),
		"updated_at":       nowText,
	}
	// 标题被摘除 Remux 后必须一起回写：面板「原始/待解析标题」与发种标题都取自 title 字段。
	if finalTitle != title && finalTitle != "" {
		titleUpdates["title"] = finalTitle
	}
	writeErr := repo.UpdateSeedParameterByKey(hash, torrentID, siteName, titleUpdates)
	if writeErr != nil {
		logx.Warnf(logModule, "标题组件回写失败：seed_id=%s_%s_%s err=%v", hash, torrentID, siteName, writeErr)
		return false, true, false
	}
	logx.Infof(logModule, "标题组件回写完成：seed_id=%s_%s_%s components=%d", hash, torrentID, siteName, len(result.Components))
	mediaType := "BDInfo"
	if result.IsMediainfo {
		mediaType = "MediaInfo"
	}
	for _, item := range result.Components {
		if strings.TrimSpace(toStringSimple(item["key"])) != "媒介" {
			continue
		}
		value := strings.TrimSpace(toStringSimple(item["value"]))
		if value != "" {
			logx.Infof(logModule, "标题组件回写媒介结果：seed_id=%s_%s_%s value=%s media_type=%s", hash, torrentID, siteName, value, mediaType)
		}
		break
	}

	if strings.TrimSpace(mediumAfter) != "" && strings.TrimSpace(mediumAfter) != mediumBefore {
		logx.Infof(
			logModule,
			"媒介标准键纠偏开始：seed_id=%s_%s_%s medium_before=%s medium_after=%s media_type=%s",
			hash,
			torrentID,
			siteName,
			mediumBefore,
			mediumAfter,
			mediaType,
		)
		mediumErr := repo.UpdateSeedParameterByKey(hash, torrentID, siteName, map[string]any{
			"medium":     mediumAfter,
			"updated_at": nowText,
		})
		if mediumErr != nil {
			logx.Warnf(logModule, "媒介标准键纠偏失败：seed_id=%s_%s_%s err=%v", hash, torrentID, siteName, mediumErr)
		} else {
			logx.Infof(logModule, "媒介标准键纠偏完成：seed_id=%s_%s_%s", hash, torrentID, siteName)
		}
	}
	return true, true, result.IsBDInfo
}

func toStringSimple(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return ""
	}
}

// discStructureResolutionForRow 取用于碟规格判定（bluray / uhd_bluray）的标准分辨率键。
// 参数/返回：row 为 seed_parameters 当前行；title/mediaText/description 用于兜底推断；返回 resolution.xxx 标准键或空串。
// 失败场景：行内值不是标准键且推断不出结果时返回空串（调用方据此放弃收敛，不做误判）。
// 副作用：无。
func discStructureResolutionForRow(row map[string]any, title, mediaText, description string) string {
	if row != nil {
		if current := strings.TrimSpace(toStringSimple(row["resolution"])); strings.HasPrefix(current, "resolution.") {
			return current
		}
	}
	inferred := parser.InferStandardizedValues(title, mediaText, description)
	if inferred == nil {
		return ""
	}
	resolution := strings.TrimSpace(toStringSimple(inferred["resolution"]))
	if strings.HasPrefix(resolution, "resolution.") {
		return resolution
	}
	return ""
}
