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

// inferAudioCodecFromMediainfo 从 MediaInfo 的全部 Audio 段的 Format 字段推断音频编码，取规格最高的一条。
// 参数/返回：mediainfo 为原始 MediaInfo 文本；无法定位 Audio 段或所有 Format 均无法识别时返回空串。
// 副作用：无。
//
// 背景：多音轨（如 AAC 附属轨 + E-AC-3 主轨）时，首条音轨未必是最高规格；取最高规格能避免
// 把标题声明的 DDP 误判成 AAC，反之亦然。
func inferAudioCodecFromMediainfo(mediainfo string) string {
	best := ""
	bestRank := -1
	for _, track := range parseAudioTracksFromMediainfo(mediainfo) {
		rank, ok := audioCodecRank[track.CodecKey]
		if !ok {
			rank = 0
		}
		if rank > bestRank {
			bestRank = rank
			best = track.CodecKey
		}
	}
	return best
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
		return nil
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
	// AudioTrackPolicyFirst 取第一条音轨。
	AudioTrackPolicyFirst AudioTrackPolicy = 1
	// AudioTrackPolicyHighestBitRate 取码率最高的一条（默认）。
	AudioTrackPolicyHighestBitRate AudioTrackPolicy = 2
	// AudioTrackPolicyHighestSpec 取规格最高的一条。
	AudioTrackPolicyHighestSpec AudioTrackPolicy = 3
)

// SelectAudioTrack 按策略从音轨列表中选出一条。
// 参数/返回：tracks 为音轨列表；policy 为选择策略；返回选中的音轨（列表为空时返回零值）。
// 副作用：无。策略非法时回退到码率最高（默认策略）。
func selectAudioTrack(tracks []AudioTrack, policy AudioTrackPolicy) AudioTrack {
	if len(tracks) == 0 {
		return AudioTrack{}
	}
	switch policy {
	case AudioTrackPolicyFirst:
		return tracks[0]
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
		// 默认：码率最高。
		best := tracks[0]
		for _, track := range tracks[1:] {
			if track.BitRateKbps > best.BitRateKbps {
				best = track
			}
		}
		return best
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
	case strings.Contains(upper, "DTS:X") || strings.Contains(upper, "DTS X"):
		return "audio.dtsx"
	case strings.Contains(upper, "DTS-HD MA") || strings.Contains(upper, "DTS HD MA"):
		return "audio.dts_hd_ma"
	case strings.Contains(upper, "DTS-HD HR") || strings.Contains(upper, "DTS HD HR"):
		return "audio.dts_hd_hr"
	case strings.Contains(upper, "DTS"):
		return "audio.dts"
	case strings.Contains(upper, "E-AC-3") || strings.Contains(upper, "EAC3") ||
		strings.Contains(upper, "DDP") || strings.Contains(upper, "DOLBY DIGITAL PLUS") || strings.Contains(upper, "DD+"):
		if strings.Contains(upper, "ATMOS") || strings.Contains(upper, "JOC") {
			return "audio.ddp_atmos"
		}
		return "audio.ddp"
	case strings.Contains(upper, "AC-3") || strings.Contains(upper, "AC3"):
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
