package sites

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/service/colaptapi"
)

func TestBuildColaptSeedData(t *testing.T) {
	torrent := colaptapi.Torrent{
		ReleaseName:  "沙丘2.Dune.Part.Two.2024.UHD.BluRay.2160p.HEVC.TrueHD.7.1-MTeam",
		Subtitle:     "沙丘2",
		ReleaseGroup: "MTeam",
		Description:  "◎产　　地　美国\n简介正文",
		MediaInfo:    "General\nUnique ID: 1",
		Screenshots:  []string{"https://img.example.com/1.jpg", " ", "https://img.example.com/2.jpg"},
		Tags:         []string{"原盘", "HDR", "1080P", "中字", "1080P", "杜比"},
		Resolution:   "2160P",
		Subtitles:    []string{"zh", "en"},
		ReleaseScope: map[string]any{
			"category": "movie",
		},
	}

	data := buildColaptSeedData(torrent)
	if data.Title != torrent.ReleaseName {
		t.Fatalf("标题异常: %q", data.Title)
	}
	if data.Subtitle != "沙丘2" || data.Team != "MTeam" {
		t.Fatalf("副标题/制作组异常: %+v", data)
	}
	if data.Type != "category.movie" {
		t.Fatalf("类型映射异常: %q", data.Type)
	}
	if data.Resolution != "2160p" {
		t.Fatalf("分辨率归一异常: %q", data.Resolution)
	}
	if data.MediaInfo != torrent.MediaInfo || !strings.Contains(data.Intro.Body, "简介正文") {
		t.Fatalf("MediaInfo/简介异常: %+v", data.Intro)
	}
	wantScreens := "[img]https://img.example.com/1.jpg[/img]\n[img]https://img.example.com/2.jpg[/img]"
	if data.Intro.Screenshots != wantScreens {
		t.Fatalf("截图拼接异常: %q", data.Intro.Screenshots)
	}
	// 1080P 与分辨率重复应被过滤，其余标签保留并去重，字幕码补成标签。
	wantTags := []string{"原盘", "HDR", "中字", "杜比", "英字"}
	if strings.Join(data.Tags, ",") != strings.Join(wantTags, ",") {
		t.Fatalf("标签异常: %v，期望 %v", data.Tags, wantTags)
	}
}

func TestBuildColaptSeedDataTypeMapping(t *testing.T) {
	cases := map[string]string{
		"movie":   "category.movie",
		"tv":      "category.tv_series",
		"anime":   "category.animation",
		"variety": "category.tv_shows",
		"other":   "",
		"":        "",
	}
	for category, want := range cases {
		data := buildColaptSeedData(colaptapi.Torrent{ReleaseScope: map[string]any{"category": category}})
		if data.Type != want {
			t.Fatalf("分类 %q 映射为 %q，期望 %q", category, data.Type, want)
		}
	}
}

func TestApplyColaptMedia(t *testing.T) {
	data := SeedData{}
	applyColaptMedia(&data, colaptapi.Media{
		DoubanID:  "35661174",
		IMDbID:    "tt15239678",
		TMDbURL:   "https://www.themoviedb.org/movie/693134",
		Countries: []string{"美国", "加拿大"},
		PosterURL: "https://img.example.com/poster.jpg",
	})
	if data.DoubanLink != "https://movie.douban.com/subject/35661174/" {
		t.Fatalf("豆瓣链接异常: %q", data.DoubanLink)
	}
	if data.IMDbLink != "https://www.imdb.com/title/tt15239678/" {
		t.Fatalf("IMDb 链接异常: %q", data.IMDbLink)
	}
	if data.TMDbLink != "https://www.themoviedb.org/movie/693134" {
		t.Fatalf("TMDB 链接异常: %q", data.TMDbLink)
	}
	if data.Source != "美国/加拿大" {
		t.Fatalf("产地异常: %q", data.Source)
	}
	if data.Intro.Poster != "[img]https://img.example.com/poster.jpg[/img]" {
		t.Fatalf("海报异常: %q", data.Intro.Poster)
	}
}

// TestApplyColaptMediaTypeFromCategory 验证类型以作品分类为准。
// 依据：契约里电影作品的 release_scope 为 null，仅凭种子详情判不出电影，只能回落 media.category。
func TestApplyColaptMediaTypeFromCategory(t *testing.T) {
	cases := map[string]string{
		"movie":   "category.movie",
		"tv":      "category.tv_series",
		"anime":   "category.animation",
		"variety": "category.tv_shows",
		"other":   "",
	}
	for category, want := range cases {
		// 种子侧 scope 为 null（电影口径），类型必须由作品分类补齐。
		data := buildColaptSeedData(colaptapi.Torrent{ReleaseName: "某片.2024.2160p"})
		if data.Type != "" {
			t.Fatalf("scope 为 null 时不应推断类型，实际 %q", data.Type)
		}
		applyColaptMedia(&data, colaptapi.Media{Category: category})
		if data.Type != want {
			t.Fatalf("作品分类 %q 映射为 %q，期望 %q", category, data.Type, want)
		}
	}
}

func TestApplyColaptMediaKeepsExistingValues(t *testing.T) {
	data := SeedData{
		DoubanLink: "https://movie.douban.com/subject/1/",
		Source:     "中国大陆",
		Intro:      IntroData{Poster: "[img]https://keep.jpg[/img]"},
	}
	applyColaptMedia(&data, colaptapi.Media{
		DoubanID:  "999",
		Countries: []string{"美国"},
		PosterURL: "https://new.jpg",
	})
	if data.DoubanLink != "https://movie.douban.com/subject/1/" || data.Source != "中国大陆" {
		t.Fatalf("已有值被覆盖: %+v", data)
	}
	if data.Intro.Poster != "[img]https://keep.jpg[/img]" {
		t.Fatalf("已有海报被覆盖: %q", data.Intro.Poster)
	}
}

func TestExtractColaptRequiresAPIKey(t *testing.T) {
	if _, err := ExtractColapt(Input{TorrentID: "6211"}, Runtime{}); err == nil {
		t.Fatal("缺少 API Key 时应返回错误")
	}
}

func TestExtractColaptReadsTorrentAndMedia(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "test-key" {
			t.Errorf("API Key 头缺失: %q", got)
		}
		switch r.URL.Path {
		case "/api/v1/automation/torrents/6211":
			_, _ = io.WriteString(w, `{
				"id":6211,"media_id":"rc88",
				"release_name":"沙丘2.Dune.Part.Two.2024.UHD.BluRay.2160p.HEVC.TrueHD.7.1-MTeam",
				"subtitle":"沙丘2","release_group":"MTeam",
				"description":"简介正文","media_info":"General\nUnique ID: 1",
				"screenshots":["https://img.example.com/1.jpg"],
				"tags":["原盘","HDR"],"resolution":"2160p","subtitles":["zh"],
				"release_scope":{"category":"movie"}
			}`)
		case "/api/v1/automation/media/rc88":
			_, _ = io.WriteString(w, `{"media_id":"rc88","douban_id":"35661174","imdb_id":"tt15239678","countries":["美国"],"poster_url":"https://img.example.com/p.jpg"}`)
		default:
			t.Errorf("未预期请求: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	data, err := ExtractColapt(Input{
		SiteCode:  "colapt",
		BaseURL:   server.URL,
		Passkey:   "test-key",
		TorrentID: "https://www.colapt.com/torrents/6211",
	}, Runtime{})
	if err != nil {
		t.Fatalf("提取失败: %v", err)
	}
	if !strings.HasPrefix(data.Title, "沙丘2.") || data.Type != "category.movie" {
		t.Fatalf("种子字段异常: %+v", data)
	}
	if data.DoubanLink == "" || data.Source != "美国" || data.Intro.Poster == "" {
		t.Fatalf("作品字段未补齐: %+v", data)
	}
}

func TestExtractColaptToleratesMediaFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/media/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"code":"media_not_found","message":"missing"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"id":6211,"media_id":"rc88","release_name":"片名.2024.1080p","media_info":"General","release_scope":{"category":"movie"}}`)
	}))
	defer server.Close()

	data, err := ExtractColapt(Input{BaseURL: server.URL, Passkey: "k", TorrentID: "6211"}, Runtime{})
	if err != nil {
		t.Fatalf("作品接口失败不应中断: %v", err)
	}
	if data.Title != "片名.2024.1080p" {
		t.Fatalf("标题异常: %q", data.Title)
	}
}
