package trafficdaily

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

const (
	dailySettleModule = "流量日结算"
	// settleCheckInterval 为补算检查粒度：每小时检查一次，覆盖漏结算的日期。
	settleCheckInterval = time.Hour
)

// Service 按自然日结算下载器流量增量，并在启动时补齐历史缺口。
// 结算结果写入 traffic_stats_daily，供「本月/今日」口径直接累加，不再回扫全部采样。
type Service struct {
	repo *repository.StatsRepository

	started atomic.Bool
	stopCh  chan struct{}
	doneCh  chan struct{}
	once    sync.Once
}

// New 创建日结算服务实例。
// 参数/返回：repo 提供结算与查询能力。
// 失败场景：无直接失败场景，初始化失败在启动后记录日志。
// 副作用：无副作用，仅构造内存对象。
func New(repo *repository.StatsRepository) *Service {
	return &Service{
		repo:   repo,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start 启动后台结算协程（sync.Once 保证只启动一次）。
// 参数/返回：无参数无返回。
// 失败场景：建表失败时记录日志但继续运行，下一个小时会重试。
// 副作用：启动 goroutine，读写 traffic_stats_daily。
func (s *Service) Start() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.started.Store(true)
		go s.run()
		logx.Infof(dailySettleModule, "日结算服务已启动")
	})
}

// Stop 停止后台协程并等待退出（幂等）。
// 参数/返回：无参数无返回；未启动时直接返回，不会阻塞。
// 失败场景：无。
// 副作用：关闭信号并等待 goroutine 结束。
func (s *Service) Stop() {
	if s == nil || !s.started.Load() {
		return
	}
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
	<-s.doneCh
}

func (s *Service) run() {
	defer close(s.doneCh)

	// tracker 也会建表，这里再确保一次（CREATE TABLE IF NOT EXISTS 幂等），
	// 避免日结算服务在 tracker 建表之前启动时读不到表。
	if err := s.repo.EnsureTrafficStatsTables(); err != nil {
		logx.Errorf(dailySettleModule, "初始化流量表失败 err=%v", err)
	}

	s.settle()

	ticker := time.NewTicker(settleCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.settle()
		}
	}
}

// settle 结算「上次已结算日 + 1」到昨天之间的所有日期。
// 参数/返回：无参数无返回。
// 失败场景：读取已结算日期或写入失败时记录日志并跳过本轮（下一轮重试）。
// 副作用：写入 traffic_stats_daily。
// 说明：日表为空时视为首次运行，回填当月 1 号至今，避免升级当月「本月」数字断崖。
func (s *Service) settle() {
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	yesterday := todayStart.AddDate(0, 0, -1)

	settledThrough, err := s.repo.QueryDailySettledThrough()
	if err != nil {
		logx.Warnf(dailySettleModule, "读取已结算日期失败 err=%v", err)
		return
	}

	var start time.Time
	if settledThrough == "" {
		start = time.Date(yesterday.Year(), yesterday.Month(), 1, 0, 0, 0, 0, yesterday.Location())
		logx.Infof(
			dailySettleModule,
			"未发现历史结算记录，开始回填当月 from=%s to=%s",
			start.Format("2006-01-02"),
			yesterday.Format("2006-01-02"),
		)
	} else {
		last, parseErr := time.ParseInLocation("2006-01-02", settledThrough, now.Location())
		if parseErr != nil {
			logx.Warnf(dailySettleModule, "已结算日期格式异常 value=%s err=%v", settledThrough, parseErr)
			return
		}
		start = last.AddDate(0, 0, 1)
	}

	// 已结算到昨天（或日表里存在未来日期）时无需重复结算。
	if start.After(yesterday) {
		return
	}

	written, err := s.repo.SettleTrafficDailyRange(start, yesterday)
	if err != nil {
		logx.Warnf(
			dailySettleModule,
			"日结算失败 from=%s to=%s err=%v",
			start.Format("2006-01-02"),
			yesterday.Format("2006-01-02"),
			err,
		)
		return
	}
	if written > 0 {
		logx.Infof(
			dailySettleModule,
			"日结算完成 from=%s to=%s rows=%d",
			start.Format("2006-01-02"),
			yesterday.Format("2006-01-02"),
			written,
		)
	}
}
