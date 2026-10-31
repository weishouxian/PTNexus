package colaptapi

import "testing"

func TestIsSite(t *testing.T) {
	cases := []struct {
		name      string
		siteCode  string
		baseURL   string
		detailURL string
		want      bool
	}{
		{name: "站点代码", siteCode: "colapt", want: true},
		{name: "简称 cola", siteCode: "cola", want: true},
		{name: "中文昵称", siteCode: "可乐", want: true},
		{name: "根地址", baseURL: "https://www.colapt.com", want: true},
		{name: "详情页地址", detailURL: "https://www.colapt.com/torrents/6211", want: true},
		{name: "其它站", siteCode: "ssd", baseURL: "https://springsunday.net", detailURL: "https://springsunday.net/details.php?id=1", want: false},
		{name: "空值", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSite(tc.siteCode, tc.baseURL, tc.detailURL); got != tc.want {
				t.Fatalf("IsSite(%q,%q,%q)=%v，期望 %v", tc.siteCode, tc.baseURL, tc.detailURL, got, tc.want)
			}
		})
	}
}

func TestExtractTorrentID(t *testing.T) {
	cases := map[string]string{
		"6211":                                 "6211",
		" 6211 ":                               "6211",
		"https://www.colapt.com/torrents/6211": "6211",
		"https://www.colapt.com/torrents/6211#comments": "6211",
		"/api/v1/torrents/6211":                         "6211",
		"/api/v1/automation/torrents/6211":              "6211",
		"https://www.colapt.com/torrents/abc":           "",
		"":                                              "",
	}
	for input, want := range cases {
		if got := ExtractTorrentID(input); got != want {
			t.Fatalf("ExtractTorrentID(%q)=%q，期望 %q", input, got, want)
		}
	}
}

func TestBaseURLHelpers(t *testing.T) {
	if got := NormalizeBaseURL(""); got != DefaultBaseURL {
		t.Fatalf("空地址应回退官方站，得到 %q", got)
	}
	if got := NormalizeBaseURL("https://www.colapt.com/"); got != "https://www.colapt.com" {
		t.Fatalf("尾部斜杠未去掉：%q", got)
	}
	if got := NormalizeBaseURL("https://www.colapt.com/api/v1"); got != "https://www.colapt.com" {
		t.Fatalf("/api/v1 后缀未去掉：%q", got)
	}
	if got := APIBase("www.colapt.com"); got != "https://www.colapt.com/api/v1" {
		t.Fatalf("APIBase=%q", got)
	}
	if got := APIBase("https://www.colapt.com/api/v1/"); got != "https://www.colapt.com/api/v1" {
		t.Fatalf("APIBase 重复前缀：%q", got)
	}
	if got := DetailURL("https://www.colapt.com", "6211"); got != "https://www.colapt.com/torrents/6211" {
		t.Fatalf("DetailURL=%q", got)
	}
	direct := "https://www.colapt.com/torrents/6211"
	if got := DetailURL("https://www.colapt.com", direct); got != direct {
		t.Fatalf("已是地址时应原样返回，得到 %q", got)
	}
}
