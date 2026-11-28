package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSitePatchTestRepo(t *testing.T) *SiteRepository {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB failed: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	create := `CREATE TABLE sites (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		nickname TEXT, site TEXT, base_url TEXT, special_tracker_domain TEXT,
		"group" TEXT, description TEXT, cookie TEXT, passkey TEXT,
		speed_limit INTEGER DEFAULT 0, ratio_threshold REAL DEFAULT 3,
		seed_speed_limit INTEGER DEFAULT 5, can_publish INTEGER DEFAULT 1,
		forbidden_transfer_sites TEXT, dupe_check_enabled INTEGER DEFAULT 0,
		dupe_size_tolerance_bytes INTEGER DEFAULT 0, dupe_rules TEXT,
		audio_track_policy INTEGER DEFAULT 0, tags TEXT, sort_order INTEGER DEFAULT 0,
		check_cookie_enabled INTEGER DEFAULT 1, publish_mode INTEGER DEFAULT 0
	)`
	if err := db.Exec(create).Error; err != nil {
		t.Fatalf("create sites failed: %v", err)
	}
	return NewSiteRepository(&Store{DB: db, DBType: "sqlite"})
}

// TestPatchSiteDetailsKeepsUntouchedColumns 验证「站点数据页就地编辑」是部分更新：
// 只写提交上来的白名单字段，Dupe 规则 / 标签 / 禁转名单 / 描述等「站点管理」页维护的配置必须原样保留。
// 这条是核心回归点 —— 换成全量 UPDATE（UpdateSiteDetails）就会把它们写成空值。
func TestPatchSiteDetailsKeepsUntouchedColumns(t *testing.T) {
	repo := newSitePatchTestRepo(t)
	insert := `INSERT INTO sites
		(nickname, site, base_url, description, cookie, passkey, dupe_rules, tags, forbidden_transfer_sites, can_publish, sort_order)
		VALUES ('原名称', 'demo', 'old.example', '不要覆盖我', 'old-cookie=1', 'old-passkey',
		'[{"medium":"Blu-ray","params":["resolution"]}]', '["测试标签"]', '["hdsky"]', 1, 5)`
	if err := repo.store.DB.Exec(insert).Error; err != nil {
		t.Fatalf("insert site failed: %v", err)
	}

	updated, err := repo.PatchSiteDetails("demo", map[string]any{
		"site":                 "demo",
		"nickname":             "新名称",
		"base_url":             "https://new.example",
		"cookie":               "new-cookie=2",
		"passkey":              "new-passkey",
		"can_publish":          false,
		"check_cookie_enabled": true,
		"publish_mode":         1,
		"sort_order":           7,
	})
	if err != nil {
		t.Fatalf("patch failed: %v", err)
	}
	if !updated {
		t.Fatalf("已存在的站点应返回 true")
	}

	detail, err := repo.GetSiteDetail("demo")
	if err != nil || detail == nil {
		t.Fatalf("读取站点详情失败: %v", err)
	}
	if got := toString(detail["nickname"], ""); got != "新名称" {
		t.Errorf("nickname 应更新为「新名称」，实际 %q", got)
	}
	if got := toString(detail["base_url"], ""); got != "https://new.example" {
		t.Errorf("base_url 未更新: %q", got)
	}
	if got := toString(detail["cookie"], ""); got != "new-cookie=2" {
		t.Errorf("cookie 未更新: %q", got)
	}
	if got := toIntWithDefault(detail["can_publish"], 1); got != 0 {
		t.Errorf("can_publish 应写入 0，实际 %d", got)
	}
	if got := toIntWithDefault(detail["publish_mode"], 0); got != 1 {
		t.Errorf("publish_mode 应写入 1（候选申请），实际 %d", got)
	}
	if got := toIntWithDefault(detail["sort_order"], 0); got != 7 {
		t.Errorf("sort_order 应写入 7，实际 %d", got)
	}

	// 未提交的列必须保持原值（全量 UPDATE 会把这些写成空/默认值）。
	for column, want := range map[string]string{
		"description":              "不要覆盖我",
		"dupe_rules":               `[{"medium":"Blu-ray","params":["resolution"]}]`,
		"tags":                     `["测试标签"]`,
		"forbidden_transfer_sites": `["hdsky"]`,
	} {
		if got := toString(detail[column], ""); got != want {
			t.Errorf("未提交的列 %s 被改动: 期望 %q，实际 %q", column, want, got)
		}
	}
}

// TestPatchSiteDetailsEdgeCases 验证边界：没有可更新字段按请求错误返回；站点不存在返回 false。
func TestPatchSiteDetailsEdgeCases(t *testing.T) {
	repo := newSitePatchTestRepo(t)
	if err := repo.store.DB.Exec(
		`INSERT INTO sites (nickname, site, base_url) VALUES ('原名称', 'demo', 'old.example')`,
	).Error; err != nil {
		t.Fatalf("insert site failed: %v", err)
	}

	// 只带了站点标识（白名单里没有 site 列）⇒ 没有可更新字段。
	if _, err := repo.PatchSiteDetails("demo", map[string]any{"site": "demo"}); !errors.Is(err, ErrNoPatchField) {
		t.Errorf("无白名单字段时应返回 ErrNoPatchField，实际 %v", err)
	}

	updated, err := repo.PatchSiteDetails("missing", map[string]any{"nickname": "x"})
	if err != nil {
		t.Fatalf("站点不存在不应报错，实际 %v", err)
	}
	if updated {
		t.Errorf("站点不存在应返回 false")
	}

	if _, err := repo.GetSiteDetail("  "); err == nil {
		t.Errorf("空站点标识应报错")
	}
	if detail, err := repo.GetSiteDetail("missing"); err != nil || detail != nil {
		t.Errorf("站点不存在应返回 nil, nil，实际 %v, %v", detail, err)
	}
}
