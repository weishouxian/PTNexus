package sites

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLimitRousiImageCandidates 校验候选裁剪：封面始终保留，随后最多 3 张截图。
func TestLimitRousiImageCandidates(t *testing.T) {
	shots := []rousiImageSource{
		{URL: "https://img.example/1.png"},
		{URL: "https://img.example/2.png"},
		{URL: "https://img.example/3.png"},
		{URL: "https://img.example/4.png"},
		{URL: "https://img.example/5.png"},
	}

	withCover := append([]rousiImageSource{{URL: "https://img.example/poster.jpg"}}, shots...)
	got := limitRousiImageCandidates(withCover, 1)
	if len(got) != 1+rousiScreenshotCandidateLimit {
		t.Fatalf("有封面时应保留 1+%d 张，实际 %d 张", rousiScreenshotCandidateLimit, len(got))
	}
	if got[0].URL != "https://img.example/poster.jpg" {
		t.Fatalf("封面应排在第一，实际 %q", got[0].URL)
	}

	if got := limitRousiImageCandidates(shots, 0); len(got) != rousiScreenshotCandidateLimit {
		t.Fatalf("无封面时应只保留 %d 张，实际 %d 张", rousiScreenshotCandidateLimit, len(got))
	}

	if got := limitRousiImageCandidates(shots[:2], 0); len(got) != 2 {
		t.Fatalf("候选不足上限时不应改动，实际 %d 张", len(got))
	}

	if got := limitRousiImageCandidates(shots[:1], 5); len(got) != 1 {
		t.Fatalf("coverCount 越界时不应越界取切片，实际 %d 张", len(got))
	}
}

// TestRousiRefererCandidates 校验 Referer 优先级：包裹页 → 源站详情页（同域才用）→ 图片域名根 → 不发。
func TestRousiRefererCandidates(t *testing.T) {
	cases := []struct {
		name          string
		target        string
		wrapper       string
		sourceReferer string
		want          []string
	}{
		{
			name:          "外链图床：源站详情页不当 Referer",
			target:        "https://img3.pixhost.to/images/x.png",
			wrapper:       "https://pixhost.to/show/thread/1",
			sourceReferer: "https://hdsky.me/details.php?id=1",
			want: []string{
				"https://pixhost.to/show/thread/1",
				"https://img3.pixhost.to/",
				"",
			},
		},
		{
			name:          "源站附件：用源站详情页",
			target:        "https://hdsky.me/attachments/a.png",
			sourceReferer: "https://hdsky.me/details.php?id=9",
			want: []string{
				"https://hdsky.me/details.php?id=9",
				"https://hdsky.me/",
				"",
			},
		},
		{
			name:          "源站子域图片同样认源站详情页",
			target:        "https://img.hdsky.me/a.png",
			sourceReferer: "https://hdsky.me/details.php?id=9",
			want: []string{
				"https://hdsky.me/details.php?id=9",
				"https://img.hdsky.me/",
				"",
			},
		},
		{
			name:   "没有包裹页也没有源站：只剩域名根与不发 Referer",
			target: "https://img.seedvault.cn/a.png",
			want:   []string{"https://img.seedvault.cn/", ""},
		},
		{
			name:    "包裹页与域名根相同则去重",
			target:  "https://img.seedvault.cn/a.png",
			wrapper: "https://img.seedvault.cn/",
			want:    []string{"https://img.seedvault.cn/", ""},
		},
	}

	for _, tc := range cases {
		got := rousiRefererCandidates(tc.target, tc.wrapper, tc.sourceReferer)
		if len(got) != len(tc.want) {
			t.Fatalf("%s: 期望 %v，实际 %v", tc.name, tc.want, got)
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Fatalf("%s: 期望 %v，实际 %v", tc.name, tc.want, got)
			}
		}
	}
}

// TestDownloadRousiImageRetriesWithNextReferer 校验 403（来源被拒）时换下一个 Referer 重试并成功。
func TestDownloadRousiImageRetriesWithNextReferer(t *testing.T) {
	const wrapper = "https://wrapper.example/show/1"
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if strings.TrimSpace(r.Header.Get("Referer")) == wrapper {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte("PNGDATA"))
	}))
	defer server.Close()

	data, err := downloadRousiImageWithReferers(context.Background(), server.URL, "", "", server.URL+"/a.png", wrapper, "")
	if err != nil {
		t.Fatalf("应换 Referer 重试并成功，实际报错 %v", err)
	}
	if string(data) != "PNGDATA" {
		t.Fatalf("图片内容不对: %q", string(data))
	}
	if hits != 2 {
		t.Fatalf("应只重试一次（403 后再取一次），实际请求 %d 次", hits)
	}
}

// TestDownloadRousiImageNoRetryOnNotFound 校验 404 不换 Referer（换也没用），只请求一次。
func TestDownloadRousiImageNoRetryOnNotFound(t *testing.T) {
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	if _, err := downloadRousiImageWithReferers(context.Background(), server.URL, "", "", server.URL+"/missing.png", "", ""); err == nil {
		t.Fatal("404 应返回错误")
	}
	if hits != 1 {
		t.Fatalf("404 不应重试其它 Referer，实际请求 %d 次", hits)
	}
}

// TestRousiSourceRefererForTorrent 校验由文件名反推源站详情页：解析不出站点时返回空串。
func TestRousiSourceRefererForTorrent(t *testing.T) {
	if got := rousiSourceRefererForTorrent("nosite-123-2026-11-19-00:00:00.torrent"); got != "" {
		t.Fatalf("未知站点不应拼出源站地址，实际 %q", got)
	}
	if got := rousiSourceRefererForTorrent("随便什么名字.torrent"); got != "" {
		t.Fatalf("文件名不含站点与 id 时应返回空串，实际 %q", got)
	}
}
