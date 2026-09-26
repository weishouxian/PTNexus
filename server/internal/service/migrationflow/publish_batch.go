package migrationflow

import (
	"runtime"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
	publishworkflow "github.com/pt-nexus/server/internal/service/publish/workflow"
	settingspkg "github.com/pt-nexus/server/internal/service/settings"
)

func (s *MigrateService) Publish(payload map[string]any) (map[string]any, int) {
	startedAt := time.Now()

	// 可发种时间检查：未到可发种时间不能发种。
	ctxTaskID := strings.TrimSpace(processingshared.ToString(payload["task_id"], processingshared.ToString(payload["taskId"], "")))
	if ctxTaskID != "" && s.contextState != nil {
		if ctx, ok := s.contextState.Get(ctxTaskID); ok {
			if publishAt, notReached := s.resolveSeedPublishAtNotReached(ctx.TorrentID, time.Now()); notReached {
				result := map[string]any{"success": false, "logs": "错误: " + publishAtBlockMessage(publishAt) + "。", "url": nil}
				s.appendPublishLog(payload, ctxTaskID, strings.TrimSpace(ctx.TorrentID), result, 400, time.Since(startedAt))
				return result, 400
			}
		}
	}

	normalizedPayload := s.normalizePublishPayloadWithCrossSeedDefaults(payload)
	defaultDownloaderID := s.resolveDefaultPublishDownloaderID()
	rootConfig := map[string]any{}
	if s.cfg != nil {
		rootConfig = s.cfg.Get()
	}

	result, status := publishworkflow.ExecutePublishFromPayload(
		publishworkflow.PublishFromPayloadInput{
			Payload:             normalizedPayload,
			TorrentPath:         "",
			DefaultDownloaderID: defaultDownloaderID,
			RootConfig:          rootConfig,
		},
		publishworkflow.PublishFromPayloadDeps{
			ContextState:                      s.contextState,
			GetSiteByName:                     s.repo.GetSiteByName,
			FindSiteNicknameByGroup:           s.repo.FindSiteNicknameByGroup,
			IsTransferForbiddenByOfficialSite: s.repo.IsTransferForbiddenByOfficialSite,
			ResolveTorrentPath: func(ctx publishworkflow.Context) string {
				return acquirefetch.ResolvePublishTorrentPath(s.repo, acquirefetch.ResolvePublishTorrentPathInput{
					OriginalTorrentPath: ctx.OriginalTorrentPath,
					TorrentDir:          ctx.TorrentDir,
					SiteName:            ctx.SiteName,
					TorrentID:           ctx.TorrentID,
					SourceNickname:      ctx.SourceNickname,
					SourceDetailURL:     ctx.SourceDetailURL,
				})
			},
			AddToDownloader: s.AddToDownloader,
			UpdateTorrentDetails: func(input publishworkflow.PublishTorrentDetailsUpdateInput) (int64, error) {
				if s == nil || s.repo == nil {
					return 0, nil
				}
				return s.repo.UpdateTorrentDetailsAfterPublish(
					input.Hashes,
					input.Name,
					input.DownloaderID,
					input.SiteNickname,
					input.DetailsURL,
				)
			},
		},
	)

	ctxTaskID = strings.TrimSpace(processingshared.ToString(normalizedPayload["task_id"], processingshared.ToString(normalizedPayload["taskId"], "")))
	ctxTorrentID := ""
	if ctxTaskID != "" && s.contextState != nil {
		if ctx, ok := s.contextState.Get(ctxTaskID); ok {
			ctxTorrentID = strings.TrimSpace(ctx.TorrentID)
		}
	}
	s.appendPublishLog(normalizedPayload, ctxTaskID, ctxTorrentID, result, status, time.Since(startedAt))
	return result, status
}

func (s *MigrateService) StartPublishBatch(payload map[string]any) (map[string]any, int) {
	targets := processingshared.ToStringSlice(payload["targetSites"])
	if len(targets) == 0 {
		targets = processingshared.ToStringSlice(payload["target_sites"])
	}
	if len(targets) == 0 {
		return map[string]any{"success": false, "message": "缺少 targetSites 参数"}, 400
	}

	// 计算批量发布并发（对齐 Python 规则：payload.concurrency 优先，否则走 cross_seed 配置）。
	rootConfig := map[string]any{}
	if s != nil && s.cfg != nil {
		rootConfig = s.cfg.Get()
	}
	crossSeed := map[string]any{}
	if rootConfig != nil {
		if item, ok := rootConfig["cross_seed"].(map[string]any); ok && item != nil {
			crossSeed = item
		}
	}

	concurrency := int(processingshared.ToFloat(payload["concurrency"]))
	if concurrency < 1 {
		mode := strings.TrimSpace(processingshared.ToString(payload["concurrency_mode"], processingshared.ToString(crossSeed["publish_batch_concurrency_mode"], "cpu")))
		manualValue := int(processingshared.ToFloat(crossSeed["publish_batch_concurrency_manual"]))
		if manualValue < 1 {
			manualValue = settingspkg.BatchPublishDefaultConcurrency
		}
		cpuThreads := runtime.NumCPU()
		if cpuThreads < 1 {
			cpuThreads = 1
		}

		switch mode {
		case "cpu":
			concurrency = cpuThreads * 2
		case "all":
			concurrency = len(targets)
		case "manual":
			concurrency = manualValue
		default:
			concurrency = settingspkg.BatchPublishDefaultConcurrency
		}
	}
	if concurrency < 1 {
		concurrency = 1
	}
	if concurrency > settingspkg.BatchPublishMaxConcurrency {
		concurrency = settingspkg.BatchPublishMaxConcurrency
	}
	if concurrency > len(targets) {
		concurrency = len(targets)
	}

	// 下载器发布节奏：该下载器「分钟间隔」>0 时接管并发，按它的并发数分波、波间等待间隔。
	// 存量下载器默认间隔为 0，此时完全不改变上面的并发策略。
	pacingDownloaderID := s.resolvePublishPacingDownloaderID(payload)
	publishInterval, pacingConcurrency := s.resolveDownloaderPublishPacing(pacingDownloaderID)
	if publishInterval > 0 {
		concurrency = pacingConcurrency
		if concurrency > len(targets) {
			concurrency = len(targets)
		}
		if concurrency < 1 {
			concurrency = 1
		}
		logx.Infof(
			publishQueueLogModule,
			"批量发布按下载器节奏执行 downloader=%s interval=%s concurrency=%d targets=%d",
			pacingDownloaderID,
			publishInterval,
			concurrency,
			len(targets),
		)
	}

	batchID := s.newID("batch")
	s.publishState.Start(batchID, len(targets), concurrency, time.Now())

	// 把本批次登记到发布队列表（status=dispatched），让「下载器发布进度」页面能看到立即发布的任务。
	// 执行仍由下面的实时 runner 负责，登记记录只用于展示与状态回写（队列调度器不会领取 dispatched）。
	progress := s.registerLivePublishProgress(livePublishProgressInput{
		BatchID:      batchID,
		Payload:      payload,
		Targets:      targets,
		Concurrency:  concurrency,
		Interval:     publishInterval,
		DownloaderID: pacingDownloaderID,
	})

	go s.runPublishBatch(batchID, payload, targets, concurrency, publishInterval, progress)
	return map[string]any{
		"success":                  true,
		"batch_id":                 batchID,
		"concurrency":              concurrency,
		"publish_interval_minutes": int(publishInterval / time.Minute),
		"message":                  "批量发布任务已启动",
	}, 200
}

func (s *MigrateService) runPublishBatch(batchID string, payload map[string]any, targets []string, concurrency int, interval time.Duration, progress *livePublishProgressTracker) {
	publishworkflow.RunManagedBatchPublishFromPayload(
		publishworkflow.ManagedBatchFromPayloadInput{
			BatchID:     batchID,
			Targets:     targets,
			Concurrency: concurrency,
			Interval:    interval,
			Payload:     payload,
		},
		publishworkflow.ManagedBatchFromPayloadDeps{
			State:          s.publishState,
			PublishPayload: s.Publish,
			// 站点开始/结束时回写进度记录，使进度页的状态与实际发布时间保持实时。
			OnSiteProgress: progress.progress,
			// 批次被取消时，把还没轮到的记录落为「已取消」，避免卡在「待发布」。
			OnBatchStopped: progress.markBatchStopped,
		},
	)
}

func (s *MigrateService) PublishBatchStatus(batchID string) (map[string]any, int) {
	task, ok := s.publishState.Status(batchID)
	if !ok {
		return map[string]any{"success": false, "message": "任务不存在"}, 404
	}
	return map[string]any{"success": true, "task": task}, 200
}

func (s *MigrateService) PublishBatchCancel(batchID string) (map[string]any, int) {
	if !s.publishState.Cancel(batchID) {
		return map[string]any{"success": false, "message": "任务不存在"}, 404
	}
	return map[string]any{"success": true, "message": "取消信号已发送"}, 200
}

func (s *MigrateService) SubscribePublishEvents(batchID string) (chan map[string]any, bool) {
	return s.publishState.Subscribe(batchID)
}

func (s *MigrateService) UnsubscribePublishEvents(batchID string, ch chan map[string]any) {
	s.publishState.Unsubscribe(batchID, ch)
}
