package downloaderclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
)

const (
	torrentFetchLogModule = "下载器-种子同步"
	// fetchTorrentsHardTimeout 是 FetchTorrents 的整体硬超时。
	// 截图修复、媒体修复等链路会同步调用它，下载器不可达或种子量极大时没有上限会把整条抓取流程挂住
	// （现场表现：批量获取任务一直「进行中」，日志停在截图阶段之后再无输出）。
	fetchTorrentsHardTimeout = 5 * time.Minute
	// qbCommentBackfillTimeout 限制 qB comment 回填阶段的总耗时。
	// qB 的 torrents/info 不返回 comment，只能逐条 torrents/properties 补齐；上万条种子时这一步能跑几十分钟。
	qbCommentBackfillTimeout = 30 * time.Second
	// qbCommentBackfillRequestTimeout 是单条 properties 请求的超时，远小于 client 默认 90s。
	qbCommentBackfillRequestTimeout = 15 * time.Second
	// torrentSnapshotCacheTTL 是「为找路径而拉全量种子」的短期缓存时长。
	// 盒子代理模式下 1125 条种子实测要 110s+，截图链路一个种子会问两三次，没有缓存就是几分钟白等。
	torrentSnapshotCacheTTL = 10 * time.Minute
	// torrentSnapshotCachedFetchTimeout 是缓存未命中时允许的等待上限（代理实测 110-120s）。
	torrentSnapshotCachedFetchTimeout = 3 * time.Minute
)

// torrentSnapshotCacheEntry 保存某个下载器最近一次种子快照列表。
type torrentSnapshotCacheEntry struct {
	items   []TorrentSnapshot
	fetched time.Time
}

var (
	torrentSnapshotCacheMu sync.Mutex
	torrentSnapshotCache   = map[string]torrentSnapshotCacheEntry{}
)

// torrentSnapshotCacheKey 生成缓存键：同一台下载器（含代理模式）才算同一份列表。
func torrentSnapshotCacheKey(d Downloader) string {
	return strings.Join([]string{d.ID, strings.TrimSpace(d.Host), fmt.Sprintf("proxy=%t", d.UseProxy)}, "|")
}

// FetchTorrentsCached 返回下载器种子快照列表，命中短期缓存时不发起请求。
// 参数/返回：供「只为解析某个种子的 save_path」的链路（截图修复、媒体修复）使用；缓存命中返回副本。
// 失败场景：缓存未命中且拉取失败/超时时返回错误，调用方降级即可。
// 副作用：未命中时会向下载器或盒子代理发起一次全量拉取。
func (d Downloader) FetchTorrentsCached() ([]TorrentSnapshot, error) {
	key := torrentSnapshotCacheKey(d)

	torrentSnapshotCacheMu.Lock()
	entry, ok := torrentSnapshotCache[key]
	torrentSnapshotCacheMu.Unlock()
	if ok && time.Since(entry.fetched) < torrentSnapshotCacheTTL {
		logx.Infof(torrentFetchLogModule, "命中种子快照缓存 id=%s count=%d age_ms=%d", d.ID, len(entry.items), time.Since(entry.fetched).Milliseconds())
		return entry.items, nil
	}

	items, err := d.FetchTorrentsWithin(torrentSnapshotCachedFetchTimeout)
	if err != nil {
		return nil, err
	}
	torrentSnapshotCacheMu.Lock()
	torrentSnapshotCache[key] = torrentSnapshotCacheEntry{items: items, fetched: time.Now()}
	torrentSnapshotCacheMu.Unlock()
	return items, nil
}

// InvalidateTorrentSnapshotCache 清空种子快照缓存（同步/增删种子后用）。
// 参数/返回：无入参，无返回。
// 失败场景：不适用。
// 副作用：下次 FetchTorrentsCached 会重新拉取。
func InvalidateTorrentSnapshotCache() {
	torrentSnapshotCacheMu.Lock()
	torrentSnapshotCache = map[string]torrentSnapshotCacheEntry{}
	torrentSnapshotCacheMu.Unlock()
}

// TorrentSnapshot 表示从下载器读取的一条标准化种子快照。
// 参数/返回：用于跨下载器统一字段，供上层服务写入 torrents/torrent_upload_stats。
// 失败场景：无直接失败场景，具体失败由 FetchTorrents 返回。
// 副作用：无副作用，仅承载数据。
type TorrentSnapshot struct {
	Hash        string
	Name        string
	SavePath    string
	ContentPath string
	Size        int64
	Progress    float64
	State       string
	Trackers    []string
	Comment     string
	Group       string
	Seeders     int64
	Uploaded    int64
}

// FetchTorrents 拉取下载器当前全部种子并归一化为统一结构。
// 参数/返回：根据下载器类型自动走 qBittorrent 或 Transmission，返回种子快照列表。
// 失败场景：连接失败、认证失败、RPC 异常、响应解析失败时返回错误；超过 fetchTorrentsHardTimeout 直接返回超时错误。
// 副作用：会向下载器发起网络请求；超时返回后底层请求可能仍在收尾（HTTP 各请求自身有超时，会自行结束）。
func (d Downloader) FetchTorrents() ([]TorrentSnapshot, error) {
	return d.FetchTorrentsWithin(fetchTorrentsHardTimeout)
}

// FetchTorrentsWithin 与 FetchTorrents 相同，但可指定本次拉取的超时上限。
// 参数/返回：timeout<=0 时回退 fetchTorrentsHardTimeout；返回种子快照列表与错误。
// 失败场景：超时返回「拉取下载器种子超时」错误，调用方可据此降级而不是无限等待。
// 副作用：同 FetchTorrents。
func (d Downloader) FetchTorrentsWithin(timeout time.Duration) ([]TorrentSnapshot, error) {
	if timeout <= 0 {
		timeout = fetchTorrentsHardTimeout
	}
	startedAt := time.Now()
	logx.Infof(torrentFetchLogModule, "开始拉取下载器种子 type=%s host=%s use_proxy=%t id=%s timeout=%s", d.Type, d.Host, d.UseProxy, d.ID, timeout)

	type fetchOutcome struct {
		items []TorrentSnapshot
		err   error
	}
	done := make(chan fetchOutcome, 1)
	go func() {
		items, err := d.fetchTorrentsOnce()
		done <- fetchOutcome{items: items, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-done:
		if result.err != nil {
			logx.Warnf(torrentFetchLogModule, "拉取下载器种子失败 type=%s host=%s elapsed_ms=%d err=%v", d.Type, d.Host, time.Since(startedAt).Milliseconds(), result.err)
			return nil, result.err
		}
		logx.Infof(torrentFetchLogModule, "拉取下载器种子完成 type=%s host=%s count=%d elapsed_ms=%d", d.Type, d.Host, len(result.items), time.Since(startedAt).Milliseconds())
		return result.items, nil
	case <-timer.C:
		logx.Warnf(torrentFetchLogModule, "拉取下载器种子超时 type=%s host=%s timeout=%s", d.Type, d.Host, timeout)
		return nil, fmt.Errorf("拉取下载器种子超时（%s）: type=%s host=%s", timeout, d.Type, d.Host)
	}
}

// fetchTorrentsOnce 按下载器类型执行一次实际抓取。
// 参数/返回：返回归一化后的种子快照列表与错误。
// 失败场景：类型不支持或底层请求失败时返回错误。
// 副作用：会向下载器或盒子代理发起网络请求。
func (d Downloader) fetchTorrentsOnce() ([]TorrentSnapshot, error) {
	if d.Type == "qbittorrent" && d.UseProxy {
		return d.fetchQBTorrentsByProxy()
	}

	switch d.Type {
	case "qbittorrent":
		return d.fetchQBTorrents()
	case "transmission":
		return d.fetchTransmissionTorrents()
	default:
		return nil, fmt.Errorf("不支持的下载器类型: %s", d.Type)
	}
}

// fetchQBTorrentsByProxy 通过盒子代理拉取 qBittorrent 全量种子。
// 参数/返回：使用配置中的 host/use_proxy/proxy_port 生成代理请求，成功返回标准化快照列表。
// 失败场景：代理地址解析失败、请求失败、HTTP 返回异常或响应解析失败时返回错误。
// 副作用：会向代理服务发起 HTTP 请求。
func (d Downloader) fetchQBTorrentsByProxy() ([]TorrentSnapshot, error) {
	proxyPort := d.ProxyPort
	if proxyPort <= 0 {
		proxyPort = 9090
	}

	parsedURL, err := parseHostURL(d.Host)
	if err != nil {
		return nil, fmt.Errorf("解析 host 失败: %w", err)
	}
	proxyIP := strings.TrimSpace(parsedURL.Hostname())
	if proxyIP == "" {
		proxyIP = extractHostFallback(d.Host)
	}
	if proxyIP == "" {
		return nil, fmt.Errorf("无法解析代理地址: host=%s", d.Host)
	}

	originPort := strings.TrimSpace(parsedURL.Port())
	if originPort == "" {
		originPort = "8080"
	}
	requestPayload := map[string]any{
		"downloaders": []map[string]any{
			{
				"id":       d.ID,
				"type":     d.Type,
				"host":     "http://127.0.0.1:" + originPort,
				"username": d.Username,
				"password": d.Password,
			},
		},
		"include_comment":  true,
		"include_trackers": true,
	}
	payloadBytes, err := json.Marshal(requestPayload)
	if err != nil {
		return nil, fmt.Errorf("构造代理请求失败: %w", err)
	}

	proxyURL := fmt.Sprintf("http://%s:%d/api/torrents/all", proxyIP, proxyPort)
	request, err := http.NewRequest(http.MethodPost, proxyURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("创建代理请求失败: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("代理请求失败: %w", err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("代理返回异常: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(body)))
	}

	rows := make([]map[string]any, 0)
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("解析代理响应失败: %w", err)
	}

	result := make([]TorrentSnapshot, 0, len(rows))
	for _, row := range rows {
		hash := strings.TrimSpace(toString(row["hash"], ""))
		name := strings.TrimSpace(toString(row["name"], ""))
		if hash == "" || name == "" {
			continue
		}

		trackers := extractProxyTrackers(row["trackers"])
		group := strings.TrimSpace(toString(row["category"], ""))
		if group == "" {
			group = firstCSVItem(toString(row["tags"], ""))
		}

		progress := normalizeProgressPercent(row["progress"])
		seeders := toInt64Any(row["seeders"])
		if seeders <= 0 {
			seeders = toInt64Any(row["num_seeds"])
		}

		result = append(result, TorrentSnapshot{
			Hash:        hash,
			Name:        name,
			SavePath:    strings.TrimSpace(toString(row["save_path"], "")),
			ContentPath: strings.TrimSpace(toString(row["content_path"], "")),
			Size:        toInt64Any(row["size"]),
			Progress:    progress,
			State:       formatTorrentState(toString(row["state"], "")),
			Trackers:    trackers,
			Comment:     strings.TrimSpace(toString(row["comment"], "")),
			Group:       group,
			Seeders:     seeders,
			Uploaded:    toInt64Any(row["uploaded"]),
		})
	}
	return result, nil
}

func (d Downloader) fetchQBTorrents() ([]TorrentSnapshot, error) {
	client, err := newQBClient(d)
	if err != nil {
		return nil, err
	}
	if err := client.Login(); err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("filter", "all")
	body, err := client.Get("torrents/info", params)
	if err != nil {
		return nil, err
	}

	rows := make([]map[string]any, 0)
	if err := json.Unmarshal(body, &rows); err != nil {
		return nil, fmt.Errorf("qB 种子列表解析失败: %w", err)
	}

	result := make([]TorrentSnapshot, 0, len(rows))
	for _, row := range rows {
		hash := strings.TrimSpace(toString(row["hash"], ""))
		name := strings.TrimSpace(toString(row["name"], ""))
		if hash == "" || name == "" {
			continue
		}

		trackers := make([]string, 0, 2)
		if tracker := strings.TrimSpace(toString(row["tracker"], "")); tracker != "" {
			trackers = append(trackers, tracker)
		}

		group := strings.TrimSpace(toString(row["category"], ""))
		if group == "" {
			group = firstCSVItem(toString(row["tags"], ""))
		}

		progress := normalizeProgressPercent(row["progress"])
		seeders := toInt64Any(row["num_seeds"])
		if seeders <= 0 {
			seeders = toInt64Any(row["num_complete"])
		}

		result = append(result, TorrentSnapshot{
			Hash:        hash,
			Name:        name,
			SavePath:    strings.TrimSpace(toString(row["save_path"], "")),
			ContentPath: strings.TrimSpace(toString(row["content_path"], "")),
			Size:        toInt64Any(row["size"]),
			Progress:    progress,
			State:       formatTorrentState(toString(row["state"], "")),
			Trackers:    trackers,
			Comment:     strings.TrimSpace(toString(row["comment"], "")),
			Group:       group,
			Seeders:     seeders,
			Uploaded:    toInt64Any(row["uploaded"]),
		})
	}

	// qBittorrent 的 torrents/info 列表接口不返回 comment（4.6.x 实测 include_comment 亦无效），
	// 但源站详情地址（details.php?id=...）正是藏在种子的 comment 里。这里对 comment 缺失的
	// 种子逐个调用 torrents/properties 补齐，避免非代理同步解析不到详情地址。
	fillQBComments(client, result)

	return result, nil
}

// fillQBComments 为 comment 为空的种子逐个调用 torrents/properties 补齐 comment。
// 参数/返回：client 为已登录的 qB 客户端；snapshots 为待补全的快照切片（原地修改）。
// 失败场景：单个种子 properties 请求失败或超出 qbCommentBackfillTimeout 时间预算时跳过该条，不影响整体同步。
// 副作用：会向 qB 发起至多 len(snapshots) 次 properties 请求（受并发上限与时间预算约束）。
func fillQBComments(client *qbClient, snapshots []TorrentSnapshot) {
	if client == nil {
		return
	}
	pending := 0
	for i := range snapshots {
		if snapshots[i].Comment == "" && snapshots[i].Hash != "" {
			pending++
		}
	}
	if pending == 0 {
		return
	}

	const concurrency = 8
	startedAt := time.Now()
	deadline := startedAt.Add(qbCommentBackfillTimeout)
	logx.Infof(torrentFetchLogModule, "开始回填 qB 种子 comment total=%d pending=%d concurrency=%d budget=%s", len(snapshots), pending, concurrency, qbCommentBackfillTimeout)

	// properties 只是补一个 comment 字段，用 15s 独立超时，避免默认 90s 把预算吃光。
	propsClient := *client
	if client.client != nil {
		propsClient.client = &http.Client{Jar: client.client.Jar, Timeout: qbCommentBackfillRequestTimeout}
	}

	var filled int64
	var skipped int64
	var failed int64
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for i := range snapshots {
		if snapshots[i].Comment != "" {
			continue
		}
		hash := snapshots[i].Hash
		if hash == "" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, h string) {
			defer wg.Done()
			defer func() { <-sem }()
			if time.Now().After(deadline) {
				atomic.AddInt64(&skipped, 1)
				return
			}
			params := url.Values{}
			params.Set("hash", h)
			body, err := propsClient.Get("torrents/properties", params)
			if err != nil {
				atomic.AddInt64(&failed, 1)
				return
			}
			var props map[string]any
			if err := json.Unmarshal(body, &props); err != nil {
				atomic.AddInt64(&failed, 1)
				return
			}
			if comment := strings.TrimSpace(toString(props["comment"], "")); comment != "" {
				snapshots[idx].Comment = comment
				atomic.AddInt64(&filled, 1)
			}
		}(i, hash)
	}
	wg.Wait()

	logx.Infof(
		torrentFetchLogModule,
		"qB comment 回填结束 total=%d pending=%d filled=%d skipped_budget=%d failed=%d elapsed_ms=%d",
		len(snapshots),
		pending,
		atomic.LoadInt64(&filled),
		atomic.LoadInt64(&skipped),
		atomic.LoadInt64(&failed),
		time.Since(startedAt).Milliseconds(),
	)
}

func (d Downloader) fetchTransmissionTorrents() ([]TorrentSnapshot, error) {
	client := newTransmissionClient(d)
	response, err := client.Call("torrent-get", map[string]any{
		"fields": []string{
			"hashString",
			"name",
			"downloadDir",
			"totalSize",
			"sizeWhenDone",
			"status",
			"comment",
			"trackers",
			"trackerStats",
			"percentDone",
			"uploadedEver",
			"peersGettingFromUs",
			"labels",
		},
	})
	if err != nil {
		return nil, err
	}

	args := toMap(response["arguments"])
	torrents := toSlice(args["torrents"])
	result := make([]TorrentSnapshot, 0, len(torrents))

	for _, raw := range torrents {
		row := toMap(raw)
		hash := strings.TrimSpace(toString(row["hashString"], ""))
		name := strings.TrimSpace(toString(row["name"], ""))
		if hash == "" || name == "" {
			continue
		}

		size := toInt64Any(row["sizeWhenDone"])
		if size <= 0 {
			size = toInt64Any(row["totalSize"])
		}

		trackers := extractTransmissionTrackers(row)
		group := firstLabel(row["labels"])
		statusText := transmissionStatusText(int(toInt64Any(row["status"])))

		result = append(result, TorrentSnapshot{
			Hash:     hash,
			Name:     name,
			SavePath: strings.TrimSpace(toString(row["downloadDir"], "")),
			Size:     size,
			Progress: normalizeProgressPercent(row["percentDone"]),
			State:    formatTorrentState(statusText),
			Trackers: trackers,
			Comment:  strings.TrimSpace(toString(row["comment"], "")),
			Group:    group,
			Seeders:  toInt64Any(row["peersGettingFromUs"]),
			Uploaded: toInt64Any(row["uploadedEver"]),
		})
	}

	return result, nil
}

func firstCSVItem(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, part := range strings.Split(trimmed, ",") {
		item := strings.TrimSpace(part)
		if item != "" {
			return item
		}
	}
	return ""
}

func firstLabel(raw any) string {
	switch typed := raw.(type) {
	case []string:
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				return trimmed
			}
		}
	case []any:
		for _, item := range typed {
			trimmed := strings.TrimSpace(toString(item, ""))
			if trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}

func extractTransmissionTrackers(row map[string]any) []string {
	result := make([]string, 0, 4)
	seen := map[string]struct{}{}

	appendTracker := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	for _, item := range toSlice(row["trackers"]) {
		tracker := toMap(item)
		appendTracker(toString(tracker["announce"], ""))
		appendTracker(toString(tracker["host"], ""))
	}
	for _, item := range toSlice(row["trackerStats"]) {
		tracker := toMap(item)
		appendTracker(toString(tracker["announce"], ""))
		appendTracker(toString(tracker["host"], ""))
	}

	return result
}

func extractProxyTrackers(raw any) []string {
	result := make([]string, 0, 4)
	seen := map[string]struct{}{}

	appendTracker := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}

	for _, item := range toSlice(raw) {
		tracker := toMap(item)
		appendTracker(toString(tracker["url"], ""))
		appendTracker(toString(tracker["announce"], ""))
		appendTracker(toString(tracker["host"], ""))
	}
	return result
}

func normalizeProgressPercent(value any) float64 {
	raw := toFloat64(value)
	if raw <= 0 {
		return 0
	}
	if raw <= 1 {
		raw *= 100
	}
	if raw < 0 {
		return 0
	}
	if raw > 100 {
		return 100
	}
	return raw
}

func toFloat64(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, err := typed.Float64()
		if err == nil {
			return parsed
		}
		return 0
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0
		}
		parsed, err := json.Number(trimmed).Float64()
		if err == nil {
			return parsed
		}
		return 0
	default:
		return 0
	}
}

func transmissionStatusText(status int) string {
	switch status {
	case 0:
		return "stopped"
	case 1:
		return "check_wait"
	case 2:
		return "checking"
	case 3:
		return "queued"
	case 4:
		return "downloading"
	case 5:
		return "queued"
	case 6:
		return "seeding"
	default:
		return fmt.Sprintf("status_%d", status)
	}
}

func formatTorrentState(state string) string {
	stateLower := strings.ToLower(strings.TrimSpace(state))
	stateMap := map[string]string{
		"downloading":   "下载中",
		"forceddl":      "下载中",
		"uploading":     "做种中",
		"stalledup":     "做种中",
		"forcedup":      "做种中",
		"seed":          "做种中",
		"seeding":       "做种中",
		"paused":        "暂停",
		"stopped":       "暂停",
		"stalleddl":     "暂停",
		"check_wait":    "校验中",
		"checking":      "校验中",
		"check":         "校验中",
		"error":         "错误",
		"missingfiles":  "文件丢失",
		"moving":        "移动中",
		"allocating":    "分配空间",
		"queued":        "队列",
		"queuedup":      "队列",
		"queueddl":      "队列",
		"meta":          "下载中",
		"stalled":       "暂停",
		"upnp":          "暂停",
		"downloadingup": "下载中",
	}
	for key, value := range stateMap {
		if strings.Contains(stateLower, key) {
			return value
		}
	}
	if strings.TrimSpace(state) == "" {
		return "未知"
	}
	runes := []rune(state)
	if len(runes) == 0 {
		return "未知"
	}
	runes[0] = []rune(strings.ToUpper(string(runes[0])))[0]
	return string(runes)
}
