//go:build hxptlive

package sites

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// 该测试只用于人工核对好学（www.hxpt.org）的真实接口契约，默认不参与编译：
//
//	HXPT_COOKIE='<浏览器里完整的 Cookie>' go test -tags hxptlive ./internal/service/publish/publisher/sites/ -run TestHXPTLiveDicts -v
//
// 只发 GET（site-profile / section-dict / tags-dict），不会真的发种。
// 期望结果：七个分类都能按名称解析出 id，四个分区字段都能解析出字典 id，
// 任何一个打印「跳过」都说明 configs/hxpt.yaml 的 mappings 名称与站点字典不再一致。
func TestHXPTLiveDicts(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("HXPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 HXPT_COOKIE，跳过好学生产接口核对")
	}

	cfg := loadHXPTConfig()
	session := hxptSession{
		client: &http.Client{Timeout: 30 * time.Second},
		base:   hxptDefaultBase,
		cookie: cookie,
		csrf:   hxptCookieValue(cookie, cfg.API.CSRFCookie),
		ua:     cfg.API.UserAgent,
	}
	if session.csrf == "" {
		t.Fatalf("Cookie 里没有 %s，无法发种", cfg.API.CSRFCookie)
	}

	profile, err := session.getJSON(cfg.API.ProfilePath)
	if err != nil {
		t.Fatalf("读取 site-profile 失败: %v", err)
	}

	// 1) 分类名称 → category_id
	for _, typeKey := range []string{
		"category.movie", "category.tv_series", "category.tv_shows",
		"category.documentaries", "category.animation", "category.sports", "category.music",
	} {
		name := hxptMapValue(cfg, "type", typeKey)
		if name == "" {
			t.Errorf("%s 没有配置分类名称", typeKey)
			continue
		}
		id := hxptDictIDByName(profile["categories"], name)
		if id <= 0 {
			t.Errorf("%s → 分类「%s」在站点分类表里找不到", typeKey, name)
			continue
		}
		t.Logf("%-24s → %s (id=%d)", typeKey, name, id)
	}

	// 2) 电影分类的分区字典：四个 kind 都要能按名称解析出 id
	dict, err := session.getJSON(fmt.Sprintf("%s?category_id=%d", cfg.API.SectionDictPath, 1))
	if err != nil {
		t.Fatalf("读取 section-dict 失败: %v", err)
	}
	sections := map[string]any{}
	hxptSetSection(sections, dict, "media", hxptMapValue(cfg, "media_by_type", "category.movie"))
	hxptSetSection(sections, dict, "codec", hxptMapValue(cfg, "video_codec", "video.h265"))
	hxptSetSection(sections, dict, "source", hxptMapValue(cfg, "source", "medium.webdl"))
	hxptSetSection(sections, dict, "team", hxptMapValue(cfg, "team", "team.hdsky"))
	for _, kind := range []string{"media", "codec", "source", "team"} {
		if int64FromAny(sections[kind], 0) <= 0 {
			t.Errorf("分区字段 %s 没解析出 id（检查 mappings 名称）", kind)
		}
	}
	t.Logf("电影分区字段: %v", sections)

	// 3) 标签字典：标准标签要能映射到站点标签 id
	tagsDict, err := session.getJSON(cfg.API.TagsDictPath)
	if err != nil {
		t.Fatalf("读取 tags-dict 失败: %v", err)
	}
	tagIDs := hxptResolveTagIDs(cfg, tagsDict, map[string]any{
		"tags": []string{"tag.禁转", "tag.中字", "tag.DIY", "tag.国语", "tag.合集", "tag.完结", "tag.独家"},
	})
	if len(tagIDs) < 7 {
		t.Errorf("标签解析不全，期望 7 个，实际 %v", tagIDs)
	}
	t.Logf("标签 id: %v", tagIDs)
}

// TestHXPTLiveUploadContract 用「无效种子」打通一次真实 POST，验证 multipart 结构 / CSRF 头 / Cookie
// 都被站点接受（站点回 3003 种子文件无效 = 请求本身合法，且绝不会创建种子）。
// 期望日志里能看到「分类: …」与「响应状态: 400 code=3003」。
func TestHXPTLiveUploadContract(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("HXPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 HXPT_COOKIE，跳过好学生产接口核对")
	}

	torrentPath := filepath.Join("..", "..", "..", "..", "..", "data", "tmp", "fake.torrent")
	if _, err := os.Stat(torrentPath); err != nil {
		t.Fatalf("找不到用于契约核对的假种子: %v", err)
	}

	_, err := PublishHXPT(publisher.PublishInput{
		TargetName:  "好学",
		SiteCode:    "hxpt",
		BaseURL:     hxptDefaultBase,
		Cookie:      cookie,
		TorrentPath: torrentPath,
		Title:       "PTNexus 契约核对请忽略",
		UploadData: map[string]any{
			"standardized_params": map[string]any{"type": "category.movie"},
		},
	})
	if err == nil {
		t.Fatal("无效种子不应发布成功")
	}
	if !strings.Contains(err.Error(), "3003") && !strings.Contains(err.Error(), "种子文件无效") {
		t.Fatalf("期望站点回 3003 种子文件无效（说明请求结构被接受），实际: %v", err)
	}
	t.Logf("契约核对通过，站点按预期拒绝无效种子: %v", err)
}
