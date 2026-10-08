package repository

import (
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

type SiteRepository struct {
	store *Store
}

func NewSiteRepository(store *Store) *SiteRepository {
	return &SiteRepository{store: store}
}

func (r *SiteRepository) ListSourceAndTargetSites() ([]string, []string, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, nil, err
	}

	// 源站凭据可以是 cookie 也可以是 passkey：
	// PrepareSourceSite 接受两者之一，且仅配 passkey 的站点同样能走 download.php 直链下载
	// （馒头这类用 API 令牌的站点就只填 passkey）。因此这里不能只按 cookie 过滤，
	// 否则站点会出现在目标站列表里、却不在源站列表里。
	sourceRows, err := sqlDB.Query(`
		SELECT nickname FROM sites
		WHERE (migration = 1 OR migration = 3)
		AND (
			(cookie IS NOT NULL AND cookie != '')
			OR (passkey IS NOT NULL AND passkey != '')
		)
		ORDER BY sort_order, nickname
	`)
	if err != nil {
		return nil, nil, err
	}
	defer sourceRows.Close()

	sourceSites := make([]string, 0)
	for sourceRows.Next() {
		var nickname string
		if err := sourceRows.Scan(&nickname); err != nil {
			return nil, nil, err
		}
		sourceSites = append(sourceSites, nickname)
	}

	targetRows, err := sqlDB.Query(`
		SELECT nickname FROM sites
		WHERE (migration = 2 OR migration = 3)
		ORDER BY sort_order, nickname
	`)
	if err != nil {
		return nil, nil, err
	}
	defer targetRows.Close()

	targetSites := make([]string, 0)
	for targetRows.Next() {
		var nickname string
		if err := targetRows.Scan(&nickname); err != nil {
			return nil, nil, err
		}
		targetSites = append(targetSites, nickname)
	}

	return sourceSites, targetSites, nil
}

func (r *SiteRepository) ListSites(filterByTorrents string) ([]map[string]any, error) {
	groupColumn := r.store.GroupColumn()
	selectFields := fmt.Sprintf(`
		s.id, s.nickname, s.site, s.base_url, s.special_tracker_domain, s.%s, s.speed_limit,
		s.ratio_threshold, s.seed_speed_limit, s.can_publish, s.forbidden_transfer_sites,
		s.dupe_check_enabled, s.dupe_size_tolerance_bytes, s.dupe_rules, s.audio_track_policy, s.sort_order,
		CASE WHEN s.cookie IS NOT NULL AND s.cookie != '' THEN 1 ELSE 0 END as has_cookie,
		CASE WHEN s.passkey IS NOT NULL AND s.passkey != '' THEN 1 ELSE 0 END as has_passkey,
		s.cookie, s.passkey
	`, groupColumn)

	query := ""
	if filterByTorrents == "active" {
		if r.store.DBType == "mysql" {
			query = fmt.Sprintf(`
					SELECT DISTINCT %s
					FROM sites s
					WHERE EXISTS (
						SELECT 1 FROM torrents t
						WHERE LOWER(s.nickname) COLLATE utf8mb4_unicode_ci = LOWER(t.sites) COLLATE utf8mb4_unicode_ci
						  AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
					)
					OR (s.cookie IS NOT NULL AND s.cookie != '')
					ORDER BY s.sort_order, s.nickname COLLATE utf8mb4_unicode_ci
			`, selectFields)
		} else {
			query = fmt.Sprintf(`
					SELECT DISTINCT %s
					FROM sites s
					WHERE EXISTS (
						SELECT 1 FROM torrents t WHERE LOWER(s.nickname) = LOWER(t.sites) AND (t.is_hidden = 0 OR t.is_hidden IS NULL)
					)
					OR (s.cookie IS NOT NULL AND s.cookie != '')
					ORDER BY s.sort_order, s.nickname
			`, selectFields)
		}
	} else {
		if r.store.DBType == "mysql" {
			query = fmt.Sprintf("SELECT %s FROM sites s ORDER BY s.sort_order, s.nickname COLLATE utf8mb4_unicode_ci", selectFields)
		} else {
			query = fmt.Sprintf("SELECT %s FROM sites s ORDER BY s.sort_order, s.nickname", selectFields)
		}
	}

	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sites, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}
	for _, s := range sites {
		s["can_publish"] = toIntWithDefault(s["can_publish"], 1) != 0
		s["forbidden_transfer_sites"] = siteStringListFromAny(s["forbidden_transfer_sites"])
		s["dupe_check_enabled"] = toIntWithDefault(s["dupe_check_enabled"], 0) != 0
		s["dupe_size_tolerance_bytes"] = toInt64WithDefault(s["dupe_size_tolerance_bytes"], DefaultDupeSizeToleranceBytes)
		// 前端直接把规则表当对象用，这里统一解析成「媒介 → 判定维度」结构。
		s["dupe_rules"] = siteDupeRulesFromAny(s["dupe_rules"])
		// 多音轨策略默认「码率最高」（2）；旧数据无该列时回填默认值。
		s["audio_track_policy"] = toIntWithDefault(s["audio_track_policy"], 2)
	}
	return sites, nil
}

func (r *SiteRepository) UpdateSiteDetails(data map[string]any) (bool, error) {
	siteID, err := toInt64(data["id"])
	if err != nil || siteID <= 0 {
		return false, errors.New("invalid site id")
	}

	cookie := strings.TrimSpace(toString(data["cookie"], ""))
	ratioThreshold := toFloat64WithDefault(data["ratio_threshold"], 3.0)
	if ratioThreshold <= 0 {
		ratioThreshold = 3.0
	}
	seedSpeedLimit := toIntWithDefault(data["seed_speed_limit"], 5)
	canPublish := toIntWithDefault(data["can_publish"], 1)
	forbiddenTransferSites := encodeSiteStringList(data["forbidden_transfer_sites"])
	dupeCheckEnabled := 0
	if toIntWithDefault(data["dupe_check_enabled"], 0) != 0 {
		dupeCheckEnabled = 1
	}
	dupeSizeTolerance := toInt64WithDefault(data["dupe_size_tolerance_bytes"], DefaultDupeSizeToleranceBytes)
	if dupeSizeTolerance < 0 {
		dupeSizeTolerance = DefaultDupeSizeToleranceBytes
	}
	// 规则表按 JSON 文本入库；未配置时写空串（读回解析为空表，等价于「该站点没有媒介规则」）。
	dupeRules := encodeSiteDupeRules(data["dupe_rules"])
	// 多音轨策略：1=第一条 / 2=码率最高（默认）/ 3=规格最高；非法值回退默认。
	audioTrackPolicy := toIntWithDefault(data["audio_track_policy"], 2)
	if audioTrackPolicy < 1 || audioTrackPolicy > 3 {
		audioTrackPolicy = 2
	}
	sortOrder := toIntWithDefault(data["sort_order"], 0)

	groupColumn := r.store.GroupColumn()
	sql := fmt.Sprintf(`
		UPDATE sites
		SET nickname = ?,
			base_url = ?,
			special_tracker_domain = ?,
			%s = ?,
			description = ?,
			cookie = ?,
			passkey = ?,
			speed_limit = ?,
			ratio_threshold = ?,
			seed_speed_limit = ?,
			can_publish = ?,
			forbidden_transfer_sites = ?,
			dupe_check_enabled = ?,
			dupe_size_tolerance_bytes = ?,
			dupe_rules = ?,
			audio_track_policy = ?,
			sort_order = ?
		WHERE id = ?
	`, groupColumn)

	result := r.store.DB.Exec(
		sql,
		toString(data["nickname"], ""),
		toString(data["base_url"], ""),
		toString(data["special_tracker_domain"], ""),
		toString(data["group"], ""),
		toString(data["description"], ""),
		cookie,
		toString(data["passkey"], ""),
		toIntWithDefault(data["speed_limit"], 0),
		ratioThreshold,
		seedSpeedLimit,
		canPublish,
		forbiddenTransferSites,
		dupeCheckEnabled,
		dupeSizeTolerance,
		dupeRules,
		audioTrackPolicy,
		sortOrder,
		siteID,
	)
	if result.Error != nil {
		return false, result.Error
	}
	// 不能用 RowsAffected 判断站点是否存在：MySQL 在「提交值与库内完全相同」时返回 0 行
	// （默认只统计真正改变的行，CLIENT_FOUND_ROWS 未开），
	// 于是「打开弹窗不改任何配置直接保存」会被误判成站点不存在并报 404。
	// 但也不能一律当成功——那样真的传错 id 也会被吞掉。
	// 因此显式回查一次主键，让「站点不存在」仍然能被上层如实报出。
	return r.siteExists(siteID)
}

// siteExists 判断站点主键是否存在。
// 参数/返回：siteID 为 sites.id；返回是否存在与错误。
// 副作用：无。
func (r *SiteRepository) siteExists(siteID int64) (bool, error) {
	var count int64
	if err := r.store.DB.Table("sites").Where("id = ?", siteID).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *SiteRepository) DeleteSite(siteID int64) (bool, error) {
	result := r.store.DB.Exec("DELETE FROM sites WHERE id = ?", siteID)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *SiteRepository) UpdateSiteCookie(nickname, cookie string) (bool, error) {
	result := r.store.DB.Exec("UPDATE sites SET cookie = ? WHERE nickname = ?", strings.TrimSpace(cookie), nickname)
	if result.Error != nil {
		return false, result.Error
	}
	// 同 UpdateSiteDetails：Cookie 与库内相同时 RowsAffected 为 0，不能据此判定站点不存在
	//（否则重复同步同一份 Cookie 会被误报为「未找到站点」）；但仍需回查以保留存在性校验。
	var count int64
	if err := r.store.DB.Table("sites").Where("nickname = ?", nickname).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// UpdateSiteCookieBySite 根据站点标识更新 Cookie。
// 参数/返回：siteCode 为 sites.site 字段，cookie 为标准 "k=v; k2=v2" 字符串；返回是否命中并更新。
// 失败场景：数据库执行失败时返回错误。
// 副作用：写入 sites 表的 cookie 字段。
func (r *SiteRepository) UpdateSiteCookieBySite(siteCode, cookie string) (bool, error) {
	trimmedSite := strings.TrimSpace(siteCode)
	result := r.store.DB.Exec("UPDATE sites SET cookie = ? WHERE site = ?", strings.TrimSpace(cookie), trimmedSite)
	if result.Error != nil {
		return false, result.Error
	}
	// 同 UpdateSiteDetails：值未变化时 RowsAffected 为 0，改用回查判断存在性。
	var count int64
	if err := r.store.DB.Table("sites").Where("site = ?", trimmedSite).Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *SiteRepository) SitesStatus() ([]map[string]any, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query("SELECT nickname, site, cookie, passkey, migration, can_publish, forbidden_transfer_sites, dupe_check_enabled, dupe_size_tolerance_bytes, sort_order FROM sites ORDER BY sort_order, nickname")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	raw, err := rowsToMaps(rows)
	if err != nil {
		return nil, err
	}

	result := make([]map[string]any, 0, len(raw))
	for _, row := range raw {
		migration := toIntWithDefault(row["migration"], 0)
		result = append(result, map[string]any{
			"name":                     toString(row["nickname"], ""),
			"site":                     toString(row["site"], ""),
			"has_cookie":               toString(row["cookie"], "") != "",
			"has_passkey":              toString(row["passkey"], "") != "",
			"is_source":                migration == 1 || migration == 3,
			"is_target":                migration == 2 || migration == 3,
			"can_publish":              toIntWithDefault(row["can_publish"], 1) != 0,
			"forbidden_transfer_sites": siteStringListFromAny(row["forbidden_transfer_sites"]),
			"dupe_check_enabled":       toIntWithDefault(row["dupe_check_enabled"], 0) != 0,
			"dupe_size_tolerance_bytes": toInt64WithDefault(
				row["dupe_size_tolerance_bytes"],
				DefaultDupeSizeToleranceBytes,
			),
		})
	}
	return result, nil
}

func (r *SiteRepository) SetTorrentSiteNotExist(torrentName, siteName string) (bool, error) {
	result := r.store.DB.Exec("UPDATE torrents SET state = ? WHERE name = ? AND sites = ?", "不存在", torrentName, siteName)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *SiteRepository) UpdateTorrentComment(torrentName, siteName, comment string) (bool, error) {
	result := r.store.DB.Exec("UPDATE torrents SET details = ? WHERE name = ? AND sites = ?", comment, torrentName, siteName)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// UpdateSitesSortOrder 批量更新站点排序序号。
// 参数/返回：siteIDs 为按目标顺序排列的站点 ID 列表；返回更新条数与错误。
// 失败场景：事务或 SQL 执行失败时返回错误。
// 副作用：写入 sites 表的 sort_order 字段。
func (r *SiteRepository) UpdateSitesSortOrder(siteIDs []int64) (int, error) {
	if len(siteIDs) == 0 {
		return 0, nil
	}
	updated := 0
	err := r.store.DB.Transaction(func(tx *gorm.DB) error {
		for idx, id := range siteIDs {
			if id <= 0 {
				continue
			}
			result := tx.Exec("UPDATE sites SET sort_order = ? WHERE id = ?", idx+1, id)
			if result.Error != nil {
				return result.Error
			}
			updated += int(result.RowsAffected)
		}
		return nil
	})
	return updated, err
}
