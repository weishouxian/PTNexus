package repair

import "testing"

func TestMediaTextHasChineseSubtitle(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "MediaInfo 键值 Language : Chinese",
			text: "Video\nID : 1\nFormat : HEVC\n\nAudio #1\nID : 2\nLanguage : English\n\nText #1\nID : 3\nLanguage : Chinese\n",
			want: true,
		},
		{
			name: "MediaInfo 键值 Language : 中文",
			text: "Text #1\nLanguage : 中文\nFormat : PGS\n",
			want: true,
		},
		{
			name: "MediaInfo 键值 Language : 中字",
			text: "Text #1\nLanguage : 中字\n",
			want: true,
		},
		{
			name: "MediaInfo 只有英文字幕",
			text: "Text #1\nLanguage : English\n",
			want: false,
		},
		{
			name: "MediaInfo 只有日语音轨无字幕",
			text: "Audio #1\nLanguage : Japanese\n\nVideo\nFormat : HEVC\n",
			want: false,
		},
		{
			name: "MediaInfo 音轨是国语但字幕是英文（只认字幕轨，应判无中文字幕）",
			text: "Audio #1\nLanguage : Chinese\n\nText #1\nLanguage : English\n",
			want: false,
		},
		{
			name: "BDInfo SUBTITLES 段含 Chinese",
			text: "SUBTITLES:\n\nCodec                 Language  Bitrate  Description\n-----                 --------  -------  -----------\nPresentation Graphics Chinese   12.345 kbps\nPresentation Graphics English   11.111 kbps\n\nFILES:\n",
			want: true,
		},
		{
			name: "BDInfo SUBTITLES 段含 中字",
			text: "SUBTITLES:\n\nCodec                 Language  Bitrate\nPresentation Graphics 中字       12.345 kbps\n",
			want: true,
		},
		{
			name: "BDInfo SUBTITLES 段只有英文",
			text: "SUBTITLES:\n\nCodec                 Language  Bitrate\nPresentation Graphics English   12.345 kbps\n",
			want: false,
		},
		{
			name: "BDInfo 音轨是中文但字幕是英文（AUDIO 段不应误判）",
			text: "AUDIO:\n\nCodec   Language  Bitrate\nDTS-HD  Chinese   2000 kbps\n\nSUBTITLES:\n\nCodec                 Language\nPresentation Graphics English\n",
			want: false,
		},
		{
			name: "空文本",
			text: "",
			want: false,
		},
		{
			name: "无任何语言信息",
			text: "Video\nFormat : HEVC\nWidth : 3840\nHeight : 2160\n",
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaTextHasChineseSubtitle(tc.text); got != tc.want {
				t.Errorf("mediaTextHasChineseSubtitle() = %v, want %v\n输入文本:\n%s", got, tc.want, tc.text)
			}
		})
	}
}

func TestResolveMediaTextFromScreenshotInput(t *testing.T) {
	cases := []struct {
		name      string
		payload   map[string]any
		sourceInfo map[string]any
		want      string
	}{
		{
			name:       "sourceInfo.mediainfo 优先",
			payload:    map[string]any{"mediainfo": "payload-mediainfo"},
			sourceInfo: map[string]any{"mediainfo": "source-mediainfo"},
			want:       "source-mediainfo",
		},
		{
			name:       "回退 payload.current_mediainfo",
			payload:    map[string]any{"current_mediainfo": "current-mediainfo"},
			sourceInfo: map[string]any{},
			want:       "current-mediainfo",
		},
		{
			name:       "回退 payload.mediainfo",
			payload:    map[string]any{"mediainfo": "payload-mediainfo"},
			sourceInfo: map[string]any{},
			want:       "payload-mediainfo",
		},
		{
			name:       "全空返回空",
			payload:    map[string]any{},
			sourceInfo: map[string]any{},
			want:       "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMediaTextFromScreenshotInput(tc.payload, tc.sourceInfo); got != tc.want {
				t.Errorf("resolveMediaTextFromScreenshotInput() = %q, want %q", got, tc.want)
			}
		})
	}
}
