package descclean

import "testing"

func TestTrimDescription(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "影片参数：含完整标记及之后内容",
			in:   "影片介绍文字\n\n[color=blue][b]【影片参数】[/b][/color]\nGeneral\nVideo: 12345",
			want: "影片介绍文字",
		},
		{
			name: "影片参数：标记在开头",
			in:   "[color=blue][b]【影片参数】[/b][/color]\nGeneral\nVideo: 12345",
			want: "",
		},
		{
			name: "影片参数：标记在中间保留前段",
			in:   "译名 xxx\n年代 2024\n[color=blue][b]【影片参数】[/b][/color]\nVideo: x",
			want: "译名 xxx\n年代 2024",
		},
		{
			name: "影片参数：b 标签在外层也能命中回退",
			in:   "前面介绍\n[b][color=blue]【影片参数】[/color][/b]\n后面 mediainfo",
			want: "前面介绍",
		},
		{
			name: "影片参数：回退仅中文标题无bbcode",
			in:   "前面介绍\n【影片参数】\n后面 mediainfo",
			want: "前面介绍",
		},
		{
			name: "影片参数：同行关键词保留前文",
			in:   "前面介绍 【影片参数】 后面 mediainfo",
			want: "前面介绍",
		},
		{
			name: "更多视频截图：原文 quote 包裹",
			in:   "影片介绍正文\n\n[quote][size=3][color=royalblue][b]★★★★★ 更多视频截图 ★★★★★[/b][/color][/size][/quote]\n[img]https://a.png[/img]\n[img]https://b.png[/img]",
			want: "影片介绍正文",
		},
		{
			name: "更多视频截图：仅 b 标签包裹",
			in:   "影片介绍正文\n[b]★★★★★ 更多视频截图 ★★★★★[/b]\n[img]https://a.png[/img]",
			want: "影片介绍正文",
		},
		{
			name: "更多视频截图：无星号但有开启标签",
			in:   "影片介绍正文\n[quote][size=3][b]更多视频截图[/b][/size][/quote]\n[img]https://a.png[/img]",
			want: "影片介绍正文",
		},
		{
			name: "更多视频截图：纯文本无bbcode无星号",
			in:   "影片介绍正文\n更多视频截图\n[img]https://a.png[/img]",
			want: "影片介绍正文",
		},
		{
			name: "更多视频截图：标记在开头",
			in:   "[quote][b]★★★★★ 更多视频截图 ★★★★★[/b][/quote]\n[img]https://a.png[/img]",
			want: "",
		},
		{
			name: "更多视频截图：空心星号也命中",
			in:   "简介\n☆★☆ 更多视频截图 ☆★☆\n[img]x[/img]",
			want: "简介",
		},
		{
			name: "更多视频截图：只删关键词及其后内容，同行前文保留",
			in:   "正文提到更多视频截图这个功能\n[img]x[/img]",
			want: "正文提到",
		},
		{
			name: "两类标记同时存在：取最靠前者",
			in:   "正文\n[b]★★★★★ 更多视频截图 ★★★★★[/b]\n[img]x[/img]\n[color=blue][b]【影片参数】[/b][/color]\nVideo: x",
			want: "正文",
		},
		{
			name: "两类标记同时存在：影片参数在前",
			in:   "正文\n[color=blue][b]【影片参数】[/b][/color]\nVideo: x\n[b]★★★★★ 更多视频截图 ★★★★★[/b]\n[img]x[/img]",
			want: "正文",
		},
		{
			name: "不含标记原样返回",
			in:   "普通简介无参数段",
			want: "普通简介无参数段",
		},
		{
			name: "空串",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TrimDescription(c.in)
			if got != c.want {
				t.Errorf("TrimDescription(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// TestTrimDescriptionAtMovieParamsAlias 校验旧入口名与新入口行为一致（向后兼容）。
func TestTrimDescriptionAtMovieParamsAlias(t *testing.T) {
	in := "影片介绍正文\n\n[quote][size=3][color=royalblue][b]★★★★★ 更多视频截图 ★★★★★[/b][/color][/size][/quote]\n[img]https://a.png[/img]"
	want := "影片介绍正文"
	if got := TrimDescriptionAtMovieParams(in); got != want {
		t.Errorf("TrimDescriptionAtMovieParams(%q) = %q, want %q", in, got, want)
	}
}
