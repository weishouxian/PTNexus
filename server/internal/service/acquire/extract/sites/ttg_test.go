package sites

import (
	"regexp"
	"strings"
	"testing"
)

// ttgRuntimeStub 构造 TTG 测试用的运行期依赖：只实现本用例用到的纯函数。
func ttgRuntimeStub() Runtime {
	stripTags := func(input string, keepLineBreak bool) string {
		text := input
		if keepLineBreak {
			text = regexp.MustCompile(`(?i)<br\s*/?>`).ReplaceAllString(text, "\n")
		}
		text = regexp.MustCompile(`(?is)<[^>]+>`).ReplaceAllString(text, "")
		text = strings.ReplaceAll(text, "&nbsp;", " ")
		lines := make([]string, 0, 16)
		for _, line := range strings.Split(text, "\n") {
			if trimmed := strings.TrimSpace(line); trimmed != "" {
				lines = append(lines, trimmed)
			}
		}
		return strings.Join(lines, "\n")
	}
	return Runtime{
		SanitizeHTMLText: stripTags,
		ExtractElementInnerHTMLByID: func(pageHTML, tagName, elementID string) string {
			match := regexp.MustCompile(`(?is)<` + tagName + `[^>]*id=["']` + elementID + `["'][^>]*>(.*)</` + tagName + `>`).FindStringSubmatch(pageHTML)
			if len(match) < 2 {
				return ""
			}
			return match[1]
		},
		InferStandardizedValues: func(title, mediainfo, body string) map[string]string {
			return map[string]string{
				"type":        "category.movie",
				"medium":      "medium.bluray",
				"video_codec": "video.x265",
				"audio_codec": "audio.lpcm",
				"resolution":  "resolution.r1080p",
				"team":        "team.wiki",
				"source":      "source.hongkong",
			}
		},
		ExtractBasicInfoType: func(pageHTML, siteCode string) string {
			if siteCode != "ttg" {
				return ""
			}
			return "category.movie"
		},
		IsLikelyMediaInfoText: func(text string) bool {
			lower := strings.ToLower(text)
			return strings.Contains(lower, "unique id") || strings.Contains(lower, "complete name")
		},
		ReIMDbLink:   regexp.MustCompile(`https?://(?:www\.)?imdb\.com/title/(tt\d+)`),
		ReDoubanLink: regexp.MustCompile(`https?://movie\.douban\.com/subject/(\d+)`),
		ReTMDbLink:   regexp.MustCompile(`https?://(?:www\.)?themoviedb\.org/movie/(\d+)`),
		NormalizeExternalLink: func(rawURL string, pattern *regexp.Regexp) string {
			return strings.TrimSpace(rawURL)
		},
	}
}

const ttgDetailFixture = `<!DOCTYPE html>
<html><head><title>TTG :: 种子 &quot;The Shadow Boxing 1979 1080p BluRay x265 10bit LPCM-WiKi&quot; 的详情</title></head>
<body>
<h1>The Shadow Boxing 1979 1080p BluRay x265 10bit LPCM-WiKi [茅山僵尸拳 / 茅山殭屍拳]  *国粤双语 内封简繁中字</h1>
<table width="750" border="1">
<tr><td class=rowhead nowrap>下载种子文件</td><td><a class="index" href="/dl/841600/9945">[TTG] The.Shadow.Boxing.1979.1080p.BluRay.x265.10bit.LPCM-WiKi.torrent</a></td></tr>
<tr><td class="heading" valign="top" align="right" nowrap>种子链接</td><td><a href='https://totheglory.im/dl/841600/4e2cca4b983ba8039a0440a5eb6261ee'>点击复制到剪切板</a></td></tr>
<tr><td class="heading" valign="top" align="right" nowrap><span id='t_d'>简述</span></td><td valign="top" align=left><div id='kt_d'><br />
<p class=sub><b>Quote:</b></p><table class=main border=1 cellspacing=0 cellpadding=10><tr><td style='border: 1px black dotted'><font color="blue"><strong>本资源素材来自DIY原盘，谢绝转载，特此感谢！</strong></font></td></tr></table><br />
<img class="topic" border=0 src="https://doubaninfo.com/dbposter/doubaninfo_com_1481829.jpg" /><br />
<strong>.Comparisons</strong><br />
<a href="https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_Source.png"><img class="topic" border=0 src="https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_Source_thumb.png" /></a>
<a href="https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_WiKi_x265.png"><img class="topic" border=0 src="https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_WiKi_x265_thumb.png" /></a><br />
<p class=sub><b>Quote:</b></p><table class=main border=1 cellspacing=0 cellpadding=10><tr><td style='border: 1px black dotted'><u><strong>.Plot</strong></u><br /><br />Acclaimed director Liu Chia-liang directs his brothers in this heralded sequel.<br />
<font face="courier new">TAGLiNE...................:</font> The Shadow Boxing (1979)<br />
<font face="courier new">GENRE.....................:</font> Comedy / Action / Fantasy<br />
<font face="courier new">ViDEO CODEC...............:</font> x265_Main 10@L4@Main @ 10318 Kbps<br />
<font face="courier new">AUDiO CODEC 1.............:</font> Mandarin LPCM 2.0 @ 1536 Kbps<br />
<font face="courier new">SUBTiTLES.................:</font> 繁体中文 简体中文<br /></td></tr></table><br />
◎译　　名　茅山僵尸拳 / 茅山殭屍拳 / 神打小子<br />
◎片　　名　The Shadow Boxing<br />
◎年　　代　1979<br />
◎产　　地　中国香港<br />
◎类　　别　喜剧&nbsp;&nbsp;/&nbsp;&nbsp;动作&nbsp;&nbsp;/&nbsp;&nbsp;奇幻<br />
◎IMDb链接&nbsp;&nbsp;<a href="https://www.imdb.com/title/tt0079527/">https://www.imdb.com/title/tt0079527/</a><br />
◎豆瓣链接　<a href="https://movie.douban.com/subject/1481829/">https://movie.douban.com/subject/1481829/</a><br />
◎简　　介<br /><br />　　以趕屍為業的陳五與徒弟范振元在一次行動中走失兩具殭屍……<br /><br />
<img border=0 src="/pic/ico_30.gif" /> <font color="red">本种子的下载流量计为实际流量的30%。</font></div></td></tr>
<tr><td class="heading" valign="top" align="right" nowrap>类型</td><td valign="top" align=left>电影1080i/p</td></tr>
<tr><td class="heading" valign="top" align="right" nowrap>尺寸</td><td valign="top" align=left>10.01 GB (10,751,777,839 字节)</td></tr>
<tr><td class="heading" valign="top" align="right" nowrap>IMDb</td><td valign="top" align=left><a href="https://www.imdb.com/title/tt0079527">6.3</a></td></tr>
</table>
</body></html>`

// TTG 详情页结构不是 NexusPHP 模板：标题是裸 <h1>，简介在 div#kt_d，
// 缩略图要还原成原图，声明与正文要分开，release info 不能被当成 MediaInfo。
func TestExtractTTG(t *testing.T) {
	data, err := ExtractTTG(Input{SiteCode: "ttg", PageHTML: ttgDetailFixture, FallbackTitle: "fallback"}, ttgRuntimeStub())
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}

	wantTitle := "The Shadow Boxing 1979 1080p BluRay x265 10bit LPCM-WiKi [茅山僵尸拳 / 茅山殭屍拳]  *国粤双语 内封简繁中字"
	if data.Title != wantTitle {
		t.Fatalf("标题提取异常：want %q, got %q", wantTitle, data.Title)
	}
	if !strings.Contains(data.Intro.Statement, "DIY原盘") {
		t.Fatalf("声明提取异常: %q", data.Intro.Statement)
	}
	if strings.Contains(data.Intro.Statement, "Plot") {
		t.Fatalf("声明误吞了剧情块: %q", data.Intro.Statement)
	}
	if !strings.Contains(data.Intro.Body, "◎片　　名　The Shadow Boxing") {
		t.Fatalf("正文提取异常: %q", data.Intro.Body)
	}
	if strings.Contains(data.Intro.Body, "TAGLiNE") {
		t.Fatalf("正文里残留了 release info: %q", data.Intro.Body)
	}
	if strings.Contains(data.Intro.Body, "下载流量计为") {
		t.Fatalf("正文里残留了促销尾注: %q", data.Intro.Body)
	}
	if data.Intro.Poster != "[img]https://doubaninfo.com/dbposter/doubaninfo_com_1481829.jpg[/img]" {
		t.Fatalf("海报提取异常: %q", data.Intro.Poster)
	}
	if strings.Contains(data.Intro.Screenshots, "_thumb") {
		t.Fatalf("截图没有还原成原图: %q", data.Intro.Screenshots)
	}
	for _, want := range []string{
		"[img]https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_Source.png[/img]",
		"[img]https://tu.totheglory.im/picture/161/API/Comp_1794917733_01_WiKi_x265.png[/img]",
	} {
		if !strings.Contains(data.Intro.Screenshots, want) {
			t.Fatalf("截图缺少 %q，实际: %q", want, data.Intro.Screenshots)
		}
	}
	if data.MediaInfo != "" {
		t.Fatalf("release info 被误当成 MediaInfo: %q", data.MediaInfo)
	}
	if data.Type != "category.movie" {
		t.Fatalf("类型异常: %q", data.Type)
	}
	if data.Medium != "medium.bluray" || data.Resolution != "resolution.r1080p" {
		t.Fatalf("标准化字段异常: medium=%q resolution=%q", data.Medium, data.Resolution)
	}
	if !strings.Contains(data.IMDbLink, "tt0079527") {
		t.Fatalf("IMDb 链接异常: %q", data.IMDbLink)
	}
	if !strings.Contains(data.DoubanLink, "1481829") {
		t.Fatalf("豆瓣链接异常: %q", data.DoubanLink)
	}
}

// 含真正 MediaInfo 的 TTG 详情页：带 Unique ID/Complete name 的块要能取到。
func TestExtractTTGWithMediaInfo(t *testing.T) {
	fixture := `<h1>Some Movie 2024 1080p BluRay x264-GRP</h1>
<div id='kt_d'>
◎片　　名　Some Movie<br />
MediaInfo<br />
General<br />
Unique ID : 123456789 (0x1ABCDEF)<br />
Complete name : Some.Movie.mkv<br />
Format : Matroska<br />
Video<br />
Format : AVC<br />
</div>`

	data, err := ExtractTTG(Input{SiteCode: "ttg", PageHTML: fixture}, ttgRuntimeStub())
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	if !strings.Contains(data.MediaInfo, "Unique ID") {
		t.Fatalf("MediaInfo 提取异常: %q", data.MediaInfo)
	}
}
