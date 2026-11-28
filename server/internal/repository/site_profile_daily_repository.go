package repository

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// SiteProfileDaily 是「站点账号数据」的按日快照，用于计算当天各站点的增量。
// 参数/返回：字段一一映射 site_profile_daily 表，site_code + stat_date 唯一。
// 失败场景：无直接失败场景。
// 副作用：无副作用，仅承载数据。
//
// 为什么需要这张表：site_profiles 只保留每个站点的最新累计值（上传量/下载量/做种积分/魔力值都是只增不减的累计量），
// 单看累计值无法回答「今天涨了多少」。每天落一份快照后，当天增量 = 当天最后采样值 - 当天基线值。
//
// 基线（Base*）的语义：当天第一次采样时，把「上一条早于当天的快照」的值锁定为基线并整日不变。
// 这样一天内多次刷新得到的增量始终是「相对日初」的增量，不会因为反复刷新而缩水或归零。
type SiteProfileDaily struct {
	ID int64 `json:"id" gorm:"column:id;primaryKey"`
	// ⚠️ 复合唯一索引里的 string 必须给 size：MySQL 驱动会把未指定长度的 string 建成 longtext，
	// longtext 上建索引直接报 Error 1170 (BLOB/TEXT column used in key specification without a key length)。
	SiteCode string `json:"site_code" gorm:"column:site_code;size:64;uniqueIndex:uk_site_daily,priority:1"`
	// StatDate 为统计日期（YYYY-MM-DD，按数据库时区，默认 Asia/Shanghai），跨日边界以它为准。
	StatDate string `json:"stat_date" gorm:"column:stat_date;size:10;uniqueIndex:uk_site_daily,priority:2"`
	Nickname string `json:"nickname" gorm:"column:nickname;size:128"`

	// 当天最后一次采样值（累计量）。
	Uploaded   int64   `json:"uploaded" gorm:"column:uploaded"`
	Downloaded int64   `json:"downloaded" gorm:"column:downloaded"`
	Bonus      float64 `json:"bonus" gorm:"column:bonus"`
	SeedBonus  float64 `json:"seed_bonus" gorm:"column:seed_bonus"`
	Ratio      float64 `json:"ratio" gorm:"column:ratio"`
	Seeding    int64   `json:"seeding" gorm:"column:seeding"`
	Leeching   int64   `json:"leeching" gorm:"column:leeching"`

	// 日初基线（首次采样时锁定），用于减去「当天之前已经积累的量」。
	BaseUploaded   int64   `json:"base_uploaded" gorm:"column:base_uploaded"`
	BaseDownloaded int64   `json:"base_downloaded" gorm:"column:base_downloaded"`
	BaseBonus      float64 `json:"base_bonus" gorm:"column:base_bonus"`
	BaseSeedBonus  float64 `json:"base_seed_bonus" gorm:"column:base_seed_bonus"`
	BaseRatio      float64 `json:"base_ratio" gorm:"column:base_ratio"`
	BaseSeeding    int64   `json:"base_seeding" gorm:"column:base_seeding"`
	// BaseDate 记录基线来自哪一天；当上次采样不是昨天时，前端据此提示「增量实际跨了几天」。
	BaseDate string `json:"base_date" gorm:"column:base_date;size:10"`
	// HasBaseline 为 1 表示当天有可比基线；首日启用时无基线，增量只能显示「—」而不是把累计量当增量。
	HasBaseline int8 `json:"has_baseline" gorm:"column:has_baseline"`

	// Samples 为当天采样次数，便于判断数据新鲜度（只刷过一次的站点增量最可信）。
	Samples int    `json:"samples" gorm:"column:samples"`
	FirstAt string `json:"first_at" gorm:"column:first_at;size:32"`
	LastAt  string `json:"last_at" gorm:"column:last_at;size:32"`
}

func (SiteProfileDaily) TableName() string { return "site_profile_daily" }

// SiteProfileDailyRepository 封装站点账号日快照的读写。
type SiteProfileDailyRepository struct {
	store *Store
}

// NewSiteProfileDailyRepository 创建日快照仓库。
func NewSiteProfileDailyRepository(store *Store) *SiteProfileDailyRepository {
	return &SiteProfileDailyRepository{store: store}
}

// Migrate 确保 site_profile_daily 表存在（列结构由 GORM 推导，三方言通用）。
func (r *SiteProfileDailyRepository) Migrate() error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil
	}
	return r.store.DB.AutoMigrate(&SiteProfileDaily{})
}

// GetDaily 取某站点某天的快照；不存在时返回空结构且 err 为 nil。
func (r *SiteProfileDailyRepository) GetDaily(siteCode, statDate string) (SiteProfileDaily, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return SiteProfileDaily{}, nil
	}
	var row SiteProfileDaily
	err := r.store.DB.
		Where("site_code = ? AND stat_date = ?", strings.TrimSpace(siteCode), strings.TrimSpace(statDate)).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SiteProfileDaily{}, nil
		}
		return SiteProfileDaily{}, err
	}
	return row, nil
}

// LatestBefore 取某站点在指定日期之前（不含当天）的最近一条快照，作为当天基线。
// 参数/返回：statDate 为 YYYY-MM-DD；不存在时返回空结构与 false。
// 失败场景：数据库未初始化（返回 false）、查询失败。
// 副作用：无（只读）。
func (r *SiteProfileDailyRepository) LatestBefore(siteCode, statDate string) (SiteProfileDaily, bool, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return SiteProfileDaily{}, false, nil
	}
	var row SiteProfileDaily
	err := r.store.DB.
		Where("site_code = ? AND stat_date < ?", strings.TrimSpace(siteCode), strings.TrimSpace(statDate)).
		Order("stat_date DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SiteProfileDaily{}, false, nil
		}
		return SiteProfileDaily{}, false, err
	}
	return row, true, nil
}

// ListByDate 返回指定日期所有站点的快照（按站点昵称排序）。
func (r *SiteProfileDailyRepository) ListByDate(statDate string) ([]SiteProfileDaily, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, nil
	}
	var rows []SiteProfileDaily
	err := r.store.DB.
		Where("stat_date = ?", strings.TrimSpace(statDate)).
		Order("nickname ASC").
		Find(&rows).Error
	return rows, err
}

// ListRange 返回 [from, to] 闭区间内所有站点的日快照（按日期升序、同一天按站点代码升序）。
// 参数/返回：from/to 为 YYYY-MM-DD；区间非法（from > to）时返回空列表。
// 失败场景：数据库未初始化返回空列表；查询失败返回错误。
// 副作用：无（只读）。
//
// 用途：上传增量趋势图一次取多天，避免前端按天循环请求。
func (r *SiteProfileDailyRepository) ListRange(from, to string) ([]SiteProfileDaily, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, nil
	}
	start := strings.TrimSpace(from)
	end := strings.TrimSpace(to)
	if start == "" || end == "" || start > end {
		return nil, nil
	}
	var rows []SiteProfileDaily
	err := r.store.DB.
		Where("stat_date >= ? AND stat_date <= ?", start, end).
		Order("stat_date ASC, site_code ASC").
		Find(&rows).Error
	return rows, err
}

// EarliestStatDate 返回整张表里最早的统计日期（YYYY-MM-DD）；表空或未初始化时返回空串。
// 参数/返回：无入参；返回最早日期或空串。
// 失败场景：数据库未初始化返回空串；查询失败返回错误。
// 副作用：无（只读）。
//
// 用途：「全部时间」趋势查询需要知道数据从哪天开始，否则前端没法算起始日期。
func (r *SiteProfileDailyRepository) EarliestStatDate() (string, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return "", nil
	}
	var earliest sql.NullString
	if err := r.store.DB.Model(&SiteProfileDaily{}).Select("MIN(stat_date)").Scan(&earliest).Error; err != nil {
		return "", err
	}
	if !earliest.Valid {
		return "", nil
	}
	return strings.TrimSpace(earliest.String), nil
}

// SaveSample 记录一次采样并返回写入后的当天快照。
//
// 参数/返回：sample 至少要有 SiteCode/StatDate；返回落库后的整行数据。
// 失败场景：数据库未初始化（原样返回）、统计日期为空、读写失败。
// 副作用：新增或更新 site_profile_daily 中该站点当天的行。
//
// 处理规则：
//  1. 当天首次采样：读取「上一条早于当天的快照」锁定为基线（有则 HasBaseline=1，BaseDate 记其日期）；
//  2. 当天非首次采样：基线保持不变，只更新最新值与采样次数；
//  3. 数值为 0 的字段（站点没渲染该字段 / 本次解析缺失）沿用当天旧值，再退到基线值，
//     避免「没抓到」被写成 0 从而算出巨大的负增量。
func (r *SiteProfileDailyRepository) SaveSample(sample SiteProfileDaily) (SiteProfileDaily, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return sample, nil
	}
	sample.SiteCode = strings.TrimSpace(sample.SiteCode)
	sample.StatDate = strings.TrimSpace(sample.StatDate)
	if sample.SiteCode == "" || sample.StatDate == "" {
		return sample, errors.New("站点代码与统计日期不能为空")
	}

	now := time.Now().In(DatabaseTimeZone).Format("2006-01-02 15:04:05")
	existing, err := r.GetDaily(sample.SiteCode, sample.StatDate)
	if err != nil {
		return sample, err
	}

	if existing.ID > 0 {
		// 当天已有快照：基线不动，缺字段沿用当天旧值。
		sample.ID = existing.ID
		sample.BaseUploaded = existing.BaseUploaded
		sample.BaseDownloaded = existing.BaseDownloaded
		sample.BaseBonus = existing.BaseBonus
		sample.BaseSeedBonus = existing.BaseSeedBonus
		sample.BaseRatio = existing.BaseRatio
		sample.BaseSeeding = existing.BaseSeeding
		sample.BaseDate = existing.BaseDate
		sample.HasBaseline = existing.HasBaseline
		sample.FirstAt = existing.FirstAt
		sample.Samples = existing.Samples + 1
		sample.LastAt = now
		if sample.FirstAt == "" {
			sample.FirstAt = now
		}
		if sample.Nickname == "" {
			sample.Nickname = existing.Nickname
		}

		sample.Uploaded = keepPositiveInt(sample.Uploaded, existing.Uploaded, existing.BaseUploaded)
		sample.Downloaded = keepPositiveInt(sample.Downloaded, existing.Downloaded, existing.BaseDownloaded)
		sample.Bonus = keepPositiveFloat(sample.Bonus, existing.Bonus, existing.BaseBonus)
		sample.SeedBonus = keepPositiveFloat(sample.SeedBonus, existing.SeedBonus, existing.BaseSeedBonus)
		sample.Seeding = keepPositiveInt(sample.Seeding, existing.Seeding, existing.BaseSeeding)
		sample.Leeching = keepPositiveInt(sample.Leeching, existing.Leeching, 0)
		if sample.Ratio == 0 {
			sample.Ratio = existing.Ratio
		}

		err = r.store.DB.Model(&SiteProfileDaily{}).Where("id = ?", existing.ID).Updates(map[string]any{
			"nickname":        sample.Nickname,
			"uploaded":        sample.Uploaded,
			"downloaded":      sample.Downloaded,
			"bonus":           sample.Bonus,
			"seed_bonus":      sample.SeedBonus,
			"ratio":           sample.Ratio,
			"seeding":         sample.Seeding,
			"leeching":        sample.Leeching,
			"base_uploaded":   sample.BaseUploaded,
			"base_downloaded": sample.BaseDownloaded,
			"base_bonus":      sample.BaseBonus,
			"base_seed_bonus": sample.BaseSeedBonus,
			"base_ratio":      sample.BaseRatio,
			"base_seeding":    sample.BaseSeeding,
			"base_date":       sample.BaseDate,
			"has_baseline":    sample.HasBaseline,
			"samples":         sample.Samples,
			"first_at":        sample.FirstAt,
			"last_at":         sample.LastAt,
		}).Error
		return sample, err
	}

	// 当天首次采样：把上一条更早的快照锁定为基线。
	previous, ok, err := r.LatestBefore(sample.SiteCode, sample.StatDate)
	if err != nil {
		return sample, err
	}
	if ok {
		sample.HasBaseline = 1
		sample.BaseDate = previous.StatDate
		sample.BaseUploaded = previous.Uploaded
		sample.BaseDownloaded = previous.Downloaded
		sample.BaseBonus = previous.Bonus
		sample.BaseSeedBonus = previous.SeedBonus
		sample.BaseRatio = previous.Ratio
		sample.BaseSeeding = previous.Seeding
	} else {
		sample.HasBaseline = 0
		sample.BaseUploaded = sample.Uploaded
		sample.BaseDownloaded = sample.Downloaded
		sample.BaseBonus = sample.Bonus
		sample.BaseSeedBonus = sample.SeedBonus
		sample.BaseRatio = sample.Ratio
		sample.BaseSeeding = sample.Seeding
	}
	sample.Samples = 1
	sample.FirstAt = now
	sample.LastAt = now
	if err := r.store.DB.Create(&sample).Error; err != nil {
		return sample, err
	}
	return sample, nil
}

// keepPositiveInt 本次值为 0 时依次退回当天旧值、基线值（用于「字段没解析到」的兜底）。
func keepPositiveInt(current, sameDay, baseline int64) int64 {
	if current > 0 {
		return current
	}
	if sameDay > 0 {
		return sameDay
	}
	return baseline
}

// keepPositiveFloat 同 keepPositiveInt，用于浮点字段（魔力值/做种积分）。
func keepPositiveFloat(current, sameDay, baseline float64) float64 {
	if current > 0 {
		return current
	}
	if sameDay > 0 {
		return sameDay
	}
	return baseline
}
