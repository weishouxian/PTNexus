package extract

import (
	"strings"
	"testing"
)

// luckptPageWithTemplateCover 构造 luckpt 类详情页：简介容器首位有站点模板注入的封面块，
// 紧随其后的 fieldset 引用（转 [quote]）才是源站声明，正文里再插同一张海报。
const luckptPageWithTemplateCover = `<html><head><title>LuckPT :: 种子详情 "Some.Movie.2024.1080p.BluRay.Remux.AVC.DTS-HD.MA.5.1-GRP" - Powered by NexusPHP</title></head><body>
<table><tr><td class="rowhead">简介</td><td class="rowfollow" valign="top" align="left">
<div id='kdescr'><div class="torrent-detail-poster"><img class="torrent-detail-poster__image" src="https://img.example.com/cover.jpg" alt="" loading="lazy" /></div><fieldset><legend> 引用 </legend><br /><br />
原盘来自某站：Some.Movie.2024.1080p.BluRay.Remux.AVC.DTS-HD.MA.5.1-GRP (#12345)<br />
字幕来自某字幕组<br />
</fieldset><br /><br />
<img style="max-width: 100%" src="https://img.example.com/cover.jpg" /><br />
◎译　　名　示例影片<br />
◎年　　代　2024<br />
◎产　　地　美国<br />
◎类　　别　剧情<br />
</div></td></tr></table>
</body></html>`

// TestTemplateCoverDoesNotSwallowStatement 校验站点模板封面块不会把源站引用声明挤进正文。
func TestTemplateCoverDoesNotSwallowStatement(t *testing.T) {
	res := extractReviewDataFromHTMLWithSite(luckptPageWithTemplateCover, "fallback", "luckpt")
	if !strings.Contains(res.Statement, "原盘来自某站") || !strings.Contains(res.Statement, "字幕来自某字幕组") {
		t.Fatalf("声明未取到，statement=%q", res.Statement)
	}
	if !strings.HasPrefix(res.Statement, "[quote]") {
		t.Fatalf("声明应保留 quote 包裹，statement=%q", res.Statement)
	}
	if !strings.Contains(res.Poster, "cover.jpg") {
		t.Fatalf("海报应由正文那张图提供，poster=%q", res.Poster)
	}
	if strings.Contains(res.Body, "原盘来自某站") || strings.Contains(res.Body, "字幕来自某字幕组") {
		t.Fatalf("声明不应残留在正文，body=%q", res.Body)
	}
	if !strings.Contains(res.Body, "◎译　　名　示例影片") {
		t.Fatalf("正文丢失影片信息，body=%q", res.Body)
	}
}

// TestTemplateCoverFallsBackToPoster 校验正文没有海报时用被剥的封面图兜底，海报字段不丢。
func TestTemplateCoverFallsBackToPoster(t *testing.T) {
	page := `<html><head><title>LuckPT :: 种子详情 "Cover.Only.2024.1080p.BluRay.Remux.AVC.DTS-HD.MA.5.1-GRP" - Powered by NexusPHP</title></head><body>
<div id='kdescr'><div class="torrent-detail-poster"><img src="https://img.example.com/only-cover.jpg" /></div>
◎译　　名　仅封面影片<br />
◎年　　代　2024<br />
</div></body></html>`
	res := extractReviewDataFromHTMLWithSite(page, "fallback", "luckpt")
	if !strings.Contains(res.Poster, "only-cover.jpg") {
		t.Fatalf("海报应由封面图兜底，poster=%q", res.Poster)
	}
	if !strings.Contains(res.Body, "◎译　　名　仅封面影片") {
		t.Fatalf("正文丢失，body=%q", res.Body)
	}
}

// TestStripTemplateCoverSkipsBlockWithVisibleText 校验封面块内含可见文字时不剥离，避免误删正文。
func TestStripTemplateCoverSkipsBlockWithVisibleText(t *testing.T) {
	html := `<div class="torrent-detail-poster"><img src="https://img.example.com/cover.jpg" />这里是正文不是封面</div>正文`
	stripped, coverURL := stripTemplateCoverFromDescr(html)
	if stripped != html || coverURL != "" {
		t.Fatalf("含文字的块不应被剥离，stripped=%q cover=%q", stripped, coverURL)
	}
}

// TestStripTemplateCoverRequiresLeadingBlock 校验只有位于简介容器首位的封面块才处理。
func TestStripTemplateCoverRequiresLeadingBlock(t *testing.T) {
	html := `◎译　　名　示例影片<br /><div class="torrent-detail-poster"><img src="https://img.example.com/cover.jpg" /></div>`
	stripped, coverURL := stripTemplateCoverFromDescr(html)
	if stripped != html || coverURL != "" {
		t.Fatalf("非首位封面块不应被剥离，stripped=%q cover=%q", stripped, coverURL)
	}
}

// TestStripTemplateCoverReturnsURL 校验剥离成功时返回封面地址（供正文缺海报时兜底）。
func TestStripTemplateCoverReturnsURL(t *testing.T) {
	html := `<div class="torrent-detail-poster"><img src="https://img.example.com/cover.jpg?a=1&amp;b=2" /></div>剩余正文`
	stripped, coverURL := stripTemplateCoverFromDescr(html)
	if stripped != "剩余正文" {
		t.Fatalf("剥离结果异常，stripped=%q", stripped)
	}
	if coverURL != "https://img.example.com/cover.jpg?a=1&b=2" {
		t.Fatalf("封面地址异常，cover=%q", coverURL)
	}
}
