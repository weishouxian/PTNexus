package uploader

import "testing"

// 好学（hxpt）发布后的直链回退逻辑：详情页 /torrent/{id} 应换算成 /api/v1/torrents/{id}/download。
// 这段兜底在发布器没给 directDownloadURL（例如历史记录重试）时生效。
func TestBuildDirectDownloadURLForPublishedHxpt(t *testing.T) {
	cases := []struct {
		name     string
		baseURL  string
		siteCode string
		publish  string
		wantURL  string
	}{
		{
			name:     "详情页换算直链",
			baseURL:  "https://www.hxpt.org",
			siteCode: "hxpt",
			publish:  "https://www.hxpt.org/torrent/23930",
			wantURL:  "https://www.hxpt.org/api/v1/torrents/23930/download",
		},
		{
			name:     "baseURL 带结尾斜杠",
			baseURL:  "https://www.hxpt.org/",
			siteCode: "hxpt",
			publish:  "https://www.hxpt.org/torrent/9527",
			wantURL:  "https://www.hxpt.org/api/v1/torrents/9527/download",
		},
		{
			name:     "识别不出 ID 时不生成直链",
			baseURL:  "https://www.hxpt.org",
			siteCode: "hxpt",
			publish:  "https://www.hxpt.org/torrent/abc",
			wantURL:  "",
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			got := BuildDirectDownloadURLForPublished(item.baseURL, "", item.siteCode, item.publish)
			if got != item.wantURL {
				t.Fatalf("want %q, got %q", item.wantURL, got)
			}
		})
	}
}
