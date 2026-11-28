package repository

import (
	"errors"
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// newSiteProfileTestRepo 建一个内存 sqlite 的 site_profiles 仓库。
func newSiteProfileTestRepo(t *testing.T) *SiteProfileRepository {
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
	repo := NewSiteProfileRepository(&Store{DB: db, DBType: "sqlite"})
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	return repo
}

// TestUpdateKeepAliveLevelOnlyTouchesOneColumn 验证手动保号只改一列：
// 抓取到的上传量、等级、等级缓存与状态都不能被顺手刷掉。
func TestUpdateKeepAliveLevelOnlyTouchesOneColumn(t *testing.T) {
	repo := newSiteProfileTestRepo(t)
	seed := SiteProfile{
		SiteCode:   "hdsky",
		Nickname:   "天空",
		UserClass:  "Power User",
		Uploaded:   1024,
		LevelsJSON: `{"version":4,"levels":[{"name":"Ultimate User","order":8}]}`,
		Status:     "ok",
	}
	if err := repo.Upsert(seed); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	if err := repo.UpdateKeepAliveLevel("hdsky", "影帝(Ultimate User)"); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	got, err := repo.GetBySiteCode("hdsky")
	if err != nil {
		t.Fatalf("get failed: %v", err)
	}
	if got.KeepAliveLevel != "影帝(Ultimate User)" {
		t.Errorf("保号等级未写入: %q", got.KeepAliveLevel)
	}
	if got.Uploaded != 1024 || got.UserClass != "Power User" || got.Status != "ok" || got.LevelsJSON != seed.LevelsJSON {
		t.Errorf("其它列被误改: %+v", got)
	}

	// 重复保存同一个值不能报「站点不存在」（MySQL 未开 clientFoundRows 时 RowsAffected 为 0）。
	if err := repo.UpdateKeepAliveLevel("hdsky", "影帝(Ultimate User)"); err != nil {
		t.Errorf("重复保存应成功，实际 %v", err)
	}
	// 传空串 = 恢复自动识别。
	if err := repo.UpdateKeepAliveLevel("hdsky", "  "); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	got, _ = repo.GetBySiteCode("hdsky")
	if got.KeepAliveLevel != "" {
		t.Errorf("清空后仍有值: %q", got.KeepAliveLevel)
	}
}

// TestUpdateKeepAliveLevelMissingSite 验证没抓取过的站点返回明确错误（前端据此提示先刷新）。
func TestUpdateKeepAliveLevelMissingSite(t *testing.T) {
	repo := newSiteProfileTestRepo(t)
	if err := repo.UpdateKeepAliveLevel("nosuchsite", "User"); !errors.Is(err, ErrSiteProfileNotFound) {
		t.Errorf("应返回 ErrSiteProfileNotFound，实际 %v", err)
	}
}

// TestUpsertKeepsManualKeepAlive 验证刷新落库不会覆盖用户手动设置的保号等级。
func TestUpsertKeepsManualKeepAlive(t *testing.T) {
	repo := newSiteProfileTestRepo(t)
	if err := repo.Upsert(SiteProfile{SiteCode: "hdsky", Status: "ok", Uploaded: 1}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	if err := repo.UpdateKeepAliveLevel("hdsky", "影帝(Ultimate User)"); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	// 模拟一次成功的抓取落库（Source 非空，走完整覆盖分支）。
	if err := repo.Upsert(SiteProfile{
		SiteCode:   "hdsky",
		Source:     "nexusphp",
		Status:     "ok",
		Uploaded:   2048,
		LevelsJSON: `{"version":4,"levels":[{"name":"Ultimate User","order":8}]}`,
	}); err != nil {
		t.Fatalf("upsert failed: %v", err)
	}
	got, _ := repo.GetBySiteCode("hdsky")
	if got.KeepAliveLevel != "影帝(Ultimate User)" {
		t.Errorf("刷新后手动保号等级被覆盖: %q", got.KeepAliveLevel)
	}
	if got.Uploaded != 2048 {
		t.Errorf("抓取数据未更新: %d", got.Uploaded)
	}
}
