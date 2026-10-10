package extract

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	// reMediaInfoSectionHeader 识别 MediaInfo 的段落头（General/Video/Audio/Text/Menu/Chapters）。
	reMediaInfoSectionHeader = regexp.MustCompile(`(?im)^\s*(General|Video|Audio|Text|Menu|Chapters)(?:\s*#\d+)?\s*$`)
	// reMediaInfoFormatField 提取段落内 "Format : xxx" / "Format : xxx / yyy" 的取值。
	reMediaInfoFormatField = regexp.MustCompile(`(?im)^\s*Format\s*:\s*([^\r\n]+)`)
	// reMediaInfoBitRateField 提取段落内 "Bit rate : 192 kb/s"（兼容 kb/s、kbps、Mbps、bps 写法）。
	reMediaInfoBitRateField = regexp.MustCompile(`(?im)^\s*Bit\s*[Rr]ate\s*(?:mode)?\s*:\s*([0-9]+(?:\.[0-9]+)?)\s*(k|K|M|b|B)?[bB]?(?:/s|ps)?\b`)
	// reMediaInfoChannelsField 提取段落内 "Channel(s) : 2 channels"。
	reMediaInfoChannelsField = regexp.MustCompile(`(?im)^\s*Channel\(?s?\)?\s*:\s*(\d+)`)
	// reMediaInfoDefaultField 提取段落内 "Default : Yes"。
	reMediaInfoDefaultField = regexp.MustCompile(`(?im)^\s*Default\s*:\s*([^\r\n]+)`)
)

// AudioTrack 表示 MediaInfo/BDInfo 里一条音轨的结构化信息。
type AudioTrack struct {
	// CodecKey 为该音轨的标准音频编码键（audio.*）。
	CodecKey string `json:"codec_key"`
	// Format 为原始 Format 文本（如 "AAC LC" / "E-AC-3"）。
	Format string `json:"format"`
	// BitRateKbps 为码率（kbps，数值已归一为 kbps）。
	BitRateKbps float64 `json:"bit_rate_kbps"`
	// Channels 为声道数（如 2 / 6 / 8）。
	Channels int `json:"channels"`
	// Default 表示该音轨是否为默认主音轨。
	Default bool `json:"default"`
	// Index 为音轨序号（从 1 开始）。
	Index int `json:"index"`
}

// inferVideoCodecFromMediainfo 从 MediaInfo 的第一条 Video 段的 Format 字段推断视频编码。
// 参数/返回：mediainfo 为原始 MediaInfo 文本；无法定位 Video 段或 Format 无法识别时返回空串。
// 副作用：无。
func inferVideoCodecFromMediainfo(mediainfo string) string {
	section := firstMediaInfoVideoSection(mediainfo)
	if section == "" {
		return ""
	}
	format := mediaInfoFormatField(section)
	if format == "" {
		return ""
	}
	return videoCodecKeyFromFormatText(format)
}

// audioCodecRank 定义音频编码标准键的规格权重，数值越大规格越高。
// 用于多音轨场景取「最高规格」的一条（TrueHD Atmos > DTS:X > DTS-HD MA > … > AAC/MP3）。
var audioCodecRank = map[string]int{
	"audio.truehd_atmos": 100,
	"audio.ddp_atmos":    95,
	"audio.truehd":       90,
	"audio.dtsx":         85,
	"audio.dts_hd_ma":    80,
	"audio.dts_hd_hr":    75,
	"audio.flac":         70,
	"audio.dts":          65,
	"audio.ddp":          60,
	"audio.ac3":          55,
	"audio.av3a":         50,
	"audio.alac":         45,
	"audio.ape":          40,
	"audio.dsd":          35,
	"audio.wav":          30,
	"audio.lpcm":         25,
	"audio.ogg":          20,
	"audio.opus":         15,
	"audio.aac":          12,
	"audio.mp3":          10,
	"audio.other":        0,
}

// inferAudioCodecFromMediainfo 从 MediaInfo 的全部 Audio 段的 Format 字段推断音频编码。
// 参数/返回：mediainfo 为原始 MediaInfo 文本；无法定位 Audio 段或首条 Format 无法识别时返回空串。
// 副作用：无。
//
// 背景：页面展示（抓取后核对详情页）与站点未配置音轨策略时的缺省口径，默认取第一条音轨。
// 站点各自的多音轨策略在发布链路里按 audio_track_policy 单独重选，与此处无关。
//
// 例外（2026-10-10 实测）：多语言发行的 WEB-DL 常把配音轨排在第一条——首轨是捷克语
// AAC LC 2.0、主音轨却是 E-AC-3 5.1，此时「取第一条」会把标准音频编码判成 audio.aac，
// 再经标题 token 替换链路写成「AAC 5.1」这类源里不存在的组合。
// 故首轨规格不高于 AAC 且存在更高规格音轨时改取规格最高者，其余情况口径不变。
func inferAudioCodecFromMediainfo(mediainfo string) string {
	tracks := parseAudioTracksFromMediainfo(mediainfo)
	if len(tracks) == 0 {
		return ""
	}
	return audioCodecKeyForInference(tracks)
}

// audioCodecKeyForInference 从结构化音轨列表里挑出用于「标准音频编码」的音轨编码键。
// 参数/返回：tracks 为按出现顺序排列的音轨；列表为空时返回空串。
// 失败场景：不返回错误；全部音轨都无可用编码键时返回首轨（可能为空串）。
// 副作用：无。
//
// 判定规则：单音轨、首轨编码无法识别、或首轨规格已高于 AAC 时，一律保持「取第一条」的既有口径；
// 仅当首轨是已识别的低规格配音轨（AAC/MP3）时才改取 audioCodecRank 最高的一条，
// 同规格时按 Default 标记、码率、出现顺序依次决胜。
//
// ⚠️ 「首轨编码无法识别（rank 0）」必须原样返回空串而不是改为次高轨：MediaInfo 把 TrueHD 的
// Format 写作 `MLP FBA`，本包映射不到标准键，若改成后面的 AAC 轨会把正确的高规格音轨降级，
// 同时掐掉 InferAudioCodecKey 里「媒体识别不到 → 回退标题 token」的兜底。
func audioCodecKeyForInference(tracks []AudioTrack) string {
	if len(tracks) == 0 {
		return ""
	}

	first := tracks[0]
	firstRank := audioCodecRank[first.CodecKey]
	if len(tracks) == 1 || firstRank <= 0 || firstRank > audioCodecRank["audio.aac"] {
		return first.CodecKey
	}

	best := first
	bestRank := firstRank
	for _, track := range tracks[1:] {
		rank := audioCodecRank[track.CodecKey]
		switch {
		case rank > bestRank:
			best, bestRank = track, rank
		case rank == bestRank && rank > firstRank:
			if track.Default && !best.Default {
				best = track
			} else if track.Default == best.Default && track.BitRateKbps > best.BitRateKbps {
				best = track
			}
		}
	}
	return best.CodecKey
}

// SelectInferenceAudioTrack 按「标准音频编码推断口径」从音轨列表中挑出主音轨（导出版本）。
// 参数/返回：tracks 为按出现顺序排列的音轨；列表为空时返回零值。
// 失败场景：不返回错误；目标编码在列表中找不到对应项时回退首轨。
// 副作用：无。
//
// 用途：processing/media 拼「音频编码」组件时复用同一套选轨规则，
// 避免组件值（物理首轨）与标准值 audio_codec（跳过低规格配音轨）两套口径长期打架。
func SelectInferenceAudioTrack(tracks []AudioTrack) AudioTrack {
	if len(tracks) == 0 {
		return AudioTrack{}
	}
	target := audioCodecKeyForInference(tracks)
	for _, track := range tracks {
		if track.CodecKey == target {
			return track
		}
	}
	return tracks[0]
}

// AudioCodecKeyFromDisplayName 把展示口径的音频编码名（DDP/TrueHD/DTS-HD MA/DD…）归一为标准键（audio.*）。
// 参数/返回：display 为 processing/media:standardAudioCode 产出的展示名；无法识别时返回空串。
// 副作用：无。
//
// 说明：展示口径由 processing/media 维护，与 mediainfo_codec.go:audioCodecKeyFromFormatText
// （面向 MediaInfo 的 `Format :` 字段）是两套不同输入，不能合并。
// 此处只做「展示名 → 标准键」的字典映射，让媒体侧能复用同一张 audioCodecRank 与同一套选轨规则，
// 避免规格权重表在两处各写一份造成漂移。
func AudioCodecKeyFromDisplayName(display string) string {
	switch strings.ToUpper(strings.TrimSpace(display)) {
	case "AV3A":
		return "audio.av3a"
	case "DTS:X":
		return "audio.dtsx"
	case "DTS-HD MA":
		return "audio.dts_hd_ma"
	case "DTS-HD HR":
		return "audio.dts_hd_hr"
	case "DTS":
		return "audio.dts"
	case "TRUEHD":
		return "audio.truehd"
	case "DDP":
		return "audio.ddp"
	// 展示口径的裸 DD 是 Dolby Digital（有损 AC-3），对应标准键 audio.ac3。
	case "DD":
		return "audio.ac3"
	case "FLAC":
		return "audio.flac"
	case "ALAC":
		return "audio.alac"
	case "APE":
		return "audio.ape"
	case "DSD":
		return "audio.dsd"
	case "WAV":
		return "audio.wav"
	case "LPCM", "PCM":
		return "audio.lpcm"
	case "OGG", "VORBIS":
		return "audio.ogg"
	case "OPUS":
		return "audio.opus"
	case "AAC":
		return "audio.aac"
	case "MP3":
		return "audio.mp3"
	default:
		return ""
	}
}

// parseAudioTracksFromMediainfo 解析 MediaInfo/BDInfo 文本中的全部音轨，返回结构化音轨列表。
// 参数/返回：mediainfo 为原始 MediaInfo/BDInfo 文本；返回按出现顺序排列的音轨（可能为空）。
// 副作用：无。
//
// 背景：多音轨选择策略（第一条 / 码率最高 / 规格最高）需要每条音轨的编码+码率+声道+默认标记，
// 而非仅一个最终 codec。抓取时落库、发布时按站点策略重选，都依赖这份结构化数据。
func parseAudioTracksFromMediainfo(mediainfo string) []AudioTrack {
	sections := allMediaInfoAudioSections(mediainfo)
	if len(sections) == 0 {
		// MediaInfo 的 `Audio` 段切不到时回退 BDInfo 音轨解析：
		// BDInfo 的段头是 `AUDIO:`（带冒号），reMediaInfoSectionHeader 不匹配，
		// 不回退会让 BDInfo 源音轨恒为 0 条 → 音频编码只能靠标题 contains 兜底
		// （2026-10-10 实测：BDInfo 第一条 LPCM 2.0、第二条 TrueHD 5.1 时被判成 TrueHD）。
		return parseBDInfoAudioTracks(mediainfo)
	}
	tracks := make([]AudioTrack, 0, len(sections))
	for i, section := range sections {
		format := mediaInfoFormatField(section)
		codecKey := ""
		if format != "" {
			codecKey = audioCodecKeyFromFormatText(format)
		}
		if codecKey == "" && format == "" {
			// 既无 format 也无 codec，跳过该段（可能是误切分）。
			continue
		}
		tracks = append(tracks, AudioTrack{
			CodecKey:    codecKey,
			Format:      strings.TrimSpace(format),
			BitRateKbps: mediaInfoBitRateKbps(section),
			Channels:    mediaInfoChannels(section),
			Default:     mediaInfoDefaultFlag(section),
			Index:       i + 1,
		})
	}
	return tracks
}

var (
	// reBDInfoAudioHeader 匹配 BDInfo 的音轨段头：`AUDIO:`（允许前导 `*` 与空白）。
	reBDInfoAudioHeader = regexp.MustCompile(`(?i)^\s*\*?\s*AUDIO\s*:\s*$`)
	// reBDInfoAudioSectionStop 匹配 BDInfo 音轨段之后的段头，用于终止音轨扫描。
	reBDInfoAudioSectionStop = regexp.MustCompile(`(?i)^(\*?\s*)?(SUBTITLES|FILES|VIDEO|CHAPTERS|DISC INFO|PLAYLIST REPORT|QUICK SUMMARY|DISC SIZE|BDINFO)\s*:?`)
	// reBDInfoAudioTableLine 匹配 BDInfo 音轨表格的表头行与分隔行（`Codec Language Bitrate Description` / `-----`）。
	reBDInfoAudioTableLine = regexp.MustCompile(`(?i)^(\*+\s*)?(CODEC|LANGUAGE|BITRATE|DESCRIPTION)\b` + `|^-+`)
	// reBDInfoAudioCodecCell 取 BDInfo 音轨行的首列（编码名），到 2 个以上连续空格为止。
	reBDInfoAudioCodecCell = regexp.MustCompile(`^(.+?)\s{2,}`)
	// reBDInfoAudioChannels 取 BDInfo 描述列里的声道布局（如 `5.1 /`、`2.0 /`、`7.1.4 /`），要求后跟 `/` 以免误命中 `2.3 Mbps`。
	reBDInfoAudioChannels = regexp.MustCompile(`\b([1-8]\.\d(?:\.\d)?)\s*/`)
	// reBDInfoAudioBitRate 取 BDInfo 音轨行的码率（kbps）。
	reBDInfoAudioBitRate = regexp.MustCompile(`(?i)\b(\d{2,6})\s*kbps\b`)
)

// parseBDInfoAudioTracks 解析 BDInfo 文本 AUDIO 段的音轨行，返回按出现顺序排列的结构化音轨。
// 参数/返回：bdinfo 为 BDInfo 原文；未定位到 `AUDIO:` 段或段内无可用音轨时返回 nil。
// 失败场景：文本为空、无 `AUDIO:` 段、或行首编码既非空也无法识别为标准音频键（表头/说明行会被跳过）。
// 副作用：无。
//
// 背景：BDInfo 的段头是 `AUDIO:`（带冒号），与 MediaInfo 的 `Audio` 段头不同，
// allMediaInfoAudioSections 切不出 BDInfo 音轨，故需要独立的行扫描实现。
// 编码名取行首列（到多空格为止），避免 Description 列里的 `AC3 Embedded` 等噪声干扰判定。
func parseBDInfoAudioTracks(bdinfo string) []AudioTrack {
	normalized := strings.ReplaceAll(bdinfo, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	start := -1
	for i, line := range lines {
		if reBDInfoAudioHeader.MatchString(line) {
			start = i
			break
		}
	}
	if start == -1 {
		return nil
	}

	tracks := make([]AudioTrack, 0, 4)
	for i := start + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if reBDInfoAudioSectionStop.MatchString(trimmed) {
			break
		}
		if reBDInfoAudioTableLine.MatchString(trimmed) {
			continue
		}

		codecText := trimmed
		if m := reBDInfoAudioCodecCell.FindStringSubmatch(trimmed); len(m) == 2 {
			codecText = strings.TrimSpace(m[1])
		}
		codecKey := audioCodecKeyFromFormatText(codecText)
		if codecKey == "" {
			continue
		}

		tracks = append(tracks, AudioTrack{
			CodecKey:    codecKey,
			Format:      strings.TrimSpace(codecText),
			BitRateKbps: bdInfoAudioBitRateKbps(trimmed),
			Channels:    bdInfoAudioChannels(trimmed),
			Default:     false,
			Index:       len(tracks) + 1,
		})
	}
	return tracks
}

// bdInfoAudioChannels 把 BDInfo 描述列里的声道布局（如 `5.1` / `2.0` / `7.1.4`）换算为声道总数。
// 参数/返回：line 为音轨整行；未匹配到布局时返回 0。
// 副作用：无。
func bdInfoAudioChannels(line string) int {
	m := reBDInfoAudioChannels.FindStringSubmatch(line)
	if len(m) != 2 {
		return 0
	}
	total := 0
	for _, part := range strings.Split(m[1], ".") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total += n
	}
	return total
}

// bdInfoAudioBitRateKbps 解析 BDInfo 音轨行里的码率（`2304 kbps`），返回 kbps 数值。
// 参数/返回：line 为音轨整行；未匹配到码率时返回 0。
// 副作用：无。
func bdInfoAudioBitRateKbps(line string) float64 {
	m := reBDInfoAudioBitRate.FindStringSubmatch(line)
	if len(m) != 2 {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
	if err != nil {
		return 0
	}
	return value
}

// mediaInfoBitRateKbps 解析段落里的 "Bit rate : xxx kb/s"，归一为 kbps 数值。
func mediaInfoBitRateKbps(section string) float64 {
	m := reMediaInfoBitRateField.FindStringSubmatch(section)
	if len(m) < 2 {
		return 0
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(m[1]), 64)
	if err != nil {
		return 0
	}
	unit := ""
	if len(m) >= 3 {
		unit = strings.ToUpper(strings.TrimSpace(m[2]))
	}
	switch unit {
	case "K":
		return value
	case "M":
		return value * 1000
	case "B":
		// bps 单位（无 k/M 前缀），换算 kbps。
		return value / 1000
	default:
		// 未识别单位时按 kbps 处理（多数情况是 kb/s）。
		return value
	}
}

// mediaInfoChannels 解析段落里的 "Channel(s) : N channels"。
func mediaInfoChannels(section string) int {
	m := reMediaInfoChannelsField.FindStringSubmatch(section)
	if len(m) < 2 {
		return 0
	}
	v, err := strconv.Atoi(strings.TrimSpace(m[1]))
	if err != nil {
		return 0
	}
	return v
}

// mediaInfoDefaultFlag 解析段落里的 "Default : Yes"。
func mediaInfoDefaultFlag(section string) bool {
	m := reMediaInfoDefaultField.FindStringSubmatch(section)
	if len(m) < 2 {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(m[1]), "yes")
}

// AudioTrackPolicy 定义多音轨选择策略。
type AudioTrackPolicy int

const (
	// AudioTrackPolicyUnset 未设置（缺省）：等价于取第一条音轨。
	AudioTrackPolicyUnset AudioTrackPolicy = 0
	// AudioTrackPolicyFirst 取第一条音轨。
	AudioTrackPolicyFirst AudioTrackPolicy = 1
	// AudioTrackPolicyHighestBitRate 取码率最高的一条。
	AudioTrackPolicyHighestBitRate AudioTrackPolicy = 2
	// AudioTrackPolicyHighestSpec 取规格最高的一条。
	AudioTrackPolicyHighestSpec AudioTrackPolicy = 3
)

// SelectAudioTrack 按策略从音轨列表中选出一条。
// 参数/返回：tracks 为音轨列表；policy 为选择策略；返回选中的音轨（列表为空时返回零值）。
// 副作用：无。未设置（0）或非法策略时回退到「第一条音轨」。
func selectAudioTrack(tracks []AudioTrack, policy AudioTrackPolicy) AudioTrack {
	if len(tracks) == 0 {
		return AudioTrack{}
	}
	switch policy {
	case AudioTrackPolicyHighestBitRate:
		best := tracks[0]
		for _, track := range tracks[1:] {
			if track.BitRateKbps > best.BitRateKbps {
				best = track
			}
		}
		return best
	case AudioTrackPolicyHighestSpec:
		best := tracks[0]
		bestRank := -1
		for _, track := range tracks {
			rank, ok := audioCodecRank[track.CodecKey]
			if !ok {
				rank = 0
			}
			if rank > bestRank {
				bestRank = rank
				best = track
			}
		}
		return best
	default:
		// 未设置（0）、第一条（1）、或非法值：统一取第一条音轨。
		return tracks[0]
	}
}

// firstMediaInfoVideoSection 返回 MediaInfo 文本中第一条 Video 段。
func firstMediaInfoVideoSection(mediainfo string) string {
	return firstMediaInfoSectionByHeader(mediainfo, "Video")
}

// allMediaInfoAudioSections 返回 MediaInfo 文本中所有 Audio 段（按出现顺序）。
func allMediaInfoAudioSections(mediainfo string) []string {
	return allMediaInfoSectionsByHeader(mediainfo, "Audio")
}

// firstMediaInfoSectionByHeader 把 MediaInfo 文本按段落头切分，返回指定类型的第一个段落。
// 参数/返回：mediainfo 为原始 MediaInfo 文本；header 为目标段落头（如 "Video"/"Audio"）；未找到返回空串。
// 副作用：无。Go RE2 不支持前瞻，故用行扫描切段而非正则 lookahead。
func firstMediaInfoSectionByHeader(mediainfo, header string) string {
	sections := allMediaInfoSectionsByHeader(mediainfo, header)
	if len(sections) == 0 {
		return ""
	}
	return sections[0]
}

// allMediaInfoSectionsByHeader 把 MediaInfo 文本按段落头切分，返回指定类型的全部段落（按出现顺序）。
// 参数/返回：mediainfo 为原始 MediaInfo 文本；header 为目标段落头（如 "Audio"）；未找到返回空切片。
// 副作用：无。Go RE2 不支持前瞻，故用行扫描切段而非正则 lookahead。
func allMediaInfoSectionsByHeader(mediainfo, header string) []string {
	trimmed := strings.TrimSpace(mediainfo)
	if trimmed == "" {
		return nil
	}
	header = strings.TrimSpace(header)
	if header == "" {
		return nil
	}

	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	// 收集每个段落头所在的行号。
	type sectionStart struct {
		name string
		line int
	}
	var starts []sectionStart
	for i, line := range lines {
		if m := reMediaInfoSectionHeader.FindStringSubmatch(line); len(m) >= 2 {
			starts = append(starts, sectionStart{name: strings.TrimSpace(m[1]), line: i})
		}
	}
	if len(starts) == 0 {
		return nil
	}

	var sections []string
	for idx, s := range starts {
		if !strings.EqualFold(s.name, header) {
			continue
		}
		end := len(lines)
		if idx+1 < len(starts) {
			end = starts[idx+1].line
		}
		section := strings.Join(lines[s.line:end], "\n")
		sections = append(sections, strings.TrimSpace(section))
	}
	return sections
}

// mediaInfoFormatField 返回段落中第一个 "Format :" 的取值（去掉行内注释部分）。
func mediaInfoFormatField(section string) string {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return ""
	}
	if m := reMediaInfoFormatField.FindStringSubmatch(trimmed); len(m) >= 2 {
		format := strings.TrimSpace(m[1])
		// "HEVC / HEVC"、"AAC LC / AAC" 这类取第一个 token 前的部分。
		if idx := strings.Index(format, "/"); idx >= 0 {
			format = strings.TrimSpace(format[:idx])
		}
		return format
	}
	return ""
}

// videoCodecKeyFromFormatText 把 MediaInfo 的 Format 文本映射为标准视频编码键。
func videoCodecKeyFromFormatText(format string) string {
	upper := strings.ToUpper(strings.TrimSpace(format))
	if upper == "" {
		return ""
	}
	switch {
	case strings.Contains(upper, "AV1"):
		return "video.av1"
	case strings.Contains(upper, "VP9"):
		return "video.vp9"
	case strings.Contains(upper, "AVS2"):
		return "video.avs2"
	case strings.Contains(upper, "HEVC") || strings.Contains(upper, "H.265") || strings.Contains(upper, "H265"):
		return "video.h265"
	case strings.Contains(upper, "AVC") || strings.Contains(upper, "H.264") || strings.Contains(upper, "H264"):
		return "video.h264"
	case strings.Contains(upper, "VC-1") || strings.Contains(upper, "VC1"):
		return "video.vc1"
	case strings.Contains(upper, "MPEG-2"):
		return "video.mpeg2"
	case strings.Contains(upper, "MPEG-4"):
		return "video.mpeg4"
	case strings.Contains(upper, "XVID"):
		return "video.xvid"
	case strings.Contains(upper, "DIVX"):
		return "video.divx"
	case strings.Contains(upper, "VP8"):
		return "video.vp8"
	case strings.Contains(upper, "WVC1"):
		return "video.vc1"
	default:
		return ""
	}
}

// audioCodecKeyFromFormatText 把 MediaInfo 的 Format 文本映射为标准音频编码键。
// 与 inferStandardizedValues 中的音频编码判定保持一致（对齐标题/技术文本的 contains 口径）。
func audioCodecKeyFromFormatText(format string) string {
	upper := strings.ToUpper(strings.TrimSpace(format))
	if upper == "" {
		return ""
	}
	switch {
	case strings.Contains(upper, "TRUEHD"):
		if strings.Contains(upper, "ATMOS") {
			return "audio.truehd_atmos"
		}
		return "audio.truehd"
	case strings.Contains(upper, "DTS"):
		// DTS 家族细分统一走 dtsCodecKeyFromText：
		// 关键是不能用 contains("DTS X") 判 DTS:X —— MediaInfo 把 DTS-HD MA 的 Format 写作 `DTS XLL`，
		// 会被误命中（2026-10-09 实测：`DTS XLL` + `DTS-HD Master Audio` 被判成 audio.dtsx）。
		return dtsCodecKeyFromText(upper)
	case strings.Contains(upper, "E-AC-3") || strings.Contains(upper, "EAC3") ||
		strings.Contains(upper, "DDP") || strings.Contains(upper, "DOLBY DIGITAL PLUS") || strings.Contains(upper, "DD+"):
		if strings.Contains(upper, "ATMOS") || strings.Contains(upper, "JOC") {
			return "audio.ddp_atmos"
		}
		return "audio.ddp"
	case strings.Contains(upper, "AC-3") || strings.Contains(upper, "AC3") || strings.Contains(upper, "DOLBY DIGITAL"):
		// BDInfo 把 AC-3 写作 `Dolby Digital Audio`，MediaInfo 写作 `AC-3`，两者都要命中。
		// `Dolby Digital Plus`（E-AC-3）已由上面的 DDP 分支拦截，不会落到这里。
		return "audio.ac3"
	case strings.Contains(upper, "FLAC"):
		return "audio.flac"
	case strings.Contains(upper, "ALAC"):
		return "audio.alac"
	case strings.Contains(upper, "APE"):
		return "audio.ape"
	case strings.Contains(upper, "WAV"):
		return "audio.wav"
	case strings.Contains(upper, "OGG"), strings.Contains(upper, "VORBIS"):
		return "audio.ogg"
	case strings.Contains(upper, "DSD"):
		return "audio.dsd"
	case strings.Contains(upper, "AAC"):
		return "audio.aac"
	case strings.Contains(upper, "LPCM"), strings.Contains(upper, "PCM"):
		return "audio.lpcm"
	case strings.Contains(upper, "OPUS"):
		return "audio.opus"
	case strings.Contains(upper, "MP3"):
		return "audio.mp3"
	case strings.Contains(upper, "MP2"):
		return "audio.mp3"
	default:
		return ""
	}
}

var (
	// reDTSXMarker 命中真正的 DTS:X 写法：`DTS:X` / `DTS-X` / `DTS X` / `DTSX`，以及 MediaInfo 对 DTS:X 的 `DTS XLL X` 写法。
	// ⚠️ 不能用 contains("DTS X")：MediaInfo 把 **DTS-HD MA** 的 Format 写作 `DTS XLL`，
	// 该写法会被 `DTS X` 误命中并判成 DTS:X（2026-10-09 实测）。
	// Go RE2 无前瞻，用「X 后必须是非字母或字符串结尾」表达 X 的右边界。
	reDTSXMarker = regexp.MustCompile(`(?:DTS[:\-\s.]*XLL[:\-\s.]*X|DTS[:\-\s.]*X)(?:[^A-Za-z]|$)`)
	// reDTSHDHRMarker 命中 DTS-HD HR：`DTS-HD HR` / `DTS HD HR` / `DTS-HDMA` 之外的 HRA 写法。
	reDTSHDHRMarker = regexp.MustCompile(`DTS[:\-\s.]*(?:HD[:\-\s.]*HR|XLL[:\-\s.]*HRA)`)
	// reDTSHDMAMarker 命中 DTS-HD MA：`DTS-HD MA` / `DTS-HDMA` / `DTS XLL` / `DTS HD MA`。
	reDTSHDMAMarker = regexp.MustCompile(`DTS[:\-\s.]*(?:HD[:\-\s.]*MA|XLL)`)
)

// dtsCodecKeyFromText 判定 DTS 家族的具体标准编码键。判定顺序：DTS:X → DTS-HD HR → DTS-HD MA → 裸 DTS。
// 参数/返回：upper 为已大写的文本（MediaInfo 的 Format 值，或标题/技术文本 token）；非 DTS 家族返回空串。
// 失败场景：文本为空或不含 `DTS` 时返回空串。
// 副作用：无。
func dtsCodecKeyFromText(upper string) string {
	key, _, _ := DTSAudioCodecKeySpan(upper)
	return key
}

// DTSAudioCodecKeySpan 返回文本中 DTS 家族的标准编码键与命中区间，供展示层（标题组件「音频编码」）复用同一套判定口径。
// 参数/返回：text 为标题/技术文本（大小写不限）；返回标准键（audio.dtsx / audio.dts_hd_hr / audio.dts_hd_ma / audio.dts）
// 与命中区间 [start,end)（相对 text 的字节下标）；非 DTS 家族返回 ""、-1、-1。
// 失败场景：text 不含 `DTS` 时返回 ""、-1、-1。
// 副作用：无。
//
// 背景：判定顺序与 dtsCodecKeyFromText 完全一致（后者已改为调用本函数），避免出现第二套 DTS 判定口径。
// 返回区间是为了让调用方能从命中 token 之后继续搜索声道（如标题 `...DTS-HDMA5.1-52pt`：
// 命中 `DTS-HDMA` 后从 `5.1` 处取声道，而不是按固定长度偏移而错过）。
func DTSAudioCodecKeySpan(text string) (string, int, int) {
	upper := strings.ToUpper(text)
	if !strings.Contains(upper, "DTS") {
		return "", -1, -1
	}
	if loc := reDTSXMarker.FindStringIndex(upper); loc != nil {
		return "audio.dtsx", loc[0], loc[1]
	}
	if loc := reDTSHDHRMarker.FindStringIndex(upper); loc != nil {
		return "audio.dts_hd_hr", loc[0], loc[1]
	}
	if loc := reDTSHDMAMarker.FindStringIndex(upper); loc != nil {
		return "audio.dts_hd_ma", loc[0], loc[1]
	}
	// 纯文本兜底（MediaInfo 的 Commercial name 写法），此时只有 `DTS` 这一处可定位。
	dtsStart := strings.Index(upper, "DTS")
	dtsEnd := dtsStart + len("DTS")
	if strings.Contains(upper, "HIGH RESOLUTION") {
		return "audio.dts_hd_hr", dtsStart, dtsEnd
	}
	if strings.Contains(upper, "MASTER AUDIO") {
		return "audio.dts_hd_ma", dtsStart, dtsEnd
	}
	return "audio.dts", dtsStart, dtsEnd
}
