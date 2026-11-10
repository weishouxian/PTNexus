package migrationflow

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"gorm.io/gorm"
)

// newAppendBaseTestService 构造一个只挂了队列仓储的最小 MigrateService（仅测试用）。
func newAppendBaseTestService(t *testing.T) (*MigrateService, *repository.PublishQueueRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&repository.PublishQueueTask{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	repo := repository.NewPublishQueueRepository(&repository.Store{DB: db, DBType: "sqlite"})
	// queueRepo 为包内字段，测试与服务同包，可直接注入；queueAppendBaseTime 只依赖它。
	return &MigrateService{queueRepo: repo}, repo
}

// insertPendingTask 写入一条待发布任务（downloader_id 用于分桶）。
func insertPendingTask(t *testing.T, repo *repository.PublishQueueRepository, downloaderID, scheduledAt string) {
	t.Helper()
	task := repository.PublishQueueTask{
		Status:       repository.PublishQueueStatusQueued,
		DownloaderID: downloaderID,
		TargetSite:   "站点",
		CreatedAt:    scheduledAt,
		UpdatedAt:    scheduledAt,
	}
	value := scheduledAt
	task.ScheduledAt = &value
	task.NextRunAt = &value
	if err := repo.DB().Create(&task).Error; err != nil {
		t.Fatalf("写入队列任务失败: %v", err)
	}
}

// TestQueueAppendBaseTimeAppendsAfterTail 未启用节奏时，新入队基准时间取队尾（不插队）。
func TestQueueAppendBaseTimeAppendsAfterTail(t *testing.T) {
	svc, repo := newAppendBaseTestService(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	tail := now.Add(30 * time.Minute)
	insertPendingTask(t, repo, "dl1", tail.Format(repository.PublishQueueTimeLayout))

	got := svc.queueAppendBaseTime("dl1", now, 0)
	if !got.Equal(tail) {
		t.Fatalf("无节奏时基准时间应为队尾: got=%s want=%s", got.Format(repository.PublishQueueTimeLayout), tail.Format(repository.PublishQueueTimeLayout))
	}
}

// TestQueueAppendBaseTimeAddsIntervalWithPacing 启用节奏时，本批次第一波从队尾再顺延一个间隔。
func TestQueueAppendBaseTimeAddsIntervalWithPacing(t *testing.T) {
	svc, repo := newAppendBaseTestService(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	tail := now.Add(30 * time.Minute)
	insertPendingTask(t, repo, "dl1", tail.Format(repository.PublishQueueTimeLayout))

	got := svc.queueAppendBaseTime("dl1", now, 5*time.Minute)
	want := tail.Add(5 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("有节奏时基准时间应为队尾+间隔: got=%s want=%s", got.Format(repository.PublishQueueTimeLayout), want.Format(repository.PublishQueueTimeLayout))
	}
}

// TestQueueAppendBaseTimeFallsBackToNow 队列为空或队尾已过期时返回 now（可立即参与发布）。
func TestQueueAppendBaseTimeFallsBackToNow(t *testing.T) {
	svc, repo := newAppendBaseTestService(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)

	if got := svc.queueAppendBaseTime("dl1", now, 0); !got.Equal(now) {
		t.Fatalf("空队列应返回 now: got=%s", got.Format(repository.PublishQueueTimeLayout))
	}

	expired := now.Add(-time.Hour)
	insertPendingTask(t, repo, "dl1", expired.Format(repository.PublishQueueTimeLayout))
	if got := svc.queueAppendBaseTime("dl1", now, 5*time.Minute); !got.Equal(now) {
		t.Fatalf("队尾已过期应返回 now: got=%s", got.Format(repository.PublishQueueTimeLayout))
	}
}

// insertDispatchedTask 写入一条「立即发布」登记的待执行记录（status=dispatched）。
func insertDispatchedTask(t *testing.T, repo *repository.PublishQueueRepository, downloaderID, scheduledAt string) {
	t.Helper()
	value := scheduledAt
	task := repository.PublishQueueTask{
		Status:       repository.PublishQueueStatusDispatched,
		DownloaderID: downloaderID,
		TargetSite:   "立即发布站",
		CreatedAt:    scheduledAt,
		UpdatedAt:    scheduledAt,
		ScheduledAt:  &value,
	}
	if err := repo.DB().Create(&task).Error; err != nil {
		t.Fatalf("写入 dispatched 任务失败: %v", err)
	}
}

// TestQueueAppendBaseTimeCountsDispatched 「立即发布」还没执行的记录也要算进队尾。
// 回归点：队列尾部查询只统计 queued/running，22:00 入队时进度页里 22:05 的 dispatched
// 记录被忽略，导致新批次第一波从当前时间立刻发出（2026-11-10 生产实测）。
func TestQueueAppendBaseTimeCountsDispatched(t *testing.T) {
	svc, repo := newAppendBaseTestService(t)
	now := time.Date(2026, 11, 10, 22, 0, 0, 0, time.Local)
	liveAt := now.Add(5 * time.Minute)
	insertDispatchedTask(t, repo, "dl1", liveAt.Format(repository.PublishQueueTimeLayout))

	got := svc.queueAppendBaseTime("dl1", now, 10*time.Minute)
	want := liveAt.Add(10 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("dispatched 记录应计入队尾: got=%s want=%s", got.Format(repository.PublishQueueTimeLayout), want.Format(repository.PublishQueueTimeLayout))
	}
}

// TestQueueAppendBaseTimeIgnoresOtherDownloaders 每个下载器一条队列：计算基准时不受其它下载器影响。
func TestQueueAppendBaseTimeIgnoresOtherDownloaders(t *testing.T) {
	svc, repo := newAppendBaseTestService(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	insertPendingTask(t, repo, "dl2", now.Add(time.Hour).Format(repository.PublishQueueTimeLayout))

	if got := svc.queueAppendBaseTime("dl1", now, 0); !got.Equal(now) {
		t.Fatalf("其它下载器的队尾不应影响本下载器: got=%s", got.Format(repository.PublishQueueTimeLayout))
	}
}
