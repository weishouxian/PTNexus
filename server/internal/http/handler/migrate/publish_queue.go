package migrate

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/repository"
)

// PublishQueueEnqueue 接收“加入队列”请求，将当前已审核的发布内容写入发种队列。
// 参数/返回：请求体为发布 payload（需包含 task_id/upload_data/targetSites）；返回入队结果与状态码。
// 失败场景：参数缺失、上下文过期或入库失败会返回 4xx/5xx。
// 副作用：写入 publish_queue_tasks，并由后台线程异步触发发布。
func (h *Handler) PublishQueueEnqueue(c *gin.Context) {
	payload, ok := bindMapPayload(c)
	if !ok {
		return
	}
	result, status := h.service.EnqueuePublishQueue(payload)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// PublishQueueEnqueueBatch 接收”一站多种”批量入队请求，一次性写入多个队列任务。
// 参数/返回：请求体需包含 target_site_name 与 seeds；返回批量入队统计与批次标识。
// 失败场景：参数缺失、队列未初始化、入队失败会返回 4xx/5xx。
// 副作用：写入 publish_queue_tasks 与 publish_logs。
func (h *Handler) PublishQueueEnqueueBatch(c *gin.Context) {
	payload, ok := bindMapPayload(c)
	if !ok {
		return
	}
	result, status := h.service.EnqueuePublishQueueBatch(payload)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// PublishQueueEnqueueBatchByNames 接收”一种多站”批量入队请求，根据种子名称查找 seed_parameters 后加入队列。
// 参数/返回：请求体需包含 torrent_names 与 target_site_name；返回批量入队统计与批次标识。
// 失败场景：参数缺失、队列未初始化、seed_parameters 未找到会跳过。
// 副作用：写入 publish_queue_tasks 与 publish_logs。
func (h *Handler) PublishQueueEnqueueBatchByNames(c *gin.Context) {
	payload, ok := bindMapPayload(c)
	if !ok {
		return
	}
	result, status := h.service.EnqueuePublishQueueBatchByNames(payload)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// PublishQueueDeleteTask 删除（取消）一条 queued 队列任务。
// 参数/返回：queue_task_id 从 URL 参数读取；返回删除结果与状态码。
// 失败场景：任务不存在返回 404，任务非 queued 返回 409，参数非法返回 400。
// 副作用：更新 publish_queue_tasks 与 publish_logs。
func (h *Handler) PublishQueueDeleteTask(c *gin.Context) {
	queueTaskID, ok := parseQueueTaskIDParam(c)
	if !ok {
		return
	}

	result, status := h.service.DeleteQueuedPublishTask(queueTaskID)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// PublishQueuePublishNow 跳过排队等待，把一条 queued 队列任务立刻交给后台队列执行。
// 参数/返回：queue_task_id 从 URL 参数读取；返回操作结果与状态码。
// 失败场景：任务不存在返回 404，任务非 queued 返回 409，参数非法返回 400，队列未开启返回 400。
// 副作用：更新 publish_queue_tasks 计划时间与 publish_logs，并唤醒队列线程。
func (h *Handler) PublishQueuePublishNow(c *gin.Context) {
	queueTaskID, ok := parseQueueTaskIDParam(c)
	if !ok {
		return
	}

	result, status := h.service.PublishQueuedTaskNow(queueTaskID)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// PublishQueuePublishNextWave 跳过等待时间，把当前范围内计划时间最早的一波待发布任务立刻执行。
// 参数/返回：请求体可选，支持 {"downloader_ids": ["id1","id2"]} 限定下载器范围（与页面顶部全局下载器一致）；
// 返回被提前的波次信息与状态码。
// 失败场景：请求体格式错误、队列未开启、查询失败时返回 4xx/5xx；没有等待中的波次时返回 success=true 且 promoted=0。
// 副作用：更新 publish_queue_tasks 计划时间与 publish_logs，并唤醒队列线程。
func (h *Handler) PublishQueuePublishNextWave(c *gin.Context) {
	payload, ok := bindOptionalMapPayload(c)
	if !ok {
		return
	}

	query := repository.PublishQueueTaskQuery{
		DownloaderIDs: queueQueryValues(payload["downloader_ids"]),
	}

	result, status := h.service.PublishNextQueueWave(query)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// parseQueueTaskIDParam 解析 URL 路径中的 queue_task_id 参数。
// 参数/返回：c 为 gin 上下文；返回任务 ID 与是否合法。
// 失败场景：参数缺失或非法时写出 400 响应并返回 false。
// 副作用：可能写入 400 响应体。
func parseQueueTaskIDParam(c *gin.Context) (int64, bool) {
	rawID := strings.TrimSpace(c.Param("queue_task_id"))
	if rawID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 queue_task_id 参数"})
		return 0, false
	}
	queueTaskID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || queueTaskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "queue_task_id 参数非法"})
		return 0, false
	}
	return queueTaskID, true
}

// bindOptionalMapPayload 解析可选 JSON 请求体：空 body 视为空对象，避免无参数调用被拒。
// 参数/返回：c 为 gin 上下文；返回 payload 与是否合法。
// 失败场景：请求体非 JSON 对象时写出 400 响应并返回 false。
// 副作用：可能写入 400 响应体。
func bindOptionalMapPayload(c *gin.Context) (map[string]any, bool) {
	payload := map[string]any{}
	err := c.ShouldBindJSON(&payload)
	if err != nil {
		message := err.Error()
		if errors.Is(err, io.EOF) || strings.Contains(message, "EOF") || strings.Contains(message, "unexpected end of JSON input") {
			return map[string]any{}, true
		}
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return nil, false
	}
	if payload == nil {
		payload = map[string]any{}
	}
	return payload, true
}

// queueQueryValues 把筛选值归一化为一组字符串，兼容 []string / []any / 逗号分隔字符串三种形态。
// 参数/返回：value 为原始值；返回去空白后的非空切片。
// 失败场景：无。
// 副作用：无。
func queueQueryValues(value any) []string {
	switch typed := value.(type) {
	case []string:
		return splitQueryCSV(strings.Join(typed, ","))
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				parts = append(parts, text)
			}
		}
		return splitQueryCSV(strings.Join(parts, ","))
	case string:
		return splitQueryCSV(typed)
	default:
		return nil
	}
}

// PublishQueueTasks 分页查询发布队列任务（供前端「下载器发布进度」页面使用）。
// 参数/返回：querystring 支持 page/page_size/search/status/scene/trigger/target_site/source_site/torrent_id/group_id/downloader_ids；
// 其中 status 与 downloader_ids 支持逗号分隔多值；返回任务列表、分页信息与状态概览。
// 失败场景：队列服务未初始化或查询失败时返回 5xx。
// 副作用：无（只读）。
func (h *Handler) PublishQueueTasks(c *gin.Context) {
	page := parsePositiveInt(c.Query("page"), 1)
	pageSize := parsePositiveInt(c.Query("page_size"), 20)
	if pageSize > 200 {
		pageSize = 200
	}

	query := repository.PublishQueueTaskQuery{
		Page:          page,
		PageSize:      pageSize,
		Search:        strings.TrimSpace(c.Query("search")),
		Statuses:      splitQueryCSV(c.Query("status")),
		Trigger:       strings.TrimSpace(c.Query("trigger")),
		Scene:         strings.TrimSpace(c.Query("scene")),
		QueueGroupID:  strings.TrimSpace(c.Query("queue_group_id")),
		TargetSite:    strings.TrimSpace(c.Query("target_site")),
		SourceSite:    strings.TrimSpace(c.Query("source_site")),
		TorrentID:     strings.TrimSpace(c.Query("torrent_id")),
		DownloaderIDs: splitQueryCSV(c.Query("downloader_ids")),
	}

	result, status := h.service.ListPublishQueueTasks(query)
	if status == 0 {
		status = http.StatusOK
	}
	c.JSON(status, result)
}

// splitQueryCSV 解析逗号分隔的查询参数（用于多值筛选，如 status=queued,running）。
// 参数/返回：raw 为原始参数；返回去空白后的非空元素切片。
// 失败场景：无。
// 副作用：无。
func splitQueryCSV(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
