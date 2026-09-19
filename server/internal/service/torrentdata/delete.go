package torrentdata

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
)

const torrentDataLogModule = "一种多站"

// DeleteTorrentByHash 删除「一种多站」中指定内容，可选同步删除下载器任务与文件。
// 参数/返回：payload.hash/hashes 为 torrents.hash；delete_files 控制是否删除下载器任务和文件；返回接口响应体与 HTTP 状态码。
// 失败场景：缺少 hash、未找到 torrents 记录、必需下载器删除失败或数据库删除失败时返回错误响应。
// 副作用：delete_files=true 时先核对下载器上的同内容副本，再按下载器分组删除任务与文件；成功后物理删除 torrents 与 torrent_upload_stats 中相关记录，
// 并把命中 hash 的自动发种记录标记为 retained（保留记录用于 RSS 去重，避免同一条目被重新下载）。
//
// 删除范围说明：「一种多站」列表的一行由「种子名 + 大小」聚合而来，本接口沿用同一口径：
//  1. 入参 hash 对应的记录；
//  2. 数据库中同内容的其它记录（含被隐藏的残留记录，避免下次刷新后复活）；
//  3. delete_files=true 时，还会核对所有启用下载器，把「同名同大小但尚未同步入库」的副本一并删除。
func (s *TorrentDataService) DeleteTorrentByHash(payload map[string]any) (map[string]any, int) {
	hashes := append(toStringSlice(payload["hashes"]), stringValue(payload["hash"], ""))
	hashes = compactStrings(hashes)
	if len(hashes) == 0 {
		return map[string]any{"success": false, "error": "缺少 hash 参数"}, 400
	}

	rows, err := s.repo.ListTorrentsByHashes(hashes)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}, 500
	}
	if len(rows) == 0 {
		return map[string]any{"success": false, "error": "未找到 hash 对应的种子记录"}, 404
	}

	deleteFiles := boolValue(payload["delete_files"])

	contentKeys := collectContentKeys(rows)
	matcher := newContentMatcher(contentKeys)

	// 待删记录 = 入参 hash + 同内容的所有记录（含隐藏记录）。
	deleteHashes := make([]string, 0, len(rows))
	for _, row := range rows {
		deleteHashes = append(deleteHashes, row.Hash)
	}
	groupHashes, err := s.repo.ListTorrentHashesByContent(contentKeys)
	if err != nil {
		logx.Warnf(torrentDataLogModule, "按内容读取同组记录失败 err=%v", err)
	} else {
		deleteHashes = append(deleteHashes, groupHashes...)
	}
	deleteHashes = compactStrings(deleteHashes)

	fileDeletedCount := int64(0)
	extraCopies := int64(0)
	warnings := []string{}
	if deleteFiles {
		knownHashes := make(map[string]struct{}, len(deleteHashes))
		for _, hash := range deleteHashes {
			knownHashes[strings.ToLower(strings.TrimSpace(hash))] = struct{}{}
		}

		outcome := s.deleteDownloaderCopies(matcher, rows, knownHashes)
		if len(outcome.requiredErrors) > 0 {
			message := strings.Join(outcome.requiredErrors, "；")
			if outcome.deletedCount > 0 {
				message = fmt.Sprintf("已有 %d 个下载器任务删除成功，但以下下载器未删除成功：%s", outcome.deletedCount, message)
			}
			logx.Warnf(torrentDataLogModule, "删除下载器任务失败 err=%s", message)
			return map[string]any{
				"success":                  false,
				"error":                    message,
				"file_delete_failed_count": len(outcome.requiredErrors),
				"file_delete_errors":       outcome.requiredErrors,
			}, 502
		}
		fileDeletedCount = outcome.deletedCount
		extraCopies = outcome.extraCopies
		warnings = outcome.warnings
		deleteHashes = compactStrings(append(deleteHashes, outcome.matchedHashes...))
	}

	deleted, err := s.repo.DeleteTorrentsByHashes(deleteHashes)
	if err != nil {
		return map[string]any{"success": false, "error": err.Error()}, 500
	}
	if deleted == 0 && fileDeletedCount == 0 {
		return map[string]any{"success": false, "error": "未删除任何种子记录"}, 404
	}

	// 只有真正把下载器任务删掉时才标记自动发种记录：
	// 「只删除记录」场景下种子仍在下载器里做种，标记 retained 会与事实不符。
	retainedCount := int64(0)
	if deleteFiles {
		retainedCount = s.markAutoSeedItemsRetained(deleteHashes)
	}

	message := fmt.Sprintf("已删除 %d 条种子记录", deleted)
	if deleteFiles {
		message = fmt.Sprintf("%s，并已删除 %d 个下载器任务和文件", message, fileDeletedCount)
		if extraCopies > 0 {
			message = fmt.Sprintf("%s（其中 %d 个是此前未同步到服务器的副本）", message, extraCopies)
		}
		if retainedCount > 0 {
			message = fmt.Sprintf("%s，同时已将 %d 条自动发种记录标记为已清理", message, retainedCount)
		}
	}
	return map[string]any{
		"success":                  true,
		"message":                  message,
		"deleted_count":            deleted,
		"file_deleted_count":       fileDeletedCount,
		"file_delete_failed_count": 0,
		"file_delete_errors":       []string{},
		"extra_copy_count":         extraCopies,
		"auto_seed_retained_count": retainedCount,
		"warnings":                 warnings,
	}, 200
}

// markAutoSeedItemsRetained 把已被删除种子的自动发种记录标记为 retained，返回标记条数。
// 参数/返回：hashes 为本次从下载器删除的种子 hash；返回被标记的记录数。
// 失败场景：仓储未注入或更新失败时记录告警日志并返回 0，不影响删除主流程。
// 副作用：更新 auto_seed_items 的状态，保留记录继续参与 RSS 去重，避免同一 RSS 条目被重新下载。
func (s *TorrentDataService) markAutoSeedItemsRetained(hashes []string) int64 {
	if s == nil || s.autoSeedRepo == nil || len(hashes) == 0 {
		return 0
	}
	count, err := s.autoSeedRepo.MarkItemsRetainedByHashes(hashes)
	if err != nil {
		logx.Warnf(torrentDataLogModule, "标记自动发种记录为 retained 失败 err=%v", err)
		return 0
	}
	if count > 0 {
		logx.Infof(torrentDataLogModule, "自动发种记录已标记为 retained count=%d", count)
	}
	return count
}

// deleteDownloaderOutcome 汇总下载器侧的副本核对与删除结果。
type deleteDownloaderOutcome struct {
	// requiredErrors 为「数据库中记录了该内容」的下载器删除失败明细，任一非空即视为整体失败。
	requiredErrors []string
	// warnings 为非必需下载器的降级提示（仅用于核对未同步副本，失败不阻塞删除）。
	warnings []string
	// matchedHashes 为下载器侧核对出的、与目标内容同名的全部 hash。
	matchedHashes []string
	// deletedCount 为实际请求删除的下载器任务数。
	deletedCount int64
	// extraCopies 为其中此前未入库（未同步到服务器）的副本数。
	extraCopies int64
}

// deleteDownloaderCopies 在下载器侧核对同一内容的副本并删除对应任务。
// 参数/返回：matcher 为内容匹配器；rows 为数据库已知记录；knownHashes 为已入库 hash 集合；返回删除统计与错误明细。
// 失败场景：必需下载器（数据库记录所属）返回错误会记入 requiredErrors；其余下载器失败仅记 warnings。
// 副作用：向下载器发起只读遍历与删除请求，会删除任务与本地文件。
func (s *TorrentDataService) deleteDownloaderCopies(matcher contentMatcher, rows []repository.TorrentRecord, knownHashes map[string]struct{}) deleteDownloaderOutcome {
	outcome := deleteDownloaderOutcome{
		requiredErrors: []string{},
		warnings:       []string{},
		matchedHashes:  []string{},
	}

	settings := s.rootConfig()
	knownByDownloader := map[string][]string{}
	for _, row := range rows {
		downloaderID := strings.TrimSpace(row.Downloader)
		if downloaderID == "" {
			continue
		}
		knownByDownloader[downloaderID] = append(knownByDownloader[downloaderID], row.Hash)
	}

	requiredIDs := make([]string, 0, len(knownByDownloader))
	for id := range knownByDownloader {
		requiredIDs = append(requiredIDs, id)
	}
	sort.Strings(requiredIDs)
	if len(requiredIDs) == 0 {
		outcome.requiredErrors = append(outcome.requiredErrors, "种子记录缺少下载器信息，无法删除下载器任务和文件")
		return outcome
	}

	requiredSet := map[string]struct{}{}
	for _, id := range requiredIDs {
		requiredSet[id] = struct{}{}
	}

	// 其余启用下载器只承担「捞回未同步副本」的职责，失败不影响本次删除。
	optionalIDs := []string{}
	if !matcher.empty() {
		for _, id := range collectDownloaderScopes(settings).enabledIDs {
			if _, exists := requiredSet[id]; exists {
				continue
			}
			optionalIDs = append(optionalIDs, id)
		}
		sort.Strings(optionalIDs)
	}

	type probeTarget struct {
		id        string
		required  bool
		fallbacks []string
	}
	targets := make([]probeTarget, 0, len(requiredIDs)+len(optionalIDs))
	for _, id := range requiredIDs {
		targets = append(targets, probeTarget{id: id, required: true, fallbacks: knownByDownloader[id]})
	}
	for _, id := range optionalIDs {
		targets = append(targets, probeTarget{id: id, required: false})
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, target := range targets {
		wg.Add(1)
		go func(target probeTarget) {
			defer wg.Done()
			result := s.deleteOneDownloaderGroup(settings, target.id, target.fallbacks, matcher, knownHashes)

			mu.Lock()
			defer mu.Unlock()
			if result.err != nil {
				detail := fmt.Sprintf("「%s」%v", result.label, result.err)
				if target.required {
					outcome.requiredErrors = append(outcome.requiredErrors, detail)
				} else {
					outcome.warnings = append(outcome.warnings, detail)
				}
			}
			outcome.deletedCount += result.deletedCount
			outcome.extraCopies += result.extraCopies
			outcome.matchedHashes = append(outcome.matchedHashes, result.matchedHashes...)
		}(target)
	}
	wg.Wait()

	logx.Infof(torrentDataLogModule, "下载器副本核对完成 targets=%d deleted=%d matched=%d extra=%d warnings=%d",
		len(targets), outcome.deletedCount, len(outcome.matchedHashes), outcome.extraCopies, len(outcome.warnings))

	sort.Strings(outcome.requiredErrors)
	sort.Strings(outcome.warnings)
	return outcome
}

// downloaderGroupResult 描述单个下载器的副本核对与删除结果。
type downloaderGroupResult struct {
	label         string
	matchedHashes []string
	deletedCount  int64
	extraCopies   int64
	err           error
}

// deleteOneDownloaderGroup 核对单个下载器上的同内容副本并删除对应任务。
// 参数/返回：settings 为当前配置快照；fallbacks 为数据库中已知属于该下载器的 hash；knownHashes 为已入库 hash 集合；返回匹配结果与删除数量。
// 失败场景：下载器配置无效、读取种子列表失败且无兜底、删除请求失败时返回 error。
// 副作用：向下载器发起删除请求，会删除任务与本地文件。
func (s *TorrentDataService) deleteOneDownloaderGroup(settings map[string]any, downloaderID string, fallbacks []string, matcher contentMatcher, knownHashes map[string]struct{}) downloaderGroupResult {
	result := downloaderGroupResult{
		label:         downloaderDisplayName(settings, downloaderID),
		matchedHashes: []string{},
	}

	downloader, err := downloaderclient.FromConfig(settings, downloaderID)
	if err != nil {
		result.err = err
		return result
	}

	hashes := compactStrings(fallbacks)
	snapshots, fetchErr := downloader.FetchTorrents()
	if fetchErr != nil {
		// 核对失败时退化为按数据库已知 hash 删除，避免因读取异常导致完全删不掉。
		if len(hashes) == 0 {
			result.err = fmt.Errorf("读取下载器种子列表失败: %w", fetchErr)
			return result
		}
		logx.Warnf(torrentDataLogModule, "读取下载器种子列表失败，退化为按数据库记录删除 downloader_id=%s err=%v", downloaderID, fetchErr)
	} else {
		matched := matcher.matchSnapshots(snapshots)
		result.matchedHashes = matched
		for _, hash := range matched {
			if _, exists := knownHashes[strings.ToLower(strings.TrimSpace(hash))]; !exists {
				result.extraCopies++
			}
		}
		hashes = compactStrings(append(hashes, matched...))
	}

	if len(hashes) == 0 {
		return result
	}

	logx.Infof(torrentDataLogModule, "请求删除下载器任务 downloader_id=%s hashes=%d", downloaderID, len(hashes))
	if err := downloader.DeleteTorrents(hashes, true); err != nil {
		result.err = err
		return result
	}
	result.deletedCount = int64(len(hashes))
	logx.Infof(torrentDataLogModule, "下载器任务删除请求已完成 downloader_id=%s hashes=%d", downloaderID, len(hashes))
	return result
}

// contentMatcher 按「种子名 + 大小」匹配同内容的种子副本。
type contentMatcher struct {
	fingerprints map[string]struct{}
}

func newContentMatcher(keys []repository.TorrentContentKey) contentMatcher {
	fingerprints := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		fingerprints[contentFingerprint(key.Name, key.Size)] = struct{}{}
	}
	return contentMatcher{fingerprints: fingerprints}
}

func (m contentMatcher) empty() bool {
	return len(m.fingerprints) == 0
}

// matchSnapshots 从下载器快照中挑出与目标内容同名同大小的副本 hash。
func (m contentMatcher) matchSnapshots(snapshots []downloaderclient.TorrentSnapshot) []string {
	if m.empty() {
		return []string{}
	}
	result := make([]string, 0, 2)
	for _, snapshot := range snapshots {
		hash := strings.TrimSpace(snapshot.Hash)
		if hash == "" {
			continue
		}
		if _, exists := m.fingerprints[contentFingerprint(snapshot.Name, snapshot.Size)]; !exists {
			continue
		}
		result = append(result, hash)
	}
	return compactStrings(result)
}

// contentFingerprint 生成内容指纹，口径与列表页「种子名 + 大小」聚合保持一致。
func contentFingerprint(name string, size int64) string {
	return fmt.Sprintf("%s\x00%d", strings.TrimSpace(name), size)
}

func collectContentKeys(rows []repository.TorrentRecord) []repository.TorrentContentKey {
	result := make([]repository.TorrentContentKey, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" || row.Size <= 0 {
			continue
		}
		fingerprint := contentFingerprint(name, row.Size)
		if _, exists := seen[fingerprint]; exists {
			continue
		}
		seen[fingerprint] = struct{}{}
		result = append(result, repository.TorrentContentKey{Name: name, Size: row.Size})
	}
	return result
}

// downloaderDisplayName 读取下载器展示名，缺失时回退为 ID，便于日志与前端提示。
func downloaderDisplayName(settings map[string]any, downloaderID string) string {
	for _, raw := range toSlice(settings["downloaders"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(toString(item["id"], "")) != downloaderID {
			continue
		}
		if name := strings.TrimSpace(toString(item["name"], "")); name != "" {
			return name
		}
	}
	return downloaderID
}

func (s *TorrentDataService) rootConfig() map[string]any {
	if s == nil || s.cfg == nil {
		return map[string]any{}
	}
	return s.cfg.Get()

}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(typed))
		return trimmed == "true" || trimmed == "1" || trimmed == "yes" || trimmed == "on"
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	default:
		return false
	}

}

func compactStrings(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
