package repository

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// PublishQueueTimeLayout 为队列任务存储时间字段的统一格式（保证跨 DB 的可读性与可排序性）。
const PublishQueueTimeLayout = "2006-01-02 15:04:05"

const (
	PublishQueueStatusQueued    = "queued"
	PublishQueueStatusRunning   = "running"
	PublishQueueStatusSuccess   = "success"
	PublishQueueStatusFailed    = "failed"
	PublishQueueStatusCancelled = "cancelled"
	// PublishQueueStatusDispatched 表示「已派发」：由「立即发布」的内存 runner 执行，
	// 仅用于进度登记展示。队列调度器只领取 queued，因此不会重复发布这些任务。
	PublishQueueStatusDispatched = "dispatched"
)

var (
	// ErrPublishQueueTaskNotFound 表示队列任务不存在。
	ErrPublishQueueTaskNotFound = errors.New("publish queue task not found")
	// ErrPublishQueueTaskNotQueued 表示队列任务当前不是 queued 状态。
	ErrPublishQueueTaskNotQueued = errors.New("publish queue task is not queued")
)

// PublishQueueTask 表示一条待执行的发布队列任务记录（单目标站点粒度）。
type PublishQueueTask struct {
	ID int64 `json:"id" gorm:"column:id;primaryKey"`

	GroupID string `json:"group_id" gorm:"column:group_id"`
	Status  string `json:"status" gorm:"column:status"`

	TaskID     string `json:"task_id" gorm:"column:task_id"`
	Trigger    string `json:"trigger" gorm:"column:publish_trigger"`
	Scene      string `json:"scene" gorm:"column:scene"`
	TorrentID  string `json:"torrent_id" gorm:"column:torrent_id"`
	SourceSite string `json:"source_site" gorm:"column:source_site"`
	TargetSite string `json:"target_site" gorm:"column:target_site"`

	DownloaderID string `json:"downloader_id" gorm:"column:downloader_id"`
	Title        string `json:"title" gorm:"column:title"`
	Subtitle     string `json:"subtitle" gorm:"column:subtitle"`

	PayloadJSON    string `json:"payload_json" gorm:"column:payload_json"`
	UploadDataJSON string `json:"upload_data_json" gorm:"column:upload_data_json"`
	ContextJSON    string `json:"context_json" gorm:"column:context_json"`

	AttemptCount int `json:"attempt_count" gorm:"column:attempt_count"`

	ScheduledAt *string `json:"scheduled_at,omitempty" gorm:"column:scheduled_at"`
	NextRunAt   *string `json:"next_run_at,omitempty" gorm:"column:next_run_at"`
	StartedAt   *string `json:"started_at,omitempty" gorm:"column:started_at"`
	FinishedAt  *string `json:"finished_at,omitempty" gorm:"column:finished_at"`

	LastError  string `json:"last_error" gorm:"column:last_error"`
	LastResult string `json:"last_result" gorm:"column:last_result"`

	CreatedAt string `json:"created_at" gorm:"column:created_at"`
	UpdatedAt string `json:"updated_at" gorm:"column:updated_at"`

	// EffectiveScheduledAt 为展示用派生字段（不落库）：任务真正可执行的时间，
	// 取 scheduled_at 与 next_run_at 中较晚者，都为空时退回 created_at（立即执行）。
	EffectiveScheduledAt string `json:"effective_scheduled_at" gorm:"-"`
}

func (PublishQueueTask) TableName() string { return "publish_queue_tasks" }

// PublishQueueRepository 负责发布队列任务的入库、领取与状态更新。
// 参数/返回：依赖 Store 访问数据库；方法返回 error 表示失败原因。
// 失败场景：DB 未初始化、事务/更新失败等。
// 副作用：会写入/更新/删除 publish_queue_tasks 表。
type PublishQueueRepository struct {
	store *Store
}

// NewPublishQueueRepository 创建发布队列仓储实例。
// 参数/返回：store 为数据库连接容器；返回仓储对象。
// 失败场景：无直接失败场景。
// 副作用：无。
func NewPublishQueueRepository(store *Store) *PublishQueueRepository {
	return &PublishQueueRepository{store: store}
}

// DB 返回底层 gorm.DB，供 Service 复用事务与查询。
// 参数/返回：无入参；返回 DB 指针（仓储未就绪时返回 nil）。
// 失败场景：无。
// 副作用：无。
func (r *PublishQueueRepository) DB() *gorm.DB {
	if r == nil || r.store == nil {
		return nil
	}
	return r.store.DB
}

// EnqueueTasks 批量写入发布队列任务。
// 参数/返回：tasks 为待写入任务；返回写入后的任务切片（包含自增 ID）与 error。
// 失败场景：数据库不可用或写入失败返回 error。
// 副作用：写入 publish_queue_tasks。
func (r *PublishQueueRepository) EnqueueTasks(tasks []PublishQueueTask) ([]PublishQueueTask, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, errors.New("publish queue repo is nil")
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	now := time.Now().Format(PublishQueueTimeLayout)
	for idx := range tasks {
		if strings.TrimSpace(tasks[idx].Status) == "" {
			tasks[idx].Status = PublishQueueStatusQueued
		}
		if strings.TrimSpace(tasks[idx].Trigger) == "" {
			tasks[idx].Trigger = "queue"
		}
		if strings.TrimSpace(tasks[idx].CreatedAt) == "" {
			tasks[idx].CreatedAt = now
		}
		tasks[idx].UpdatedAt = now
	}

	if err := r.store.DB.Table("publish_queue_tasks").Create(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// CountActiveTasks 统计当前队列中“排队中/运行中”的任务数。
// 参数/返回：无入参；返回数量与 error。
// 失败场景：数据库查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
func (r *PublishQueueRepository) CountActiveTasks() (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}
	var count int64
	if err := r.store.DB.Table("publish_queue_tasks").
		Where("status IN ?", []string{PublishQueueStatusQueued, PublishQueueStatusRunning}).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

// ClaimNextRunnableTask 原子领取下一条可执行的 queued 任务，并标记为 running。
// 参数/返回：now 为当前时间；返回任务、是否命中与 error。
// 失败场景：事务/更新/查询失败返回 error。
// 副作用：更新 publish_queue_tasks.status/started_at/updated_at。
func (r *PublishQueueRepository) ClaimNextRunnableTask(now time.Time) (*PublishQueueTask, bool, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, false, errors.New("publish queue repo is nil")
	}

	nowText := now.Format(PublishQueueTimeLayout)
	claimed := (*PublishQueueTask)(nil)

	err := r.store.DB.Transaction(func(tx *gorm.DB) error {
		row := struct {
			ID int64 `gorm:"column:id"`
		}{}
		if err := tx.Raw(
			`SELECT id
			 FROM publish_queue_tasks
			 WHERE status = ?
			   AND (scheduled_at IS NULL OR scheduled_at <= ?)
			   AND (next_run_at IS NULL OR next_run_at <= ?)
			 ORDER BY COALESCE(next_run_at, created_at) ASC, id ASC
			 LIMIT 1`,
			PublishQueueStatusQueued,
			nowText,
			nowText,
		).Scan(&row).Error; err != nil {
			return err
		}
		if row.ID == 0 {
			return nil
		}

		result := tx.Exec(
			`UPDATE publish_queue_tasks
			 SET status = ?, started_at = ?, updated_at = ?
			 WHERE id = ? AND status = ?`,
			PublishQueueStatusRunning,
			nowText,
			nowText,
			row.ID,
			PublishQueueStatusQueued,
		)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		task := PublishQueueTask{}
		if err := tx.Raw(`SELECT * FROM publish_queue_tasks WHERE id = ?`, row.ID).Scan(&task).Error; err != nil {
			return err
		}
		claimed = &task
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if claimed == nil || claimed.ID == 0 {
		return nil, false, nil
	}
	return claimed, true, nil
}

// UpdateTaskAfterRequeue 将 running 任务重置为 queued，并写入下次运行时间与原因（不增加 attempt_count）。
// 参数/返回：id 为任务主键；nextRunAt 为下次可运行时间；reason/result 为调试信息；返回 error。
// 失败场景：更新失败返回 error。
// 副作用：更新 publish_queue_tasks 状态与字段。
func (r *PublishQueueRepository) UpdateTaskAfterRequeue(id int64, nextRunAt time.Time, reason string, result string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	nextText := nextRunAt.Format(PublishQueueTimeLayout)
	return r.store.DB.Table("publish_queue_tasks").
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      PublishQueueStatusQueued,
			"next_run_at": nextText,
			"started_at":  nil,
			"finished_at": nil,
			"last_error":  strings.TrimSpace(reason),
			"last_result": strings.TrimSpace(result),
			"updated_at":  nowText,
		}).Error
}

// UpdateTaskAfterFailure 记录失败并按需重试（写入 attempt_count 与 next_run_at）。
// 参数/返回：id 为任务主键；attemptCount 为最新次数；nextRunAt 为空表示不再重试；reason/result 为调试信息；返回 error。
// 失败场景：更新失败返回 error。
// 副作用：更新 publish_queue_tasks 状态与字段。
func (r *PublishQueueRepository) UpdateTaskAfterFailure(id int64, attemptCount int, nextRunAt *time.Time, reason string, result string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	status := PublishQueueStatusFailed
	nextText := (*string)(nil)
	if nextRunAt != nil {
		status = PublishQueueStatusQueued
		value := nextRunAt.Format(PublishQueueTimeLayout)
		nextText = &value
	}

	updates := map[string]any{
		"status":        status,
		"attempt_count": attemptCount,
		"next_run_at":   nextText,
		"last_error":    strings.TrimSpace(reason),
		"last_result":   strings.TrimSpace(result),
		"updated_at":    nowText,
	}
	if nextRunAt != nil {
		updates["started_at"] = nil
		updates["finished_at"] = nil
	} else {
		updates["finished_at"] = nowText
	}

	return r.store.DB.Table("publish_queue_tasks").
		Where("id = ?", id).
		Updates(updates).Error
}

// UpdateTaskAfterSuccess 将任务标记为成功并写入结果。
// 参数/返回：id 为任务主键；result 为调试信息；返回 error。
// 失败场景：更新失败返回 error。
// 副作用：更新 publish_queue_tasks 状态与字段。
func (r *PublishQueueRepository) UpdateTaskAfterSuccess(id int64, result string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	return r.store.DB.Table("publish_queue_tasks").
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      PublishQueueStatusSuccess,
			"next_run_at": nil,
			"finished_at": nowText,
			"last_error":  "",
			"last_result": strings.TrimSpace(result),
			"updated_at":  nowText,
		}).Error
}

// FindTaskByID 按主键读取队列任务。
// 参数/返回：id 为任务主键；返回任务、是否命中与 error。
// 失败场景：数据库查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
func (r *PublishQueueRepository) FindTaskByID(id int64) (*PublishQueueTask, bool, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, false, errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil, false, nil
	}

	task := PublishQueueTask{}
	if err := r.store.DB.Table("publish_queue_tasks").Where("id = ?", id).Limit(1).Find(&task).Error; err != nil {
		return nil, false, err
	}
	if task.ID <= 0 {
		return nil, false, nil
	}
	return &task, true, nil
}

// CancelQueuedTask 将 queued 状态任务取消为 cancelled，供 UI 删除待发布项使用。
// 参数/返回：id 为任务主键；reason 为取消原因；返回 error。
// 失败场景：任务不存在或非 queued 状态会返回对应错误；更新失败返回 error。
// 副作用：更新 publish_queue_tasks 状态与时间字段。
func (r *PublishQueueRepository) CancelQueuedTask(id int64, reason string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return ErrPublishQueueTaskNotFound
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	result := r.store.DB.Table("publish_queue_tasks").
		Where("id = ? AND status = ?", id, PublishQueueStatusQueued).
		Updates(map[string]any{
			"status":      PublishQueueStatusCancelled,
			"next_run_at": nil,
			"started_at":  nil,
			"finished_at": nowText,
			"last_error":  strings.TrimSpace(reason),
			"updated_at":  nowText,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	exists := int64(0)
	if err := r.store.DB.Table("publish_queue_tasks").Where("id = ?", id).Count(&exists).Error; err != nil {
		return err
	}
	if exists == 0 {
		return ErrPublishQueueTaskNotFound
	}
	return ErrPublishQueueTaskNotQueued
}

// PromoteQueuedTaskToNow 把 queued 任务的可执行时间提前到当前时刻，使队列下一轮扫描立即领取执行。
// 参数/返回：id 为任务主键；now 为当前时间；返回 error。
// 失败场景：任务不存在返回 ErrPublishQueueTaskNotFound；任务非 queued 返回 ErrPublishQueueTaskNotQueued；更新失败返回 error。
// 副作用：更新 publish_queue_tasks 的 scheduled_at/next_run_at/last_error/updated_at（状态仍保持 queued）。
// 说明：仅改时间不改状态，任务依旧由队列调度器按正常流程领取执行，
// 因此不会绕过预检查、可发种时间等限制（不满足时会被重新排队）。
func (r *PublishQueueRepository) PromoteQueuedTaskToNow(id int64, now time.Time) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return ErrPublishQueueTaskNotFound
	}

	nowText := now.Format(PublishQueueTimeLayout)
	result := r.store.DB.Table("publish_queue_tasks").
		Where("id = ? AND status = ?", id, PublishQueueStatusQueued).
		Updates(map[string]any{
			"scheduled_at": nowText,
			"next_run_at":  nowText,
			"last_error":   "",
			"updated_at":   nowText,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	exists := int64(0)
	if err := r.store.DB.Table("publish_queue_tasks").Where("id = ?", id).Count(&exists).Error; err != nil {
		return err
	}
	if exists == 0 {
		return ErrPublishQueueTaskNotFound
	}
	return ErrPublishQueueTaskNotQueued
}

// PromoteWaveToNow 把同一批次的某一波（同 group_id + 同计划发布时间）待发布任务整体提前到当前时刻。
// 参数/返回：groupID 为队列分组；scheduledAt 为该波原计划发布时间（PublishQueueTimeLayout 文本）；
// now 为当前时间；返回实际提前的任务数与 error。
// 失败场景：DB 未初始化、参数为空或更新失败返回 error。
// 副作用：更新 publish_queue_tasks 的 scheduled_at/next_run_at/last_error/updated_at（状态仍保持 queued）。
func (r *PublishQueueRepository) PromoteWaveToNow(groupID string, scheduledAt string, now time.Time) (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}
	groupID = strings.TrimSpace(groupID)
	scheduledAt = strings.TrimSpace(scheduledAt)
	if groupID == "" || scheduledAt == "" {
		return 0, nil
	}

	nowText := now.Format(PublishQueueTimeLayout)
	result := r.store.DB.Table("publish_queue_tasks").
		Where("group_id = ? AND scheduled_at = ? AND status = ?", groupID, scheduledAt, PublishQueueStatusQueued).
		Updates(map[string]any{
			"scheduled_at": nowText,
			"next_run_at":  nowText,
			"last_error":   "",
			"updated_at":   nowText,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// BatchCancelByTaskIDs 批量取消 queued 状态的队列任务（仅取消仍处于 queued 的任务，跳过其他状态）。
// 参数/返回：ids 为队列任务主键列表；reason 为取消原因；返回实际取消行数与 error。
// 失败场景：DB 未初始化或更新失败返回 error。
// 副作用：UPDATE publish_queue_tasks 状态为 cancelled。
func (r *PublishQueueRepository) BatchCancelByTaskIDs(ids []int64, reason string) (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}
	if len(ids) == 0 {
		return 0, nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	result := r.store.DB.Table("publish_queue_tasks").
		Where("id IN ? AND status = ?", ids, PublishQueueStatusQueued).
		Updates(map[string]any{
			"status":      PublishQueueStatusCancelled,
			"next_run_at": nil,
			"started_at":  nil,
			"finished_at": nowText,
			"last_error":  strings.TrimSpace(reason),
			"updated_at":  nowText,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// CleanupFinishedTasks 清理已完成任务，避免队列表无限增长。
// 参数/返回：olderThan 为截止时间；返回删除条数与 error。
// 失败场景：数据库删除失败返回 error。
// 副作用：删除 publish_queue_tasks 表中 success/failed 的旧记录。
func (r *PublishQueueRepository) CleanupFinishedTasks(olderThan time.Time) (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}

	cutoffText := olderThan.Format(PublishQueueTimeLayout)
	result := r.store.DB.Table("publish_queue_tasks").
		Where(
			"status IN ? AND finished_at IS NOT NULL AND finished_at < ?",
			[]string{PublishQueueStatusSuccess, PublishQueueStatusFailed, PublishQueueStatusCancelled},
			cutoffText,
		).
		Delete(&PublishQueueTask{})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// PublishQueueTaskQuery 定义发布队列任务的分页查询条件。
// 参数/返回：数值字段为 0 表示使用默认值；字符串字段为空表示不过滤；切片字段为空表示不限定。
// 失败场景：无。
// 副作用：无。
type PublishQueueTaskQuery struct {
	Page     int
	PageSize int

	Search string

	Statuses      []string
	Trigger       string
	Scene         string
	QueueGroupID  string
	TargetSite    string
	SourceSite    string
	TorrentID     string
	DownloaderIDs []string
}

// PublishQueueTaskStatusCounts 汇总队列任务在筛选范围内的状态分布（供页面顶部概览使用）。
type PublishQueueTaskStatusCounts struct {
	Total      int64 `json:"total"`
	Queued     int64 `json:"queued"`
	Dispatched int64 `json:"dispatched"`
	Running    int64 `json:"running"`
	Success    int64 `json:"success"`
	Failed     int64 `json:"failed"`
	Cancelled  int64 `json:"cancelled"`
}

// publishQueueTaskOrderClause 让「发布中/待发布（含已派发）」按计划时间升序排到最前，历史记录按完成时间倒序跟随。
// 排序语句为跨 DB 兼容写法（MySQL / PostgreSQL / SQLite 均支持），不含任何外部输入。
const publishQueueTaskOrderClause = "CASE WHEN status IN ('running','queued','dispatched') THEN 0 ELSE 1 END ASC, " +
	"CASE WHEN status IN ('running','queued','dispatched') THEN COALESCE(scheduled_at, next_run_at, created_at) END ASC, " +
	"CASE WHEN status IN ('running','queued','dispatched') THEN NULL ELSE COALESCE(finished_at, updated_at, created_at) END DESC, " +
	"id DESC"

// ListTasks 按条件分页查询发布队列任务。
// 参数/返回：query 为查询条件；返回任务列表、总数与 error。
// 失败场景：DB 未初始化或查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
func (r *PublishQueueRepository) ListTasks(query PublishQueueTaskQuery) ([]PublishQueueTask, int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, 0, errors.New("publish queue repo is nil")
	}

	_, pageSize, offset := normalizePublishQueuePaging(query.Page, query.PageSize)

	var total int64
	if err := r.applyTaskFilters(r.store.DB.Model(&PublishQueueTask{}), query).Count(&total).Error; err != nil {
		return nil, 0, err
	}

	rows := make([]PublishQueueTask, 0)
	if err := r.applyTaskFilters(r.store.DB.Model(&PublishQueueTask{}), query).
		Order(publishQueueTaskOrderClause).
		Offset(offset).
		Limit(pageSize).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// CountTaskStatuses 统计筛选范围内的队列任务状态分布。
// 参数/返回：query 为查询条件（其中 Statuses 会被忽略）；返回状态计数与 error。
// 失败场景：DB 未初始化或查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
// 说明：刻意忽略状态筛选——页头概览需要展示整体分布，而列表可能正按某一状态过滤。
func (r *PublishQueueRepository) CountTaskStatuses(query PublishQueueTaskQuery) (PublishQueueTaskStatusCounts, error) {
	counts := PublishQueueTaskStatusCounts{}
	if r == nil || r.store == nil || r.store.DB == nil {
		return counts, errors.New("publish queue repo is nil")
	}

	query.Statuses = nil

	type statusRow struct {
		Status string `gorm:"column:status"`
		Total  int64  `gorm:"column:total"`
	}
	rows := make([]statusRow, 0)
	if err := r.applyTaskFilters(r.store.DB.Model(&PublishQueueTask{}), query).
		Select("status, COUNT(*) AS total").
		Group("status").
		Scan(&rows).Error; err != nil {
		return counts, err
	}

	for _, item := range rows {
		counts.Total += item.Total
		switch strings.TrimSpace(item.Status) {
		case PublishQueueStatusQueued:
			counts.Queued = item.Total
		case PublishQueueStatusDispatched:
			counts.Dispatched = item.Total
		case PublishQueueStatusRunning:
			counts.Running = item.Total
		case PublishQueueStatusSuccess:
			counts.Success = item.Total
		case PublishQueueStatusFailed:
			counts.Failed = item.Total
		case PublishQueueStatusCancelled:
			counts.Cancelled = item.Total
		}
	}
	return counts, nil
}

// PublishQueueWave 描述一个尚未到点的发布波次：同批次内计划发布时间相同的待发布任务视为一波。
type PublishQueueWave struct {
	GroupID     string `json:"group_id" gorm:"column:group_id"`
	ScheduledAt string `json:"scheduled_at" gorm:"column:scheduled_at"`
	TaskCount   int64  `json:"task_count" gorm:"column:task_count"`
}

// ListPendingWaves 列出筛选范围内计划发布时间仍在未来的待发布波次（按计划时间升序）。
// 参数/返回：query 为查询条件（Statuses 会被忽略，固定只看 queued）；now 为当前时间；limit 为最大波次数；
// 返回波次列表与 error。
// 失败场景：DB 未初始化或查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
// 说明：scheduled_at 为该表统一的定长本地时间字符串，字符串序即时间序，可直接比较。
// 只看 scheduled_at 而忽略 next_run_at，是为了把「发布节奏分波」与「预检查/可发种时间重排队」区分开：
// 前者写 scheduled_at，后者只写 next_run_at（此时 scheduled_at 已过期），因此等待限制解除的任务不会被当成下一波。
func (r *PublishQueueRepository) ListPendingWaves(query PublishQueueTaskQuery, now time.Time, limit int) ([]PublishQueueWave, error) {
	waves := make([]PublishQueueWave, 0)
	if r == nil || r.store == nil || r.store.DB == nil {
		return waves, errors.New("publish queue repo is nil")
	}
	if limit <= 0 {
		limit = 20
	}

	query.Statuses = nil
	nowText := now.Format(PublishQueueTimeLayout)

	if err := r.applyTaskFilters(r.store.DB.Model(&PublishQueueTask{}), query).
		Where("status = ?", PublishQueueStatusQueued).
		Where("scheduled_at IS NOT NULL AND scheduled_at <> '' AND scheduled_at > ?", nowText).
		Select("group_id, scheduled_at, COUNT(*) AS task_count").
		Group("group_id, scheduled_at").
		Order("scheduled_at ASC, group_id ASC").
		Limit(limit).
		Scan(&waves).Error; err != nil {
		return nil, err
	}
	return waves, nil
}

// ListWaveTasks 列出某一波（同 group_id + 同计划发布时间）中仍处于 queued 的任务。
// 参数/返回：groupID 为队列分组；scheduledAt 为该波计划发布时间文本；返回任务列表与 error。
// 失败场景：DB 未初始化或查询失败返回 error。
// 副作用：读取 publish_queue_tasks。
func (r *PublishQueueRepository) ListWaveTasks(groupID string, scheduledAt string) ([]PublishQueueTask, error) {
	rows := make([]PublishQueueTask, 0)
	if r == nil || r.store == nil || r.store.DB == nil {
		return rows, errors.New("publish queue repo is nil")
	}
	groupID = strings.TrimSpace(groupID)
	scheduledAt = strings.TrimSpace(scheduledAt)
	if groupID == "" || scheduledAt == "" {
		return rows, nil
	}

	if err := r.store.DB.Model(&PublishQueueTask{}).
		Where("group_id = ? AND scheduled_at = ? AND status = ?", groupID, scheduledAt, PublishQueueStatusQueued).
		Order("id ASC").
		Limit(200).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// applyTaskFilters 将查询条件拼接到 GORM 语句上（列表与状态统计共用，保证过滤口径一致）。
// 参数/返回：db 为基础语句；query 为查询条件；返回拼接后的语句。
// 失败场景：无。
// 副作用：无。
func (r *PublishQueueRepository) applyTaskFilters(db *gorm.DB, query PublishQueueTaskQuery) *gorm.DB {
	if statuses := normalizeQueueQueryValues(query.Statuses); len(statuses) > 0 {
		db = db.Where("status IN ?", statuses)
	}
	if value := strings.TrimSpace(query.Trigger); value != "" {
		db = db.Where("publish_trigger = ?", value)
	}
	if value := strings.TrimSpace(query.Scene); value != "" {
		db = db.Where("scene = ?", value)
	}
	if value := strings.TrimSpace(query.QueueGroupID); value != "" {
		db = db.Where("group_id = ?", value)
	}
	if value := strings.TrimSpace(query.TargetSite); value != "" {
		db = db.Where("target_site = ?", value)
	}
	if value := strings.TrimSpace(query.SourceSite); value != "" {
		db = db.Where("source_site = ?", value)
	}
	if value := strings.TrimSpace(query.TorrentID); value != "" {
		db = db.Where("torrent_id = ?", value)
	}
	if downloaderIDs := normalizeQueueQueryValues(query.DownloaderIDs); len(downloaderIDs) > 0 {
		db = db.Where("downloader_id IN ?", downloaderIDs)
	}
	if value := strings.TrimSpace(query.Search); value != "" {
		like := "%" + value + "%"
		if r.store.DBType == "postgresql" {
			db = db.Where(
				"(title ILIKE ? OR subtitle ILIKE ? OR torrent_id ILIKE ? OR target_site ILIKE ? OR source_site ILIKE ? OR task_id ILIKE ? OR group_id ILIKE ?)",
				like, like, like, like, like, like, like,
			)
		} else {
			db = db.Where(
				"(title LIKE ? OR subtitle LIKE ? OR torrent_id LIKE ? OR target_site LIKE ? OR source_site LIKE ? OR task_id LIKE ? OR group_id LIKE ?)",
				like, like, like, like, like, like, like,
			)
		}
	}
	return db
}

// normalizePublishQueuePaging 归一化分页参数（每页上限 200，避免一次拉全表）。
// 参数/返回：page/pageSize 为原始入参；返回归一化后的页码、每页条数与偏移量。
// 失败场景：无。
// 副作用：无。
func normalizePublishQueuePaging(page int, pageSize int) (int, int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	return page, pageSize, (page - 1) * pageSize
}

// normalizeQueueQueryValues 清理切片查询值（去空白、去空串，保持原顺序）。
// 参数/返回：values 为原始切片；返回清理后的切片（可能为空）。
// 失败场景：无。
// 副作用：无。
func normalizeQueueQueryValues(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, item := range values {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	return cleaned
}

// InsertDispatchedTasks 批量写入「已派发」进度记录（供「立即发布」登记进度）。
// 参数/返回：tasks 为待写入记录；返回带自增 ID 的记录与 error。
// 失败场景：DB 未初始化或写入失败返回 error。
// 副作用：写入 publish_queue_tasks（status=dispatched）。
// 说明：dispatched 不会被队列调度器领取（ClaimNextRunnableTask 只领 queued），
// 这些记录只做进度展示，实际执行由调用方的实时 runner 完成并回写状态。
func (r *PublishQueueRepository) InsertDispatchedTasks(tasks []PublishQueueTask) ([]PublishQueueTask, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return nil, errors.New("publish queue repo is nil")
	}
	if len(tasks) == 0 {
		return nil, nil
	}

	now := time.Now().Format(PublishQueueTimeLayout)
	for idx := range tasks {
		tasks[idx].Status = PublishQueueStatusDispatched
		if strings.TrimSpace(tasks[idx].Trigger) == "" {
			tasks[idx].Trigger = "batch_live"
		}
		if strings.TrimSpace(tasks[idx].CreatedAt) == "" {
			tasks[idx].CreatedAt = now
		}
		tasks[idx].UpdatedAt = now
	}

	if err := r.store.DB.Table("publish_queue_tasks").Create(&tasks).Error; err != nil {
		return nil, err
	}
	return tasks, nil
}

// MarkDispatchedRunning 把已派发记录标记为「发布中」并写入实际开始时间。
// 参数/返回：id 为任务主键；startedAt 为实际开始发布时间；返回 error。
// 失败场景：DB 未初始化或更新失败返回 error。
// 副作用：更新 publish_queue_tasks（status=dispatched → running）。
func (r *PublishQueueRepository) MarkDispatchedRunning(id int64, startedAt time.Time) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	return r.store.DB.Table("publish_queue_tasks").
		Where("id = ? AND status = ?", id, PublishQueueStatusDispatched).
		Updates(map[string]any{
			"status":     PublishQueueStatusRunning,
			"started_at": startedAt.Format(PublishQueueTimeLayout),
			"updated_at": nowText,
		}).Error
}

// MarkDispatchedFinished 写入已派发记录的最终结果。
// 参数/返回：id 为任务主键；success 决定 success/failed；result/errText 为结果与错误摘要；返回 error。
// 失败场景：DB 未初始化或更新失败返回 error。
// 副作用：更新 publish_queue_tasks。
func (r *PublishQueueRepository) MarkDispatchedFinished(id int64, success bool, result string, errText string) error {
	if r == nil || r.store == nil || r.store.DB == nil {
		return errors.New("publish queue repo is nil")
	}
	if id <= 0 {
		return nil
	}

	status := PublishQueueStatusFailed
	if success {
		status = PublishQueueStatusSuccess
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	return r.store.DB.Table("publish_queue_tasks").
		Where("id = ?", id).
		Updates(map[string]any{
			"status":      status,
			"next_run_at": nil,
			"finished_at": nowText,
			"last_result": strings.TrimSpace(result),
			"last_error":  strings.TrimSpace(errText),
			"updated_at":  nowText,
		}).Error
}

// MarkDispatchedCancelled 把仍处于「已派发」状态的记录标记为已取消（批次被用户取消时使用）。
// 参数/返回：ids 为任务主键列表；reason 为取消原因；返回受影响条数与 error。
// 失败场景：DB 未初始化或更新失败返回 error。
// 副作用：更新 publish_queue_tasks（dispatched → cancelled）。
func (r *PublishQueueRepository) MarkDispatchedCancelled(ids []int64, reason string) (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}

	cleaned := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id > 0 {
			cleaned = append(cleaned, id)
		}
	}
	if len(cleaned) == 0 {
		return 0, nil
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	text := strings.TrimSpace(reason)
	if text == "" {
		text = "批次已取消"
	}

	result := r.store.DB.Table("publish_queue_tasks").
		Where("id IN ? AND status = ?", cleaned, PublishQueueStatusDispatched).
		Updates(map[string]any{
			"status":      PublishQueueStatusCancelled,
			"next_run_at": nil,
			"finished_at": nowText,
			"last_result": text,
			"last_error":  "",
			"updated_at":  nowText,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}

// FailStaleDispatchedTasks 把残留的「已派发」记录标记为失败。
// 参数/返回：reason 为失败原因；返回受影响条数与 error。
// 失败场景：DB 未初始化或更新失败返回 error。
// 副作用：更新 publish_queue_tasks（dispatched → failed）。
// 说明：dispatched 记录依赖进程内的实时 runner 推进，服务重启后没有任何线程会再执行它们，
// 因此启动时统一标记为失败，避免进度页出现永远「待发布」的僵尸记录。
func (r *PublishQueueRepository) FailStaleDispatchedTasks(reason string) (int64, error) {
	if r == nil || r.store == nil || r.store.DB == nil {
		return 0, errors.New("publish queue repo is nil")
	}

	nowText := time.Now().Format(PublishQueueTimeLayout)
	text := strings.TrimSpace(reason)
	if text == "" {
		text = "服务重启，任务未执行"
	}

	result := r.store.DB.Table("publish_queue_tasks").
		Where("status = ?", PublishQueueStatusDispatched).
		Updates(map[string]any{
			"status":      PublishQueueStatusFailed,
			"next_run_at": nil,
			"finished_at": nowText,
			"last_result": text,
			"last_error":  text,
			"updated_at":  nowText,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	return result.RowsAffected, nil
}
