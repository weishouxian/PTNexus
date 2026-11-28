package siteprofile

import (
	"context"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service/settings"
)

const (
	// 调度器检查间隔：按分钟判断是否到点，30 秒轮询足够（不会漏掉某一分钟）。
	siteDataScheduleInterval = 30 * time.Second
	// 单次全量刷新上限：站点多时按并发抓取，预留 10 分钟兜底。
	siteDataScheduleTimeout = 10 * time.Minute
)

// Scheduler 负责「站点数据」页的定时刷新。
// 设计：SettingsService 里保存开关与若干 HH:mm 时刻，这里每分钟比对一次；
// 同一个「日期+时刻」只触发一次（lastRunKey），进程重启后最多再补跑一次，避免重复抓取。
type Scheduler struct {
	service  *Service
	settings *settings.SettingsService

	stopCh    chan struct{}
	doneCh    chan struct{}
	startOnce sync.Once
	stopOnce  sync.Once

	mu         sync.Mutex
	running    bool
	lastRunKey string
}

// NewScheduler 创建站点数据定时刷新调度器。
func NewScheduler(service *Service, settingsSvc *settings.SettingsService) *Scheduler {
	return &Scheduler{
		service:  service,
		settings: settingsSvc,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
}

// Start 启动后台调度（幂等）。
// 说明：站点数据页的定时刷新默认关闭，只有用户在页面上开启并保存时间后才执行。
func (s *Scheduler) Start() {
	if s == nil || s.service == nil || s.settings == nil {
		return
	}
	s.startOnce.Do(func() {
		go s.run()
		logx.Infof(logModule, "定时刷新调度器已启动 检查间隔=%s", siteDataScheduleInterval)
	})
}

// Stop 停止后台调度。
func (s *Scheduler) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-time.After(5 * time.Second):
	}
}

func (s *Scheduler) run() {
	defer close(s.doneCh)
	ticker := time.NewTicker(siteDataScheduleInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case now := <-ticker.C:
			s.tick(now)
		}
	}
}

// tick 判断当前时刻是否需要触发刷新。
// 说明：容器内 /etc/localtime 是 UTC，必须显式换算成 Asia/Shanghai，
// 否则用户设置的 06:00 会在北京时间 14:00 才执行。
func (s *Scheduler) tick(now time.Time) {
	cfg := s.settings.GetSiteDataRefresh()
	if !cfg.Enabled {
		return
	}
	local := now.In(siteDataLocation())
	current := local.Format("15:04")
	if !containsRefreshTime(cfg.Times, current) {
		return
	}
	// 一天可以有多个刷新点，所以去重键必须带时刻，否则第二个点会被「今天已跑过」挡掉。
	runKey := local.Format("2006-01-02") + " " + current

	s.mu.Lock()
	if s.running || s.lastRunKey == runKey {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.lastRunKey = runKey
	s.mu.Unlock()

	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		start := time.Now()
		logx.Infof(logModule, "定时刷新开始 at=%s", current)
		ctx, cancel := context.WithTimeout(context.Background(), siteDataScheduleTimeout)
		defer cancel()
		profiles, err := s.service.Refresh(ctx, nil)
		if err != nil {
			logx.Warnf(logModule, "定时刷新失败 err=%v cost=%s", err, time.Since(start).Round(time.Second))
			return
		}
		failed := 0
		for _, profile := range profiles {
			if profile.Status == "fail" {
				failed++
			}
		}
		logx.Infof(logModule, "定时刷新完成 count=%d failed=%d cost=%s", len(profiles), failed, time.Since(start).Round(time.Second))
	}()
}

// containsRefreshTime 判断当前 HH:mm 是否命中配置里的任一刷新点。
// 配置读取时已做过规范化（去重、升序、HH:mm），这里只做等值比较。
func containsRefreshTime(times []string, current string) bool {
	for _, item := range times {
		if item == current {
			return true
		}
	}
	return false
}

// siteDataLocation 返回站点数据调度使用的时区：优先 Asia/Shanghai，缺 tzdata 时退回固定 UTC+8。
func siteDataLocation() *time.Location {
	if loc, err := time.LoadLocation("Asia/Shanghai"); err == nil && loc != nil {
		return loc
	}
	return time.FixedZone("CST", 8*3600)
}
