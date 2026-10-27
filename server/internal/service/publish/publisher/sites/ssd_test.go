package sites

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// TestPublishSSDFields 端到端验证 SSD（不可说）发种字段装配：
// 直接正式发种（不再提交候选标记 offer）、六维度映射、独立 checkbox 标签、
// 海报/截图/媒体信息字段分离、简介只放声明。
func TestPublishSSDFields(t *testing.T) {
	var captured map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload.php"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			// 直发权限下的上传页：普通发种表单，不含 offer 隐藏字段。
			fmt.Fprint(w, `<html><head><title>SSD :: 发布 - Powered by NexusPHP</title></head><body>
<form action="takeupload.php" method="post" enctype="multipart/form-data">
<input type="file" name="file"/>
<input type="text" name="name"/>
</form></body></html>`)
		case strings.HasSuffix(r.URL.Path, "/takeupload.php"):
			_ = r.ParseMultipartForm(32 << 20)
			captured = map[string]string{}
			for k := range r.MultipartForm.Value {
				captured[k] = r.MultipartForm.Value[k][0]
			}
			http.Redirect(w, r, "/details.php?id=99999", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := t.TempDir() + "/x.torrent"
	if err := os.WriteFile(torrentPath, []byte("d8:announce0:4:infod4:name6:testee"), 0o644); err != nil {
		t.Fatalf("写种子失败: %v", err)
	}

	input := publisher.PublishInput{
		SiteCode:    "ssd",
		BaseURL:     srv.URL,
		Cookie:      "SPRINGID=test",
		TargetName:  "不可说",
		TorrentPath: torrentPath,
		Title:       "Test Movie 2024 1080p WEB-DL H.265 AAC 2.0-GROUP",
		MediaInfo:   "General\nFormat : Matroska",
		UploadData: map[string]any{
			"standardized_params": map[string]any{
				"type":        "category.movie",
				"source":      "source.western",
				"medium":      "medium.webdl",
				"resolution":  "resolution.r2160p",
				"video_codec": "video.h265",
				"audio_codec": "audio.aac",
				"tags":        []any{"tag.中字", "tag.HDR10"},
			},
			"poster":      "[img]https://img.example/poster.jpg[/img]",
			"screenshots": "[img]https://img.example/s1.png[/img]\n[img]https://img.example/s2.png[/img]",
			"mediainfo":   "General\nFormat : Matroska",
			"intro": map[string]any{
				"statement": "[quote]CSWEB官组作品[/quote]",
			},
		},
	}

	if _, err := PublishSSD(input); err != nil {
		t.Fatalf("PublishSSD 失败: %v", err)
	}
	if captured == nil {
		t.Fatal("未捕获到表单字段")
	}

	checks := map[string]string{
		"name":           "Test.Movie.2024.1080p.WEB-DL.H.265.AAC.2.0-GROUP", // 空格→点号
		"type":           "501",
		"source_sel":     "4",
		"medium_sel":     "7",
		"standard_sel":   "1",
		"codec_sel":      "1",
		"audiocodec_sel": "5",
		"subtitlezh":     "1", // 中字 checkbox（value=1）
		"hdr10":          "1",
		"url_poster":     "https://img.example/poster.jpg",
		"Media_BDInfo":   "General\nFormat : Matroska",
	}
	for k, want := range checks {
		if captured[k] != want {
			t.Errorf("%s = %q, want %q", k, captured[k], want)
		}
	}
	if v, ok := captured["offer"]; ok {
		t.Errorf("直发权限下不应提交候选标记 offer，got %q", v)
	}
	if captured["url_vimages"] != "https://img.example/s1.png\nhttps://img.example/s2.png" {
		t.Errorf("url_vimages = %q", captured["url_vimages"])
	}
	if !strings.Contains(captured["descr"], "CSWEB官组作品") {
		t.Errorf("descr 应含声明, got %q", captured["descr"])
	}
	if strings.Contains(captured["descr"], "img.example") {
		t.Errorf("descr 不应含海报/截图 URL（字段已分离）, got %q", captured["descr"])
	}
}

// TestPublishSSDMandarinRule 验证「产地为中国则不打国配标签」：
// 中国大陆/香港/台湾/港台复合产地都不下发 mandarin；其他产地照常下发 mandarin=1。
func TestPublishSSDMandarinRule(t *testing.T) {
	capture := func(t *testing.T, source string) map[string]string {
		t.Helper()
		var captured map[string]string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/upload.php"):
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<html><head><title>SSD :: 发布</title></head><body>
<form action="takeupload.php" method="post" enctype="multipart/form-data">
<input type="file" name="file"/>
<input type="text" name="name"/>
</form></body></html>`)
			case strings.HasSuffix(r.URL.Path, "/takeupload.php"):
				_ = r.ParseMultipartForm(32 << 20)
				captured = map[string]string{}
				for k := range r.MultipartForm.Value {
					captured[k] = r.MultipartForm.Value[k][0]
				}
				http.Redirect(w, r, "/details.php?id=1", http.StatusFound)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		t.Cleanup(srv.Close)

		torrentPath := t.TempDir() + "/x.torrent"
		if err := os.WriteFile(torrentPath, []byte("d8:announce0:4:infod4:name6:testee"), 0o644); err != nil {
			t.Fatalf("写种子失败: %v", err)
		}
		input := publisher.PublishInput{
			SiteCode:    "ssd",
			BaseURL:     srv.URL,
			Cookie:      "SPRINGID=test",
			TargetName:  "不可说",
			TorrentPath: torrentPath,
			Title:       "Test Movie 2024 1080p WEB-DL H.265 AAC 2.0-GROUP",
			MediaInfo:   "General\nFormat : Matroska",
			UploadData: map[string]any{
				"standardized_params": map[string]any{
					"type":        "category.movie",
					"source":      source,
					"medium":      "medium.webdl",
					"resolution":  "resolution.r1080p",
					"video_codec": "video.h265",
					"audio_codec": "audio.aac",
					"tags":        []any{"tag.国语"},
				},
				"mediainfo": "General\nFormat : Matroska",
				"intro":     map[string]any{"statement": "CSWEB官组作品"},
			},
		}
		if _, err := PublishSSD(input); err != nil {
			t.Fatalf("PublishSSD 失败: %v", err)
		}
		if captured == nil {
			t.Fatal("未捕获到表单字段")
		}
		return captured
	}

	for _, source := range []string{"source.china", "source.hongkong", "source.taiwan", "source.hongkong_taiwan"} {
		if got := capture(t, source); got["mandarin"] != "" {
			t.Errorf("产地=%s 属中国，不应下发国配(mandarin)，got %q", source, got["mandarin"])
		}
	}
	for _, source := range []string{"source.western", "source.japan"} {
		if got := capture(t, source); got["mandarin"] != "1" {
			t.Errorf("产地=%s 非中国，应照常下发国配(mandarin)=1，got %q", source, got["mandarin"])
		}
	}
}
