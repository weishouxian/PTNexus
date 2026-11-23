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

// newTTGStub 起一个假 TTG 站点：GET /upload.php 返回新版三栏表单，POST /takeupload.php 捕获字段。
func newTTGStub(t *testing.T, captured *map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/upload.php"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 上传</title></head><body>
<form name="upload" id="upload" enctype="multipart/form-data" action="/takeupload.php" method="post">
<input type="hidden" name="team" value="" />
<input type="hidden" name="hr" value="no" />
<input type="file" name="file"/>
<input type="text" name="name"/>
<input type="text" name="subtitle"/>
<input type="text" name="highlight"/>
<select name="type"><option value="0">（请选择一个）</option><option value="53">电影1080i/p</option><option value="54">BluRay原盘</option></select>
<textarea name="descr"></textarea>
<select name="anonymity"><option value="-1">请选择</option><option value="yes">是</option><option value="no">否</option></select>
<select name="nodistr"><option value="yes">是</option><option value="no" selected="selected">否</option></select>
<input type="text" name="imdb_c"/>
<input type="text" name="douban_id"/>
</form></body></html>`)
		case strings.HasSuffix(r.URL.Path, "/takeupload.php"):
			_ = r.ParseMultipartForm(32 << 20)
			result := map[string]string{}
			if r.MultipartForm != nil {
				for k := range r.MultipartForm.Value {
					result[k] = r.MultipartForm.Value[k][0]
				}
			}
			*captured = result
			http.Redirect(w, r, "/dl/842062/abcdef123456", http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPublishTTGTitleFields 验证 TTG 新版上传表单的标题三栏装配：
// 主标题原样（不再 "." → "{@}"）、副标题独立成栏并剥掉 []、禁转标签驱动 nodistr、type 按规格合并。
func TestPublishTTGTitleFields(t *testing.T) {
	var captured map[string]string
	srv := newTTGStub(t, &captured)

	torrentPath := t.TempDir() + "/x.torrent"
	if err := os.WriteFile(torrentPath, []byte("d8:announce0:4:infod4:name6:testee"), 0o644); err != nil {
		t.Fatalf("写种子失败: %v", err)
	}

	input := publisher.PublishInput{
		SiteCode:    "ttg",
		BaseURL:     srv.URL,
		Cookie:      "uid=1; pass=test",
		TargetName:  "TTG",
		TorrentPath: torrentPath,
		Title:       "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY",
		Subtitle:    "[拳击影子 / The Shadow Boxing 简繁中字]",
		MediaInfo:   "General\nFormat : Matroska",
		UploadData: map[string]any{
			"standardized_params": map[string]any{
				"type":       "category.movie",
				"medium":     "medium.bluray",
				"resolution": "resolution.r1080p",
				"tags":       []any{"tag.禁转", "tag.中字"},
			},
		},
	}

	result, err := PublishTTG(input)
	if err != nil {
		t.Fatalf("PublishTTG 失败: %v", err)
	}
	if captured == nil {
		t.Fatal("未捕获到表单字段")
	}

	// 主标题必须原样保留点号：站点现有种子标题就是 "DTS-HD MA 5.1-DIY@TTG" 形态
	if want := "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY"; captured["name"] != want {
		t.Errorf("name = %q, want %q", captured["name"], want)
	}
	if strings.Contains(captured["name"], "{@}") {
		t.Errorf("name 不应含 {@} 占位符, got %q", captured["name"])
	}
	// 副标题独立成栏，剥掉 []
	if want := "拳击影子 / The Shadow Boxing 简繁中字"; captured["subtitle"] != want {
		t.Errorf("subtitle = %q, want %q", captured["subtitle"], want)
	}
	if _, exists := captured["small_descr"]; exists {
		t.Errorf("不应提交 small_descr（TTG 表单无此字段）, got %q", captured["small_descr"])
	}
	// type 由 category + medium + resolution 合并：BluRay 原盘落 54（BluRay原盘）
	if captured["type"] != "54" {
		t.Errorf("type = %q, want 54", captured["type"])
	}
	// 禁转标签 → nodistr=yes
	if captured["nodistr"] != "yes" {
		t.Errorf("nodistr = %q, want yes（带 tag.禁转）", captured["nodistr"])
	}
	// 上传后被重定向到 /dl/{id}/{passkey}，应归一化为详情页链接
	if want := srv.URL + "/details.php?id=842062"; result.PublishURL != want {
		t.Errorf("PublishURL = %q, want %q", result.PublishURL, want)
	}
}

// TestPublishTTGSubtitleTruncate 验证副标题按站点 maxlength=55 截断（按 rune，不切坏汉字），
// 且无禁转标签时 nodistr 保持 no。
func TestPublishTTGSubtitleTruncate(t *testing.T) {
	var captured map[string]string
	srv := newTTGStub(t, &captured)

	torrentPath := t.TempDir() + "/x.torrent"
	if err := os.WriteFile(torrentPath, []byte("d8:announce0:4:infod4:name6:testee"), 0o644); err != nil {
		t.Fatalf("写种子失败: %v", err)
	}

	longSubtitle := strings.Repeat("中文字幕", 20) // 80 个字符，超过 55
	input := publisher.PublishInput{
		SiteCode:    "ttg",
		BaseURL:     srv.URL,
		Cookie:      "uid=1; pass=test",
		TargetName:  "TTG",
		TorrentPath: torrentPath,
		Title:       "Some.Movie.2024.1080p.BluRay.AVC.DTS-HD.MA.5.1-GROUP",
		Subtitle:    longSubtitle,
		UploadData: map[string]any{
			"standardized_params": map[string]any{
				"type":       "category.movie",
				"medium":     "medium.bluray",
				"resolution": "resolution.r1080p",
			},
		},
	}

	if _, err := PublishTTG(input); err != nil {
		t.Fatalf("PublishTTG 失败: %v", err)
	}
	if got := len([]rune(captured["subtitle"])); got != 55 {
		t.Errorf("subtitle 长度 = %d, want 55", got)
	}
	if captured["nodistr"] != "no" {
		t.Errorf("nodistr = %q, want no（无禁转标签）", captured["nodistr"])
	}
}

// TestPublishTTGOfferFields 验证 TTG 候选提交：候选页字段名（name/picture/msg/type/seeding_env）装配、
// type 沿用分类+媒介+分辨率合并规则、名称按「英文名 [中文名]」格式、候选链接解析。
func TestPublishTTGOfferFields(t *testing.T) {
	var captured map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/offer.php"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 申请上传</title></head><body>
<form action="takeoffer.php" name="Form" id="Form" method="post">
<input type="text" name="name" size="80" />
<input type=text name=picture>
<textarea name="msg" rows="20" cols="100"></textarea>
<select name="type"><option value="0">(请选择)</option><option value="54">BluRay原盘</option></select>
<input type="text" name="seeding_env" size=20 />
<input type="submit" class=btn value="提交！" />
</form></body></html>`)
		case strings.HasSuffix(r.URL.Path, "/takeoffer.php"):
			_ = r.ParseForm()
			result := map[string]string{}
			for k := range r.PostForm {
				result[k] = r.PostForm.Get(k)
			}
			captured = result
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 申请成功！</title></head><body>
<p>候选已提交，<a href="/offers.php?id=7788">查看</a></p></body></html>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	input := publisher.PublishInput{
		SiteCode:    "ttg",
		BaseURL:     srv.URL,
		Cookie:      "uid=1; pass=test",
		TargetName:  "TTG",
		TorrentPath: "", // 候选不读种子文件
		Title:       "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY",
		Subtitle:    "[拳击影子 / The Shadow Boxing]",
		Description: "简介正文\n[img]https://img.example/poster.jpg[/img]",
		UploadData: map[string]any{
			"poster": "[img]https://img.example/poster.jpg[/img]",
			"standardized_params": map[string]any{
				"type":       "category.movie",
				"medium":     "medium.bluray",
				"resolution": "resolution.r1080p",
			},
		},
	}

	result, err := PublishTTGOffer(input)
	if err != nil {
		t.Fatalf("PublishTTGOffer 失败: %v", err)
	}
	if captured == nil {
		t.Fatal("未捕获到候选提交字段")
	}

	if want := "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY [拳击影子 / The Shadow Boxing]"; captured["name"] != want {
		t.Errorf("name = %q, want %q", captured["name"], want)
	}
	if captured["type"] != "54" {
		t.Errorf("type = %q, want 54（BluRay原盘）", captured["type"])
	}
	if captured["picture"] != "https://img.example/poster.jpg" {
		t.Errorf("picture = %q", captured["picture"])
	}
	if !strings.Contains(captured["msg"], "简介正文") {
		t.Errorf("msg 应含简介正文, got %q", captured["msg"])
	}
	if captured["seeding_env"] != "7x24小时在线" {
		t.Errorf("seeding_env = %q, want 7x24小时在线", captured["seeding_env"])
	}
	// 候选不应提交种子文件相关字段
	for _, key := range []string{"file", "descr", "small_descr"} {
		if _, exists := captured[key]; exists {
			t.Errorf("候选不应提交字段 %q", key)
		}
	}
	if want := srv.URL + "/offers.php?id=7788"; result.PublishURL != want {
		t.Errorf("PublishURL = %q, want %q", result.PublishURL, want)
	}
}
