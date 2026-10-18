package extract

import "testing"

// TestNormalizeSubtitleBracketTags 校验副标题里的【媒体标签】改写成「 /内容」。
func TestNormalizeSubtitleBracketTags(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "尾部媒体标签",
			in:   "丛林/逃出亚马逊(台)/丛林炼狱【内封简繁/双语字幕】",
			want: "丛林/逃出亚马逊(台)/丛林炼狱 /内封简繁/双语字幕",
		},
		{
			name: "标签前已有空格",
			in:   "袒露/我的初恋赤裸裸(港)/赤身裸爱(台)/裸露青春 【BD原盘 DIY简繁/双语字幕】",
			want: "袒露/我的初恋赤裸裸(港)/赤身裸爱(台)/裸露青春 /BD原盘 DIY简繁/双语字幕",
		},
		{
			name: "方括号内含前导空格",
			in:   "宇宙巨人：希曼崛起（真人版）【 UHD原盘 DIY 官译简繁+简英繁英字幕】",
			want: "宇宙巨人：希曼崛起（真人版） /UHD原盘 DIY 官译简繁+简英繁英字幕",
		},
		{
			name: "多个标签",
			in:   "万王之王 【内封简繁】 【双语字幕】",
			want: "万王之王 /内封简繁 /双语字幕",
		},
		{
			name: "对白类标签",
			in:   "恋恋风尘【内封闽南方言 繁体中字】",
			want: "恋恋风尘 /内封闽南方言 繁体中字",
		},
		{
			name: "作品名里的方括号不动",
			in:   "借出去的魔力要用【分期还款】强制收回 第1季 第3集 | 原名：貸した魔力は【リボ払い】で強制徴収 | 类型：动画",
			want: "借出去的魔力要用【分期还款】强制收回 第1季 第3集 | 原名：貸した魔力は【リボ払い】で強制徴収 | 类型：动画",
		},
		{
			name: "半角方括号媒体标签",
			in:   "花开伊吕波 第1季 全26集 [内封中字]",
			want: "花开伊吕波 第1季 全26集 /内封中字",
		},
		{
			name: "半角方括号多字段标签",
			in:   "小黄人与大怪兽 [简繁英字幕|双语字幕|粤语配音|杜比全景声|极高参压制]",
			want: "小黄人与大怪兽 /简繁英字幕|双语字幕|粤语配音|杜比全景声|极高参压制",
		},
		{
			name: "半角与全角混排",
			in:   "萨利机长 [汤姆 汉克斯2016力作 全国院线12月9日上映] 【英简繁+简繁双语字幕】",
			want: "萨利机长 [汤姆 汉克斯2016力作 全国院线12月9日上映] /英简繁+简繁双语字幕",
		},
		{
			name: "半角方括号里是演员或宣传语不动",
			in:   "抢红 [简繁中字] [张涵予 黎明 杜鹃 苍井空] [第一次看苍老师穿衣服的片子]",
			want: "抢红 /简繁中字 [张涵予 黎明 杜鹃 苍井空] [第一次看苍老师穿衣服的片子]",
		},
		{
			name: "方括号后紧跟正文时补空格",
			in:   "捍卫生死线(台)【内封国语音轨 简繁双语字幕】基努·李维斯为家人对抗世界",
			want: "捍卫生死线(台) /内封国语音轨 简繁双语字幕 基努·李维斯为家人对抗世界",
		},
		{
			name: "方括号后紧跟标点时不补空格",
			in:   "某片 [内封中字]。",
			want: "某片 /内封中字。",
		},
		{
			name: "无方括号原样返回",
			in:   "战车椅子 第1季 第3集 | 类型：动画",
			want: "战车椅子 第1季 第3集 | 类型：动画",
		},
	}
	for _, c := range cases {
		if got := normalizeSubtitleBracketTags(c.in); got != c.want {
			t.Fatalf("%s: 期望 %q，实际 %q", c.name, c.want, got)
		}
	}
}

// TestCleanSubtitleValueRewritesBracketTags 校验抓取侧副标题清洗链路里方括号改写生效。
func TestCleanSubtitleValueRewritesBracketTags(t *testing.T) {
	if got, want := cleanSubtitleValue("丛林炼狱【内封简繁/双语字幕】 | By ARDTU"), "丛林炼狱 /内封简繁/双语字幕"; got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	if got, want := cleanSubtitleValue("花开伊吕波 第1季 全26集 [内封中字] | By ARDTU"), "花开伊吕波 第1季 全26集 /内封中字"; got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	// 方括号内不是媒体标签时，尾部工具署名仍按原规则清理，但不改方括号。
	if got, want := cleanSubtitleValue("借出去的魔力要用【分期还款】强制收回 | By ARDTU"), "借出去的魔力要用【分期还款】强制收回"; got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
	if got, want := cleanSubtitleValue("抢红 [张涵予 黎明 杜鹃 苍井空]"), "抢红 [张涵予 黎明 杜鹃 苍井空]"; got != want {
		t.Fatalf("期望 %q，实际 %q", want, got)
	}
}
