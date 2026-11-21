//go:build hxptlive

package fetch

import (
	"os"
	"strings"
	"testing"
)

// 人工核对：好学（hxpt）详情页 / 直链能否真的取到种子文件。
// 用法：
//
//	HXPT_COOKIE='<整串 Cookie>' HXPT_TORRENT_ID=23930 go test -tags hxptlive ./internal/service/acquire/fetch/ -run TestHxptDownloadLive -v
func TestHxptDownloadLive(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("HXPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 HXPT_COOKIE")
	}
	torrentID := strings.TrimSpace(os.Getenv("HXPT_TORRENT_ID"))
	if torrentID == "" {
		t.Skip("未设置 HXPT_TORRENT_ID")
	}

	info := map[string]any{
		"site":      "hxpt",
		"nickname":  "好学",
		"base_url":  "https://www.hxpt.org",
		"cookie":    cookie,
		"passkey":   "",
		"migration": 2,
	}

	// 传详情页地址，模拟「自动添加到下载器」时手上有的是 publishURL 的场景。
	detailInput := "https://www.hxpt.org/torrent/" + torrentID
	path, detailURL, body, err := DownloadTorrentForSource(info, detailInput)
	t.Logf("detail_input=%s detail=%s path=%s bytes=%d err=%v", detailInput, detailURL, path, len(body), err)
	if err != nil {
		t.Fatalf("从详情页下载失败: %v", err)
	}
	if len(body) == 0 || !isLikelyTorrent(body) {
		t.Fatalf("下载内容不是 torrent（%d 字节）", len(body))
	}
}
