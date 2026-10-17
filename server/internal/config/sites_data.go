package config

import (
	"encoding/json"
	"os"
	"strings"
)

// SitesDataRow 是 sites_data.json 里单个站点的元数据（只声明本包需要的字段）。
//
// ⚠️ 该文件是「站点注册表」：站点身份、migration 角色、限速等都从这里同步进 sites 表。
// 这里额外读取 allow_episode_publish（分集发布的站点级开关），因为发布链路
// （service/publish/workflow）不能反向依赖 repository 包，而该开关需要在发布时实时查询。
//
// JSON 不支持注释，字段缺失一律按零值处理（allow_episode_publish 即 false）。
type SitesDataRow struct {
	Site  string `json:"site"`
	Nick  string `json:"nickname"`
	Allow bool   `json:"allow_episode_publish"`
}

// SiteAllowsEpisodePublish 读取站点在 sites_data.json 中声明的分集发布开关。
// 参数/返回：siteCode 为站点标识（大小写不敏感，可传 site 或 nickname）；返回是否允许分集发布。
//
// 说明：
//  1. 默认 false —— JSON 没写该字段即为「不允许分集发布」（fail-closed）；
//  2. 匹配顺序与 SyncSitesFromJSON 一致：先按 site（统一小写）匹配，再按 nickname（原样）匹配；
//  3. 读文件失败同样按 false 处理 —— 配置不可读时不得放行。
//
// 副作用：每次调用都会读一次 sites_data.json。该文件仅在发布时查询，
// 读取开销可忽略；这样修改 JSON 后无需重启服务即可生效。
func SiteAllowsEpisodePublish(siteCode string) bool {
	code := strings.TrimSpace(siteCode)
	if code == "" {
		return false
	}
	rows, err := loadSitesDataRows()
	if err != nil {
		return false
	}
	for _, row := range rows {
		if strings.ToLower(strings.TrimSpace(row.Site)) == strings.ToLower(code) {
			return row.Allow
		}
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Nick) == code {
			return row.Allow
		}
	}
	return false
}

// loadSitesDataRows 读取 sites_data.json 的全部站点条目。
// 参数/返回：无；返回站点条目列表与错误。
// 副作用：无（每次调用重新读文件，见上方说明）。
func loadSitesDataRows() ([]SitesDataRow, error) {
	paths := ResolveRuntimePaths()
	content, err := os.ReadFile(paths.SitesData)
	if err != nil {
		return nil, err
	}
	rows := make([]SitesDataRow, 0)
	if err := json.Unmarshal(content, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}
