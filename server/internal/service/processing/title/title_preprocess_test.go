package title

import "testing"

// titleComponentValue 从组件数组里取指定 key 的值，缺失返回空串。
func titleComponentValue(components []map[string]any, key string) string {
	for _, item := range components {
		if toStringAny(item["key"], "") == key {
			return toStringAny(item["value"], "")
		}
	}
	return ""
}

// TestBuildSimpleTitleComponentsDotSeparatedWithCJKPrefix 覆盖「中文片名.英文片名.技术参数」形态：
// 解析前应先剥掉中文前缀、把点分隔归一成空格，否则 `WEB-DL.H265.AAC-ADWeb` 会被整段当成制作组，
// 媒介/视频编码/音频编码全部为空（2026-11-22 实测问题）。
func TestBuildSimpleTitleComponentsDotSeparatedWithCJKPrefix(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		expect  map[string]string
		require []string
	}{
		{
			name:  "都市古仙医 S01E231",
			input: "都市古仙医.The.Immortal.Doctor.S01E231.2024.2160p.WEB-DL.H265.AAC-ADWeb",
			expect: map[string]string{
				"主标题": "The Immortal Doctor",
				"季集":  "S01E231",
				"年份":  "2024",
				"分辨率": "2160p",
				"媒介":  "WEB-DL",
				"视频编码": "H.265",
				"音频编码": "AAC",
				"制作组": "ADWeb",
				"无法识别": "",
			},
			require: []string{"主标题", "季集", "年份", "分辨率", "媒介", "视频编码", "音频编码", "制作组", "无法识别"},
		},
		{
			name:  "纯中文剧名 + 点分隔技术参数（不得剥空）",
			input: "三体.2023.S01.2160p.WEB-DL.H265.DDP5.1-ADWeb",
			expect: map[string]string{
				"主标题": "三体",
				"季集":  "S01",
				"年份":  "2023",
				"分辨率": "2160p",
				"媒介":  "WEB-DL",
				"视频编码": "H.265",
				"音频编码": "DDP 5.1",
				"制作组": "ADWeb",
			},
			require: []string{"主标题", "季集", "年份", "分辨率", "媒介", "视频编码", "音频编码", "制作组"},
		},
		{
			name:  "中文片名含数字 + 英文片名",
			input: "流浪地球2.The.Wandering.Earth.II.2023.2160p.WEB-DL.H265.AAC-ADWeb",
			expect: map[string]string{
				"主标题": "The Wandering Earth II",
				"年份":  "2023",
				"媒介":  "WEB-DL",
				"视频编码": "H.265",
				"音频编码": "AAC",
				"制作组": "ADWeb",
			},
			require: []string{"主标题", "年份", "媒介", "视频编码", "音频编码", "制作组"},
		},
		{
			name:  "空格分隔纯中文剧名",
			input: "三体 S01 2023 2160p WEB-DL H265-ADWeb",
			expect: map[string]string{
				"主标题": "三体",
				"季集":  "S01",
				"媒介":  "WEB-DL",
				"视频编码": "H.265",
				"制作组": "ADWeb",
			},
			require: []string{"主标题", "季集", "媒介", "视频编码", "制作组"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			components := BuildSimpleTitleComponents(tc.input, "")
			for _, key := range tc.require {
				want, ok := tc.expect[key]
				if !ok {
					continue
				}
				if got := titleComponentValue(components, key); got != want {
					t.Errorf("%s: got %q, want %q", key, got, want)
				}
			}
		})
	}
}

// TestStripLeadingCJKTitle 覆盖中文前缀剥离的边界：技术词开头不剥、无中文不剥。
func TestStripLeadingCJKTitle(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"都市古仙医 The Immortal Doctor", "The Immortal Doctor"},
		{"名侦探柯南 Detective Conan", "Detective Conan"},
		{"长安十二时辰 The Longest Day In Chang'an", "The Longest Day In Chang'an"},
		// 剩余部分以季集/年份/技术标签开头时不剥离
		{"三体 S01 2023 2160p", "三体 S01 2023 2160p"},
		{"三体 2023 2160p WEB-DL", "三体 2023 2160p WEB-DL"},
		{"三体 2160p WEB-DL", "三体 2160p WEB-DL"},
		{"三体 WEB-DL H265", "三体 WEB-DL H265"},
		{"三体 E05 2023", "三体 E05 2023"},
		// 无中文前缀，原样返回
		{"Some Movie 2020", "Some Movie 2020"},
		{"", ""},
		{"三体", "三体"},
	}
	for _, tc := range cases {
		if got := stripLeadingCJKTitle(tc.input); got != tc.want {
			t.Errorf("stripLeadingCJKTitle(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// TestNormalizeTitleForParsingKeepsCodecAndChannelDots 确认点归一不会破坏 H.265 与 5.1。
func TestNormalizeTitleForParsingKeepsCodecAndChannelDots(t *testing.T) {
	cases := []struct{ input, want string }{
		{"Movie.2020.1080p.WEB-DL.H.265.DDP5.1-GROUP", "Movie 2020 1080p WEB-DL H.265 DDP5.1-GROUP"},
		{"Mr. Robot S01E01 2015 1080p", "Mr Robot S01E01 2015 1080p"},
	}
	for _, tc := range cases {
		if got := normalizeTitleForParsing(tc.input); got != tc.want {
			t.Errorf("normalizeTitleForParsing(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
