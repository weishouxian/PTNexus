package siteprofile

import (
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newKeepAliveTestService 建一个内存 sqlite 的站点数据服务（sites + site_profiles + 日快照）。
func newKeepAliveTestService(t *testing.T) (*Service, *repository.SiteProfileRepository) {
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
	if err := db.Exec(`INSERT INTO sites (site, nickname, base_url, cookie, sort_order) VALUES (?,?,?,?,?)`,
		"hdsky", "天空", "https://hdsky.me", "c_secure_uid=x", 1).Error; err != nil {
		t.Fatalf("seed site failed: %v", err)
	}
	store := &repository.Store{DB: db, DBType: "sqlite"}
	profileRepo := repository.NewSiteProfileRepository(store)
	if err := profileRepo.Migrate(); err != nil {
		t.Fatalf("migrate profile failed: %v", err)
	}
	dailyRepo := repository.NewSiteProfileDailyRepository(store)
	if err := dailyRepo.Migrate(); err != nil {
		t.Fatalf("migrate daily failed: %v", err)
	}
	return NewService(repository.NewSiteRepository(store), profileRepo, dailyRepo), profileRepo
}

// TestSetKeepAliveLevelFlow 验证手动保号等级的完整链路：
// 保存 → 列表里保号标记跟着走，而「自动识别结果」保持不变；非法值/未知站点要有明确错误。
func TestSetKeepAliveLevelFlow(t *testing.T) {
	svc, profileRepo := newKeepAliveTestService(t)
	levels := []UserLevel{
		{Name: "Veteran User", Order: 6},
		{Name: "影帝(Ultimate User)", Order: 8, KeepAlive: true},
		{Name: "终身影帝(Nexus Master)", Order: 9},
	}
	if err := profileRepo.Upsert(repository.SiteProfile{
		SiteCode:   "hdsky",
		Nickname:   "天空",
		UserClass:  "Power User",
		Status:     "ok",
		LevelsJSON: encodeLevelsCache(levels),
	}); err != nil {
		t.Fatalf("seed profile failed: %v", err)
	}

	list, err := svc.List()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应有 1 条记录，实际 %d", len(list))
	}
	if list[0].KeepAliveAuto != "影帝(Ultimate User)" {
		t.Errorf("自动识别结果不对: %q", list[0].KeepAliveAuto)
	}
	if got := keepAliveName(list[0].Levels); got != "影帝(Ultimate User)" {
		t.Errorf("未设置手动值时保号应为自动结果，实际 %q", got)
	}

	// 手动改成 Veteran User：行内标记换位置，自动识别结果不变。
	if err := svc.SetKeepAliveLevel("hdsky", "Veteran User"); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	list, _ = svc.List()
	if got := keepAliveName(list[0].Levels); got != "Veteran User" {
		t.Errorf("手动保号未生效，实际 %q", got)
	}
	if list[0].KeepAliveLevel != "Veteran User" {
		t.Errorf("手动值未回传: %q", list[0].KeepAliveLevel)
	}
	if list[0].KeepAliveAuto != "影帝(Ultimate User)" {
		t.Errorf("自动识别结果被手动值污染: %q", list[0].KeepAliveAuto)
	}

	// 清空 = 恢复自动识别。
	if err := svc.SetKeepAliveLevel("hdsky", ""); err != nil {
		t.Fatalf("clear failed: %v", err)
	}
	list, _ = svc.List()
	if got := keepAliveName(list[0].Levels); got != "影帝(Ultimate User)" {
		t.Errorf("清空后应回到自动结果，实际 %q", got)
	}

	// 非法等级名要拒绝（防止把别的站的等级写进来）。
	if err := svc.SetKeepAliveLevel("hdsky", "Nexus Master"); !errors.Is(err, ErrLevelUnknown) {
		t.Errorf("未知等级应返回 ErrLevelUnknown，实际 %v", err)
	}
	// 没抓取过的站点要提示先刷新。
	if err := svc.SetKeepAliveLevel("nosite", "User"); !errors.Is(err, ErrProfileMissing) {
		t.Errorf("无数据站点应返回 ErrProfileMissing，实际 %v", err)
	}
}
