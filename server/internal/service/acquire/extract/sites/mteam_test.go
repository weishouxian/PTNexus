package sites

import (
	"testing"

	"github.com/pt-nexus/server/internal/service/mteamapi"
)

// 用馒头 detail 接口真实返回的字段样例，固化 buildMTeamSeedData 的映射口径。
func TestBuildMTeamSeedData(t *testing.T) {
	detail := mteamapi.DetailData{
		ID:         "1280027",
		Name:       "Alice Doesn't Live Here Anymore 1974 2160p UHD BluRay Remux DoVi HDR10 HEVC PCM 1.0-HDS",
		SmallDescr: "再见爱丽斯 / 艾丽丝不在这里 / 艾丽丝不再住在这里 /简繁英字幕|杜比视界",
		Descr:      "◎译　　名　再见爱丽斯\n◎片　　名　Alice Doesn't Live Here Anymore\n◎年　　代　1974\n◎产　　地　美国\n◎简　　介\n　　爱丽丝是一名全职太太……",
		MediaInfo:  "General\nUnique ID : 1",
		Category:   "439",
		Standard:   "6",
		VideoCodec: "16",
		AudioCodec: "14",
		Source:     "4",
		Processing: "2",
		Team:       "",
		IMDb:       "https://www.imdb.com/title/tt0071115/",
		Douban:     "https://movie.douban.com/subject/1296596/",
		LabelsNew:  []string{"中字", "4k", "hdr10", "DoVi"},
		ImageList:  []string{"https://img.example.com/poster.jpg"},
	}

	data := buildMTeamSeedData(detail, Runtime{})

	if data.Title != detail.Name {
		t.Fatalf("标题异常: %q", data.Title)
	}
	if data.Subtitle != detail.SmallDescr {
		t.Fatalf("副标题异常: %q", data.Subtitle)
	}
	if data.MediaInfo != detail.MediaInfo {
		t.Fatalf("MediaInfo 异常: %q", data.MediaInfo)
	}
	if data.IMDbLink != detail.IMDb || data.DoubanLink != detail.Douban {
		t.Fatalf("外链异常: imdb=%q douban=%q", data.IMDbLink, data.DoubanLink)
	}
	if data.Type != "category.movie" {
		t.Fatalf("类型映射异常(439→movie): %q", data.Type)
	}
	if data.Resolution != "2160p" {
		t.Fatalf("分辨率映射异常(6→2160p): %q", data.Resolution)
	}
	if data.VideoCodec != "video.h265" {
		t.Fatalf("视频编码映射异常(16→h265): %q", data.VideoCodec)
	}
	if data.AudioCodec != "audio.lpcm" {
		t.Fatalf("音频编码映射异常(14→lpcm): %q", data.AudioCodec)
	}
	if data.Medium != "medium.remux" {
		t.Fatalf("媒介映射异常(4→remux): %q", data.Medium)
	}
	if data.Source != "source.western" {
		t.Fatalf("产地映射异常(2→western): %q", data.Source)
	}
	// runtime 为空时分段器不走，descr 整体落入正文。
	if data.Intro.Body != detail.Descr {
		t.Fatalf("正文异常: %q", data.Intro.Body)
	}
	// 标签应与 labelsNew 一致。
	wantTags := []string{"中字", "4k", "hdr10", "DoVi"}
	if len(data.Tags) != len(wantTags) {
		t.Fatalf("标签数量异常: %v", data.Tags)
	}
	for i, tag := range wantTags {
		if data.Tags[i] != tag {
			t.Fatalf("标签[%d]异常: %q 期望 %q", i, data.Tags[i], tag)
		}
	}
}

func TestMTeamIDReverseMapping(t *testing.T) {
	cases := []struct {
		name string
		fn   func(string) string
		in   string
		want string
	}{
		{"category电影Remux", mteamCategoryKey, "439", "category.movie"},
		{"category影剧", mteamCategoryKey, "402", "category.tv_series"},
		{"category动画", mteamCategoryKey, "405", "category.animation"},
		{"category未知", mteamCategoryKey, "409", ""},
		{"resolution4K", mteamStandardValue, "6", "2160p"},
		{"resolution1080p", mteamStandardValue, "1", "1080p"},
		{"videoCodecH265", mteamVideoCodecValue, "16", "video.h265"},
		{"videoCodecH264", mteamVideoCodecValue, "1", "video.h264"},
		{"audioCodecLPCM", mteamAudioCodecValue, "14", "audio.lpcm"},
		{"audioCodecTrueHD", mteamAudioCodecValue, "9", "audio.truehd"},
		{"mediumRemux", mteamMediumValue, "4", "medium.remux"},
		{"mediumBluray", mteamMediumValue, "1", "medium.bluray"},
		{"processingWestern", mteamProcessingValue, "2", "source.western"},
		{"processingChina", mteamProcessingValue, "1", "source.china"},
	}
	for _, c := range cases {
		if got := c.fn(c.in); got != c.want {
			t.Errorf("%s: %q → %q，期望 %q", c.name, c.in, got, c.want)
		}
	}
}
