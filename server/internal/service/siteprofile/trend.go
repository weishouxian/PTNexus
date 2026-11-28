package siteprofile

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/repository"
)

// ErrInvalidTrendRange 表示趋势查询的日期范围/粒度不合法，接口层据此返回 400。
var ErrInvalidTrendRange = errors.New("趋势查询参数不合法")

// 本文件实现「上传增量趋势」：把 site_profile_daily 里按天落盘的增量聚合成折线图数据。
//
// 数据口径与「当日增量」完全一致：当天增量 = 当天最后采样值 - 日初基线（BaseUploaded）。
// 时间桶一律占位（不能压缩日期轴），但「没采到数据」和「增量为 0」必须区分：
// 前者给 null（折线断口，说明那天服务没跑 / 该站没抓到），后者给 0。
//
// 粒度：
//   - day：区间内每一天一个点；
//   - month：把每一天的增量按月求和（月内断更的日子依然是 0，不参与拉高）。

const (
	// TrendGranularityDay 按天聚合。
	TrendGranularityDay = "day"
	// TrendGranularityMonth 按月聚合。
	TrendGranularityMonth = "month"

	// TrendRangeAll 是 from 的特殊值：从表里最早的一天开始（前端「全部时间」）。
	TrendRangeAll = "all"

	// trendMaxRangeDays 是按天查询的最大跨度（一年），防止前端传 2000-01-01 时拉全表。
	trendMaxRangeDays = 366
	// trendMaxRangeMonths 是按月查询的最大跨度（10 年）：按月只需要 12 个点左右，
	// 但「全部时间」要能覆盖上线以来全部历史，所以比按天的上限放得宽。
	trendMaxRangeMonths = 120
	// trendDefaultRangeDays 是按天且未传 from/to 时的默认跨度（近 30 天，含今天）。
	trendDefaultRangeDays = 30
	// trendDefaultRangeMonths 是按月且未传 from/to 时的默认跨度（近 6 个自然月，含当月）。
	trendDefaultRangeMonths = 6
)

// TrendSiteSeries 是趋势图里某个站点的一条折线。
// Values 里的 nil 表示「那天没采到该站数据」（断线），0 表示「有数据但当天的增量为 0」。
type TrendSiteSeries struct {
	SiteCode string   `json:"site_code"`
	Nickname string   `json:"nickname"`
	Values   []*int64 `json:"values"`
	Total    int64    `json:"total"`
}

// UploadTrend 是「上传增量趋势」接口的完整返回。
type UploadTrend struct {
	From        string            `json:"from"`
	To          string            `json:"to"`
	Granularity string            `json:"granularity"`
	// Buckets 是时间轴的原始键（day：2026-11-28；month：2026-11），前端用它做 tooltip/对比。
	Buckets []string `json:"buckets"`
	// Labels 是展示用的短标签（day：11-28；month：2026-11）。
	Labels []string `json:"labels"`
	// Total 是所有站点合计的上传增量，与 Buckets 等长；nil 表示该时间桶完全没有采样。
	Total []*int64 `json:"total"`
	// TotalSum 是区间合计，方便前端直接显示「近 30 天总上传」。
	TotalSum int64 `json:"total_sum"`
	// Sites 按区间合计降序排列（只含有数据的站点）。
	Sites []TrendSiteSeries `json:"sites"`
	// Days 是区间天数（含首尾），前端用于算日均。
	Days int `json:"days"`
}

// UploadTrend 返回上传增量趋势。
// 参数/返回：from/to 为 YYYY-MM-DD（空则按粒度取默认：day 近 30 天、month 近 6 个月），
// from 传 "all" 表示「全部时间」（取表里最早的一天）；granularity 为 day/month（空则 day）。
// 失败场景：日期格式非法、跨度超限（day 366 天 / month 120 个月）、粒度非法、数据库查询失败。
// 副作用：无（只读）。
func (s *Service) UploadTrend(from, to, granularity string) (UploadTrend, error) {
	gran := strings.ToLower(strings.TrimSpace(granularity))
	switch gran {
	case "", TrendGranularityDay:
		gran = TrendGranularityDay
	case TrendGranularityMonth:
	default:
		return UploadTrend{}, fmt.Errorf("%w: 粒度只能是 day 或 month", ErrInvalidTrendRange)
	}

	from = s.resolveTrendFrom(from)
	start, end, err := resolveTrendRange(from, to, gran)
	if err != nil {
		return UploadTrend{}, err
	}

	buckets, labels := buildTrendBuckets(start, end, gran)
	result := UploadTrend{
		From:        start.Format(dailyDateFormat),
		To:          end.Format(dailyDateFormat),
		Granularity: gran,
		Buckets:     buckets,
		Labels:      labels,
		Total:       make([]*int64, len(buckets)),
		Sites:       []TrendSiteSeries{},
		Days:        int(end.Sub(start).Hours()/24) + 1,
	}
	if s == nil || s.dailyRepo == nil {
		return result, nil
	}

	rows, err := s.dailyRepo.ListRange(result.From, result.To)
	if err != nil {
		return result, err
	}
	nicknames := s.siteNicknames()

	index := make(map[string]int, len(buckets))
	for i, bucket := range buckets {
		index[bucket] = i
	}
	series := map[string]*TrendSiteSeries{}
	for _, row := range rows {
		// 没有基线的日子算不出增量：留 0，不把累计量当增量。
		if row.HasBaseline != 1 {
			continue
		}
		delta := row.Uploaded - row.BaseUploaded
		bucket := trendBucketKey(row.StatDate, gran)
		position, ok := index[bucket]
		if !ok {
			continue
		}
		if result.Total[position] == nil {
			zero := int64(0)
			result.Total[position] = &zero
		}
		*result.Total[position] += delta
		result.TotalSum += delta

		code := strings.TrimSpace(row.SiteCode)
		if code == "" {
			continue
		}
		item, ok := series[code]
		if !ok {
			item = &TrendSiteSeries{
				SiteCode: code,
				Nickname: firstNonEmpty(nicknames[code], row.Nickname, code),
				Values:   make([]*int64, len(buckets)),
			}
			series[code] = item
		}
		if item.Values[position] == nil {
			zero := int64(0)
			item.Values[position] = &zero
		}
		*item.Values[position] += delta
		item.Total += delta
	}

	for _, item := range series {
		result.Sites = append(result.Sites, *item)
	}
	// 站点很多时前端只画选中的几条，排序让「谁贡献最大」一眼可见。
	sort.Slice(result.Sites, func(i, j int) bool {
		if result.Sites[i].Total != result.Sites[j].Total {
			return result.Sites[i].Total > result.Sites[j].Total
		}
		return result.Sites[i].Nickname < result.Sites[j].Nickname
	})
	return result, nil
}

// resolveTrendFrom 处理「全部时间」：把 from=all 换成表里最早的统计日期。
// 表里还没有任何快照时返回空串（走默认跨度，图上自然是空的）。
func (s *Service) resolveTrendFrom(from string) string {
	if !strings.EqualFold(strings.TrimSpace(from), TrendRangeAll) {
		return from
	}
	if s == nil || s.dailyRepo == nil {
		return ""
	}
	earliest, err := s.dailyRepo.EarliestStatDate()
	if err != nil || earliest == "" {
		return ""
	}
	return earliest
}

// resolveTrendRange 规范化趋势查询的时间范围。
// 空 from 时按粒度取默认跨度：day = 近 30 天（含今天），month = 近 6 个自然月（对齐到月初）。
func resolveTrendRange(from, to, granularity string) (time.Time, time.Time, error) {
	location := repository.DatabaseTimeZone
	now := time.Now().In(location)
	end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	if strings.TrimSpace(to) != "" {
		parsed, err := time.ParseInLocation(dailyDateFormat, strings.TrimSpace(to), location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: to=%s（应为 YYYY-MM-DD）", ErrInvalidTrendRange, to)
		}
		end = parsed
	}

	var start time.Time
	if strings.TrimSpace(from) != "" {
		parsed, err := time.ParseInLocation(dailyDateFormat, strings.TrimSpace(from), location)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: from=%s（应为 YYYY-MM-DD）", ErrInvalidTrendRange, from)
		}
		start = parsed
	} else if granularity == TrendGranularityMonth {
		// 对齐到「当月 1 号」再往前推，保证每个桶都是完整自然月，
		// 否则首月只统计了半个月，会看成一个假的低谷。
		monthStart := time.Date(end.Year(), end.Month(), 1, 0, 0, 0, 0, location)
		start = monthStart.AddDate(0, -(trendDefaultRangeMonths - 1), 0)
	} else {
		start = end.AddDate(0, 0, -(trendDefaultRangeDays - 1))
	}

	if start.After(end) {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: 开始日期不能晚于结束日期", ErrInvalidTrendRange)
	}
	if granularity == TrendGranularityMonth {
		months := (end.Year()-start.Year())*12 + int(end.Month()-start.Month()) + 1
		if months > trendMaxRangeMonths {
			return time.Time{}, time.Time{}, fmt.Errorf("%w: 按月查询最多 %d 个月", ErrInvalidTrendRange, trendMaxRangeMonths)
		}
		return start, end, nil
	}
	if days := int(end.Sub(start).Hours()/24) + 1; days > trendMaxRangeDays {
		return time.Time{}, time.Time{}, fmt.Errorf("%w: 按天查询最多 %d 天（按月可选更长区间）", ErrInvalidTrendRange, trendMaxRangeDays)
	}
	return start, end, nil
}

// buildTrendBuckets 生成时间轴：day 逐日、month 逐月（首尾两个月都完整列出）。
func buildTrendBuckets(start, end time.Time, granularity string) ([]string, []string) {
	buckets := []string{}
	labels := []string{}
	if granularity == TrendGranularityMonth {
		cursor := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, start.Location())
		for !cursor.After(end) {
			buckets = append(buckets, cursor.Format("2006-01"))
			labels = append(labels, cursor.Format("2006-01"))
			cursor = cursor.AddDate(0, 1, 0)
		}
		return buckets, labels
	}
	for cursor := start; !cursor.After(end); cursor = cursor.AddDate(0, 0, 1) {
		buckets = append(buckets, cursor.Format(dailyDateFormat))
		labels = append(labels, cursor.Format("01-02"))
	}
	return buckets, labels
}

// trendBucketKey 把某天的快照归到对应的时间桶（天：原样；月：截到月份）。
func trendBucketKey(statDate, granularity string) string {
	trimmed := strings.TrimSpace(statDate)
	if len(trimmed) < 7 {
		return trimmed
	}
	if granularity == TrendGranularityMonth {
		return trimmed[:7]
	}
	return trimmed
}

// siteNicknames 返回站点代码 → 昵称的映射（站点可能改名，快照里的昵称只作兜底）。
func (s *Service) siteNicknames() map[string]string {
	result := map[string]string{}
	if s == nil || s.siteRepo == nil {
		return result
	}
	sites, err := s.siteRepo.ListSites("all")
	if err != nil {
		return result
	}
	for _, site := range sites {
		code := strings.TrimSpace(toString(site["site"]))
		if code == "" {
			continue
		}
		result[code] = strings.TrimSpace(toString(site["nickname"]))
	}
	return result
}
