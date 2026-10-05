package torrentdata

import (
	"sort"
	"strings"

	"github.com/pt-nexus/server/internal/repository"
)

func (s *TorrentDataService) applyFilters(data []map[string]any, params TorrentsDataParams, siteConfigMap map[string]repository.SiteConfig) []map[string]any {
	filtered := data
	nameSearch := strings.ToLower(strings.TrimSpace(params.NameSearch))
	if nameSearch != "" {
		filtered = filterData(filtered, func(item map[string]any) bool {
			return strings.Contains(strings.ToLower(stringValue(item["name"], "")), nameSearch)
		})
	}

	if len(params.PathFilters) > 0 {
		allowed := toStringSet(params.PathFilters)
		filtered = filterData(filtered, func(item map[string]any) bool {
			_, ok := allowed[stringValue(item["save_path"], "")]
			return ok
		})
	}

	if len(params.StateFilters) > 0 {
		allowed := toStringSet(params.StateFilters)
		filtered = filterData(filtered, func(item map[string]any) bool {
			states := strings.Split(stringValue(item["state"], ""), ",")
			for _, state := range states {
				if _, ok := allowed[strings.TrimSpace(state)]; ok {
					return true
				}
			}
			return false
		})
	}

	if len(params.SourceDataStatusFilters) > 0 {
		allowed := toStringSet(params.SourceDataStatusFilters)
		filtered = filterData(filtered, func(item map[string]any) bool {
			status := stringValue(item["source_data_status"], "missing")
			_, ok := allowed[status]
			return ok
		})
	}

	if len(params.TypeFilters) > 0 {
		filtered = filterStandardField(filtered, "type", params.TypeFilters)
	}

	if len(params.MediumFilters) > 0 {
		filtered = filterStandardField(filtered, "medium", params.MediumFilters)
	}

	if len(params.SourceFilters) > 0 {
		filtered = filterStandardField(filtered, "source", params.SourceFilters)
	}

	if len(params.DownloaderFilters) > 0 {
		allowed := toStringSet(params.DownloaderFilters)
		filtered = filterData(filtered, func(item map[string]any) bool {
			ids := toStringSlice(item["downloaderIds"])
			for _, id := range ids {
				if _, ok := allowed[id]; ok {
					return true
				}
			}
			return false
		})
	}

	if len(params.SourceAvailabilityFilters) > 0 {
		hasAvailableFilter := containsString(params.SourceAvailabilityFilters, "存在源站点")
		hasUnavailableFilter := containsString(params.SourceAvailabilityFilters, "无可用源站点")
		if hasAvailableFilter != hasUnavailableFilter {
			filtered = filterData(filtered, func(item map[string]any) bool {
				sitesMap, _ := item["sites"].(map[string]any)
				hasSource := false
				for siteName := range sitesMap {
					cfg, ok := siteConfigMap[siteName]
					if !ok {
						continue
					}
					if (cfg.Migration == 1 || cfg.Migration == 3) && strings.TrimSpace(cfg.Cookie) != "" {
						hasSource = true
						break
					}
				}
				if hasAvailableFilter {
					return hasSource
				}
				return !hasSource
			})
		}
	}

	if len(params.ExistSiteNames) > 0 {
		required := toStringSet(params.ExistSiteNames)
		filtered = filterData(filtered, func(item map[string]any) bool {
			sitesMap, _ := item["sites"].(map[string]any)
			for site := range required {
				if _, exists := sitesMap[site]; !exists {
					return false
				}
			}
			return true
		})
	}

	if len(params.NotExistSiteNames) > 0 {
		forbidden := toStringSet(params.NotExistSiteNames)
		filtered = filterData(filtered, func(item map[string]any) bool {
			sitesMap, _ := item["sites"].(map[string]any)
			for site := range forbidden {
				if _, exists := sitesMap[site]; exists {
					return false
				}
			}
			return true
		})
	}

	if params.ExcludeExisting {
		names, err := s.repo.DistinctSeedParameterNames()
		if err == nil {
			existing := toStringSet(names)
			filtered = filterData(filtered, func(item map[string]any) bool {
				_, found := existing[stringValue(item["name"], "")]
				return !found
			})
		}
	}

	return filtered
}

// filterStandardField 按标准值集合过滤指定字段（类型/媒介/地区）。
// 参数/返回：field 为 item 中的字段名（type/medium/source），selected 为用户勾选的标准值；返回过滤后的切片。
// 失败场景：无。字段为空（源站数据未获取）时不会命中任何勾选项。
// 副作用：无。
func filterStandardField(data []map[string]any, field string, selected []string) []map[string]any {
	allowed := toStringSet(selected)
	return filterData(data, func(item map[string]any) bool {
		_, ok := allowed[strings.TrimSpace(stringValue(item[field], ""))]
		return ok
	})
}

func (s *TorrentDataService) sortData(data []map[string]any, sortProp, sortOrder string) {
	if len(data) <= 1 {
		return
	}

	// 默认按种子大小降序排列。
	// 若指定了排序字段，则按指定字段排序，并以种子大小降序作为次要排序。
	hasExplicit := strings.TrimSpace(sortProp) != "" && strings.TrimSpace(sortOrder) != ""
	descending := hasExplicit && strings.EqualFold(sortOrder, "descending")
	if !hasExplicit {
		sort.SliceStable(data, func(i, j int) bool {
			return numberValue(data[i]["size"]) > numberValue(data[j]["size"])
		})
		return
	}
	mapped := map[string]string{
		"size_formatted":           "size",
		"total_uploaded_formatted": "total_uploaded",
	}
	if value, ok := mapped[sortProp]; ok {
		sortProp = value
	}

	numericFields := map[string]struct{}{
		"size":               {},
		"progress":           {},
		"total_uploaded":     {},
		"site_count":         {},
		"target_sites_count": {},
		"seeders":            {},
	}

	if _, ok := numericFields[sortProp]; ok {
		sort.SliceStable(data, func(i, j int) bool {
			left := numberValue(data[i][sortProp])
			right := numberValue(data[j][sortProp])
			if left != right {
				if descending {
					return left > right
				}
				return left < right
			}
			// 相同时按种子大小降序作为次要排序
			return numberValue(data[i]["size"]) > numberValue(data[j]["size"])
		})
		return
	}

	sort.SliceStable(data, func(i, j int) bool {
		left := stringValue(data[i]["name"], "")
		right := stringValue(data[j]["name"], "")
		if left != right {
			if descending {
				return customNameLess(right, left)
			}
			return customNameLess(left, right)
		}
		// 相同时按种子大小降序作为次要排序
		return numberValue(data[i]["size"]) > numberValue(data[j]["size"])
	})
}

func (s *TorrentDataService) selectBestDownloader(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	cfg := s.cfg.Get()
	downloaders := toSlice(cfg["downloaders"])
	enabledOrder := make([]string, 0)
	for _, raw := range downloaders {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if !toBool(item["enabled"], true) {
			continue
		}
		enabledOrder = append(enabledOrder, toString(item["id"], ""))
	}
	for _, candidate := range enabledOrder {
		if containsString(ids, candidate) {
			return candidate
		}
	}
	return ids[0]
}
