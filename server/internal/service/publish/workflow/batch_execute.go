package workflow

import (
	"time"

	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
)

// ManagedBatchInput 定义带状态管理的批量发布执行输入。
// Interval 为下载器发布节奏的波间隔，>0 时按波次执行（每波 Concurrency 个站点，波间等待 Interval）。
type ManagedBatchInput struct {
	BatchID     string
	Targets     []string
	Concurrency int
	Interval    time.Duration
}

// ManagedBatchDeps 定义带状态管理的批量发布执行依赖。
type ManagedBatchDeps struct {
	State         *BatchState
	PublishToSite func(siteName string) (map[string]any, int)
	// OnSiteProgress 为可选的站点级进度钩子，phase 取 "started"/"finished"，
	// 在状态更新与事件广播之后调用，用于把进度登记到外部存储（如发布队列表）。
	OnSiteProgress func(siteName string, phase string, result map[string]any)
	// OnBatchStopped 为可选的批次中断钩子，在检测到取消信号、提前结束批次时调用。
	OnBatchStopped func()
}

// RunManagedBatchPublish 执行“发布循环 + 状态更新 + 事件广播”的完整批量流程。
// 参数/返回：input 提供批次标识与目标站点，deps 注入状态存储与发布回调；无返回值。
// 失败场景：状态容器为空时直接返回；单站发布错误由回调结果记录，不中断整个批次。
// 副作用：更新 BatchState、广播 SSE 事件，并在结束时关闭订阅通道。
func RunManagedBatchPublish(input ManagedBatchInput, deps ManagedBatchDeps) {
	if deps.State == nil {
		return
	}

	runnerDeps := BatchRunnerDeps{
		IsCancelled: func() bool {
			return deps.State.IsCancelled(input.BatchID)
		},
		PublishToSite: deps.PublishToSite,
		OnSiteStarted: func(siteName string) {
			deps.State.Emit(input.BatchID, map[string]any{"type": "site_started", "siteName": siteName})
			if deps.OnSiteProgress != nil {
				deps.OnSiteProgress(siteName, "started", nil)
			}
		},
		OnSiteFinished: func(siteName string, result map[string]any) {
			deps.State.MarkSiteResult(input.BatchID, siteName, result, processingpersist.BoolFromAny(result["success"]))
			deps.State.Emit(input.BatchID, map[string]any{"type": "site_finished", "siteName": siteName, "result": result})
			if deps.OnSiteProgress != nil {
				deps.OnSiteProgress(siteName, "finished", result)
			}
		},
		OnBatchStopped: func() {
			deps.State.Emit(input.BatchID, map[string]any{
				"type":    "batch_stopped",
				"reason":  "cancelled",
				"message": "批量发布任务已取消",
			})
			if deps.OnBatchStopped != nil {
				deps.OnBatchStopped()
			}
		},
	}

	switch {
	case input.Interval > 0:
		// 下载器配置了发布节奏：按波次发布，波间等待间隔。
		RunBatchPublishConcurrentPaced(input.Targets, input.Concurrency, input.Interval, runnerDeps)
	case input.Concurrency > 1:
		RunBatchPublishConcurrent(input.Targets, input.Concurrency, runnerDeps)
	default:
		RunBatchPublish(input.Targets, runnerDeps)
	}

	deps.State.Finish(input.BatchID, time.Now())
	deps.State.Emit(input.BatchID, map[string]any{"type": "batch_finished"})
	time.Sleep(80 * time.Millisecond)
	deps.State.CloseSubscribers(input.BatchID)
}
