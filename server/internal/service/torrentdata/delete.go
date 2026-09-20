package torrentdata

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
)

const torrentDataLogModule = "一种多站"

// 下载器配置项：删除前是否先拉取该下载器种子列表，把同名同大小的其它副本一并删除。
// 默认关闭（字段缺失即视为关闭），表示只按数据库已知记录删除。
const downloaderFetchCopiesBeforeDeleteKey = "fetch_copies_before_delete"

// DeleteTorrentByHash 删除「一种多站」中指定内容，可选同步删除下载器任务与文件。
// 参数/返回：payload.hash/hashes 为 torrents.hash；delete_files 控制是否删除下载器任务和文件；返回接口响应体与 HTTP 状态码。
// 失败场景：缺少 hash、未找到 torrents 记录、下载器删除失败或数据库删除失败时返回错误响应。
// 副作用：delete_files=true 时向种子所属下载器下发删除任务（会删除任务与本地文件）；随后物理删除 torrents 与 torrent_upload_stats 中相关记录，
// 并把命中 hash 的自动发种记录标记为 retained（保留记录用于 RSS 去重，避免同一条目被重新下载）。
//
// 删除范围说明：「一种多站」列表的一行由「种子名 + 大小」聚合而来，本接口沿用同一口径，只处理数据库中已有记录的 hash：
//  1. 入参 hash 对应的记录；
//  2. 数据库中同内容的其它记录（含被隐藏的残留记录，避免下次刷新后复活）。
//
// 若下载器配置开启 fetch_copies_before_delete，还会在删除前读取**该种子所属下载器**的种子列表，
// 把「同名同大小但尚未同步入库」的副本一并删除（核对范围仅限该下载器，不遍历其它下载器）。
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

	// 待删记录 = 入参 hash + 同内容的所有记录（含隐藏记录）。
	deleteHashes := make([]string, 0, len(rows))
	for _, row := range rows {
		deleteHashes = append(deleteHashes, row.Hash)
	}
	contentKeys := collectContentKeys(rows)
	groupHashes, err := s.repo.ListTorrentHashesByContent(contentKeys)
	if err != nil {
		logx.Warnf(torrentDataLogModule, "按内容读取同组记录失败 err=%v", err)
	} else {
		deleteHashes = append(deleteHashes, groupHashes...)
	}
	deleteHashes = compactStrings(deleteHashes)

	fileDeletedCount := int64(0)
	extraCopies := int64(0)
	failures := []string{}
	if deleteFiles {
		deletedCount, extra, failed := s.deleteDownloaderTorrents(rows, deleteHashes, newContentMatcher(contentKeys))
		fileDeletedCount = deletedCount
		extraCopies = extra
		failures = failed
		if len(failures) > 0 {
			message := strings.Join(failures, "；")
			if deletedCount > 0 {
				message = fmt.Sprintf("已有 %d 个下载器任务删除成功，但以下下载器未删除成功：%s", deletedCount, message)
			}
			logx.Warnf(torrentDataLogModule, "删除下载器任务失败 err=%s", message)
			return map[string]any{
				"success":                  false,
				"error":                    message,
				"file_delete_failed_count": len(failures),
				"file_delete_errors":       failures,
			}, 502
		}
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

// deleteDownloaderTorrents 向种子所属下载器下发删除任务，返回删除任务数、未入库副本数与失败明细。
// 参数/返回：rows 为数据库已知记录（携带 downloader 归属）；hashes 为本次要删除的全部 hash；
// matcher 为「名称+大小」内容匹配器；返回 (删除任务数, 未入库副本数, 失败明细)。
// 失败场景：记录缺少下载器信息、下载器配置无效、删除请求失败，或开启副本核对后读取种子列表失败时，对应下载器记入失败明细。
// 副作用：向下载器发起删除请求，会删除任务与本地文件；开启副本核对时会额外读取一次该下载器的种子列表。
func (s *TorrentDataService) deleteDownloaderTorrents(rows []repository.TorrentRecord, hashes []string, matcher contentMatcher) (int64, int64, []string) {
	settings := s.rootConfig()
	knownHashes := make([]string, 0, len(hashes))
	for _, hash := range hashes {
		knownHashes = append(knownHashes, normalizeHash(hash))
	}
	knownHashes = compactStrings(knownHashes)
	knownSet := make(map[string]struct{}, len(knownHashes))
	for _, hash := range knownHashes {
		knownSet[hash] = struct{}{}
	}

	hashesByDownloader := map[string][]string{}
	for _, row := range rows {
		downloaderID := strings.TrimSpace(row.Downloader)
		if downloaderID == "" {
			continue
		}
		hashesByDownloader[downloaderID] = append(hashesByDownloader[downloaderID], row.Hash)
	}
	if len(hashesByDownloader) == 0 {
		return 0, 0, []string{"种子记录缺少下载器信息，无法删除下载器任务和文件"}
	}

	// 按下载器 ID 排序，保证多下载器场景下日志与失败明细顺序稳定。
	downloaderIDs := make([]string, 0, len(hashesByDownloader))
	for id := range hashesByDownloader {
		downloaderIDs = append(downloaderIDs, id)
	}
	sort.Strings(downloaderIDs)

	deletedCount := int64(0)
	extraCopies := int64(0)
	failures := []string{}
	for _, downloaderID := range downloaderIDs {
		label := downloaderDisplayName(settings, downloaderID)
		downloader, err := downloaderclient.FromConfig(settings, downloaderID)
		if err != nil {
			failures = append(failures, fmt.Sprintf("「%s」%v", label, err))
			continue
		}

		// 入参 hash 也一并下发：部分 hash 只存在于被隐藏的记录中，按分组拿不到归属，
		// 全量下发可覆盖「数据库记录已隐藏但下载器任务仍在」的情况。
		targets := compactStrings(append(append([]string{}, hashesByDownloader[downloaderID]...), knownHashes...))

		if downloaderFetchCopiesBeforeDelete(settings, downloaderID) && !matcher.empty() {
			snapshots, fetchErr := downloader.FetchTorrents()
			if fetchErr != nil {
				// 用户显式开启了口径核对，读取失败时不静默降级，否则会误以为已删干净。
				failures = append(failures, fmt.Sprintf("「%s」读取种子列表失败: %v", label, fetchErr))
				continue
			}
			matched := matcher.matchSnapshots(snapshots)
			extra := int64(0)
			for _, hash := range matched {
				if _, exists := knownSet[normalizeHash(hash)]; !exists {
					extra++
				}
			}
			extraCopies += extra
			targets = compactStrings(append(targets, matched...))
			logx.Infof(torrentDataLogModule, "删除前副本核对完成 downloader_id=%s matched=%d extra=%d", downloaderID, len(matched), extra)
		}

		if len(targets) == 0 {
			continue
		}
		logx.Infof(torrentDataLogModule, "请求删除下载器任务 downloader_id=%s hashes=%d", downloaderID, len(targets))
		if err := downloader.DeleteTorrents(targets, true); err != nil {
			failures = append(failures, fmt.Sprintf("「%s」%v", label, err))
			continue
		}
		deletedCount += int64(len(targets))
		logx.Infof(torrentDataLogModule, "下载器任务删除请求已完成 downloader_id=%s hashes=%d", downloaderID, len(targets))
	}

	sort.Strings(failures)
	return deletedCount, extraCopies, failures
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

// collectContentKeys 汇总记录的「名称+大小」内容标识，用于捞出同内容（含隐藏）的其它记录。
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

// findDownloaderConfig 从配置快照中取出指定下载器的配置项，未命中返回 nil。
func findDownloaderConfig(settings map[string]any, downloaderID string) map[string]any {
	for _, raw := range toSlice(settings["downloaders"]) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if strings.TrimSpace(toString(item["id"], "")) != downloaderID {
			continue
		}
		return item
	}
	return nil
}

// downloaderFetchCopiesBeforeDelete 读取下载器的「删除前获取其他副本」开关，字段缺失或类型异常时按关闭处理。
func downloaderFetchCopiesBeforeDelete(settings map[string]any, downloaderID string) bool {
	item := findDownloaderConfig(settings, downloaderID)
	if item == nil {
		return false
	}
	return boolValue(item[downloaderFetchCopiesBeforeDeleteKey])
}

// downloaderDisplayName 读取下载器展示名，缺失时回退为 ID，便于日志与前端提示。
func downloaderDisplayName(settings map[string]any, downloaderID string) string {
	item := findDownloaderConfig(settings, downloaderID)
	if item == nil {
		return downloaderID
	}
	if name := strings.TrimSpace(toString(item["name"], "")); name != "" {
		return name
	}
	return downloaderID
}

// normalizeHash 统一 hash 比较口径：去空格并转小写。
func normalizeHash(hash string) string {
	return strings.ToLower(strings.TrimSpace(hash))
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
