package torrentdata

import (
	"fmt"
	"strings"
)

func (s *TorrentDataService) GetCachedSites(name string, size int64) (map[string]any, int) {
	if strings.TrimSpace(name) == "" {
		return map[string]any{"error": "缺少必要参数：name"}, 400
	}
	if size <= 0 {
		return map[string]any{"error": "size参数必须是整数"}, 400
	}
	cachedSites, matchedHashes, err := s.repo.CachedSitesByNameAndSize(name, size)
	if err != nil {
		return map[string]any{"error": "查询缓存站点失败", "success": false}, 500
	}
	return map[string]any{"success": true, "cached_sites": cachedSites, "matched_hashes": matchedHashes}, 200
}

// DeleteCachedSite 删除「一种多站」源站点弹窗中某个站点在该种子下的缓存参数。
// 参数/返回：payload 需含 name、size、site；返回接口响应体与 HTTP 状态码。
// 失败场景：缺少必要参数返回 400；该种子未命中任何缓存 hash、或该站点没有缓存记录返回 404；数据库失败返回 500。
// 副作用：物理删除 seed_parameters 中该站点在该种子下的记录，其它站点的缓存保持不变；
// 删除后该站点会从「已缓存」落到「未缓存」，可重新抓取数据。
func (s *TorrentDataService) DeleteCachedSite(payload map[string]any) (map[string]any, int) {
	name := strings.TrimSpace(toString(payload["name"], ""))
	site := strings.TrimSpace(toString(payload["site"], ""))
	size := int64Value(payload["size"], 0)

	if name == "" {
		return map[string]any{"success": false, "error": "缺少必要参数：name"}, 400
	}
	if site == "" {
		return map[string]any{"success": false, "error": "缺少必要参数：site"}, 400
	}
	if size <= 0 {
		return map[string]any{"success": false, "error": "size参数必须是整数"}, 400
	}

	// 复用列表页同一口径：先按「种子名 + 大小」拿到命中的 hash 集合，再只删该站点这一列。
	_, matchedHashes, err := s.repo.CachedSitesByNameAndSize(name, size)
	if err != nil {
		return map[string]any{"success": false, "error": "查询缓存站点失败"}, 500
	}
	if len(matchedHashes) == 0 {
		return map[string]any{"success": false, "error": "未找到该种子的缓存数据"}, 404
	}

	deleted, err := s.repo.DeleteCachedSitesByHashesAndSite(matchedHashes, site)
	if err != nil {
		return map[string]any{"success": false, "error": "删除缓存数据失败"}, 500
	}
	if deleted == 0 {
		return map[string]any{"success": false, "error": fmt.Sprintf("未找到站点「%s」的缓存数据", site)}, 404
	}

	return map[string]any{
		"success":       true,
		"deleted_count": deleted,
		"message":       fmt.Sprintf("已删除站点「%s」的 %d 条缓存数据", site, deleted),
	}, 200
}
