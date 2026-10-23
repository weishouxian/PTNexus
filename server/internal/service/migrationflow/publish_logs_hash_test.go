package migrationflow

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/pt-nexus/server/internal/repository"
	"gorm.io/gorm"
)

// newPublishLogRepoForTest 构造内存 SQLite 发种日志仓储（每个用例独立库名，避免相互串数据）。
func newPublishLogRepoForTest(t *testing.T) *repository.PublishLogRepository {
	t.Helper()
	dsn := "file:" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&repository.PublishLogEntry{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return repository.NewPublishLogRepository(&repository.Store{DB: db, DBType: "sqlite"})
}

// TestResolvePublishLogHashPrefersPayload 正常链路：payload/upload_data 里带 hash 时直接采用。
func TestResolvePublishLogHashPrefersPayload(t *testing.T) {
	s := &MigrateService{}
	got := s.resolvePublishLogHash(
		map[string]any{"hash": "AAA111"},
		map[string]any{"hash": "BBB222"},
		map[string]any{},
		"638987",
		nil,
	)
	if got != "bbb222" {
		t.Fatalf("应优先取 upload_data.hash（小写化），got=%q", got)
	}
}

// TestResolvePublishLogHashFallsBackToQueueLog 执行阶段 payload 缺 hash 时，回退到同一队列任务已落库的 hash。
// 这正是「发布到 青蛙 失败」这类记录从「一种多站」发布记录里消失的原因：hash 为空后被聚合查询过滤。
func TestResolvePublishLogHashFallsBackToQueueLog(t *testing.T) {
	repo := newPublishLogRepoForTest(t)
	taskID := int64(16204)
	if _, err := repo.Insert(&repository.PublishLogEntry{
		Trigger:     "manual",
		QueueTaskID: &taskID,
		TorrentID:   "638987",
		Hash:        "7fd05df1f5da65996a86fb1b714092b92602ed9a",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("写入排队日志失败: %v", err)
	}

	s := &MigrateService{publishLogRepo: repo}
	got := s.resolvePublishLogHash(map[string]any{}, map[string]any{}, map[string]any{}, "638987", &taskID)
	if got != "7fd05df1f5da65996a86fb1b714092b92602ed9a" {
		t.Fatalf("未从队列日志回填 hash, got=%q", got)
	}
}

// TestResolvePublishLogHashEmptyWhenNothingKnown 三处都取不到时返回空串（不 panic）。
func TestResolvePublishLogHashEmptyWhenNothingKnown(t *testing.T) {
	s := &MigrateService{}
	if got := s.resolvePublishLogHash(map[string]any{}, map[string]any{}, map[string]any{}, "", nil); got != "" {
		t.Fatalf("应返回空串, got=%q", got)
	}
}
