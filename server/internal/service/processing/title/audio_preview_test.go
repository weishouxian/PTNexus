package title

import (
	"testing"

	parser "github.com/pt-nexus/server/internal/service/acquire/extract"
)

// TestAudioTrackDisplayAtmosOrder 回归：Atmos 细分音轨（TrueHD Atmos / DDP Atmos）的展示写法
// 必须是「编码 + 声道 + Atmos」（如 `TrueHD 7.1 Atmos`），而不是「编码 + Atmos + 声道」。
// 根因：AudioDisplayTokenFromCodecKey 对 atmos 键返回 `TrueHD Atmos`（Atmos 已并入编码 token），
// 若直接在后面拼声道会得到 `TrueHD Atmos 7.1` 的错误顺序。
func TestAudioTrackDisplayAtmosOrder(t *testing.T) {
	cases := []struct {
		name    string
		codecKey string
		channels int
		want     string
	}{
		{"TrueHD Atmos 8ch", "audio.truehd_atmos", 8, "TrueHD 7.1 Atmos"},
		{"DDP Atmos 6ch", "audio.ddp_atmos", 6, "DDP 5.1 Atmos"},
		{"裸 TrueHD 8ch 不带 Atmos", "audio.truehd", 8, "TrueHD 7.1"},
		{"AC-3 6ch", "audio.ac3", 6, "DD 5.1"},
		{"DDP 6ch 不带 Atmos", "audio.ddp", 6, "DDP 5.1"},
	}

	for _, tc := range cases {
		got := audioTrackDisplay(parser.AudioTrack{CodecKey: tc.codecKey, Channels: tc.channels})
		if got != tc.want {
			t.Errorf("%s: display got=%q want=%q", tc.name, got, tc.want)
		}
	}
}
