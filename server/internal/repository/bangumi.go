package repository

import (
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// 番组条目匹配相关限制：控制单次检索返回的条目数与参与模糊匹配的候选标题数。
const (
	bangumiTmdbMatchLimit          = 10
	bangumiTitleMatchLimit         = 20
	bangumiTranslateLikeCandidates = 6
)

// bangumiMatchSeparators 标题匹配归一化时剔除的分隔符集合。
// ⚠️ SQL 侧（REPLACE 链）与 Go 侧（NormalizeBangumiMatchTitle）共用本列表，新增分隔符时两边同时生效。
var bangumiMatchSeparators = []string{" ", "\t", "-", "－", "_", "·", "・", ":", "：", "　"}

// NormalizeBangumiMatchTitle 归一化标题用于匹配：去首尾空白、转小写并剔除常见分隔符。
// 参数/返回：raw 为原始标题；返回归一化结果（仅用于比较，不用于展示）。
// 失败场景：无。
// 副作用：无。
func NormalizeBangumiMatchTitle(raw string) string {
	result := strings.ToLower(strings.TrimSpace(raw))
	for _, separator := range bangumiMatchSeparators {
		result = strings.ReplaceAll(result, separator, "")
	}
	return result
}

// bangumiNormalizedTitleExpr 构造标题归一化 SQL 表达式，语义与 NormalizeBangumiMatchTitle 一致。
// 三方言（sqlite/mysql/postgresql）均支持 LOWER / TRIM / COALESCE / REPLACE。
func bangumiNormalizedTitleExpr(column string) string {
	expr := "LOWER(TRIM(COALESCE(" + column + ", '')))"
	for _, separator := range bangumiMatchSeparators {
		expr = "REPLACE(" + expr + ", '" + separator + "', '')"
	}
	return expr
}

// escapeBangumiLikePattern 转义 LIKE 模式中的特殊字符（配合 ESCAPE '!' 使用，避免方言对反斜杠的差异）。
func escapeBangumiLikePattern(raw string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(raw)
}

// compactBangumiMatchCandidates 清理标题候选：去空白、丢弃无意义项并按归一化结果去重（保持入参顺序）。
func compactBangumiMatchCandidates(titles []string) []string {
	result := make([]string, 0, len(titles))
	seen := make(map[string]struct{}, len(titles))
	for _, title := range titles {
		trimmed := strings.TrimSpace(title)
		if trimmed == "" || len([]rune(trimmed)) > 200 {
			continue
		}
		key := NormalizeBangumiMatchTitle(trimmed)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

// BangumiItem 表示 Bangumi 番组数据源（bangumi-data）中的一条动画条目。
// 数据每次同步整表替换，因此不承载业务外键；bangumi_id 仅作检索索引，允许重复（上游存在重复条目）。
type BangumiItem struct {
	ID             int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	BangumiID      string `gorm:"column:bangumi_id" json:"bangumi_id"`
	Title          string `gorm:"column:title" json:"title"`
	TitleZH        string `gorm:"column:title_zh" json:"title_zh"`
	TitleTransJSON string `gorm:"column:title_translate_json" json:"-"`
	SitesJSON      string `gorm:"column:sites_json" json:"-"`
	ItemType       string `gorm:"column:item_type" json:"type"`
	Lang           string `gorm:"column:lang" json:"lang"`
	OfficialSite   string `gorm:"column:official_site" json:"official_site"`
	BeginAt        string `gorm:"column:begin_at" json:"begin"`
	EndAt          string `gorm:"column:end_at" json:"end"`
	BeginTimestamp int64  `gorm:"column:begin_ts" json:"begin_ts"`
	Broadcast      string `gorm:"column:broadcast" json:"broadcast"`
	Comment        string `gorm:"column:comment" json:"comment"`
	TmdbID         string `gorm:"column:tmdb_id" json:"tmdb_id"`
	MalID          string `gorm:"column:mal_id" json:"mal_id"`
	AnidbID        string `gorm:"column:anidb_id" json:"anidb_id"`
	AniListID      string `gorm:"column:anilist_id" json:"anilist_id"`
	CreatedAt      string `gorm:"column:created_at" json:"-"`
	UpdatedAt      string `gorm:"column:updated_at" json:"-"`
}

// TableName 指定 BangumiItem 对应的数据表名。
func (BangumiItem) TableName() string { return "bangumi_items" }

// BangumiSiteLink 表示条目在某外部站点上的链接（如 bangumi / tmdb / mal / bilibili）。
type BangumiSiteLink struct {
	Site  string `json:"site"`
	Title string `json:"title"`
	Type  string `json:"type"`
	ID    string `json:"id"`
	URL   string `json:"url"`
}

// BangumiSyncMeta 描述一次同步的状态快照，持久化在 bangumi_sync_meta 单行记录中。
type BangumiSyncMeta struct {
	Status        string `json:"status"`
	Message       string `json:"message"`
	LastSyncAt    string `json:"last_sync_at"`
	LastAttemptAt string `json:"last_attempt_at"`
	NextSyncAt    string `json:"next_sync_at"`
	ItemCount     int    `json:"item_count"`
	DurationMs    int64  `json:"duration_ms"`
	SourceURL     string `json:"source_url"`
	Version       string `json:"version"`
	UpdatedAt     string `json:"updated_at"`
}

// BangumiListFilter 描述番组列表查询条件。
type BangumiListFilter struct {
	Keyword  string
	ItemType string
	Lang     string
	Page     int
	PageSize int
}

// 同步状态取值：running 表示正在同步，success / failed 表示上一次同步结果。
const (
	BangumiSyncStatusRunning = "running"
	BangumiSyncStatusSuccess = "success"
	BangumiSyncStatusFailed  = "failed"
)

const bangumiSyncMetaKey = "sync"

// BangumiRepository 提供 Bangumi 番组数据的读写能力。
type BangumiRepository struct {
	store *Store
}

// NewBangumiRepository 创建 Bangumi 数据仓储。
// 参数/返回：store 为数据库连接容器；返回可复用的仓储实例。
// 失败场景：无。
// 副作用：无副作用，仅构造对象。
func NewBangumiRepository(store *Store) *BangumiRepository {
	return &BangumiRepository{store: store}
}

// Ready 判断仓储是否可用。
func (r *BangumiRepository) Ready() bool {
	return r != nil && r.store != nil && r.store.DB != nil
}

// ReplaceItems 在单个事务中整表替换番组数据。
// 参数/返回：items 为本次同步解析出的全部条目；返回写入条数。
// 失败场景：仓储未初始化、items 为空（拒绝用空数据覆盖库内数据）或写入失败时返回错误。
// 副作用：清空并重建 bangumi_items 表内容（事务保证，失败自动回滚）。
func (r *BangumiRepository) ReplaceItems(items []BangumiItem) (int, error) {
	if !r.Ready() {
		return 0, errors.New("bangumi repository is nil")
	}
	// 空数据集通常是上游异常或解析失败，直接拒绝覆盖，避免把已有数据清空。
	if len(items) == 0 {
		return 0, errors.New("番组数据为空，已跳过本次写入")
	}

	err := r.store.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM bangumi_items").Error; err != nil {
			return err
		}
		return tx.CreateInBatches(items, 100).Error
	})
	if err != nil {
		return 0, err
	}
	return len(items), nil
}

// ListItems 分页查询番组条目，支持关键字与类型/语言筛选。
// 参数/返回：filter 为查询条件；返回条目列表与命中总数。
// 失败场景：仓储未初始化或数据库查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
func (r *BangumiRepository) ListItems(filter BangumiListFilter) ([]BangumiItem, int64, error) {
	if !r.Ready() {
		return nil, 0, errors.New("bangumi repository is nil")
	}
	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	query := r.store.DB.Table("bangumi_items")
	if keyword := strings.TrimSpace(filter.Keyword); keyword != "" {
		like := "%" + keyword + "%"
		query = query.Where(
			"title LIKE ? OR title_zh LIKE ? OR bangumi_id LIKE ? OR tmdb_id LIKE ? OR mal_id LIKE ?",
			like, like, like, like, like,
		)
	}
	if itemType := strings.TrimSpace(filter.ItemType); itemType != "" && !strings.EqualFold(itemType, "all") {
		query = query.Where("item_type = ?", itemType)
	}
	if lang := strings.TrimSpace(filter.Lang); lang != "" && !strings.EqualFold(lang, "all") {
		query = query.Where("lang = ?", lang)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]BangumiItem, 0, pageSize)
	offset := (page - 1) * pageSize
	if err := query.
		Order("begin_ts DESC, id ASC").
		Limit(pageSize).
		Offset(offset).
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CountByType 统计各类别（tv/movie/ova/web）的条目数。
// 参数/返回：返回类别计数映射与总条目数。
// 失败场景：仓储未初始化或数据库查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
func (r *BangumiRepository) CountByType() (map[string]int64, int64, error) {
	if !r.Ready() {
		return nil, 0, errors.New("bangumi repository is nil")
	}
	rows := make([]struct {
		ItemType string `gorm:"column:item_type"`
		Total    int64  `gorm:"column:total"`
	}, 0)
	if err := r.store.DB.Table("bangumi_items").
		Select("item_type, COUNT(1) AS total").
		Group("item_type").
		Scan(&rows).Error; err != nil {
		return nil, 0, err
	}

	counts := make(map[string]int64, len(rows))
	var total int64
	for _, row := range rows {
		key := strings.TrimSpace(row.ItemType)
		if key == "" {
			key = "unknown"
		}
		counts[key] += row.Total
		total += row.Total
	}
	return counts, total, nil
}

// LoadSyncMeta 读取同步元信息；记录不存在时返回零值快照而非错误。
// 参数/返回：返回元信息快照。
// 失败场景：仓储未初始化或查询异常时返回错误。
// 副作用：仅读取 bangumi_sync_meta 表。
func (r *BangumiRepository) LoadSyncMeta() (*BangumiSyncMeta, error) {
	if !r.Ready() {
		return nil, errors.New("bangumi repository is nil")
	}
	rows := make([]struct {
		ValueJSON string `gorm:"column:value_json"`
	}, 0, 1)
	if err := r.store.DB.Raw(
		"SELECT value_json FROM bangumi_sync_meta WHERE meta_key = ? LIMIT 1",
		bangumiSyncMetaKey,
	).Scan(&rows).Error; err != nil {
		return nil, err
	}
	meta := &BangumiSyncMeta{Status: "idle"}
	if len(rows) == 0 || strings.TrimSpace(rows[0].ValueJSON) == "" {
		return meta, nil
	}
	if err := json.Unmarshal([]byte(rows[0].ValueJSON), meta); err != nil {
		// 元信息损坏不应阻塞功能，回退为零值快照。
		return &BangumiSyncMeta{Status: "idle", Message: "同步元信息解析失败"}, nil
	}
	if strings.TrimSpace(meta.Status) == "" {
		meta.Status = "idle"
	}
	return meta, nil
}

// SaveSyncMeta 写入同步元信息（单行 upsert）。
// 参数/返回：meta 为待持久化的快照；成功返回 nil。
// 失败场景：仓储未初始化、序列化失败或写入失败时返回错误。
// 副作用：写入 bangumi_sync_meta 表。
func (r *BangumiRepository) SaveSyncMeta(meta *BangumiSyncMeta) error {
	if !r.Ready() {
		return errors.New("bangumi repository is nil")
	}
	if meta == nil {
		return errors.New("bangumi sync meta is nil")
	}
	now := time.Now().Format("2006-01-02 15:04:05")
	meta.UpdatedAt = now
	content, err := json.Marshal(meta)
	if err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(r.store.DBType)) {
	case "mysql":
		return r.store.DB.Exec(
			`INSERT INTO bangumi_sync_meta (meta_key, value_json, created_at, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON DUPLICATE KEY UPDATE value_json = VALUES(value_json), updated_at = VALUES(updated_at)`,
			bangumiSyncMetaKey, string(content), now, now,
		).Error
	case "postgresql":
		return r.store.DB.Exec(
			`INSERT INTO bangumi_sync_meta (meta_key, value_json, created_at, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT (meta_key) DO UPDATE
			 SET value_json = EXCLUDED.value_json, updated_at = EXCLUDED.updated_at`,
			bangumiSyncMetaKey, string(content), now, now,
		).Error
	default:
		return r.store.DB.Exec(
			`INSERT INTO bangumi_sync_meta (meta_key, value_json, created_at, updated_at)
			 VALUES (?, ?, ?, ?)
			 ON CONFLICT(meta_key) DO UPDATE
			 SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
			bangumiSyncMetaKey, string(content), now, now,
		).Error
	}
}

// ParseBangumiTitleTranslate 解析条目译名 JSON，失败时返回空映射。
func ParseBangumiTitleTranslate(raw string) map[string][]string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string][]string{}
	}
	result := map[string][]string{}
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return map[string][]string{}
	}
	return result
}

// ParseBangumiSites 解析条目站点链接 JSON，失败时返回空切片。
func ParseBangumiSites(raw string) []BangumiSiteLink {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return []BangumiSiteLink{}
	}
	result := make([]BangumiSiteLink, 0, 8)
	if err := json.Unmarshal([]byte(trimmed), &result); err != nil {
		return []BangumiSiteLink{}
	}
	return result
}

// FindItemsByTmdbID 按 TMDB 站点 ID 检索番组条目。
// 参数/返回：tmdbID 为 TMDB 数字 ID；limit 为返回上限（<=0 时取默认上限）；返回按播出时间倒序的条目。
// 失败场景：仓储未初始化或查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
// 说明：库内 tmdb_id 保存的是上游站点原始 ID（形如 tv/209867，可能带 /season/1/episode/29 后缀），
// 因此按“等于 ID”“以 /ID 结尾”“含 /ID/”三种形态匹配，避免把 2098670 误判为 209867。
func (r *BangumiRepository) FindItemsByTmdbID(tmdbID string, limit int) ([]BangumiItem, error) {
	if !r.Ready() {
		return nil, errors.New("bangumi repository is nil")
	}
	id := strings.TrimSpace(tmdbID)
	if id == "" {
		return nil, nil
	}
	if limit <= 0 || limit > bangumiTmdbMatchLimit {
		limit = bangumiTmdbMatchLimit
	}
	rows := make([]BangumiItem, 0, limit)
	if err := r.store.DB.Table("bangumi_items").
		Where("tmdb_id = ? OR tmdb_id LIKE ? OR tmdb_id LIKE ?", id, "%/"+id, "%/"+id+"/%").
		Order("begin_ts DESC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// FindItemsByTitles 按标题集合检索番组条目：先精确匹配原名/中文名，再回退译名表模糊匹配。
// 参数/返回：titles 为候选标题（调用方按可信度降序排列）；limit 为返回上限（<=0 时取默认上限）。
// 失败场景：仓储未初始化或查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
func (r *BangumiRepository) FindItemsByTitles(titles []string, limit int) ([]BangumiItem, error) {
	if !r.Ready() {
		return nil, errors.New("bangumi repository is nil")
	}
	candidates := compactBangumiMatchCandidates(titles)
	if len(candidates) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > bangumiTitleMatchLimit {
		limit = bangumiTitleMatchLimit
	}

	normalized := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalized = append(normalized, NormalizeBangumiMatchTitle(candidate))
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(normalized)), ",")
	args := make([]any, 0, len(normalized)*2)
	for _, value := range normalized {
		args = append(args, value)
	}
	for _, value := range normalized {
		args = append(args, value)
	}

	rows := make([]BangumiItem, 0, limit)
	titleExpr := bangumiNormalizedTitleExpr("title")
	titleZHExpr := bangumiNormalizedTitleExpr("title_zh")
	if err := r.store.DB.Table("bangumi_items").
		Where(titleExpr+" IN ("+placeholders+") OR "+titleZHExpr+" IN ("+placeholders+")", args...).
		Order("begin_ts DESC").
		Limit(limit).
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	seen := make(map[int64]struct{}, len(rows))
	for _, row := range rows {
		seen[row.ID] = struct{}{}
	}

	// 译名表（titleTranslate）模糊匹配：覆盖只存在于英文/日文等别名中的写法。
	if len(candidates) > bangumiTranslateLikeCandidates {
		candidates = candidates[:bangumiTranslateLikeCandidates]
	}
	likeExprs := make([]string, 0, len(candidates))
	likeArgs := make([]any, 0, len(candidates))
	for _, candidate := range candidates {
		likeExprs = append(likeExprs, "title_translate_json LIKE ? ESCAPE '!'")
		likeArgs = append(likeArgs, "%"+escapeBangumiLikePattern(candidate)+"%")
	}
	extra := make([]BangumiItem, 0, limit)
	if err := r.store.DB.Table("bangumi_items").
		Where("("+strings.Join(likeExprs, " OR ")+")", likeArgs...).
		Order("begin_ts DESC").
		Limit(limit).
		Scan(&extra).Error; err != nil {
		return nil, err
	}
	for _, row := range extra {
		if _, exists := seen[row.ID]; exists {
			continue
		}
		seen[row.ID] = struct{}{}
		rows = append(rows, row)
	}
	return rows, nil
}
