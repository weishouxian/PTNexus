package fetch

import "testing"

// 好学（hxpt）详情页是单数 /torrent/{id}，下载直链是复数 /api/v1/torrents/{id}/download。
// 不能再拼 NexusPHP 的 download.php（站点已改成 Next.js + 自研 JSON API）。
func TestHxptDetailAndDownloadURL(t *testing.T) {
	const baseURL = "https://www.hxpt.org"

	cases := []struct {
		name       string
		torrentID  string
		wantDetail string
		wantDL     string
	}{
		{
			name:       "裸数字 ID",
			torrentID:  "23930",
			wantDetail: "https://www.hxpt.org/torrent/23930",
			wantDL:     "https://www.hxpt.org/api/v1/torrents/23930/download",
		},
		{
			name:       "详情页 URL",
			torrentID:  "https://www.hxpt.org/torrent/23930",
			wantDetail: "https://www.hxpt.org/torrent/23930",
			wantDL:     "https://www.hxpt.org/api/v1/torrents/23930/download",
		},
		{
			name:       "直接粘下载直链",
			torrentID:  "https://www.hxpt.org/api/v1/torrents/23930/download",
			wantDetail: "https://www.hxpt.org/api/v1/torrents/23930/download",
			wantDL:     "https://www.hxpt.org/api/v1/torrents/23930/download",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			detail := buildDetailURL(baseURL, "hxpt", item.torrentID)
			if detail != item.wantDetail {
				t.Fatalf("详情页构造异常：want %q, got %q", item.wantDetail, detail)
			}
			download := buildDirectDownloadURL(baseURL, "hxpt", detail, item.torrentID, "")
			if download != item.wantDL {
				t.Fatalf("下载直链构造异常：want %q, got %q", item.wantDL, download)
			}
		})
	}
}

// 识别不出数字 ID 时不应生成直链，也不应把详情页误当直链。
func TestHxptRejectsUnparsableID(t *testing.T) {
	if got := buildDirectDownloadURL("https://www.hxpt.org", "hxpt", "https://www.hxpt.org/torrent/abc", "abc", ""); got != "" {
		t.Fatalf("无法识别 ID 时应返回空直链，实际 %q", got)
	}
	if got := extractHxptTorrentID("https://www.hxpt.org/torrent/23930"); got != "23930" {
		t.Fatalf("详情页 ID 提取异常: %q", got)
	}
	if got := extractHxptTorrentID("https://www.hxpt.org/api/v1/torrents/9527/download"); got != "9527" {
		t.Fatalf("下载直链 ID 提取异常: %q", got)
	}
	if got := extractHxptTorrentID("23930"); got != "23930" {
		t.Fatalf("裸 ID 提取异常: %q", got)
	}
	if got := extractHxptTorrentID("https://www.hxpt.org/torrent"); got != "" {
		t.Fatalf("无 ID 时应返回空串，实际 %q", got)
	}
}

// 站点判定只认 hxpt，不能把其他 SPA 站误判进来。
func TestIsHxptSource(t *testing.T) {
	if !isHxptSource("hxpt", "", "") {
		t.Fatal("siteCode=hxpt 应命中")
	}
	if !isHxptSource("", "https://www.hxpt.org", "") {
		t.Fatal("baseURL 含 hxpt 应命中")
	}
	if !isHxptSource("", "", "https://www.hxpt.org/torrent/1") {
		t.Fatal("detailURL 含 hxpt 应命中")
	}
	if isHxptSource("rousi", "https://rousi.pro", "https://rousi.pro/torrents/1") {
		t.Fatal("肉丝不应被判成好学")
	}
}
