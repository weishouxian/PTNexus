package migrationflow

import (
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingbdflow "github.com/pt-nexus/server/internal/service/processing/bdflow"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
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

	// 蓝光截图决策钩子：BDInfo 完成产出文本后，据此判断是否有中文字幕。
	// 若判不出中文字幕，标记截图待人工确认（pending），供审查阶段人工选图；
	// 命中中文字幕则不改动，维持现状（自动截图直出）。
	s.decideScreenshotReviewAfterBDInfo(hash, torrentID, siteName, mediaInfo)

	logx.Infof(bdinfoTaskLogModule, "BDInfo后处理完成 task_id=%s seed_id=%s", taskID, seedID)
	return result, status
}

// decideScreenshotReviewAfterBDInfo 蓝光 BDInfo 完成后，据 BDInfo 文本判定中文字幕并决定截图审查状态。
// 参数/返回：hash/torrentID/siteName 定位种子，bdinfoText 为 BDInfo 报告文本。
// 行为：bdinfoText 判不出中文字幕时，把 screenshot_review_status 标记为 pending（待人工选图）；
// 命中中文字幕或已人工确认（is_reviewed=true）时不改动。
// 副作用：可能写库更新 screenshot_review_status。
func (s *MigrateService) decideScreenshotReviewAfterBDInfo(hash, torrentID, siteName, bdinfoText string) {
	if processingrepair.MediaTextHasChineseSubtitle(bdinfoText) {
		logx.Infof(bdinfoTaskLogModule, "蓝光截图决策：BDInfo 命中中文字幕，维持自动截图 seed_id=%s", processingpersist.ComposeSeedID(hash, torrentID, siteName))
		return
	}
	// 判不出中文字幕：标记待人工确认，供审查阶段人工选择截图。
	if err := s.repo.UpdateSeedParameterByKey(hash, torrentID, siteName, map[string]any{
		"screenshot_review_status": processingshared.ScreenshotReviewStatusPending,
		"updated_at":               time.Now().Format("2006-01-02 15:04:05"),
	}); err != nil {
		logx.Warnf(bdinfoTaskLogModule, "蓝光截图决策：标记待人工确认失败 seed_id=%s err=%v", processingpersist.ComposeSeedID(hash, torrentID, siteName), err)
		return
	}
	logx.Infof(bdinfoTaskLogModule, "蓝光截图决策：BDInfo 判不出中文字幕，截图标记待人工确认 seed_id=%s", processingpersist.ComposeSeedID(hash, torrentID, siteName))
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
