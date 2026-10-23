package repository

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newPublishLogTestRepo 构造一个基于内存 SQLite 的发种日志仓储（仅测试用）。
func newPublishLogTestRepo(t *testing.T) *PublishLogRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&PublishLogEntry{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return NewPublishLogRepository(&Store{DB: db, DBType: "sqlite"})
}

// TestUpsertByQueueTaskIDKeepsHashWhenEntryBlank 队列任务执行完成时若 payload 里没有 hash，
// 不能把入队时写入的 hash 抹空——否则「一种多站」发布记录（按 hash 聚合）里会查不到这条记录。
func TestUpsertByQueueTaskIDKeepsHashWhenEntryBlank(t *testing.T) {
	repo := newPublishLogTestRepo(t)
	taskID := int64(16204)
	if _, err := repo.Insert(&PublishLogEntry{
		Trigger:     "manual",
		Scene:       "multi_site",
		QueueTaskID: &taskID,
		TorrentID:   "638987",
		Hash:        "7fd05df1f5da65996a86fb1b714092b92602ed9a",
		Name:        "K-POP 猎魔女团 KPop Demon Hunters 2025 1080p",
		TargetSite:  "青蛙",
		SourceSite:  "天空",
		Title:       "KPop Demon Hunters 2025",
		Status:      "queued",
		CreatedAt:   "2026-10-24 00:46:00",
		UpdatedAt:   "2026-10-24 00:46:00",
	}); err != nil {
		t.Fatalf("写入排队日志失败: %v", err)
	}

	// 执行阶段：payload/upload_data 都没带 hash，name 也拿不到。
	if err := repo.UpsertByQueueTaskID(&PublishLogEntry{
		Trigger:     "manual",
		Scene:       "multi_site",
		QueueTaskID: &taskID,
		TorrentID:   "638987",
		TargetSite:  "青蛙",
		Status:      "failed",
		Logs:        "发布结果：发布到 青蛙 失败: 疑似登录页（cookie 可能已失效）",
	}); err != nil {
		t.Fatalf("更新发布日志失败: %v", err)
	}

	row, ok, err := repo.FindLatestByQueueTaskID(taskID)
	if err != nil || !ok || row == nil {
		t.Fatalf("读取发布日志失败 ok=%v err=%v", ok, err)
	}
	if row.Hash != "7fd05df1f5da65996a86fb1b714092b92602ed9a" {
		t.Fatalf("hash 被空值覆盖了: %q", row.Hash)
	}
	if row.Name == "" {
		t.Fatalf("name 被空值覆盖了")
	}
	if row.Status != "failed" {
		t.Fatalf("状态未更新: %q", row.Status)
	}
	if row.Logs == "" {
		t.Fatalf("日志未更新")
	}
}

// TestUpsertByQueueTaskIDOverwritesWithNewHash 新值非空时照常覆盖（正常发布链路）。
func TestUpsertByQueueTaskIDOverwritesWithNewHash(t *testing.T) {
	repo := newPublishLogTestRepo(t)
	taskID := int64(20001)
	if _, err := repo.Insert(&PublishLogEntry{
		Trigger:     "manual",
		QueueTaskID: &taskID,
		Hash:        "aaaa",
		Status:      "queued",
	}); err != nil {
		t.Fatalf("写入排队日志失败: %v", err)
	}
	if err := repo.UpsertByQueueTaskID(&PublishLogEntry{
		Trigger:     "manual",
		QueueTaskID: &taskID,
		Hash:        "bbbb",
		Status:      "success",
	}); err != nil {
		t.Fatalf("更新发布日志失败: %v", err)
	}
	row, _, err := repo.FindLatestByQueueTaskID(taskID)
	if err != nil || row == nil {
		t.Fatalf("读取发布日志失败: %v", err)
	}
	if row.Hash != "bbbb" {
		t.Fatalf("非空 hash 未覆盖: %q", row.Hash)
	}
}
