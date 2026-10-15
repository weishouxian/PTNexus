package uploader

import (
	"strings"
	"testing"
)

func TestExtractUploadNoticeFromAudiencesErrorPage(t *testing.T) {
	// 简化自 Audiences 实际返回：广告位 <p> 出现在 <h1> 之前，提示正文在其后。
	page := `<!DOCTYPE html>
<html><head><title>Audiences :: 上传失败！ - Powered by NexusPHP</title></head>
<body>
<div id="ad_belownav"><p><span>站点广告内容</span></p></div>
<h1>上传失败！</h1>
<p>非完结剧集需在主标题内标明集数 SxxExx ，已完结剧集需勾选完结标签，请重新检查并修正。</p>
</body></html>`

	want := "上传失败！非完结剧集需在主标题内标明集数 SxxExx ，已完结剧集需勾选完结标签，请重新检查并修正。"
	if got := extractUploadNotice(page); got != want {
		t.Fatalf("extractUploadNotice 结果不符\nwant: %q\ngot:  %q", want, got)
	}
}

func TestExtractUploadNoticeFallbacks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "成功页不提取",
			in:   `<html><body><h1>上传成功</h1><p>感谢分享</p></body></html>`,
			want: "",
		},
		{
			name: "无 h1 返回空",
			in:   `<html><body><p>普通页面</p></body></html>`,
			want: "",
		},
		{
			name: "纯文本返回空",
			in:   `{"result":"ok"}`,
			want: "",
		},
		{
			name: "空串返回空",
			in:   "",
			want: "",
		},
		{
			name: "无正文段落仅返回标题",
			in:   `<html><body><h1>错误</h1></body></html>`,
			want: "错误",
		},
		{
			name: "标签与实体清洗",
			in:   `<html><body><h1>上传失败！</h1><p><b>标题</b>不能为空 &amp; 需含集数</p></body></html>`,
			want: "上传失败！标题 不能为空 & 需含集数",
		},
		{
			// 我堡（OurBits）实测返回：h2 标题 + id='stdmsg' 提示块，无错误关键词标题、无 <p> 正文。
			name: "stdmsg 资质不足提示块",
			in: `<table class="main" width="500"><tr><td class="embedded">
<h2>资质不足</h2>
<table width="100%" border="1" cellpadding="10"><tr><td class="text" id='stdmsg'>只有已经成功通过审核10个种子且当前待修改种子不超过2个（当前通过数：614）的用户才有权限直接发布,请提交<a href="/offers.php">候选</a></td></tr></table>
</td></tr></table>`,
			want: "资质不足：只有已经成功通过审核10个种子且当前待修改种子不超过2个（当前通过数：614）的用户才有权限直接发布,请提交 候选",
		},
		{
			name: "stdmsg 中性文案不提取",
			in:   `<html><body><td id='stdmsg'>感谢分享，请耐心等待审核</td></body></html>`,
			want: "",
		},
		{
			name: "详情页 stdmsg 不提取",
			in:   `<html><body><td id='stdmsg'>该种子已被锁定，无权限下载</td><a href="details.php?id=999">详情</a></body></html>`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractUploadNotice(tc.in); got != tc.want {
				t.Fatalf("extractUploadNotice 结果不符\nwant: %q\ngot:  %q", tc.want, got)
			}
		})
	}
}

func TestSummarizeResponseBodyPrefersSiteNotice(t *testing.T) {
	page := `<html><head><title>Audiences :: 上传失败！ - Powered by NexusPHP</title></head>` +
		`<body><style>body{color:red}</style><h1>上传失败！</h1><p>标题缺少集数</p></body></html>`

	want := "站点错误提示: 上传失败！标题缺少集数"
	if got := summarizeResponseBody(page); got != want {
		t.Fatalf("summarizeResponseBody 结果不符\nwant: %q\ngot:  %q", want, got)
	}
}

func TestSummarizeResponseBodyFallsBackForNonErrorPage(t *testing.T) {
	page := `<html><head><title>Audiences :: 发布 - Powered by NexusPHP</title></head>` +
		`<body><h1>上传成功</h1><p>感谢分享</p></body></html>`

	got := summarizeResponseBody(page)
	if !strings.HasPrefix(got, "返回 HTML 页面 (title=") {
		t.Fatalf("非错误页应回退到原始摘要，got: %q", got)
	}
}

func TestSummarizeResponseBodyExtractsStdMsgDenial(t *testing.T) {
	// 我堡（OurBits）实测整页：HTTP 200 + 无详情页链接，正文是 <h2>资质不足</h2> + <td id='stdmsg'>…</td>。
	// 页头的 userdetails.php?id=61948 必须不被误判成「发布成功详情页」，否则提示会被整页 HTML 淹没。
	page := `<!DOCTYPE html>
<html lang="zh">
<head><title>OurBits - Powered by NexusPHP</title></head>
<body>
<table class="mainouter" width="982"><tr><td id="nav_block" class="nav_bg text">
<div id="nav"><ul id="mainmenu" class="menu"><li><a href="upload.php">发 布</a></li></ul></div>
<input type="hidden" name="userId" id="myUserId" value="61948">
<a href="userdetails.php?id=61948" class='VeteranUser_Name'><b>fenglaile</b></a>
</td></tr>
<tr><td id="outer" align="center" class="outer">
<p><table border="0" cellpadding="10"><tr><td style='background: red'><b>你有1条新短讯！</b></td></tr></table></p><br />
<table align="center" class="main" width="500" border="0" cellpadding="0" cellspacing="0"><tr><td class="embedded">
<h2>资质不足</h2>
<table width="100%" border="1" cellspacing="0" cellpadding="10"><tr><td class="text" id='stdmsg'>只有已经成功通过审核10个种子且当前待修改种子不超过2个（当前通过数：614）的用户才有权限直接发布,请提交<a href="/offers.php">候选</a></td></tr></table></td></tr></table>
</td></tr></table>
<div id="footer">(c) <a href="/">OurBits</a> Powered by <a href="aboutnexus.php">NexusPHP</a></div>
</body></html>`

	want := "站点错误提示: 资质不足：只有已经成功通过审核10个种子且当前待修改种子不超过2个（当前通过数：614）的用户才有权限直接发布,请提交 候选"
	if got := summarizeResponseBody(page); got != want {
		t.Fatalf("summarizeResponseBody 结果不符\nwant: %q\ngot:  %q", want, got)
	}
}

func TestStdMsgNoticeSkipsPublishedDetailPage(t *testing.T) {
	// 发布成功后跳详情页：页面里同样有 id='stdmsg'，不得当成失败原因。
	page := `<html><body><table><tr><td id='stdmsg'>该种子已发布，感谢分享</td></tr></table>` +
		`<a href="details.php?id=12345">详情</a></body></html>`

	if got := extractUploadNotice(page); got != "" {
		t.Fatalf("详情页 stdmsg 不应提取为错误提示, got: %q", got)
	}
}
