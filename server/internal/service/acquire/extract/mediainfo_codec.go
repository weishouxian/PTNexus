package extract

import (
	"regexp"
	"strings"
)

var (
	// reMediaInfoSectionHeader 识别 MediaInfo 的段落头（General/Video/Audio/Text/Menu/Chapters）。
	reMediaInfoSectionHeader = regexp.MustCompile(`(?im)^\s*(General|Video|Audio|Text|Menu|Chapters)(?:\s*#\d+)?\s*$`)
	// reMediaInfoFormatField 提取段落内 "Format : xxx" / "Format : xxx / yyy" 的取值。
	reMediaInfoFormatField = regexp.MustCompile(`(?im)^\s*Format\s*:\s*([^\r\n]+)`)
)

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
	for _, section := range allMediaInfoAudioSections(mediainfo) {
		format := mediaInfoFormatField(section)
		if format == "" {
			continue
		}
		key := audioCodecKeyFromFormatText(format)
		if key == "" {
			continue
		}
		rank, ok := audioCodecRank[key]
		if !ok {
			rank = 0
		}
		if rank > bestRank {
			bestRank = rank
			best = key
		}
	}
	return best
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
