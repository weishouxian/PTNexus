package workflow

import (
	"sync"
	"time"
)

// batchPacingCancelCheckStep 带节奏等待时的取消轮询间隔。
const batchPacingCancelCheckStep = 200 * time.Millisecond

// RunBatchPublishConcurrentPaced 按「波次」执行批量发布：每波同时发布 concurrency 个站点，
// 上一波全部结束后再等待 interval 才开始下一波。
// 参数/返回：targets 为目标站点列表；concurrency 为每波站点数；interval 为波间隔；deps 为回调依赖；无返回值。
// 失败场景：回调内部错误由调用方处理，函数本身不中断进程。
// 副作用：会并发调用 PublishToSite，波与波之间会阻塞等待 interval（可被 IsCancelled 打断）。
func RunBatchPublishConcurrentPaced(targets []string, concurrency int, interval time.Duration, deps BatchRunnerDeps) {
	if len(targets) == 0 {
		return
	}
	if interval <= 0 {
		RunBatchPublishConcurrent(targets, concurrency, deps)
		return
	}

	waveSize := concurrency
	if waveSize > len(targets) {
		waveSize = len(targets)
	}
	if waveSize < 1 {
		waveSize = 1
	}

	var stopOnce sync.Once
	emitStopped := func() {
		stopOnce.Do(func() {
			if deps.OnBatchStopped != nil {
				deps.OnBatchStopped()
			}
		})
	}
	isCancelled := func() bool {
		return deps.IsCancelled != nil && deps.IsCancelled()
	}

	for start := 0; start < len(targets); start += waveSize {
		end := start + waveSize
		if end > len(targets) {
			end = len(targets)
		}

		wave := sync.WaitGroup{}
		for _, siteName := range targets[start:end] {
			wave.Add(1)
			go func(site string) {
				defer wave.Done()
				if isCancelled() {
					emitStopped()
					return
				}
				runBatchSitePublish(site, deps)
			}(siteName)
		}
		wave.Wait()

		if end >= len(targets) {
			break
		}
		if isCancelled() {
			emitStopped()
			return
		}
		if !sleepUntilCancelled(interval, isCancelled) {
			emitStopped()
			return
		}
	}
}

// sleepUntilCancelled 分段等待指定时长，期间被取消时立即返回 false。
// 参数/返回：d 为总等待时长；isCancelled 为取消判定；返回 true 表示等待完整结束。
// 失败场景：无。
// 副作用：会阻塞当前 goroutine，最多每 batchPacingCancelCheckStep 检查一次取消状态。
func sleepUntilCancelled(d time.Duration, isCancelled func() bool) bool {
	deadline := time.Now().Add(d)
	for {
		if isCancelled != nil && isCancelled() {
			return false
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return true
		}
		if remaining > batchPacingCancelCheckStep {
			remaining = batchPacingCancelCheckStep
		}
		time.Sleep(remaining)
	}
}
