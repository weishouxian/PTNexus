//go:build colaptlive

package fetch

import (
	"os"
	"strings"
	"testing"
)

// 人工核对：可乐PT 作为源站能否真的取到 .torrent（授权申请 + 下载两步）。
// 用法：
//
//	COLAPT_COOKIE='<整串 Cookie>' COLAPT_TORRENT_ID=28914 \
//	  go test -tags colaptlive ./internal/service/acquire/fetch/ -run TestColaptDownloadLive -v -count=1
func TestColaptDownloadLive(t *testing.T) {
	cookie := strings.TrimSpace(os.Getenv("COLAPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 COLAPT_COOKIE")
	}
	torrentID := strings.TrimSpace(os.Getenv("COLAPT_TORRENT_ID"))
	if torrentID == "" {
		t.Skip("未设置 COLAPT_TORRENT_ID")
	}

	info := map[string]any{
		"site":      "colapt",
		"nickname":  "可乐",
		"base_url":  "https://www.colapt.com",
		"cookie":    cookie,
		"passkey":   "",
		"migration": 3,
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
