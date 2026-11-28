package siteprofile

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDailyTestService(t *testing.T) *Service {
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
	store := &repository.Store{DB: db, DBType: "sqlite"}
	dailyRepo := repository.NewSiteProfileDailyRepository(store)
	if err := dailyRepo.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// siteRepo 留空：Daily 内部对 nil 有保护，昵称退回快照里的值。
	return NewService(nil, nil, dailyRepo)
}

// TestResolveDailyDate 验证日期参数解析：空/today/yesterday 与非法格式。
func TestResolveDailyDate(t *testing.T) {
	now := time.Now().In(repository.DatabaseTimeZone)

	today, err := resolveDailyDate("")
	if err != nil || today != now.Format(dailyDateFormat) {
		t.Errorf("空参数应返回今天: got %q err=%v", today, err)
	}
	if got, err := resolveDailyDate("today"); err != nil || got != today {
		t.Errorf("today 应为今天: got %q err=%v", got, err)
	}
	if got, err := resolveDailyDate("yesterday"); err != nil || got != now.AddDate(0, 0, -1).Format(dailyDateFormat) {
		t.Errorf("yesterday 应为昨天: got %q err=%v", got, err)
	}
	if got, err := resolveDailyDate("2026-11-20"); err != nil || got != "2026-11-20" {
		t.Errorf("标准日期应原样返回: got %q err=%v", got, err)
	}
	if _, err := resolveDailyDate("2026/11/20"); !errors.Is(err, ErrInvalidDailyDate) {
		t.Errorf("非法日期应返回 ErrInvalidDailyDate, got %v", err)
	}
}

// TestDailyDeltaCalculation 验证当日增量：
//   - 有基线的站点给出「最新值 - 基线值」，并累加到合计；
//   - 首日（无基线）的增量字段为 nil，且不计入合计；
//   - 结果按上传增量降序排列。
func TestDailyDeltaCalculation(t *testing.T) {
	service := newDailyTestService(t)

	// 前一天两个站点的累计值。
	if _, err := service.dailyRepo.SaveSample(repository.SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-24",
		Uploaded: 1000, Downloaded: 100, Bonus: 50, SeedBonus: 10,
	}); err != nil {
		t.Fatalf("save a day1: %v", err)
	}
	if _, err := service.dailyRepo.SaveSample(repository.SiteProfileDaily{
		SiteCode: "hxpt", Nickname: "好学", StatDate: "2026-11-24",
		Uploaded: 800, Downloaded: 80, Bonus: 40, SeedBonus: 8,
	}); err != nil {
		t.Fatalf("save b day1: %v", err)
	}

	// 当天：天空 +500 上传，好学 +100 上传（排序应把天空排前面）。
	if _, err := service.dailyRepo.SaveSample(repository.SiteProfileDaily{
		SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-25",
		Uploaded: 1500, Downloaded: 120, Bonus: 70, SeedBonus: 14,
	}); err != nil {
		t.Fatalf("save a day2: %v", err)
	}
	if _, err := service.dailyRepo.SaveSample(repository.SiteProfileDaily{
		SiteCode: "hxpt", Nickname: "好学", StatDate: "2026-11-25",
		Uploaded: 900, Downloaded: 90, Bonus: 45, SeedBonus: 9,
	}); err != nil {
		t.Fatalf("save b day2: %v", err)
	}

	summary, err := service.Daily("2026-11-25")
	if err != nil {
		t.Fatalf("daily: %v", err)
	}
	if len(summary.Rows) != 2 {
		t.Fatalf("应返回 2 行, got %d", len(summary.Rows))
	}
	if summary.Rows[0].SiteCode != "hdsky" {
		t.Errorf("应按上传增量降序, 首行应为 hdsky, got %s", summary.Rows[0].SiteCode)
	}
	first := summary.Rows[0]
	if first.UploadedDelta == nil || *first.UploadedDelta != 500 {
		t.Errorf("天空上传增量应为 500, got %v", first.UploadedDelta)
	}
	if first.DownloadedDelta == nil || *first.DownloadedDelta != 20 {
		t.Errorf("天空下载增量应为 20, got %v", first.DownloadedDelta)
	}
	if first.BonusDelta == nil || *first.BonusDelta != 20 {
		t.Errorf("天空魔力值增量应为 20, got %v", first.BonusDelta)
	}
	if first.SeedBonusDelta == nil || *first.SeedBonusDelta != 4 {
		t.Errorf("天空做种积分增量应为 4, got %v", first.SeedBonusDelta)
	}
	if !summary.HasBaseline || summary.TotalUploadedDelta != 600 || summary.TotalDownloadedDelta != 30 {
		t.Errorf("合计错误: hasBaseline=%v up=%d down=%d", summary.HasBaseline, summary.TotalUploadedDelta, summary.TotalDownloadedDelta)
	}

	// 首日：没有更早采样，增量必须是 nil（不能把累计值当增量）。
	day1, err := service.Daily("2026-11-24")
	if err != nil {
		t.Fatalf("daily day1: %v", err)
	}
	if len(day1.Rows) != 2 {
		t.Fatalf("首日应返回 2 行, got %d", len(day1.Rows))
	}
	if day1.HasBaseline || day1.TotalUploadedDelta != 0 {
		t.Errorf("首日不应有基线合计, got hasBaseline=%v total=%d", day1.HasBaseline, day1.TotalUploadedDelta)
	}
	for _, row := range day1.Rows {
		if row.UploadedDelta != nil || row.BonusDelta != nil || row.SeedBonusDelta != nil {
			t.Errorf("首日增量应为 nil, got %+v", row)
		}
	}

	// 没有采样的日期：返回空列表而不是报错。
	empty, err := service.Daily("2026-10-01")
	if err != nil {
		t.Fatalf("daily empty: %v", err)
	}
	if len(empty.Rows) != 0 {
		t.Errorf("无采样日期应返回空列表, got %d", len(empty.Rows))
	}
}
