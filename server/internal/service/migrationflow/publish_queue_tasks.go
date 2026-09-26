package migrationflow

import (
	"strings"

	"github.com/pt-nexus/server/internal/repository"
)

// ListPublishQueueTasks 分页查询发布队列任务（供前端「下载器发布进度」页面使用）。
// 参数/返回：query 为查询条件；返回 {success,data,total,page,page_size,page_total,status_counts} 与 HTTP 状态码。
// 失败场景：队列仓储未初始化返回 500；查询失败返回 500。
// 副作用：无（只读）。
func (s *MigrateService) ListPublishQueueTasks(query repository.PublishQueueTaskQuery) (map[string]any, int) {
	if s == nil || s.queueRepo == nil {
		return map[string]any{"success": false, "message": "发布队列未初始化"}, 500
	}

	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 200 {
		pageSize = 200
	}
	query.Page = page
	query.PageSize = pageSize

	rows, total, err := s.queueRepo.ListTasks(query)
	if err != nil {
		return map[string]any{"success": false, "message": "查询发布队列失败: " + err.Error()}, 500
	}

	// 回填展示用派生字段：任务真正可执行的时间（计划时间与下次可运行时间中较晚者）。
	for i := range rows {
		rows[i].EffectiveScheduledAt = resolveQueueEffectiveScheduledAt(rows[i])
	}

	counts, err := s.queueRepo.CountTaskStatuses(query)
	if err != nil {
		// 状态概览失败不影响列表展示，仅返回零值统计。
		counts = repository.PublishQueueTaskStatusCounts{}
	}

	return map[string]any{
		"success":       true,
		"data":          rows,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"pageSize":      pageSize,
		"page_total":    (total + int64(pageSize) - 1) / int64(pageSize),
		"status_counts": counts,
	}, 200
}

// resolveQueueEffectiveScheduledAt 计算队列任务真正可执行的时间。
// 参数/返回：task 为队列任务；返回 "YYYY-MM-DD HH:MM:SS" 文本，全部缺失时返回空串。
// 失败场景：无。
// 副作用：无。
// 说明：scheduled_at 与 next_run_at 必须同时满足，故取两者中较晚者；都为空则退回创建时间（立即执行）。
// 比较依赖 repository.PublishQueueTimeLayout 的固定格式，该格式下的字典序即时间序。
func resolveQueueEffectiveScheduledAt(task repository.PublishQueueTask) string {
	latest := ""
	appendCandidate := func(raw *string) {
		if raw == nil {
			return
		}
		text := strings.TrimSpace(*raw)
		if text == "" {
			return
		}
		if latest == "" || text > latest {
			latest = text
		}
	}

	appendCandidate(task.ScheduledAt)
	appendCandidate(task.NextRunAt)
	if latest == "" {
		if created := strings.TrimSpace(task.CreatedAt); created != "" {
			latest = created
		}
	}
	return latest
}
