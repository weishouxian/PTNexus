package repository

import (
	"fmt"
	"strings"
	"time"
)

type TorrentRepository struct {
	store *Store
}

func NewTorrentRepository(store *Store) *TorrentRepository {
	return &TorrentRepository{store: store}
}

func (r *TorrentRepository) DistinctPaths() ([]string, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query("SELECT DISTINCT save_path FROM torrents WHERE save_path IS NOT NULL AND save_path != '' AND (is_hidden = 0 OR is_hidden IS NULL) ORDER BY save_path")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make([]string, 0)
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// TrafficTotals 返回每个下载器的累计上传/下载总量。
// 参数/返回：返回 downloader_id -> {total_dl,total_ul} 映射。
// 失败场景：查询失败返回错误；小时表缺失时自动回退为仅查原始采样表。
// 副作用：无副作用，仅读取数据库。
// 说明：原始表只保留近几天，累计值必须同时看小时聚合表，否则清档后会从历史峰值掉到近几天峰值。
func (r *TorrentRepository) TrafficTotals() (map[string]map[string]int64, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query(`
		SELECT downloader_id,
		       MAX(cumulative_downloaded) as total_dl,
		       MAX(cumulative_uploaded) as total_ul
		FROM (
			SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
			FROM traffic_stats
			UNION ALL
			SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
			FROM traffic_stats_hourly
		) combined
		WHERE cumulative_downloaded > 0 OR cumulative_uploaded > 0
		GROUP BY downloader_id
	`)
	if err != nil && isMissingTableError(err) {
		rows, err = sqlDB.Query(`
			SELECT downloader_id,
			       MAX(cumulative_downloaded) as total_dl,
			       MAX(cumulative_uploaded) as total_ul
			FROM traffic_stats
			WHERE cumulative_downloaded > 0 OR cumulative_uploaded > 0
			GROUP BY downloader_id
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := map[string]map[string]int64{}
	for rows.Next() {
		var downloaderID string
		var totalDL any
		var totalUL any
		if err := rows.Scan(&downloaderID, &totalDL, &totalUL); err != nil {
			return nil, err
		}
		result[downloaderID] = map[string]int64{
			"total_dl": normalizeToInt64(totalDL),
			"total_ul": normalizeToInt64(totalUL),
		}
	}
	return result, nil
}

// TrafficToday 返回每个下载器「今天」的上传/下载增量。
// 参数/返回：返回 downloader_id -> {today_dl,today_ul} 映射。
// 失败场景：查询失败返回错误；日表缺失时起点退回今天第一条采样。
// 副作用：无副作用，仅读取数据库。
// 说明：起点取昨日日结算的 end 累计（链式基线），终点取今天最新一条采样，
// 这样能把零点到首次采样之间的增量算进来，避免旧口径（当日 MAX-MIN）漏算。
func (r *TorrentRepository) TrafficToday() (map[string]map[string]int64, error) {
	return r.queryTodayTraffic(time.Now())
}

// queryTodayTraffic 按「当前累计 - 今日起点」计算当天流量。
// 参数/返回：now 决定所属自然日；返回 downloader_id -> {today_dl,today_ul}。
// 失败场景：连接或查询失败时返回错误（日表缺失视为空基线，不报错）。
// 副作用：无副作用，仅读取 traffic_stats 与 traffic_stats_daily。
func (r *TorrentRepository) queryTodayTraffic(now time.Time) (map[string]map[string]int64, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}

	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterdayKey := todayStart.AddDate(0, 0, -1).Format("2006-01-02")
	sinceText := todayStart.Format("2006-01-02 15:04:05")

	// 1) 昨日日结算基线：start 优先承接前一日 end。
	startMap := map[string][2]int64{}
	startRows, err := sqlDB.Query(`
		SELECT downloader_id, end_cumulative_uploaded, end_cumulative_downloaded
		FROM traffic_stats_daily
		WHERE stat_date = ?
	`, yesterdayKey)
	if err != nil {
		if !isMissingTableError(err) {
			return nil, err
		}
	} else {
		for startRows.Next() {
			var downloaderID string
			var upl, dnl int64
			if scanErr := startRows.Scan(&downloaderID, &upl, &dnl); scanErr != nil {
				startRows.Close()
				return nil, scanErr
			}
			startMap[downloaderID] = [2]int64{upl, dnl}
		}
		startRows.Close()
	}

	// 2) 今天最新一条采样 = 当前累计；同时取首条采样作为日表缺失时的兜底起点。
	latestMap := map[string][2]int64{}
	latestRows, err := sqlDB.Query(`
		SELECT t.downloader_id, t.cumulative_uploaded, t.cumulative_downloaded
		FROM traffic_stats t
		JOIN (
			SELECT downloader_id, MAX(stat_datetime) AS max_dt
			FROM traffic_stats
			WHERE stat_datetime >= ?
			GROUP BY downloader_id
		) latest ON t.downloader_id = latest.downloader_id AND t.stat_datetime = latest.max_dt
	`, sinceText)
	if err != nil {
		return nil, err
	}
	for latestRows.Next() {
		var downloaderID string
		var upl, dnl int64
		if scanErr := latestRows.Scan(&downloaderID, &upl, &dnl); scanErr != nil {
			latestRows.Close()
			return nil, scanErr
		}
		latestMap[downloaderID] = [2]int64{upl, dnl}
	}
	latestRows.Close()

	firstMap := map[string][2]int64{}
	firstRows, err := sqlDB.Query(`
		SELECT t.downloader_id, t.cumulative_uploaded, t.cumulative_downloaded
		FROM traffic_stats t
		JOIN (
			SELECT downloader_id, MIN(stat_datetime) AS min_dt
			FROM traffic_stats
			WHERE stat_datetime >= ?
			GROUP BY downloader_id
		) first ON t.downloader_id = first.downloader_id AND t.stat_datetime = first.min_dt
	`, sinceText)
	if err != nil {
		return nil, err
	}
	for firstRows.Next() {
		var downloaderID string
		var upl, dnl int64
		if scanErr := firstRows.Scan(&downloaderID, &upl, &dnl); scanErr != nil {
			firstRows.Close()
			return nil, scanErr
		}
		firstMap[downloaderID] = [2]int64{upl, dnl}
	}
	firstRows.Close()

	result := map[string]map[string]int64{}
	for downloaderID, latest := range latestMap {
		startUL, startDL := latest[0], latest[1]
		if baseline, ok := startMap[downloaderID]; ok {
			startUL, startDL = baseline[0], baseline[1]
		} else if first, ok := firstMap[downloaderID]; ok {
			startUL, startDL = first[0], first[1]
		}

		todayUL := latest[0] - startUL
		if todayUL < 0 {
			todayUL = 0
		}
		todayDL := latest[1] - startDL
		if todayDL < 0 {
			todayDL = 0
		}
		result[downloaderID] = map[string]int64{
			"today_dl": todayDL,
			"today_ul": todayUL,
		}
	}
	return result, nil
}

// TrafficThisMonth 返回每个下载器「本月」的上传/下载增量。
// 参数/返回：返回 downloader_id -> {month_dl,month_ul} 映射。
// 失败场景：查询失败返回错误；日表缺失时回退为旧口径（当月采样 MAX-MIN）。
// 副作用：无副作用，仅读取数据库。
// 说明：已结算日期直接累加 traffic_stats_daily，今天单独按实时累计差补齐，
// 口径 = SUM(当日增量, 1号~昨天) + (当前累计 - 昨日 end)。
func (r *TorrentRepository) TrafficThisMonth() (map[string]map[string]int64, error) {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())

	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}

	rows, err := sqlDB.Query(`
		SELECT downloader_id,
		       SUM(uploaded) as month_ul,
		       SUM(downloaded) as month_dl
		FROM traffic_stats_daily
		WHERE stat_date >= ? AND stat_date < ?
		GROUP BY downloader_id
	`, monthStart.Format("2006-01-02"), todayStart.Format("2006-01-02"))
	if err != nil {
		if isMissingTableError(err) {
			return r.trafficThisMonthBySamples()
		}
		return nil, fmt.Errorf("query this month traffic failed: %w", err)
	}
	defer rows.Close()

	result := map[string]map[string]int64{}
	for rows.Next() {
		var downloaderID string
		var monthUL any
		var monthDL any
		if err := rows.Scan(&downloaderID, &monthUL, &monthDL); err != nil {
			return nil, err
		}
		result[downloaderID] = map[string]int64{
			"month_dl": normalizeToInt64(monthDL),
			"month_ul": normalizeToInt64(monthUL),
		}
	}

	// 今天尚未结算，单独按实时累计差补上；失败时保留已结算部分，不让整月归零。
	if todayMap, todayErr := r.queryTodayTraffic(now); todayErr == nil {
		for downloaderID, today := range todayMap {
			entry, ok := result[downloaderID]
			if !ok {
				entry = map[string]int64{"month_dl": 0, "month_ul": 0}
				result[downloaderID] = entry
			}
			entry["month_ul"] += today["today_ul"]
			entry["month_dl"] += today["today_dl"]
		}
	}
	return result, nil
}

// trafficThisMonthBySamples 是日表不可用时的旧口径兜底（当月采样 MAX-MIN）。
// 参数/返回：返回 downloader_id -> {month_dl,month_ul} 映射。
// 失败场景：查询失败返回错误。
// 副作用：无副作用，仅读取 traffic_stats 与 traffic_stats_hourly。
func (r *TorrentRepository) trafficThisMonthBySamples() (map[string]map[string]int64, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}

	query := ""
	switch r.store.DBType {
	case "postgresql":
		query = `
			SELECT downloader_id,
			       GREATEST(0, (MAX(cumulative_downloaded) - MIN(cumulative_downloaded))::bigint) as month_dl,
			       GREATEST(0, (MAX(cumulative_uploaded) - MIN(cumulative_uploaded))::bigint) as month_ul
			FROM (
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats
				WHERE stat_datetime >= DATE_TRUNC('month', CURRENT_DATE)
				UNION ALL
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats_hourly
				WHERE stat_datetime >= DATE_TRUNC('month', CURRENT_DATE)
			) combined
			GROUP BY downloader_id
		`
	case "mysql":
		query = `
			SELECT downloader_id,
			       GREATEST(0, MAX(cumulative_downloaded) - MIN(cumulative_downloaded)) as month_dl,
			       GREATEST(0, MAX(cumulative_uploaded) - MIN(cumulative_uploaded)) as month_ul
			FROM (
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats
				WHERE stat_datetime >= DATE_FORMAT(CURDATE(), '%Y-%m-01')
				UNION ALL
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats_hourly
				WHERE stat_datetime >= DATE_FORMAT(CURDATE(), '%Y-%m-01')
			) combined
			GROUP BY downloader_id
		`
	default:
		query = `
			SELECT downloader_id,
			       CASE WHEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded) > 0
			            THEN MAX(cumulative_downloaded) - MIN(cumulative_downloaded)
			            ELSE 0 END as month_dl,
			       CASE WHEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded) > 0
			            THEN MAX(cumulative_uploaded) - MIN(cumulative_uploaded)
			            ELSE 0 END as month_ul
			FROM (
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats
				WHERE DATE(stat_datetime) >= DATE('now', 'localtime', 'start of month')
				UNION ALL
				SELECT downloader_id, cumulative_downloaded, cumulative_uploaded
				FROM traffic_stats_hourly
				WHERE DATE(stat_datetime) >= DATE('now', 'localtime', 'start of month')
			) combined
			GROUP BY downloader_id
		`
	}

	rows, err := sqlDB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("query this month traffic failed: %w", err)
	}
	defer rows.Close()

	result := map[string]map[string]int64{}
	for rows.Next() {
		var downloaderID string
		var monthDL any
		var monthUL any
		if err := rows.Scan(&downloaderID, &monthDL, &monthUL); err != nil {
			return nil, err
		}
		result[downloaderID] = map[string]int64{
			"month_dl": normalizeToInt64(monthDL),
			"month_ul": normalizeToInt64(monthUL),
		}
	}
	return result, nil
}

func (r *TorrentRepository) DistinctDownloaderIDs() ([]string, error) {
	ids := make([]string, 0)
	if err := r.store.DB.Table("torrents").Distinct("downloader_id").Where("downloader_id IS NOT NULL AND downloader_id != '' AND (is_hidden = 0 OR is_hidden IS NULL)").Order("downloader_id ASC").Pluck("downloader_id", &ids).Error; err != nil {
		return nil, err
	}
	result := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result, nil
}

func (r *TorrentRepository) UpdateDownloaderID(oldID, newID string) (int64, error) {
	result := r.store.DB.Table("torrents").Where("downloader_id = ?", oldID).Updates(map[string]any{"downloader_id": newID})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func normalizeToInt64(value any) int64 {
	parsed, err := toInt64(value)
	if err == nil {
		return parsed
	}
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case float32:
		return int64(typed)
	default:
		return 0
	}
}

func (r *TorrentRepository) EnsureDownloaderMigrationTable() error {
	createSQL := ""
	switch r.store.DBType {
	case "postgresql":
		createSQL = `
			CREATE TABLE IF NOT EXISTS downloader_id_migration (
				old_id VARCHAR(64) NOT NULL PRIMARY KEY,
				new_id VARCHAR(64) NOT NULL,
				host VARCHAR(255) NOT NULL,
				name VARCHAR(255) NOT NULL,
				migrated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	case "mysql":
		createSQL = `
			CREATE TABLE IF NOT EXISTS downloader_id_migration (
				old_id VARCHAR(64) NOT NULL PRIMARY KEY,
				new_id VARCHAR(64) NOT NULL,
				host VARCHAR(255) NOT NULL,
				name VARCHAR(255) NOT NULL,
				migrated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	default:
		createSQL = `
			CREATE TABLE IF NOT EXISTS downloader_id_migration (
				old_id TEXT PRIMARY KEY,
				new_id TEXT NOT NULL,
				host TEXT NOT NULL,
				name TEXT NOT NULL,
				migrated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			)
		`
	}
	return r.store.DB.Exec(createSQL).Error
}

func (r *TorrentRepository) UpsertDownloaderMigration(oldID, newID, host, name string) error {
	switch r.store.DBType {
	case "postgresql":
		return r.store.DB.Exec(`
			INSERT INTO downloader_id_migration (old_id, new_id, host, name)
			VALUES (?, ?, ?, ?)
			ON CONFLICT (old_id) DO UPDATE SET
				new_id = EXCLUDED.new_id,
				host = EXCLUDED.host,
				name = EXCLUDED.name,
				migrated_at = CURRENT_TIMESTAMP
		`, oldID, newID, host, name).Error
	case "mysql":
		return r.store.DB.Exec(`
			INSERT INTO downloader_id_migration (old_id, new_id, host, name)
			VALUES (?, ?, ?, ?)
			ON DUPLICATE KEY UPDATE
				new_id = VALUES(new_id),
				host = VALUES(host),
				name = VALUES(name),
				migrated_at = CURRENT_TIMESTAMP
		`, oldID, newID, host, name).Error
	default:
		return r.store.DB.Exec(`
			INSERT OR REPLACE INTO downloader_id_migration (old_id, new_id, host, name, migrated_at)
			VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		`, oldID, newID, host, name).Error
	}
}

func (r *TorrentRepository) ListDownloaderMigrationHistory() ([]map[string]any, error) {
	rows := make([]map[string]any, 0)
	err := r.store.DB.Table("downloader_id_migration").
		Select("old_id, new_id, host, name, migrated_at").
		Order("migrated_at DESC").
		Scan(&rows).Error
	if err != nil {
		if isMissingTableError(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	return rows, nil
}

func (r *TorrentRepository) UpdateDownloaderIDAcrossTables(oldID, newID string) (map[string]int64, error) {
	counts := map[string]int64{}
	tables := []string{"traffic_stats", "traffic_stats_hourly", "traffic_stats_daily", "torrents", "torrent_upload_stats"}
	for _, table := range tables {
		affected, err := r.updateDownloaderIDInTable(table, oldID, newID)
		if err != nil {
			return nil, err
		}
		counts[table] = affected
	}
	return counts, nil
}

func (r *TorrentRepository) updateDownloaderIDInTable(tableName, oldID, newID string) (int64, error) {
	result := r.store.DB.Table(tableName).Where("downloader_id = ?", oldID).Updates(map[string]any{"downloader_id": newID})
	if result.Error != nil {
		if isMissingTableError(result.Error) {
			return 0, nil
		}
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

func isMissingTableError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "no such table") || strings.Contains(text, "does not exist") || strings.Contains(text, "undefined table")
}
