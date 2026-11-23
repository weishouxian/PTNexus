package mapping

import "testing"

func TestLoadWintersakuraConfig(t *testing.T) {
	cfg, err := LoadSitePublishConfig("wintersakura")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.UploadFileField != "file" {
		t.Errorf("UploadFileField=%q", cfg.UploadFileField)
	}
	wantFields := map[string]string{
		"category":       "type",
		"medium":         "medium_sel[4]",
		"video_codec":    "codec_sel[4]",
		"audio_codec":    "audiocodec_sel[4]",
		"resolution":     "standard_sel[4]",
		"team":           "team_sel[4]",
		"description":    "descr",
		"imdb_url":       "url",
		"pt_gen":         "pt_gen",
		"technical_info": "technical_info",
		"tags[4][]":      "tags[4][]",
	}
	for key, want := range wantFields {
		if got := cfg.FormFields[key]; got != want {
			t.Errorf("FormFields[%q]=%q want %q", key, got, want)
		}
	}

	// 站点真实选项断言：每个映射值都必须落在抓取到的 select/checkbox 取值集合里
	allowed := map[string]map[string]bool{
		"type":        {"401": true, "402": true, "403": true, "406": true, "407": true, "408": true, "409": true, "410": true, "413": true, "414": true, "418": true, "422": true, "423": true, "424": true, "425": true, "426": true, "427": true, "428": true},
		"medium":      {"10": true, "11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true, "18": true, "21": true, "22": true, "23": true, "24": true, "25": true},
		"video_codec": {"6": true, "7": true, "8": true, "9": true, "10": true, "11": true, "13": true, "15": true, "16": true},
		"audio_codec": {"8": true, "9": true, "10": true, "11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true, "18": true, "19": true, "20": true, "21": true, "22": true, "23": true, "24": true, "25": true, "26": true, "27": true},
		"resolution":  {"1": true, "2": true, "3": true, "4": true, "5": true, "6": true},
		"team":        {"1": true, "2": true, "5": true, "6": true, "7": true, "8": true, "9": true, "11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true, "18": true, "19": true},
		"tag":         {"1": true, "2": true, "4": true, "5": true, "6": true, "7": true, "8": true, "9": true, "10": true, "11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true, "18": true, "19": true, "20": true, "21": true, "22": true},
	}
	for dim, values := range allowed {
		m, ok := cfg.Mappings[dim]
		if !ok {
			t.Fatalf("mappings.%s missing", dim)
		}
		for key, value := range m {
			if key == "default" {
				continue
			}
			if !values[value] {
				if dim == "tag" && value == "" {
					continue
				}
				t.Errorf("mappings.%s[%q]=%q 不在站点真实选项集合内", dim, key, value)
			}
		}
	}

	// 标准值必须能命中（不能静默回落 default）
	cases := [][3]string{
		{"type", "category.movie", "401"},
		{"type", "category.tv_series", "414"},
		{"type", "category.animation", "423"},
		{"type", "category.tv_shows", "403"},
		{"type", "category.documentaries", "410"},
		{"medium", "medium.uhd_bluray", "10"},
		{"medium", "medium.remux", "14"},
		{"medium", "medium.webdl", "21"},
		{"video_codec", "video.h265", "9"},
		{"video_codec", "video.x265", "7"},
		{"audio_codec", "audio.truehd_atmos", "10"},
		{"audio_codec", "audio.dts_hd_ma", "8"},
		{"resolution", "resolution.r2160p", "5"},
		{"team", "team.chd", "2"},
		{"tag", "tag.国语", "5"},
		{"tag", "tag.HDR10+", "10"},
	}
	for _, c := range cases {
		got := PickMappedValueWithFallback(c[0], cfg.Mappings[c[0]], c[1])
		if got != c[2] {
			t.Errorf("%s 映射 %q 得到 %q，期望 %q", c[0], c[1], got, c[2])
		}
	}

	// dupe 校验参数（站点搜索页实测：search_area 无豆瓣范围、筛选只有 cat{N}）
	if !cfg.DupeCheck.Enabled {
		t.Error("dupe_check.enabled 未生效")
	}
	if cfg.DupeCheck.SearchAreaParam != "search_area" {
		t.Errorf("SearchAreaParam=%q", cfg.DupeCheck.SearchAreaParam)
	}
	if got := cfg.DupeCheck.SearchAreas["imdb"]; got != "4" {
		t.Errorf("SearchAreas[imdb]=%q", got)
	}
	if _, exists := cfg.DupeCheck.SearchAreas["douban"]; exists {
		t.Error("站点无豆瓣搜索范围，不应配置 SearchAreas[douban]")
	}
	if got := cfg.DupeCheck.ParamTemplates["type"]; got != "cat{value}" {
		t.Errorf("ParamTemplates[type]=%q", got)
	}
}
