package repository

import (
	"time"
)

// SiteAlert 表示一条站点健康检测产生的提醒（如 Cookie/Passkey 失效、获取不到用户信息）。
// 参数/返回：字段直接映射 site_alerts 表，供前端站内提醒铃铛展示。
// 失败场景：无直接失败场景。
// 副作用：无副作用，仅承载数据。
type SiteAlert struct {
	ID        int64  `json:"id" gorm:"column:id;primaryKey"`
	SiteCode  string `json:"site_code" gorm:"column:site_code"`
	Nickname  string `json:"nickname" gorm:"column:nickname"`
	Level     string `json:"level" gorm:"column:level"` // warning / error
	Message   string `json:"message" gorm:"column:message"`
	Read      bool   `json:"read" gorm:"column:read"`
	CreatedAt string `json:"created_at" gorm:"column:created_at"`
}

func (SiteAlert) TableName() string { return "site_alerts" }

// SiteAlertRepository 封装站点健康提醒的读写。
type SiteAlertRepository struct {
	store *Store
}

// NewSiteAlertRepository 创建站点提醒仓库。
func NewSiteAlertRepository(store *Store) *SiteAlertRepository {
	return &SiteAlertRepository{store: store}
}

// Migrate 确保 site_alerts 表存在（列结构由 GORM 推导，sqlite/mysql 通用）。
func (r *SiteAlertRepository) Migrate() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.AutoMigrate(&SiteAlert{})
}

// Add 写入一条站点提醒（read 初始为 false）。
func (r *SiteAlertRepository) Add(siteCode, nickname, level, message string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	now := time.Now().In(DatabaseTimeZone).Format("2006-01-02 15:04:05")
	alert := SiteAlert{
		SiteCode:  siteCode,
		Nickname:  nickname,
		Level:     level,
		Message:   message,
		Read:      false,
		CreatedAt: now,
	}
	return r.store.DB.Create(&alert).Error
}

// ListUnread 返回未读提醒，按创建时间倒序。
func (r *SiteAlertRepository) ListUnread(limit int) ([]SiteAlert, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	var alerts []SiteAlert
	err := r.store.DB.Where("`read` = ?", false).Order("id DESC").Limit(limit).Find(&alerts).Error
	return alerts, err
}

// ListRecent 返回最近提醒（含已读），供页面查看历史。
func (r *SiteAlertRepository) ListRecent(limit int) ([]SiteAlert, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	var alerts []SiteAlert
	err := r.store.DB.Order("id DESC").Limit(limit).Find(&alerts).Error
	return alerts, err
}

// UnreadCount 返回未读提醒数量。
func (r *SiteAlertRepository) UnreadCount() (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, nil
	}
	var count int64
	err := r.store.DB.Model(&SiteAlert{}).Where("`read` = ?", false).Count(&count).Error
	return count, err
}

// MarkAllRead 将所有未读提醒标记为已读。
func (r *SiteAlertRepository) MarkAllRead() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.Model(&SiteAlert{}).Where("`read` = ?", false).Update("read", true).Error
}

// ClearAll 清空全部站点提醒（物理删除）。
func (r *SiteAlertRepository) ClearAll() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.Exec("DELETE FROM `site_alerts`").Error
}
