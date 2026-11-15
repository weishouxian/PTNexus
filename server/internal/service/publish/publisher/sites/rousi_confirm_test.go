package sites

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newRousiConfirmServer 模拟 PeerGo 的列表与详情接口：只有 targetID 命中给定 infohash。
func newRousiConfirmServer(t *testing.T, baselineID int64, targetID int64, targetHash string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/torrents" {
			json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{"id": baselineID}},
			})
			return
		}
		prefix := "/api/v1/torrents/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, prefix)
		if id == "10344" && targetID == 10344 {
			json.NewEncoder(w).Encode(map[string]any{
				"id":           targetID,
				"info_hash_v1": targetHash,
				"state":        "pending_review",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"code":"torrent_not_found"}`))
	}))
}

// TestRousiConfirmUploadedFindsTorrent 验证 500 后能按 infohash 反查到已落库的种子。
func TestRousiConfirmUploadedFindsTorrent(t *testing.T) {
	const hash = "ca31675bea0e17f318000729affe94636cd1c0d0"
	srv := newRousiConfirmServer(t, 10340, 10344, hash)
	defer srv.Close()

	id, state, ok := rousiConfirmUploaded(srv.Client(), srv.URL, "cookie", "ua", hash, 10340)
	if !ok || id != 10344 || state != "pending_review" {
		t.Fatalf("回查结果不符合预期: id=%d state=%s ok=%v", id, state, ok)
	}
}

// TestRousiConfirmUploadedNotLanded 验证没落库时不会误判成功。
func TestRousiConfirmUploadedNotLanded(t *testing.T) {
	srv := newRousiConfirmServer(t, 10340, 10344, "ca31675bea0e17f318000729affe94636cd1c0d0")
	defer srv.Close()

	if _, _, ok := rousiConfirmUploaded(srv.Client(), srv.URL, "cookie", "ua", "aaaabbbbcccc", 10340); ok {
		t.Fatal("infohash 不匹配时不应判定为已落库")
	}
	// 基线缺失（列表接口不可用）时直接放弃回查，避免全站扫描
	if _, _, ok := rousiConfirmUploaded(srv.Client(), srv.URL, "cookie", "ua", "aaaabbbbcccc", 0); ok {
		t.Fatal("基线为 0 时不应回查")
	}
}

// TestRousiMaxPublicTorrentID 验证取列表最大 id（列表可能不按 id 排序）。
func TestRousiMaxPublicTorrentID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{"id": 10338}, {"id": 10340}, {"id": 10339}},
		})
	}))
	defer srv.Close()

	if got := rousiMaxPublicTorrentID(srv.Client(), srv.URL, "cookie", "ua"); got != 10340 {
		t.Fatalf("最大 id 解析错误: %d", got)
	}
}
