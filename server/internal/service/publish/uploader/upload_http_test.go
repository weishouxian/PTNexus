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
