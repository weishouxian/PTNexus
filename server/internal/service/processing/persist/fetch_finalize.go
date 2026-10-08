package persist

import (
	"errors"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
)

// FinalizeFetchedSeedInput 定义抓取阶段草稿收敛为可入库记录所需的输入。
type FinalizeFetchedSeedInput struct {
	Draft *SeedDraft

	MetaName           string
	SiteIdentifier     string
	SavePath           string
	DownloaderID       string
	TorrentNameForPath string
	RootConfig         map[string]any
	Now                time.Time

	BuildSimpleTitleComponents func(title string, releaseGroup string, mediaInfo string) []map[string]any
}

// FinalizeFetchedSeedResult 定义抓取草稿收敛后的产物与判定信息。
type FinalizeFetchedSeedResult struct {
	Record map[string]any

	FormatIsMediainfo bool
	FormatIsBDInfo    bool
	FormatReason      string
	MediainfoValid    bool
	MediainfoStatus   string

	MediumBefore string
	MediumAfter  string
	TitleBefore  string
	TitleAfter   string

	UnmappedTags []string
}

// FinalizeFetchedSeed 将抓取后的 SeedDraft 做最终纠偏与补全，并生成可写入 seed_parameters 的记录。
// 参数/返回：输入需提供 Draft 与上下文（站点、路径、当前时间）；返回媒体判定结果与 record。
// 失败场景：Draft 为空时返回 error。
// 副作用：会原地修改 Draft 的字段值（标题、标签、媒体状态、时间戳等）。
func FinalizeFetchedSeed(input FinalizeFetchedSeedInput) (FinalizeFetchedSeedResult, error) {
	draft := input.Draft
	if draft == nil {
		return FinalizeFetchedSeedResult{}, errors.New("seed draft is nil")
	}

	formatIsMediainfo, formatIsBDInfo, formatReason := processingmedia.ValidateMediaInfoFormat(draft.Mediainfo)
	mediainfoValid := formatIsMediainfo || formatIsBDInfo

	mediumBefore, mediumAfter, titleBefore, titleAfter := "", "", "", ""
	if mediainfoValid {
		mediumBefore, mediumAfter, titleBefore, titleAfter = draft.CorrectMediumAndTitleByMediaType(formatIsMediainfo, formatIsBDInfo)
	} else {
		// 当媒体文本不合规时，不信任基于“标题+媒体文本”的音频推断，避免点阵表格把 DTS-HD MA 等误写入 audio_codec。
		// 对齐抓取链路：此处仅回退音频编码为“仅标题推断”的结果，等待后续 MediaInfo 刷新后再纠偏。
		titleOnly := parser.InferStandardizedValues(strings.TrimSpace(draft.Title), "", "")
		if inferredAudio := strings.TrimSpace(titleOnly["audio_codec"]); inferredAudio != "" {
			draft.AudioCodec = inferredAudio
		}
		// 媒体文本缺失不代表没有物理证据：抓取期往往还没跑出 BDInfo（正是因为它缺失才要去跑），
		// 但种子文件列表里的 .iso/BDMV 结构此时已经拿到。此处单独按文件列表纠偏媒介，
		// 否则媒介会停在标题声明的 medium.remux，标签也会跟着留下失效的 tag.Remux。
		mediumBefore, mediumAfter, titleBefore, titleAfter = draft.CorrectMediumByDiscStructureOnly()
	}

	// 在抓取修复更新正文后，用简介中的“产地/制片国家/地区”再修正一次 source，避免修复前推断锁死。
	description := strings.TrimSpace(strings.Join([]string{strings.TrimSpace(draft.Statement), strings.TrimSpace(draft.Body)}, "\n"))
	if inferredSource := strings.TrimSpace(parser.InferSourceFromDescription(description)); inferredSource != "" {
		draft.Source = inferredSource
	}

	// 音频编码以媒体文本（首条音轨）为准：标题音频 token 与标准键矛盾时先替换标题，
	// 再建组件——组件「音频编码」由标题文本推导，先改标题才能让它跟随。
	draft.alignTitleAudioCodecWithStandard()

	draft.BuildTitleComponents(input.BuildSimpleTitleComponents)

	// 年份改以简介“年代”行为准：站点标题常缺年份，或标题年份与发行年份不一致（如重映/合集）。
	if year := strings.TrimSpace(parser.InferYearFromDescription(description)); year != "" {
		titleComponentsBefore := titleComponentValue(draft.TitleComponents, "年份")
		if titleComponentsBefore != year {
			draft.TitleComponents = processingtitle.OverrideTitleComponentValue(draft.TitleComponents, "年份", year)
			logx.Infof("抓取-年份纠偏", "按简介年代行覆盖标题组件年份 torrent_id=%s before=%s after=%s",
				draft.TorrentID, titleComponentsBefore, year)
		}
	}

	// 源站类型为动画时按“季集”重判类型：有季集值视为电视剧，无季集值视为电影。
	seasonEpisode := SeasonEpisodeFromTitleComponents(draft.TitleComponents)
	if typeBefore, typeAfter := draft.CorrectAnimationTypeBySeasonEpisode(); typeBefore != typeAfter {
		logx.Infof("迁移-类型纠偏", "源站类型为动画，按季集重判类型 torrent_id=%s title=%q 季集=%q before=%s after=%s",
			draft.TorrentID, draft.Title, seasonEpisode, typeBefore, typeAfter)
	}

	unmappedTags := draft.CompleteAndMapTags(
		input.SiteIdentifier,
		formatIsBDInfo,
		input.SavePath,
		input.TorrentNameForPath,
		input.DownloaderID,
		input.RootConfig,
	)

	now := input.Now
	if now.IsZero() {
		now = time.Now()
	}
	nowText := now.Format("2006-01-02 15:04:05")

	mediainfoStatus := "queued"
	if mediainfoValid {
		mediainfoStatus = "completed"
	}

	draft.MediainfoStatus = mediainfoStatus
	draft.ScreenshotReviewStatus = processingshared.NormalizeScreenshotReviewStatus(draft.ScreenshotReviewStatus)
	draft.IsReviewed = false
	draft.BDInfoTaskID = nil
	draft.BDInfoStartedAt = nil
	draft.BDInfoCompletedAt = nil
	draft.BDInfoError = ""
	draft.CreatedAt = nowText
	draft.UpdatedAt = nowText
	draft.NormalizeSeedParamName(input.MetaName)

	return FinalizeFetchedSeedResult{
		Record:            draft.ToSeedParameterRecord(),
		FormatIsMediainfo: formatIsMediainfo,
		FormatIsBDInfo:    formatIsBDInfo,
		FormatReason:      formatReason,
		MediainfoValid:    mediainfoValid,
		MediainfoStatus:   mediainfoStatus,
		MediumBefore:      mediumBefore,
		MediumAfter:       mediumAfter,
		TitleBefore:       titleBefore,
		TitleAfter:        titleAfter,
		UnmappedTags:      unmappedTags,
	}, nil
}

// titleComponentValue 读取标题组件中指定 key 的值；组件缺失或值为空时返回空字符串。
func titleComponentValue(components []map[string]any, key string) string {
	trimmedKey := strings.TrimSpace(key)
	if trimmedKey == "" {
		return ""
	}
	for _, component := range components {
		if strings.TrimSpace(toStringAny(component["key"], "")) != trimmedKey {
			continue
		}
		return strings.TrimSpace(toStringAny(component["value"], ""))
	}
	return ""
}
