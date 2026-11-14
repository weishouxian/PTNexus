package fetch

import "testing"

// 肉丝（PeerGo）详情页与下载直链都走 /torrents/{数字ID} 形态，不能再拼 NexusPHP 的 download.php。
func TestRousiDetailAndDownloadURL(t *testing.T) {
	const baseURL = "https://rousi.pro"

	cases := []struct {
		name       string
		torrentID  string
		wantDetail string
		wantDL     string
	}{
		{
			name:       "裸数字 ID",
			torrentID:  "10338",
			wantDetail: "https://rousi.pro/torrents/10338",
			wantDL:     "https://rousi.pro/api/v1/torrents/10338/download",
		},
		{
			name:       "详情页 URL",
			torrentID:  "https://rousi.pro/torrents/10338",
			wantDetail: "https://rousi.pro/torrents/10338",
			wantDL:     "https://rousi.pro/api/v1/torrents/10338/download",
		},
		{
			name:       "直接粘下载直链",
			torrentID:  "https://rousi.pro/api/v1/torrents/10338/download",
			wantDetail: "https://rousi.pro/api/v1/torrents/10338/download",
			wantDL:     "https://rousi.pro/api/v1/torrents/10338/download",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			detail := buildDetailURL(baseURL, "rousi", item.torrentID)
			if detail != item.wantDetail {
				t.Fatalf("详情页构造异常：want %q, got %q", item.wantDetail, detail)
			}
			download := buildDirectDownloadURL(baseURL, "rousi", detail, item.torrentID, "")
			if download != item.wantDL {
				t.Fatalf("下载直链构造异常：want %q, got %q", item.wantDL, download)
			}
		})
	}
}

// 旧 UUID 形态的肉丝链接不再生成直链（站点已换成 PeerGo 数字 ID）。
func TestRousiRejectsUnparsableID(t *testing.T) {
	if got := buildDirectDownloadURL("https://rousi.pro", "rousi", "https://rousi.pro/torrents/abc", "abc", ""); got != "" {
		t.Fatalf("无法识别 ID 时应返回空直链，实际 %q", got)
	}
	if got := extractRousiTorrentID("https://rousi.pro/torrents/10338", "10338"); got != "10338" {
		t.Fatalf("ID 提取异常: %q", got)
	}
	if got := extractRousiTorrentID("/api/v1/torrents/9527/download"); got != "9527" {
		t.Fatalf("下载直链 ID 提取异常: %q", got)
	}
}
