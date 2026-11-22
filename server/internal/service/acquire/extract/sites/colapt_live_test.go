//go:build colaptlive

package sites

import (
	"os"
	"strings"
	"testing"
)

// 人工核对：可乐PT 的 /api/v1/torrents/{id} 能否用浏览器 Cookie 取到完整源数据。
// 用法：
//
//	COLAPT_COOKIE='<整串 Cookie>' COLAPT_TORRENT_ID=28914 \
//	  go test -tags colaptlive ./internal/service/acquire/extract/sites/ -run TestColaptExtractLive -v -count=1
func TestColaptExtractLive(t *testing.T) {
	cookie := strings.TrimSpace(os.Getenv("COLAPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 COLAPT_COOKIE")
	}
	torrentID := strings.TrimSpace(os.Getenv("COLAPT_TORRENT_ID"))
	if torrentID == "" {
		t.Skip("未设置 COLAPT_TORRENT_ID")
	}

	data, err := ExtractColapt(Input{
		SiteCode:     "colapt",
		SiteNickname: "可乐",
		BaseURL:      "https://www.colapt.com",
		Cookie:       cookie,
		TorrentID:    torrentID,
	}, Runtime{})
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	t.Logf("title=%q", data.Title)
	t.Logf("subtitle=%q", data.Subtitle)
	t.Logf("team=%q type=%q resolution=%q source=%q", data.Team, data.Type, data.Resolution, data.Source)
	t.Logf("tags=%v", data.Tags)
	t.Logf("douban=%q imdb=%q tmdb=%q", data.DoubanLink, data.IMDbLink, data.TMDbLink)
	t.Logf("mediainfo_len=%d body_len=%d screenshots_len=%d poster=%v",
		len(data.MediaInfo), len(data.Intro.Body), len(data.Intro.Screenshots), data.Intro.Poster != "")
	if strings.TrimSpace(data.Title) == "" {
		t.Fatal("标题为空")
	}
}
