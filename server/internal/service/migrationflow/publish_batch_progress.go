package migrationflow

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	publishworkflow "github.com/pt-nexus/server/internal/service/publish/workflow"
)

// livePublishProgressInput 定义「立即发布」批次登记进度所需的输入。
type livePublishProgressInput struct {
	BatchID      string
	Payload      map[string]any
	Targets      []string
	Concurrency  int
	Interval     time.Duration
	DownloaderID string
}

// livePublishProgressTracker 持有「立即发布」批次里每个目标站对应的进度记录 ID，
// 供实时 runner 在站点开始/结束时回写状态与时间。
type livePublishProgressTracker struct {
	service *MigrateService
	ids     map[string]int64
}

// progress 是 runner 的站点级进度回调（phase 为 started/finished），实现真正的状态回写。
// 参数/返回：siteName 为站点名；phase 为阶段；result 为发布结果（started 阶段为 nil）；无返回值。
// 失败场景：记录未登记或更新失败时只记录日志，不影响发布流程。
// 副作用：更新 publish_queue_tasks 中对应记录的状态与时间字段。
func (t *livePublishProgressTracker) progress(siteName string, phase string, result map[string]any) {
	if t == nil || t.service == nil || t.service.queueRepo == nil {
		return
	}

	id, ok := t.ids[strings.TrimSpace(siteName)]
	if !ok || id <= 0 {
		return
	}

	switch phase {
	case "started":
		if err := t.service.queueRepo.MarkDispatchedRunning(id, time.Now()); err != nil {
			logx.Warnf(publishQueueLogModule, "回写立即发布进度失败(开始) queue_task_id=%d site=%s err=%v", id, siteName, err)
		}
	case "finished":
		success := processingshared.ToBool(result["success"])
		detail := strings.TrimSpace(processingshared.ToString(result["logs"], ""))
		if detail == "" {
			detail = strings.TrimSpace(processingshared.ToString(result["message"], ""))
		}
		detail = truncateProgressText(detail, 500)

		errText := ""
		if !success {
			errText = detail
		}
		if err := t.service.queueRepo.MarkDispatchedFinished(id, success, detail, errText); err != nil {
			logx.Warnf(publishQueueLogModule, "回写立即发布进度失败(结束) queue_task_id=%d site=%s err=%v", id, siteName, err)
		}
	}
}

// markBatchStopped 在批次被取消、提前结束时，把仍未执行的进度记录标记为已取消。
// 参数/返回：无参数无返回。
// 失败场景：记录未登记或更新失败时只记录日志。
// 副作用：更新 publish_queue_tasks（dispatched → cancelled）。
func (t *livePublishProgressTracker) markBatchStopped() {
	if t == nil || t.service == nil || t.service.queueRepo == nil {
		return
	}

	ids := make([]int64, 0, len(t.ids))
	for _, id := range t.ids {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return
	}

	count, err := t.service.queueRepo.MarkDispatchedCancelled(ids, "批量发布已取消")
	if err != nil {
		logx.Warnf(publishQueueLogModule, "回写立即发布进度失败(取消) err=%v", err)
		return
	}
	if count > 0 {
		logx.Infof(publishQueueLogModule, "已将 %d 条未执行的立即发布记录标记为已取消", count)
	}
}

// registerLivePublishProgress 为「立即发布」批次登记队列进度记录（status=dispatched）。
// 参数/返回：input 为批次信息；返回进度回写器（即使登记失败也返回可用对象，只是不含记录 ID）。
// 失败场景：队列仓储未初始化或写入失败时记录日志并返回空回写器，不影响发布流程。
// 副作用：写入 publish_queue_tasks（这些记录不会被队列调度器领取，只做进度展示）。
func (s *MigrateService) registerLivePublishProgress(input livePublishProgressInput) *livePublishProgressTracker {
	tracker := &livePublishProgressTracker{service: s, ids: map[string]int64{}}
	if s == nil || s.queueRepo == nil || s.queueRepo.DB() == nil {
		return tracker
	}
	if len(input.Targets) == 0 {
		return tracker
	}

	payload := input.Payload
	if payload == nil {
		payload = map[string]any{}
	}

	taskID := strings.TrimSpace(processingshared.ToString(payload["task_id"], processingshared.ToString(payload["taskId"], "")))
	uploadData := map[string]any{}
	if item, ok := payload["upload_data"].(map[string]any); ok && item != nil {
		uploadData = item
	}

	ctx := publishworkflow.Context{}
	if taskID != "" && s.contextState != nil {
		if item, ok := s.contextState.Get(taskID); ok {
			ctx = item
		}
	}

	sourceSite := strings.TrimSpace(processingshared.ToString(payload["sourceSite"], processingshared.ToString(payload["source_site"], "")))
	if sourceSite == "" {
		sourceSite = strings.TrimSpace(ctx.SourceNickname)
	}
	if sourceSite == "" {
		sourceSite = strings.TrimSpace(ctx.SiteName)
	}

	torrentID := strings.TrimSpace(ctx.TorrentID)
	if torrentID == "" {
		torrentID = strings.TrimSpace(processingshared.ToString(uploadData["torrent_id"], processingshared.ToString(payload["torrent_id"], "")))
	}

	title, subtitle := resolvePublishLogTitleFromUploadData(uploadData, firstNonEmptyString(strings.TrimSpace(ctx.Name), torrentID))
	if title == "" {
		title = strings.TrimSpace(firstNonEmptyString(ctx.Name, torrentID))
	}

	downloaderID := strings.TrimSpace(input.DownloaderID)
	if downloaderID == "" {
		downloaderID = strings.TrimSpace(ctx.DownloaderID)
	}

	scene := strings.TrimSpace(processingshared.ToString(payload["publish_scene"], ""))
	concurrency := input.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}

	now := time.Now()
	nowText := now.Format(repository.PublishQueueTimeLayout)
	uploadBytes, _ := json.Marshal(uploadData)
	ctxBytes, _ := json.Marshal(ctx)

	records := make([]repository.PublishQueueTask, 0, len(input.Targets))
	sequence := 0
	for _, target := range input.Targets {
		siteName := strings.TrimSpace(target)
		if siteName == "" {
			continue
		}

		record := repository.PublishQueueTask{
			GroupID:      input.BatchID,
			Status:       repository.PublishQueueStatusDispatched,
			TaskID:       taskID,
			Trigger:      "batch_live",
			Scene:        scene,
			TorrentID:    torrentID,
			SourceSite:   sourceSite,
			TargetSite:   siteName,
			DownloaderID: downloaderID,
			Title:        title,
			Subtitle:     subtitle,
			// 立即发布的任务不参与队列调度，无需保存可重放的 payload。
			PayloadJSON:    "{}",
			UploadDataJSON: string(uploadBytes),
			ContextJSON:    string(ctxBytes),
			CreatedAt:      nowText,
			UpdatedAt:      nowText,
		}

		// 与实时 runner 的波次保持一致：第 1 波立即执行，之后每波延后一个间隔。
		plannedText := nowText
		if input.Interval > 0 {
			if wave := sequence / concurrency; wave > 0 {
				plannedText = now.Add(time.Duration(wave) * input.Interval).Format(repository.PublishQueueTimeLayout)
			}
		}
		planned := plannedText
		record.ScheduledAt = &planned
		sequence++

		records = append(records, record)
	}

	if len(records) == 0 {
		return tracker
	}

	created, err := s.queueRepo.InsertDispatchedTasks(records)
	if err != nil {
		logx.Warnf(publishQueueLogModule, "登记立即发布进度失败 batch_id=%s err=%v", input.BatchID, err)
		return tracker
	}

	for _, item := range created {
		if item.ID > 0 {
			tracker.ids[strings.TrimSpace(item.TargetSite)] = item.ID
		}
	}
	logx.Infof(
		publishQueueLogModule,
		"已登记立即发布进度 batch_id=%s sites=%d concurrency=%d interval=%s",
		input.BatchID,
		len(created),
		concurrency,
		input.Interval,
	)
	return tracker
}

// truncateProgressText 截断过长的结果文本，避免写入超长日志字段。
// 参数/返回：text 为原始文本；limit 为字符上限；返回截断后的文本。
// 失败场景：无。
// 副作用：无。
func truncateProgressText(text string, limit int) string {
	trimmed := strings.TrimSpace(text)
	if limit <= 0 {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) <= limit {
		return trimmed
	}
	return string(runes[:limit]) + "…"
}
