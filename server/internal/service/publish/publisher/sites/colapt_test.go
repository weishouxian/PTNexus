package sites

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// colaptTestInput 构造一份最小可用的发布输入（媒体信息里带一条 DTS-HD MA 音轨，用于验证声道拼接）。
func colaptTestInput(baseURL string, torrentPath string) publisher.PublishInput {
	return publisher.PublishInput{
		TargetName:  "可乐PT",
		SiteCode:    "colapt",
		BaseURL:     baseURL,
		TargetInfo:  map[string]any{"site": "colapt", "passkey": "cola-test-api-key"},
		TorrentPath: torrentPath,
		Title:       "Some.Movie.2024.2160p.BluRay.REMUX.HEVC.DTS-HD.MA.5.1-GROUP",
		Subtitle:    "国语简中",
		Description: "◎译　　名 某电影",
		DoubanLink:  "https://movie.douban.com/subject/35267224/",
		MediaInfo: "Video\nFormat : HEVC\n\nAudio #1\nFormat : DTS XLL\nChannel(s) : 6 channels\n" +
			"Language : Chinese\nDefault : Yes\n",
		UploadData: map[string]any{
			"standardized_params": map[string]any{
				"type":        "category.movie",
				"medium":      "medium.remux",
				"resolution":  "resolution.r2160p",
				"video_codec": "video.h265",
				"audio_codec": "audio.dts_hd_ma",
				"team":        "team.group",
			},
			"tags":        []any{"tag.中字", "tag.杜比", "tag.HDR10"},
			"screenshots": "[img]https://img.example.com/1.jpg[/img]\n[img]https://img.example.com/2.jpg[/img]",
			"team":        "GROUP",
			"year":        "2024",
		},
	}
}

// TestPublishColaptFlow 端到端验证可乐PT 三段式发种：
// 上传种子 → 写元数据（片源类型/分辨率/编码/HDR/音轨/字幕/截图）→ 关联作品提交。
func TestPublishColaptFlow(t *testing.T) {
	var (
		uploadContentType string
		uploadAPIKey      string
		metadata          map[string]any
		submit            map[string]any
		metadataRev       any
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents":
			uploadContentType = r.Header.Get("Content-Type")
			uploadAPIKey = r.Header.Get("X-API-Key")
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":123,"info_hash":"abc123","status":"pending","revision":1,"submitted_at":null,"publication_stage":"draft"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/automation/torrents/123/metadata":
			body, _ := io.ReadAll(r.Body)
			metadata = map[string]any{}
			_ = json.Unmarshal(body, &metadata)
			metadataRev = metadata["expected_revision"]
			_, _ = w.Write([]byte(`{"id":123,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/automation/torrents/123":
			_, _ = w.Write([]byte(`{"id":123,"revision":2,"status":"pending","publication_stage":"draft","warnings":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents/123/submit":
			body, _ := io.ReadAll(r.Body)
			submit = map[string]any{}
			_ = json.Unmarshal(body, &submit)
			_, _ = w.Write([]byte(`{"id":123,"status":"pending","revision":3,"submitted_at":"2026-10-22T12:00:00Z","publication_stage":"published","media":{"media_id":"rc9"},"screenshots":[],"site_revision":0,"site_write_state":"idle","warnings":[]}`))
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce4:http4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	result, err := PublishColapt(colaptTestInput(srv.URL, torrentPath))
	if err != nil {
		t.Fatalf("发布失败: %v\n日志:\n%s", err, result.AttemptDetailLog)
	}

	if uploadAPIKey != "cola-test-api-key" {
		t.Errorf("上传请求未带正确的 X-API-Key，实际=%q", uploadAPIKey)
	}
	if uploadContentType != "application/x-bittorrent" {
		t.Errorf("上传请求 Content-Type 应为 application/x-bittorrent，实际=%q", uploadContentType)
	}
	if metadataRev != float64(1) {
		t.Errorf("元数据写入应带 expected_revision=1，实际=%v", metadataRev)
	}

	wantMetadata := map[string]any{
		"release_name":  "Some.Movie.2024.2160p.BluRay.REMUX.HEVC.DTS-HD.MA.5.1-GROUP",
		"subtitle":      "国语简中",
		"source":        "Remux",
		"resolution":    "2160p",
		"video_codec":   "HEVC / H.265",
		"hdr":           "Dolby Vision + HDR10",
		"release_group": "GROUP",
	}
	for key, want := range wantMetadata {
		if got := metadata[key]; got != want {
			t.Errorf("元数据 %s 期望 %q，实际 %v", key, want, got)
		}
	}

	audio, _ := metadata["audio"].([]any)
	if len(audio) != 1 || audio[0] != "DTS-HD MA 5.1" {
		t.Errorf("音轨应拼成 [DTS-HD MA 5.1]，实际=%v", metadata["audio"])
	}
	subtitles, _ := metadata["subtitles"].([]any)
	if len(subtitles) != 1 || subtitles[0] != "zh" {
		t.Errorf("字幕应为 [zh]，实际=%v", metadata["subtitles"])
	}
	screenshots, _ := metadata["screenshots"].([]any)
	if len(screenshots) != 2 {
		t.Errorf("截图应提取 2 张，实际=%v", metadata["screenshots"])
	}
	if _, ok := metadata["release_scope"]; ok {
		t.Errorf("电影不应提交 release_scope，实际=%v", metadata["release_scope"])
	}

	if got := submit["media_id"]; got != "https://movie.douban.com/subject/35267224/" {
		t.Errorf("提交应关联豆瓣作品标识，实际=%v", got)
	}
	if got := submit["expected_revision"]; got != float64(2) {
		t.Errorf("提交应带最新 revision=2，实际=%v", got)
	}
	if _, ok := submit["new_media"]; ok {
		t.Errorf("已有作品标识时不应再传 new_media")
	}
	if !strings.HasSuffix(result.PublishURL, "/torrents/123") {
		t.Errorf("详情页地址不对: %s", result.PublishURL)
	}
	if !strings.Contains(result.AttemptDetailLog, "步骤3 提交成功") {
		t.Errorf("日志缺少提交成功记录:\n%s", result.AttemptDetailLog)
	}
}

// TestPublishColaptMediaNotFoundFallback 验证站内没有该作品档案时，自动改用 new_media 建档重提一次。
func TestPublishColaptMediaNotFoundFallback(t *testing.T) {
	submitCount := 0
	var secondSubmit map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"info_hash":"x","revision":1,"status":"pending","publication_stage":"draft"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/metadata"):
			_, _ = w.Write([]byte(`{"id":7,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/automation/torrents/7":
			_, _ = w.Write([]byte(`{"id":7,"revision":2}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/submit"):
			submitCount++
			if submitCount == 1 {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"code":"media_not_found","message":"media_not_found","fields":{},"request_id":"r1"}}`))
				return
			}
			body, _ := io.ReadAll(r.Body)
			secondSubmit = map[string]any{}
			_ = json.Unmarshal(body, &secondSubmit)
			_, _ = w.Write([]byte(`{"id":7,"status":"pending","revision":3,"submitted_at":"2026-10-22T12:00:00Z","publication_stage":"pending_review","media":{"media_id":"rc1"},"screenshots":[],"site_revision":0,"site_write_state":"idle","warnings":[]}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	result, err := PublishColapt(colaptTestInput(srv.URL, torrentPath))
	if err != nil {
		t.Fatalf("发布失败: %v\n日志:\n%s", err, result.AttemptDetailLog)
	}
	if submitCount != 2 {
		t.Fatalf("应提交两次（先 media_id 后 new_media），实际=%d", submitCount)
	}
	newMedia, ok := secondSubmit["new_media"].(map[string]any)
	if !ok {
		t.Fatalf("第二次提交应带 new_media，实际=%v", secondSubmit)
	}
	if newMedia["category"] != "movie" {
		t.Errorf("new_media 分类应为 movie，实际=%v", newMedia["category"])
	}
	if newMedia["douban_id"] != "35267224" {
		t.Errorf("new_media 应带豆瓣 ID，实际=%v", newMedia["douban_id"])
	}
	if _, ok := secondSubmit["media_id"]; ok {
		t.Errorf("第二次提交不应再带 media_id")
	}
	if !strings.Contains(result.AttemptDetailLog, "未上架") && !strings.Contains(result.AttemptDetailLog, "审核") {
		t.Errorf("非 published 阶段应提示审核，日志:\n%s", result.AttemptDetailLog)
	}
}

// TestPublishColaptMissingAPIKey 验证未配置 API Key 时给出可操作的提示，且不发任何请求。
func TestPublishColaptMissingAPIKey(t *testing.T) {
	input := colaptTestInput("https://www.colapt.com", "does-not-matter.torrent")
	input.TargetInfo = map[string]any{"site": "colapt"}
	input.Cookie = ""

	_, err := PublishColapt(input)
	if err == nil {
		t.Fatal("缺少 API Key 时应返回错误")
	}
	if !strings.Contains(err.Error(), "API Key") {
		t.Errorf("错误提示应说明缺少 API Key，实际=%q", err.Error())
	}
}

// TestColaptDupeRejectedAsPreCheck 验证站点侧查重拒绝被包装成 PreCheckError（上层判为跳过而非失败重试）。
func TestColaptDupeRejectedAsPreCheck(t *testing.T) {
	var picked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"revision":1,"status":"pending","publication_stage":"draft"}`))
		case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/metadata"):
			_, _ = w.Write([]byte(`{"id":9,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/automation/torrents/9":
			_, _ = w.Write([]byte(`{"id":9,"revision":2}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/submit"):
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"error":{"code":"dupe_lower_quality","message":"已有更高画质的同版本","fields":{},"request_id":"r2"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	picked = "done"

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	_, err := PublishColapt(colaptTestInput(srv.URL, torrentPath))
	if err == nil {
		t.Fatal("站点查重拒绝时应返回错误")
	}
	if _, ok := publisher.AsPreCheckError(err); !ok {
		t.Fatalf("查重拒绝应包装为 PreCheckError，实际=%T: %v", err, err)
	}
	if picked != "done" {
		t.Fatal("测试桩未执行")
	}
}

// TestColaptPick 验证字典查找：完整键与去前缀键都能命中，未命中回落兜底。
func TestColaptPick(t *testing.T) {
	mapping := map[string]string{"bluray_disc": "Blu-ray Disc", "remux": "Remux"}
	cases := []struct {
		raw      string
		prefix   string
		fallback string
		want     string
		matched  bool
	}{
		{raw: "medium.bluray_disc", prefix: "medium.", fallback: "未知", want: "Blu-ray Disc", matched: true},
		{raw: "bluray_disc", prefix: "medium.", fallback: "未知", want: "Blu-ray Disc", matched: true},
		{raw: "MEDIUM.Remux", prefix: "medium.", fallback: "未知", want: "Remux", matched: true},
		{raw: "medium.cd", prefix: "medium.", fallback: "未知", want: "未知", matched: false},
		{raw: "", prefix: "medium.", fallback: "未知", want: "未知", matched: false},
	}
	for _, tc := range cases {
		got, matched := colaptPick(mapping, tc.raw, tc.prefix, tc.fallback)
		if got != tc.want || matched != tc.matched {
			t.Errorf("colaptPick(%q) = (%q,%v)，期望 (%q,%v)", tc.raw, got, matched, tc.want, tc.matched)
		}
	}
}

// TestColaptExternalReferences 验证作品标识归一（豆瓣 subject URL / IMDb tt 号）。
func TestColaptExternalReferences(t *testing.T) {
	doubanCases := map[string]string{
		"https://movie.douban.com/subject/35267224/": "https://movie.douban.com/subject/35267224/",
		"https://movie.douban.com/subject/35267224":  "https://movie.douban.com/subject/35267224/",
		"35267224":   "https://movie.douban.com/subject/35267224/",
		"":           "",
		"not-a-link": "",
	}
	for raw, want := range doubanCases {
		if got := colaptDoubanReference(raw); got != want {
			t.Errorf("colaptDoubanReference(%q) = %q，期望 %q", raw, got, want)
		}
	}

	imdbCases := map[string]string{
		"https://www.imdb.com/title/tt1160419/": "tt1160419",
		"tt1160419":                             "tt1160419",
		"1160419":                               "",
		"":                                      "",
	}
	for raw, want := range imdbCases {
		if got := colaptIMDbReference(raw); got != want {
			t.Errorf("colaptIMDbReference(%q) = %q，期望 %q", raw, got, want)
		}
	}
}

// TestBuildColaptReleaseScope 验证发布范围：剧集按单集/合集区分，动漫补 anime_type，电影不带范围。
func TestBuildColaptReleaseScope(t *testing.T) {
	cfg := colaptDefaultConfig

	movie := map[string]any{"standardized_params": map[string]any{"type": "category.movie"}}
	if scope, seasons := buildColaptReleaseScope(movie, colaptStandardized(movie), resolveSiteCombinedTags(movie), "movie"); scope != nil || seasons != "" {
		t.Errorf("电影不应产生发布范围/季，实际 scope=%v seasons=%q", scope, seasons)
	}

	tvSingle := map[string]any{
		"standardized_params": map[string]any{"type": "category.tv_series"},
		"season_episode":      "S02E05",
	}
	scope, _ := buildColaptReleaseScope(tvSingle, colaptStandardized(tvSingle), resolveSiteCombinedTags(tvSingle), "tv")
	if scope["scope"] != "single" || scope["episodes"] != "5" || scope["season"] != "2" {
		t.Errorf("单集发布范围不对: %v", scope)
	}

	tvPack := map[string]any{
		"standardized_params": map[string]any{"type": "category.tv_series"},
		"tags":                []any{"tag.合集"},
		"season_episode":      "S01E01",
	}
	scope, _ = buildColaptReleaseScope(tvPack, colaptStandardized(tvPack), resolveSiteCombinedTags(tvPack), "tv")
	if scope["scope"] != "complete" || scope["episodes"] != nil {
		t.Errorf("合集发布范围不对: %v", scope)
	}

	anime := map[string]any{
		"standardized_params": map[string]any{"type": "category.animation"},
		"tags":                []any{"tag.动画"},
		"title":               "某番 剧场版",
	}
	scope, _ = buildColaptReleaseScope(anime, colaptStandardized(anime), resolveSiteCombinedTags(anime), "anime")
	if scope["anime_type"] != "movie" || scope["season"] != nil || scope["episodes"] != nil {
		t.Errorf("剧场版动漫范围不对: %v", scope)
	}

	if got := buildColaptCategory(tvSingle, colaptStandardized(tvSingle), resolveSiteCombinedTags(tvSingle), cfg); got != "tv" {
		t.Errorf("剧集分类应为 tv，实际=%q", got)
	}
	if got := buildColaptCategory(anime, colaptStandardized(anime), resolveSiteCombinedTags(anime), cfg); got != "anime" {
		t.Errorf("动画标签应优先归为 anime，实际=%q", got)
	}
}

// TestColaptTruncateUTF8 验证按字节截断不会切碎中文字符。
func TestColaptTruncateUTF8(t *testing.T) {
	text := strings.Repeat("可乐", 10) // 60 字节
	got := colaptTruncateUTF8(text, 31)
	if len(got) > 31 {
		t.Fatalf("截断后仍超长: %d", len(got))
	}
	if strings.Contains(got, "\uFFFD") {
		t.Fatalf("截断破坏了多字节字符: %q", got)
	}
	if got != strings.Repeat("可乐", 5) {
		t.Fatalf("截断结果不符: %q", got)
	}
}

// TestPublishColaptFallsBackToSessionWhenKeyDenied 验证 API Key 被站点拒绝时自动改用 Cookie 会话重走一遍。
// 场景依据（2026-10-31 实测）：账号未获「自动发种」资格时 Key 的 scopes 为空，
// 连 POST /automation/torrents 都回 403 api_key_scope_denied；而账号本身有 torrent.upload 权限，
// 换网页版 /api/v1/torrents 通道（Cookie + CSRF）即可发种。此时站点侧还没建草稿，切换是安全的。
func TestPublishColaptFallsBackToSessionWhenKeyDenied(t *testing.T) {
	var (
		keyUploadCount int
		sessionUpload  struct {
			cookie         string
			csrf           string
			idempotencyKey string
			contentType    string
			origin         string
		}
		metadataCSRF string
		submitCSRF   string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents":
			keyUploadCount++
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"api_key_scope_denied","message":"api_key_scope_denied","fields":{},"request_id":"r1"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
			_, _ = w.Write([]byte(`{"id":101,"username":"u","session":{"csrf_token":"tok-1","expires_at":"2026-11-07T05:11:24Z"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents":
			_, _ = io.ReadAll(r.Body)
			sessionUpload.cookie = r.Header.Get("Cookie")
			sessionUpload.csrf = r.Header.Get("X-CSRF-Token")
			sessionUpload.idempotencyKey = r.Header.Get("Idempotency-Key")
			sessionUpload.contentType = r.Header.Get("Content-Type")
			sessionUpload.origin = r.Header.Get("Origin")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":321,"revision":1,"info_hash":"h","publication_stage":"draft"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/torrents/321/metadata":
			metadataCSRF = r.Header.Get("X-CSRF-Token")
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":321,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/torrents/321":
			_, _ = w.Write([]byte(`{"id":321,"revision":2,"warnings":[]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents/321/submit":
			submitCSRF = r.Header.Get("X-CSRF-Token")
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":321,"status":"approved","revision":3,"publication_stage":"published"}`))
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d8:announce4:http4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	input := colaptTestInput(srv.URL, torrentPath)
	input.Cookie = "reelcore_session=abcdef; reelcore_session_marker=1"

	result, err := PublishColapt(input)
	if err != nil {
		t.Fatalf("降级到会话通道后仍失败: %v\n日志:\n%s", err, result.AttemptDetailLog)
	}
	if keyUploadCount != 1 {
		t.Errorf("应先尝试一次 API Key 通道，实际 %d 次", keyUploadCount)
	}
	if sessionUpload.cookie == "" || !strings.Contains(sessionUpload.cookie, "reelcore_session=abcdef") {
		t.Errorf("会话上传未带 Cookie: %q", sessionUpload.cookie)
	}
	if sessionUpload.csrf != "tok-1" || metadataCSRF != "tok-1" || submitCSRF != "tok-1" {
		t.Errorf("写请求未统一带 CSRF: upload=%q metadata=%q submit=%q", sessionUpload.csrf, metadataCSRF, submitCSRF)
	}
	if sessionUpload.contentType != "application/x-bittorrent" {
		t.Errorf("会话上传 Content-Type 应为 application/x-bittorrent，实际=%q", sessionUpload.contentType)
	}
	if len(sessionUpload.idempotencyKey) < 16 {
		t.Errorf("会话上传缺少幂等键: %q", sessionUpload.idempotencyKey)
	}
	if sessionUpload.origin == "" {
		t.Error("会话写请求缺少 Origin 头")
	}
	if !strings.HasSuffix(result.PublishURL, "/torrents/321") {
		t.Errorf("详情页地址不对: %s", result.PublishURL)
	}
	if !strings.Contains(result.AttemptDetailLog, "已自动降级") {
		t.Errorf("日志未记录降级过程:\n%s", result.AttemptDetailLog)
	}
}

// TestPublishColaptSessionOnlySkipsKeyChannel 验证只配了 Cookie 时直接走会话通道，不发多余的 Key 请求。
func TestPublishColaptSessionOnlySkipsKeyChannel(t *testing.T) {
	keyHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v1/automation"):
			keyHits++
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"api_key_required","message":"api_key_required"}}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
			_, _ = w.Write([]byte(`{"session":{"csrf_token":"tok-2"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents":
			_, _ = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":7,"revision":1}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/torrents/7/metadata":
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":7,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/torrents/7":
			_, _ = w.Write([]byte(`{"id":7,"revision":2}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents/7/submit":
			_, _ = io.ReadAll(r.Body)
			_, _ = w.Write([]byte(`{"id":7,"publication_stage":"published","status":"approved"}`))
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	input := colaptTestInput(srv.URL, torrentPath)
	input.TargetInfo = map[string]any{"site": "colapt"}
	input.Cookie = "reelcore_session=xyz"

	result, err := PublishColapt(input)
	if err != nil {
		t.Fatalf("会话通道发布失败: %v\n日志:\n%s", err, result.AttemptDetailLog)
	}
	if keyHits != 0 {
		t.Errorf("只配 Cookie 时不应请求 automation 接口，实际 %d 次", keyHits)
	}
	if !strings.Contains(result.AttemptDetailLog, "Cookie 会话") {
		t.Errorf("日志未标注使用会话凭据:\n%s", result.AttemptDetailLog)
	}
}

// TestPublishColaptRefreshesCSRFOnForbidden 验证会话通道遇到 403 时刷新 CSRF 并重放一次请求。
func TestPublishColaptRefreshesCSRFOnForbidden(t *testing.T) {
	meCalls := 0
	uploadCalls := 0
	csrfSeen := []string{}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
			meCalls++
			token := "tok-old"
			if meCalls > 1 {
				token = "tok-new"
			}
			_, _ = w.Write([]byte(`{"session":{"csrf_token":"` + token + `"}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents":
			uploadCalls++
			csrfSeen = append(csrfSeen, r.Header.Get("X-CSRF-Token"))
			if uploadCalls == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"csrf_token_invalid","message":"csrf"}}`))
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":9,"revision":1}`))
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/torrents/9/metadata":
			_, _ = w.Write([]byte(`{"id":9,"revision":2}`))
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/torrents/9":
			_, _ = w.Write([]byte(`{"id":9,"revision":2}`))
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents/9/submit":
			_, _ = w.Write([]byte(`{"id":9,"publication_stage":"published"}`))
		default:
			t.Errorf("未预期的请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	torrentPath := filepath.Join(t.TempDir(), "test.torrent")
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name1:aee"), 0o600); err != nil {
		t.Fatalf("写入测试种子失败: %v", err)
	}

	input := colaptTestInput(srv.URL, torrentPath)
	input.TargetInfo = map[string]any{"site": "colapt"}
	input.Cookie = "reelcore_session=xyz"

	if _, err := PublishColapt(input); err != nil {
		t.Fatalf("CSRF 刷新后仍失败: %v", err)
	}
	if meCalls != 2 {
		t.Errorf("CSRF 应刷新一次（共请求 /me 两次），实际 %d", meCalls)
	}
	if len(csrfSeen) != 2 || csrfSeen[0] != "tok-old" || csrfSeen[1] != "tok-new" {
		t.Errorf("重放未使用新 CSRF 令牌: %v", csrfSeen)
	}
}
