package reseed

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
)

// 辅种记录状态取值。
const (
	StatusAdded     = "added"     // 已下载种子并加入下载器
	StatusDuplicate = "duplicate" // 下载器已存在同一 infohash
	StatusSkipped   = "skipped"   // 目标站已有真实做种记录
	StatusMismatch  = "mismatch"  // IYUU 返回的 infohash 与本地不一致
	StatusNoMatch   = "no_match"  // IYUU 没有返回该目标站的记录（未收录/站点没有该种）
	// StatusAuthFailed 表示目标站凭据问题（Cookie 失效 / Passkey 无效 / 需要登录），
	// 与普通下载失败分开，页面才能直接指出该去更新哪个站点的哪项凭据。
	StatusAuthFailed = "auth_failed"
	StatusFailed     = "failed" // 下载或添加失败
)

// Params 是启动一次辅种任务的入参。
type Params struct {
	// Sites 为目标站点昵称列表（对应 sites.nickname）。
	Sites []string `json:"sites"`
	// Paths 为保存路径过滤，为空表示不限制。
	Paths []string `json:"paths"`
	// DownloaderID 为空表示沿用种子记录自身的下载器。
	DownloaderID string `json:"downloader_id"`
	// Limit 为本次最多处理的种子组数量。
	Limit int `json:"limit"`
	// SkipChecking 表示加入下载器时跳过校验（辅种默认 true）。
	SkipChecking bool `json:"skip_checking"`
	// Hashes 非空时只处理这些 infohash 命中的种子（指定种子试跑用），
	// 此时忽略体积下限与数量上限，避免用户明确选了种子却被静默过滤掉。
	Hashes []string `json:"hashes"`
	// Tags 为加入下载器时写入的标签（qB 的 tags / Transmission 的 labels），为空表示不打标签。
	Tags []string `json:"tags"`
	// TagBySite 为 true 时在每个种子的标签里额外加上「站点/目标站名」，
	// 与设置中心「下载器标签/分类设置」的站点标签同一口径，便于统一筛选。
	TagBySite bool `json:"tag_by_site"`
}

// Task 表示一次辅种任务的运行时状态（内存保存在进程内）。
type Task struct {
	TaskID     string         `json:"task_id"`
	CreatedAt  string         `json:"created_at"`
	FinishedAt *string        `json:"finished_at"`
	IsRunning  bool           `json:"isRunning"`
	Success    *bool          `json:"success"`
	Message    string         `json:"message"`
	Total      int            `json:"total"`
	Processed  int            `json:"processed"`
	Stats      map[string]any `json:"stats"`
	Params     Params         `json:"params"`
}

// candidate 表示按 name+size 归并后的一个种子组。
type candidate struct {
	Name         string
	Size         int64
	SavePath     string
	DownloaderID string
	SourceSite   string
	Hashes       []string
}

// matchItem 表示 IYUU 返回的「某站点上也存在该种子」的命中信息。
type matchItem struct {
	Site      string
	SID       int64
	TorrentID string
	InfoHash  string
	DetailURL string
}

// Service 提供 IYUU 指定站点辅种能力。
type Service struct {
	repo        *repository.ReseedRepository
	torrentRepo *repository.TorrentDataRepository
	migrateRepo *repository.MigrateRepository
	store       *repository.Store
	cfg         *config.Manager

	mu    sync.RWMutex
	tasks map[string]*Task

	running       atomic.Bool
	stopRequested atomic.Bool

	// supportedNames / supportedKnown 缓存「IYUU 支持且本地存在的站点昵称」，
	// 状态接口每次都会用到，避免每开一次页面就请求一次 IYUU。
	supportedMu        sync.Mutex
	supportedNames     map[string]struct{}
	supportedKnown     bool
	supportedCheckedAt time.Time
}

// NewService 创建辅种服务。
func NewService(repo *repository.ReseedRepository, torrentRepo *repository.TorrentDataRepository, migrateRepo *repository.MigrateRepository, store *repository.Store, cfg *config.Manager) *Service {
	return &Service{
		repo:        repo,
		torrentRepo: torrentRepo,
		migrateRepo: migrateRepo,
		store:       store,
		cfg:         cfg,
		tasks:       map[string]*Task{},
	}
}
