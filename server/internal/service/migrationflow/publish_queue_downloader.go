package migrationflow

import (
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// resolveDownloaderQueueConfig 按下载器覆盖发布队列的调度参数。
// 参数/返回：base 为全局队列配置；downloaderID 为任务所属下载器；返回该下载器队列实际生效的配置。
// 覆盖规则：publish_concurrency 决定该下载器队列每轮最多领取几条（默认沿用全局 MaxWorkers）；
// 触发条件阈值仍是全局配置，但判断用的是该下载器自身的统计数据，各下载器互不影响。
// 失败场景：服务或配置不可用、未匹配到下载器时原样返回 base。
// 副作用：只读配置。
func (s *MigrateService) resolveDownloaderQueueConfig(base publishQueueConfig, downloaderID string) publishQueueConfig {
	out := base
	id := strings.TrimSpace(downloaderID)
	if id == "" || s == nil || s.cfg == nil {
		return out
	}
	root := s.cfg.Get()
	if root == nil {
		return out
	}

	for _, item := range downloaderConfigList(root["downloaders"]) {
		if strings.TrimSpace(processingshared.ToString(item["id"], "")) != id {
			continue
		}
		if value, exists := item["publish_concurrency"]; exists {
			if parsed := int(processingshared.ToFloat(value)); parsed > 0 {
				out.MaxWorkers = clampInt(parsed, 1, publishPacingMaxConcurrency)
			}
		}
		break
	}
	return out
}

// queueWaveBaseTime 计算本次入队在该下载器队列时间轴上的第一波起点。
// 参数/返回：downloaderID 为下载器 ID；now 为当前时间；interval 为该下载器的发种间隔（<=0 表示未启用节奏）。
// 返回本批次第一波的基准时间：队列里已有更晚的计划时间时顺延一个间隔，否则返回 now。
// 失败场景：查询队列尾部失败时记录日志并退回 now（不影响入队）。
// 副作用：读取 publish_queue_tasks。
func (s *MigrateService) queueWaveBaseTime(downloaderID string, now time.Time, interval time.Duration) time.Time {
	if interval <= 0 || s == nil || s.queueRepo == nil {
		return now
	}
	latest, err := s.queueRepo.LatestQueuedScheduledAt(downloaderID)
	if err != nil {
		logx.Warnf(publishQueueLogModule, "读取下载器队列尾部时间失败 downloader_id=%s err=%v", strings.TrimSpace(downloaderID), err)
		return now
	}
	if latest == nil || !latest.After(now) {
		return now
	}
	return latest.Add(interval)
}
