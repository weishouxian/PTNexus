package dupe

import (
	"sort"
	"strings"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
	"github.com/pt-nexus/server/internal/service/reversemapping"
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
	// Medium 为标准媒介键，即规则表里的键（如 medium.remux）。
	Medium string `json:"medium"`
	// Label 为中文名（取自反向映射，取不到时回退为标准键本身）。
	Label string `json:"label"`
	// SiteValue 为该媒介在站点上传表单里的取值。
	// 多个标准媒介可能映射到同一取值（如 medium.uhd_bluray 与 medium.uhd_diy 都是 UHD Blu-ray），
	// 界面上按它分组展示，避免用户误以为要逐个添加同义项。
	SiteValue string `json:"site_value"`
}

// SiteDupeOptions 描述站点 dupe 规则的可选项。
type SiteDupeOptions struct {
	// Enabled 表示站点声明了 dupe 校验能力（YAML dupe_check.enabled）。
	Enabled bool `json:"enabled"`
	// Mediums 为站点 mappings.medium 中的全部标准媒介键（已排序）。
	Mediums []SiteDupeMediumOption `json:"mediums"`
	// Dimensions 为全部可选判定维度（含可用性标记）。
	Dimensions []DupeDimensionOption `json:"dimensions"`
	// FallbackMedium 为兜底规则在规则表里的保留键，前端保存兜底规则时用它作为键，
	// 由后端下发而不是前端硬编码，避免两边写歪。
	FallbackMedium string `json:"fallback_medium"`
}

// BuildSiteDupeOptions 汇总站点 dupe 规则的可选项。
// 参数/返回：siteCode 为站点标识；返回选项集合（站点未声明 dupe 能力时 Enabled=false 且列表为空）。
// 说明：媒介清单来自站点 YAML 的 mappings.medium —— 与发布时写进 standardized_params.medium 的
// 取值同源，因此这里列出的键就是规则能命中的键；中文名来自反向映射表。
// 副作用：加载站点配置与反向映射（均有缓存/文件读取）。
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

	labelSource := mediumLabelSource()
	mediumMapping := siteCfg.Mappings["medium"]
	mediums := make([]SiteDupeMediumOption, 0, len(mediumMapping))
	for standard, siteValue := range mediumMapping {
		key := strings.TrimSpace(standard)
		// mappings 里的 default 是兜底项而非真实媒介，不能作为规则目标。
		if key == "" || key == "default" || !strings.Contains(key, ".") {
			continue
		}
		mediums = append(mediums, SiteDupeMediumOption{
			Medium:    key,
			Label:     firstNonEmptyValue(labelSource[key], humanizeMediumKey(key)),
			SiteValue: strings.TrimSpace(siteValue),
		})
	}
	// 按站点取值分组（同义媒介相邻），组内按标准键排序，保证每次返回顺序稳定。
	sort.Slice(mediums, func(i, j int) bool {
		if mediums[i].SiteValue != mediums[j].SiteValue {
			return mediums[i].SiteValue < mediums[j].SiteValue
		}
		return mediums[i].Medium < mediums[j].Medium
	})
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

// humanizeMediumKey 为没有中文名可用的标准媒介键生成可读标签。
// 参数/返回：key 为标准媒介键（如 medium.bluray_diy）；返回去掉命名空间前缀、下划线转空格的文本。
// 说明：反向映射表覆盖不到少数冷门键（如 medium.iso / medium.bluray_diy），
// 直接展示原始键太生硬，这里退化成「bluray diy」这类可读形式。
// 副作用：无。
func humanizeMediumKey(key string) string {
	trimmed := strings.TrimSpace(key)
	if idx := strings.LastIndex(trimmed, "."); idx >= 0 && idx+1 < len(trimmed) {
		trimmed = trimmed[idx+1:]
	}
	return strings.ReplaceAll(trimmed, "_", " ")
}

// mediumLabelSource 返回「标准媒介键 → 中文名」映射（反向映射表）。
func mediumLabelSource() map[string]string {
	reverse := reversemapping.Build(nil)
	raw, ok := reverse["medium"].(map[string]string)
	if !ok || raw == nil {
		return map[string]string{}
	}
	return raw
}
