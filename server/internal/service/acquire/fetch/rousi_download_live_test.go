//go:build rousilive

package fetch

import (
	"os"
	"strings"
	"testing"
)

// 人工核对：肉丝（PeerGo）详情页/下载直链能否真的取到种子文件。
// 用法：ROUSI_COOKIE="__Host-peergo_session=..." go test -tags rousilive ./internal/service/acquire/fetch/ -run TestRousiDownloadLive -v
func TestRousiDownloadLive(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("ROUSI_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 ROUSI_COOKIE")
	}
	torrentID := strings.TrimSpace(os.Getenv("ROUSI_TORRENT_ID"))
	if torrentID == "" {
		t.Skip("未设置 ROUSI_TORRENT_ID")
	}

	info := map[string]any{
		"site":      "rousi",
		"nickname":  "肉丝",
		"base_url":  "https://rousi.pro",
		"cookie":    cookie,
		"passkey":   "",
		"migration": 2,
	}

	path, detailURL, body, err := DownloadTorrentForSource(info, torrentID)
	t.Logf("detail=%s path=%s bytes=%d err=%v", detailURL, path, len(body), err)
	if err != nil {
		t.Fatalf("下载失败: %v", err)
	}
	if len(body) == 0 || !isLikelyTorrent(body) {
		t.Fatalf("下载内容不是 torrent（%d 字节）", len(body))
	}
}
