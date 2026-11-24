package torrentdata

import (
	"fmt"
	"strings"
)

// DeleteSiteLink 删除「一种多站」展开行里某个站点的详情链接地址。
// 参数/返回：payload 需含 name、size、site；返回接口响应体与 HTTP 状态码。
// 失败场景：缺少参数返回 400；该站点在这颗种子下没有可删的链接返回 404；数据库失败返回 500。
// 副作用：只把 torrents.details（即站点详情链接）置空，做种记录、发种日志与下载器任务/文件都不受影响。
func (s *TorrentDataService) DeleteSiteLink(payload map[string]any) (map[string]any, int) {
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

	cleared, err := s.repo.ClearSiteLinkByNameAndSize(name, size, site)
	if err != nil {
		return map[string]any{"success": false, "error": "删除站点链接失败: " + err.Error()}, 500
	}
	if cleared == 0 {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("站点「%s」在这颗种子下没有可删除的链接地址", site),
		}, 404
	}

	return map[string]any{
		"success":       true,
		"cleared_count": cleared,
		"message":       fmt.Sprintf("已删除站点「%s」的链接地址", site),
	}, 200
}
