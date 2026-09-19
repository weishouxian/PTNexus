package torrentdata

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
)

const refreshLogModule = "种子刷新"

var (
	commentURLPattern = regexp.MustCompile(`https?://[^\s/$.?#].[^\s]*`)
	commentObTid      = regexp.MustCompile(`ob_tid=(\d+)`)
	commentHDH        = regexp.MustCompile(`[A-Za-z0-9]+x(\d+)x\d+x[0-9a-zA-Z]+`)
	commentOnlyID     = regexp.MustCompile(`^\s*(\d+)\s*$`)
	groupBracket      = regexp.MustCompile(`\[.*?\]`)
)

type refreshSiteMatcher struct {
	hostMap map[string]string
	coreMap map[string]string
}

type refreshGroupEntry struct {
	original   string
	lower      string
	cleanLower string
	owner      string
}

type refreshGroupMatcher struct {
	groups []refreshGroupEntry
}

// refreshFromDownloaders 执行下载器种子同步的主流程。
// 参数/返回：targetDownloaderID 为空表示同步全部启用下载器，非空表示只同步该下载器；返回同步统计与失败明细。
// 失败场景：已有刷新在运行、无可用下载器、指定下载器不存在/已停用时返回 success=false。
// 副作用：请求下载器接口、写入 torrents 等数据表，并在处理期间持有刷新互斥标记。
func (s *TorrentDataService) refreshFromDownloaders(targetDownloaderID string) map[string]any {
	if !s.beginRefresh() {
		return map[string]any{
			"success": false,
			"message": "种子数据更新正在进行中，请稍后再试",
		}
	}
	defer s.finishRefresh()

	startedAt := time.Now()
	settings := s.cfg.Get()
	scopes := collectDownloaderScopes(settings)

	// 顶部「按下载器刷新」只收窄抓取范围；对账范围由 narrowTo 原样带过来，
	// 未被本次同步的启用下载器不会被误判为停用或已删除。
	targetID := strings.TrimSpace(targetDownloaderID)
	scopes, targetName, narrowErr := scopes.narrowTo(targetID)
	if narrowErr != nil {
		logx.Warnf(refreshLogModule, "指定下载器不可用 downloader_id=%s err=%v", targetID, narrowErr)
		return map[string]any{
			"success": false,
			"message": narrowErr.Error(),
		}
	}

	siteRows, err := s.repo.ListSiteIdentities()
	if err != nil {
		logx.Warnf(refreshLogModule, "读取站点识别信息失败 err=%v", err)
		siteRows = []repository.SiteIdentity{}
	}
	siteMatcher := newRefreshSiteMatcher(siteRows)
	groupMatcher := newRefreshGroupMatcher(siteRows)
	siteLinkRules := toSiteLinkRules(siteRows)

	nowStr := time.Now().Format("2006-01-02 15:04:05")
	hiddenDisabledCount := int64(0)
	hiddenRemovedCount := int64(0)

	// 对账始终使用 scopes 里的全量范围（配置的完整快照），与本次抓取目标无关。
	// 若这里传入被收窄的列表，未参与本次同步的下载器数据会被整片误标记为隐藏。
	hiddenDisabledCount, hideDisabledErr := s.repo.HideDisabledDownloaderData(scopes.enabledIDs, scopes.configuredIDs)
	if hideDisabledErr != nil {
		logx.Warnf(refreshLogModule, "隐藏停用下载器数据失败 err=%v", hideDisabledErr)
	} else if hiddenDisabledCount > 0 {
		logx.Infof(refreshLogModule, "已隐藏停用下载器种子 hidden=%d", hiddenDisabledCount)
	}

	hiddenRemovedCount, hideRemovedErr := s.repo.HideRemovedDownloaderData(scopes.configuredIDs)
	if hideRemovedErr != nil {
		logx.Warnf(refreshLogModule, "隐藏已删除下载器数据失败 err=%v", hideRemovedErr)
	} else if hiddenRemovedCount > 0 {
		logx.Infof(refreshLogModule, "已隐藏无效下载器种子 hidden=%d", hiddenRemovedCount)
	}

	if len(scopes.fetchTargets) == 0 {
		return map[string]any{
			"success": false,
			"message": "没有可用的启用下载器，无法刷新种子数据",
			"stats": map[string]any{
				"total_downloaders":       len(scopes.failedConfigs),
				"success_downloaders":     0,
				"failed_downloaders":      len(scopes.failedConfigs),
				"inserted_torrents":       0,
				"updated_torrents":        0,
				"deleted_torrents":        hiddenDisabledCount + hiddenRemovedCount,
				"upserted_upload_stats":   0,
				"cleanup_deleted_records": hiddenRemovedCount,
				"hidden_disabled_records": hiddenDisabledCount,
				"hidden_removed_records":  hiddenRemovedCount,
				"elapsed_ms":              time.Since(startedAt).Milliseconds(),
				"only_local_available":    false,
			},
			"failed_downloaders": scopes.failedConfigs,
		}
	}

	totalDownloaders := len(scopes.fetchTargets) + len(scopes.failedConfigs)
	successDownloaders := 0
	failedDownloaders := len(scopes.failedConfigs)
	insertedTorrents := int64(0)
	updatedTorrents := int64(0)
	deletedTorrents := hiddenDisabledCount + hiddenRemovedCount
	upsertedUploadStats := int64(0)
	failedDetails := append([]map[string]any{}, scopes.failedConfigs...)

	for _, downloader := range scopes.fetchTargets {
		snapshots, fetchErr := downloader.FetchTorrents()
		if fetchErr != nil {
			failedDownloaders++
			failedDetails = append(failedDetails, map[string]any{
				"id":    downloader.ID,
				"name":  downloader.Name,
				"error": fetchErr.Error(),
			})
			logx.Warnf(refreshLogModule, "拉取下载器种子失败 downloader_id=%s name=%s err=%v", downloader.ID, downloader.Name, fetchErr)
			continue
		}

		records := buildSyncRecords(downloader.ID, snapshots, siteMatcher, groupMatcher, siteLinkRules)
		syncStats, syncErr := s.repo.SyncDownloaderTorrents(downloader.ID, records, nowStr)
		if syncErr != nil {
			failedDownloaders++
			failedDetails = append(failedDetails, map[string]any{
				"id":    downloader.ID,
				"name":  downloader.Name,
				"error": syncErr.Error(),
			})
			logx.Warnf(refreshLogModule, "写入下载器种子失败 downloader_id=%s name=%s err=%v", downloader.ID, downloader.Name, syncErr)
			continue
		}

		successDownloaders++
		insertedTorrents += syncStats.Inserted
		updatedTorrents += syncStats.Updated
		deletedTorrents += syncStats.Deleted
		upsertedUploadStats += syncStats.UpsertedUpload
		logx.Infof(
			refreshLogModule,
			"下载器同步完成 downloader_id=%s name=%s inserted=%d updated=%d deleted=%d uploads=%d fetched=%d",
			downloader.ID,
			downloader.Name,
			syncStats.Inserted,
			syncStats.Updated,
			syncStats.Deleted,
			syncStats.UpsertedUpload,
			len(snapshots),
		)
	}

	torrents, listErr := s.repo.ListTorrents(false)
	if listErr != nil {
		logx.Warnf(refreshLogModule, "读取刷新后种子统计失败 err=%v", listErr)
		torrents = []repository.TorrentRecord{}
	}
	completed := 0
	for _, torrent := range torrents {
		stateLower := strings.ToLower(strings.TrimSpace(torrent.State))
		if strings.Contains(stateLower, "做种") || strings.Contains(stateLower, "seeding") {
			completed++
		}
	}
	seedGroups, _ := s.repo.DistinctSeedParameterNames()

	success := successDownloaders > 0
	message := fmt.Sprintf("种子数据更新完成：成功 %d/%d 个下载器", successDownloaders, totalDownloaders)
	if targetID != "" {
		// 指定下载器刷新时给出更直观的单点结果提示。
		if success {
			message = fmt.Sprintf("种子数据更新完成：下载器「%s」同步成功", targetName)
		} else {
			message = fmt.Sprintf("种子数据更新失败：下载器「%s」同步失败", targetName)
		}
	} else if !success {
		message = "种子数据更新失败：所有下载器均同步失败"
	}

	return map[string]any{
		"success": success,
		"message": message,
		"stats": map[string]any{
			"total_torrents":          len(torrents),
			"completed_torrents":      completed,
			"cached_seed_groups":      len(seedGroups),
			"refreshed_at":            nowStr,
			"only_local_available":    false,
			"total_downloaders":       totalDownloaders,
			"success_downloaders":     successDownloaders,
			"failed_downloaders":      failedDownloaders,
			"inserted_torrents":       insertedTorrents,
			"updated_torrents":        updatedTorrents,
			"deleted_torrents":        deletedTorrents,
			"upserted_upload_stats":   upsertedUploadStats,
			"cleanup_deleted_records": hiddenRemovedCount,
			"hidden_disabled_records": hiddenDisabledCount,
			"hidden_removed_records":  hiddenRemovedCount,
			"elapsed_ms":              time.Since(startedAt).Milliseconds(),
		},
		"failed_downloaders": failedDetails,
	}
}

func (s *TorrentDataService) beginRefresh() bool {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()
	if s.refreshRunning {
		return false
	}
	s.refreshRunning = true
	return true
}

func (s *TorrentDataService) finishRefresh() {
	s.refreshMu.Lock()
	s.refreshRunning = false
	s.refreshMu.Unlock()
}

// downloaderScopes 汇总一次刷新涉及的两类下载器范围，二者必须分开维护：
//   - 对账范围：configuredIDs（配置内全部下载器，含停用）与 enabledIDs（其中已启用的部分），
//     只用于判断哪些历史种子需要被隐藏，始终代表配置的完整快照。
//   - 抓取范围：fetchTargets，本次实际要去同步的下载器，可能被收窄到单个下载器。
//
// 收窄抓取范围只能通过 narrowTo 进行，它不会改动对账范围。
type downloaderScopes struct {
	configuredIDs []string
	enabledIDs    []string
	fetchTargets  []downloaderclient.Downloader
	failedConfigs []map[string]any
}

// narrowTo 把本次抓取范围收窄到指定下载器，对账范围保持全量不变。
// 参数/返回：targetID 为空表示不收窄（同步全部启用下载器）；返回新的范围对象、命中的下载器名称与错误。
// 失败场景：targetID 非空但不在启用下载器列表中时返回错误，调用方应据此终止流程。
// 副作用：无，不读写数据库，也不发起网络请求。
func (sc downloaderScopes) narrowTo(targetID string) (downloaderScopes, string, error) {
	trimmed := strings.TrimSpace(targetID)
	if trimmed == "" {
		return sc, "", nil
	}

	for _, item := range sc.fetchTargets {
		if strings.TrimSpace(item.ID) != trimmed {
			continue
		}
		narrowed := sc
		narrowed.fetchTargets = []downloaderclient.Downloader{item}
		// 统计与失败明细只反映本次同步目标，避免混入其他下载器的配置错误。
		narrowed.failedConfigs = []map[string]any{}
		return narrowed, item.Name, nil
	}

	return sc, "", fmt.Errorf("未找到可用的下载器「%s」，请确认该下载器已启用。", trimmed)
}

// collectDownloaderScopes 从配置快照解析出下载器范围。
// 参数/返回：settings 为配置快照；返回对账范围、抓取范围与配置无效明细。
// 失败场景：不返回错误；单个下载器配置无效时记入 failedConfigs 并跳过该下载器。
// 副作用：无，仅解析内存中的配置快照。
func collectDownloaderScopes(settings map[string]any) downloaderScopes {
	downloadersRaw := toSlice(settings["downloaders"])
	scopes := downloaderScopes{
		configuredIDs: make([]string, 0, len(downloadersRaw)),
		enabledIDs:    make([]string, 0, len(downloadersRaw)),
		fetchTargets:  make([]downloaderclient.Downloader, 0, len(downloadersRaw)),
		failedConfigs: make([]map[string]any, 0),
	}
	configuredSet := map[string]struct{}{}
	enabledSet := map[string]struct{}{}

	for _, raw := range downloadersRaw {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(toString(item["id"], ""))
		if id == "" {
			continue
		}
		if _, exists := configuredSet[id]; !exists {
			configuredSet[id] = struct{}{}
			scopes.configuredIDs = append(scopes.configuredIDs, id)
		}
		if !toBool(item["enabled"], true) {
			continue
		}
		if _, exists := enabledSet[id]; !exists {
			enabledSet[id] = struct{}{}
			scopes.enabledIDs = append(scopes.enabledIDs, id)
		}

		downloader, err := downloaderclient.FromConfig(settings, id)
		if err != nil {
			scopes.failedConfigs = append(scopes.failedConfigs, map[string]any{
				"id":    id,
				"name":  strings.TrimSpace(toString(item["name"], id)),
				"error": "配置无效: " + err.Error(),
			})
			logx.Warnf(refreshLogModule, "下载器配置无效 downloader_id=%s name=%s err=%v", id, strings.TrimSpace(toString(item["name"], id)), err)
			continue
		}
		scopes.fetchTargets = append(scopes.fetchTargets, downloader)
	}

	return scopes
}

func buildSyncRecords(
	downloaderID string,
	snapshots []downloaderclient.TorrentSnapshot,
	siteMatcher refreshSiteMatcher,
	groupMatcher refreshGroupMatcher,
	siteLinkRules map[string]string,
) []repository.TorrentSyncRecord {
	records := make([]repository.TorrentSyncRecord, 0, len(snapshots))
	for _, snapshot := range snapshots {
		hash := strings.TrimSpace(snapshot.Hash)
		name := strings.TrimSpace(snapshot.Name)
		if hash == "" || name == "" {
			continue
		}
		details := extractDetailFromComment(snapshot.Comment)
		siteName := siteMatcher.Match(snapshot.Trackers, details, snapshot.Comment)
		details = normalizeDetailURL(details, siteName, siteLinkRules)
		torrentGroup := groupMatcher.Match(name, snapshot.Group)
		records = append(records, repository.TorrentSyncRecord{
			Hash:         hash,
			Name:         name,
			SavePath:     strings.TrimSpace(snapshot.SavePath),
			Size:         snapshot.Size,
			Progress:     snapshot.Progress,
			State:        strings.TrimSpace(snapshot.State),
			Sites:        siteName,
			Details:      details,
			TorrentGroup: torrentGroup,
			OfficialSite: groupMatcher.Owner(torrentGroup),
			DownloaderID: downloaderID,
			Seeders:      snapshot.Seeders,
			Uploaded:     snapshot.Uploaded,
		})
	}
	return records
}

func toSiteLinkRules(rows []repository.SiteIdentity) map[string]string {
	result := map[string]string{}
	for _, row := range rows {
		nickname := strings.TrimSpace(row.Nickname)
		baseURL := strings.TrimSpace(row.BaseURL)
		if nickname == "" || baseURL == "" {
			continue
		}
		result[nickname] = baseURL
	}
	return result
}

func normalizeDetailURL(details string, siteName string, siteLinkRules map[string]string) string {
	trimmed := strings.TrimSpace(details)
	if trimmed == "" {
		return ""
	}

	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		// 数字 ID / 站点自定义 comment 保持原样，交由前端根据 site_link_rules 拼接。
		return trimmed
	}

	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil {
		return trimmed
	}

	// 清理掉历史遗留 existed=1（避免前端重复处理，且该参数无实际用途）。
	query := parsed.Query()
	query.Del("existed")
	parsed.RawQuery = query.Encode()

	baseURL := strings.TrimSpace(siteLinkRules[strings.TrimSpace(siteName)])
	if baseURL == "" {
		return parsed.String()
	}

	normalizedBase := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(baseURL, "https://"), "http://"), "/"))
	if normalizedBase == "" {
		return parsed.String()
	}

	detailHost := parseHostCandidate(parsed.String())
	baseHost := parseHostCandidate(baseURL)
	if detailHost != "" && baseHost != "" && detailHost != baseHost && extractCoreDomain(detailHost) != extractCoreDomain(baseHost) {
		return ""
	}

	parsed.Scheme = "https"
	parsed.Host = normalizedBase
	return parsed.String()
}

func newRefreshGroupMatcher(rows []repository.SiteIdentity) refreshGroupMatcher {
	entries := make([]refreshGroupEntry, 0)
	seen := map[string]struct{}{}
	for _, row := range rows {
		if strings.TrimSpace(row.SiteGroup) == "" {
			continue
		}
		parts := strings.Split(row.SiteGroup, ",")
		for _, part := range parts {
			original := strings.TrimSpace(part)
			if original == "" {
				continue
			}
			lower := strings.ToLower(original)
			if _, exists := seen[lower]; exists {
				continue
			}
			cleanLower := strings.TrimSpace(strings.TrimLeft(lower, "-"))
			if cleanLower == "" {
				continue
			}
			seen[lower] = struct{}{}
			entries = append(entries, refreshGroupEntry{
				original:   original,
				lower:      lower,
				cleanLower: cleanLower,
				owner:      strings.TrimSpace(row.Nickname),
			})
		}
	}
	return refreshGroupMatcher{groups: entries}
}

func (m refreshGroupMatcher) Owner(group string) string {
	normalized := strings.TrimSpace(strings.TrimLeft(strings.ToLower(group), "-"))
	if normalized == "" {
		return ""
	}
	for _, entry := range m.groups {
		if entry.cleanLower == normalized {
			return entry.owner
		}
	}
	return ""
}

func (m refreshGroupMatcher) Match(name string, snapshotGroup string) string {
	nameLower := strings.ToLower(strings.TrimSpace(name))
	if nameLower == "" {
		return m.matchSnapshotGroup(snapshotGroup)
	}

	exactMatches := make([]string, 0)
	partialMatches := make([]string, 0)
	exactSet := map[string]struct{}{}
	partialSet := map[string]struct{}{}

	if strings.Contains(nameLower, "@") {
		parts := strings.Split(nameLower, "@")
		for _, part := range parts {
			cleanPart := strings.TrimSpace(strings.TrimLeft(part, "-"))
			cleanPart = strings.TrimSpace(groupBracket.ReplaceAllString(cleanPart, ""))
			if cleanPart == "" {
				continue
			}
			for _, entry := range m.groups {
				if entry.cleanLower == cleanPart {
					if _, exists := exactSet[entry.original]; !exists {
						exactSet[entry.original] = struct{}{}
						exactMatches = append(exactMatches, entry.original)
					}
					continue
				}
				if strings.Contains(cleanPart, entry.cleanLower) || strings.Contains(entry.cleanLower, cleanPart) {
					if _, exists := exactSet[entry.original]; exists {
						continue
					}
					if _, exists := partialSet[entry.original]; !exists {
						partialSet[entry.original] = struct{}{}
						partialMatches = append(partialMatches, entry.original)
					}
				}
			}
		}
	}

	if len(exactMatches) > 0 {
		return shortestString(exactMatches)
	}
	if len(partialMatches) > 0 {
		return longestString(partialMatches)
	}

	for _, entry := range m.groups {
		if strings.Contains(nameLower, entry.lower) {
			if _, exists := partialSet[entry.original]; !exists {
				partialSet[entry.original] = struct{}{}
				partialMatches = append(partialMatches, entry.original)
			}
		}
	}

	if len(partialMatches) > 0 {
		return longestString(partialMatches)
	}
	return m.matchSnapshotGroup(snapshotGroup)
}

func (m refreshGroupMatcher) matchSnapshotGroup(snapshotGroup string) string {
	trimmed := strings.TrimSpace(snapshotGroup)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, ",") {
		parts := strings.Split(trimmed, ",")
		trimmed = ""
		for _, part := range parts {
			item := strings.TrimSpace(part)
			if item != "" {
				trimmed = item
				break
			}
		}
		if trimmed == "" {
			return ""
		}
	}

	normalized := strings.TrimSpace(strings.TrimLeft(strings.ToLower(trimmed), "-"))
	if normalized == "" {
		return ""
	}

	for _, entry := range m.groups {
		if entry.cleanLower == normalized {
			return entry.original
		}
	}
	return ""
}

func shortestString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	best := values[0]
	for _, value := range values[1:] {
		if len(value) < len(best) {
			best = value
		}
	}
	return best
}

func longestString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	best := values[0]
	for _, value := range values[1:] {
		if len(value) > len(best) {
			best = value
		}
	}
	return best
}

func newRefreshSiteMatcher(rows []repository.SiteIdentity) refreshSiteMatcher {
	matcher := refreshSiteMatcher{
		hostMap: map[string]string{},
		coreMap: map[string]string{},
	}
	for _, row := range rows {
		nickname := strings.TrimSpace(row.Nickname)
		if nickname == "" {
			continue
		}
		candidates := []string{row.BaseURL, row.SpecialTrackerDomain, row.Site}
		for _, candidate := range candidates {
			host := parseHostCandidate(candidate)
			if host == "" {
				continue
			}
			if _, exists := matcher.hostMap[host]; !exists {
				matcher.hostMap[host] = nickname
			}
			core := extractCoreDomain(host)
			if core != "" {
				if _, exists := matcher.coreMap[core]; !exists {
					matcher.coreMap[core] = nickname
				}
			}
		}
	}
	return matcher
}

func (m refreshSiteMatcher) Match(trackers []string, detail string, comment string) string {
	candidates := make([]string, 0, len(trackers)+2)
	candidates = append(candidates, trackers...)
	if strings.TrimSpace(detail) != "" {
		candidates = append(candidates, detail)
	}
	if extracted := extractDetailFromComment(comment); extracted != "" {
		candidates = append(candidates, extracted)
	}

	for _, candidate := range candidates {
		host := parseHostCandidate(candidate)
		if host == "" {
			continue
		}
		if nickname, exists := m.hostMap[host]; exists {
			return nickname
		}
		core := extractCoreDomain(host)
		if core == "" {
			continue
		}
		if nickname, exists := m.coreMap[core]; exists {
			return nickname
		}
	}
	return ""
}

func parseHostCandidate(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		parsed, err := url.Parse(trimmed)
		if err == nil {
			return normalizeHostForMatch(parsed.Hostname())
		}
	}
	normalized := strings.TrimPrefix(trimmed, "udp://")
	normalized = strings.TrimPrefix(normalized, "ws://")
	normalized = strings.TrimPrefix(normalized, "wss://")
	if strings.Contains(normalized, "://") {
		parsed, err := url.Parse(normalized)
		if err == nil {
			return normalizeHostForMatch(parsed.Hostname())
		}
	}
	if idx := strings.Index(normalized, "/"); idx >= 0 {
		normalized = normalized[:idx]
	}
	if idx := strings.Index(normalized, ":"); idx >= 0 {
		normalized = normalized[:idx]
	}
	return normalizeHostForMatch(normalized)
}

func normalizeHostForMatch(host string) string {
	trimmed := strings.ToLower(strings.TrimSpace(host))
	if trimmed == "" {
		return ""
	}
	trimmed = strings.TrimPrefix(trimmed, "www.")
	return trimmed
}

func extractCoreDomain(host string) string {
	cleaned := normalizeHostForMatch(host)
	if cleaned == "" {
		return ""
	}
	for _, prefix := range []string{"tracker.", "kp.", "pt.", "t.", "ipv4.", "ipv6.", "on.", "daydream."} {
		cleaned = strings.TrimPrefix(cleaned, prefix)
	}
	parts := strings.Split(cleaned, ".")
	if len(parts) == 0 {
		return ""
	}
	if len(parts) > 2 {
		last := parts[len(parts)-1]
		prev := parts[len(parts)-2]
		if len(last) <= 3 && len(prev) <= 3 && len(parts) >= 3 {
			return parts[len(parts)-3]
		}
	}
	if len(parts) > 1 {
		return parts[len(parts)-2]
	}
	return parts[0]
}

func extractDetailFromComment(comment string) string {
	trimmed := strings.TrimSpace(comment)
	if trimmed == "" {
		return ""
	}
	if matched := commentURLPattern.FindString(trimmed); matched != "" {
		return strings.TrimSpace(matched)
	}
	if groups := commentObTid.FindStringSubmatch(trimmed); len(groups) > 1 {
		return strings.TrimSpace(groups[1])
	}
	if groups := commentHDH.FindStringSubmatch(trimmed); len(groups) > 1 {
		return strings.TrimSpace(groups[1])
	}
	if groups := commentOnlyID.FindStringSubmatch(trimmed); len(groups) > 1 {
		return strings.TrimSpace(groups[1])
	}
	return ""
}
