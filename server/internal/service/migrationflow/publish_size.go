package migrationflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

const publishSizeModule = "发布-大小"

// InitPublishSizeSource 注入种子体积来源（torrents 表），用于发布进度 / 发种日志列表展示「大小」。
// 参数/返回：repo 为种子数据仓储；无返回值。
// 失败场景：repo 为空时仅记录日志并跳过注入，列表照常可用（不显示大小）。
// 副作用：无（只保存依赖）。
func (s *MigrateService) InitPublishSizeSource(repo *repository.TorrentDataRepository) {
	if s == nil {
		return
	}
	if repo == nil {
		logx.Warnf(publishSizeModule, "初始化体积来源失败：torrentDataRepo 为空")
		return
	}
	s.torrentDataRepo = repo
}

// formatPublishSizeText 把字节数格式化为人类可读文本（与「一种多站」列表口径一致）。
// 参数/返回：size 为字节数（<=0 视为未知）；返回如 "12.50 GB"，未知时返回空串。
// 失败场景：无。
// 副作用：无。
func formatPublishSizeText(size int64) string {
	if size <= 0 {
		return ""
	}
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	value := float64(size)
	idx := 0
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	if idx == 0 {
		return fmt.Sprintf("%d %s", int64(value), units[idx])
	}
	return fmt.Sprintf("%.2f %s", value, units[idx])
}

// extractPublishContextHash 从发布队列任务的 context_json 中取出种子 infohash。
// 参数/返回：contextJSON 为 publish_queue_tasks.context_json；返回 trim 后的 Hash，解析失败或缺失时返回空串。
// 失败场景：无（JSON 非法按缺失处理）。
// 副作用：无。
func extractPublishContextHash(contextJSON string) string {
	trimmed := strings.TrimSpace(contextJSON)
	if trimmed == "" {
		return ""
	}
	payload := struct {
		Hash string `json:"Hash"`
	}{}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Hash)
}

// fillPublishQueueTaskSizes 为发布队列任务列表回填种子体积（按 context_json 里的 Hash 关联 torrents.size）。
// 参数/返回：rows 为队列任务切片（原地修改）；无返回值。
// 失败场景：体积来源未注入或查询失败时仅记录日志，不返回错误（列表仍正常展示）。
// 副作用：只读取 torrents；不改写数据库。
//
// 说明：context_json 是发布上下文的快照，实测覆盖率为 100%，因此进度页的大小基本都能显示；
// 已从下载器删除的种子同样会被 SizeByHashes 命中（该方法不过滤 is_hidden）。
func (s *MigrateService) fillPublishQueueTaskSizes(rows []repository.PublishQueueTask) {
	if s == nil || s.torrentDataRepo == nil || len(rows) == 0 {
		return
	}

	hashes := make([]string, 0, len(rows))
	for i := range rows {
		if hash := extractPublishContextHash(rows[i].ContextJSON); hash != "" {
			hashes = append(hashes, hash)
		}
	}
	if len(hashes) == 0 {
		return
	}

	sizes, err := s.torrentDataRepo.SizeByHashes(hashes)
	if err != nil {
		logx.Warnf(publishSizeModule, "查询队列任务种子体积失败 err=%v", err)
		return
	}
	for i := range rows {
		hash := strings.ToLower(extractPublishContextHash(rows[i].ContextJSON))
		if hash == "" {
			continue
		}
		size, ok := sizes[hash]
		if !ok || size <= 0 {
			continue
		}
		rows[i].Size = size
		rows[i].SizeFormatted = formatPublishSizeText(size)
	}
}

// fillPublishLogSizes 为发种日志列表回填种子体积。
// 参数/返回：rows 为发种日志切片（原地修改）；无返回值。
// 失败场景：体积来源/仓储未注入或查询失败时仅记录日志，不返回错误（列表仍正常展示）。
// 副作用：只读取 seed_parameters 与 torrents；不改写数据库。
//
// 关联口径：publish_logs 没有 hash 字段，只能按 torrent_id 反查 seed_parameters.hash，
// 再按 hash 取 torrents.size；实测历史数据约 63% 可命中，剩余记录（多为种子已彻底移除）保持未填，前端显示「-」。
func (s *MigrateService) fillPublishLogSizes(rows []repository.PublishLogEntry) {
	if s == nil || s.torrentDataRepo == nil || s.repo == nil || len(rows) == 0 {
		return
	}

	torrentIDs := make([]string, 0, len(rows))
	for i := range rows {
		if id := strings.TrimSpace(rows[i].TorrentID); id != "" {
			torrentIDs = append(torrentIDs, id)
		}
	}
	if len(torrentIDs) == 0 {
		return
	}

	hashByTorrentID, err := s.repo.SeedParameterHashesByTorrentIDs(torrentIDs)
	if err != nil {
		logx.Warnf(publishSizeModule, "按 torrent_id 反查种子 hash 失败 err=%v", err)
		return
	}
	if len(hashByTorrentID) == 0 {
		return
	}

	hashes := make([]string, 0, len(hashByTorrentID))
	for _, hash := range hashByTorrentID {
		hashes = append(hashes, hash)
	}
	sizes, err := s.torrentDataRepo.SizeByHashes(hashes)
	if err != nil {
		logx.Warnf(publishSizeModule, "查询发种日志种子体积失败 err=%v", err)
		return
	}

	for i := range rows {
		hash := hashByTorrentID[strings.TrimSpace(rows[i].TorrentID)]
		if hash == "" {
			continue
		}
		size, ok := sizes[hash]
		if !ok || size <= 0 {
			continue
		}
		rows[i].Size = size
		rows[i].SizeFormatted = formatPublishSizeText(size)
	}
}
