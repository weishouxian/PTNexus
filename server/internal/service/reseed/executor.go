package reseed

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
	"github.com/pt-nexus/server/internal/service/iyuu"
)

const (
	reseedLogModule   = "IYUU辅种"
	maxHashesPerBatch = 200
	maxQueryAttempts  = 3
)

// execute 执行一次辅种任务：解析 sid → 批量反查 → 逐站点下种并加入下载器 → 写记录。
// 参数/返回：taskID/token/params/groups 为任务要素；无返回值。
// 失败场景：IYUU 接口错误、站点鉴权缺失、下载或添加失败；失败会落到记录与任务状态。
// 副作用：远程 HTTP 请求、写数据库（torrents / reseed_records）、向下载器添加任务。
func (s *Service) execute(taskID string, token string, params Params, groups []candidate) {
	defer s.running.Store(false)
	s.stopRequested.Store(false)

	ctx := context.Background()
	stats := baseStats(len(groups))

	fail := func(message string) {
		logx.Warnf(reseedLogModule, "任务失败 task=%s err=%s", taskID, message)
		s.finishTask(taskID, false, message, stats)
	}

	client := iyuu.NewClient()

	localSites, err := s.torrentRepo.ListDistinctTorrentSites()
	if err != nil {
		fail("读取本地站点失败: " + err.Error())
		return
	}
	fieldToNickname, _ := s.torrentRepo.SiteFieldToNicknameMap()

	// 把目标站点强制并入 sid 上报，否则本地从未出现过的站点不会返回结果。
	sidSha1, supportedSites, err := iyuu.ResolveSidSha1(ctx, client, token, localSites, params.Sites, fieldToNickname, true)
	if err != nil {
		fail("获取 IYUU sid_sha1 失败: " + err.Error())
		return
	}

	siteByNickname := map[string]iyuu.Site{}
	for _, site := range supportedSites {
		if field := strings.TrimSpace(site.Site); field != "" {
			if nickname, ok := fieldToNickname[field]; ok && strings.TrimSpace(nickname) != "" {
				siteByNickname[strings.TrimSpace(nickname)] = site
			}
		}
		if nickname := strings.TrimSpace(site.Nickname); nickname != "" {
			if _, exists := siteByNickname[nickname]; !exists {
				siteByNickname[nickname] = site
			}
		}
	}

	targetSIDToNickname := map[int64]string{}
	unavailable := make([]string, 0)
	for _, nickname := range params.Sites {
		site, ok := siteByNickname[nickname]
		if !ok {
			unavailable = append(unavailable, nickname)
			continue
		}
		targetSIDToNickname[site.ID] = nickname
	}
	if len(targetSIDToNickname) == 0 {
		fail(fmt.Sprintf("所选站点均不在 IYUU 支持列表：%s", strings.Join(unavailable, "、")))
		return
	}
	if len(unavailable) > 0 {
		logx.Warnf(reseedLogModule, "以下站点不在 IYUU 支持列表，已跳过：%s", strings.Join(unavailable, "、"))
	}

	s.updateTask(taskID, func(task *Task) {
		task.Message = fmt.Sprintf("已解析目标站点 %d 个，开始查询 %d 个种子组", len(targetSIDToNickname), len(groups))
	})

	// 打点记录目标站 sid 与候选 hash，命中为 0 时能直接判断是「IYUU 没收录」还是「站点映射错」。
	siteLogs := make([]string, 0, len(targetSIDToNickname))
	for sid, nickname := range targetSIDToNickname {
		siteLogs = append(siteLogs, fmt.Sprintf("%s(sid=%d)", nickname, sid))
	}
	logx.Infof(reseedLogModule, "task=%s 目标站点=%s 候选组=%d 待查hash=%d",
		taskID, strings.Join(siteLogs, ","), len(groups), countCandidateHashes(groups))

	matches, diag := s.queryMatches(ctx, client, token, sidSha1, groups, targetSIDToNickname, taskID)
	logx.Infof(reseedLogModule, "task=%s IYUU 查询返回：hash=%d 条记录=%d", taskID, diag.queriedHashes, diag.rawItems)

	added, duplicate, skipped, mismatch, authFailed, failed := 0, 0, 0, 0, 0, 0
	processedGroups := 0
	matchedGroups := 0
	// authIssues 收集「站点 → 凭据问题」，任务结束时直接写在结论里，
	// 否则用户只能从几十条记录里逐条翻才知道是哪个站的 Cookie/Passkey 挂了。
	authIssues := map[string]string{}

	for index, group := range groups {
		if s.stopRequested.Load() {
			s.finishTask(taskID, false, fmt.Sprintf("任务已手动停止：处理 %d/%d 组", processedGroups, len(groups)), stats)
			return
		}

		groupMatches := matches[index]
		if len(groupMatches) > 0 {
			matchedGroups++
		}

		// 同一组内多个目标站按昵称顺序逐个处理，保证记录稳定。
		for _, nickname := range params.Sites {
			item, ok := groupMatches[nickname]
			if !ok {
				// 指定种子试跑时把「该站没有」也落一条记录，否则页面上什么都没发生，看不出结论。
				if len(params.Hashes) > 0 {
					record := resumeRecord(group, matchItem{Site: nickname}, StatusNoMatch, "IYUU 未返回该站点的记录（未收录或该站没有这个种子）", "", "", params)
					if err := s.repo.Add(record); err != nil {
						logx.Warnf(reseedLogModule, "写入未命中记录失败 name=%s site=%s err=%v", group.Name, nickname, err)
					}
				}
				continue
			}

			status, message, targetHash, detailURL := s.pinOne(group, item, params)

			switch status {
			case StatusAdded:
				added++
				stats["added"] = added
			case StatusDuplicate:
				duplicate++
				stats["duplicate"] = duplicate
			case StatusSkipped:
				skipped++
				stats["skipped"] = skipped
			case StatusMismatch:
				mismatch++
				stats["mismatch"] = mismatch
			case StatusAuthFailed:
				authFailed++
				stats["auth_failed"] = authFailed
				if _, exists := authIssues[item.Site]; !exists {
					authIssues[item.Site] = message
				}
			default:
				failed++
				stats["failed"] = failed
			}

			record := resumeRecord(group, item, status, message, targetHash, detailURL, params)
			if err := s.repo.Add(record); err != nil {
				logx.Warnf(reseedLogModule, "写入辅种记录失败 name=%s site=%s err=%v", group.Name, item.Site, err)
			}
		}

		processedGroups++
		stats["processed"] = processedGroups
		stats["matched"] = matchedGroups
		stats["no_match"] = processedGroups - matchedGroups
		s.updateTask(taskID, func(task *Task) {
			task.Processed = processedGroups
			task.Message = fmt.Sprintf("已处理 %d/%d 组（命中 %d 组，新增做种 %d）", processedGroups, len(groups), matchedGroups, added)
			task.Stats = cloneStats(stats)
		})
	}

	stats["no_match"] = processedGroups - matchedGroups

	var summary string
	switch {
	case matchedGroups == 0:
		// 全零统计很容易被误读成「成功」，这里把结论写在最前面并给出可判断的依据。
		summary = fmt.Sprintf(
			"未命中：IYUU 没有返回 %s 的记录（已查询 %d 个 infohash，接口共返回 %d 条记录，其中目标站 0 条）。"+
				"说明本次查询执行成功但没有可辅种的种子，常见原因是 IYUU 未收录该资源、目标站没有这个种子，或该资源在目标站的 infohash 与本地不同。",
			strings.Join(params.Sites, "、"), diag.queriedHashes, diag.rawItems,
		)
	case added == 0 && duplicate == 0 && skipped == 0 && mismatch == 0 && authFailed == 0 && failed == 0:
		summary = fmt.Sprintf("命中 %d 组但没有任何站点完成添加，请查看记录中的说明", matchedGroups)
	default:
		summary = fmt.Sprintf("辅种完成：处理 %d 组，命中 %d 组；新增做种 %d，已存在 %d，跳过 %d，校验不一致 %d，凭据失效 %d，失败 %d",
			processedGroups, matchedGroups, added, duplicate, skipped, mismatch, authFailed, failed)
	}
	if len(authIssues) > 0 {
		summary += "。凭据问题：" + describeAuthIssues(authIssues)
	}
	logx.Infof(reseedLogModule, "task=%s %s", taskID, summary)
	s.finishTask(taskID, true, summary, stats)
}

// queryDiagnostics 记录一次反查的可观测数据，用于判断「全零」是接口没数据还是目标站没有。
type queryDiagnostics struct {
	// queriedHashes 为实际送查的 infohash 数量。
	queriedHashes int
	// rawItems 为 IYUU 返回的记录条数（未按目标站过滤）。
	rawItems int
}

// queryMatches 批量反查每个种子组在目标站的存在情况。
// 参数/返回：返回「组下标 → 站点昵称 → 命中信息」的映射与查询统计。
// 失败场景：接口错误时该轮跳过，最终未命中的组视为无结果。
// 副作用：发起远程 HTTP 请求并更新任务进度。
func (s *Service) queryMatches(ctx context.Context, client *iyuu.Client, token, sidSha1 string, groups []candidate, targetSIDs map[int64]string, taskID string) ([]map[string]matchItem, queryDiagnostics) {
	matches := make([]map[string]matchItem, len(groups))
	for i := range matches {
		matches[i] = map[string]matchItem{}
	}
	diag := queryDiagnostics{}
	resolved := make([]bool, len(groups))
	// 每组需要命中的站点数
	required := make([]int, len(groups))
	for i := range groups {
		required[i] = len(targetSIDs)
	}
	hitCount := make([]int, len(groups))

	for attempt := 0; attempt < maxQueryAttempts; attempt++ {
		hashToIndexes := map[string][]int{}
		for i, group := range groups {
			if resolved[i] || attempt >= len(group.Hashes) {
				if attempt >= len(group.Hashes) {
					resolved[i] = true
				}
				continue
			}
			hash := strings.ToLower(strings.TrimSpace(group.Hashes[attempt]))
			if hash == "" {
				continue
			}
			hashToIndexes[hash] = append(hashToIndexes[hash], i)
		}
		if len(hashToIndexes) == 0 {
			break
		}

		hashes := make([]string, 0, len(hashToIndexes))
		for hash := range hashToIndexes {
			hashes = append(hashes, hash)
		}

		for start := 0; start < len(hashes); start += maxHashesPerBatch {
			end := start + maxHashesPerBatch
			if end > len(hashes) {
				end = len(hashes)
			}
			batch := hashes[start:end]

			results, err := client.QueryCrossSeedBatch(ctx, token, batch, sidSha1)
			if err != nil {
				logx.Warnf(reseedLogModule, "IYUU 查询失败 attempt=%d batch=%d err=%v", attempt+1, start/maxHashesPerBatch+1, err)
				continue
			}
			diag.queriedHashes += len(batch)
			for _, hash := range batch {
				items := results[strings.ToLower(hash)]
				diag.rawItems += len(items)
				for _, rawItem := range items {
					sid := int64(toIntAny(rawItem["sid"], 0))
					nickname, ok := targetSIDs[sid]
					if !ok {
						continue
					}
					for _, index := range hashToIndexes[hash] {
						if _, exists := matches[index][nickname]; exists {
							continue
						}
						matches[index][nickname] = matchItem{
							Site:      nickname,
							SID:       sid,
							TorrentID: strings.TrimSpace(toStringAny(rawItem["torrent_id"], "")),
							InfoHash:  strings.ToLower(strings.TrimSpace(toStringAny(rawItem["info_hash"], ""))),
							DetailURL: strings.TrimSpace(toStringAny(rawItem["details_page"], "")),
						}
						hitCount[index]++
						if hitCount[index] >= required[index] {
							resolved[index] = true
						}
					}
				}
			}

			s.updateTask(taskID, func(task *Task) {
				task.Message = fmt.Sprintf("IYUU 查询中：attempt %d/%d，已完成 %d/%d 个 hash", attempt+1, maxQueryAttempts, end, len(hashes))
			})
			if s.stopRequested.Load() {
				return matches, diag
			}
		}
	}
	return matches, diag
}

// pinOne 对单个「资源 × 目标站」执行下种与做种，返回状态与说明。
// 参数/返回：返回状态、说明、目标 infohash 与详情页链接。
// 失败场景：站点缺少 cookie/passkey、下载种子失败、infohash 校验不一致、下载器添加失败。
// 副作用：下载 .torrent 写入临时目录，向下载器添加任务，回填 torrents 占位行。
func (s *Service) pinOne(group candidate, item matchItem, params Params) (string, string, string, string) {

	seeded, err := s.repo.ExistsSeeded(group.Name, group.Size, item.Site)
	if err != nil {
		return StatusFailed, "检查已有做种记录失败: " + err.Error(), item.InfoHash, item.DetailURL
	}
	if seeded {
		return StatusSkipped, "目标站已有该种子的做种记录", item.InfoHash, item.DetailURL
	}

	// 没有保存路径时绝不能加任务：下载器会用默认目录并真的去下载数据，违背辅种前提。
	if strings.TrimSpace(group.SavePath) == "" {
		return StatusFailed, "缺少保存路径（save_path），为避免重新下载数据已跳过", item.InfoHash, item.DetailURL
	}

	siteAuth, err := s.repo.GetSiteAuth(item.Site)
	if err != nil {
		return StatusFailed, "读取站点配置失败: " + err.Error(), item.InfoHash, item.DetailURL
	}
	if len(siteAuth) == 0 {
		return StatusFailed, "站点配置不存在，无法下载种子", item.InfoHash, item.DetailURL
	}
	if strings.TrimSpace(toStringAny(siteAuth["cookie"], "")) == "" && strings.TrimSpace(toStringAny(siteAuth["passkey"], "")) == "" {
		return StatusFailed, "站点缺少 Cookie/Passkey，无法下载种子", item.InfoHash, item.DetailURL
	}

	torrentPath, detailURL, body, err := acquirefetch.DownloadTorrentForSource(siteAuth, item.TorrentID)
	_ = torrentPath
	if err != nil {
		if strings.TrimSpace(detailURL) == "" {
			detailURL = item.DetailURL
		}
		status, message := classifyDownloadError(err)
		return status, message, item.InfoHash, detailURL
	}
	if len(body) == 0 {
		return StatusFailed, "下载到的种子内容为空", item.InfoHash, item.DetailURL
	}

	meta, err := acquirefetch.ParseTorrentMeta(body)
	if err != nil {
		return StatusFailed, "解析种子失败: " + err.Error(), item.InfoHash, item.DetailURL
	}
	targetHash := strings.ToLower(strings.TrimSpace(meta.InfoHash))
	if targetHash == "" {
		return StatusFailed, "无法解析目标种子的 infohash", item.InfoHash, item.DetailURL
	}
	// IYUU 索引的 info_hash 与站点真实种子不一致时说明数据已过期（站点重打包/替换），不添加。
	if expected := strings.ToLower(strings.TrimSpace(item.InfoHash)); expected != "" && expected != targetHash {
		return StatusMismatch, fmt.Sprintf("IYUU 记录 infohash 与站点实际种子不一致（IYUU=%s 实际=%s），已跳过", expected, targetHash), targetHash, detailURL
	}
	if detailURL == "" {
		detailURL = item.DetailURL
	}

	downloaderID := strings.TrimSpace(params.DownloaderID)
	if downloaderID == "" {
		downloaderID = group.DownloaderID
	}
	downloader, err := downloaderclient.FromConfig(s.cfg.Get(), downloaderID)
	if err != nil {
		return StatusFailed, "下载器不可用: " + err.Error(), targetHash, detailURL
	}

	skipChecking := params.SkipChecking
	tags := effectiveTags(params, item.Site)
	options := downloaderclient.AddTorrentOptions{
		Paused:       false,
		SkipChecking: &skipChecking,
		Tags:         tags,
	}
	fileName := sanitizeFileName(group.Name) + ".torrent"
	if err := downloader.AddTorrentDataWithOptions(body, fileName, group.SavePath, options); err != nil {
		if isDuplicateError(err) {
			// 任务已在下载器里时标签不会自动补，这里按真实 hash 补一次，避免测试时看不到标签效果。
			if applyErr := tagExistingTorrent(downloader, targetHash, tags); applyErr != nil {
				logx.Warnf(reseedLogModule, "补写标签失败 hash=%s err=%v", targetHash, applyErr)
			}
			return StatusDuplicate, "下载器已存在同一任务", targetHash, detailURL
		}
		return StatusFailed, "加入下载器失败: " + err.Error(), targetHash, detailURL
	}

	// 回填 torrents：先补占位行再换真 hash，让「一种多站」列表立刻能看到该站点。
	now := time.Now().Format("2006-01-02 15:04:05")
	sourceHash := ""
	if len(group.Hashes) > 0 {
		sourceHash = group.Hashes[0]
	}
	if _, err := s.torrentRepo.InsertMissingIYUUSiteTorrents(group.Name, group.Size, group.SavePath, downloaderID, sourceHash, map[string]string{item.Site: detailURL}, now); err != nil {
		logx.Warnf(reseedLogModule, "写入占位站点失败 name=%s site=%s err=%v", group.Name, item.Site, err)
	}
	if _, _, err := s.migrateRepo.ReplaceUnseededPlaceholderHash(group.Name, group.Size, downloaderID, item.Site, targetHash); err != nil {
		logx.Warnf(reseedLogModule, "回填真实 hash 失败 name=%s site=%s err=%v", group.Name, item.Site, err)
	}
	// 站点行已存在但 details 为空时补上详情链接（占位行替换不会覆盖这种情况）。
	if _, err := s.torrentRepo.UpdateIYUUCheckAndFillDetails(group.Name, group.Size, map[string]string{item.Site: detailURL}, now); err != nil {
		logx.Warnf(reseedLogModule, "回填详情链接失败 name=%s site=%s err=%v", group.Name, item.Site, err)
	}

	_ = torrentPath
	message := "已加入下载器并开始做种"
	if len(tags) > 0 {
		message = fmt.Sprintf("已加入下载器并开始做种（标签：%s）", strings.Join(tags, "、"))
	}
	return StatusAdded, message, targetHash, detailURL
}

// classifyDownloadError 把下载种子失败细分为「站点凭据问题」与「其它失败」。
// 参数/返回：err 为下载错误；返回记录状态与可直接展示给用户的说明。
// 失败场景：不适用。
// 副作用：无。
//
// Cookie 失效与 Passkey 无效属于配置问题，用户需要知道去改哪个站点的哪一项；
// 混在「下载种子失败」里会让人误以为是站点或网络问题。
func classifyDownloadError(err error) (string, string) {
	if err == nil {
		return StatusFailed, ""
	}
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	switch {
	case errors.Is(err, acquirefetch.ErrSourceCookieExpired):
		return StatusAuthFailed, "站点 Cookie 已失效（登录态不可用），请到「设置 → 站点」更新 Cookie 后重试"
	case strings.Contains(lower, "passkey"),
		strings.Contains(lower, "cookie"),
		strings.Contains(message, "登录"):
		return StatusAuthFailed, "站点凭据问题：" + message
	}
	return StatusFailed, "下载种子失败: " + message
}

// siteNameTagPrefix 是「按目标站名打标签」的前缀，必须与设置中心「下载器标签/分类设置」里
// 「站点/{站点名称}」占位符替换后的前缀一致（见 publish/downloader.resolveConfiguredTagsAndCategory），
// 这样辅种和转种/发种写入下载器的站点标签同名，可按同一个标签统一筛选。
const siteNameTagPrefix = "站点/"

// effectiveTags 计算单个种子实际写入下载器的标签。
// 参数/返回：params 为任务参数，site 为目标站昵称；返回去重后的标签列表。
// 失败场景：不适用。
// 副作用：无。
func effectiveTags(params Params, site string) []string {
	tags := make([]string, 0, len(params.Tags)+1)
	seen := map[string]struct{}{}
	for _, tag := range params.Tags {
		trimmed := strings.TrimSpace(tag)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		tags = append(tags, trimmed)
	}
	if params.TagBySite {
		// 与设置里的站点标签同一口径：「站点/财神」，而不是裸站名。
		if trimmed := strings.TrimSpace(site); trimmed != "" {
			siteTag := siteNameTagPrefix + trimmed
			if _, ok := seen[siteTag]; !ok {
				tags = append(tags, siteTag)
			}
		}
	}
	return tags
}

// describeAuthIssues 把「站点 → 凭据问题」拼成一行结论，供任务消息展示。
// 参数/返回：issues 为站点昵称到原因的映射；返回顿号分隔的说明。
// 失败场景：不适用。
// 副作用：无。
func describeAuthIssues(issues map[string]string) string {
	names := make([]string, 0, len(issues))
	for name := range issues {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		reason := strings.TrimSpace(issues[name])
		if reason == "" {
			reason = "凭据不可用"
		}
		parts = append(parts, fmt.Sprintf("%s（%s）", name, reason))
	}
	return strings.Join(parts, "；")
}

// tagExistingTorrent 给下载器里已存在的任务补写标签。
// 参数/返回：downloader 为已建好的下载器客户端；hash 为任务 infohash；tags 为空时直接跳过。
// 失败场景：下载器请求失败时返回错误（调用方只记日志，不影响主流程）。
// 副作用：向下载器写入标签。
func tagExistingTorrent(downloader downloaderclient.Downloader, hash string, tags []string) error {
	hash = strings.TrimSpace(hash)
	if hash == "" || len(tags) == 0 {
		return nil
	}
	return downloader.ApplyTorrentTags([]string{hash}, tags)
}

// countCandidateHashes 统计候选组里待查询的 hash 数量（去重）。
func countCandidateHashes(groups []candidate) int {
	seen := map[string]struct{}{}
	for _, group := range groups {
		for _, hash := range group.Hashes {
			trimmed := strings.ToLower(strings.TrimSpace(hash))
			if trimmed == "" {
				continue
			}
			seen[trimmed] = struct{}{}
		}
	}
	return len(seen)
}

// resumeRecord 组装一条辅种记录。
func resumeRecord(group candidate, item matchItem, status, message, targetHash, detailURL string, params Params) repository.ReseedRecord {
	downloaderID := strings.TrimSpace(params.DownloaderID)
	if downloaderID == "" {
		downloaderID = group.DownloaderID
	}
	return repository.ReseedRecord{
		TorrentName:     group.Name,
		Size:            group.Size,
		SourceHash:      firstHash(group.Hashes),
		SourceSite:      group.SourceSite,
		TargetSite:      item.Site,
		TargetSID:       item.SID,
		TargetTorrentID: item.TorrentID,
		TargetInfoHash:  targetHash,
		DetailURL:       detailURL,
		SavePath:        group.SavePath,
		DownloaderID:    downloaderID,
		Tags:            recordTags(status, params, item.Site),
		Status:          status,
		Message:         message,
	}
}

// recordTags 返回记录里应该展示的标签。
// 只有真正写入过下载器的记录（新增/已存在）才有标签，其余状态打了也等于没打，留空更准确。
func recordTags(status string, params Params, site string) string {
	if status != StatusAdded && status != StatusDuplicate {
		return ""
	}
	return strings.Join(effectiveTags(params, site), ",")
}

func firstHash(hashes []string) string {
	if len(hashes) == 0 {
		return ""
	}
	return hashes[0]
}

func cloneStats(stats map[string]any) map[string]any {
	copied := make(map[string]any, len(stats))
	for k, v := range stats {
		copied[k] = v
	}
	return copied
}

// isDuplicateError 判断下载器返回的错误是否表示任务已存在。
func isDuplicateError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate") ||
		strings.Contains(message, "already") ||
		strings.Contains(message, "exists")
}

// sanitizeFileName 把种子名转成安全的文件名片段。
func sanitizeFileName(name string) string {
	replacer := strings.NewReplacer(
		"/", "_", "\\", "_", ":", "_", "*", "_",
		"?", "_", "\"", "_", "<", "_", ">", "_", "|", "_",
	)
	result := strings.TrimSpace(replacer.Replace(name))
	if result == "" {
		result = "reseed"
	}
	if len(result) > 120 {
		result = result[:120]
	}
	return result
}
