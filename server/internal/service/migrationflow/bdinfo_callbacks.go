package migrationflow

import (
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingbdflow "github.com/pt-nexus/server/internal/service/processing/bdflow"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

func (s *MigrateService) BDInfoProgressCallback(payload map[string]any) (map[string]any, int) {
	return processingbdflow.HandleBDInfoProgressCallback(s.bdinfoState, payload, time.Now())
}

func (s *MigrateService) BDInfoCompleteCallback(payload map[string]any) (map[string]any, int) {
	result, status := processingbdflow.HandleBDInfoCompleteCallback(s.bdinfoState, s.repo, payload, time.Now())

	taskID := strings.TrimSpace(processingshared.ToString(payload["task_id"], ""))
	success := processingshared.ToBool(payload["success"])
	mediaInfo := strings.TrimSpace(processingshared.ToString(payload["bdinfo"], ""))
	if taskID == "" {
		return result, status
	}
	task, ok := s.bdinfoState.TaskStatus(taskID)
	if !ok {
		return result, status
	}
	seedID := strings.TrimSpace(task.SeedID)
	if seedID == "" {
		return result, status
	}

	if !success || mediaInfo == "" {
		logx.Warnf(bdinfoTaskLogModule, "BDInfo完成回调：未产出有效内容 task_id=%s seed_id=%s success=%t bytes=%d", taskID, seedID, success, len(mediaInfo))
		return result, status
	}

	hash, torrentID, siteName, parseErr := processingpersist.ParseSeedID(seedID)
	if parseErr != nil {
		return result, status
	}
	row, rowErr := s.repo.GetSeedParameterByKey(hash, torrentID, siteName)
	if rowErr != nil || len(row) == 0 {
		logx.Warnf(bdinfoTaskLogModule, "BDInfo后处理跳过：读取行失败 task_id=%s seed_id=%s err=%v", taskID, seedID, rowErr)
		return result, status
	}
	if processingpersist.BoolFromAny(row["is_reviewed"]) {
		logx.Infof(bdinfoTaskLogModule, "BDInfo后处理跳过：is_reviewed=true task_id=%s seed_id=%s", taskID, seedID)
		return result, status
	}

	// 走到这里说明盒子代理的 BDInfo 任务已成功完成并产出了内容：BDInfo 只能对碟结构运行，
	// 因此本体可认定为原盘（discStructure=true）。回写函数内部还会要求文本确实是 BDInfo 报告
	// （ValidateMediaInfoFormat 判定 IsBDInfo），所以代理侧即使回退成 MediaInfo 文本也不会触发纠偏。
	processingpersist.RewriteSeedTitleComponentsByMediaInfo(
		bdinfoTaskLogModule,
		s.repo,
		hash,
		torrentID,
		siteName,
		time.Now(),
		row,
		mediaInfo,
		true,
	)
	s.recomputeAndPersistTags(hash, torrentID, siteName, strings.TrimSpace(processingshared.ToString(row["save_path"], "")), strings.TrimSpace(processingshared.ToString(row["name"], "")), "BDInfo完成")
	logx.Infof(bdinfoTaskLogModule, "BDInfo后处理完成 task_id=%s seed_id=%s", taskID, seedID)
	return result, status
}

func (s *MigrateService) BDInfoTasks() (map[string]any, int) {
	tasks, stats := s.bdinfoState.Snapshot()
	return map[string]any{"tasks": tasks, "stats": stats}, 200
}

func (s *MigrateService) CleanupBDInfo(seedID string) (map[string]any, int) {
	if s.bdinfoState.CleanupBySeedID(seedID, time.Now()) {
		return map[string]any{"success": true, "message": "已清理残留进程"}, 200
	}
	return map[string]any{"success": true, "message": "未找到需要清理的进程"}, 200
}
