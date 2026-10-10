package dupe

import (
	"strings"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

// DupeDimensionOption 描述一个可选判定维度，供站点设置页渲染勾选框。
// 字段名用 snake_case tag 对齐前端：不加 tag 会按 Go 字段名输出（Value/Label…），前端读到的是 undefined。
type DupeDimensionOption struct {
	// Value 为维度常量（写进规则表的取值）。
	Value string `json:"value"`
	// Label 为中文名。
	Label string `json:"label"`
	// NeedsSiteFilter 为 true 表示该维度依赖站点检索筛选参数：
	// 站点未声明时勾了也不生效，界面上应置灰并给出说明。
	NeedsSiteFilter bool `json:"needs_site_filter"`
	// Supported 表示当前站点是否真的能用该维度（客户端比对维度恒为 true）。
	Supported bool `json:"supported"`
}

// SiteDupeMediumOption 描述一个可配置规则的目标媒介。
type SiteDupeMediumOption struct {
	// Medium 为标准媒介键，即规则表里的键（见 medium_groups.go 的 6 类）。
	Medium string `json:"medium"`
	// Label 为展示名（固定文案：UHD Blu-ray / Blu-ray / HDTV / Encode / Remux / WEB）。
	Label string `json:"label"`
	// SiteValue 为该媒介在站点上传表单里的取值（组内任一成员在该站的映射值）。
	// 仅作展示参考：实际检索用的取值取自待发布种子自己的表单字段，与规则键无关。
	SiteValue string `json:"site_value"`
}

// SiteDupeOptions 描述站点 dupe 规则的可选项。
type SiteDupeOptions struct {
	// Enabled 表示站点声明了 dupe 校验能力（YAML dupe_check.enabled）。
	Enabled bool `json:"enabled"`
	// Mediums 为可配置规则的媒介组（固定 6 类，顺序固定；站点完全没映射的组会被略去）。
	Mediums []SiteDupeMediumOption `json:"mediums"`
	// Dimensions 为全部可选判定维度（含可用性标记）。
	Dimensions []DupeDimensionOption `json:"dimensions"`
	// FallbackMedium 为兜底规则在规则表里的保留键，前端保存兜底规则时用它作为键，
	// 由后端下发而不是前端硬编码，避免两边写歪。
	FallbackMedium string `json:"fallback_medium"`
}

// BuildSiteDupeOptions 汇总站点 dupe 规则的可选项。
// 参数/返回：siteCode 为站点标识；返回选项集合（站点未声明 dupe 能力时 Enabled=false 且列表为空）。
// 说明：媒介清单不再直接照搬站点 YAML 的 mappings.medium（那里同义键太多），
// 而是收敛成 6 类（见 medium_groups.go）；只要站点映射了该组的任一成员就列出，
// 组内的细分键由 MatchRule 自动归类命中。
// 副作用：加载站点配置（有缓存）。
func BuildSiteDupeOptions(siteCode string) SiteDupeOptions {
	options := SiteDupeOptions{
		Dimensions:     buildDimensionOptions(siteCode),
		FallbackMedium: DupeFallbackMedium,
	}
	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil || !siteCfg.DupeCheck.Enabled {
		return options
	}
	options.Enabled = true

	mediumMapping := siteCfg.Mappings["medium"]
	mediums := make([]SiteDupeMediumOption, 0, len(dupeMediumGroups))
	for _, group := range dupeMediumGroups {
		siteValue := ""
		for _, member := range group.Members {
			if value := strings.TrimSpace(mediumMapping[member]); value != "" {
				siteValue = value
				break
			}
		}
		// 站点没映射该组的任何标准键 → 无法用该媒介检索，列出来只会让人勾了却不生效。
		if siteValue == "" {
			continue
		}
		mediums = append(mediums, SiteDupeMediumOption{
			Medium:    group.Key,
			Label:     group.Label,
			SiteValue: siteValue,
		})
	}
	options.Mediums = mediums
	return options
}

// buildDimensionOptions 组装维度选项，并标注当前站点对每个维度的支持情况。
func buildDimensionOptions(siteCode string) []DupeDimensionOption {
	supportedFilters := map[string]struct{}{}
	for _, dimension := range FilterDimensionsForSite(siteCode) {
		supportedFilters[dimension] = struct{}{}
	}
	options := make([]DupeDimensionOption, 0, len(dupeDimensionOrder))
	for _, dimension := range dupeDimensionOrder {
		item := DupeDimensionOption{
			Value:           dimension,
			Label:           DupeDimensionLabel(dimension),
			NeedsSiteFilter: IsFilterDimension(dimension),
			Supported:       true,
		}
		if item.NeedsSiteFilter {
			_, item.Supported = supportedFilters[dimension]
		}
		options = append(options, item)
	}
	return options
}
