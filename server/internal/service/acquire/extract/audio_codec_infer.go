package extract

import "strings"

// inferAudioCodecFromText 从（已归一的）标题/技术文本 token 中推断音频编码标准键。
// 判据为 contains，与站点映射口径保持一致；文本为空或无法识别时返回空串。
// 说明：此处刻意与 mediainfo_codec.go:audioCodecKeyFromFormatText 分开维护——
// 后者面向 MediaInfo 的 `Format :` 字段（多若干细分编码分支），
// 前者面向发布名/技术文本的宽松 token 匹配，两者历史口径不同，合一会造成既有行为漂移。
func inferAudioCodecFromText(upperText string) string {
	upper := strings.ToUpper(strings.TrimSpace(upperText))
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
		// 与 MediaInfo `Format :` 分支共用同一套 DTS 细分判定：
		// 不能只看 contains("DTS X")，否则 `DTS XLL`（DTS-HD MA 的 MediaInfo 写法）会被判成 DTS:X。
		return dtsCodecKeyFromText(upper)
	case strings.Contains(upper, "E-AC-3") || strings.Contains(upper, "DDP") || strings.Contains(upper, "DD+"):
		// 对齐 MediaInfo/BDInfo 解析：E-AC-3 + JOC 属独立标准值 audio.ddp_atmos，
		// 漏判会退化成普通 DDP，发布到支持杜比全景声的站点时丢失 Atmos 标记。
		if strings.Contains(upper, "ATMOS") || strings.Contains(upper, "JOC") {
			return "audio.ddp_atmos"
		}
		return "audio.ddp"
	case strings.Contains(upper, "AC-3") || strings.Contains(upper, "AC3"):
		return "audio.ac3"
	case strings.Contains(upper, "FLAC"):
		return "audio.flac"
	case strings.Contains(upper, "AV3A") || strings.Contains(upper, "AUDIO VIVID"):
		return "audio.av3a"
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

// InferAudioCodecKey 按标准值口径推断音频编码标准键（audio.*）。
// 优先级：MediaInfo 的 Audio 段 Format（多轨取规格最高）> 标题 token > 「标题+媒体文本」合并文本 token。
// 参数/返回：title 为种子标题；mediainfo 为 MediaInfo/BDInfo 原文；全部无法识别时返回空串。
// 副作用：无。
//
// 该函数是音频编码的唯一判定入口：标准值（inferStandardizedValues）与标签链路
// （tagging.ExtractRawTagsFromAudioCodec）共用，避免两处口径漂移——
// 典型症状是站点「音频编码」字段判出 Atmos、而「标签」字段漏标（或反之）。
func InferAudioCodecKey(title, mediainfo string) string {
	sanitized := SanitizeMediaTextForAnalysis(mediainfo)
	normalizedTitle := normalizeAudioCodecTokensForInference(title)
	normalizedCombined := normalizeAudioCodecTokensForInference(title + "\n" + sanitized)

	if fromMediaInfo := inferAudioCodecFromMediainfo(sanitized); fromMediaInfo != "" {
		return upgradeAudioCodecKeyWithAtmos(fromMediaInfo, normalizedCombined)
	}
	fromTitle := inferAudioCodecFromText(normalizedTitle)
	if fromTitle == "" {
		// 标题没有可用编码 token 时才回退「标题+媒体文本」合并文本（原链路口径不变）。
		return inferAudioCodecFromText(normalizedCombined)
	}
	return upgradeAudioCodecKeyWithAtmos(fromTitle, normalizedCombined)
}

// upgradeAudioCodecKeyWithAtmos 在「同族基础编码 + 全景声线索」并存时把标准键升级为对应的 Atmos 键。
// 背景：发布名常只写「TrueHD7.1」而把 Atmos 落在 MediaInfo/BDInfo 的音轨描述里
// （BDInfo：`Dolby TrueHD/Atmos Audio`；MediaInfo：`Commercial name : Dolby TrueHD with Dolby Atmos`）。
// 原链路里标题 token 一旦命中就短路，合并文本的 Atmos 线索永远读不到，
// 导致「音频编码」字段与「标签」字段同时漏标全景声（2026-10-09 实测）。
// 只在同族编码内升级（TrueHD→TrueHD Atmos / DDP→DDP Atmos），不跨编码族改判，
// 避免媒体文本里的无关 codec 噪声覆盖标题结论。
// 参数/返回：key 为已判出的标准键；text 为归一化后的「标题+媒体文本」；返回升级后的键（不满足条件时原样返回）。
// 失败场景：key 非 TrueHD/DDP 基础键、text 为空或无 Atmos/JOC 线索时原样返回。
// 副作用：无。
func upgradeAudioCodecKeyWithAtmos(key, text string) string {
	upper := strings.ToUpper(strings.TrimSpace(text))
	if upper == "" {
		return key
	}
	switch key {
	case "audio.truehd":
		if strings.Contains(upper, "ATMOS") {
			return "audio.truehd_atmos"
		}
	case "audio.ddp":
		if strings.Contains(upper, "ATMOS") || strings.Contains(upper, "JOC") {
			return "audio.ddp_atmos"
		}
	}
	return key
}

// AudioCodecKeyIndicatesAtmos 判断音频编码标准键是否为杜比全景声（TrueHD Atmos / DDP Atmos）。
// 参数/返回：key 为 audio.* 标准键；命中返回 true。
// 失败场景：空串或非 Atmos 键返回 false。
// 副作用：无。
func AudioCodecKeyIndicatesAtmos(key string) bool {
	switch strings.TrimSpace(key) {
	case "audio.truehd_atmos", "audio.ddp_atmos":
		return true
	default:
		return false
	}
}
