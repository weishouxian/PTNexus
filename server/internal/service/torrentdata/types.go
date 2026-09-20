package torrentdata

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
)

type TorrentsDataParams struct {
	Page                      int
	PageSize                  int
	PathFilters               []string
	StateFilters              []string
	SourceDataStatusFilters   []string
	DownloaderFilters         []string
	SourceAvailabilityFilters []string
	ExistSiteNames            []string
	NotExistSiteNames         []string
	NameSearch                string
	SortProp                  string
	SortOrder                 string
	ExcludeExisting           bool
	OnlyCompleted             bool
}

type siteSummary struct {
	Uploaded  int64
	Comment   *string
	Migration int
	State     string
	Seeders   int64
}

type torrentSummary struct {
	Hash          string
	Hashes        []string
	Name          string
	SavePath      string
	Size          int64
	Progress      float64
	StateSet      map[string]struct{}
	Sites         map[string]*siteSummary
	TotalUploaded int64
	Seeders       int64
	DownloaderIDs []string
	OfficialSite  string
}

type TorrentDataService struct {
	repo      *repository.TorrentDataRepository
	cfg       *config.Manager
	iyuuTasks *IYUUTaskService
	// autoSeedRepo 用于「一种多站」删除种子后把对应的自动发种记录标记为 retained，可缺省（为 nil 时跳过标记）。
	autoSeedRepo *repository.AutoSeedRepository
	// publishLogRepo 用于「一种多站」删除种子后把对应的发种日志标记为作废，可缺省（为 nil 时跳过作废）。
	publishLogRepo *repository.PublishLogRepository

	refreshMu      sync.Mutex
	refreshRunning bool
	// 运行态快照：刷新进行中时供 refresh_data 返回「谁在跑、跑了多久」，
	// 避免前端只能拿到一句笼统的「正在进行中」而不知道原因。
	refreshTrigger   string
	refreshStartedAt time.Time
	refreshTargets   []string

	refreshStopCh chan struct{}
	refreshDoneCh chan struct{}
	refreshOnce   sync.Once

	iyuuMu      sync.Mutex
	iyuuRunning atomic.Bool

	iyuuLog func(level string, message string)
}

func NewTorrentDataService(repo *repository.TorrentDataRepository, cfg *config.Manager) *TorrentDataService {
	return &TorrentDataService{
		repo:          repo,
		cfg:           cfg,
		iyuuTasks:     NewIYUUTaskService(),
		refreshStopCh: make(chan struct{}),
		refreshDoneCh: make(chan struct{}),
	}
}

// SetAutoSeedRepository 注入自动发种仓储，用于删除种子时同步标记 auto_seed_items 记录。
// 参数/返回：repo 为自动发种仓储实例；无返回值。
// 失败场景：无。
// 副作用：后续「一种多站」删除操作会把命中的自动发种记录标记为 retained。
func (s *TorrentDataService) SetAutoSeedRepository(repo *repository.AutoSeedRepository) {
	if s == nil {
		return
	}
	s.autoSeedRepo = repo
}

// SetPublishLogRepository 注入发种日志仓储，用于删除种子时同步作废对应的发种日志。
// 参数/返回：repo 为发种日志仓储实例；无返回值。
// 失败场景：无。
// 副作用：后续「一种多站」删除操作会把命中种子的「已发布」日志标记为已作废。
func (s *TorrentDataService) SetPublishLogRepository(repo *repository.PublishLogRepository) {
	if s == nil {
		return
	}
	s.publishLogRepo = repo
}

// SetIYUULogger 设置 IYUU 查询过程的日志回调，便于在设置页展示进度信息。
// 参数/返回：level 为 INFO/WARN/ERROR 等等级，message 为完整日志文本；无返回值。
// 失败场景：无。
// 副作用：将日志写入回调实现方（通常为 SettingsService 的内存日志队列）。
func (s *TorrentDataService) SetIYUULogger(logger func(level string, message string)) {
	s.iyuuMu.Lock()
	s.iyuuLog = logger
	s.iyuuMu.Unlock()
}
