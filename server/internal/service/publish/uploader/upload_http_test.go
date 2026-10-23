package uploader

import (
	"net/http"
	"net/http/httptest"
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

// cloudflareChallengePage 简化自 UBits 实测返回：HTTP 403 + Cloudflare 挑战页。
const cloudflareChallengePage = `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title>
<meta http-equiv="Content-Type" content="text/html; charset=UTF-8">
<script src="https://challenges.cloudflare.com/turnstile/v0/api.js"></script></head>
<body><div id="cf-chl-widget"></div><noscript>Enable JavaScript and cookies to continue</noscript></body></html>`

func TestLooksLikeCloudflareChallenge(t *testing.T) {
	if !looksLikeCloudflareChallenge(cloudflareChallengePage) {
		t.Fatalf("Cloudflare 挑战页应被识别")
	}
	if looksLikeCloudflareChallenge(`<html><body><h1>上传失败！</h1><p>标题不能为空</p></body></html>`) {
		t.Fatalf("普通 NexusPHP 错误页不应被识别为 Cloudflare 挑战页")
	}
	if looksLikeCloudflareChallenge("") {
		t.Fatalf("空正文不应被识别为 Cloudflare 挑战页")
	}
}

func TestBuildUploadForbiddenError(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		notice  string
		wantSub []string
	}{
		{
			name:    "403 Cloudflare 挑战页",
			status:  403,
			body:    cloudflareChallengePage,
			wantSub: []string{"HTTP 403 上传失败", "Cloudflare", "Cookie 是否已失效", "发种权限"},
		},
		{
			name:    "403 普通错误页",
			status:  403,
			body:    `<html><body>Forbidden</body></html>`,
			wantSub: []string{"HTTP 403 上传失败", "站点拒绝了本次上传请求", "Cookie 是否已失效", "发种权限"},
		},
		{
			name:    "站点自带拒绝原因优先",
			status:  403,
			body:    `<html><body>403</body></html>`,
			notice:  "你没有发布种子的权限",
			wantSub: []string{"你没有发布种子的权限", "Cookie 是否已失效", "发种权限"},
		},
		{
			name:    "401 视为未登录",
			status:  401,
			body:    "",
			wantSub: []string{"HTTP 401 上传失败", "Cookie 是否已失效"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := buildUploadForbiddenError(tc.status, tc.body, tc.notice)
			if err == nil {
				t.Fatalf("应返回错误")
			}
			msg := err.Error()
			for _, sub := range tc.wantSub {
				if !strings.Contains(msg, sub) {
					t.Fatalf("错误提示缺少 %q，got: %q", sub, msg)
				}
			}
			if strings.Contains(msg, "<!DOCTYPE") || strings.Contains(msg, "challenges.cloudflare.com") {
				t.Fatalf("错误提示不应包含整页 HTML，got: %q", msg)
			}
		})
	}
}

func TestIsUploadForbiddenStatus(t *testing.T) {
	for _, code := range []int{401, 403} {
		if !isUploadForbiddenStatus(code) {
			t.Fatalf("状态码 %d 应判定为拒绝访问", code)
		}
	}
	for _, code := range []int{200, 302, 400, 404, 500, 503} {
		if isUploadForbiddenStatus(code) {
			t.Fatalf("状态码 %d 不应判定为拒绝访问", code)
		}
	}
}

func TestTryUploadTorrentForbiddenReturnsActionableHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/upload.php":
			// 上传页也一起被 Cloudflare 拦下，走 fetchUploadFormFields 的 403 分支。
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(cloudflareChallengePage))
		default:
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(cloudflareChallengePage))
		}
	}))
	defer server.Close()

	_, _, detail, err := TryUploadTorrent(
		server.URL+"/takeupload.php",
		server.URL,
		"uid=1; pass=abc",
		"file",
		[]byte("d4:infod4:name1:aee"),
		"test.torrent",
		map[string]string{"name": "测试种子"},
	)
	if err == nil {
		t.Fatalf("403 应返回错误")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Cookie 是否已失效") || !strings.Contains(msg, "发种权限") {
		t.Fatalf("403 提示应给出 Cookie/权限排查方向，got: %q", msg)
	}
	if strings.Contains(msg, "<!DOCTYPE") {
		t.Fatalf("403 提示不应倾倒整页 HTML，got: %q", msg)
	}
	// 上传页 403 的提示同样要能被看到（Cookie 失效时这里就是第一现场）。
	if !strings.Contains(detail, "上传页响应状态异常: 403") {
		t.Fatalf("详情日志应记录上传页 403，got: %q", detail)
	}
}
