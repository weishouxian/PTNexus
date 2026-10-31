package title

import (
	"fmt"
	"strings"

	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// AudioTrackPreview 表示一条音轨在前端「多音轨预生成标题」里展示所需的最小信息。
type AudioTrackPreview struct {
	// CodecKey 为该音轨的标准音频编码键（audio.*）。
	CodecKey string `json:"codec_key"`
	// Display 为该音轨在标题里的展示写法（如 TrueHD 7.1 / DTS:X / DDP 5.1）。
	Display string `json:"display"`
	// BitRateKbps 为码率（kbps）。
	BitRateKbps float64 `json:"bit_rate_kbps"`
	// Channels 为声道数（如 2 / 6 / 8）。
	Channels int `json:"channels"`
	// Index 为音轨序号（从 1 开始）。
	Index int `json:"index"`
}

// AudioTitlePreview 表示按某条音轨策略预生成的标题。
type AudioTitlePreview struct {
	// Policy 为音轨策略键（1=第一条 / 2=码率最高 / 3=规格最高）。
	Policy int `json:"policy"`
	// Label 为策略中文标签（第一条音轨 / 码率最高 / 规格最高）。
	Label string `json:"label"`
	// CodecKey 为选中音轨的标准音频编码键。
	CodecKey string `json:"codec_key"`
	// Title 为替换音频编码组件后重拼出的完整标题。
	Title string `json:"title"`
}

// BuildAudioTitlePreviews 基于标题组件与媒体文本，生成多音轨的 3 个预生成标题。
// 参数/返回：components 为已解析的标题组件；mediainfo 为 MediaInfo/BDInfo 文本；
// 返回（音轨列表，预生成标题列表）。非多音轨（≤1 条音轨）或解析不出音轨时返回空。
// 副作用：无。
//
// 背景：多音轨种子在核对页「主要信息」需在主标题下方展示三种口径的预生成标题
// （第一条音轨 / 码率最高 / 规格最高），供用户对照发种标题随音轨策略如何变化。
// 预生成标题 = 把「音频编码」组件值替换为选中音轨的展示 token 后，复用同一套
// BuildPreviewTitleFromTitleComponents 重拼，保证与正式发种标题口径一致。
func BuildAudioTitlePreviews(components []map[string]any, mediainfo string) ([]AudioTrackPreview, []AudioTitlePreview) {
	trimmedMedia := strings.TrimSpace(mediainfo)
	if trimmedMedia == "" {
		return nil, nil
	}
	tracks := parser.ParseAudioTracksFromMediainfo(trimmedMedia)
	return BuildAudioTitlePreviewsFromTracks(components, tracks)
}

// BuildAudioTitlePreviewsFromTracks 基于已解析的音轨列表生成多音轨的展示数据与 3 个预生成标题。
// 参数/返回：components 为标题组件；tracks 为已解析的音轨列表；返回（音轨展示列表，预生成标题列表）。
// 非多音轨（≤1 条音轨）或无法识别编码时返回空。副作用：无。
//
// 背景：数据库 seed_parameters 已持久化 audio_tracks（JSON），读取种子信息时无需再解析
// mediainfo，直接用库内音轨列表生成预生成标题，保证与发种标题口径一致。
func BuildAudioTitlePreviewsFromTracks(components []map[string]any, tracks []parser.AudioTrack) ([]AudioTrackPreview, []AudioTitlePreview) {
	if len(tracks) < 2 {
		return nil, nil
	}

	trackPreviews := make([]AudioTrackPreview, 0, len(tracks))
	for _, track := range tracks {
		trackPreviews = append(trackPreviews, AudioTrackPreview{
			CodecKey:    track.CodecKey,
			Display:     audioTrackDisplay(track),
			BitRateKbps: track.BitRateKbps,
			Channels:    track.Channels,
			Index:       track.Index,
		})
	}

	policies := []struct {
		policy parser.AudioTrackPolicy
		label  string
	}{
		{parser.AudioTrackPolicyFirst, "第一条音轨"},
		{parser.AudioTrackPolicyHighestBitRate, "码率最高"},
		{parser.AudioTrackPolicyHighestSpec, "规格最高"},
	}

	previews := make([]AudioTitlePreview, 0, len(policies))
	for _, item := range policies {
		selected := parser.SelectAudioTrack(tracks, item.policy)
		if selected.CodecKey == "" {
			continue
		}
		display := audioTrackDisplay(selected)
		if display == "" {
			continue
		}
		previews = append(previews, AudioTitlePreview{
			Policy:   int(item.policy),
			Label:    item.label,
			CodecKey: selected.CodecKey,
			Title:    rebuildPreviewTitleWithAudio(components, display),
		})
	}

	return trackPreviews, previews
}

// audioTrackDisplay 把一条音轨拼成标题里的展示写法：编码 + 声道布局（+ Atmos，已并入编码 token）。
// 参数/返回：track 为 extract 包的结构化音轨；编码键无法识别时返回空串。
// 副作用：无。
//
// 声道布局由声道总数映射：8→7.1、6→5.1、2→2.0、1→1.0，其余按 N.0；
// 该口径与 processing/media:channelLayoutFromMediaInfo 的 count 分支一致，满足「区分音轨」的展示需求。
func audioTrackDisplay(track parser.AudioTrack) string {
	codec := parser.AudioDisplayTokenFromCodecKey(track.CodecKey)
	if codec == "" {
		return ""
	}
	layout := channelsLayoutLabel(track.Channels)
	if layout != "" {
		return codec + " " + layout
	}
	return codec
}

// channelsLayoutLabel 把声道总数映射为声道布局展示（8→7.1、6→5.1、2→2.0…）。
func channelsLayoutLabel(channels int) string {
	switch channels {
	case 8:
		return "7.1"
	case 6:
		return "5.1"
	case 2:
		return "2.0"
	case 1:
		return "1.0"
	default:
		if channels > 0 {
			return fmt.Sprintf("%d.0", channels)
		}
		return ""
	}
}

// rebuildPreviewTitleWithAudio 把「音频编码」组件值替换为指定音频 token 后重拼标题。
// 参数/返回：components 为标题组件；audioDisplay 为替换后的音频编码展示 token；返回重拼标题。
// 副作用：无；不修改入参组件。
func rebuildPreviewTitleWithAudio(components []map[string]any, audioDisplay string) string {
	cloned := make([]any, 0, len(components))
	for _, component := range components {
		item := make(map[string]any, len(component)+1)
		for key, value := range component {
			item[key] = value
		}
		if strings.TrimSpace(processingshared.ToString(item["key"], "")) == "音频编码" {
			item["value"] = audioDisplay
		}
		cloned = append(cloned, item)
	}
	// fallbackTitle 传空：组件已含「主标题」，重拼不需要兜底标题。
	return BuildPreviewTitleFromTitleComponents(cloned, "")
}
