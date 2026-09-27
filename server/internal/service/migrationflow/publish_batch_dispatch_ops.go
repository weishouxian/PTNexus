package migrationflow

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// CancelDispatchedTask 取消一条 dispatched 队列任务（进度页「立即发布」批次的单站取消）。
// 参数/返回：queueTaskID 为队列任务主键；返回标准响应与状态码。
// 失败场景：任务不存在返回 404；非 dispatched 返回 409；批次上下文存在时联动 runner 跳过该站点。
// 副作用：更新 publish_queue_tasks（dispatched → cancelled）与 publish_logs；标记 BatchState 站点已处理。
func (s *MigrateService) CancelDispatchedTask(queueTaskID int64) (map[string]any, int) {
	if s == nil || s.queueRepo == nil || s.queueRepo.DB() == nil {
		return map[string]any{"success": false, "message": "队列服务未初始化"}, 500
	}
	if queueTaskID <= 0 {
		return map[string]any{"success": false, "message": "缺少有效的 queue_task_id"}, 400
	}

	task, ok, err := s.queueRepo.FindTaskByID(queueTaskID)
	if err != nil {
		return map[string]any{"success": false, "message": "查询队列任务失败: " + err.Error()}, 500
	}
	if !ok || task == nil {
		return map[string]any{"success": false, "message": "队列任务不存在"}, 404
	}
	if strings.TrimSpace(task.Status) != repository.PublishQueueStatusDispatched {
		return map[string]any{"success": false, "message": "仅立即发布登记的任务支持在此取消"}, 409
	}

	batchID := strings.TrimSpace(task.GroupID)
	siteName := strings.TrimSpace(task.TargetSite)
	// 联动 runner：标记站点已处理，runner 轮到该站时直接跳过（批次已结束则无需标记）。
	s.liveBatchesMu.Lock()
	_, batchAlive := s.liveBatches[batchID]
	s.liveBatchesMu.Unlock()
	if batchAlive {
		s.publishState.MarkSiteHandled(batchID, siteName)
	}

	reason := "已从发布进度页取消"
	if count, err := s.queueRepo.MarkDispatchedCancelled([]int64{queueTaskID}, reason); err != nil {
		return map[string]any{"success": false, "message": "取消队列任务失败: " + err.Error()}, 500
	} else if count == 0 {
		// 并发窗口内状态已被 runner 推进（running/success 等），按非 dispatched 处理。
		return map[string]any{"success": false, "message": "任务已开始发布，无法取消"}, 409
	}

	if s.publishLogRepo != nil {
		if updateErr := s.publishLogRepo.UpdateStatusAndLogsByQueueTaskID(queueTaskID, repository.PublishQueueStatusCancelled, reason); updateErr != nil {
			logx.Warnf(publishLogModule, "更新已取消日志失败 queue_task_id=%d err=%v", queueTaskID, updateErr)
		}
	}

	logx.Infof(publishQueueLogModule, "立即发布任务已单站取消 queue_task_id=%d batch_id=%s site=%s batch_alive=%t", queueTaskID, batchID, siteName, batchAlive)
	return map[string]any{"success": true, "message": "已取消该站点任务（runner 将跳过此站点）"}, 200
}

// PublishDispatchedTaskNow 立即执行一条 dispatched 队列任务（进度页单站立即发布）。
// 参数/返回：queueTaskID 为队列任务主键；返回标准响应与状态码。
// 失败场景：任务不存在 404；非 dispatched 409；批次已结束/上下文丢失 409（服务重启后的残留记录由启动清理兜底）。
// 副作用：标记 runner 跳过该站点；站外 goroutine 复用批次发布链路立即发布，进度经 tracker 回写。
func (s *MigrateService) PublishDispatchedTaskNow(queueTaskID int64) (map[string]any, int) {
	if s == nil || s.queueRepo == nil || s.queueRepo.DB() == nil {
		return map[string]any{"success": false, "message": "队列服务未初始化"}, 500
	}
	if queueTaskID <= 0 {
		return map[string]any{"success": false, "message": "缺少有效的 queue_task_id"}, 400
	}

	task, ok, err := s.queueRepo.FindTaskByID(queueTaskID)
	if err != nil {
		return map[string]any{"success": false, "message": "查询队列任务失败: " + err.Error()}, 500
	}
	if !ok || task == nil {
		return map[string]any{"success": false, "message": "队列任务不存在"}, 404
	}
	if strings.TrimSpace(task.Status) != repository.PublishQueueStatusDispatched {
		return map[string]any{"success": false, "message": "仅立即发布登记的任务支持在此立即发布"}, 409
	}

	batchID := strings.TrimSpace(task.GroupID)
	siteName := strings.TrimSpace(task.TargetSite)

	s.liveBatchesMu.Lock()
	ctx, batchAlive := s.liveBatches[batchID]
	s.liveBatchesMu.Unlock()
	if !batchAlive || ctx == nil || ctx.tracker == nil {
		return map[string]any{"success": false, "message": "批次已结束或服务已重启，无法立即发布（请重新发起发布）"}, 409
	}

	// 先标记站点已处理，runner 轮到该站时跳过，避免与站外执行重复发布。
	// 若恰好在 runner 发布该站的过程中（running），上面的状态校验已拦截，不会走到这里。
	s.publishState.MarkSiteHandled(batchID, siteName)

	// 标记后复查状态：若 runner 恰好在标记前后领取了该站（status 已离开 dispatched），
	// 放弃站外执行，避免同一站点双重发布。
	if recheck, reok, reErr := s.queueRepo.FindTaskByID(queueTaskID); reErr == nil && reok && recheck != nil {
		if strings.TrimSpace(recheck.Status) != repository.PublishQueueStatusDispatched {
			return map[string]any{"success": false, "message": "任务已开始发布，无法重复立即发布"}, 409
		}
	}

	targetPayload := map[string]any{}
	for key, value := range ctx.payload {
		targetPayload[key] = value
	}
	targetPayload["targetSite"] = siteName

	go func() {
		logx.Infof(publishQueueLogModule, "立即发布任务站外执行开始 queue_task_id=%d batch_id=%s site=%s", queueTaskID, batchID, siteName)
		// 同步发布面板 SSE 视图（runner 会跳过该站，不再产生事件）。
		s.publishState.Emit(batchID, map[string]any{"type": "site_started", "siteName": siteName})
		ctx.tracker.progress(siteName, "started", nil)

		result, status := s.Publish(targetPayload)
		if result == nil {
			result = map[string]any{"success": false, "logs": "发布返回为空"}
		}
		success := processingshared.ToBool(result["success"])
		if status != 200 && success {
			result["success"] = false
			success = false
		}

		// finished 回写会经 MarkDispatchedFinished 落库并同步 publish_logs。
		ctx.tracker.progress(siteName, "finished", result)
		s.publishState.MarkSiteResult(batchID, siteName, result, success)
		s.publishState.Emit(batchID, map[string]any{"type": "site_finished", "siteName": siteName, "result": result})
		logx.Infof(publishQueueLogModule, "立即发布任务站外执行结束 queue_task_id=%d batch_id=%s site=%s success=%t", queueTaskID, batchID, siteName, success)
	}()

	return map[string]any{
		"success":       true,
		"message":       "已提交立即发布（跳过剩余等待，马上执行该站点）",
		"queue_task_id": queueTaskID,
	}, 200
}
