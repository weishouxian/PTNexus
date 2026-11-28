package reseed

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/iyuu"
)

const (
	// supportedTTL 是「已确知 IYUU 支持站点」的内存缓存时长。
	supportedTTL = time.Hour
	// supportedRetryTTL 是取不到站点列表后的重试间隔，避免每次开页面都卡在网络上。
	supportedRetryTTL = 5 * time.Minute
	// supportedFetchTimeout 限制状态接口里请求 IYUU 站点列表的时长。
	supportedFetchTimeout = 10 * time.Second
)

// 默认辅种标签存放在全局配置里（不是浏览器本地），这样辅种页保存一次后，
// 「一种多站」列表里的一键辅种也会带上同一组标签，两个入口口径一致。
const (
	defaultTagsConfigKey = "reseed_default_tags"
	maxDefaultTags       = 20
	maxDefaultTagRunes   = 64
)

// Status 返回辅种页初始化所需的状态：Token 是否配置、是否有任务在跑、可选站点与下载器。
// 站点列表只包含已设置 Cookie 或 Passkey 的站点（site_hidden 回传被剔除的数量），
// 其中再按 IYUU 是否收录标记 can_reseed / disabled_reason。
// 参数/返回：ctx 用于 IYUU 站点列表请求的可取消；返回状态对象。
// 失败场景：数据库查询失败时对应字段为空；IYUU 站点列表取不到时标记为「未知」。
// 副作用：只读配置与数据库，必要时发起一次 IYUU 站点列表请求（带内存缓存）。
func (s *Service) Status(ctx context.Context) map[string]any {
	cfg := s.cfg.Get()
	token := strings.TrimSpace(toStringAny(cfg["iyuu_token"], ""))

	allSites := make([]repository.ReseedSiteRow, 0)
	if list, err := s.repo.ListSites(); err == nil {
		allSites = list
	}

	// 目标站只展示「已设置 Cookie 或 Passkey」的站点：没有凭据根本下载不到种子，
	// 留在下拉里只会让用户选中后必然失败，直接在列表里剔除（另把数量回传给页面做说明）。
	candidateSites, hiddenNoCredential := filterCredentialSites(allSites)

	supportedNames, supportedKnown := s.resolveSupportedNames(ctx, token, candidateSites)

	sites := make([]repository.ReseedSiteRow, 0, len(candidateSites))
	enabledCount := 0
	for _, site := range candidateSites {
		reason := ""
		if supportedKnown {
			if _, ok := supportedNames[site.Nickname]; !ok {
				reason = "IYUU 未收录该站：没有跨站辅种数据可用"
			}
		}
		site.DisabledReason = reason
		site.CanReseed = reason == ""
		if site.CanReseed {
			enabledCount++
		}
		sites = append(sites, site)
	}

	downloaders := make([]map[string]any, 0)
	for _, item := range toSliceAny(cfg["downloaders"]) {
		entry := toMapAny(item)
		id := strings.TrimSpace(toStringAny(entry["id"], ""))
		if id == "" {
			continue
		}
		downloaders = append(downloaders, map[string]any{
			"id":      id,
			"name":    strings.TrimSpace(toStringAny(entry["name"], id)),
			"type":    strings.TrimSpace(toStringAny(entry["type"], "")),
			"enabled": toBoolAny(entry["enabled"], true),
		})
	}

	// 页面刷新后如果任务还在后台跑，把 task_id 带回去让前端接着轮询进度。
	currentTaskID := ""
	s.mu.RLock()
	for id, item := range s.tasks {
		if item.IsRunning {
			currentTaskID = id
			break
		}
	}
	s.mu.RUnlock()

	return map[string]any{
		"token_configured":     token != "",
		"sites":                sites,
		"site_count":           enabledCount,
		"site_total":           len(sites),
		"site_hidden":          hiddenNoCredential,
		"iyuu_site_list_known": supportedKnown,
		"default_tags":         s.DefaultTags(),
		"downloaders":          downloaders,
		"running":              s.running.Load(),
		"current_task_id":      currentTaskID,
	}
}

// DefaultTags 读取全局配置里的默认辅种标签。
// 参数/返回：无参数；返回清洗后的标签列表（去空/去重/截断），未配置时为空。
// 标签里允许保留 `{站点名称}` 占位符，执行时按目标站展开成 `站点/财神`。
// 失败场景：无（配置缺失按空处理）。
// 副作用：只读配置。
func (s *Service) DefaultTags() []string {
	cfg := s.cfg.Get()
	return normalizeTagList(cfg[defaultTagsConfigKey])
}

// SetDefaultTags 保存默认辅种标签，传空列表表示清空。
// 参数/返回：tags 为前端提交的标签；返回保存失败原因。
// 失败场景：配置写入数据库/文件失败。
// 副作用：整份配置落库（与设置中心其他保存动作走同一路径）。
func (s *Service) SetDefaultTags(tags []string) error {
	cfg := s.cfg.Get()
	cfg[defaultTagsConfigKey] = normalizeTagList(tags)
	return s.cfg.Save(cfg)
}

// normalizeTagList 清洗标签列表：去空白、去重（按小写）、限长与限量。
// 参数/返回：value 为配置值或前端入参（[]string/[]any）；返回可直接写入下载器的标签。
// 失败场景：不适用（不认识的类型按空列表处理）。
// 副作用：无。
func normalizeTagList(value any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 4)
	for _, item := range toSliceAny(value) {
		text := strings.TrimSpace(toStringAny(item, ""))
		if text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > maxDefaultTagRunes {
			text = string(runes[:maxDefaultTagRunes])
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, text)
		if len(result) >= maxDefaultTags {
			break
		}
	}
	return result
}

// filterCredentialSites 只保留已配置 Cookie 或 Passkey 的站点。
// 参数/返回：sites 为 sites 表全量站点；返回可作目标站的站点与被剔除（无凭据）的数量。
// 失败场景：无。
// 副作用：无。
func filterCredentialSites(sites []repository.ReseedSiteRow) ([]repository.ReseedSiteRow, int) {
	kept := make([]repository.ReseedSiteRow, 0, len(sites))
	hidden := 0
	for _, site := range sites {
		if !site.HasCookie && !site.HasPasskey {
			hidden++
			continue
		}
		kept = append(kept, site)
	}
	return kept, hidden
}

// resolveSupportedNames 取得「IYUU 支持且本地存在」的站点昵称集合。
// 参数/返回：token 为 IYUU Token；sites 为本地站点列表；返回昵称集合与是否已确知（取不到时为 false）。
// 失败场景：IYUU 接口异常/超时、Token 未配置且无缓存 —— 此时返回 unknown，页面只按凭据置灰。
// 副作用：可能发起一次 IYUU 支持站点请求；结果在内存缓存（已知 1 小时、未知 5 分钟后重试）。
func (s *Service) resolveSupportedNames(ctx context.Context, token string, sites []repository.ReseedSiteRow) (map[string]struct{}, bool) {
	s.supportedMu.Lock()
	ttl := supportedTTL
	if !s.supportedKnown {
		ttl = supportedRetryTTL
	}
	if !s.supportedCheckedAt.IsZero() && time.Since(s.supportedCheckedAt) < ttl {
		names, known := s.supportedNames, s.supportedKnown
		s.supportedMu.Unlock()
		return names, known
	}
	s.supportedMu.Unlock()

	localSites := make([]string, 0, len(sites))
	for _, site := range sites {
		if nickname := strings.TrimSpace(site.Nickname); nickname != "" {
			localSites = append(localSites, nickname)
		}
	}
	fieldToNickname, _ := s.torrentRepo.SiteFieldToNicknameMap()

	if supported, ok := iyuu.LoadLatestSupportedSites(); ok {
		names := iyuu.MatchedSiteNicknames(supported, localSites, fieldToNickname)
		s.cacheSupportedNames(names, true)
		return names, true
	}

	if token == "" {
		s.cacheSupportedNames(nil, false)
		return nil, false
	}

	client := iyuu.NewClient()
	// 页面加载不该被 IYUU 的 5s 限流拖住：单独给短超时，失败就退化为只按凭据判断。
	reqCtx, cancel := context.WithTimeout(ctx, supportedFetchTimeout)
	defer cancel()
	supported, err := client.GetSupportedSites(reqCtx, token)
	if err != nil {
		logx.Warnf(reseedLogModule, "读取 IYUU 支持站点失败，本次只按凭据判断可否辅种 err=%v", err)
		s.cacheSupportedNames(nil, false)
		return nil, false
	}
	names := iyuu.MatchedSiteNicknames(supported, localSites, fieldToNickname)
	s.cacheSupportedNames(names, true)
	return names, true
}

// cacheSupportedNames 写入支持站点昵称缓存。
// 参数/返回：names 为昵称集合；known 表示这份结果是否确知。
// 失败场景：无。
// 副作用：更新内存缓存时间戳。
func (s *Service) cacheSupportedNames(names map[string]struct{}, known bool) {
	s.supportedMu.Lock()
	defer s.supportedMu.Unlock()
	s.supportedNames = names
	s.supportedKnown = known
	s.supportedCheckedAt = time.Now()
}

// DefaultPaths 返回 torrents 表中出现过的保存路径（去重排序），供路径筛选下拉。
// 参数/返回：无参数；返回路径列表。
// 失败场景：数据库查询失败时返回空列表。
// 副作用：只读数据库。
func (s *Service) DefaultPaths() []string {
	rows, err := s.torrentRepo.ListTorrents(false)
	if err != nil {
		return []string{}
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, 16)
	for _, row := range rows {
		path := strings.TrimSpace(row.SavePath)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

// Run 启动一次指定站点辅种任务（异步执行，立即返回 task_id）。
// 参数/返回：payload 为前端入参；返回 success/task_id 或错误说明与 HTTP 状态码。
// 失败场景：未配置 IYUU Token、未选择站点、已有任务在跑、候选为空时返回 400/409。
// 副作用：创建内存任务并启动后台 goroutine。
func (s *Service) Run(payload map[string]any) (map[string]any, int) {
	cfg := s.cfg.Get()
	token := strings.TrimSpace(toStringAny(cfg["iyuu_token"], ""))
	if token == "" {
		return map[string]any{
			"success": false,
			"message": "未配置 IYUU Token，请先到「设置 → IYUU设置」填写后再辅种",
			"code":    "iyuu_token_missing",
		}, 400
	}

	params := Params{
		Sites:        normalizeStringList(payload["sites"]),
		Paths:        normalizeStringList(payload["paths"]),
		DownloaderID: strings.TrimSpace(toStringAny(payload["downloader_id"], "")),
		Limit:        toIntAny(payload["limit"], 200),
		SkipChecking: toBoolAny(payload["skip_checking"], true),
		Hashes:       normalizeHashList(payload["hashes"]),
		Tags:         normalizeStringList(payload["tags"]),
		TagBySite:    toBoolAny(payload["tag_by_site"], false),
	}
	if len(params.Sites) == 0 {
		return map[string]any{"success": false, "message": "请至少选择一个目标站点"}, 400
	}
	// 调用方没带 tags 字段（旧页面、脚本、列表页以外的新入口）时沿用页面保存的默认标签；
	// 显式传空数组仍表示「本次不打标签」，不会被默认值覆盖。
	if _, provided := payload["tags"]; !provided {
		params.Tags = s.DefaultTags()
	}
	// 目标站必须有 Cookie/Passkey 才能登录站点下载种子：前端已过滤，这里再兜一次，
	// 避免旧页面/直接调接口时把无凭据站点送到执行器里逐条失败。
	// 同时把站点顺序重排成「站点管理」页的顺序：任务消息、进度里的站点顺序与页面展示一致。
	orderedSites, invalidSites := s.prepareTargetSites(params.Sites)
	params.Sites = orderedSites
	if len(invalidSites) > 0 {
		return map[string]any{
			"success": false,
			"message": "以下站点未设置 Cookie / Passkey，无法辅种：" + strings.Join(invalidSites, "、"),
			"code":    "site_credential_missing",
		}, 400
	}
	if params.Limit <= 0 {
		params.Limit = 200
	}
	if params.Limit > 2000 {
		params.Limit = 2000
	}

	if !s.running.CompareAndSwap(false, true) {
		return map[string]any{"success": false, "message": "已有辅种任务在执行中，请等待完成后再试"}, 409
	}

	groups, err := s.buildCandidates(params)
	if err != nil {
		s.running.Store(false)
		return map[string]any{"success": false, "message": "构建待辅种列表失败: " + err.Error()}, 500
	}
	if len(groups) == 0 {
		s.running.Store(false)
		message := "没有符合条件的种子（需已做种、体积大于 198MB，且匹配所选路径/下载器）"
		if len(params.Hashes) > 0 {
			message = "指定的种子没有可辅种记录（可能已不在做种状态、缺少 infohash 或未同步到本地库）"
		} else if params.DownloaderID != "" {
			message = fmt.Sprintf(
				"下载器「%s」中没有符合条件的种子（需已做种、体积大于 198MB 并匹配所选路径）；如需换下载器请在页面右上角切换",
				resolveDownloaderName(cfg, params.DownloaderID),
			)
		}
		return map[string]any{"success": false, "message": message}, 400
	}

	task := s.createTask(len(groups), params)
	go s.execute(task.TaskID, token, params, groups)

	scope := ""
	downloaderScope := "全部下载器"
	if len(params.Hashes) > 0 {
		scope = "（指定种子试跑）"
	}
	if params.DownloaderID != "" {
		downloaderScope = "下载器「" + resolveDownloaderName(cfg, params.DownloaderID) + "」"
	}
	return map[string]any{
		"success": true,
		"task_id": task.TaskID,
		"total":   len(groups),
		"message": fmt.Sprintf("已启动辅种任务%s：%d 个种子组（范围：%s），目标站点 %s", scope, len(groups), downloaderScope, strings.Join(params.Sites, "、")),
	}, 200
}

// prepareTargetSites 按「站点管理」页的顺序重排目标站，并挑出未配置 Cookie / Passkey 的站点。
// 参数/返回：sites 为前端选择的目标站昵称；返回按站点管理顺序排好的列表与缺少凭据的站点。
// 排序规则与站点管理页一致：sort_order 升序、未排序（0/空）排最后、同序号按昵称；库里没有的站点保持原相对顺序排最后。
// 失败场景：数据库读取失败或站点表为空时原样返回站点列表、不额外拦截。
// 副作用：只读数据库。
func (s *Service) prepareTargetSites(sites []string) ([]string, []string) {
	list, err := s.repo.ListSites()
	if err != nil || len(list) == 0 {
		return sites, nil
	}
	rank := make(map[string]int, len(list))
	credential := make(map[string]struct{}, len(list))
	for index, site := range list {
		key := strings.ToLower(strings.TrimSpace(site.Nickname))
		if key == "" {
			continue
		}
		if _, exists := rank[key]; !exists {
			rank[key] = index
		}
		if site.HasCookie || site.HasPasskey {
			credential[key] = struct{}{}
		}
	}
	invalid := make([]string, 0, 2)
	known := make([]string, 0, len(sites))
	unknown := make([]string, 0, 2)
	for _, name := range sites {
		key := strings.ToLower(strings.TrimSpace(name))
		if _, ok := credential[key]; !ok {
			invalid = append(invalid, name)
		}
		if _, ok := rank[key]; ok {
			known = append(known, name)
		} else {
			unknown = append(unknown, name)
		}
	}
	sort.SliceStable(known, func(i, j int) bool {
		return rank[strings.ToLower(strings.TrimSpace(known[i]))] < rank[strings.ToLower(strings.TrimSpace(known[j]))]
	})
	return append(known, unknown...), invalid
}

// resolveDownloaderName 把下载器 ID 换成配置里的显示名，用于任务消息。
// 参数/返回：cfg 为当前配置；id 为下载器 ID；返回名称，找不到时回落为 ID 本身。
// 失败场景：配置缺失时返回原 ID。
// 副作用：无。
func resolveDownloaderName(cfg map[string]any, id string) string {
	target := strings.TrimSpace(id)
	if target == "" {
		return ""
	}
	for _, item := range toSliceAny(cfg["downloaders"]) {
		entry := toMapAny(item)
		if strings.TrimSpace(toStringAny(entry["id"], "")) == target {
			if name := strings.TrimSpace(toStringAny(entry["name"], "")); name != "" {
				return name
			}
			return target
		}
	}
	return target
}

// Progress 查询指定任务的进度。
// 参数/返回：taskID 为任务 ID；返回任务副本与是否存在。
// 失败场景：任务不存在时 found=false。
// 副作用：只读内存任务表。
func (s *Service) Progress(taskID string) (*Task, bool) {
	return s.getTask(taskID)
}

// Records 分页查询辅种记录。
// 参数/返回：filter 为筛选条件；返回记录与总数。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (s *Service) Records(filter repository.ReseedRecordFilter) ([]repository.ReseedRecord, int64, error) {
	return s.repo.List(filter)
}

// Stop 请求停止当前任务（仅在组与组之间生效）。
// 参数/返回：无；返回是否成功标记。
// 失败场景：没有运行中的任务时返回 false。
// 副作用：写入任务取消标记。
func (s *Service) Stop() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, task := range s.tasks {
		if task.IsRunning {
			task.Message = "正在停止：当前种子处理完后退出"
			s.stopRequested.Store(true)
			return true
		}
	}
	return false
}

func (s *Service) createTask(total int, params Params) *Task {
	task := &Task{
		TaskID:    newTaskID(),
		CreatedAt: time.Now().Format("2006-01-02 15:04:05"),
		IsRunning: true,
		Message:   "任务已启动",
		Total:     total,
		Stats:     baseStats(total),
		Params:    params,
	}
	s.mu.Lock()
	s.tasks[task.TaskID] = task
	s.mu.Unlock()
	return task
}

func (s *Service) updateTask(taskID string, fn func(task *Task)) {
	if strings.TrimSpace(taskID) == "" || fn == nil {
		return
	}
	s.mu.Lock()
	if task, ok := s.tasks[taskID]; ok {
		fn(task)
	}
	s.mu.Unlock()
}

func (s *Service) finishTask(taskID string, success bool, message string, stats map[string]any) {
	finished := time.Now().Format("2006-01-02 15:04:05")
	s.updateTask(taskID, func(task *Task) {
		task.IsRunning = false
		task.Success = &success
		task.FinishedAt = &finished
		task.Message = message
		if stats != nil {
			task.Stats = stats
		}
	})
}

func (s *Service) getTask(taskID string) (*Task, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	item, ok := s.tasks[strings.TrimSpace(taskID)]
	if !ok {
		return nil, false
	}
	copied := *item
	if item.Stats != nil {
		copied.Stats = map[string]any{}
		for k, v := range item.Stats {
			copied.Stats[k] = v
		}
	}
	return &copied, true
}

func baseStats(total int) map[string]any {
	return map[string]any{
		"total_groups": total,
		"processed":    0,
		"matched":      0,
		"no_match":     0,
		"added":        0,
		"duplicate":    0,
		"skipped":      0,
		"mismatch":     0,
		"auth_failed":  0,
		"failed":       0,
	}
}

func newTaskID() string {
	return fmt.Sprintf("reseed-%d", time.Now().UnixNano())
}

// buildCandidates 从 torrents 表构建待辅种的种子组（按 name+size 归并）。
// 参数/返回：params 为运行参数；返回按名称排序的候选组。
// 失败场景：数据库查询失败。
// 副作用：只读数据库。
func (s *Service) buildCandidates(params Params) ([]candidate, error) {
	rows, err := s.torrentRepo.ListTorrents(false)
	if err != nil {
		return nil, err
	}
	return groupCandidates(rows, params), nil
}

// groupCandidates 把 torrents 行归并成「name+size」粒度的候选组。
// 参数/返回：rows 为种子记录；返回按入库顺序、最多 params.Limit 组。
// 失败场景：无。
// 副作用：无（纯计算）。
func groupCandidates(rows []repository.TorrentRecord, params Params) []candidate {
	pathSet := map[string]struct{}{}
	for _, p := range params.Paths {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			pathSet[trimmed] = struct{}{}
		}
	}
	// 指定种子试跑：只保留这些 infohash 的行，并放开体积下限与数量上限，
	// 否则用户手选的种子很容易被「大于 198MB」「前 N 个」这两个批量默认规则静默过滤掉。
	hashSet := map[string]struct{}{}
	for _, hash := range params.Hashes {
		if trimmed := strings.ToLower(strings.TrimSpace(hash)); trimmed != "" {
			hashSet[trimmed] = struct{}{}
		}
	}
	pinned := len(hashSet) > 0

	downloaderFilter := strings.TrimSpace(params.DownloaderID)
	limit := params.Limit
	if limit <= 0 {
		limit = 200
	}
	if pinned {
		limit = len(hashSet)
	}

	type groupKey struct {
		name string
		size int64
	}
	groupMap := map[groupKey]*candidate{}
	order := make([]groupKey, 0, len(rows))

	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" || row.Size <= 0 {
			continue
		}
		rowHash := strings.ToLower(strings.TrimSpace(row.Hash))
		if pinned {
			if _, ok := hashSet[rowHash]; !ok {
				continue
			}
		}
		if !pinned && row.Size <= 207374182 {
			continue
		}
		if strings.TrimSpace(row.State) == "不存在" {
			continue
		}
		// 指定种子时以用户所选的这一行为准，不再用路径/下载器条件二次过滤，
		// 否则「选了这个种子但选了别的下载器」会导致静默查不到候选。
		if !pinned && len(pathSet) > 0 {
			if _, ok := pathSet[strings.TrimSpace(row.SavePath)]; !ok {
				continue
			}
		}
		if !pinned && downloaderFilter != "" && strings.TrimSpace(row.Downloader) != downloaderFilter {
			continue
		}

		key := groupKey{name: name, size: row.Size}
		item, exists := groupMap[key]
		if !exists {
			item = &candidate{
				Name:         name,
				Size:         row.Size,
				SavePath:     strings.TrimSpace(row.SavePath),
				DownloaderID: strings.TrimSpace(row.Downloader),
				SourceSite:   strings.TrimSpace(row.Sites),
			}
			groupMap[key] = item
			order = append(order, key)
		}

		hash := rowHash
		if hash == "" {
			continue
		}
		// 真实做种行优先作为代表（保存路径/下载器/hash 都取它的）。
		if strings.TrimSpace(row.State) != "未做种" {
			if item.SavePath == "" || item.DownloaderID == "" {
				item.SavePath = strings.TrimSpace(row.SavePath)
				item.DownloaderID = strings.TrimSpace(row.Downloader)
				item.SourceSite = strings.TrimSpace(row.Sites)
			}
			item.Hashes = appendFrontUnique(item.Hashes, hash)
		} else {
			item.Hashes = appendUniqueHash(item.Hashes, hash)
		}
	}

	groups := make([]candidate, 0, len(order))
	for _, key := range order {
		item := groupMap[key]
		if item.DownloaderID == "" {
			continue
		}
		if len(item.Hashes) == 0 {
			continue
		}
		if len(item.Hashes) > 3 {
			item.Hashes = item.Hashes[:3]
		}
		groups = append(groups, *item)
		if len(groups) >= limit {
			break
		}
	}
	return groups
}

func appendUniqueHash(list []string, hash string) []string {
	for _, item := range list {
		if item == hash {
			return list
		}
	}
	return append(list, hash)
}

func appendFrontUnique(list []string, hash string) []string {
	filtered := make([]string, 0, len(list)+1)
	filtered = append(filtered, hash)
	for _, item := range list {
		if item != hash {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func normalizeStringList(value any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 8)
	for _, item := range toSliceAny(value) {
		text := strings.TrimSpace(toStringAny(item, ""))
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		result = append(result, text)
	}
	return result
}

// normalizeHashList 把前端传入的 infohash 列表统一成小写去重结果。
// 参数/返回：value 为任意类型的列表；返回清洗后的 hash 列表。
// 失败场景：不适用（格式不识别时返回空列表）。
// 副作用：无。
func normalizeHashList(value any) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, 4)
	for _, item := range toSliceAny(value) {
		text := strings.ToLower(strings.TrimSpace(toStringAny(item, "")))
		if text == "" {
			continue
		}
		if _, ok := seen[text]; ok {
			continue
		}
		seen[text] = struct{}{}
		result = append(result, text)
	}
	return result
}

func toStringAny(value any, defaultValue string) string {
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return defaultValue
		}
		return trimmed
	case nil:
		return defaultValue
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return defaultValue
	}
}

func toIntAny(value any, defaultValue int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return defaultValue
		}
		return parsed
	default:
		return defaultValue
	}
}

func toBoolAny(value any, defaultValue bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(typed))
		if trimmed == "true" || trimmed == "1" || trimmed == "yes" {
			return true
		}
		if trimmed == "false" || trimmed == "0" || trimmed == "no" {
			return false
		}
		return defaultValue
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return defaultValue
	}
}

func toSliceAny(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result
	default:
		return []any{}
	}
}

func toMapAny(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}
