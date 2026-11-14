package sites

import (
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

func TestRousiResolveCookieHeader(t *testing.T) {
	cases := []struct {
		name    string
		passkey string
		want    string
	}{
		{name: "裸 session 值", passkey: "abc123", want: "__Host-peergo_session=abc123"},
		{name: "完整 session 键值", passkey: "__Host-peergo_session=abc123", want: "__Host-peergo_session=abc123"},
		{name: "整段 Cookie 串", passkey: "foo=1; __Host-peergo_session=abc123", want: "foo=1; __Host-peergo_session=abc123"},
		{name: "带 Cookie 前缀", passkey: "Cookie: __Host-peergo_session=abc123", want: "__Host-peergo_session=abc123"},
		{name: "空值", passkey: "  ", want: ""},
	}

	for _, tc := range cases {
		if got := rousiResolveCookieHeader(tc.passkey); got != tc.want {
			t.Fatalf("%s: 期望 %q，实际 %q", tc.name, tc.want, got)
		}
	}
}

func TestResolveRousiCategory(t *testing.T) {
	cases := map[string]string{
		"category.movie":         "movies",
		"category.animation":     "anime",
		"category.tv_series":     "tv",
		"category.tv_shows":      "variety",
		"category.documentaries": "documentary",
		"category.ebook":         "ebooks",
		"category.game":          "other",
		"category.sports":        "sports",
		"动漫":                     "anime",
	}

	for raw, want := range cases {
		if got := resolveRousiCategory(raw, nil); got != want {
			t.Fatalf("%s: 期望 %q，实际 %q", raw, want, got)
		}
	}
}

func TestInferRousiReleaseFacetSelections(t *testing.T) {
	facets := []rousiFacet{
		{ID: "source-medium", Options: []rousiFacetOption{
			{Key: "blu-ray", Label: "Blu-ray"},
			{Key: "uhd-blu-ray", Label: "UHD Blu-ray"},
			{Key: "other", Label: "其它"},
		}},
		{ID: "release-type", Options: []rousiFacetOption{
			{Key: "remux", Label: "Remux"},
			{Key: "web-dl", Label: "WEB-DL"},
		}},
	}

	result := inferRousiReleaseFacetSelections(facets, "Some.Movie.2024.2160p.UHD.BluRay.REMUX.HDR.HEVC-TEST")
	if got := result["source-medium"]; len(got) == 0 || got[0] != "uhd-blu-ray" {
		t.Fatalf("来源推断错误: %#v", result)
	}
	if got := result["release-type"]; len(got) == 0 || got[0] != "remux" {
		t.Fatalf("发布类型推断错误: %#v", result)
	}

	// 站点没有该选项时不应提交非法 key。
	narrow := []rousiFacet{{ID: "source-medium", Options: []rousiFacetOption{{Key: "blu-ray", Label: "Blu-ray"}}}}
	if got := inferRousiReleaseFacetSelections(narrow, "Some.2160p.UHD.BluRay.REMUX-TEST"); len(got) != 0 {
		t.Fatalf("应丢弃站点不支持的选项，实际 %#v", got)
	}
}

func TestBuildRousiFacetSelections(t *testing.T) {
	facets := []rousiFacet{
		{ID: "genre", Name: "类型", Required: true, SelectionMode: "multi_option", Options: []rousiFacetOption{
			{Key: "剧情", Label: "剧情"},
			{Key: "动作", Label: "动作"},
			{Key: "其它", Label: "其它"},
		}},
		{ID: "region", Name: "地区", Required: true, SelectionMode: "single_option", Options: []rousiFacetOption{
			{Key: "mainland-china", Label: "大陆"},
			{Key: "other", Label: "其它"},
		}},
		{ID: "resolution", Name: "分辨率", Required: true, SelectionMode: "single_option", Options: []rousiFacetOption{
			{Key: "2160p", Label: "4K / 2160p"},
			{Key: "1080p", Label: "1080p"},
			{Key: "other", Label: "其它"},
		}},
		{ID: "source-medium", Name: "来源", RequirementGroup: "source", Options: []rousiFacetOption{
			{Key: "uhd-blu-ray", Label: "UHD Blu-ray"},
			{Key: "other", Label: "其它"},
		}},
		{ID: "release-type", Name: "发布类型", RequirementGroup: "source", Options: []rousiFacetOption{
			{Key: "remux", Label: "Remux"},
		}},
	}

	siteCfg := &publishmapping.SitePublishConfig{
		Mappings: map[string]map[string]string{
			"region":     {"source.china": "mainland-china"},
			"resolution": {"resolution.r2160p": "2160p"},
		},
	}
	uploadData := map[string]any{
		"tags": []any{"tag.剧情", "tag.动作", "tag.科幻"},
		"standardized_params": map[string]any{
			"region":     "source.china",
			"resolution": "resolution.r2160p",
		},
	}

	selections, summary, err := buildRousiFacetSelections("movies", facets, uploadData, uploadData["standardized_params"].(map[string]any), siteCfg, "Movie.2024.2160p.UHD.BluRay.REMUX-TEST")
	if err != nil {
		t.Fatalf("构造维度失败: %v", err)
	}

	got := map[string][]string{}
	for _, selection := range selections {
		got[selection.FacetID] = selection.OptionKeys
	}
	if keys := got["genre"]; len(keys) != 2 || keys[0] != "剧情" || keys[1] != "动作" {
		t.Fatalf("题材映射错误: %#v", got["genre"])
	}
	if keys := got["region"]; len(keys) != 1 || keys[0] != "mainland-china" {
		t.Fatalf("产地映射错误: %#v", got["region"])
	}
	if keys := got["resolution"]; len(keys) != 1 || keys[0] != "2160p" {
		t.Fatalf("分辨率映射错误: %#v", got["resolution"])
	}
	if keys := got["source-medium"]; len(keys) != 1 || keys[0] != "uhd-blu-ray" {
		t.Fatalf("来源映射错误: %#v", got["source-medium"])
	}
	if summary == "" {
		t.Fatalf("缺少映射摘要")
	}

	// 产地无法映射时必须兜底到 other，不能留空导致站点拒绝。
	empty := map[string]any{"standardized_params": map[string]any{"region": "source.unknown", "resolution": "resolution.r9999p"}}
	selections, _, err = buildRousiFacetSelections("movies", facets, empty, empty["standardized_params"].(map[string]any), siteCfg, "Movie.2024.1080p.WEB-DL-TEST")
	if err != nil {
		t.Fatalf("兜底分支不应失败: %v", err)
	}
	fallback := map[string][]string{}
	for _, selection := range selections {
		fallback[selection.FacetID] = selection.OptionKeys
	}
	if keys := fallback["region"]; len(keys) != 1 || keys[0] != "other" {
		t.Fatalf("产地兜底错误: %#v", fallback["region"])
	}
	if keys := fallback["resolution"]; len(keys) != 1 || keys[0] != "other" {
		t.Fatalf("分辨率兜底错误: %#v", fallback["resolution"])
	}
}

func TestBuildRousiMultipartBody(t *testing.T) {
	body, contentType, err := buildRousiMultipartBody(rousiMultipartInput{
		CategoryID:  "movies",
		Title:       "Movie.2024.2160p.UHD.BluRay.REMUX-TEST",
		Subtitle:    "测试副标题",
		Description: "**简介**",
		MediaInfo:   "General\ncomplete name: test.mkv",
		Anonymous:   false,
		ExternalIDs: map[string]string{"imdb_id": "tt0111161"},
		Selections: []rousiFacetSelection{
			{FacetID: "genre", OptionKeys: []string{"剧情"}},
			{FacetID: "source-medium", OptionKeys: []string{"uhd-blu-ray"}},
		},
		Screenshots:  []rousiScreenshot{{Name: "screenshot-1.jpg", Data: []byte{0xFF, 0xD8, 0xFF, 0xD9}}},
		TorrentName:  "demo.torrent",
		TorrentBytes: []byte("d4:infod4:name1:xee"),
	})
	if err != nil {
		t.Fatalf("构造 multipart 失败: %v", err)
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/form-data") {
		t.Fatalf("Content-Type 异常: %q err=%v", contentType, err)
	}

	reader := multipart.NewReader(body, params["boundary"])
	fields := map[string]string{}
	facetParts := []string{}
	fileParts := []string{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("读取 part 失败: %v", err)
		}
		data, _ := io.ReadAll(part)
		switch part.FormName() {
		case "facet_selections":
			payload := rousiFacetSelection{}
			if err := json.Unmarshal(data, &payload); err != nil {
				t.Fatalf("facet_selections 不是合法 JSON: %v", err)
			}
			facetParts = append(facetParts, part.FileName()+":"+payload.FacetID)
		case "screenshots", "torrent_file":
			fileParts = append(fileParts, part.FormName()+":"+part.FileName())
		default:
			fields[part.FormName()] = string(data)
		}
	}

	if fields["category_id"] != "movies" || fields["anonymous"] != "false" || fields["imdb_id"] != "tt0111161" {
		t.Fatalf("基础字段异常: %#v", fields)
	}
	if len(facetParts) != 2 || facetParts[0] != "facet-selection-1.json:genre" {
		t.Fatalf("facet part 异常: %#v", facetParts)
	}
	if len(fileParts) != 2 || fileParts[1] != "torrent_file:demo.torrent" {
		t.Fatalf("文件 part 异常: %#v", fileParts)
	}
}
