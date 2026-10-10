package uploader

import "testing"

func TestTrimDescriptionAtMovieParams(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "含完整标记及之后内容",
			in:   "影片介绍文字\n\n[color=blue][b]【影片参数】[/b][/color]\nGeneral\nVideo: 12345",
			want: "影片介绍文字",
		},
		{
			name: "标记在开头",
			in:   "[color=blue][b]【影片参数】[/b][/color]\nGeneral\nVideo: 12345",
			want: "",
		},
		{
			name: "标记在中间保留前段",
			in:   "译名 xxx\n年代 2024\n[color=blue][b]【影片参数】[/b][/color]\nVideo: x",
			want: "译名 xxx\n年代 2024",
		},
		{
			name: "不含标记原样返回",
			in:   "普通简介无参数段",
			want: "普通简介无参数段",
		},
		{
			name: "回退：仅中文标题无bbcode",
			in:   "前面介绍\n【影片参数】\n后面 mediainfo",
			want: "前面介绍",
		},
		{
			name: "空串",
			in:   "",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := TrimDescriptionAtMovieParams(c.in)
			if got != c.want {
				t.Errorf("TrimDescriptionAtMovieParams(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
