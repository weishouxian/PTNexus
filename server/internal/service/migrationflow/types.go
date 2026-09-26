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
	}
}

func (s *MigrateService) newID(prefix string) string {
	return fmt.Sprintf("%s-%d-%06d", prefix, time.Now().UnixNano(), rand.Intn(1000000))
}
