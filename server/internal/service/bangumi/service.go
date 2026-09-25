package bangumi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

// 番组数据（bangumi-data）同步：整表拉取、整表替换，并记录同步元信息供前端展示。
const (
	logModule = "番组数据"

	// syncInterval 两次成功同步之间的最小间隔：1 天。
	syncInterval = 24 * time.Hour
	// checkInterval 后台轮询间隔，用于判断是否已到同步时间（配合重启后的补偿同步）。
	checkInterval = 30 * time.Minute
	// initialDelay 启动后延迟多久执行首次检查，避免与启动流程抢资源。
	initialDelay = 15 * time.Second
	// fetchTimeout 单次下载超时时间（数据包约 8MB，留足余量）。
	fetchTimeout = 5 * time.Minute
	// maxBodyBytes 下载体积上限，防止异常响应撑爆内存。
	maxBodyBytes = 128 << 20

	// dataURLEnv 允许通过环境变量覆盖数据源地址（多地址用逗号分隔）。
	dataURLEnv = "PTNEXUS_BANGUMI_DATA_URL"

	timeLayout = "2006-01-02 15:04:05"
)

// defaultDataURLs 为默认数据源候选列表，按顺序尝试：
// 主源固定使用 unpkg 的最新版（不带版本号会 302 到当前 latest），
// 备用源为 jsDelivr 最新版，两者均指向 bangumi-data 仓库构建产物。
var defaultDataURLs = []string{
	"https://unpkg.com/bangumi-data/dist/data.json",
	"https://cdn.jsdelivr.net/npm/bangumi-data/dist/data.json",
}

// ErrSyncInProgress 表示已有同步任务正在执行，本次请求被拒绝。
var ErrSyncInProgress = errors.New("番组数据同步正在进行中，请稍后再试")

// Service 负责番组数据的下载、解析、落库与定时调度。
type Service struct {
	repo   *repository.BangumiRepository
	client *http.Client

	syncMu  sync.Mutex
	running bool

	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

// NewService 创建番组数据服务。
// 参数/返回：repo 为番组数据仓储；返回可复用的服务实例。
// 失败场景：无。
// 副作用：无副作用，仅构造对象。
func NewService(repo *repository.BangumiRepository) *Service {
	client := &http.Client{
		Timeout: fetchTimeout,
		// 使用 http.DefaultTransport（已被 netproxy 接管），自动遵循应用的网络代理配置。
	}
	return &Service{
		repo:   repo,
		client: client,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
}

// Start 启动后台定时同步（幂等）。
// 参数/返回：无参数，无返回值。
// 失败场景：无（网络异常仅在后台记录日志）。
// 副作用：启动一个后台 goroutine，按 24 小时周期同步番组数据。
func (s *Service) Start() {
	if s == nil || s.repo == nil {
		return
	}
	s.startOnce.Do(func() {
		go s.run()
		logx.Infof(logModule, "定时同步调度器已启动 间隔=24h")
	})
}

// Stop 停止后台同步调度。
// 参数/返回：无参数，无返回值。
// 失败场景：无。
// 副作用：关闭后台 goroutine。
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() { close(s.stopCh) })
	select {
	case <-s.doneCh:
	case <-time.After(5 * time.Second):
	}
}

// Running 返回当前是否有同步任务在执行。
func (s *Service) Running() bool {
	if s == nil {
		return false
	}
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	return s.running
}

// Meta 读取最近一次同步的状态快照。
func (s *Service) Meta() (*repository.BangumiSyncMeta, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("番组数据仓储未初始化")
	}
	return s.repo.LoadSyncMeta()
}

// List 分页查询番组条目。
func (s *Service) List(filter repository.BangumiListFilter) ([]repository.BangumiItem, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("番组数据仓储未初始化")
	}
	return s.repo.ListItems(filter)
}

// Counts 统计各类别条目数与总数。
func (s *Service) Counts() (map[string]int64, int64, error) {
	if s == nil || s.repo == nil {
		return nil, 0, errors.New("番组数据仓储未初始化")
	}
	return s.repo.CountByType()
}

// TriggerSyncAsync 在后台触发一次同步，立即返回是否成功启动。
// 参数/返回：返回 false 表示已有同步在执行。
// 失败场景：无（同步失败仅在后台记录状态与日志）。
// 副作用：启动后台 goroutine 执行同步。
func (s *Service) TriggerSyncAsync() bool {
	if s == nil || s.repo == nil {
		return false
	}
	if s.Running() {
		return false
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout+time.Minute)
		defer cancel()
		if _, err := s.Sync(ctx); err != nil {
			logx.Warnf(logModule, "手动同步未成功 err=%v", err)
		}
	}()
	return true
}

// Sync 执行一次完整的同步：下载 -> 解析 -> 整表替换 -> 记录元信息。
// 参数/返回：ctx 用于控制取消与超时；成功返回本次同步元信息。
// 失败场景：已有同步在执行、数据源全部不可用、响应解析失败、数据为空或落库失败时返回错误。
// 副作用：替换 bangumi_items 表内容并写入 bangumi_sync_meta。
func (s *Service) Sync(ctx context.Context) (*repository.BangumiSyncMeta, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("番组数据仓储未初始化")
	}
	if !s.tryLock() {
		return nil, ErrSyncInProgress
	}
	defer s.unlock()

	startedAt := time.Now()
	previous, err := s.repo.LoadSyncMeta()
	if err != nil || previous == nil {
		previous = &repository.BangumiSyncMeta{}
	}

	// 先把状态置为 running 并落库，前端轮询即可看到「同步中」。
	pending := &repository.BangumiSyncMeta{
		Status:        repository.BangumiSyncStatusRunning,
		Message:       "正在同步 Bangumi 番组数据…",
		LastSyncAt:    previous.LastSyncAt,
		LastAttemptAt: startedAt.Format(timeLayout),
		NextSyncAt:    formatNextSync(startedAt),
		ItemCount:     previous.ItemCount,
		SourceURL:     previous.SourceURL,
		Version:       previous.Version,
	}
	if saveErr := s.repo.SaveSyncMeta(pending); saveErr != nil {
		logx.Warnf(logModule, "写入同步中状态失败 err=%v", saveErr)
	}

	body, sourceURL, err := s.fetch(ctx)
	if err != nil {
		s.markFailed(startedAt, previous, err)
		return nil, err
	}

	dataset, err := parseDataSet(body)
	if err != nil {
		s.markFailed(startedAt, previous, err)
		return nil, err
	}

	items := buildItems(dataset)
	if len(items) == 0 {
		err = errors.New("数据源未解析出任何番组条目")
		s.markFailed(startedAt, previous, err)
		return nil, err
	}

	count, err := s.repo.ReplaceItems(items)
	if err != nil {
		s.markFailed(startedAt, previous, err)
		return nil, fmt.Errorf("写入番组数据失败: %w", err)
	}

	finishedAt := time.Now()
	meta := &repository.BangumiSyncMeta{
		Status:        repository.BangumiSyncStatusSuccess,
		Message:       fmt.Sprintf("同步成功，共 %d 条番组条目", count),
		LastSyncAt:    finishedAt.Format(timeLayout),
		LastAttemptAt: startedAt.Format(timeLayout),
		NextSyncAt:    formatNextSync(finishedAt),
		ItemCount:     count,
		DurationMs:    finishedAt.Sub(startedAt).Milliseconds(),
		SourceURL:     sourceURL,
		Version:       extractDataVersion(sourceURL),
	}
	if saveErr := s.repo.SaveSyncMeta(meta); saveErr != nil {
		logx.Warnf(logModule, "写入同步结果失败 err=%v", saveErr)
	}

	logx.Infof(logModule, "同步完成 条目数=%d 耗时=%dms 数据源=%s", count, meta.DurationMs, sourceURL)
	return meta, nil
}

// markFailed 记录同步失败状态（保留上一次成功同步时间与条目数）。
func (s *Service) markFailed(startedAt time.Time, previous *repository.BangumiSyncMeta, cause error) {
	if s == nil || s.repo == nil {
		return
	}
	finishedAt := time.Now()
	meta := &repository.BangumiSyncMeta{
		Status:        repository.BangumiSyncStatusFailed,
		Message:       "同步失败: " + cause.Error(),
		LastSyncAt:    previous.LastSyncAt,
		LastAttemptAt: startedAt.Format(timeLayout),
		NextSyncAt:    previous.NextSyncAt,
		ItemCount:     previous.ItemCount,
		DurationMs:    finishedAt.Sub(startedAt).Milliseconds(),
		SourceURL:     previous.SourceURL,
		Version:       previous.Version,
	}
	if err := s.repo.SaveSyncMeta(meta); err != nil {
		logx.Warnf(logModule, "写入同步失败状态异常 err=%v", err)
	}
	logx.Warnf(logModule, "同步失败 err=%v", cause)
}

func (s *Service) tryLock() bool {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	return true
}

func (s *Service) unlock() {
	s.syncMu.Lock()
	s.running = false
	s.syncMu.Unlock()
}

// run 后台调度循环：启动后补一次「过期即同步」，此后每 30 分钟检查一次是否到达 24 小时周期。
func (s *Service) run() {
	defer close(s.doneCh)

	select {
	case <-s.stopCh:
		return
	case <-time.After(initialDelay):
	}
	s.syncIfStale()

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stopCh:
			logx.Infof(logModule, "定时同步调度器已停止")
			return
		case <-ticker.C:
			s.syncIfStale()
		}
	}
}

// syncIfStale 在「距上次成功同步已超过 24 小时」时触发一次同步。
func (s *Service) syncIfStale() {
	if s == nil || s.repo == nil || s.Running() {
		return
	}
	meta, err := s.repo.LoadSyncMeta()
	if err != nil {
		logx.Warnf(logModule, "读取同步状态失败 err=%v", err)
		return
	}
	if meta.Status == repository.BangumiSyncStatusRunning {
		return
	}
	if last, ok := parseTime(meta.LastSyncAt); ok && time.Since(last) < syncInterval {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout+time.Minute)
	defer cancel()
	if _, err := s.Sync(ctx); err != nil {
		// Sync 内部已记录失败状态与日志，这里仅避免错误被忽略。
		logx.Warnf(logModule, "定时同步未成功 err=%v", err)
	}
}

// fetch 依次尝试候选数据源，返回首个成功响应的内容与实际使用（含重定向后）的地址。
func (s *Service) fetch(ctx context.Context) ([]byte, string, error) {
	urls := resolveDataURLs()
	var lastErr error
	for _, target := range urls {
		body, finalURL, err := s.fetchOne(ctx, target)
		if err == nil {
			return body, finalURL, nil
		}
		lastErr = err
		logx.Warnf(logModule, "数据源不可用 地址=%s err=%v", target, err)
	}
	if lastErr == nil {
		lastErr = errors.New("未配置任何数据源地址")
	}
	return nil, "", fmt.Errorf("全部数据源均不可用: %w", lastErr)
}

// fetchOne 下载单个数据源地址，并返回跟随重定向后的真实地址。
func (s *Service) fetchOne(ctx context.Context, target string) ([]byte, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "PTNexus/bangumi-sync")

	response, err := s.client.Do(request)
	if err != nil {
		return nil, "", err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", response.StatusCode)
	}

	finalURL := target
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL.String()
	}

	limited := io.LimitReader(response.Body, maxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBodyBytes {
		return nil, "", errors.New("响应体超过体积上限")
	}
	if len(body) == 0 {
		return nil, "", errors.New("响应体为空")
	}
	return body, finalURL, nil
}

// resolveDataURLs 解析数据源候选地址：环境变量优先（逗号分隔），否则使用内置默认列表。
func resolveDataURLs() []string {
	raw := strings.TrimSpace(os.Getenv(dataURLEnv))
	if raw == "" {
		return defaultDataURLs
	}
	urls := make([]string, 0, 2)
	for _, item := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			urls = append(urls, trimmed)
		}
	}
	if len(urls) == 0 {
		return defaultDataURLs
	}
	return urls
}

// rawDataSet 对应 bangumi-data 的顶层结构。
type rawDataSet struct {
	SiteMeta map[string]rawSiteMeta `json:"siteMeta"`
	Items    []rawItem              `json:"items"`
}

type rawSiteMeta struct {
	Title       string   `json:"title"`
	URLTemplate string   `json:"urlTemplate"`
	Type        string   `json:"type"`
	Regions     []string `json:"regions"`
}

type rawItem struct {
	Title          string              `json:"title"`
	TitleTranslate map[string][]string `json:"titleTranslate"`
	Type           string              `json:"type"`
	Lang           string              `json:"lang"`
	OfficialSite   string              `json:"officialSite"`
	Begin          string              `json:"begin"`
	End            string              `json:"end"`
	Comment        string              `json:"comment"`
	Broadcast      string              `json:"broadcast"`
	Sites          []rawItemSite       `json:"sites"`
}

type rawItemSite struct {
	Site string `json:"site"`
	ID   string `json:"id"`
}

// parseDataSet 解析下载到的 JSON。
func parseDataSet(body []byte) (*rawDataSet, error) {
	dataset := &rawDataSet{}
	if err := json.Unmarshal(body, dataset); err != nil {
		return nil, fmt.Errorf("解析番组数据失败: %w", err)
	}
	return dataset, nil
}

// buildItems 把原始数据转换为可落库的条目集合。
// 参数/返回：dataset 为原始数据集；返回按上游顺序排列的条目切片。
// 失败场景：无（单条数据异常会跳过对应字段而非整体失败）。
// 副作用：无。
func buildItems(dataset *rawDataSet) []repository.BangumiItem {
	if dataset == nil {
		return nil
	}
	now := time.Now().Format(timeLayout)
	items := make([]repository.BangumiItem, 0, len(dataset.Items))
	for _, raw := range dataset.Items {
		title := strings.TrimSpace(raw.Title)
		if title == "" {
			continue
		}

		links := make([]repository.BangumiSiteLink, 0, len(raw.Sites))
		siteIDs := map[string]string{}
		for _, site := range raw.Sites {
			code := strings.TrimSpace(site.Site)
			id := strings.TrimSpace(site.ID)
			if code == "" || id == "" {
				continue
			}
			siteIDs[code] = id
			meta := dataset.SiteMeta[code]
			links = append(links, repository.BangumiSiteLink{
				Site:  code,
				Title: meta.Title,
				Type:  meta.Type,
				ID:    id,
				URL:   buildSiteURL(meta.URLTemplate, id),
			})
		}

		translate := raw.TitleTranslate
		if translate == nil {
			translate = map[string][]string{}
		}
		translateJSON, err := json.Marshal(translate)
		if err != nil {
			translateJSON = []byte("{}")
		}
		sitesJSON, err := json.Marshal(links)
		if err != nil {
			sitesJSON = []byte("[]")
		}

		items = append(items, repository.BangumiItem{
			BangumiID:      siteIDs["bangumi"],
			Title:          title,
			TitleZH:        pickChineseTitle(translate),
			TitleTransJSON: string(translateJSON),
			SitesJSON:      string(sitesJSON),
			ItemType:       strings.TrimSpace(raw.Type),
			Lang:           strings.TrimSpace(raw.Lang),
			OfficialSite:   strings.TrimSpace(raw.OfficialSite),
			BeginAt:        strings.TrimSpace(raw.Begin),
			EndAt:          strings.TrimSpace(raw.End),
			BeginTimestamp: parseTimestamp(raw.Begin),
			Broadcast:      strings.TrimSpace(raw.Broadcast),
			Comment:        strings.TrimSpace(raw.Comment),
			TmdbID:         siteIDs["tmdb"],
			MalID:          siteIDs["mal"],
			AnidbID:        siteIDs["anidb"],
			AniListID:      siteIDs["aniList"],
			CreatedAt:      now,
			UpdatedAt:      now,
		})
	}
	return items
}

// pickChineseTitle 从译名表中挑选中文标题：优先简体，其次繁体。
func pickChineseTitle(translate map[string][]string) string {
	for _, key := range []string{"zh-Hans", "zh-Hant"} {
		for _, candidate := range translate[key] {
			if trimmed := strings.TrimSpace(candidate); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

// buildSiteURL 用站点 urlTemplate 与条目 ID 拼出详情链接，模板缺失时返回空串。
func buildSiteURL(template string, id string) string {
	trimmed := strings.TrimSpace(template)
	if trimmed == "" || strings.TrimSpace(id) == "" {
		return ""
	}
	if strings.Contains(trimmed, "{{id}}") {
		return strings.ReplaceAll(trimmed, "{{id}}", id)
	}
	return trimmed + id
}

// parseTimestamp 把 ISO 时间字符串转为 Unix 秒，无法解析时返回 0。
func parseTimestamp(raw string) int64 {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	if parsed, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
		return parsed.Unix()
	}
	return 0
}

// parseTime 解析库内存储的时间字符串，失败时返回 false。
func parseTime(raw string) (time.Time, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{timeLayout, time.RFC3339} {
		if parsed, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// formatNextSync 计算下次计划同步时间（当前时间 + 24 小时）。
func formatNextSync(from time.Time) string {
	return from.Add(syncInterval).Format(timeLayout)
}

// extractDataVersion 从数据源地址中提取版本号（形如 bangumi-data@0.3.228），无法提取时返回 "latest"。
func extractDataVersion(url string) string {
	const marker = "bangumi-data@"
	index := strings.Index(url, marker)
	if index < 0 {
		return "latest"
	}
	rest := url[index+len(marker):]
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		rest = rest[:cut]
	}
	if strings.TrimSpace(rest) == "" {
		return "latest"
	}
	return rest
}
