package publisher

import (
	"strings"
	"testing"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

// chdbitsOfferInput 构造一条彩虹岛电影的候选发布输入（Blu-ray 1080p H.264 / DTS / 国剧 / CHDBits 组 + 中字）。
func chdbitsOfferInput() PublishInput {
	return PublishInput{
		SiteCode:    "chdbits",
		Title:       "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY",
		Subtitle:    "[拳击影子 / The Shadow Boxing]",
		Description: "简介正文\n[img]https://img.example/poster.jpg[/img]",
		IMDbLink:    "https://www.imdb.com/title/tt0079896/",
		UploadData: map[string]any{
			"poster": "[img]https://img.example/poster.jpg[/img]",
			"standardized_params": map[string]any{
				"type":         "category.movie",
				"medium":       "medium.bluray",
				"video_codec":  "video.h264",
				"audio_codec":  "audio.dts",
				"resolution":   "resolution.r1080p",
				"source":       "source.china",
				"team":         "team.chdbits",
				"tags":         []any{"tag.中字"},
				"release_name": "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY",
			},
		},
	}
}

// TestAssembleOfferFormFieldsCHDBits 验证彩虹岛候选「复制发种字段」：
// 分类/媒介/编码/音频/分辨率/地区/制作组/标签 checkbox/固定字段全部复用发种映射，
// 简介走 body、副标题独立落 small_descr（不再拼进名称）、IMDb 落 url；
// 海报栏（picture）按 omit_fields 不提交 —— 站点留空即通过，填了才走 http:// 前缀校验。
func TestAssembleOfferFormFieldsCHDBits(t *testing.T) {
	siteCfg, err := publishmapping.LoadSitePublishConfig("chdbits")
	if err != nil {
		t.Fatalf("加载 chdbits 配置失败: %v", err)
	}
	if !siteCfg.Offer.CopyUploadFields {
		t.Fatal("chdbits.yaml 未开启 offer.copy_upload_fields")
	}

	fields, err := assembleOfferFormFields(chdbitsOfferInput(), siteCfg, nil)
	if err != nil {
		t.Fatalf("assembleOfferFormFields 失败: %v", err)
	}

	want := map[string]string{
		"name":           "The.Shadow.Boxing.1979.1080p.BluRay.x264.LPCM-YzYY",
		"small_descr":    "拳击影子 / The Shadow Boxing",
		"url":            "https://www.imdb.com/title/tt0079896/",
		"type":           "401", // Movies
		"medium_sel":     "1",   // Blu-ray
		"codec_sel":      "1",   // H.264/AVC
		"audiocodec_sel": "3",   // DTS
		"standard_sel":   "1",   // 1080p
		"processing_sel": "8",   // 国剧
		"team_sel":       "14",  // CHDBits
		"source_sel":     "7",   // static_fields：转载
		"cnsub":          "yes", // checkbox_tags：tag.中字
	}
	for key, expected := range want {
		if got := strings.TrimSpace(fields[key]); got != expected {
			t.Errorf("%s = %q, want %q", key, got, expected)
		}
	}

	if !strings.Contains(fields["body"], "简介正文") {
		t.Errorf("body 应含简介正文, got %q", fields["body"])
	}
	// 候选不提交种子文件相关字段；海报栏按 omit_fields 也不提交
	for _, key := range []string{"torrentfile", "file", "descr", "picture"} {
		if _, exists := fields[key]; exists {
			t.Errorf("候选不应提交字段 %q", key)
		}
	}
}

// TestAssembleOfferFormFieldsOmitsPicture 验证 omit_fields 在站点回调之后生效：
// 连 adjust 里补写的海报也要清掉，否则「配置说不填」会被回调悄悄推翻。
func TestAssembleOfferFormFieldsOmitsPicture(t *testing.T) {
	siteCfg, err := publishmapping.LoadSitePublishConfig("chdbits")
	if err != nil {
		t.Fatalf("加载 chdbits 配置失败: %v", err)
	}
	if len(siteCfg.Offer.OmitFields) == 0 {
		t.Fatal("chdbits.yaml 应配置 offer.omit_fields 跳过海报栏")
	}

	adjust := func(_ PublishInput, formFields map[string]string) {
		formFields["picture"] = "https://img.example/poster.jpg"
	}
	fields, err := assembleOfferFormFields(chdbitsOfferInput(), siteCfg, adjust)
	if err != nil {
		t.Fatalf("assembleOfferFormFields 失败: %v", err)
	}
	if value, exists := fields["picture"]; exists {
		t.Errorf("omit_fields 未生效，picture = %q", value)
	}
	if strings.TrimSpace(fields["name"]) == "" {
		t.Error("名称不应被一并删除")
	}
}

// TestOfferOmittedFieldNames 验证 omit_fields 归一：逻辑名按 offer.fields 换算成站点字段名、
// 去空去重；未声明时返回 nil（调用方据此跳过日志）。
func TestOfferOmittedFieldNames(t *testing.T) {
	cases := []struct {
		name string
		cfg  publishmapping.SiteOfferConfig
		want []string
	}{
		{
			name: "逻辑名按 fields 换算成站点字段名",
			cfg:  publishmapping.SiteOfferConfig{OmitFields: []string{"picture"}, Fields: map[string]string{"picture": "image"}},
			want: []string{"image"},
		},
		{
			name: "未声明映射时按原字段名删除",
			cfg:  publishmapping.SiteOfferConfig{OmitFields: []string{"picture"}},
			want: []string{"picture"},
		},
		{
			name: "去空去重",
			cfg:  publishmapping.SiteOfferConfig{OmitFields: []string{"picture", " picture ", "", "url", "url"}},
			want: []string{"picture", "url"},
		},
		{
			name: "无声明返回 nil",
			cfg:  publishmapping.SiteOfferConfig{},
			want: nil,
		},
	}
	for _, tc := range cases {
		got := offerOmittedFieldNames(tc.cfg)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%s: offerOmittedFieldNames() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestAssembleOfferFormFieldsCopyDisabled 验证精简型候选页（TTG 那种只有 5 个字段的表单）
// 只提交分类，不把媒介/编码/分辨率/地区/制作组/标签塞过去 —— 那些控件在页面上不存在，
// 提交会被站点当成非法参数或直接忽略，反而干扰排查。
func TestAssembleOfferFormFieldsCopyDisabled(t *testing.T) {
	siteCfg, err := publishmapping.LoadSitePublishConfig("chdbits")
	if err != nil {
		t.Fatalf("加载 chdbits 配置失败: %v", err)
	}
	if !siteCfg.Offer.CopyUploadFields {
		t.Fatal("chdbits.yaml 默认应开启 copy_upload_fields，测试前提不成立")
	}

	// 复用同一套映射，只把「复制发种字段」关掉，等价于 TTG 的精简型候选页
	slimCfg := *siteCfg
	slimCfg.Offer = siteCfg.Offer
	slimCfg.Offer.CopyUploadFields = false

	fields, err := assembleOfferFormFields(chdbitsOfferInput(), &slimCfg, nil)
	if err != nil {
		t.Fatalf("assembleOfferFormFields 失败: %v", err)
	}

	if got, want := strings.TrimSpace(fields["type"]), "401"; got != want {
		t.Errorf("type = %q, want %q（精简型候选页仍应提交分类）", got, want)
	}
	for _, key := range []string{"medium_sel", "codec_sel", "audiocodec_sel", "standard_sel", "processing_sel", "team_sel", "source_sel", "cnsub"} {
		if _, exists := fields[key]; exists {
			t.Errorf("精简型候选页不应提交维度字段 %q", key)
		}
	}
}

// TestApplyOfferPictureScheme 验证海报协议前缀改写：
// 彩虹岛候选端点的 picture 校验只认 http://，https:// 会被回「图片必须是jpg, gif 或png格式。」
// （站点只跑正则、不回源检查），所以站点声明 picture_scheme 时要把 https 降级成 http；
// 未声明（空串）的站点（TTG 等）必须原样保留，不能顺手改别人的地址。
func TestApplyOfferPictureScheme(t *testing.T) {
	cases := []struct {
		name   string
		pic    string
		scheme string
		want   string
	}{
		{"降级 https→http", "https://img.seedvault.cn/i/2026/11/23/abc.jpg", "http", "http://img.seedvault.cn/i/2026/11/23/abc.jpg"},
		{"已是 http 不重复改写", "http://img.seedvault.cn/a.jpg", "http", "http://img.seedvault.cn/a.jpg"},
		{"未声明站点原样保留", "https://img.example/poster.jpg", "", "https://img.example/poster.jpg"},
		{"反向升级 http→https", "http://img.example/poster.jpg", "https", "https://img.example/poster.jpg"},
		{"非 http(s) 地址不改写", "/pic/poster.jpg", "http", "/pic/poster.jpg"},
		{"空地址返回空串", "   ", "http", ""},
		{"大小写协议也能识别", "HTTPS://img.example/poster.jpg", "http", "http://img.example/poster.jpg"},
	}
	for _, tc := range cases {
		if got := applyOfferPictureScheme(tc.pic, tc.scheme); got != tc.want {
			t.Errorf("%s: applyOfferPictureScheme(%q, %q) = %q, want %q", tc.name, tc.pic, tc.scheme, got, tc.want)
		}
	}
}

// TestAssembleOfferFormFieldsRequiresName 验证名称为空时返回明确错误（站点会直接拒收）。
func TestAssembleOfferFormFieldsRequiresName(t *testing.T) {
	input := chdbitsOfferInput()
	input.Title = ""
	input.Subtitle = ""

	if _, err := assembleOfferFormFields(input, nil, nil); err == nil {
		t.Fatal("名称为空时应返回错误")
	}
}
