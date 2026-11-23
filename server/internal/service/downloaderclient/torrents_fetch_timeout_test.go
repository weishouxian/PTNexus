package downloaderclient

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// proxyDownloaderFor 构造一个走盒子代理的 qB 下载器，指向本地假代理。
// 参数/返回：hostPort 为假代理监听端口（Host 里只用来取 IP，真实请求打到 ProxyPort）。
func proxyDownloaderFor(port int) Downloader {
	return Downloader{
		ID:        "test-qb",
		Name:      "test",
		Type:      "qbittorrent",
		Host:      "http://127.0.0.1:8181/",
		UseProxy:  true,
		ProxyPort: port,
	}
}

// 代理正常返回时 FetchTorrentsWithin 应完成并归一化快照。
func TestFetchTorrentsWithinSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/torrents/all" {
			t.Errorf("path = %q, want /api/torrents/all", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"hash":"abc123","name":"Demo","save_path":"/data/demo","content_path":"/data/demo/file.mkv","size":1024,"progress":1,"state":"uploading","comment":"https://www.colapt.com/torrents/1"}]`))
	}))
	defer server.Close()

	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	items, err := proxyDownloaderFor(port).FetchTorrentsWithin(5 * time.Second)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(items) != 1 || items[0].Hash != "abc123" || items[0].Name != "Demo" {
		t.Fatalf("items = %+v, want 1 条 abc123/Demo", items)
	}
	if items[0].ContentPath != "/data/demo/file.mkv" {
		t.Errorf("ContentPath = %q", items[0].ContentPath)
	}
}

// 代理不响应时必须在给定超时内返回错误，而不是把调用方（截图修复链路）挂死。
func TestFetchTorrentsWithinTimeout(t *testing.T) {	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		w.WriteHeader(http.StatusOK)
	}))
	defer func() {
		close(block)
		server.Close()
	}()

	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}

	startedAt := time.Now()
	_, err = proxyDownloaderFor(port).FetchTorrentsWithin(300 * time.Millisecond)
	elapsed := time.Since(startedAt)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("err = %v, want 含「超时」", err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("elapsed = %s，超时未生效", elapsed)
	}
}

// 截图链路同一批种子会反复问 save_path，命中缓存时只应打一次代理。
func TestFetchTorrentsCachedReusesResult(t *testing.T) {
	InvalidateTorrentSnapshotCache()
	t.Cleanup(InvalidateTorrentSnapshotCache)

	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"hash":"hashA","name":"A","save_path":"/data/a","content_path":"/data/a/a.mkv"}]`))
	}))
	defer server.Close()

	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	downloader := proxyDownloaderFor(port)

	for i := 0; i < 3; i++ {
		items, err := downloader.FetchTorrentsCached()
		if err != nil {
			t.Fatalf("call %d err: %v", i+1, err)
		}
		if len(items) != 1 || items[0].Hash != "hashA" {
			t.Fatalf("call %d items = %+v", i+1, items)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("proxy 调用次数 = %d, want 1（应命中缓存）", got)
	}

	InvalidateTorrentSnapshotCache()
	if _, err := downloader.FetchTorrentsCached(); err != nil {
		t.Fatalf("after invalidate err: %v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("清缓存后调用次数 = %d, want 2", got)
	}
}
