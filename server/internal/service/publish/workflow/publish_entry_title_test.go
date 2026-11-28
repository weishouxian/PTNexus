package workflow

import "testing"

// 发种前的标题字符集门禁：标题只接受英文，汉字/中文标点必须被识别出来，
// 纯英文标题（含数字、点号、连字符）不能误报。
func TestChineseTitleChars(t *testing.T) {
	cases := []struct {
		name  string
		title string
		want  int
	}{
		{name: "纯英文标题", title: "Blossoms.Shanghai.2023.S01.1080p.WEB-DL.H264.DDP5.1-OurTV", want: 0},
		{name: "英文含数字与点号", title: "Dune.Part.Two.2024.2160p.UHD.BluRay.REMUX.HDR.HEVC.TrueHD.7.1.Atmos-FRDS", want: 0},
		{name: "主标题为中文", title: "繁花 2023 1080p WEB-DL H264-OurTV", want: 2},
		{name: "重复汉字去重", title: "繁花繁花 2023", want: 2},
		{name: "中文标点", title: "Blossoms, Shanghai（2023）", want: 2},
		{name: "书名号", title: "《繁花》2023", want: 4},
		{name: "空标题", title: "   ", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := chineseTitleChars(tc.title)
			if len(got) != tc.want {
				t.Fatalf("chineseTitleChars(%q) 命中 %d 个字符（%q），期望 %d 个", tc.title, len(got), string(got), tc.want)
			}
		})
	}
}

// 命中数量上限：字符种类很多时最多返回 8 个，避免提示文案过长。
func TestChineseTitleCharsLimit(t *testing.T) {
	title := "一二三四五六七八九十"
	got := chineseTitleChars(title)
	if len(got) != 8 {
		t.Fatalf("字符上限应为 8，实际 %d（%q）", len(got), string(got))
	}
}
