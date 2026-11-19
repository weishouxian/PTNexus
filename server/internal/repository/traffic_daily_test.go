package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newTrafficTestStore(t *testing.T) *Store {
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

func readDailyRow(t *testing.T, store *Store, dateKey string) (int64, int64, int64, int64, int64, int64) {
	t.Helper()
	sqlDB, err := store.DB.DB()
	if err != nil {
		t.Fatalf("sql.DB: %v", err)
	}
	var ul, dl, startUL, startDL, endUL, endDL int64
	err = sqlDB.QueryRow(`
		SELECT uploaded, downloaded, start_cumulative_uploaded, start_cumulative_downloaded,
		       end_cumulative_uploaded, end_cumulative_downloaded
		FROM traffic_stats_daily WHERE stat_date = ? AND downloader_id = 'qb1'
	`, dateKey).Scan(&ul, &dl, &startUL, &startDL, &endUL, &endDL)
	if err != nil {
		t.Fatalf("read daily row %s: %v", dateKey, err)
	}
	return ul, dl, startUL, startDL, endUL, endDL
}

// TestSettleTrafficDailyChain 验证日结算的链式基线与幂等性。
func TestSettleTrafficDailyChain(t *testing.T) {
	store := newTrafficTestStore(t)
	repo := NewStatsRepository(store)
	if err := repo.EnsureTrafficStatsTables(); err != nil {
		t.Fatalf("ensure tables: %v", err)
	}

	loc := time.Local
	day1 := time.Date(2026, 11, 17, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)

	records := []TrafficStatRecord{
		{StatDatetime: day1.Add(1 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 1000, CumulativeDownloaded: 200},
		{StatDatetime: day1.Add(23 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 5000, CumulativeDownloaded: 800},
		{StatDatetime: day2.Add(1 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 5000, CumulativeDownloaded: 800},
		{StatDatetime: day2.Add(23 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 12000, CumulativeDownloaded: 3000},
	}
	if _, err := repo.UpsertTrafficStats(records); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}

	if _, err := repo.SettleTrafficDaily(day1); err != nil {
		t.Fatalf("settle day1: %v", err)
	}
	ul, dl, startUL, startDL, endUL, endDL := readDailyRow(t, store, "2026-11-17")
	if ul != 4000 || dl != 600 {
		t.Fatalf("day1 delta = %d/%d, want 4000/600", ul, dl)
	}
	if startUL != 1000 || startDL != 200 || endUL != 5000 || endDL != 800 {
		t.Fatalf("day1 baseline start=%d/%d end=%d/%d, want 1000/200 -> 5000/800", startUL, startDL, endUL, endDL)
	}

	if _, err := repo.SettleTrafficDaily(day2); err != nil {
		t.Fatalf("settle day2: %v", err)
	}
	ul, dl, startUL, startDL, endUL, endDL = readDailyRow(t, store, "2026-11-18")
	if ul != 7000 || dl != 2200 {
		t.Fatalf("day2 delta = %d/%d, want 7000/2200", ul, dl)
	}
	if startUL != 5000 || startDL != 800 || endUL != 12000 || endDL != 3000 {
		t.Fatalf("day2 baseline start=%d/%d end=%d/%d, want 5000/800 -> 12000/3000", startUL, startDL, endUL, endDL)
	}

	// 幂等：同日重跑结果不变。
	if _, err := repo.SettleTrafficDaily(day2); err != nil {
		t.Fatalf("settle day2 again: %v", err)
	}
	ul, dl, _, _, _, _ = readDailyRow(t, store, "2026-11-18")
	if ul != 7000 || dl != 2200 {
		t.Fatalf("day2 delta after re-settle = %d/%d, want 7000/2200", ul, dl)
	}

	through, err := repo.QueryDailySettledThrough()
	if err != nil {
		t.Fatalf("settled through: %v", err)
	}
	if through != "2026-11-18" {
		t.Fatalf("settled through = %q, want 2026-11-18", through)
	}
}

// TestSettleTrafficDailyClampsReset 验证下载器累计回退（清零/换实例）时增量不为负。
func TestSettleTrafficDailyClampsReset(t *testing.T) {
	store := newTrafficTestStore(t)
	repo := NewStatsRepository(store)
	if err := repo.EnsureTrafficStatsTables(); err != nil {
		t.Fatalf("ensure tables: %v", err)
	}

	loc := time.Local
	day1 := time.Date(2026, 10, 10, 0, 0, 0, 0, loc)
	day2 := day1.AddDate(0, 0, 1)

	records := []TrafficStatRecord{
		{StatDatetime: day1.Add(2 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 90000, CumulativeDownloaded: 9000},
		{StatDatetime: day1.Add(20 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 95000, CumulativeDownloaded: 9500},
		{StatDatetime: day2.Add(2 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 300, CumulativeDownloaded: 30},
		{StatDatetime: day2.Add(20 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 900, CumulativeDownloaded: 90},
	}
	if _, err := repo.UpsertTrafficStats(records); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}
	if _, err := repo.SettleTrafficDaily(day1); err != nil {
		t.Fatalf("settle day1: %v", err)
	}
	if _, err := repo.SettleTrafficDaily(day2); err != nil {
		t.Fatalf("settle day2: %v", err)
	}

	ul, dl, startUL, _, endUL, _ := readDailyRow(t, store, "2026-10-11")
	if ul != 0 || dl != 0 {
		t.Fatalf("reset day delta = %d/%d, want 0/0", ul, dl)
	}
	if startUL != 95000 || endUL != 900 {
		t.Fatalf("reset day baseline start=%d end=%d, want 95000 -> 900", startUL, endUL)
	}
}

// TestTrafficMonthAndTodayUseDaily 验证「本月/今日」走日表 + 实时累计差。
func TestTrafficMonthAndTodayUseDaily(t *testing.T) {
	store := newTrafficTestStore(t)
	statsRepo := NewStatsRepository(store)
	if err := statsRepo.EnsureTrafficStatsTables(); err != nil {
		t.Fatalf("ensure tables: %v", err)
	}
	torrentRepo := NewTorrentRepository(store)

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.AddDate(0, 0, -1)

	records := []TrafficStatRecord{
		// 昨天：累计 1000 -> 4000
		{StatDatetime: yesterday.Add(time.Hour), DownloaderID: "qb1", CumulativeUploaded: 1000, CumulativeDownloaded: 100},
		{StatDatetime: yesterday.Add(23 * time.Hour), DownloaderID: "qb1", CumulativeUploaded: 4000, CumulativeDownloaded: 400},
		// 今天：累计 4000 -> 9000
		{StatDatetime: todayStart.Add(time.Minute), DownloaderID: "qb1", CumulativeUploaded: 4000, CumulativeDownloaded: 400},
		{StatDatetime: now.Add(-time.Minute), DownloaderID: "qb1", CumulativeUploaded: 9000, CumulativeDownloaded: 900},
	}
	if _, err := statsRepo.UpsertTrafficStats(records); err != nil {
		t.Fatalf("upsert stats: %v", err)
	}
	if _, err := statsRepo.SettleTrafficDaily(yesterday); err != nil {
		t.Fatalf("settle yesterday: %v", err)
	}

	today, err := torrentRepo.TrafficToday()
	if err != nil {
		t.Fatalf("traffic today: %v", err)
	}
	if today["qb1"]["today_ul"] != 5000 || today["qb1"]["today_dl"] != 500 {
		t.Fatalf("today = %d/%d, want 5000/500", today["qb1"]["today_ul"], today["qb1"]["today_dl"])
	}

	month, err := torrentRepo.TrafficThisMonth()
	if err != nil {
		t.Fatalf("traffic month: %v", err)
	}
	// 昨天所在月份与今天同月时：月 = (4000-1000) + (9000-4000) = 8000。
	if todayStart.Month() == yesterday.Month() && todayStart.Year() == yesterday.Year() {
		if month["qb1"]["month_ul"] != 8000 || month["qb1"]["month_dl"] != 800 {
			t.Fatalf("month = %d/%d, want 8000/800", month["qb1"]["month_ul"], month["qb1"]["month_dl"])
		}
	}

	totals, err := torrentRepo.TrafficTotals()
	if err != nil {
		t.Fatalf("traffic totals: %v", err)
	}
	if totals["qb1"]["total_ul"] != 9000 || totals["qb1"]["total_dl"] != 900 {
		t.Fatalf("totals = %d/%d, want 9000/900", totals["qb1"]["total_ul"], totals["qb1"]["total_dl"])
	}
}
