package repository

import (
	"strings"
	"time"
)

// ReseedRecord 表示一条 IYUU 辅种记录（每次「资源 × 目标站」一行）。
// 参数/返回：字段直接映射 reseed_records 表。
// 失败场景：无。
// 副作用：无。
type ReseedRecord struct {
	ID              int64  `json:"id" gorm:"column:id;primaryKey;autoIncrement"`
	TaskID          string `json:"task_id" gorm:"column:task_id;size:64;index:idx_reseed_task"`
	TorrentName     string `json:"torrent_name" gorm:"column:torrent_name;size:512"`
	Size            int64  `json:"size" gorm:"column:size"`
	SourceHash      string `json:"source_hash" gorm:"column:source_hash;size:64"`
	SourceSite      string `json:"source_site" gorm:"column:source_site;size:64"`
	TargetSite      string `json:"target_site" gorm:"column:target_site;size:64;index:idx_reseed_target"`
	TargetSID       int64  `json:"target_sid" gorm:"column:target_sid"`
	TargetTorrentID string `json:"target_torrent_id" gorm:"column:target_torrent_id;size:64"`
	TargetInfoHash  string `json:"target_info_hash" gorm:"column:target_info_hash;size:64"`
	DetailURL       string `json:"detail_url" gorm:"column:detail_url;size:512"`
	SavePath        string `json:"save_path" gorm:"column:save_path;size:512"`
	DownloaderID    string `json:"downloader_id" gorm:"column:downloader_id;size:64"`
	Tags            string `json:"tags" gorm:"column:tags;size:255"`
	Status          string `json:"status" gorm:"column:status;size:32;index:idx_reseed_status"`
	Message         string `json:"message" gorm:"column:message;type:text"`
	CreatedAt       string `json:"created_at" gorm:"column:created_at;size:32;index:idx_reseed_created"`
}

// TableName 固定辅种记录表名。
func (ReseedRecord) TableName() string { return "reseed_records" }

// ReseedRecordFilter 定义辅种记录列表的筛选条件。
type ReseedRecordFilter struct {
	Page       int
	PageSize   int
	TargetSite string
	Status     string
	Keyword    string
	TaskID     string
}

// ReseedRepository 封装辅种记录的读写。
type ReseedRepository struct {
	store *Store
}

// NewReseedRepository 创建辅种记录仓库。
func NewReseedRepository(store *Store) *ReseedRepository {
	return &ReseedRepository{store: store}
}

// Migrate 确保 reseed_records 表存在（列结构由 GORM 推导，sqlite/mysql/postgres 通用）。
func (r *ReseedRepository) Migrate() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.AutoMigrate(&ReseedRecord{})
}

// Add 写入一条辅种记录，CreatedAt 为空时自动补当前时间。
func (r *ReseedRepository) Add(record ReseedRecord) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	if strings.TrimSpace(record.CreatedAt) == "" {
		record.CreatedAt = time.Now().In(DatabaseTimeZone).Format("2006-01-02 15:04:05")
	}
	return r.store.DB.Create(&record).Error
}

// List 按筛选条件分页返回辅种记录（时间倒序）与总数。
// 参数/返回：filter 为筛选条件；返回记录列表与总数。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (r *ReseedRepository) List(filter ReseedRecordFilter) ([]ReseedRecord, int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return []ReseedRecord{}, 0, nil
	}
	page := filter.Page
	if page <= 0 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}

	query := r.store.DB.Model(&ReseedRecord{})
	if site := strings.TrimSpace(filter.TargetSite); site != "" {
		query = query.Where("target_site = ?", site)
	}
	if status := strings.TrimSpace(filter.Status); status != "" {
		query = query.Where("status = ?", status)
	}
	if taskID := strings.TrimSpace(filter.TaskID); taskID != "" {
		query = query.Where("task_id = ?", taskID)
	}
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		query = query.Where("torrent_name LIKE ?", "%"+keyword+"%")
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]ReseedRecord, 0, pageSize)
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&rows).Error
	return rows, total, err
}

// ExistsSeeded 判断某资源在指定站点是否已有真实做种记录（排除「未做种」占位行）。
// 参数/返回：name+size 定位资源，site 为站点昵称；返回是否存在。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (r *ReseedRepository) ExistsSeeded(name string, size int64, site string) (bool, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return false, nil
	}
	name = strings.TrimSpace(name)
	site = strings.TrimSpace(site)
	if name == "" || size <= 0 || site == "" {
		return false, nil
	}
	var count int64
	err := r.store.DB.Table("torrents").
		Where("name = ? AND size = ? AND sites = ? AND state != ? AND (is_hidden = 0 OR is_hidden IS NULL)", name, size, site, "未做种").
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// ListTargetSites 返回历史辅种记录里出现过的目标站点（倒序），供前端筛选下拉。
func (r *ReseedRepository) ListTargetSites() ([]string, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return []string{}, nil
	}
	rows := make([]string, 0)
	err := r.store.DB.Model(&ReseedRecord{}).
		Where("target_site != ''").
		Distinct("target_site").
		Pluck("target_site", &rows).Error
	return rows, err
}

// ReseedSiteRow 是辅种页可选站点的精简视图（不含 cookie/passkey 本体）。
// CanReseed / DisabledReason 由服务层按「凭据 + IYUU 是否收录」填写，
// 页面据此把不可用的站点置灰并展示原因。
type ReseedSiteRow struct {
	Nickname       string `json:"nickname"`
	Site           string `json:"site"`
	BaseURL        string `json:"base_url"`
	HasCookie      bool   `json:"has_cookie"`
	HasPasskey     bool   `json:"has_passkey"`
	Migration      int    `json:"migration"`
	CanReseed      bool   `json:"can_reseed"`
	DisabledReason string `json:"disabled_reason"`
}

// ListSites 返回 sites 表全部站点（含无凭据的）；辅种页只展示其中已配置 Cookie/Passkey 的站点，
// 由服务层（reseed.filterCredentialSites）筛选。
// 参数/返回：无参数；返回站点精简列表（按昵称排序）。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (r *ReseedRepository) ListSites() ([]ReseedSiteRow, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return []ReseedSiteRow{}, nil
	}
	rows := make([]struct {
		Nickname  string `gorm:"column:nickname"`
		Site      string `gorm:"column:site"`
		BaseURL   string `gorm:"column:base_url"`
		Cookie    string `gorm:"column:cookie"`
		Passkey   string `gorm:"column:passkey"`
		Migration int    `gorm:"column:migration"`
	}, 0)
	if err := r.store.DB.Table("sites").
		Select("nickname, site, base_url, cookie, passkey, migration").
		Where("nickname IS NOT NULL AND nickname != ''").
		Order("nickname ASC").
		Scan(&rows).Error; err != nil {
		return []ReseedSiteRow{}, err
	}

	result := make([]ReseedSiteRow, 0, len(rows))
	for _, row := range rows {
		result = append(result, ReseedSiteRow{
			Nickname:   strings.TrimSpace(row.Nickname),
			Site:       strings.TrimSpace(row.Site),
			BaseURL:    strings.TrimSpace(row.BaseURL),
			HasCookie:  strings.TrimSpace(row.Cookie) != "",
			HasPasskey: strings.TrimSpace(row.Passkey) != "",
			Migration:  row.Migration,
		})
	}
	return result, nil
}

// GetSiteAuth 读取指定站点的鉴权配置（含 cookie/passkey 本体），供下载种子使用。
// 参数/返回：nickname 为站点昵称；返回站点配置 map，未找到时返回空 map。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (r *ReseedRepository) GetSiteAuth(nickname string) (map[string]any, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return map[string]any{}, nil
	}
	nickname = strings.TrimSpace(nickname)
	if nickname == "" {
		return map[string]any{}, nil
	}
	rows := make([]map[string]any, 0, 1)
	err := r.store.DB.Table("sites").
		Select("nickname, site, base_url, cookie, passkey").
		Where("nickname = ?", nickname).
		Limit(1).
		Scan(&rows).Error
	if err != nil {
		return map[string]any{}, err
	}
	if len(rows) == 0 {
		return map[string]any{}, nil
	}
	return rows[0], nil
}
