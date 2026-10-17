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

// EnvAllowEpisodePublish 是「分集发布」全局总开关的环境变量名。
//
// 它是整个应用能否处理分集资源的第一道闸门，取值口径与项目既有环境变量一致
// （UPLOAD_TEST_MODE / DEV_ENV 等都是 os.Getenv(x) == "true"）。
//
// 影响面（两处消费方，都以此为准，不要各写一份）：
//  1. 自动发种下载：为 false 时带「分集」标签/RSS 标题含集号的条目一律不下载；
//  2. 人工/队列发布：为 false 时任何站点都不允许发分集，
//     为 true 时还要再看目标站点的 sites_data.json allow_episode_publish。
const EnvAllowEpisodePublish = "ALLOW_EPISODE_PUBLISH"

// AllowsEpisodePublish 读取分集发布的全局总开关。
// 参数/返回：无；返回环境变量是否等于 "true"。
// 说明：fail-closed —— 只有显式等于 "true" 才算开启；空值、1、yes 等一律视为关闭，
// 这样即便 .env 漏配或拼错，也不会意外放开分集资源。
// 副作用：无（进程启动后环境变量不再变化，无需缓存）。
func AllowsEpisodePublish() bool {
	return os.Getenv(EnvAllowEpisodePublish) == "true"
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
