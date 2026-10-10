package persist

import (
	"encoding/json"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
	processingtitle "github.com/pt-nexus/server/internal/service/processing/title"
)

// subtitleSeasonLogModule 副标题季集补全日志模块名。
const subtitleSeasonLogModule = "迁移-副标题季集"

// SeedDraft 表示“种子参数”在抓取/修复/纠偏过程中逐步补全的领域实体草稿。
// 它的职责是：统一承载字段，并在流程末端生成可写入 `seed_parameters` 的 record。
// 注意：SeedDraft 不是 ORM 实体；数据库读写仍由 repository 负责。
type SeedDraft struct {
	Hash      string
	TorrentID string
	SiteName  string
	Nickname  string

	Name string

	Title       string
	Subtitle    string
	IMDbLink    string
	DoubanLink  string
	TMDbLink    string
	BangumiLink string

	Type         string
	Medium       string
	VideoCodec   string
	AudioCodec   string
	Resolution   string
	Team         string
	OfficialSite string
	Source       string

	Poster                 string
	Screenshots            string
	ScreenshotReviewStatus string
	Statement              string
	Body                   string
	Mediainfo              string

	RawTags          []string
	Tags             []string
	AudioTracks      []parser.AudioTrack
	TitleComponents  []map[string]any
	TorrentFileNames []string
	EpisodeTagReason string

	RemovedARDTUDeclarations []string

	IsReviewed        bool
	MediainfoStatus   string
	BDInfoTaskID      any
	BDInfoStartedAt   any
	BDInfoCompletedAt any
	BDInfoError       string

	PublishAt     any
	LastPublishAt any

	CreatedAt string
	UpdatedAt string
}

// NewSeedDraft 创建一个空的种子草稿，调用方需按流程逐步 Apply 字段并最终落库。
func NewSeedDraft(hash, torrentID, siteName, nickname string) *SeedDraft {
	return &SeedDraft{
		Hash:      strings.TrimSpace(hash),
		TorrentID: strings.TrimSpace(torrentID),
		SiteName:  strings.TrimSpace(siteName),
		Nickname:  strings.TrimSpace(nickname),

		RawTags:                  []string{},
		Tags:                     []string{},
		TitleComponents:          []map[string]any{},
		RemovedARDTUDeclarations: []string{},
	}
}

// ApplyReviewExtract 将详情页解析得到的 ReviewExtractedData 写入草稿（不包含后续修复/映射）。
func (d *SeedDraft) ApplyReviewExtract(review parser.ReviewExtractedData, detailHTML string) {
	if d == nil {
		return
	}

	if strings.TrimSpace(review.Title) != "" {
		d.Title = strings.TrimSpace(review.Title)
	}

	d.Statement = strings.TrimSpace(review.Statement)
	d.Poster = strings.TrimSpace(review.Poster)
	d.Body = strings.TrimSpace(review.Body)
	d.Screenshots = strings.TrimSpace(review.Screens)
	d.Mediainfo = strings.TrimSpace(review.Mediainfo)

	d.Type = strings.TrimSpace(review.Type)
	d.Medium = strings.TrimSpace(review.Medium)
	d.VideoCodec = strings.TrimSpace(review.VideoCodec)
	d.AudioCodec = strings.TrimSpace(review.AudioCodec)
	d.Resolution = strings.TrimSpace(review.Resolution)
	d.Team = strings.TrimSpace(review.Team)
	d.Source = strings.TrimSpace(review.Source)

	d.RawTags = append([]string{}, review.Tags...)

	if review.RemovedARDTUDeclarations != nil {
		d.RemovedARDTUDeclarations = append([]string{}, review.RemovedARDTUDeclarations...)
	} else {
		d.RemovedARDTUDeclarations = []string{}
	}

	d.IMDbLink = firstNonEmpty(
		strings.TrimSpace(review.IMDbLink),
		processingrepair.NormalizeExternalLink(parser.ReIMDbLink().FindString(detailHTML), parser.ReIMDbLink()),
	)
	d.DoubanLink = firstNonEmpty(
		strings.TrimSpace(review.DoubanLink),
		processingrepair.NormalizeExternalLink(parser.ReDoubanLink().FindString(detailHTML), parser.ReDoubanLink()),
	)
	d.TMDbLink = firstNonEmpty(
		strings.TrimSpace(review.TMDbLink),
		processingrepair.NormalizeExternalLink(parser.ReTMDbLink().FindString(detailHTML), parser.ReTMDbLink()),
	)

	subtitle := strings.TrimSpace(review.Subtitle)
	if subtitle == "" {
		subtitle = strings.TrimSpace(processingrepair.ExtractDoubanSummary(detailHTML))
	}
	d.Subtitle = subtitle
}

// ApplyRepairResult 将“抓取修复”阶段（海报/简介/截图修复）的产物写回草稿。
func (d *SeedDraft) ApplyRepairResult(result processingrepair.ParallelFetchRepairResult) {
	if d == nil {
		return
	}
	d.Poster = strings.TrimSpace(result.ReviewData.Poster)
	d.Body = strings.TrimSpace(result.ReviewData.Body)
	d.Screenshots = strings.TrimSpace(result.ReviewData.Screens)
	d.ScreenshotReviewStatus = strings.TrimSpace(result.ScreenshotReviewStatus)

	d.IMDbLink = strings.TrimSpace(result.IMDbLink)
	d.DoubanLink = strings.TrimSpace(result.DoubanLink)
	d.TMDbLink = strings.TrimSpace(result.TMDbLink)
}

// mediumDiscStructureLogModule 按种子碟结构纠偏媒介的日志模块名。
const mediumDiscStructureLogModule = "抓取-媒介纠偏"

// discDIYTagLogModule 按碟指纹补 DIY 标签、按标准媒介剔除失效标签的日志模块名。
const discDIYTagLogModule = "抓取-标签纠偏"

// CorrectMediumAndTitleByMediaType 在识别 MediaInfo/BDInfo 后，对媒介键与标题 BluRay 标记纠偏。
func (d *SeedDraft) CorrectMediumAndTitleByMediaType(isMediainfo, isBDInfo bool) (string, string, string, string) {
	if d == nil {
		return "", "", "", ""
	}
	mediumBefore := strings.TrimSpace(d.Medium)
	titleBefore := strings.TrimSpace(d.Title)

	d.Medium = processingtitle.PreferExplicitTitleMedium(d.Medium, d.Title, d.Mediainfo)
	d.Medium = processingmedia.NormalizeMediumByMediaType(d.Medium, isMediainfo, isBDInfo)
	d.applyDiscStructureOverride("")
	d.Title = processingmedia.NormalizeBlurayTokenByMediaType(d.Title, isMediainfo, isBDInfo)
	d.dropRemuxTitleTokenIfMediumNotRemux()

	return mediumBefore, strings.TrimSpace(d.Medium), titleBefore, strings.TrimSpace(d.Title)
}

// CorrectMediumByDiscStructureOnly 仅按「种子文件列表」这一物理证据纠偏媒介，不依赖媒体文本。
// 参数/返回：返回纠偏前的媒介与标题、纠偏后的媒介与标题。
// 失败场景：草稿为空或未命中碟结构时返回原值。
// 副作用：可能修改 d.Medium。
//
// 背景：抓取期媒体文本通常还不存在——正是因为它缺失才会去跑 BDInfo/MediaInfo。
// 若把碟结构纠偏挂在「媒体文本合法」之后，最常见的时序下文件列表里的 .iso 证据会被整条跳过，
// 媒介停在标题声明的 medium.remux，标签也随之留下失效的 tag.Remux。
func (d *SeedDraft) CorrectMediumByDiscStructureOnly() (string, string, string, string) {
	if d == nil {
		return "", "", "", ""
	}
	mediumBefore := strings.TrimSpace(d.Medium)
	titleBefore := strings.TrimSpace(d.Title)
	d.applyDiscStructureOverride(" （无媒体文本）")
	d.dropRemuxTitleTokenIfMediumNotRemux()
	return mediumBefore, strings.TrimSpace(d.Medium), titleBefore, strings.TrimSpace(d.Title)
}

// dropRemuxTitleTokenIfMediumNotRemux 在标准媒介已明确不是 Remux 时，摘除标题里的 Remux 媒介标记。
// 参数/返回：无；原地修改 d.Title。
// 失败场景：媒介为空（信息缺失）、媒介仍属 Remux 档、标题未命中 Remux 时不做任何修改。
// 副作用：可能修改 d.Title，并写一条纠偏日志。
//
// 背景：标题组件「媒介」由标题文本推导（extractMediumPythonish），媒介被碟结构纠偏收敛为
// medium.bluray 之后，若标题仍写 “…Blu-ray Remux…”，面板上「媒介」会一直显示 “Blu-ray Remux”，
// 用户只能手工改成 “Blu-ray”；发种标题也会带上与本体矛盾的 Remux 声明。
func (d *SeedDraft) dropRemuxTitleTokenIfMediumNotRemux() {
	if d == nil {
		return
	}
	if !processingmedia.ShouldDropRemuxTag(d.Medium) {
		return
	}
	before := strings.TrimSpace(d.Title)
	if before == "" {
		return
	}
	after := processingmedia.StripRemuxMediumToken(before)
	if after == "" || after == before {
		return
	}
	logx.Infof(mediumDiscStructureLogModule, "按标准媒介摘除标题 Remux 标记 torrent_id=%s medium=%s before=%q after=%q",
		d.TorrentID, strings.TrimSpace(d.Medium), before, after)
	d.Title = after
}

// audioTitleAlignLogModule 按标准音频编码替换标题音频标记的日志模块名。
const audioTitleAlignLogModule = "抓取-音频纠偏"

// alignTitleAudioCodecWithStandard 在标准音频编码与标题音频 token 矛盾时替换标题里的音频标记。
// 参数/返回：无；原地修改 d.Title。
// 失败场景：标准键缺失/非法、标题未命中音频 token、或与标准键同家族时不做任何修改。
// 副作用：可能修改 d.Title，并写一条纠偏日志。
//
// 背景：标题组件「音频编码」由标题文本推导（extractAudioFromTitle）。标准 audio_codec 改以
// MediaInfo 第一条音轨为准后，标题声明（如 DDP2.0）可能与首音轨（AAC）矛盾——面板「音频编码」
// 会停留在标题声明上，发种标题也会带着与标准键矛盾的编码。须在 BuildTitleComponents 之前调用，
// 组件随标题重建自动跟随。
func (d *SeedDraft) alignTitleAudioCodecWithStandard() {
	if d == nil {
		return
	}
	standard := strings.TrimSpace(d.AudioCodec)
	if !strings.HasPrefix(standard, "audio.") {
		return
	}
	before := strings.TrimSpace(d.Title)
	if before == "" {
		return
	}
	after := processingmedia.ReplaceTitleAudioCodecToken(before, standard)
	if after == "" || after == before {
		return
	}
	logx.Infof(audioTitleAlignLogModule, "按标准音频编码替换标题音频标记 torrent_id=%s audio_codec=%s before=%q after=%q",
		d.TorrentID, standard, before, after)
	d.Title = after
}

// applyDiscStructureOverride 在种子文件列表证实本体是 ISO/BDMV 碟结构时，把媒介收敛回原盘档。
// 参数/返回：logSuffix 追加到日志文案尾部，用于区分纠偏来自哪条链路；无返回值。
// 失败场景：未命中碟结构、DVD 系媒介、分辨率缺失或 SD 档时保持原值。
// 副作用：可能修改 d.Medium，并写一条纠偏日志。
//
// 标题侧只看得到 “BluRay.Remux” 这类声明，判不出实际发的是原盘镜像；
// 文件列表是物理事实，优先于标题声明，且只做「非原盘档 → 原盘档」的单向收敛。
func (d *SeedDraft) applyDiscStructureOverride(logSuffix string) {
	if d == nil {
		return
	}
	refined := processingmedia.OverrideMediumByDiscStructure(d.Medium, d.discStructureResolution(), d.TorrentFileNames)
	if refined == d.Medium {
		return
	}
	logx.Infof(mediumDiscStructureLogModule, "按种子碟结构纠偏媒介%s torrent_id=%s before=%s after=%s files=%d",
		logSuffix, d.TorrentID, strings.TrimSpace(d.Medium), refined, len(d.TorrentFileNames))
	d.Medium = refined
}

// discStructureResolution 返回用于碟规格判定的标准分辨率键。
// 优先取草稿字段（站点详情页分辨率），缺失时按标题+媒体文本兜底推断，避免只因素材字段空缺而放弃纠偏。
func (d *SeedDraft) discStructureResolution() string {
	if d == nil {
		return ""
	}
	if resolution := strings.TrimSpace(d.Resolution); resolution != "" {
		return resolution
	}
	inferred := parser.InferStandardizedValues(strings.TrimSpace(d.Title), strings.TrimSpace(d.Mediainfo), "")
	if inferred == nil {
		return ""
	}
	return strings.TrimSpace(inferred["resolution"])
}

// BuildTitleComponents 基于标题与媒体文本生成标题组件，并在必要时用标题组件覆盖视频编码字段。
func (d *SeedDraft) BuildTitleComponents(buildSimpleComponents func(title string, releaseGroup string, mediaInfo string) []map[string]any) []map[string]any {
	if d == nil {
		return []map[string]any{}
	}
	result := processingtitle.BuildTitleComponentsForStorage(d.Title, d.Mediainfo, buildSimpleComponents)
	d.TitleComponents = result.Components

	if fromTitle := processingtitle.StandardizedVideoCodecFromTitleComponents(d.TitleComponents); fromTitle != "" {
		d.VideoCodec = fromTitle
	}
	return d.TitleComponents
}

// CorrectAnimationTypeBySeasonEpisode 纠偏动画类型：源站类型为动画时，按标题组件中的“季集”重判——
// 有季集值视为电视剧（category.tv_series），无季集值视为电影（category.movie）。
// 参数/返回：返回纠偏前后的类型值（未命中动画类型时前后一致，不做修改）。
// 副作用：命中动画类型时原地修改 d.Type；需在 BuildTitleComponents 之后调用。
func (d *SeedDraft) CorrectAnimationTypeBySeasonEpisode() (string, string) {
	if d == nil {
		return "", ""
	}
	before := strings.TrimSpace(d.Type)
	if !isAnimationTypeValue(before) {
		return before, before
	}

	after := "category.movie"
	if SeasonEpisodeFromTitleComponents(d.TitleComponents) != "" {
		after = "category.tv_series"
	}
	if after != before {
		d.Type = after
	}
	return before, d.Type
}

// isAnimationTypeValue 判断类型值是否属于动画类（标准化键或站点原始文本）。
func isAnimationTypeValue(value string) bool {
	switch strings.TrimSpace(value) {
	case "category.animation", "动画", "动漫":
		return true
	default:
		return false
	}
}

// SeasonEpisodeFromTitleComponents 从标题组件中提取“季集”值；不存在或为空时返回空字符串。
func SeasonEpisodeFromTitleComponents(components []map[string]any) string {
	for _, component := range components {
		if strings.TrimSpace(toStringAny(component["key"], "")) != "季集" {
			continue
		}
		return strings.TrimSpace(toStringAny(component["value"], ""))
	}
	return ""
}

// CompleteAndMapTags 进行标签补全与标准化映射（只保留能映射到标准 tag.* 的标签）。
func (d *SeedDraft) CompleteAndMapTags(siteIdentifier string, formatIsBDInfo bool, savePath string, torrentNameForPath string, downloaderID string, rootConfig map[string]any) []string {
	if d == nil {
		return []string{}
	}

	descriptionForTags := strings.TrimSpace(strings.Join([]string{d.Statement, d.Body}, "\n"))
	rawTagCandidates := make([]string, 0, len(d.RawTags)+16)
	rawTagCandidates = append(rawTagCandidates, d.RawTags...)
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractRawTagsFromTitleComponents(d.TitleComponents)...)
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractRawTagsFromSubtitle(d.Subtitle)...)
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractTagsFromDescriptionCategory(descriptionForTags)...)
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractTagsFromDescriptionScore(descriptionForTags)...)
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractRawTagsFromMediaText(d.Mediainfo, formatIsBDInfo)...)
	// 音频编码标签（Atmos）与标准值同源补齐：标题漏写 Atmos 时靠 MediaInfo/BDInfo 兜底。
	rawTagCandidates = append(rawTagCandidates, processingtagging.ExtractRawTagsFromAudioCodec(d.Title, d.Mediainfo)...)

	// 本体已落到原盘档且 BDInfo 碟指纹提示 DIY 时补 DIY 原始标签，
	// 交由 MapTagsToStandard 按站点过滤（站点没配 tag.DIY 映射时自然被丢弃）。
	if shouldAddDIY, reason := processingtagging.ShouldAddDiscDIYRawTag(d.Medium, d.Mediainfo, torrentNameForPath); shouldAddDIY {
		rawTagCandidates = append(rawTagCandidates, "DIY")
		logx.Infof(discDIYTagLogModule, "按碟指纹补 DIY 标签 torrent_id=%s 依据=%s", strings.TrimSpace(d.TorrentID), reason)
	}

	completion := processingtagging.CheckCompletionStatusWithDownloaderContext(
		d.Title,
		d.Subtitle,
		descriptionForTags,
		processingtagging.CompletionCheckContext{
			SavePath:     savePath,
			TorrentName:  torrentNameForPath,
			ContentName:  d.Title,
			DownloaderID: downloaderID,
			RootConfig:   rootConfig,
		},
	)
	if processingtagging.ShouldAddCompletionTag(rawTagCandidates, completion) {
		rawTagCandidates = append(rawTagCandidates, "完结")
	}
	episodeTagResult := processingtagging.DetectEpisodeTag(processingtagging.EpisodeTagInput{
		Title:            d.Title,
		Subtitle:         d.Subtitle,
		TorrentName:      torrentNameForPath,
		Type:             d.Type,
		ExistingTags:     rawTagCandidates,
		TorrentFileNames: d.TorrentFileNames,
		Completion:       completion,
	})
	d.EpisodeTagReason = strings.TrimSpace(episodeTagResult.Reason)
	if episodeTagResult.Matched {
		rawTagCandidates = append(rawTagCandidates, "分集")
	}

	// 电视剧/动漫的整季合集：副标题补写「第x季 全y集」（幂等，副标题已含「全X集」时原样返回）。
	if enriched, reason := processingtagging.EnrichSubtitleWithSeasonEpisode(processingtagging.SubtitleSeasonEpisodeInput{
		Subtitle:         d.Subtitle,
		Title:            d.Title,
		SeasonEpisode:    SeasonEpisodeFromTitleComponents(d.TitleComponents),
		Description:      descriptionForTags,
		Type:             d.Type,
		Tags:             rawTagCandidates,
		TorrentFileNames: d.TorrentFileNames,
		Completion:       completion,
	}); enriched != d.Subtitle {
		logx.Infof(subtitleSeasonLogModule, "%s torrent_id=%s 副标题 %q -> %q",
			strings.TrimSpace(reason), d.TorrentID, strings.TrimSpace(d.Subtitle), enriched)
		d.Subtitle = enriched
	}

	mappedTags, unmappedTags := processingtagging.MapTagsToStandard(rawTagCandidates, siteIdentifier)
	// 媒介已被碟结构纠偏收敛为原盘时，剔除随之失效的 Remux 标签（标签是从标题组件推的，不会自动跟随）。
	if reconciled := processingtagging.ReconcileMediumTagsWithStandardMedium(mappedTags, d.Medium); len(reconciled) != len(mappedTags) {
		logx.Infof(discDIYTagLogModule, "按标准媒介剔除失效标签 torrent_id=%s medium=%s before=%d after=%d",
			strings.TrimSpace(d.TorrentID), strings.TrimSpace(d.Medium), len(mappedTags), len(reconciled))
		mappedTags = reconciled
	}
	d.Tags = mappedTags
	return unmappedTags
}

// NormalizeSeedParamName 对齐迁移链路：seed_parameters.name 使用 torrent 元数据名。
func (d *SeedDraft) NormalizeSeedParamName(metaName string) {
	if d == nil {
		return
	}
	d.Name = normalizeSeedParamName(metaName, d.Title, d.TorrentID)
}

func normalizeSeedParamName(metaName string, fallbackTitle string, fallbackTorrentID string) string {
	name := strings.TrimSpace(metaName)
	if name == "" {
		name = strings.TrimSpace(fallbackTitle)
	}
	if strings.HasSuffix(strings.ToLower(name), ".torrent") {
		name = strings.TrimSpace(name[:len(name)-len(".torrent")])
	}
	if name == "" {
		name = strings.TrimSpace(fallbackTorrentID)
	}
	return name
}

// ToSeedParameterRecord 将草稿序列化为可写入 seed_parameters 的 record（字段名保持与历史一致）。
func (d *SeedDraft) ToSeedParameterRecord() map[string]any {
	if d == nil {
		return map[string]any{}
	}

	tags := d.Tags
	if tags == nil {
		tags = []string{}
	}
	encodedTags, _ := json.Marshal(tags)

	removed := d.RemovedARDTUDeclarations
	if removed == nil {
		removed = []string{}
	}
	encodedRemoved, _ := json.Marshal(removed)

	components := d.TitleComponents
	if components == nil {
		components = []map[string]any{}
	}
	encodedComponents, _ := json.Marshal(components)

	audioTracks := d.AudioTracks
	if audioTracks == nil {
		audioTracks = []parser.AudioTrack{}
	}
	encodedAudioTracks, _ := json.Marshal(audioTracks)

	return map[string]any{
		"hash":                      strings.TrimSpace(d.Hash),
		"torrent_id":                strings.TrimSpace(d.TorrentID),
		"site_name":                 strings.TrimSpace(d.SiteName),
		"nickname":                  strings.TrimSpace(d.Nickname),
		"name":                      strings.TrimSpace(d.Name),
		"title":                     strings.TrimSpace(d.Title),
		"subtitle":                  strings.TrimSpace(d.Subtitle),
		"imdb_link":                 strings.TrimSpace(d.IMDbLink),
		"douban_link":               strings.TrimSpace(d.DoubanLink),
		"tmdb_link":                 strings.TrimSpace(d.TMDbLink),
		"bangumi_link":              strings.TrimSpace(d.BangumiLink),
		"type":                      strings.TrimSpace(d.Type),
		"medium":                    strings.TrimSpace(d.Medium),
		"video_codec":               strings.TrimSpace(d.VideoCodec),
		"audio_codec":               strings.TrimSpace(d.AudioCodec),
		"resolution":                strings.TrimSpace(d.Resolution),
		"team":                      parser.NormalizeTeamKeyForSite(d.Team, d.SiteName),
		"official_site":             strings.TrimSpace(d.OfficialSite),
		"source":                    strings.TrimSpace(d.Source),
		"tags":                      string(encodedTags),
		"poster":                    strings.TrimSpace(d.Poster),
		"screenshots":               strings.TrimSpace(d.Screenshots),
		"screenshot_review_status":  strings.TrimSpace(d.ScreenshotReviewStatus),
		"statement":                 strings.TrimSpace(d.Statement),
		"body":                      strings.TrimSpace(d.Body),
		"mediainfo":                 strings.TrimSpace(d.Mediainfo),
		"audio_tracks":              string(encodedAudioTracks),
		"title_components":          string(encodedComponents),
		"removed_ardtudeclarations": string(encodedRemoved),
		"is_reviewed":               d.IsReviewed,
		"mediainfo_status":          strings.TrimSpace(d.MediainfoStatus),
		"bdinfo_task_id":            d.BDInfoTaskID,
		"bdinfo_started_at":         d.BDInfoStartedAt,
		"bdinfo_completed_at":       d.BDInfoCompletedAt,
		"bdinfo_error":              strings.TrimSpace(d.BDInfoError),
		"publish_at":                d.PublishAt,
		"last_publish_at":           d.LastPublishAt,
		"created_at":                strings.TrimSpace(d.CreatedAt),
		"updated_at":                strings.TrimSpace(d.UpdatedAt),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
