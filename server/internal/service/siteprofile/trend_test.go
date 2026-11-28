package siteprofile

import (
	"errors"
	"testing"
	"time"

	"github.com/pt-nexus/server/internal/repository"
)

// trendValue 取可空增量的值，nil（该时间桶没有采样）统一当 -1 便于断言。
func trendValue(value *int64) int64 {
	if value == nil {
		return -1
	}
	return *value
}

// TestUploadTrendDaily 验证按天趋势：时间轴连续、增量与「当日增量」同口径、站点序列按合计降序。
func TestUploadTrendDaily(t *testing.T) {
	service := newDailyTestService(t)

	samples := []repository.SiteProfileDaily{
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-24", Uploaded: 1000},
		{SiteCode: "hxpt", Nickname: "好学", StatDate: "2026-11-24", Uploaded: 800},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-25", Uploaded: 1500},
		{SiteCode: "hxpt", Nickname: "好学", StatDate: "2026-11-25", Uploaded: 900},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-26", Uploaded: 1800},
	}
	for _, sample := range samples {
		if _, err := service.dailyRepo.SaveSample(sample); err != nil {
			t.Fatalf("save sample: %v", err)
		}
	}

	trend, err := service.UploadTrend("2026-11-25", "2026-11-26", "day")
	if err != nil {
		t.Fatalf("trend: %v", err)
	}
	if len(trend.Buckets) != 2 || trend.Buckets[0] != "2026-11-25" || trend.Buckets[1] != "2026-11-26" {
		t.Fatalf("时间轴应为 25/26 两天，实际 %v", trend.Buckets)
	}
	// 25 日：天空 +500、好学 +100；26 日：天空 +300（好学当天没采样记 0）
	if trendValue(trend.Total[0]) != 600 || trendValue(trend.Total[1]) != 300 {
		t.Fatalf("合计应为 [600 300]，实际 [%d %d]", trendValue(trend.Total[0]), trendValue(trend.Total[1]))
	}
	if trend.TotalSum != 900 || trend.Days != 2 {
		t.Fatalf("区间合计应为 900 / 2 天，实际 %d / %d", trend.TotalSum, trend.Days)
	}
	if len(trend.Sites) != 2 {
		t.Fatalf("应返回 2 个站点，实际 %d", len(trend.Sites))
	}
	if trend.Sites[0].SiteCode != "hdsky" || trend.Sites[0].Total != 800 {
		t.Fatalf("站点应按合计降序，首位应为 hdsky(800)，实际 %s(%d)", trend.Sites[0].SiteCode, trend.Sites[0].Total)
	}
	if len(trend.Sites[0].Values) != 2 || trendValue(trend.Sites[0].Values[0]) != 500 || trendValue(trend.Sites[0].Values[1]) != 300 {
		t.Fatalf("天空序列应为 [500 300]，实际 [%d %d]", trendValue(trend.Sites[0].Values[0]), trendValue(trend.Sites[0].Values[1]))
	}

	// 首日没有基线：不能把累计量当增量。
	first, err := service.UploadTrend("2026-11-24", "2026-11-24", "day")
	if err != nil {
		t.Fatalf("trend first day: %v", err)
	}
	if first.Total[0] != nil || first.TotalSum != 0 {
		t.Fatalf("无基线的日子应为 null（没数据），实际 %d / %d", trendValue(first.Total[0]), first.TotalSum)
	}

	// 中间没有数据的日子也要占位，时间轴不能压缩。
	gap, err := service.UploadTrend("2026-11-25", "2026-11-29", "day")
	if err != nil {
		t.Fatalf("trend gap: %v", err)
	}
	if len(gap.Buckets) != 5 {
		t.Fatalf("应生成 5 个连续日期桶，实际 %d（%v）", len(gap.Buckets), gap.Buckets)
	}
	if gap.Total[4] != nil {
		t.Fatalf("最后一天无数据应为 null，实际 %d", trendValue(gap.Total[4]))
	}
	// 有数据但增量为 0 的日子应该是 0 而不是 null：这里 11-26 好学没采样，但天空有，
	// 合计不为 nil。
	if gap.Total[1] == nil {
		t.Fatalf("有采样的日子不应为 null")
	}
}

// TestUploadTrendMonthly 验证按月聚合：同月各天增量求和，时间轴按月展开。
func TestUploadTrendMonthly(t *testing.T) {
	service := newDailyTestService(t)

	samples := []repository.SiteProfileDaily{
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-10-31", Uploaded: 100},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-01", Uploaded: 400},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2026-11-02", Uploaded: 700},
	}
	for _, sample := range samples {
		if _, err := service.dailyRepo.SaveSample(sample); err != nil {
			t.Fatalf("save sample: %v", err)
		}
	}

	trend, err := service.UploadTrend("2026-10-01", "2026-11-30", "month")
	if err != nil {
		t.Fatalf("trend month: %v", err)
	}
	if len(trend.Buckets) != 2 || trend.Buckets[0] != "2026-10" || trend.Buckets[1] != "2026-11" {
		t.Fatalf("月份轴应为 [2026-10 2026-11]，实际 %v", trend.Buckets)
	}
	// 10 月只有 10-31 一个点（有基线与否取决于是否更早采样：这里没有，记 0）
	if trend.Total[0] != nil {
		t.Fatalf("10 月无基线（没数据）应为 null，实际 %d", trendValue(trend.Total[0]))
	}
	if trendValue(trend.Total[1]) != 600 {
		t.Fatalf("11 月应为 300+300=600，实际 %d", trendValue(trend.Total[1]))
	}
}

// TestUploadTrendMonthDefaultRange 验证按月未传区间时默认「近 6 个自然月」，且起点对齐到月初。
func TestUploadTrendMonthDefaultRange(t *testing.T) {
	service := newDailyTestService(t)

	location := repository.DatabaseTimeZone
	now := time.Now().In(location)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, location)
	wantFrom := monthStart.AddDate(0, -(trendDefaultRangeMonths - 1), 0).Format(dailyDateFormat)

	trend, err := service.UploadTrend("", "", "month")
	if err != nil {
		t.Fatalf("trend month default: %v", err)
	}
	if trend.Granularity != TrendGranularityMonth {
		t.Fatalf("粒度应为 month，实际 %s", trend.Granularity)
	}
	// 首月必须是月初：月中起点会让第一个月的柱看起来凭空变矮。
	if trend.From != wantFrom {
		t.Fatalf("按月默认起点应为 %s，实际 %s", wantFrom, trend.From)
	}
	if len(trend.Buckets) != trendDefaultRangeMonths {
		t.Fatalf("按月默认应有 %d 个桶，实际 %d（%v）", trendDefaultRangeMonths, len(trend.Buckets), trend.Buckets)
	}
}

// TestUploadTrendAllRange 验证 from=all：起点取表里最早的一天（前端「全部时间」）。
func TestUploadTrendAllRange(t *testing.T) {
	service := newDailyTestService(t)

	samples := []repository.SiteProfileDaily{
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2025-06-15", Uploaded: 100},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2025-06-16", Uploaded: 400},
		{SiteCode: "hdsky", Nickname: "天空", StatDate: "2025-07-02", Uploaded: 900},
	}
	for _, sample := range samples {
		if _, err := service.dailyRepo.SaveSample(sample); err != nil {
			t.Fatalf("save sample: %v", err)
		}
	}

	trend, err := service.UploadTrend("ALL", "", "month")
	if err != nil {
		t.Fatalf("trend all: %v", err)
	}
	if trend.From != "2025-06-15" {
		t.Fatalf("全部时间应从最早一天开始，实际 %s", trend.From)
	}
	if len(trend.Buckets) < 2 || trend.Buckets[0] != "2025-06" || trend.Buckets[1] != "2025-07" {
		t.Fatalf("月份轴应含 2025-06 / 2025-07，实际 %v", trend.Buckets)
	}
	// 6 月：06-15 无基线记 null，06-16 相对 06-15 增 300；7 月：相对 06-16 增 500。
	if trendValue(trend.Total[0]) != 300 || trendValue(trend.Total[1]) != 500 {
		t.Fatalf("6/7 月增量应为 [300 500]，实际 [%d %d]", trendValue(trend.Total[0]), trendValue(trend.Total[1]))
	}

	// 空表时 from=all 退回默认跨度，不能报错。
	empty := newDailyTestService(t)
	if _, err := empty.UploadTrend("all", "", "month"); err != nil {
		t.Fatalf("空表 all 不应报错: %v", err)
	}
}

// TestUploadTrendMonthRangeLimit 验证按月放宽跨度上限（近 1 年可用、超过 120 个月才拒绝）。
func TestUploadTrendMonthRangeLimit(t *testing.T) {
	service := newDailyTestService(t)

	location := repository.DatabaseTimeZone
	now := time.Now().In(location)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	// 近 1 年（12 个自然月）必须能查：前端「近 1 年」就落在这个区间。
	yearAgo := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, location).AddDate(0, -11, 0)
	if _, err := service.UploadTrend(yearAgo.Format(dailyDateFormat), end.Format(dailyDateFormat), "month"); err != nil {
		t.Fatalf("按月查近 1 年不应报错: %v", err)
	}
	// 超过 120 个月才拒绝。
	far := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, location).AddDate(0, -(trendMaxRangeMonths+1), 0)
	if _, err := service.UploadTrend(far.Format(dailyDateFormat), end.Format(dailyDateFormat), "month"); !errors.Is(err, ErrInvalidTrendRange) {
		t.Fatalf("按月跨度超限应返回 ErrInvalidTrendRange，实际 %v", err)
	}
}

// TestUploadTrendRejectsInvalidParams 校验非法参数返回 ErrInvalidTrendRange（接口层据此转 400）。
func TestUploadTrendRejectsInvalidParams(t *testing.T) {
	service := newDailyTestService(t)

	cases := []struct{ from, to, gran string }{
		{"2026/11/01", "2026-11-30", "day"},
		{"2026-12-01", "2026-11-01", "day"},
		{"2020-01-01", "2026-11-01", "day"},
		{"2026-11-01", "2026-11-30", "week"},
	}
	for _, item := range cases {
		if _, err := service.UploadTrend(item.from, item.to, item.gran); !errors.Is(err, ErrInvalidTrendRange) {
			t.Fatalf("参数 %+v 应返回 ErrInvalidTrendRange，实际 %v", item, err)
		}
	}

	// 默认范围：近 30 天（含今天），结束时点是今天。
	defaults, err := service.UploadTrend("", "", "")
	if err != nil {
		t.Fatalf("默认参数不应报错: %v", err)
	}
	if defaults.Days != trendDefaultRangeDays || defaults.Granularity != TrendGranularityDay {
		t.Fatalf("默认应为近 %d 天按天，实际 %d 天 / %s", trendDefaultRangeDays, defaults.Days, defaults.Granularity)
	}
	if defaults.From == "" || defaults.To == "" || len(defaults.Buckets) != trendDefaultRangeDays {
		t.Fatalf("默认时间轴不完整: from=%s to=%s buckets=%d", defaults.From, defaults.To, len(defaults.Buckets))
	}
}
