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

// 源站只给纯文本声明时（TTG 常见「XX官组作品，感谢原制作者发布。」），统一补 [quote] 包裹；
// 已包裹的必须原样返回，保证抓取归一与发种组装两侧重复调用安全（幂等）。
func TestEnsureStatementQuote(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "纯文本声明补包裹",
			in:   "Audiences官组作品，感谢原制作者发布。",
			want: "[quote]Audiences官组作品，感谢原制作者发布。[/quote]",
		},
		{
			name: "带 BBCode 但无 quote 也要包",
			in:   "[b][color=blue]本资源禁转[/color][/b]",
			want: "[quote][b][color=blue]本资源禁转[/color][/b][/quote]",
		},
		{
			name: "已包裹原样返回",
			in:   "[quote][b][color=blue][size=5]ADWeb官组作品，感谢原制作者发布。[/size][/color][/b][/quote]",
			want: "[quote][b][color=blue][size=5]ADWeb官组作品，感谢原制作者发布。[/size][/color][/b][/quote]",
		},
		{
			name: "多个 quote 块并列不重复包裹",
			in:   "[quote]A[/quote]\n\n[quote]B[/quote]",
			want: "[quote]A[/quote]\n\n[quote]B[/quote]",
		},
		{
			name: "带作者的 quote 参数也算已包裹",
			in:   "[quote=某发布组]本资源禁转[/quote]",
			want: "[quote=某发布组]本资源禁转[/quote]",
		},
		{
			name: "大小写不敏感",
			in:   "[QUOTE]本资源禁转[/QUOTE]",
			want: "[QUOTE]本资源禁转[/QUOTE]",
		},
		{
			name: "首尾空白清除后再判定",
			in:   "  \n [quote]A[/quote] \n ",
			want: "[quote]A[/quote]",
		},
		{
			name: "空声明保持空",
			in:   "   ",
			want: "",
		},
	}
	for _, tc := range cases {
		if got := EnsureStatementQuote(tc.in); got != tc.want {
			t.Fatalf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

// release info 段「Quote:★★★★★ General Information ★★★★★」及其后的 RELEASE.NAME/VIDEO.CODEC/UPLOADER
// 都是发布参数冗余，从标记起整段删掉；正文里没有星号装饰的普通提及不能误伤。
func TestTrimDescriptionGeneralInformation(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "TTG 纯文本形态（真实样本）",
			in: "◎片　　名　仙逆剧场版 弑仙之战\n◎简　　介\n王林于雷之仙界遭遇宿敌绝命追杀，在生死绝境中展开一场惊天逆袭，以弱胜强，终成弑仙壮举。\n" +
				"Quote:★★★★★ General Information ★★★★★\nRELEASE.NAME...: 仙逆剧场版.弑仙之战.2026.2160p.TX.WEB-DL.H265.DDP2.0-ADWeb\n" +
				"VIDEO.CODEC....: Tencent-V265-1.6.9.6. Main@L5@Main @ 7 838 kb/s\nUPLOADER.......: ADWeb",
			want: "◎片　　名　仙逆剧场版 弑仙之战\n◎简　　介\n王林于雷之仙界遭遇宿敌绝命追杀，在生死绝境中展开一场惊天逆袭，以弱胜强，终成弑仙壮举。",
		},
		{
			name: "BBCode 包裹：从开启标签起截断，不留悬空标签",
			in:   "前面介绍\n[b]Quote:[/b][b]★★★★★ General Information ★★★★★[/b]\nRELEASE.NAME...: x",
			want: "前面介绍",
		},
		{
			name: "quote 块包裹",
			in:   "前面介绍\n[quote][size=3][b]★★★★★ General Information ★★★★★[/b][/size][/quote]\nRELEASE.SIZE...: 5.21 GiB",
			want: "前面介绍",
		},
		{
			name: "星号紧贴文字无空格的变体",
			in:   "介绍文字\nQuote:★★★★★General Information★★★★★\nRELEASE.TIME...: x",
			want: "介绍文字",
		},
		{
			name: "正文普通提及不截断",
			in:   "介绍文字里提到 General Information 这个词，但没有星号装饰。\n后面还有正文。",
			want: "介绍文字里提到 General Information 这个词，但没有星号装饰。\n后面还有正文。",
		},
	}
	for _, tc := range cases {
		if got := TrimDescription(tc.in); got != tc.want {
			t.Fatalf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}

// 正文里只要出现「Quote:」，该标记及其后全部内容都要删掉（后面通常是 RELEASE.NAME 等发布参数）。
// 口径要点：只认带冒号的 quote:，BBCode 引用块 [quote] 与英文单词 Quoted 都不能误伤。
func TestTrimDescriptionQuoteSection(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "TTG 纯文本 Quote: 起头（真实形态）",
			in: "◎片　　名　仙逆剧场版 弑仙之战\n◎简　　介\n王林于雷之仙界遭遇宿敌绝命追杀，终成弑仙壮举。\n" +
				"Quote: 以下为发布组参数，与影片无关\nRELEASE.NAME...: x\nVIDEO.CODEC....: y\nUPLOADER.......: ADWeb",
			want: "◎片　　名　仙逆剧场版 弑仙之战\n◎简　　介\n王林于雷之仙界遭遇宿敌绝命追杀，终成弑仙壮举。",
		},
		{
			name: "BBCode 包裹：从行首开启标签起截断，不留悬空标签",
			in:   "前面介绍\n[b]Quote:[/b]\nRELEASE.NAME...: x",
			want: "前面介绍",
		},
		{
			name: "全角冒号与空格变体",
			in:   "前面介绍\nQuote ：后面的内容\nRELEASE.SIZE...: 5.21 GiB",
			want: "前面介绍",
		},
		{
			name: "小写 quote: 同样截断",
			in:   "前面介绍\nquote: xxx",
			want: "前面介绍",
		},
		{
			name: "BBCode 引用块 [quote] 不是标记",
			in:   "前面介绍\n[quote]这是引用的一段影评，没有冒号[/quote]\n后面还有正文。",
			want: "前面介绍\n[quote]这是引用的一段影评，没有冒号[/quote]\n后面还有正文。",
		},
		{
			name: "带作者的引用块 [quote=某人] 不是标记",
			in:   "前面介绍\n[quote=某组]说明文字[/quote]\n后面还有正文。",
			want: "前面介绍\n[quote=某组]说明文字[/quote]\n后面还有正文。",
		},
		{
			name: "英文单词 Quoted 不截断",
			in:   "前面介绍\nQuoted from somewhere: 这不是标记\n后面还有正文。",
			want: "前面介绍\nQuoted from somewhere: 这不是标记\n后面还有正文。",
		},
	}
	for _, tc := range cases {
		if got := TrimDescription(tc.in); got != tc.want {
			t.Fatalf("%s:/n want %q\n got  %q", tc.name, tc.want, got)
		}
	}
}
