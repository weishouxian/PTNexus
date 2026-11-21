package downloader

import "testing"

// 自研 API 站（肉丝 PeerGo / 好学 hxpt）的 /api/v1/torrents/{id}/download 必须被认成直链，
// 否则会退回「按详情页解析下载链接」的分支 —— SPA 详情页里没有下载链接，加种必然失败。
func TestLooksLikeDirectDownloadURL(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.hxpt.org/api/v1/torrents/23930/download", true},
		{"https://rousi.pro/api/v1/torrents/10338/download", true},
		{"https://kp.m-team.cc/api/torrent/download/123/abcdef", true},
		{"https://zmpt.cc/download.php?id=585143&passkey=x", true},
		{"https://example.com/a/file.torrent", true},
		// 详情页不算直链：仍需走「从详情页解析」
		{"https://www.hxpt.org/torrent/23930", false},
		{"https://rousi.pro/torrents/10338", false},
		{"https://zmpt.cc/details.php?id=585143", false},
		{"", false},
	}

	for _, item := range cases {
		if got := looksLikeDirectDownloadURL(item.url); got != item.want {
			t.Errorf("looksLikeDirectDownloadURL(%q) = %v, want %v", item.url, got, item.want)
		}
	}
}
