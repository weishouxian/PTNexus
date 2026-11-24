package iyuu

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
)

// SiteCacheEntry 是一条按「上报站点集合」缓存的 sid_sha1 快照。
type SiteCacheEntry struct {
	Timestamp      string   `json:"timestamp"`
	SIDSHA1        string   `json:"sid_sha1"`
	SupportedSites []Site   `json:"supported_sites"`
	SitesList      []string `json:"sites_list"`
}

// SiteCacheFile 是 data 目录下的多条目缓存结构。
// 之所以按集合分条目：sid_sha1 与上报的站点集合绑定，IYUU 查询（本地已有站点）与
// 辅种（额外并入目标站）上报集合不同，共用一个 key 会互相踢掉缓存。
type SiteCacheFile struct {
	Entries map[string]SiteCacheEntry `json:"entries"`
}

// CacheExpiryDays 是站点缓存的过期天数。
const CacheExpiryDays = 7

// maxCacheEntries 限制缓存条目数，避免站点集合组合过多导致文件膨胀。
const maxCacheEntries = 8

// CacheFilePath 返回缓存文件路径（data/iyuu_site_cache.json）。
func CacheFilePath() string {
	paths := config.ResolveRuntimePaths()
	return filepath.Join(paths.DataDir, "iyuu_site_cache.json")
}

// LoadSiteCache 按站点集合读取缓存。
// 参数/返回：sites 为本次要上报的站点集合；返回 (sid_sha1, sites, valid)。
// 失败场景：文件不存在/损坏/过期/该集合无缓存时返回 valid=false。
// 副作用：读取本地文件。
func LoadSiteCache(sites []string) (string, []Site, bool) {
	path := CacheFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", nil, false
	}
	var cache SiteCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return "", nil, false
	}
	entry, ok := cache.Entries[cacheKey(sites)]
	if !ok {
		return "", nil, false
	}
	if entry.SIDSHA1 == "" || entry.Timestamp == "" {
		return "", nil, false
	}
	ts, err := time.Parse(time.RFC3339, entry.Timestamp)
	if err != nil {
		ts, err = time.Parse("2006-01-02 15:04:05", entry.Timestamp)
		if err != nil {
			return "", nil, false
		}
	}
	if time.Since(ts) > (CacheExpiryDays * 24 * time.Hour) {
		return "", nil, false
	}
	return entry.SIDSHA1, entry.SupportedSites, true
}

// SaveSiteCache 写入（或覆盖）某个站点集合对应的缓存条目。
// 参数/返回：sidSha1/sites 为结果，sitesList 为上报集合；返回错误。
// 失败场景：读取旧文件失败时按空缓存处理，写入失败返回错误。
// 副作用：写入本地文件。
func SaveSiteCache(sidSha1 string, sites []Site, sitesList []string) error {
	if sidSha1 == "" {
		return errors.New("sid_sha1 为空，无法写入缓存")
	}
	path := CacheFilePath()
	cache := SiteCacheFile{Entries: map[string]SiteCacheEntry{}}
	if data, err := os.ReadFile(path); err == nil {
		var existing SiteCacheFile
		if json.Unmarshal(data, &existing) == nil && existing.Entries != nil {
			cache.Entries = existing.Entries
		}
	}

	normalized := normalizeSiteList(sitesList)
	cache.Entries[cacheKey(normalized)] = SiteCacheEntry{
		Timestamp:      time.Now().Format(time.RFC3339),
		SIDSHA1:        sidSha1,
		SupportedSites: sites,
		SitesList:      normalized,
	}
	pruneCacheEntries(cache.Entries)

	content, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// InvalidateSiteCache 删除缓存文件，强制下次重新拉取站点与 sid_sha1。
// 参数/返回：无；返回删除错误（文件不存在视为成功）。
// 副作用：删除本地缓存文件。
func InvalidateSiteCache() error {
	err := os.Remove(CacheFilePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ResolveSidSha1 获取（或命中缓存）sid_sha1，并返回与本地站点集合匹配的 IYUU 站点列表。
// 参数/返回：localSites 为本地 torrents 表出现过的站点昵称；extraSites 为额外强制纳入的站点（如辅种目标站）；
// fieldToNickname 为 sites 表 `site` 字段到昵称的映射；useCache 表示是否允许读缓存；返回 sid_sha1 与过滤后的站点。
// 失败场景：IYUU API 调用失败、没有交集站点、缓存写入失败（仅记录告警）。
// 副作用：可能读取/写入本地缓存文件，发起远程 HTTP 请求。
func ResolveSidSha1(ctx context.Context, client *Client, token string, localSites []string, extraSites []string, fieldToNickname map[string]string, useCache bool) (string, []Site, error) {
	merged := mergeSites(localSites, extraSites)

	if useCache {
		if sid, sites, ok := LoadSiteCache(merged); ok {
			return sid, filterSites(sites, merged, fieldToNickname), nil
		}
	}

	supported, err := client.GetSupportedSites(ctx, token)
	if err != nil {
		return "", nil, err
	}

	siteSet := normalizeSet(merged)
	ids := make([]int64, 0, len(supported))
	seen := map[int64]struct{}{}
	for _, site := range supported {
		if site.ID <= 0 {
			continue
		}
		if _, ok := seen[site.ID]; ok {
			continue
		}
		if !matchesSiteSet(site, siteSet, fieldToNickname) {
			continue
		}
		seen[site.ID] = struct{}{}
		ids = append(ids, site.ID)
	}
	if len(ids) == 0 {
		return "", nil, errors.New("没有找到与本地站点匹配的 IYUU 支持站点")
	}

	sidSha1, err := client.ReportExisting(ctx, token, ids)
	if err != nil {
		return "", nil, err
	}
	// 缓存整份支持站点列表，后续不同集合可复用同一份数据（过滤在内存做）。
	if err := SaveSiteCache(sidSha1, supported, merged); err != nil {
		logx.Warnf(LogModule, "写入 IYUU 缓存失败 err=%v", err)
	}
	return sidSha1, filterSites(supported, merged, fieldToNickname), nil
}

// filterSites 从 IYUU 支持站点中筛出与本地站点集合匹配的部分。
func filterSites(sites []Site, sitesList []string, fieldToNickname map[string]string) []Site {
	siteSet := normalizeSet(sitesList)
	filtered := make([]Site, 0, len(sites))
	seen := map[int64]struct{}{}
	for _, site := range sites {
		if site.ID <= 0 {
			continue
		}
		if _, ok := seen[site.ID]; ok {
			continue
		}
		if !matchesSiteSet(site, siteSet, fieldToNickname) {
			continue
		}
		seen[site.ID] = struct{}{}
		filtered = append(filtered, site)
	}
	return filtered
}

// matchesSiteSet 判断一个 IYUU 站点是否属于本地站点集合（site 字段映射优先，昵称兜底）。
func matchesSiteSet(site Site, siteSet map[string]struct{}, fieldToNickname map[string]string) bool {
	if field := strings.TrimSpace(site.Site); field != "" {
		if nickname, ok := fieldToNickname[field]; ok {
			if _, exists := siteSet[strings.TrimSpace(nickname)]; exists {
				return true
			}
		}
	}
	if nickname := strings.TrimSpace(site.Nickname); nickname != "" {
		if _, exists := siteSet[nickname]; exists {
			return true
		}
	}
	return false
}

// mergeSites 合并两组站点名并去重排序，作为上报集合。
func mergeSites(localSites []string, extraSites []string) []string {
	combined := make([]string, 0, len(localSites)+len(extraSites))
	combined = append(combined, localSites...)
	combined = append(combined, extraSites...)
	return normalizeSiteList(combined)
}

// LoadLatestSupportedSites 读取缓存中最近一次拉取的 IYUU 支持站点列表。
// 参数/返回：无参数；返回支持站点与是否可用（缓存不存在/损坏/过期时为 false）。
// 失败场景：文件不存在、解析失败、全部条目过期。
// 副作用：读取本地缓存文件。
// 用途：辅种页需要「IYUU 是否支持某站」时不必再请求接口，直接复用这份列表。
func LoadLatestSupportedSites() ([]Site, bool) {
	path := CacheFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var cache SiteCacheFile
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, false
	}
	var latest SiteCacheEntry
	found := false
	for _, entry := range cache.Entries {
		if len(entry.SupportedSites) == 0 || entry.Timestamp == "" {
			continue
		}
		if !found || entry.Timestamp > latest.Timestamp {
			latest = entry
			found = true
		}
	}
	if !found {
		return nil, false
	}
	ts, err := time.Parse(time.RFC3339, latest.Timestamp)
	if err != nil {
		ts, err = time.Parse("2006-01-02 15:04:05", latest.Timestamp)
		if err != nil {
			return nil, false
		}
	}
	if time.Since(ts) > (CacheExpiryDays * 24 * time.Hour) {
		return nil, false
	}
	return latest.SupportedSites, true
}

// MatchedSiteNicknames 返回 IYUU 支持站点中与本地站点集合匹配的「本地昵称」集合。
// 参数/返回：sites 为 IYUU 支持站点；localSites 为本地站点集合；fieldToNickname 为 sites.site → 昵称的映射。
// 失败场景：无（纯计算）。
// 副作用：无。
// 说明：匹配规则与 filterSites 完全一致（site 字段映射优先，昵称兜底），避免两处判断口径不同。
func MatchedSiteNicknames(sites []Site, localSites []string, fieldToNickname map[string]string) map[string]struct{} {
	siteSet := normalizeSet(localSites)
	result := make(map[string]struct{}, len(sites))
	for _, site := range sites {
		if site.ID <= 0 || !matchesSiteSet(site, siteSet, fieldToNickname) {
			continue
		}
		if nickname, ok := fieldToNickname[strings.TrimSpace(site.Site)]; ok {
			if _, exists := siteSet[strings.TrimSpace(nickname)]; exists {
				result[strings.TrimSpace(nickname)] = struct{}{}
				continue
			}
		}
		if nickname := strings.TrimSpace(site.Nickname); nickname != "" {
			if _, exists := siteSet[nickname]; exists {
				result[nickname] = struct{}{}
			}
		}
	}
	return result
}

// normalizeSiteList 去空去重并排序，保证同一集合生成稳定的缓存键。
func normalizeSiteList(items []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	sort.Strings(result)
	return result
}

func normalizeSet(items []string) map[string]struct{} {
	set := make(map[string]struct{}, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		set[trimmed] = struct{}{}
	}
	return set
}

// cacheKey 以集合内容生成稳定键。
func cacheKey(sites []string) string {
	return strings.Join(normalizeSiteList(sites), "|")
}

// pruneCacheEntries 条目超限时丢弃最旧的若干条。
func pruneCacheEntries(entries map[string]SiteCacheEntry) {
	if len(entries) <= maxCacheEntries {
		return
	}
	type pair struct {
		key string
		ts  string
	}
	list := make([]pair, 0, len(entries))
	for key, entry := range entries {
		list = append(list, pair{key: key, ts: entry.Timestamp})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ts < list[j].ts })
	for i := 0; i < len(list)-maxCacheEntries; i++ {
		delete(entries, list[i].key)
	}
}
