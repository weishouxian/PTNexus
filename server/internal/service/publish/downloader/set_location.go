package downloader

import (
	"strings"

	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/downloaderclient"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// SetTorrentLocation 更改下载器中已做种任务的保存位置（移动文件）。
// 参数/返回：payload 需包含 hashes（可单个 hash 或多个 hash，同一资源的全部站点种子）与 new_path；
// torrentDataRepo 用于按 hash 反查各任务归属的下载器；rootConfig 为全局配置；返回接口响应与状态码。
// 失败场景：缺少参数、下载器配置错误、移动失败时返回对应错误。
// 副作用：会向一个或多个下载器发起移动文件请求，把同资源的所有站点种子一并移动。
//
// 说明：同一资源（种子名+体积相同）在不同站点各自对应一个 hash，且可能落在不同下载器上。
// 因此这里按 hash → downloader 分组，对每个下载器分别调用 SetLocation，保证「改一个种子、同资源其余站点种子一起移动」。
func SetTorrentLocation(payload map[string]any, rootConfig map[string]any, torrentDataRepo *repository.TorrentDataRepository) (map[string]any, int) {
	hashes := compactHashes(append(processingshared.ToStringSlice(payload["hashes"]), processingshared.ToString(payload["hash"], "")))
	newPath := strings.TrimSpace(processingshared.ToString(payload["new_path"], processingshared.ToString(payload["newPath"], "")))

	if len(hashes) == 0 || newPath == "" {
		return map[string]any{"success": false, "message": "缺少必要参数: hashes, new_path"}, 400
	}

	// 按 hash 反查归属下载器：优先用数据库记录；数据库查不到时回退到显式传入的 downloader_id。
	hashesByDownloader := map[string][]string{}
	fallbackID := strings.TrimSpace(processingshared.ToString(payload["downloader_id"], processingshared.ToString(payload["downloaderId"], "")))

	if torrentDataRepo != nil {
		rows, err := torrentDataRepo.ListTorrentsByHashes(hashes)
		if err != nil {
			return map[string]any{"success": false, "message": "查询种子记录失败: " + err.Error()}, 500
		}
		known := map[string]struct{}{}
		for _, row := range rows {
			key := strings.ToLower(strings.TrimSpace(row.Hash))
			known[key] = struct{}{}
			dl := strings.TrimSpace(row.Downloader)
			if dl == "" {
				continue
			}
			hashesByDownloader[dl] = append(hashesByDownloader[dl], row.Hash)
		}
		// 数据库里没有记录的 hash（如刚同步到下载器但尚未入库），归到显式 downloader_id。
		var orphans []string
		for _, h := range hashes {
			if _, ok := known[strings.ToLower(strings.TrimSpace(h))]; !ok {
				orphans = append(orphans, h)
			}
		}
		if len(orphans) > 0 {
			if fallbackID == "" {
				return map[string]any{"success": false, "message": "部分种子未关联下载器，无法确定目标下载器"}, 400
			}
			hashesByDownloader[fallbackID] = append(hashesByDownloader[fallbackID], orphans...)
		}
	} else {
		if fallbackID == "" {
			return map[string]any{"success": false, "message": "缺少必要参数: downloader_id"}, 400
		}
		hashesByDownloader[fallbackID] = hashes
	}

	if len(hashesByDownloader) == 0 {
		return map[string]any{"success": false, "message": "没有可移动的种子任务"}, 400
	}

	movedHashes := make([]string, 0, len(hashes))
	var firstErr string
	var dbWarn string
	for downloaderID, group := range hashesByDownloader {
		downloader, err := downloaderclient.FromConfig(rootConfig, downloaderID)
		if err != nil {
			if firstErr == "" {
				firstErr = "下载器 " + downloaderID + " 配置错误: " + err.Error()
			}
			continue
		}
		if err := downloader.SetLocation(compactHashes(group), newPath); err != nil {
			if firstErr == "" {
				firstErr = "下载器 " + downloaderID + " 更改保存位置失败: " + err.Error()
			}
			continue
		}
		movedHashes = append(movedHashes, group...)
		// 移动成功后同步更新数据库 save_path，避免列表刷新后仍显示旧路径。
		if torrentDataRepo != nil {
			if _, dbErr := torrentDataRepo.UpdateSavePathByHashes(group, newPath); dbErr != nil && dbWarn == "" {
				// 文件已移动成功，数据库更新失败仅作为告警提示，不判为整体失败。
				dbWarn = "已移动文件，但更新本地保存路径记录失败: " + dbErr.Error()
			}
		}
	}

	if firstErr != "" {
		return map[string]any{
			"success":      false,
			"message":      firstErr,
			"moved_hashes": movedHashes,
			"moved_count":  len(movedHashes),
			"new_path":     newPath,
		}, 500
	}

	message := "保存位置已更改"
	if dbWarn != "" {
		message += "；" + dbWarn
	}
	return map[string]any{
		"success":      true,
		"message":      message,
		"moved_hashes": movedHashes,
		"moved_count":  len(movedHashes),
		"new_path":     newPath,
	}, 200
}

// compactHashes 去空、去重（大小写不敏感，统一保留原值）。
func compactHashes(items []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out
}
