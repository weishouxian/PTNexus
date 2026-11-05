package extract

import "testing"

// TestParseAudioTracksTrueHDMLPFBA 回归：MediaInfo 把 TrueHD 的 Format 写作 `MLP FBA`，
// 真实编码名在 `Commercial name : Dolby TrueHD with Dolby Atmos` 与 `Codec ID : A_TRUEHD`。
// 单看 Format 字段会漏判（CodecKey 为空），导致「码率最高 / 规格最高」策略全落到次轨（如 DD 5.1）。
// 同时验证大码率的千位分隔空格（`3 223 kb/s`）被正确归一，而不是抓成 `3`。
func TestParseAudioTracksTrueHDMLPFBA(t *testing.T) {
	mediainfo := `Audio #1
ID                                       : 2
Format                                   : MLP FBA 16-ch
Format/Info                              : Meridian Lossless Packing FBA with 16-channel presentation
Commercial name                          : Dolby TrueHD with Dolby Atmos
Codec ID                                 : A_TRUEHD
Bit rate mode                            : Variable
Bit rate                                 : 3 223 kb/s
Maximum bit rate                         : 5 028 kb/s
Channel(s)                               : 8 channels
Default                                  : Yes

Audio #2
ID                                       : 3
Format                                   : AC-3
Format/Info                              : Audio Coding 3
Commercial name                          : Dolby Digital
Codec ID                                 : A_AC3
Bit rate                                 : 448 kb/s
Channel(s)                               : 6 channels
Default                                  : No`

	tracks := parseAudioTracksFromMediainfo(mediainfo)
	if len(tracks) != 2 {
		t.Fatalf("音轨数 got=%d want=2", len(tracks))
	}

	trueHD := tracks[0]
	if trueHD.CodecKey != "audio.truehd_atmos" {
		t.Errorf("首轨 CodecKey got=%q want=audio.truehd_atmos", trueHD.CodecKey)
	}
	if trueHD.BitRateKbps != 3223 {
		t.Errorf("首轨 BitRateKbps got=%.0f want=3223（千位分隔空格未归一）", trueHD.BitRateKbps)
	}
	if trueHD.Channels != 8 {
		t.Errorf("首轨 Channels got=%d want=8", trueHD.Channels)
	}

	ac3 := tracks[1]
	if ac3.CodecKey != "audio.ac3" {
		t.Errorf("次轨 CodecKey got=%q want=audio.ac3", ac3.CodecKey)
	}

	// 码率最高应选 TrueHD（3223 > 448）。
	if got := selectAudioTrack(tracks, AudioTrackPolicyHighestBitRate); got.CodecKey != "audio.truehd_atmos" {
		t.Errorf("码率最高 got=%q want=audio.truehd_atmos", got.CodecKey)
	}
	// 规格最高应选 TrueHD Atmos（rank 100 > ac3 55）。
	if got := selectAudioTrack(tracks, AudioTrackPolicyHighestSpec); got.CodecKey != "audio.truehd_atmos" {
		t.Errorf("规格最高 got=%q want=audio.truehd_atmos", got.CodecKey)
	}
}
