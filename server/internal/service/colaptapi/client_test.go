package colaptapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestClient 起一个假的可乐站 API 服务并返回客户端。
func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := NewClient(server.URL, "test-key")
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	return client, server.Close
}

func TestNewClientRequiresAPIKey(t *testing.T) {
	if _, err := NewClient("https://www.colapt.com", "   "); err == nil {
		t.Fatal("缺少 API Key 时应返回错误")
	}
}

func TestTorrentParsesDetail(t *testing.T) {
	payload := `{
		"id": 6211,
		"media_id": "rc88",
		"release_name": "沙丘2.Dune.Part.Two.2024.UHD.BluRay.2160p.HEVC.TrueHD.7.1-MTeam",
		"subtitle": "沙丘2",
		"release_group": "MTeam",
		"description": "◎产　　地　美国\n简介正文",
		"media_info": "General\nUnique ID: 1",
		"size_bytes": "10737418240",
		"screenshots": ["https://img.example.com/1.jpg"],
		"tags": ["原盘", "HDR", "中字", "1080P"],
		"resolution": "2160p",
		"source": "Blu-ray Disc",
		"video_codec": "H.265",
		"hdr": "Dolby Vision",
		"audio": ["DTS-HD MA 5.1"],
		"subtitles": ["zh", "en"],
		"release_scope": {"category": "movie"},
		"download_allowed": true
	}`
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/automation/torrents/6211" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Errorf("API Key 头错误: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, payload)
	})
	defer closeFn()

	torrent, err := client.Torrent("https://www.colapt.com/torrents/6211")
	if err != nil {
		t.Fatalf("读取详情失败: %v", err)
	}
	if torrent.ReleaseName == "" || torrent.MediaID != "rc88" {
		t.Fatalf("字段解析异常: %+v", torrent)
	}
	if got := torrent.SizeBytesValue(); got != 10737418240 {
		t.Fatalf("size_bytes 解析错误: %d", got)
	}
	if got := torrent.ScopeCategory(); got != "movie" {
		t.Fatalf("分类解析错误: %q", got)
	}
}

func TestTorrentAuthError(t *testing.T) {
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"code":"authentication_required","message":"authentication_required","fields":{},"request_id":"abc"}}`)
	})
	defer closeFn()

	_, err := client.Torrent("6211")
	if err == nil {
		t.Fatal("401 应返回错误")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("错误类型应为 *APIError，实际 %T", err)
	}
	if apiErr.Code != "authentication_required" || apiErr.Status != 401 || apiErr.RequestID != "abc" {
		t.Fatalf("错误字段解析异常: %+v", apiErr)
	}
	if !strings.Contains(apiErr.Error(), "读取种子详情") {
		t.Fatalf("错误信息缺少阶段名: %s", apiErr.Error())
	}
}

func TestTorrentRejectsEmptyReleaseName(t *testing.T) {
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":6211,"release_name":""}`)
	})
	defer closeFn()

	if _, err := client.Torrent("6211"); err == nil {
		t.Fatal("release_name 为空时应返回错误")
	}
}

func TestMediaParsesExternalIDs(t *testing.T) {
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/automation/media/rc88" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"media_id":"rc88","title":"沙丘2","year":2024,"douban_id":"35661174","imdb_id":"tt15239678","tmdb_url":"https://www.themoviedb.org/movie/693134","countries":["美国","加拿大"],"poster_url":"https://img.example.com/p.jpg","category":"movie"}`)
	})
	defer closeFn()

	media, err := client.Media("rc88")
	if err != nil {
		t.Fatalf("读取作品失败: %v", err)
	}
	if media.DoubanID != "35661174" || media.IMDbID != "tt15239678" {
		t.Fatalf("外链 ID 解析异常: %+v", media)
	}
	if len(media.Countries) != 2 || media.Year != 2024 {
		t.Fatalf("作品字段解析异常: %+v", media)
	}
}

func TestDownloadTorrentIssuesAuthorizationFirst(t *testing.T) {
	var postedBodies []string
	var downloadAuthIDs []string
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/automation/torrents/6211/authorizations":
			raw, _ := io.ReadAll(r.Body)
			postedBodies = append(postedBodies, string(raw))
			_, _ = io.WriteString(w, `{"id":77,"torrent_id":6211,"usable":true,"replaces_id":null,"revoked_at":null,"created_at":"2026-10-31T00:00:00Z"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/automation/torrents/6211/download":
			downloadAuthIDs = append(downloadAuthIDs, r.URL.Query().Get("authorization_id"))
			if r.Header.Get("Accept") != "application/x-bittorrent" {
				t.Errorf("Accept 头错误: %q", r.Header.Get("Accept"))
			}
			_, _ = w.Write([]byte("d8:announce7:http://"))
		default:
			t.Errorf("未预期请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	body, err := client.DownloadTorrent("6211")
	if err != nil {
		t.Fatalf("下载种子失败: %v", err)
	}
	if string(body) != "d8:announce7:http://" {
		t.Fatalf("种子内容错误: %q", body)
	}
	if len(postedBodies) != 1 || strings.TrimSpace(postedBodies[0]) != "{}" {
		t.Fatalf("授权请求体异常: %v", postedBodies)
	}
	if len(downloadAuthIDs) != 1 || downloadAuthIDs[0] != "77" {
		t.Fatalf("下载未使用授权 ID: %v", downloadAuthIDs)
	}
}

func TestDownloadTorrentRetriesWithReplacementAuthorization(t *testing.T) {
	authorizationCount := 0
	downloadAttempts := 0
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/authorizations"):
			authorizationCount++
			if authorizationCount == 2 {
				var payload map[string]any
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &payload)
				if replaced, ok := payload["replaces_id"].(float64); !ok || int64(replaced) != 77 {
					t.Errorf("第二次授权未带上 replaces_id=77: %s", raw)
				}
			}
			if authorizationCount == 1 {
				_, _ = io.WriteString(w, `{"id":77,"usable":true}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":78,"usable":true}`)
		case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/download"):
			downloadAttempts++
			if downloadAttempts == 1 {
				w.WriteHeader(http.StatusConflict)
				_, _ = io.WriteString(w, `{"error":{"code":"authorization_consumed","message":"authorization 已使用"}}`)
				return
			}
			if got := r.URL.Query().Get("authorization_id"); got != "78" {
				t.Errorf("重试未使用新授权: %s", got)
			}
			_, _ = w.Write([]byte("d4:infod4:name4:teste"))
		default:
			t.Errorf("未预期请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	body, err := client.DownloadTorrent("6211")
	if err != nil {
		t.Fatalf("重试后仍失败: %v", err)
	}
	if string(body) != "d4:infod4:name4:teste" {
		t.Fatalf("种子内容错误: %q", body)
	}
	if authorizationCount != 2 || downloadAttempts != 2 {
		t.Fatalf("重试次数异常: 授权=%d 下载=%d", authorizationCount, downloadAttempts)
	}
}

func TestIssueDownloadAuthorizationRejectsMissingID(t *testing.T) {
	client, closeFn := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":0,"usable":false}`)
	})
	defer closeFn()

	if _, err := client.IssueDownloadAuthorization("6211", 0); err == nil {
		t.Fatal("授权 ID 缺失时应返回错误")
	}
}

// newSessionClient 起一个假的站点并返回会话模式客户端（模拟浏览器 Cookie）。
func newSessionClient(t *testing.T, handler http.HandlerFunc) (*Client, func()) {
	t.Helper()
	server := httptest.NewServer(handler)
	client, err := NewSessionClient(server.URL, "reelcore_session=abc; reelcore_session_marker=1")
	if err != nil {
		t.Fatalf("构造会话客户端失败: %v", err)
	}
	return client, server.Close
}

func TestNewClientWithAuthPrefersSessionCookie(t *testing.T) {
	client, err := NewClientWithAuth("https://www.colapt.com", "rcapi_x", "reelcore_session=abc")
	if err != nil {
		t.Fatalf("构造客户端失败: %v", err)
	}
	// 站点 API Key 没有读权限，同时配了 Cookie 时必须走会话读网页版接口。
	if got := client.AuthMode(); got != "session" {
		t.Fatalf("应优先使用 Cookie 会话，实际 %q", got)
	}
	if _, err := NewClientWithAuth("https://www.colapt.com", "rcapi_x", ""); err != nil {
		t.Fatalf("只配 API Key 时应降级为 Key 模式: %v", err)
	}
	if _, err := NewClientWithAuth("https://www.colapt.com", "", ""); err == nil {
		t.Fatal("两种凭据都为空时应返回错误")
	}
}

func TestSessionClientReadsWebEndpoints(t *testing.T) {
	client, closeFn := newSessionClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/torrents/6211":
			if got := r.Header.Get("Cookie"); !strings.Contains(got, "reelcore_session=abc") {
				t.Errorf("会话模式未带 Cookie: %q", got)
			}
			if got := r.Header.Get("X-API-Key"); got != "" {
				t.Errorf("会话模式不应带 API Key 头: %q", got)
			}
			_, _ = io.WriteString(w, `{"id":6211,"media_id":"rc88","release_name":"沙丘2.2024.2160p","release_scope":null}`)
		case "/api/v1/media/rc88":
			_, _ = io.WriteString(w, `{"media_id":"rc88","category":"movie","countries":["美国"]}`)
		default:
			t.Errorf("未预期请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	if got := client.AuthMode(); got != "session" {
		t.Fatalf("鉴权模式错误: %q", got)
	}
	torrent, err := client.Torrent("6211")
	if err != nil {
		t.Fatalf("会话模式读取详情失败: %v", err)
	}
	if torrent.ReleaseName == "" {
		t.Fatalf("详情解析异常: %+v", torrent)
	}
	media, err := client.Media("rc88")
	if err != nil {
		t.Fatalf("会话模式读取作品失败: %v", err)
	}
	if media.Category != "movie" {
		t.Fatalf("作品分类解析异常: %+v", media)
	}
}

func TestSessionClientDownloadUsesCSRFAndIdempotency(t *testing.T) {
	csrfIssued := 0
	var idempotencyKeys []string
	var origins []string
	client, closeFn := newSessionClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
			csrfIssued++
			_, _ = io.WriteString(w, `{"id":1,"username":"u","session":{"csrf_token":"tok-1","expires_at":"2026-11-01T00:00:00Z"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents/6211/authorizations":
			if got := r.Header.Get("X-CSRF-Token"); got != "tok-1" {
				t.Errorf("CSRF 头错误: %q", got)
			}
			origins = append(origins, r.Header.Get("Origin"))
			key := r.Header.Get("Idempotency-Key")
			if len(key) < 16 {
				t.Errorf("幂等键过短: %q", key)
			}
			idempotencyKeys = append(idempotencyKeys, key)
			_, _ = io.WriteString(w, `{"id":91,"usable":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/torrents/6211/download":
			if got := r.URL.Query().Get("authorization_id"); got != "91" {
				t.Errorf("下载未使用授权 ID: %q", got)
			}
			_, _ = w.Write([]byte("d8:announce7:http://"))
		default:
			t.Errorf("未预期请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	body, err := client.DownloadTorrent("6211")
	if err != nil {
		t.Fatalf("会话模式下载失败: %v", err)
	}
	if string(body) != "d8:announce7:http://" {
		t.Fatalf("种子内容错误: %q", body)
	}
	if csrfIssued != 1 {
		t.Fatalf("CSRF 应只取一次，实际 %d", csrfIssued)
	}
	if len(idempotencyKeys) != 1 || origins[0] == "" {
		t.Fatalf("写请求头缺失: 幂等键=%v Origin=%v", idempotencyKeys, origins)
	}
}

func TestSessionClientRefreshesCSRFOnForbidden(t *testing.T) {
	csrfIssued := 0
	postCount := 0
	var tokens []string
	client, closeFn := newSessionClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/me":
			csrfIssued++
			token := "tok-1"
			if csrfIssued > 1 {
				token = "tok-2"
			}
			_, _ = io.WriteString(w, `{"session":{"csrf_token":"`+token+`","expires_at":"2026-11-01T00:00:00Z"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/torrents/6211/authorizations":
			postCount++
			tokens = append(tokens, r.Header.Get("X-CSRF-Token"))
			if postCount == 1 {
				w.WriteHeader(http.StatusForbidden)
				_, _ = io.WriteString(w, `{"error":{"code":"csrf_token_invalid","message":"csrf"}}`)
				return
			}
			_, _ = io.WriteString(w, `{"id":92,"usable":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/torrents/6211/download":
			_, _ = w.Write([]byte("d4:infod4:name4:teste"))
		default:
			t.Errorf("未预期请求: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	defer closeFn()

	if _, err := client.DownloadTorrent("6211"); err != nil {
		t.Fatalf("CSRF 刷新后仍失败: %v", err)
	}
	if csrfIssued != 2 {
		t.Fatalf("CSRF 刷新次数异常: %d", csrfIssued)
	}
	if len(tokens) != 2 || tokens[0] != "tok-1" || tokens[1] != "tok-2" {
		t.Fatalf("重试未使用新令牌: %v", tokens)
	}
}
