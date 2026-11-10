package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newPublishQueueTestRepo 构造一个基于内存 SQLite 的发布队列仓储（仅测试用）。
func newPublishQueueTestRepo(t *testing.T) *PublishQueueRepository {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := db.AutoMigrate(&PublishQueueTask{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	return NewPublishQueueRepository(&Store{DB: db, DBType: "sqlite"})
}

// insertQueuedTask 写入一条 queued 任务，返回自增 id。
// scheduledAt/nextRunAt 传空串表示该字段为 NULL。
func insertQueuedTask(t *testing.T, repo *PublishQueueRepository, targetSite, scheduledAt, nextRunAt string) int64 {
	t.Helper()
	task := PublishQueueTask{
		GroupID:    "g1",
		Status:     PublishQueueStatusQueued,
		TargetSite: targetSite,
		CreatedAt:  "2026-11-10 20:00:00",
		UpdatedAt:  "2026-11-10 20:00:00",
	}
	if scheduledAt != "" {
		value := scheduledAt
		task.ScheduledAt = &value
	}
	if nextRunAt != "" {
		value := nextRunAt
		task.NextRunAt = &value
	}
	if err := repo.store.DB.Create(&task).Error; err != nil {
		t.Fatalf("写入队列任务失败: %v", err)
	}
	return task.ID
}

// listPendingIDs 按进度页口径列出待发布任务 id（与前端表格顺序一致）。
func listPendingIDs(t *testing.T, repo *PublishQueueRepository) []int64 {
	t.Helper()
	rows, _, err := repo.ListTasks(PublishQueueTaskQuery{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatalf("查询队列任务失败: %v", err)
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

// claimIDs 连续领取若干次，返回实际领取顺序。
func claimIDs(t *testing.T, repo *PublishQueueRepository, now time.Time, count int) []int64 {
	t.Helper()
	ids := make([]int64, 0, count)
	for i := 0; i < count; i++ {
		task, ok, err := repo.ClaimNextRunnableTask(now)
		if err != nil {
			t.Fatalf("领取任务失败: %v", err)
		}
		if !ok || task == nil {
			break
		}
		ids = append(ids, task.ID)
	}
	return ids
}

func assertIDs(t *testing.T, label string, got []int64, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s 数量不符: got=%v want=%v", label, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s 顺序不符: got=%v want=%v", label, got, want)
		}
	}
}

// TestPublishQueuePendingOrderMatchesClaimOrder 计划时间相同时，进度页列表与后台领取必须同为「先入队先发」。
// 回归点：列表曾用 id DESC 做同时间兜底，而领取用 id ASC，导致页面最上面那条实际最后才发。
func TestPublishQueuePendingOrderMatchesClaimOrder(t *testing.T) {
	repo := newPublishQueueTestRepo(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	planned := now.Add(-time.Minute).Format(PublishQueueTimeLayout)

	want := make([]int64, 0, 4)
	for i := 0; i < 4; i++ {
		want = append(want, insertQueuedTask(t, repo, "站点"+string(rune('A'+i)), planned, planned))
	}

	assertIDs(t, "列表待发布顺序", listPendingIDs(t, repo), want)
	assertIDs(t, "后台领取顺序", claimIDs(t, repo, now, len(want)), want)
}

// TestPublishQueueClaimFollowsScheduledTime 计划时间不同且 id 顺序相反时，仍按计划时间早的先发。
// 回归点：领取曾只按 next_run_at/created_at 排序，忽略 scheduled_at，与列表口径不一致。
func TestPublishQueueClaimFollowsScheduledTime(t *testing.T) {
	repo := newPublishQueueTestRepo(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	late := now.Add(time.Hour).Format(PublishQueueTimeLayout)
	early := now.Add(-time.Minute).Format(PublishQueueTimeLayout)

	lateID := insertQueuedTask(t, repo, "晚发站", late, late)
	earlyID := insertQueuedTask(t, repo, "早发站", early, early)

	assertIDs(t, "列表待发布顺序", listPendingIDs(t, repo), []int64{earlyID, lateID})
	assertIDs(t, "后台领取顺序", claimIDs(t, repo, now, 1), []int64{earlyID})
}

// TestLatestQueuedScheduledAtIgnoresNextRunAt 队列尾部只认 scheduled_at（发布节奏位置），
// 不认 next_run_at（门控/重试的临时等待）。
// 回归点：曾用 COALESCE(scheduled_at, next_run_at, created_at) 取最大值，一条因上传速度过高
// 被推迟 30 分钟的任务会把新加入的整批任务一起推到 30 分钟后。
func TestLatestQueuedScheduledAtIgnoresNextRunAt(t *testing.T) {
	repo := newPublishQueueTestRepo(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	firstWave := now.Add(5 * time.Minute).Format(PublishQueueTimeLayout)
	secondWave := now.Add(10 * time.Minute).Format(PublishQueueTimeLayout)
	gateWait := now.Add(30 * time.Minute).Format(PublishQueueTimeLayout)

	insertQueuedTask(t, repo, "第一波", firstWave, firstWave)
	insertQueuedTask(t, repo, "第二波", secondWave, secondWave)
	// 门控重排队：没有计划时间，只有下次运行时间。
	insertQueuedTask(t, repo, "门控等待站", "", gateWait)

	latest, err := repo.LatestQueuedScheduledAt("")
	if err != nil {
		t.Fatalf("读取队尾时间失败: %v", err)
	}
	if latest == nil {
		t.Fatalf("队尾时间不应为空")
	}
	want, _ := time.ParseInLocation(PublishQueueTimeLayout, secondWave, time.Local)
	if !latest.Equal(want) {
		t.Fatalf("队尾时间未按 scheduled_at 取值: got=%s want=%s", latest.Format(PublishQueueTimeLayout), secondWave)
	}
}

// TestRequeueStaleRunningTasks 启动时把残留的 running 任务放回队列，避免僵尸行长期占据列表首位。
func TestRequeueStaleRunningTasks(t *testing.T) {
	repo := newPublishQueueTestRepo(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	// 中断 10 分钟：在回收时限内，应回队列继续执行。
	started := now.Add(-10 * time.Minute).Format(PublishQueueTimeLayout)
	stale := PublishQueueTask{
		Status:     PublishQueueStatusRunning,
		TargetSite: "中断站",
		StartedAt:  &started,
		CreatedAt:  started,
		UpdatedAt:  started,
	}
	if err := repo.store.DB.Create(&stale).Error; err != nil {
		t.Fatalf("写入 running 任务失败: %v", err)
	}

	requeued, discarded, err := repo.RequeueStaleRunningTasks(now)
	if err != nil {
		t.Fatalf("回收 running 任务失败: %v", err)
	}
	if requeued != 1 || discarded != 0 {
		t.Fatalf("回收条数不符: requeued=%d discarded=%d want=1/0", requeued, discarded)
	}

	task, ok, err := repo.FindTaskByID(stale.ID)
	if err != nil || !ok || task == nil {
		t.Fatalf("读取任务失败 ok=%v err=%v", ok, err)
	}
	if task.Status != PublishQueueStatusQueued {
		t.Fatalf("任务状态未回到 queued: %s", task.Status)
	}
	if task.StartedAt != nil {
		t.Fatalf("started_at 未清空: %v", *task.StartedAt)
	}
}

// TestRequeueStaleRunningTasksDiscardsExpired 中断超过时限的 running 任务直接判失败，不重新发布。
// 回归点：曾不限时限回收，导致 2026-08-01 的僵尸任务在 11 月重启后被放回队列并真的发到了站点。
func TestRequeueStaleRunningTasksDiscardsExpired(t *testing.T) {
	repo := newPublishQueueTestRepo(t)
	now := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	started := now.Add(-72 * time.Hour).Format(PublishQueueTimeLayout)
	expired := PublishQueueTask{
		Status:     PublishQueueStatusRunning,
		TargetSite: "僵尸站",
		StartedAt:  &started,
		CreatedAt:  started,
		UpdatedAt:  started,
	}
	if err := repo.store.DB.Create(&expired).Error; err != nil {
		t.Fatalf("写入 running 任务失败: %v", err)
	}

	requeued, discarded, err := repo.RequeueStaleRunningTasks(now)
	if err != nil {
		t.Fatalf("回收 running 任务失败: %v", err)
	}
	if requeued != 0 || discarded != 1 {
		t.Fatalf("回收条数不符: requeued=%d discarded=%d want=0/1", requeued, discarded)
	}

	task, ok, err := repo.FindTaskByID(expired.ID)
	if err != nil || !ok || task == nil {
		t.Fatalf("读取任务失败 ok=%v err=%v", ok, err)
	}
	if task.Status != PublishQueueStatusFailed {
		t.Fatalf("超时任务应判为 failed，实际: %s", task.Status)
	}
	if task.FinishedAt == nil || strings.TrimSpace(*task.FinishedAt) == "" {
		t.Fatalf("超时任务未写 finished_at")
	}
}
