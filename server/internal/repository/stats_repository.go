package repository

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type TrafficDeltaRow struct {
	TimeGroup    string  `gorm:"column:time_group"`
	DownloaderID string  `gorm:"column:downloader_id"`
	TotalUL      float64 `gorm:"column:total_ul"`
	TotalDL      float64 `gorm:"column:total_dl"`
}

type SpeedRow struct {
	TimeGroup    string  `gorm:"column:time_group"`
	DownloaderID string  `gorm:"column:downloader_id"`
	ULSpeed      float64 `gorm:"column:ul_speed"`
	DLSpeed      float64 `gorm:"column:dl_speed"`
}

type LatestSpeedRow struct {
	DownloaderID  string  `gorm:"column:downloader_id"`
	UploadSpeed   float64 `gorm:"column:upload_speed"`
	DownloadSpeed float64 `gorm:"column:download_speed"`
}

// TrafficStatRecord 表示一条待写入 traffic_stats 的原始采样记录。
type TrafficStatRecord struct {
	StatDatetime         time.Time
	DownloaderID         string
	Uploaded             int64
	Downloaded           int64
	UploadSpeed          int64
	DownloadSpeed        int64
	CumulativeUploaded   int64
	CumulativeDownloaded int64
}

// CumulativeSnapshot 表示某下载器最近一次累计流量快照。
type CumulativeSnapshot struct {
	CumulativeUploaded   int64
	CumulativeDownloaded int64
}

type SiteStatRow struct {
	SiteName     string `gorm:"column:site_name"`
	TotalSize    int64  `gorm:"column:total_size"`
	TorrentCount int64  `gorm:"column:torrent_count"`
}

type GroupStatRow struct {
	SiteName     string `gorm:"column:site_name"`
	GroupSuffix  string `gorm:"column:group_suffix"`
	TorrentCount int64  `gorm:"column:torrent_count"`
	TotalSize    int64  `gorm:"column:total_size"`
}

type StatsRepository struct {
	store *Store
}

func NewStatsRepository(store *Store) *StatsRepository {
	return &StatsRepository{store: store}
}

// EnsureTrafficStatsTables 确保流量原始表与小时聚合表存在。
// 参数/返回：无参数，成功返回 nil。
// 失败场景：数据库不可写或 SQL 执行失败时返回错误。
// 副作用：会在数据库中创建表结构（若不存在）。
func (r *StatsRepository) EnsureTrafficStatsTables() error {
	var trafficStatsSQL string
	var trafficStatsHourlySQL string
	var trafficStatsDailySQL string

	switch r.store.DBType {
	case "mysql":
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime DATETIME NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				upload_speed BIGINT DEFAULT 0,
				download_speed BIGINT DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime DATETIME NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				avg_upload_speed BIGINT DEFAULT 0,
				avg_download_speed BIGINT DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
		// traffic_stats_daily：按天结算的上传/下载增量，同时保存当日首末累计基线。
		// stat_date 统一用 'YYYY-MM-DD' 文本（字典序即时间序），避免 DATE 类型在
		// 各驱动下的时区二次转换；start_* 由前一天的 end_* 承接，保证日表首尾相接。
		trafficStatsDailySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_daily (
				stat_date VARCHAR(10) NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT NOT NULL DEFAULT 0,
				downloaded BIGINT NOT NULL DEFAULT 0,
				start_cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				start_cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				end_cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				end_cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				settled_at DATETIME NOT NULL,
				PRIMARY KEY (stat_date, downloader_id)
			) ENGINE=InnoDB ROW_FORMAT=Dynamic
		`
	case "postgresql":
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime TIMESTAMP NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				upload_speed BIGINT DEFAULT 0,
				download_speed BIGINT DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime TIMESTAMP NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT DEFAULT 0,
				downloaded BIGINT DEFAULT 0,
				avg_upload_speed BIGINT DEFAULT 0,
				avg_download_speed BIGINT DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsDailySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_daily (
				stat_date VARCHAR(10) NOT NULL,
				downloader_id VARCHAR(64) NOT NULL,
				uploaded BIGINT NOT NULL DEFAULT 0,
				downloaded BIGINT NOT NULL DEFAULT 0,
				start_cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				start_cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				end_cumulative_uploaded BIGINT NOT NULL DEFAULT 0,
				end_cumulative_downloaded BIGINT NOT NULL DEFAULT 0,
				settled_at TIMESTAMP NOT NULL,
				PRIMARY KEY (stat_date, downloader_id)
			)
		`
	default:
		trafficStatsSQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats (
				stat_datetime TEXT NOT NULL,
				downloader_id TEXT NOT NULL,
				uploaded INTEGER DEFAULT 0,
				downloaded INTEGER DEFAULT 0,
				upload_speed INTEGER DEFAULT 0,
				download_speed INTEGER DEFAULT 0,
				cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsHourlySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_hourly (
				stat_datetime TEXT NOT NULL,
				downloader_id TEXT NOT NULL,
				uploaded INTEGER DEFAULT 0,
				downloaded INTEGER DEFAULT 0,
				avg_upload_speed INTEGER DEFAULT 0,
				avg_download_speed INTEGER DEFAULT 0,
				samples INTEGER DEFAULT 0,
				cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				PRIMARY KEY (stat_datetime, downloader_id)
			)
		`
		trafficStatsDailySQL = `
			CREATE TABLE IF NOT EXISTS traffic_stats_daily (
				stat_date TEXT NOT NULL,
				downloader_id TEXT NOT NULL,
				uploaded INTEGER NOT NULL DEFAULT 0,
				downloaded INTEGER NOT NULL DEFAULT 0,
				start_cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				start_cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				end_cumulative_uploaded INTEGER NOT NULL DEFAULT 0,
				end_cumulative_downloaded INTEGER NOT NULL DEFAULT 0,
				settled_at TEXT NOT NULL,
				PRIMARY KEY (stat_date, downloader_id)
			)
		`
	}

	if err := r.store.DB.Exec(trafficStatsSQL).Error; err != nil {
		return err
	}
	if err := r.store.DB.Exec(trafficStatsHourlySQL).Error; err != nil {
		return err
	}
	if err := r.store.DB.Exec(trafficStatsDailySQL).Error; err != nil {
		return err
	}
	return nil
}

// dailyCumulativeEdge 表示某下载器在某个自然日内累计流量的极值。
type dailyCumulativeEdge struct {
	MaxUploaded   int64
	MaxDownloaded int64
	MinUploaded   int64
	MinDownloaded int64
}

// SettleTrafficDaily 结算指定自然日的上传/下载增量并写入 traffic_stats_daily。
// 参数/返回：day 为待结算日期（取其年月日，时区与调用方一致）；返回写入/更新的下载器条数。
// 失败场景：查询采样或写入日表失败时返回错误；当日无任何采样时返回 0 且不写行。
// 副作用：写入或覆盖 traffic_stats_daily 中该日期的记录（幂等，可重复调用）。
// 口径：start 优先承接前一日的 end_cumulative_*，缺失时退回前一日采样最大值，
// 再缺失（前一日完全停机）则退回当日采样最小值；delta 恒大于等于 0。
func (r *StatsRepository) SettleTrafficDaily(day time.Time) (int, error) {
	dayStart := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	dayEnd := dayStart.AddDate(0, 0, 1)
	prevStart := dayStart.AddDate(0, 0, -1)
	dayKey := dayStart.Format("2006-01-02")
	prevKey := prevStart.Format("2006-01-02")

	edges, err := r.queryDailyCumulativeEdges(prevStart, dayEnd)
	if err != nil {
		return 0, err
	}
	current := edges[dayKey]
	if len(current) == 0 {
		return 0, nil
	}

	prevDaily, err := r.queryDailyEndSnapshot(prevKey)
	if err != nil {
		return 0, err
	}
	prevEdges := edges[prevKey]

	upsertSQL := ""
	switch r.store.DBType {
	case "mysql":
		upsertSQL = `
			INSERT INTO traffic_stats_daily (
				stat_date, downloader_id, uploaded, downloaded,
				start_cumulative_uploaded, start_cumulative_downloaded,
				end_cumulative_uploaded, end_cumulative_downloaded, settled_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				uploaded = VALUES(uploaded),
				downloaded = VALUES(downloaded),
				start_cumulative_uploaded = VALUES(start_cumulative_uploaded),
				start_cumulative_downloaded = VALUES(start_cumulative_downloaded),
				end_cumulative_uploaded = VALUES(end_cumulative_uploaded),
				end_cumulative_downloaded = VALUES(end_cumulative_downloaded),
				settled_at = VALUES(settled_at)
		`
	default:
		upsertSQL = `
			INSERT INTO traffic_stats_daily (
				stat_date, downloader_id, uploaded, downloaded,
				start_cumulative_uploaded, start_cumulative_downloaded,
				end_cumulative_uploaded, end_cumulative_downloaded, settled_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_date, downloader_id) DO UPDATE SET
				uploaded = excluded.uploaded,
				downloaded = excluded.downloaded,
				start_cumulative_uploaded = excluded.start_cumulative_uploaded,
				start_cumulative_downloaded = excluded.start_cumulative_downloaded,
				end_cumulative_uploaded = excluded.end_cumulative_uploaded,
				end_cumulative_downloaded = excluded.end_cumulative_downloaded,
				settled_at = excluded.settled_at
		`
	}

	settledAt := time.Now().Format("2006-01-02 15:04:05")
	tx := r.store.DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	written := 0
	for downloaderID, cur := range current {
		startUL, startDL := int64(0), int64(0)
		switch {
		case prevDaily[downloaderID].CumulativeUploaded > 0 || prevDaily[downloaderID].CumulativeDownloaded > 0:
			startUL = prevDaily[downloaderID].CumulativeUploaded
			startDL = prevDaily[downloaderID].CumulativeDownloaded
		case prevEdges[downloaderID].MaxUploaded > 0 || prevEdges[downloaderID].MaxDownloaded > 0:
			startUL = prevEdges[downloaderID].MaxUploaded
			startDL = prevEdges[downloaderID].MaxDownloaded
		default:
			startUL = cur.MinUploaded
			startDL = cur.MinDownloaded
		}

		deltaUL := cur.MaxUploaded - startUL
		if deltaUL < 0 {
			deltaUL = 0
		}
		deltaDL := cur.MaxDownloaded - startDL
		if deltaDL < 0 {
			deltaDL = 0
		}

		if err := tx.Exec(
			upsertSQL,
			dayKey,
			downloaderID,
			deltaUL,
			deltaDL,
			startUL,
			startDL,
			cur.MaxUploaded,
			cur.MaxDownloaded,
			settledAt,
		).Error; err != nil {
			tx.Rollback()
			return written, err
		}
		written++
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return written, nil
}

// SettleTrafficDailyRange 按日期升序结算 [start, end] 闭区间内的每一天。
// 参数/返回：start/end 只取年月日；返回累计写入的下载器条数。
// 失败场景：任一天结算失败立即返回错误（已成功的日期保持已写入状态）。
// 副作用：逐日写入 traffic_stats_daily。
func (r *StatsRepository) SettleTrafficDailyRange(start, end time.Time) (int, error) {
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, end.Location())
	if endDay.Before(startDay) {
		return 0, nil
	}

	total := 0
	for day := startDay; !day.After(endDay); day = day.AddDate(0, 0, 1) {
		written, err := r.SettleTrafficDaily(day)
		if err != nil {
			return total, err
		}
		total += written
	}
	return total, nil
}

// QueryDailySettledThrough 返回 traffic_stats_daily 中已结算的最大日期（YYYY-MM-DD）。
// 参数/返回：表为空时返回空字符串且不报错。
// 失败场景：表不存在或查询失败时返回错误。
// 副作用：无副作用，仅读取数据库。
func (r *StatsRepository) QueryDailySettledThrough() (string, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return "", err
	}
	var maxDate sql.NullString
	if err := sqlDB.QueryRow("SELECT MAX(stat_date) FROM traffic_stats_daily").Scan(&maxDate); err != nil {
		if isMissingTableError(err) {
			return "", nil
		}
		return "", err
	}
	if !maxDate.Valid {
		return "", nil
	}
	return strings.TrimSpace(maxDate.String), nil
}

// queryDailyCumulativeEdges 查询按自然日分组、按下载器聚合的累计流量极值。
// 参数/返回：start/end 为时间窗（只取两侧边界）；返回 day(YYYY-MM-DD) -> downloader_id -> 极值。
// 失败场景：查询失败返回错误；表不存在时返回空映射。
// 副作用：无副作用，仅读取 traffic_stats 与 traffic_stats_hourly。
// 说明：原始表保留窗口有限，小时表永久保留，两表 union 才能覆盖历史日期；
// 时间边界一律用 'YYYY-MM-DD HH:MM:SS' 文本，避免 sqlite TEXT 列与 time.Time 的格式错配。
func (r *StatsRepository) queryDailyCumulativeEdges(start, end time.Time) (map[string]map[string]dailyCumulativeEdge, error) {
	dayExpr := r.dailyGroupExpr()
	query := fmt.Sprintf(`
		SELECT %s AS day_group,
		       downloader_id,
		       MAX(cumulative_uploaded) AS max_ul,
		       MAX(cumulative_downloaded) AS max_dl,
		       MIN(cumulative_uploaded) AS min_ul,
		       MIN(cumulative_downloaded) AS min_dl
		FROM (
			SELECT stat_datetime, downloader_id, cumulative_uploaded, cumulative_downloaded
			FROM traffic_stats
			WHERE stat_datetime >= ? AND stat_datetime < ?
			UNION ALL
			SELECT stat_datetime, downloader_id, cumulative_uploaded, cumulative_downloaded
			FROM traffic_stats_hourly
			WHERE stat_datetime >= ? AND stat_datetime < ?
		) combined
		GROUP BY day_group, downloader_id
	`, dayExpr)

	rows := make([]struct {
		DayGroup     string `gorm:"column:day_group"`
		DownloaderID string `gorm:"column:downloader_id"`
		MaxUL        int64  `gorm:"column:max_ul"`
		MaxDL        int64  `gorm:"column:max_dl"`
		MinUL        int64  `gorm:"column:min_ul"`
		MinDL        int64  `gorm:"column:min_dl"`
	}, 0)

	startText := start.Format("2006-01-02 15:04:05")
	endText := end.Format("2006-01-02 15:04:05")
	if err := r.store.DB.Raw(query, startText, endText, startText, endText).Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return map[string]map[string]dailyCumulativeEdge{}, nil
		}
		return nil, err
	}

	result := map[string]map[string]dailyCumulativeEdge{}
	for _, row := range rows {
		day := strings.TrimSpace(row.DayGroup)
		if day == "" {
			continue
		}
		if result[day] == nil {
			result[day] = map[string]dailyCumulativeEdge{}
		}
		result[day][row.DownloaderID] = dailyCumulativeEdge{
			MaxUploaded:   row.MaxUL,
			MaxDownloaded: row.MaxDL,
			MinUploaded:   row.MinUL,
			MinDownloaded: row.MinDL,
		}
	}
	return result, nil
}

// queryDailyEndSnapshot 查询指定日期已结算的日表末值（作为次日起点基线）。
// 参数/返回：dateKey 为 'YYYY-MM-DD'；返回 downloader_id -> 当日 end 累计。
// 失败场景：查询失败返回错误；表不存在时返回空映射。
// 副作用：无副作用，仅读取 traffic_stats_daily。
func (r *StatsRepository) queryDailyEndSnapshot(dateKey string) (map[string]CumulativeSnapshot, error) {
	rows := make([]struct {
		DownloaderID            string `gorm:"column:downloader_id"`
		EndCumulativeUploaded   int64  `gorm:"column:end_cumulative_uploaded"`
		EndCumulativeDownloaded int64  `gorm:"column:end_cumulative_downloaded"`
	}, 0)
	query := `
		SELECT downloader_id, end_cumulative_uploaded, end_cumulative_downloaded
		FROM traffic_stats_daily
		WHERE stat_date = ?
	`
	if err := r.store.DB.Raw(query, dateKey).Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return map[string]CumulativeSnapshot{}, nil
		}
		return nil, err
	}

	result := map[string]CumulativeSnapshot{}
	for _, row := range rows {
		result[row.DownloaderID] = CumulativeSnapshot{
			CumulativeUploaded:   row.EndCumulativeUploaded,
			CumulativeDownloaded: row.EndCumulativeDownloaded,
		}
	}
	return result, nil
}

// QueryLatestCumulativeByDownloader 查询每个下载器最近一条累计流量记录。
// 参数/返回：返回 downloader_id -> 累计值 的映射。
// 失败场景：查询失败时返回错误；表不存在时返回空映射且不报错。
// 副作用：无副作用，仅读取数据库。
func (r *StatsRepository) QueryLatestCumulativeByDownloader() (map[string]CumulativeSnapshot, error) {
	rows := make([]struct {
		DownloaderID         string `gorm:"column:downloader_id"`
		CumulativeUploaded   int64  `gorm:"column:cumulative_uploaded"`
		CumulativeDownloaded int64  `gorm:"column:cumulative_downloaded"`
	}, 0)

	query := `
		SELECT t.downloader_id, t.cumulative_uploaded, t.cumulative_downloaded
		FROM traffic_stats t
		JOIN (
			SELECT downloader_id, MAX(stat_datetime) AS max_dt
			FROM traffic_stats
			WHERE cumulative_uploaded > 0 OR cumulative_downloaded > 0
			GROUP BY downloader_id
		) last ON t.downloader_id = last.downloader_id AND t.stat_datetime = last.max_dt
	`
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		if isMissingTableError(err) {
			return map[string]CumulativeSnapshot{}, nil
		}
		return nil, err
	}

	result := map[string]CumulativeSnapshot{}
	for _, row := range rows {
		if strings.TrimSpace(row.DownloaderID) == "" {
			continue
		}
		result[row.DownloaderID] = CumulativeSnapshot{
			CumulativeUploaded:   row.CumulativeUploaded,
			CumulativeDownloaded: row.CumulativeDownloaded,
		}
	}
	return result, nil
}

// UpsertTrafficStats 批量写入原始流量采样数据。
// 参数/返回：records 为采样批次，返回成功写入条数。
// 失败场景：事务失败或 SQL 执行异常时回滚并返回错误。
// 副作用：写入/更新 traffic_stats 表数据。
func (r *StatsRepository) UpsertTrafficStats(records []TrafficStatRecord) (int, error) {
	if len(records) == 0 {
		return 0, nil
	}

	insertSQL := ""
	switch r.store.DBType {
	case "mysql":
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				uploaded = VALUES(uploaded),
				downloaded = VALUES(downloaded),
				upload_speed = VALUES(upload_speed),
				download_speed = VALUES(download_speed),
				cumulative_uploaded = VALUES(cumulative_uploaded),
				cumulative_downloaded = VALUES(cumulative_downloaded)
		`
	case "postgresql":
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = EXCLUDED.uploaded,
				downloaded = EXCLUDED.downloaded,
				upload_speed = EXCLUDED.upload_speed,
				download_speed = EXCLUDED.download_speed,
				cumulative_uploaded = EXCLUDED.cumulative_uploaded,
				cumulative_downloaded = EXCLUDED.cumulative_downloaded
		`
	default:
		insertSQL = `
			INSERT INTO traffic_stats (
				stat_datetime, downloader_id, uploaded, downloaded,
				upload_speed, download_speed, cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = excluded.uploaded,
				downloaded = excluded.downloaded,
				upload_speed = excluded.upload_speed,
				download_speed = excluded.download_speed,
				cumulative_uploaded = excluded.cumulative_uploaded,
				cumulative_downloaded = excluded.cumulative_downloaded
		`
	}

	tx := r.store.DB.Begin()
	if tx.Error != nil {
		return 0, tx.Error
	}

	for _, record := range records {
		timestamp := record.StatDatetime.Format("2006-01-02 15:04:05")
		if err := tx.Exec(
			insertSQL,
			timestamp,
			record.DownloaderID,
			record.Uploaded,
			record.Downloaded,
			record.UploadSpeed,
			record.DownloadSpeed,
			record.CumulativeUploaded,
			record.CumulativeDownloaded,
		).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(records), nil
}

// AggregateHourlyTraffic 将历史原始流量按小时聚合到 traffic_stats_hourly，并清理旧原始记录。
// 参数/返回：retentionHours 为原始数据保留时长，返回聚合条数与清理条数。
// 失败场景：聚合查询、upsert 或删除失败时回滚并返回错误。
// 副作用：写入 traffic_stats_hourly 并删除 traffic_stats 中的旧数据。
func (r *StatsRepository) AggregateHourlyTraffic(retentionHours int) (int, int64, error) {
	if retentionHours <= 0 {
		retentionHours = 48
	}

	now := time.Now()
	cutoff := now.Add(-time.Duration(retentionHours) * time.Hour)
	safeCutoff := now.AddDate(0, 0, -3)
	safeCutoff = time.Date(safeCutoff.Year(), safeCutoff.Month(), safeCutoff.Day(), 0, 0, 0, 0, safeCutoff.Location())
	if cutoff.After(safeCutoff) {
		cutoff = safeCutoff
	}
	cutoffText := cutoff.Format("2006-01-02 15:04:05")
	groupExpr := r.hourlyGroupExpr()

	aggregatedRows := make([]struct {
		HourGroup        string  `gorm:"column:hour_group"`
		DownloaderID     string  `gorm:"column:downloader_id"`
		TotalUploaded    float64 `gorm:"column:total_uploaded"`
		TotalDownloaded  float64 `gorm:"column:total_downloaded"`
		AvgUploadSpeed   float64 `gorm:"column:avg_upload_speed"`
		AvgDownloadSpeed float64 `gorm:"column:avg_download_speed"`
		Samples          int64   `gorm:"column:samples"`
	}, 0)

	aggregateSQL := ""
	switch r.store.DBType {
	case "postgresql":
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				GREATEST(0, (MAX(cumulative_uploaded) - MIN(cumulative_uploaded))::bigint) AS total_uploaded,
				GREATEST(0, (MAX(cumulative_downloaded) - MIN(cumulative_downloaded))::bigint) AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	case "mysql":
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				GREATEST(0, MAX(cumulative_uploaded) - MIN(cumulative_uploaded)) AS total_uploaded,
				GREATEST(0, MAX(cumulative_downloaded) - MIN(cumulative_downloaded)) AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	default:
		aggregateSQL = fmt.Sprintf(`
			SELECT
				%s AS hour_group,
				downloader_id,
				CASE WHEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded) > 0
					THEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded)
					ELSE 0 END AS total_uploaded,
				CASE WHEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded) > 0
					THEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded)
					ELSE 0 END AS total_downloaded,
				AVG(upload_speed) AS avg_upload_speed,
				AVG(download_speed) AS avg_download_speed,
				COUNT(*) AS samples
			FROM traffic_stats
			WHERE stat_datetime < ?
			GROUP BY hour_group, downloader_id
		`, groupExpr)
	}
	if err := r.store.DB.Raw(aggregateSQL, cutoffText).Scan(&aggregatedRows).Error; err != nil {
		if isMissingTableError(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}
	if len(aggregatedRows) == 0 {
		return 0, 0, nil
	}

	cumulativeRows := make([]struct {
		HourGroup                 string `gorm:"column:hour_group"`
		DownloaderID              string `gorm:"column:downloader_id"`
		FinalCumulativeUploaded   int64  `gorm:"column:final_cumulative_uploaded"`
		FinalCumulativeDownloaded int64  `gorm:"column:final_cumulative_downloaded"`
	}, 0)
	cumulativeSQL := fmt.Sprintf(`
		SELECT
			%s AS hour_group,
			downloader_id,
			MAX(cumulative_uploaded) AS final_cumulative_uploaded,
			MAX(cumulative_downloaded) AS final_cumulative_downloaded
		FROM traffic_stats
		WHERE stat_datetime < ?
		GROUP BY hour_group, downloader_id
	`, groupExpr)
	if err := r.store.DB.Raw(cumulativeSQL, cutoffText).Scan(&cumulativeRows).Error; err != nil {
		if isMissingTableError(err) {
			return 0, 0, nil
		}
		return 0, 0, err
	}

	type cumulativeKey struct {
		HourGroup    string
		DownloaderID string
	}
	cumulativeMap := map[cumulativeKey]CumulativeSnapshot{}
	for _, row := range cumulativeRows {
		key := cumulativeKey{HourGroup: row.HourGroup, DownloaderID: row.DownloaderID}
		cumulativeMap[key] = CumulativeSnapshot{
			CumulativeUploaded:   row.FinalCumulativeUploaded,
			CumulativeDownloaded: row.FinalCumulativeDownloaded,
		}
	}

	upsertSQL := ""
	switch r.store.DBType {
	case "mysql":
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				uploaded = uploaded + VALUES(uploaded),
				downloaded = downloaded + VALUES(downloaded),
				avg_upload_speed = ((avg_upload_speed * samples) + (VALUES(avg_upload_speed) * VALUES(samples))) / (samples + VALUES(samples)),
				avg_download_speed = ((avg_download_speed * samples) + (VALUES(avg_download_speed) * VALUES(samples))) / (samples + VALUES(samples)),
				samples = samples + VALUES(samples),
				cumulative_uploaded = GREATEST(cumulative_uploaded, VALUES(cumulative_uploaded)),
				cumulative_downloaded = GREATEST(cumulative_downloaded, VALUES(cumulative_downloaded))
		`
	case "postgresql":
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = traffic_stats_hourly.uploaded + EXCLUDED.uploaded,
				downloaded = traffic_stats_hourly.downloaded + EXCLUDED.downloaded,
				avg_upload_speed = ((traffic_stats_hourly.avg_upload_speed * traffic_stats_hourly.samples) + (EXCLUDED.avg_upload_speed * EXCLUDED.samples)) / (traffic_stats_hourly.samples + EXCLUDED.samples),
				avg_download_speed = ((traffic_stats_hourly.avg_download_speed * traffic_stats_hourly.samples) + (EXCLUDED.avg_download_speed * EXCLUDED.samples)) / (traffic_stats_hourly.samples + EXCLUDED.samples),
				samples = traffic_stats_hourly.samples + EXCLUDED.samples,
				cumulative_uploaded = GREATEST(traffic_stats_hourly.cumulative_uploaded, EXCLUDED.cumulative_uploaded),
				cumulative_downloaded = GREATEST(traffic_stats_hourly.cumulative_downloaded, EXCLUDED.cumulative_downloaded)
		`
	default:
		upsertSQL = `
			INSERT INTO traffic_stats_hourly (
				stat_datetime, downloader_id, uploaded, downloaded,
				avg_upload_speed, avg_download_speed, samples,
				cumulative_uploaded, cumulative_downloaded
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(stat_datetime, downloader_id) DO UPDATE SET
				uploaded = traffic_stats_hourly.uploaded + excluded.uploaded,
				downloaded = traffic_stats_hourly.downloaded + excluded.downloaded,
				avg_upload_speed = ((traffic_stats_hourly.avg_upload_speed * traffic_stats_hourly.samples) + (excluded.avg_upload_speed * excluded.samples)) / (traffic_stats_hourly.samples + excluded.samples),
				avg_download_speed = ((traffic_stats_hourly.avg_download_speed * traffic_stats_hourly.samples) + (excluded.avg_download_speed * excluded.samples)) / (traffic_stats_hourly.samples + excluded.samples),
				samples = traffic_stats_hourly.samples + excluded.samples,
				cumulative_uploaded = MAX(traffic_stats_hourly.cumulative_uploaded, excluded.cumulative_uploaded),
				cumulative_downloaded = MAX(traffic_stats_hourly.cumulative_downloaded, excluded.cumulative_downloaded)
		`
	}

	tx := r.store.DB.Begin()
	if tx.Error != nil {
		return 0, 0, tx.Error
	}

	for _, row := range aggregatedRows {
		key := cumulativeKey{HourGroup: row.HourGroup, DownloaderID: row.DownloaderID}
		cumulative := cumulativeMap[key]
		if err := tx.Exec(
			upsertSQL,
			row.HourGroup,
			row.DownloaderID,
			int64(row.TotalUploaded),
			int64(row.TotalDownloaded),
			int64(row.AvgUploadSpeed),
			int64(row.AvgDownloadSpeed),
			row.Samples,
			cumulative.CumulativeUploaded,
			cumulative.CumulativeDownloaded,
		).Error; err != nil {
			tx.Rollback()
			return 0, 0, err
		}
	}

	deleteResult := tx.Exec("DELETE FROM traffic_stats WHERE stat_datetime < ?", cutoffText)
	if deleteResult.Error != nil {
		tx.Rollback()
		return 0, 0, deleteResult.Error
	}

	if err := tx.Commit().Error; err != nil {
		return 0, 0, err
	}
	return len(aggregatedRows), deleteResult.RowsAffected, nil
}

func (r *StatsRepository) QueryTrafficDeltas(start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	return r.queryTrafficDeltasTable("traffic_stats", start, end, format)
}

func (r *StatsRepository) QueryTrafficDeltasHourly(start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	return r.queryTrafficDeltasTable("traffic_stats_hourly", start, end, format)
}

func (r *StatsRepository) queryTrafficDeltasTable(table string, start, end time.Time, format string) ([]TrafficDeltaRow, error) {
	groupExpr := r.timeGroupExpr(format)
	var query string
	switch r.store.DBType {
	case "postgresql":
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       GREATEST(0, (MAX(cumulative_uploaded) - MIN(cumulative_uploaded))::bigint) AS total_ul,
			       GREATEST(0, (MAX(cumulative_downloaded) - MIN(cumulative_downloaded))::bigint) AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	case "mysql":
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       GREATEST(0, MAX(cumulative_uploaded) - MIN(cumulative_uploaded)) AS total_ul,
			       GREATEST(0, MAX(cumulative_downloaded) - MIN(cumulative_downloaded)) AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	default:
		query = fmt.Sprintf(`
			SELECT %s AS time_group,
			       downloader_id,
			       CASE WHEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded) > 0
			            THEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded)
			            ELSE 0 END AS total_ul,
			       CASE WHEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded) > 0
			            THEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded)
			            ELSE 0 END AS total_dl
			FROM %s
			WHERE stat_datetime >= ? AND stat_datetime < ?
			GROUP BY time_group, downloader_id
			ORDER BY time_group
		`, groupExpr, table)
	}

	rows := make([]TrafficDeltaRow, 0)
	if err := r.store.DB.Raw(query, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QuerySpeedAverages(start, end time.Time, format string) ([]SpeedRow, error) {
	return r.querySpeedAveragesTable("traffic_stats", "upload_speed", "download_speed", start, end, format)
}

func (r *StatsRepository) QuerySpeedAveragesHourly(start, end time.Time, format string) ([]SpeedRow, error) {
	return r.querySpeedAveragesTable("traffic_stats_hourly", "avg_upload_speed", "avg_download_speed", start, end, format)
}

func (r *StatsRepository) querySpeedAveragesTable(table, ulCol, dlCol string, start, end time.Time, format string) ([]SpeedRow, error) {
	groupExpr := r.timeGroupExpr(format)
	query := fmt.Sprintf(`
		SELECT %s AS time_group,
		       downloader_id,
		       AVG(%s) AS ul_speed,
		       AVG(%s) AS dl_speed
		FROM %s
		WHERE stat_datetime >= ? AND stat_datetime < ?
		GROUP BY time_group, downloader_id
		ORDER BY time_group
	`, groupExpr, ulCol, dlCol, table)

	rows := make([]SpeedRow, 0)
	if err := r.store.DB.Raw(query, start, end).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QueryLatestSpeeds() ([]LatestSpeedRow, error) {
	query := `
		SELECT t1.downloader_id,
		       t1.upload_speed,
		       t1.download_speed
		FROM traffic_stats t1
		JOIN (
			SELECT downloader_id, MAX(stat_datetime) AS max_dt
			FROM traffic_stats
			GROUP BY downloader_id
		) t2 ON t1.downloader_id = t2.downloader_id AND t1.stat_datetime = t2.max_dt
	`
	rows := make([]LatestSpeedRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QuerySiteStats() ([]SiteStatRow, error) {
	query := `
		SELECT sites AS site_name,
		       SUM(size) AS total_size,
		       COUNT(name) AS torrent_count
		FROM (
			SELECT DISTINCT name, size, sites
			FROM torrents
			WHERE sites IS NOT NULL AND sites != '' AND (is_hidden = 0 OR is_hidden IS NULL)
		) unique_torrents
		GROUP BY sites
		ORDER BY sites
	`
	rows := make([]SiteStatRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) QueryGroupStats(siteName string) ([]GroupStatRow, error) {
	groupColumn := r.store.GroupColumn()
	if siteName != "" {
		var query string
		switch r.store.DBType {
		case "mysql":
			query = fmt.Sprintf(`
				SELECT s.nickname AS site_name,
				       ut.%[1]s AS group_suffix,
				       COUNT(ut.name) AS torrent_count,
				       SUM(ut.size) AS total_size
					FROM (
						SELECT name, %[1]s, MAX(size) AS size
						FROM torrents
						WHERE %[1]s IS NOT NULL AND %[1]s != '' AND sites = ? AND (is_hidden = 0 OR is_hidden IS NULL)
						GROUP BY name, %[1]s
					) AS ut
				JOIN sites AS s ON FIND_IN_SET(ut.%[1]s, s.%[1]s) > 0
				GROUP BY s.nickname, ut.%[1]s
				ORDER BY torrent_count DESC
			`, groupColumn)
		default:
			// sqlite / postgresql
			query = fmt.Sprintf(`
				SELECT s.nickname AS site_name,
				       ut.%[1]s AS group_suffix,
				       COUNT(ut.name) AS torrent_count,
				       SUM(ut.size) AS total_size
					FROM (
						SELECT name, %[1]s, MAX(size) AS size
						FROM torrents
						WHERE %[1]s IS NOT NULL AND %[1]s != '' AND sites = ? AND (is_hidden = 0 OR is_hidden IS NULL)
						GROUP BY name, %[1]s
					) AS ut
				JOIN sites AS s ON (',' || s.%[1]s || ',' LIKE '%%,' || ut.%[1]s || ',%%')
				GROUP BY s.nickname, ut.%[1]s
				ORDER BY torrent_count DESC
			`, groupColumn)
		}

		rows := make([]GroupStatRow, 0)
		if err := r.store.DB.Raw(query, siteName).Scan(&rows).Error; err != nil {
			return nil, err
		}
		return rows, nil
	}

	var aggExpr string
	switch r.store.DBType {
	case "mysql":
		// Match Python: GROUP_CONCAT(DISTINCT ut.`group` ORDER BY ut.`group` SEPARATOR ', ')
		aggExpr = fmt.Sprintf("GROUP_CONCAT(DISTINCT ut.%s ORDER BY ut.%s SEPARATOR ', ')", groupColumn, groupColumn)
	case "postgresql":
		aggExpr = fmt.Sprintf("STRING_AGG(DISTINCT ut.%s, ', ')", groupColumn)
	default:
		// sqlite
		aggExpr = fmt.Sprintf("GROUP_CONCAT(DISTINCT ut.%s)", groupColumn)
	}

	var query string
	switch r.store.DBType {
	case "mysql":
		query = fmt.Sprintf(`
			SELECT s.nickname AS site_name,
			       %s AS group_suffix,
			       COUNT(ut.name) AS torrent_count,
			       SUM(ut.size) AS total_size
				FROM (
					SELECT name, %s, MAX(size) AS size
					FROM torrents
					WHERE %s IS NOT NULL AND %s != '' AND (is_hidden = 0 OR is_hidden IS NULL)
					GROUP BY name, %s
				) AS ut
			JOIN sites AS s ON FIND_IN_SET(ut.%s, s.%s) > 0
			GROUP BY s.nickname
			ORDER BY s.nickname
		`, aggExpr, groupColumn, groupColumn, groupColumn, groupColumn, groupColumn, groupColumn)
	default:
		// sqlite / postgresql
		// NOTE: fmt.Sprintf treats '%' as formatting, so the LIKE pattern uses '%%' to emit literal '%'.
		query = fmt.Sprintf(`
			SELECT s.nickname AS site_name,
			       %s AS group_suffix,
			       COUNT(ut.name) AS torrent_count,
			       SUM(ut.size) AS total_size
				FROM (
					SELECT name, %s, MAX(size) AS size
					FROM torrents
					WHERE %s IS NOT NULL AND %s != '' AND (is_hidden = 0 OR is_hidden IS NULL)
					GROUP BY name, %s
				) AS ut
			JOIN sites AS s ON (',' || s.%s || ',' LIKE '%%,' || ut.%s || ',%%')
			GROUP BY s.nickname
			ORDER BY s.nickname
		`, aggExpr, groupColumn, groupColumn, groupColumn, groupColumn, groupColumn, groupColumn)
	}

	rows := make([]GroupStatRow, 0)
	if err := r.store.DB.Raw(query).Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func (r *StatsRepository) timeGroupExpr(format string) string {
	switch r.store.DBType {
	case "mysql":
		mysqlFormat := strings.ReplaceAll(format, "%M", "%i")
		return "DATE_FORMAT(stat_datetime, '" + mysqlFormat + "')"
	case "postgresql":
		pgFormat := format
		pgFormat = strings.ReplaceAll(pgFormat, "%Y", "YYYY")
		pgFormat = strings.ReplaceAll(pgFormat, "%m", "MM")
		pgFormat = strings.ReplaceAll(pgFormat, "%d", "DD")
		pgFormat = strings.ReplaceAll(pgFormat, "%H", "HH24")
		pgFormat = strings.ReplaceAll(pgFormat, "%M", "MI")
		pgFormat = strings.ReplaceAll(pgFormat, "%S", "SS")
		return "TO_CHAR(stat_datetime, '" + pgFormat + "')"
	default:
		return "STRFTIME('" + format + "', stat_datetime)"
	}
}

func (r *StatsRepository) hourlyGroupExpr() string {
	switch r.store.DBType {
	case "mysql":
		return "DATE_FORMAT(stat_datetime, '%Y-%m-%d %H:00:00')"
	case "postgresql":
		return "TO_CHAR(stat_datetime, 'YYYY-MM-DD HH24:00:00')"
	default:
		return "STRFTIME('%Y-%m-%d %H:00:00', stat_datetime)"
	}
}

// dailyGroupExpr 返回按自然日分组的表达式，统一输出 'YYYY-MM-DD'。
// 参数/返回：无参数，按数据库方言返回对应格式化表达式。
// 失败场景：无。
// 副作用：无副作用，仅拼接 SQL 片段。
func (r *StatsRepository) dailyGroupExpr() string {
	switch r.store.DBType {
	case "mysql":
		return "DATE_FORMAT(stat_datetime, '%Y-%m-%d')"
	case "postgresql":
		return "TO_CHAR(stat_datetime, 'YYYY-MM-DD')"
	default:
		return "STRFTIME('%Y-%m-%d', stat_datetime)"
	}
}
