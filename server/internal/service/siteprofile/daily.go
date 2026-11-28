package siteprofile

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

// ErrInvalidDailyDate 表示传入的日期参数不合法，接口层据此返回 400（而不是 500）。
var ErrInvalidDailyDate = errors.New("日期参数不合法")

// 本文件实现「按天统计各站点增量」：上传量、下载量、做种积分、魔力值。
//
// 数据链条：
//  1. 每次刷新抓到某站数据后，往 site_profile_daily 写一条当天采样（当天多次刷新只更新最新值，不新增行）；
//  2. 当天第一次写入时，把「上一条更早的快照」锁定为日初基线（Base*），当天不再变动；
//  3. 查询当天增量时：增量 = 当天最新值 - 基线值。
//
// 为什么不直接用「今天 - 昨天」：服务不一定每天都跑（用户可能几天才点一次刷新），
// 也不一定在 0 点前后各跑一次。以「上一条更早的采样」为基线更稳，且把基线日期一并返回，
// 前端能明确提示「这条增量实际跨了几天」，不会让人误以为都是当天的量。

// dailyDateFormat 是日快照的日期格式（同时也是 site_profile_daily.stat_date 的存储格式）。
const dailyDateFormat = "2006-01-02"

// DailyStat 是接口输出的一行「当日增量」。
type DailyStat struct {
	SiteCode   string  `json:"site_code"`
	Nickname   string  `json:"nickname"`
	StatDate   string  `json:"stat_date"`
	Uploaded   int64   `json:"uploaded"`
	Downloaded int64   `json:"downloaded"`
	Bonus      float64 `json:"bonus"`
	SeedBonus  float64 `json:"seed_bonus"`
	Seeding    int64   `json:"seeding"`

	// 增量字段用指针：nil 表示「当天没有可比基线」（首日启用），前端显示「—」，
	// 而不是把累计量当成当天增量误导用户。
	UploadedDelta   *int64   `json:"uploaded_delta"`
	DownloadedDelta *int64   `json:"downloaded_delta"`
	BonusDelta      *float64 `json:"bonus_delta"`
	SeedBonusDelta  *float64 `json:"seed_bonus_delta"`
	SeedingDelta    *int64   `json:"seeding_delta"`

	// BaseDate 为基线所属日期；与 StatDate 不同天时说明这条增量跨了多天。
	BaseDate    string `json:"base_date"`
	HasBaseline bool   `json:"has_baseline"`
	Samples     int    `json:"samples"`
	LastAt      string `json:"last_at"`
}

// DailySummary 是「当日增量」接口的完整返回：日期 + 各行 + 合计。
type DailySummary struct {
	Date        string      `json:"date"`
	HasBaseline bool        `json:"has_baseline"`
	Rows        []DailyStat `json:"rows"`
	// 合计只累加有基线的站点，避免把「无法计算」的站点当 0 拉低总量。
	TotalUploadedDelta   int64   `json:"total_uploaded_delta"`
	TotalDownloadedDelta int64   `json:"total_downloaded_delta"`
	TotalBonusDelta      float64 `json:"total_bonus_delta"`
	TotalSeedBonusDelta  float64 `json:"total_seed_bonus_delta"`
}

// recordDailySample 把一次成功抓取的结果写成当天快照。
// 参数/返回：target 提供站点代码与昵称，profile 为本次抓到的累计值；无返回值。
// 失败场景：写入失败只记日志，不影响主流程（快照是附属统计，不该让刷新失败）。
// 副作用：写 site_profile_daily。
func (s *Service) recordDailySample(target SiteTarget, profile Profile) {
	if s == nil || s.dailyRepo == nil {
		return
	}
	now := time.Now().In(repository.DatabaseTimeZone)
	sample := repository.SiteProfileDaily{
		SiteCode:   target.SiteCode,
		Nickname:   target.Nickname,
		StatDate:   now.Format(dailyDateFormat),
		Uploaded:   profile.Uploaded,
		Downloaded: profile.Downloaded,
		Bonus:      profile.Bonus,
		SeedBonus:  profile.SeedBonus,
		Ratio:      profile.Ratio,
		Seeding:    profile.Seeding,
		Leeching:   profile.Leeching,
	}
	if _, err := s.dailyRepo.SaveSample(sample); err != nil {
		logx.Warnf(logModule, "写入站点日快照失败 site=%s err=%v", target.Nickname, err)
	}
}

// Daily 返回指定日期各站点的当日增量。
// 参数/返回：date 支持 YYYY-MM-DD（空串表示今天）、today、yesterday；返回按上传增量降序排列的汇总。
// 失败场景：日期格式非法、数据库查询失败。
// 副作用：无（只读）。
func (s *Service) Daily(date string) (DailySummary, error) {
	statDate, err := resolveDailyDate(date)
	if err != nil {
		return DailySummary{}, err
	}
	summary := DailySummary{Date: statDate, Rows: []DailyStat{}}
	if s == nil || s.dailyRepo == nil {
		return summary, nil
	}

	rows, err := s.dailyRepo.ListByDate(statDate)
	if err != nil {
		return summary, err
	}

	// 站点昵称以 sites 表为真源（站点可能改名），快照里的昵称只作兜底。
	nicknames := map[string]string{}
	if s.siteRepo != nil {
		if sites, err := s.siteRepo.ListSites("all"); err == nil {
			for _, site := range sites {
				code := strings.TrimSpace(toString(site["site"]))
				if code == "" {
					continue
				}
				nicknames[code] = strings.TrimSpace(toString(site["nickname"]))
			}
		}
	}

	for _, row := range rows {
		stat := DailyStat{
			SiteCode:    row.SiteCode,
			Nickname:    firstNonEmpty(nicknames[row.SiteCode], row.Nickname, row.SiteCode),
			StatDate:    row.StatDate,
			Uploaded:    row.Uploaded,
			Downloaded:  row.Downloaded,
			Bonus:       row.Bonus,
			SeedBonus:   row.SeedBonus,
			Seeding:     row.Seeding,
			BaseDate:    row.BaseDate,
			HasBaseline: row.HasBaseline == 1,
			Samples:     row.Samples,
			LastAt:      row.LastAt,
		}
		if stat.HasBaseline {
			uploadedDelta := row.Uploaded - row.BaseUploaded
			downloadedDelta := row.Downloaded - row.BaseDownloaded
			bonusDelta := row.Bonus - row.BaseBonus
			seedBonusDelta := row.SeedBonus - row.BaseSeedBonus
			seedingDelta := row.Seeding - row.BaseSeeding
			stat.UploadedDelta = &uploadedDelta
			stat.DownloadedDelta = &downloadedDelta
			stat.BonusDelta = &bonusDelta
			stat.SeedBonusDelta = &seedBonusDelta
			stat.SeedingDelta = &seedingDelta

			summary.HasBaseline = true
			summary.TotalUploadedDelta += uploadedDelta
			summary.TotalDownloadedDelta += downloadedDelta
			summary.TotalBonusDelta += bonusDelta
			summary.TotalSeedBonusDelta += seedBonusDelta
		}
		summary.Rows = append(summary.Rows, stat)
	}

	sortDailyRows(summary.Rows)
	return summary, nil
}

// resolveDailyDate 把日期参数规范化成 YYYY-MM-DD。
// 支持空串（今天）、today、yesterday 与标准日期，便于前端下拉切换。
func resolveDailyDate(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	now := time.Now().In(repository.DatabaseTimeZone)
	switch strings.ToLower(trimmed) {
	case "", "today":
		return now.Format(dailyDateFormat), nil
	case "yesterday":
		return now.AddDate(0, 0, -1).Format(dailyDateFormat), nil
	}
	parsed, err := time.ParseInLocation(dailyDateFormat, trimmed, repository.DatabaseTimeZone)
	if err != nil {
		return "", fmt.Errorf("%w: %s（应为 YYYY-MM-DD）", ErrInvalidDailyDate, raw)
	}
	return parsed.Format(dailyDateFormat), nil
}

// sortDailyRows 按上传增量降序排列，无基线的排后面，再按下载增量与昵称兜底。
// 参数/返回：rows 原地排序。
// 失败场景：无。
// 副作用：修改 rows 顺序。
func sortDailyRows(rows []DailyStat) {
	deltaOf := func(row DailyStat) int64 {
		if row.UploadedDelta == nil {
			return -1
		}
		return *row.UploadedDelta
	}
	for i := 1; i < len(rows); i++ {
		current := rows[i]
		j := i - 1
		for j >= 0 {
			left := deltaOf(rows[j])
			right := deltaOf(current)
			if left > right || (left == right && rows[j].Nickname <= current.Nickname) {
				break
			}
			rows[j+1] = rows[j]
			j--
		}
		rows[j+1] = current
	}
}
