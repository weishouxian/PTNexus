package sites

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// hxptTestServer 构造一个模拟好学 JSON API 的站点，返回 server 与「最近一次上传」的采集结果。
type hxptCapturedUpload struct {
	csrf      string
	cookie    string
	form      map[string]string
	file      int
	fileBytes []byte
}

func newHXPTTestServer(t *testing.T, uploadResponse string, uploadStatus int) (*httptest.Server, *hxptCapturedUpload) {
	t.Helper()
	captured := &hxptCapturedUpload{form: map[string]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/site-profile", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":0,"data":{"categories":[{"id":1,"name":"电影"},{"id":2,"name":"电视剧"},{"id":8,"name":"纪录片"}]}}`)
	})
	mux.HandleFunc("/api/v1/section-dict", func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("category_id"); got != "1" {
			t.Errorf("section-dict 的 category_id 参数不符: %s", got)
		}
		io.WriteString(w, `{"code":0,"data":{"kinds":[{"kind":"media"},{"kind":"codec"},{"kind":"source"},{"kind":"team"}],`+
			`"media":[{"id":443,"name":"视频"},{"id":444,"name":"音频"}],`+
			`"codec":[{"id":435,"name":"H.264"},{"id":436,"name":"H.265"}],`+
			`"source":[{"id":451,"name":"WEB-DL"},{"id":453,"name":"BluRay"}],`+
			`"team":[{"id":470,"name":"HDSky"},{"id":529,"name":"其他"}]}}`)
	})
	mux.HandleFunc("/api/v1/tags-dict", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":0,"data":{"groups":[],"tags":[[3,"官种","official"],[8,"禁转","plain"],[10,"中字","plain"],[11,"DIY","plain"]]}}`)
	})
	mux.HandleFunc("/api/v1/torrents", func(w http.ResponseWriter, r *http.Request) {
		captured.csrf = r.Header.Get("x-csrf-token")
		captured.cookie = r.Header.Get("Cookie")
		if _, params, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err == nil {
			reader := multipart.NewReader(r.Body, params["boundary"])
			for {
				part, err := reader.NextPart()
				if err != nil {
					break
				}
				if part.FormName() == "file" {
					captured.file++
					captured.fileBytes, _ = io.ReadAll(part)
					continue
				}
				value, _ := io.ReadAll(part)
				captured.form[part.FormName()] = string(value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(uploadStatus)
		io.WriteString(w, uploadResponse)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, captured
}

// newHXPTTestInput 构造一次最小可用的发布输入（种子文件写进临时目录）。
func newHXPTTestInput(t *testing.T, baseURL string) publisher.PublishInput {
	t.Helper()
	dir := t.TempDir()
	torrentPath := filepath.Join(dir, "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name4:testee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}
	return publisher.PublishInput{
		TargetName:  "好学",
		SiteCode:    "hxpt",
		BaseURL:     baseURL,
		Cookie:      "c_secure_pass=abc; flux_csrf=csrf-token-123; flux.session=1",
		TorrentPath: torrentPath,
		Title:       "测试种子 2026 1080p BluRay x265-HDSky",
		Subtitle:    "测试副标题",
		Description: "简介正文",
		MediaInfo:   "General\nUnique ID: 1",
		IMDbLink:    "https://www.imdb.com/title/tt1234567/",
		UploadData: map[string]any{
			"standardized_params": map[string]any{
				"type":        "category.movie",
				"medium":      "medium.bluray",
				"video_codec": "video.h265",
				"team":        "team.hdsky",
				"tags":        []string{"tag.禁转", "tag.中字", "tag.杜比"},
			},
			"tmdb_link": "https://www.themoviedb.org/movie/1",
			"poster":    "[img]https://img.example.com/poster.jpg[/img]",
		},
		RootConfig: map[string]any{"upload_settings": map[string]any{"anonymous_upload": true}},
	}
}

// TestPublishHXPTUploadsMappedFields 覆盖一次成功的 API 发种：字段映射、CSRF 头、详情页拼装。
func TestPublishHXPTUploadsMappedFields(t *testing.T) {
	server, captured := newHXPTTestServer(t, `{"code":0,"data":{"id":12345},"message":"ok"}`, http.StatusOK)
	result, err := PublishHXPT(newHXPTTestInput(t, server.URL))
	if err != nil {
		t.Fatalf("发布应成功，实际报错: %v", err)
	}

	if captured.csrf != "csrf-token-123" {
		t.Fatalf("x-csrf-token 未按 flux_csrf 传递: %q", captured.csrf)
	}
	if !strings.Contains(captured.cookie, "flux_csrf=csrf-token-123") {
		t.Fatalf("Cookie 未原样透传: %q", captured.cookie)
	}
	if captured.file != 1 {
		t.Fatalf("种子文件字段应提交 1 次，实际 %d", captured.file)
	}
	if got := captured.form["category_id"]; got != "1" {
		t.Fatalf("category_id 应为电影(1)，实际 %q", got)
	}
	if got := captured.form["anonymous"]; got != "true" {
		t.Fatalf("anonymous 应为 true，实际 %q", got)
	}
	if got := captured.form["name"]; !strings.Contains(got, "测试种子") {
		t.Fatalf("name 未提交标题: %q", got)
	}
	if got := captured.form["poster"]; got != "https://img.example.com/poster.jpg" {
		t.Fatalf("poster 应取简介里的图片地址，实际 %q", got)
	}

	sections := map[string]any{}
	if err := json.Unmarshal([]byte(captured.form["sections"]), &sections); err != nil {
		t.Fatalf("sections 不是合法 JSON: %v (%q)", err, captured.form["sections"])
	}
	wantSections := map[string]float64{"media": 443, "codec": 436, "source": 453, "team": 470}
	for kind, want := range wantSections {
		if got, ok := sections[kind].(float64); !ok || got != want {
			t.Fatalf("sections.%s 应为 %v，实际 %v（完整 %v）", kind, want, sections[kind], sections)
		}
	}

	tags := []int{}
	if err := json.Unmarshal([]byte(captured.form["tags"]), &tags); err != nil {
		t.Fatalf("tags 不是合法 JSON: %v (%q)", err, captured.form["tags"])
	}
	if len(tags) != 2 || tags[0] != 8 || tags[1] != 10 {
		t.Fatalf("tags 应为 [8 10]（禁转/中字，杜比站内没有应被丢弃），实际 %v", tags)
	}

	if result.PublishURL != server.URL+"/torrent/12345" {
		t.Fatalf("详情页地址不符: %s", result.PublishURL)
	}
	if !strings.Contains(result.AttemptDetailLog, "分类: 电影 (category_id=1)") {
		t.Fatalf("日志缺少分类解析结果: %s", result.AttemptDetailLog)
	}
}

// TestPublishHXPTRequiresCSRFToken 校验 Cookie 缺 flux_csrf 时提前拒绝（站点会回 403 无权限）。
func TestPublishHXPTRequiresCSRFToken(t *testing.T) {
	server, _ := newHXPTTestServer(t, `{"code":0,"data":{"id":1}}`, http.StatusOK)
	input := newHXPTTestInput(t, server.URL)
	input.Cookie = "c_secure_pass=abc; flux.session=1"

	_, err := PublishHXPT(input)
	if err == nil {
		t.Fatal("缺 flux_csrf 应报错")
	}
	var precheck *publisher.PreCheckError
	if !errors.As(err, &precheck) {
		t.Fatalf("应返回 PreCheckError，实际 %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "flux_csrf") {
		t.Fatalf("错误提示应指明缺 flux_csrf: %v", err)
	}
}

// TestPublishHXPTRejectsUnmappedType 校验未识别类型直接拒绝，避免误发到默认分类。
func TestPublishHXPTRejectsUnmappedType(t *testing.T) {
	server, _ := newHXPTTestServer(t, `{"code":0,"data":{"id":1}}`, http.StatusOK)
	input := newHXPTTestInput(t, server.URL)
	input.UploadData["standardized_params"] = map[string]any{"type": "category.other"}

	_, err := PublishHXPT(input)
	if err == nil {
		t.Fatal("未映射类型应报错")
	}
	var precheck *publisher.PreCheckError
	if !errors.As(err, &precheck) {
		t.Fatalf("应返回 PreCheckError，实际 %T: %v", err, err)
	}
}

// TestPublishHXPTReportsForbidden 校验站点 403/2003 的提示能区分「权限/Cookie」而不是裸状态码。
func TestPublishHXPTReportsForbidden(t *testing.T) {
	server, _ := newHXPTTestServer(t, `{"code":2003,"data":null,"message":"无权限"}`, http.StatusForbidden)
	_, err := PublishHXPT(newHXPTTestInput(t, server.URL))
	if err == nil {
		t.Fatal("403 应报错")
	}
	if !strings.Contains(err.Error(), "cookie 可能已失效") && !strings.Contains(err.Error(), "Cookie 可能已失效") {
		t.Fatalf("错误提示应指向 Cookie/CSRF: %v", err)
	}
}

// TestPublishHXPTReportsFieldErrors 校验 1002 参数校验失败时把字段级原因带进错误。
func TestPublishHXPTReportsFieldErrors(t *testing.T) {
	server, _ := newHXPTTestServer(t,
		`{"code":1002,"data":[{"field":"descr","error":"简介不能为空"}],"message":"参数校验失败"}`, http.StatusBadRequest)
	_, err := PublishHXPT(newHXPTTestInput(t, server.URL))
	if err == nil {
		t.Fatal("1002 应报错")
	}
	if !strings.Contains(err.Error(), "descr=简介不能为空") {
		t.Fatalf("错误应包含字段级原因: %v", err)
	}
}

// TestPublishHXPTIsolatesInfoHash 校验上传的种子 info 内写入了 source=HXPT，
// 使好学种子与源站种子 infohash 不同：站点只替换 announce、不改 info，
// 不隔离的话下载器里已有源站任务时加种会判重复（删掉源站任务才加得上）。
func TestPublishHXPTIsolatesInfoHash(t *testing.T) {
	server, captured := newHXPTTestServer(t, `{"code":0,"data":{"id":12345},"message":"ok"}`, http.StatusOK)
	input := newHXPTTestInput(t, server.URL)

	original, err := os.ReadFile(input.TorrentPath)
	if err != nil {
		t.Fatalf("读取测试种子失败: %v", err)
	}
	origHash, err := acquirefetch.TorrentInfoHash(original)
	if err != nil {
		t.Fatalf("原始 infohash 计算失败: %v", err)
	}

	result, err := PublishHXPT(input)
	if err != nil {
		t.Fatalf("发布应成功，实际报错: %v", err)
	}
	if len(captured.fileBytes) == 0 {
		t.Fatal("未采集到上传的种子字节")
	}
	if !strings.Contains(string(captured.fileBytes), "6:source4:HXPT") {
		t.Fatalf("上传的种子 info 内应写入 source=HXPT，实际内容: %q", string(captured.fileBytes))
	}

	newHash, err := acquirefetch.TorrentInfoHash(captured.fileBytes)
	if err != nil {
		t.Fatalf("上传种子解析失败: %v", err)
	}
	if newHash == origHash {
		t.Fatalf("上传种子 infohash 未变化（仍是 %s），下载器会判为与源站重复", newHash)
	}
	if !strings.Contains(result.AttemptDetailLog, "info hash 隔离") {
		t.Fatalf("日志缺少 info hash 隔离说明: %s", result.AttemptDetailLog)
	}
	if !strings.Contains(result.AttemptDetailLog, origHash) || !strings.Contains(result.AttemptDetailLog, newHash) {
		t.Fatalf("日志缺少前后 infohash: %s", result.AttemptDetailLog)
	}
}

// TestHXPTCookieValue 覆盖 Cookie 解析（含 URL 转义）。
func TestHXPTCookieValue(t *testing.T) {
	cookie := "a=1; flux_csrf=abc%3Ddef; b=2"
	if got := hxptCookieValue(cookie, "flux_csrf"); got != "abc=def" {
		t.Fatalf("Cookie 解析不符: %q", got)
	}
	if got := hxptCookieValue(cookie, "missing"); got != "" {
		t.Fatalf("缺键应返回空串，实际 %q", got)
	}
}
