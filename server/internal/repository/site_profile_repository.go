package repository

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SiteProfile 表示从站点侧抓取到的账号数据（上传量/下载量/分享率/等级/做种数等）。
// 参数/返回：字段一一映射 site_profiles 表，每个站点只保留最新一条记录。
// 失败场景：无直接失败场景。
// 副作用：无副作用，仅承载数据。
//
// 与「下载器流量统计」的区别：本表数据来自 PT 站点个人资料页/接口，
// 是站点认定的事实（决定账号等级与被降级风险）；traffic_stats 系列只是本地下载器的累计收发量。
type SiteProfile struct {
	ID int64 `json:"id" gorm:"column:id;primaryKey"`
	// ⚠️ 所有参与索引/唯一约束的 string 必须给 size：
	// gorm 的 MySQL 驱动把未指定长度的 string 映射成 longtext，longtext 上建索引会报
	// Error 1170 (BLOB/TEXT column used in key specification without a key length)。
	SiteCode   string  `json:"site_code" gorm:"column:site_code;size:64;uniqueIndex"`
	Nickname   string  `json:"nickname" gorm:"column:nickname;size:128"`
	BaseURL    string  `json:"base_url" gorm:"column:base_url;size:255"`
	UserID     string  `json:"user_id" gorm:"column:user_id;size:64"`
	Username   string  `json:"username" gorm:"column:username;size:128"`
	UserClass  string  `json:"user_class" gorm:"column:user_class;size:64"`
	Uploaded   int64   `json:"uploaded" gorm:"column:uploaded"`
	Downloaded int64   `json:"downloaded" gorm:"column:downloaded"`
	Ratio      float64 `json:"ratio" gorm:"column:ratio"`
	Bonus      float64 `json:"bonus" gorm:"column:bonus"`
	// SeedBonus 为「做种积分」（NexusPHP 里靠做种时长/体积累积的积分，部分站叫「做种得分」「积分」）。
	// 与魔力值（Bonus）是两套不同的计分体系，站点通常分开显示，因此单独存一列。
	SeedBonus float64 `json:"seed_bonus" gorm:"column:seed_bonus"`
	Seeding   int64   `json:"seeding" gorm:"column:seeding"`
	Leeching  int64   `json:"leeching" gorm:"column:leeching"`
	Invites   int64   `json:"invites" gorm:"column:invites"`
	JoinDate  string  `json:"join_date" gorm:"column:join_date;size:64"`
	// LevelsJSON 存该站完整等级体系（等级名/权限/升级条件/保号标记）的 JSON 文本。
	// 由 siteprofile 服务写入与解析，接口层输出时展开为 levels 数组（不直接暴露本字段）。
	LevelsJSON string `json:"-" gorm:"column:levels_json;type:text"`
	// KeepAliveLevel 是用户手动指定的保号等级名（空 = 按站点权限自动识别）。
	// ⚠️ 必须单独存一列，不能写进 LevelsJSON：等级体系每 7 天会重抓一次，写进缓存会被覆盖，
	// 用户改过的保号线就白改了。Upsert 的更新清单里也刻意不含本列。
	KeepAliveLevel string `json:"keep_alive_level" gorm:"column:keep_alive_level;size:128"`
	// Source 记录本次数据来源通道：nexusphp / json，便于排错时定位解析分支。
	Source string `json:"source" gorm:"column:source;size:16"`
	// Status 为 ok / fail / skip，前端据此着色；Message 存放失败原因或解析告警。
	Status    string `json:"status" gorm:"column:status;size:16"`
	Message   string `json:"message" gorm:"column:message;size:512"`
	FetchedAt string `json:"fetched_at" gorm:"column:fetched_at;size:32"`
	UpdatedAt string `json:"updated_at" gorm:"column:updated_at;size:32"`
}

func (SiteProfile) TableName() string { return "site_profiles" }

// SiteProfileRepository 封装站点账号数据的读写。
type SiteProfileRepository struct {
	store *Store
}

// NewSiteProfileRepository 创建站点账号数据仓库。
func NewSiteProfileRepository(store *Store) *SiteProfileRepository {
	return &SiteProfileRepository{store: store}
}

// Migrate 确保 site_profiles 表存在（列结构由 GORM 推导，三方言通用）。
func (r *SiteProfileRepository) Migrate() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.AutoMigrate(&SiteProfile{})
}

// List 返回全部站点账号数据，按站点昵称排序。
func (r *SiteProfileRepository) List() ([]SiteProfile, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, nil
	}
	var profiles []SiteProfile
	err := r.store.DB.Order("nickname ASC").Find(&profiles).Error
	return profiles, err
}

// GetBySiteCode 按站点代码取单条记录，不存在时返回空结构且 err 为 nil。
func (r *SiteProfileRepository) GetBySiteCode(siteCode string) (SiteProfile, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return SiteProfile{}, nil
	}
	var profile SiteProfile
	err := r.store.DB.Where("site_code = ?", siteCode).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SiteProfile{}, nil
		}
		return SiteProfile{}, err
	}
	return profile, nil
}

// Upsert 按 site_code 覆盖写入一条记录。
// 参数/返回：profile.SiteCode 必须非空；成功返回 nil。
// 失败场景：数据库未初始化、site_code 为空、写入失败。
// 副作用：会新增或更新 site_profiles 行；不修改 created 时间（本表无该列）。
//
// 说明：不用 `ON CONFLICT` 之类的方言语法，先查后写，保证 sqlite/mysql/postgresql 一致。
func (r *SiteProfileRepository) Upsert(profile SiteProfile) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	now := time.Now().In(DatabaseTimeZone).Format("2006-01-02 15:04:05")
	profile.UpdatedAt = now
	if profile.FetchedAt == "" {
		profile.FetchedAt = now
	}

	existing, err := r.GetBySiteCode(profile.SiteCode)
	if err != nil {
		return err
	}
	if existing.ID > 0 {
		// 抓取失败或跳过时（Source 为空，携带的是一份空 Profile），不要用空值覆盖已有数据：
		// 否则一次 Cookie 失效就会把上传量/等级清成 0，日快照的增量也会跟着算成负数。
		if strings.TrimSpace(profile.Source) == "" {
			profile.UserID = firstNonEmpty(profile.UserID, existing.UserID)
			profile.Username = firstNonEmpty(profile.Username, existing.Username)
			profile.UserClass = firstNonEmpty(profile.UserClass, existing.UserClass)
			profile.Uploaded = keepPositiveInt(profile.Uploaded, existing.Uploaded, 0)
			profile.Downloaded = keepPositiveInt(profile.Downloaded, existing.Downloaded, 0)
			profile.Bonus = keepPositiveFloat(profile.Bonus, existing.Bonus, 0)
			profile.SeedBonus = keepPositiveFloat(profile.SeedBonus, existing.SeedBonus, 0)
			profile.Seeding = keepPositiveInt(profile.Seeding, existing.Seeding, 0)
			profile.Leeching = keepPositiveInt(profile.Leeching, existing.Leeching, 0)
			profile.Invites = keepPositiveInt(profile.Invites, existing.Invites, 0)
			profile.JoinDate = firstNonEmpty(profile.JoinDate, existing.JoinDate)
		}
		if profile.Ratio == 0 {
			profile.Ratio = existing.Ratio
		}
		if strings.TrimSpace(profile.LevelsJSON) == "" {
			profile.LevelsJSON = existing.LevelsJSON
		}
		profile.ID = existing.ID
		return r.store.DB.Model(&SiteProfile{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"nickname":    profile.Nickname,
			"base_url":    profile.BaseURL,
			"user_id":     profile.UserID,
			"username":    profile.Username,
			"user_class":  profile.UserClass,
			"uploaded":    profile.Uploaded,
			"downloaded":  profile.Downloaded,
			"ratio":       profile.Ratio,
			"bonus":       profile.Bonus,
			"seed_bonus":  profile.SeedBonus,
			"seeding":     profile.Seeding,
			"leeching":    profile.Leeching,
			"invites":     profile.Invites,
			"join_date":   profile.JoinDate,
			"source":      profile.Source,
			"status":      profile.Status,
			"message":     profile.Message,
			"levels_json": profile.LevelsJSON,
			"fetched_at":  profile.FetchedAt,
			"updated_at":  profile.UpdatedAt,
		}).Error
	}
	return r.store.DB.Create(&profile).Error
}

// UpdateKeepAliveLevel 写入用户手动指定的保号等级名（空串表示恢复自动识别）。
// 参数/返回：siteCode 必填；name 为等级表里的等级名或空；返回 error。
// 失败场景：数据库未初始化、站点无数据行、写入失败。
// 副作用：只更新 keep_alive_level 一列，不动抓取结果与等级缓存。
//
// 说明：用 Update 单列而不是 UpdateSiteProfile 全量写 —— 这里只改一个用户偏好，
// 不能顺手把上次抓取的上传量/等级/状态刷成零值。
func (r *SiteProfileRepository) UpdateKeepAliveLevel(siteCode, name string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	siteCode = strings.TrimSpace(siteCode)
	if siteCode == "" {
		return errors.New("站点代码不能为空")
	}
	// 先查后写：Update 影响 0 行分不清「站点不存在」和「值没变」（MySQL 未开
	// clientFoundRows 时值相同也返回 0），回查主键才是可靠的存在性判断。
	existing, err := r.GetBySiteCode(siteCode)
	if err != nil {
		return err
	}
	if existing.ID == 0 {
		return ErrSiteProfileNotFound
	}
	return r.store.DB.Model(&SiteProfile{}).Where("id = ?", existing.ID).
		Update("keep_alive_level", strings.TrimSpace(name)).Error
}

// ErrSiteProfileNotFound 表示该站点还没有抓取过数据（site_profiles 里没有对应行）。
// 属于请求错误：用户要先刷新一次，页面拿到等级表后才能设置保号等级。
var ErrSiteProfileNotFound = errors.New("site profile not found")
