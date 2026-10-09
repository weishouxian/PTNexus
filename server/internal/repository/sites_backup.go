package repository

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// 站点导入导出（备份 / 换机迁移）相关口径。
//
// 导入采用「仅补空」策略，判定依据是库里当前的旧值：
//   - 文本字段：库里为空串 / NULL 时，才用文件里的值填充；
//   - 列表与规则：库里为空列表、空规则表时，才填充；
//   - 数值与开关：库里仍等于「系统默认值」（视为从未配置过）时，才填充；
//   - Cookie 与 Passkey 视为一组凭据：库里任意一个有值就整组都不动，
//     只有两者都为空时才一起用文件值填充（避免凭据配对错位）。
//
// 也就是说，只要用户在站点管理里手动改过某个字段，导入就不会覆盖它。
// 导入不新增站点：文件里存在但库里没有的站点会被跳过并在结果里列出来。
const (
	siteBackupDefaultMigration              = 1
	siteBackupDefaultSpeedLimit             = 0
	siteBackupDefaultRatioThreshold         = 3.0
	siteBackupDefaultSeedSpeedLimit         = 5
	siteBackupDefaultCanPublish             = 1
	siteBackupDefaultDupeCheckEnabled       = 0
	siteBackupDefaultDupeSizeToleranceBytes = DefaultDupeSizeToleranceBytes
	siteBackupDefaultAudioTrackPolicy       = 2
	siteBackupDefaultSortOrder              = 0
)

// siteBackupSelectSQL 返回导入导出共用的整行读取语句。
// 参数/返回：无入参；返回 SQL 文本。
// 副作用：无。
func (r *SiteRepository) siteBackupSelectSQL() string {
	return fmt.Sprintf(`
		SELECT s.id, s.site, s.nickname, s.base_url, s.special_tracker_domain,
			s.%s AS group_name, s.description, s.migration, s.cookie, s.passkey,
			s.speed_limit, s.ratio_threshold, s.seed_speed_limit, s.can_publish,
			s.forbidden_transfer_sites, s.dupe_check_enabled, s.dupe_size_tolerance_bytes,
			s.dupe_rules, s.audio_track_policy, s.tags, s.sort_order
		FROM sites s
		ORDER BY s.sort_order, s.nickname
	`, r.store.GroupColumn())
}

// siteBackupValues 表示一个站点在备份文件里的全部可编辑字段。
// 列表与规则以「写库口径」的文本保存（JSON 数组 / JSON 对象），前端展示时会再解析成结构化数据。
type siteBackupValues struct {
	Site                   string
	Nickname               string
	BaseURL                string
	SpecialTrackerDomain   string
	Group                  string
	Description            string
	Cookie                 string
	Passkey                string
	Migration              int
	SpeedLimit             int
	RatioThreshold         float64
	SeedSpeedLimit         int
	CanPublish             int
	ForbiddenTransferSites string
	DupeCheckEnabled       int
	DupeSizeToleranceBytes int64
	DupeRules              string
	AudioTrackPolicy       int
	Tags                   string
	SortOrder              int
}

// updateArgs 拼装 UPDATE sites 的参数（与 siteBackupUpdateSQL 的占位符一一对应）。
func (v siteBackupValues) updateArgs(siteID int64) []any {
	return []any{
		v.Nickname,
		v.BaseURL,
		v.SpecialTrackerDomain,
		v.Group,
		v.Description,
		v.Cookie,
		v.Passkey,
		v.Migration,
		v.SpeedLimit,
		v.RatioThreshold,
		v.SeedSpeedLimit,
		v.CanPublish,
		v.ForbiddenTransferSites,
		v.DupeCheckEnabled,
		v.DupeSizeToleranceBytes,
		v.DupeRules,
		v.AudioTrackPolicy,
		v.Tags,
		v.SortOrder,
		siteID,
	}
}

// asExportMap 转成导出 JSON 里的站点条目（列表与规则解析成结构化数据，便于人工查看编辑）。
func (v siteBackupValues) asExportMap() map[string]any {
	return map[string]any{
		"site":                      v.Site,
		"nickname":                  v.Nickname,
		"base_url":                  v.BaseURL,
		"special_tracker_domain":    v.SpecialTrackerDomain,
		"group":                     v.Group,
		"description":               v.Description,
		"migration":                 v.Migration,
		"cookie":                    v.Cookie,
		"passkey":                   v.Passkey,
		"speed_limit":               v.SpeedLimit,
		"ratio_threshold":           v.RatioThreshold,
		"seed_speed_limit":          v.SeedSpeedLimit,
		"can_publish":               v.CanPublish != 0,
		"forbidden_transfer_sites":  siteStringListFromAny(v.ForbiddenTransferSites),
		"dupe_check_enabled":        v.DupeCheckEnabled != 0,
		"dupe_size_tolerance_bytes": v.DupeSizeToleranceBytes,
		"dupe_rules":                siteDupeRulesFromAny(v.DupeRules),
		"audio_track_policy":        v.AudioTrackPolicy,
		"tags":                      siteStringListFromAny(v.Tags),
		"sort_order":                v.SortOrder,
	}
}

// siteBackupValuesFromRow 把一行数据库记录归一成备份口径的值。
func siteBackupValuesFromRow(row map[string]any) siteBackupValues {
	return siteBackupValues{
		Site:                   strings.TrimSpace(toString(row["site"], "")),
		Nickname:               strings.TrimSpace(toString(row["nickname"], "")),
		BaseURL:                strings.TrimSpace(toString(row["base_url"], "")),
		SpecialTrackerDomain:   strings.TrimSpace(toString(row["special_tracker_domain"], "")),
		Group:                  strings.TrimSpace(toString(row["group_name"], "")),
		Description:            strings.TrimSpace(toString(row["description"], "")),
		Cookie:                 strings.TrimSpace(toString(row["cookie"], "")),
		Passkey:                strings.TrimSpace(toString(row["passkey"], "")),
		Migration:              toIntWithDefault(row["migration"], siteBackupDefaultMigration),
		SpeedLimit:             toIntWithDefault(row["speed_limit"], siteBackupDefaultSpeedLimit),
		RatioThreshold:         normalizeSiteBackupRatio(row["ratio_threshold"]),
		SeedSpeedLimit:         toIntWithDefault(row["seed_speed_limit"], siteBackupDefaultSeedSpeedLimit),
		CanPublish:             normalizeSiteBackupToggle(row["can_publish"], siteBackupDefaultCanPublish),
		ForbiddenTransferSites: encodeSiteStringList(row["forbidden_transfer_sites"]),
		DupeCheckEnabled:       normalizeSiteBackupToggle(row["dupe_check_enabled"], siteBackupDefaultDupeCheckEnabled),
		DupeSizeToleranceBytes: toInt64WithDefault(row["dupe_size_tolerance_bytes"], siteBackupDefaultDupeSizeToleranceBytes),
		DupeRules:              encodeSiteDupeRules(row["dupe_rules"]),
		AudioTrackPolicy:       normalizeSiteBackupAudioTrackPolicy(row["audio_track_policy"]),
		Tags:                   encodeSiteStringList(row["tags"]),
		SortOrder:              toIntWithDefault(row["sort_order"], siteBackupDefaultSortOrder),
	}
}

// siteBackupValuesFromImport 把备份文件里的一条站点记录归一成备份口径的值。
// 说明：只读文件里提供的字段，字段缺失时回落到系统默认值（后续「补空」比较会自然跳过）。
func siteBackupValuesFromImport(item map[string]any) siteBackupValues {
	return siteBackupValues{
		Site:                   strings.TrimSpace(toString(item["site"], "")),
		Nickname:               strings.TrimSpace(toString(item["nickname"], "")),
		BaseURL:                strings.TrimSpace(toString(item["base_url"], "")),
		SpecialTrackerDomain:   strings.TrimSpace(toString(item["special_tracker_domain"], "")),
		Group:                  strings.TrimSpace(toString(item["group"], "")),
		Description:            strings.TrimSpace(toString(item["description"], "")),
		Cookie:                 strings.TrimSpace(toString(item["cookie"], "")),
		Passkey:                strings.TrimSpace(toString(item["passkey"], "")),
		Migration:              toIntWithDefault(item["migration"], siteBackupDefaultMigration),
		SpeedLimit:             toIntWithDefault(item["speed_limit"], siteBackupDefaultSpeedLimit),
		RatioThreshold:         normalizeSiteBackupRatio(item["ratio_threshold"]),
		SeedSpeedLimit:         toIntWithDefault(item["seed_speed_limit"], siteBackupDefaultSeedSpeedLimit),
		CanPublish:             normalizeSiteBackupToggle(item["can_publish"], siteBackupDefaultCanPublish),
		ForbiddenTransferSites: encodeSiteStringList(item["forbidden_transfer_sites"]),
		DupeCheckEnabled:       normalizeSiteBackupToggle(item["dupe_check_enabled"], siteBackupDefaultDupeCheckEnabled),
		DupeSizeToleranceBytes: toInt64WithDefault(item["dupe_size_tolerance_bytes"], siteBackupDefaultDupeSizeToleranceBytes),
		DupeRules:              encodeSiteDupeRules(item["dupe_rules"]),
		AudioTrackPolicy:       normalizeSiteBackupAudioTrackPolicy(item["audio_track_policy"]),
		Tags:                   encodeSiteStringList(item["tags"]),
		SortOrder:              toIntWithDefault(item["sort_order"], siteBackupDefaultSortOrder),
	}
}

// normalizeSiteBackupRatio 归一 ratio_threshold：非法或非正数一律回落到默认值（未配置）。
func normalizeSiteBackupRatio(value any) float64 {
	ratio := toFloat64WithDefault(value, siteBackupDefaultRatioThreshold)
	if ratio <= 0 {
		return siteBackupDefaultRatioThreshold
	}
	return ratio
}

// normalizeSiteBackupToggle 归一 0/1 开关：只认 0，其余一律视为开启（与既有读写口径一致）。
func normalizeSiteBackupToggle(value any, fallback int) int {
	parsed := toIntWithDefault(value, fallback)
	if parsed == 0 {
		return 0
	}
	return 1
}

// normalizeSiteBackupAudioTrackPolicy 归一多音轨策略，非法值回落到默认（2）。
func normalizeSiteBackupAudioTrackPolicy(value any) int {
	policy := toIntWithDefault(value, siteBackupDefaultAudioTrackPolicy)
	if policy < 1 || policy > 3 {
		return siteBackupDefaultAudioTrackPolicy
	}
	return policy
}

// ExportSites 导出全部站点配置（含 Cookie / Passkey），用于备份或换机迁移。
// 参数/返回：无入参；返回按 sort_order 排序的站点条目列表。
// 失败场景：数据库查询失败时返回错误。
// 副作用：只读。
func (r *SiteRepository) ExportSites() ([]map[string]any, error) {
	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query(r.siteBackupSelectSQL())
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
		values := siteBackupValuesFromRow(row)
		if values.Site == "" && values.Nickname == "" {
			continue
		}
		result = append(result, values.asExportMap())
	}
	return result, nil
}

// ImportSites 按「仅补空」策略导入站点配置（不新增站点）。
// 参数/返回：items 为备份文件里的站点条目；返回统计结果（updated/unchanged/missing/invalid/failed）。
// 失败场景：读取现有站点或事务提交失败时返回错误；单条写入失败不中断整体导入，计入 failed。
// 副作用：按需 UPDATE sites 表。
func (r *SiteRepository) ImportSites(items []map[string]any) (map[string]any, error) {
	empty := map[string]any{
		"total":     len(items),
		"updated":   0,
		"unchanged": 0,
		"missing":   []string{},
		"invalid":   []string{},
		"failed":    []map[string]any{},
	}
	if len(items) == 0 {
		return empty, nil
	}

	sqlDB, err := r.store.DB.DB()
	if err != nil {
		return nil, err
	}
	rows, err := sqlDB.Query(r.siteBackupSelectSQL())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	existingRows, err := rowsToMaps(rows)
	// 显式关闭游标再进事务：SQLite 连接池只有 1 个连接（MaxOpenConns=1），
	// 若游标没释放就开事务，会一直等连接导致死锁。
	rows.Close()
	if err != nil {
		return nil, err
	}

	// 三种匹配键：站点标识优先，其次昵称、下载页地址（大小写不敏感）。
	bySite := map[string]map[string]any{}
	byNickname := map[string]map[string]any{}
	byBaseURL := map[string]map[string]any{}
	for _, row := range existingRows {
		if key := strings.TrimSpace(toString(row["site"], "")); key != "" {
			if _, exists := bySite[key]; !exists {
				bySite[key] = row
			}
		}
		if key := strings.ToLower(strings.TrimSpace(toString(row["nickname"], ""))); key != "" {
			if _, exists := byNickname[key]; !exists {
				byNickname[key] = row
			}
		}
		if key := strings.ToLower(strings.TrimSpace(toString(row["base_url"], ""))); key != "" {
			if _, exists := byBaseURL[key]; !exists {
				byBaseURL[key] = row
			}
		}
	}

	updated := 0
	unchanged := 0
	missing := make([]string, 0)
	invalid := make([]string, 0)
	failed := make([]map[string]any, 0)

	err = r.store.DB.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			incoming := siteBackupValuesFromImport(item)
			label := incoming.Site
			if label == "" {
				label = incoming.Nickname
			}
			if incoming.Site == "" && incoming.Nickname == "" && incoming.BaseURL == "" {
				invalid = append(invalid, "（缺少站点标识的条目）")
				continue
			}

			current := bySite[incoming.Site]
			if current == nil && incoming.Nickname != "" {
				current = byNickname[strings.ToLower(incoming.Nickname)]
			}
			if current == nil && incoming.BaseURL != "" {
				current = byBaseURL[strings.ToLower(incoming.BaseURL)]
			}
			if current == nil {
				missing = append(missing, label)
				continue
			}

			siteID, idErr := toInt64(current["id"])
			if idErr != nil || siteID <= 0 {
				failed = append(failed, map[string]any{"site": label, "error": "站点主键异常"})
				continue
			}

			merged, changed := mergeSiteBackup(siteBackupValuesFromRow(current), incoming)
			if !changed {
				unchanged++
				continue
			}
			if execErr := tx.Exec(siteBackupUpdateSQL(r.store.GroupColumn()), merged.updateArgs(siteID)...).Error; execErr != nil {
				failed = append(failed, map[string]any{"site": label, "error": execErr.Error()})
				continue
			}
			updated++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"total":     len(items),
		"updated":   updated,
		"unchanged": unchanged,
		"missing":   missing,
		"invalid":   invalid,
		"failed":    failed,
	}, nil
}

// siteBackupUpdateSQL 返回按主键整行写回的 UPDATE 语句。
func siteBackupUpdateSQL(groupColumn string) string {
	return fmt.Sprintf(`
		UPDATE sites
		SET nickname = ?,
			base_url = ?,
			special_tracker_domain = ?,
			%s = ?,
			description = ?,
			cookie = ?,
			passkey = ?,
			migration = ?,
			speed_limit = ?,
			ratio_threshold = ?,
			seed_speed_limit = ?,
			can_publish = ?,
			forbidden_transfer_sites = ?,
			dupe_check_enabled = ?,
			dupe_size_tolerance_bytes = ?,
			dupe_rules = ?,
			audio_track_policy = ?,
			tags = ?,
			sort_order = ?
		WHERE id = ?
	`, groupColumn)
}

// mergeSiteBackup 按「仅补空」策略把备份值合并进库内值。
// 参数/返回：current 为库内当前值；incoming 为备份文件值；返回合并结果与是否产生实际改动。
// 说明：站点标识（site）不参与导入，避免改动站点身份；昵称等文本字段仅当库内为空才填；
// Cookie / Passkey 作为一组凭据，库里任一有值则两个都不动。
func mergeSiteBackup(cur, in siteBackupValues) (siteBackupValues, bool) {
	merged := cur

	merged.Nickname = fillSiteBackupText(cur.Nickname, in.Nickname)
	merged.BaseURL = fillSiteBackupText(cur.BaseURL, in.BaseURL)
	merged.SpecialTrackerDomain = fillSiteBackupText(cur.SpecialTrackerDomain, in.SpecialTrackerDomain)
	merged.Group = fillSiteBackupText(cur.Group, in.Group)
	merged.Description = fillSiteBackupText(cur.Description, in.Description)
	// Cookie 与 Passkey 作为「一组凭据」整体处理：库里任意一个有值，就两个字段都不动。
	// 否则会出现「库里只填了 Cookie、导入把 Passkey 补成另一个账号的」这种配对错位。
	if strings.TrimSpace(cur.Cookie) == "" && strings.TrimSpace(cur.Passkey) == "" {
		merged.Cookie = strings.TrimSpace(in.Cookie)
		merged.Passkey = strings.TrimSpace(in.Passkey)
	}
	if cur.Migration == siteBackupDefaultMigration {
		merged.Migration = in.Migration
	}
	if cur.SpeedLimit == siteBackupDefaultSpeedLimit {
		merged.SpeedLimit = in.SpeedLimit
	}
	if cur.RatioThreshold == siteBackupDefaultRatioThreshold {
		merged.RatioThreshold = in.RatioThreshold
	}
	if cur.SeedSpeedLimit == siteBackupDefaultSeedSpeedLimit {
		merged.SeedSpeedLimit = in.SeedSpeedLimit
	}
	if cur.CanPublish == siteBackupDefaultCanPublish {
		merged.CanPublish = in.CanPublish
	}
	if cur.DupeCheckEnabled == siteBackupDefaultDupeCheckEnabled {
		merged.DupeCheckEnabled = in.DupeCheckEnabled
	}
	if cur.DupeSizeToleranceBytes == siteBackupDefaultDupeSizeToleranceBytes {
		merged.DupeSizeToleranceBytes = in.DupeSizeToleranceBytes
	}
	if cur.AudioTrackPolicy == siteBackupDefaultAudioTrackPolicy {
		merged.AudioTrackPolicy = in.AudioTrackPolicy
	}
	if cur.SortOrder == siteBackupDefaultSortOrder {
		merged.SortOrder = in.SortOrder
	}
	if len(siteStringListFromAny(cur.ForbiddenTransferSites)) == 0 {
		merged.ForbiddenTransferSites = in.ForbiddenTransferSites
	}
	if len(siteStringListFromAny(cur.Tags)) == 0 {
		merged.Tags = in.Tags
	}
	if len(siteDupeRulesFromAny(cur.DupeRules)) == 0 {
		merged.DupeRules = in.DupeRules
	}

	return merged, merged != cur
}

// fillSiteBackupText 只在库内为空时用导入值填充文本字段。
func fillSiteBackupText(current, incoming string) string {
	if strings.TrimSpace(current) != "" {
		return current
	}
	return strings.TrimSpace(incoming)
}
