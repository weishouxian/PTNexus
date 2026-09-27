package migrationflow

import (
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/repository"
	extract "github.com/pt-nexus/server/internal/service/acquire/extract"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	processingbdflow "github.com/pt-nexus/server/internal/service/processing/bdflow"
	publishworkflow "github.com/pt-nexus/server/internal/service/publish/workflow"
)

type MigrateService struct {
	repo *repository.MigrateRepository
	cfg  *config.Manager

	extractorEngine *extract.Engine

	contextState    *publishworkflow.ContextState
	logStreamState  *acquirefetch.LogStreamState
	batchFetchState *acquirefetch.BatchFetchState
	publishState    *publishworkflow.BatchState
	bdinfoState     *processingbdflow.BDInfoState

	queueRepo      *repository.PublishQueueRepository
	publishLogRepo *repository.PublishLogRepository
	statsRepo      *repository.StatsRepository

	publishQueueScheduledSeedContinueHook func(trigger string, countAsSkipped bool)

	queueStartOnce sync.Once
	queueStopCh    chan struct{}
	queueDoneCh    chan struct{}
	// queueWakeCh 用于「立即发布」等操作唤醒队列线程，立刻执行一轮扫描而不必等下一个轮询周期。
	// 带缓冲（容量 1）以支持非阻塞投递：已有待处理唤醒时重复投递被丢弃即可。
	queueWakeCh chan struct{}

	// liveBatchesMu 保护 liveBatches；键为 batchID。
	liveBatchesMu sync.Mutex
	// liveBatches 记录运行中的「立即发布」批次上下文（进度回写器 + 基础 payload），
	// 供进度页对 dispatched 记录做单站取消 / 单站立即发布时复用发布链路与进度回写。
	liveBatches map[string]*liveBatchContext
}

// liveBatchContext 保存一个运行中的立即发布批次的站外操作所需上下文。
type liveBatchContext struct {
	tracker *livePublishProgressTracker
	payload map[string]any
}

func NewMigrateService(repo *repository.MigrateRepository, cfg *config.Manager) *MigrateService {
	return &MigrateService{
		repo:            repo,
		cfg:             cfg,
		extractorEngine: extract.NewPageExtractorEngine(),
		contextState:    publishworkflow.NewContextState(),
		logStreamState:  acquirefetch.NewLogStreamState(),
		batchFetchState: acquirefetch.NewBatchFetchState(),
		publishState:    publishworkflow.NewBatchState(),
		bdinfoState:     processingbdflow.NewBDInfoState(),
		liveBatches:     map[string]*liveBatchContext{},
	}
}

func (s *MigrateService) newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%06d", prefix, time.Now().UnixNano(), rand.Intn(1000000))
}
