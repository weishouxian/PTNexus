package repository

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newSiteDailyTestStore(t *testing.T) *Store {
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
	return &Store{DB: db, DBType: "sqlite"}
}

// TestSaveSampleLocksBaseline 验证日快照的基线锁定与抗污染行为：
//   - 首日没有更早的快照，HasBaseline 为 0（前端显示「—」而不是把累计量当增量）；
//   - 次日基线取「前一天最后一次采样」的值，而不是当天第一次的值；
//   - 同一站同一天多次采样只更新最新值，基线不变（增量不会因为多刷一次而缩水）；
//   - 本次抓取缺失的字段（值为 0）不会被写成 0，而是沿用当天旧值。
func TestSaveSampleLocksBaseline(t *testing.T) {
	store := newSiteDailyTestStore(t)
	repo := NewSiteProfileDailyRepository(store)
	if err := repo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 第一天：没有可比基线。
	day1, err := repo.SaveSample(SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-24",
		Uploaded: 1000, Downloaded: 100, Bonus: 50, SeedBonus: 10, Seeding: 5,
	})
	if err != nil {
		t.Fatalf("save day1: %v", err)
	}
	if day1.HasBaseline != 0 {
		t.Errorf("首日不应有基线, got %d", day1.HasBaseline)
	}
	if day1.Samples != 1 {
		t.Errorf("首日采样次数应为 1, got %d", day1.Samples)
	}

	// 第一天再采样一次：最新值更新，基线不重新计算，采样次数累加。
	day1b, err := repo.SaveSample(SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-24",
		Uploaded: 1500, Downloaded: 150, Bonus: 80, SeedBonus: 15, Seeding: 6,
	})
	if err != nil {
		t.Fatalf("save day1 second: %v", err)
	}
	if day1b.Uploaded != 1500 || day1b.Samples != 2 {
		t.Errorf("当天二次采样应更新最新值并累加次数, got uploaded=%d samples=%d", day1b.Uploaded, day1b.Samples)
	}
	if day1b.FirstAt != day1.FirstAt {
		t.Errorf("当天首次采样时间不应变化: %q -> %q", day1.FirstAt, day1b.FirstAt)
	}
	if day1b.HasBaseline != 0 {
		t.Errorf("当天二次采样不应凭空造出基线, got %d", day1b.HasBaseline)
	}

	// 第二天：基线必须取「前一天最后一次采样」（1500/150/80/15），否则当天的增量会把前一天的量算进来。
	day2, err := repo.SaveSample(SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-25",
		Uploaded: 2000, Downloaded: 200, Bonus: 100, SeedBonus: 20, Seeding: 8,
	})
	if err != nil {
		t.Fatalf("save day2: %v", err)
	}
	if day2.HasBaseline != 1 || day2.BaseDate != "2026-11-24" {
		t.Fatalf("次日基线错误: has=%d baseDate=%s", day2.HasBaseline, day2.BaseDate)
	}
	if day2.BaseUploaded != 1500 || day2.BaseDownloaded != 150 || day2.BaseBonus != 80 || day2.BaseSeedBonus != 15 {
		t.Errorf("基线应取前一天最后值, got %+v", day2)
	}
	if got := day2.Uploaded - day2.BaseUploaded; got != 500 {
		t.Errorf("当天上传增量应为 500, got %d", got)
	}

	// 第二天第三次采样时站点没渲染这些字段（解析结果全 0）：不能把已有值冲掉。
	day2b, err := repo.SaveSample(SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-25",
	})
	if err != nil {
		t.Fatalf("save day2 empty: %v", err)
	}
	if day2b.Uploaded != 2000 || day2b.Downloaded != 200 || day2b.Bonus != 100 || day2b.SeedBonus != 20 {
		t.Errorf("空采样应沿用当天旧值, got %+v", day2b)
	}
	if day2b.BaseUploaded != 1500 {
		t.Errorf("基线不应被空采样改写, got %d", day2b.BaseUploaded)
	}
	if day2b.Samples != 2 {
		t.Errorf("采样次数应累加到 2, got %d", day2b.Samples)
	}

	// 查询接口：某天列表与「之前最近一条」都要能取到。
	rows, err := repo.ListByDate("2026-11-25")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].SiteCode != "hdsky" {
		t.Fatalf("按日期查询结果异常: %+v", rows)
	}
	previous, ok, err := repo.LatestBefore("hdsky", "2026-11-25")
	if err != nil || !ok {
		t.Fatalf("取前一天快照失败: ok=%v err=%v", ok, err)
	}
	if previous.StatDate != "2026-11-24" || previous.Uploaded != 1500 {
		t.Errorf("前一天快照内容异常: %+v", previous)
	}

	// 没有更早记录时 LatestBefore 应返回 false，让调用方走「无基线」分支。
	if _, ok, err := repo.LatestBefore("hdsky", "2026-11-24"); err != nil || ok {
		t.Errorf("首日不应取到更早快照: ok=%v err=%v", ok, err)
	}
}
