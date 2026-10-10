package dupe

import "strings"

// dupeMediumGroupDefinition 描述一个可配置查重规则的媒介组。
type dupeMediumGroupDefinition struct {
	// Key 为规则表里使用的键（组代表媒介）。它是该组最具代表性的标准媒介键，
	// 也是写进 sites.dupe_rules 的键。
	Key string
	// Label 为界面展示名。用固定文案而不是查反向映射表，保证 6 项的名称与顺序稳定。
	Label string
	// Members 为归入本组的标准媒介键（含 Key 自身）。
	// 待发布种子的 standardized_params.medium 落在 Members 里，即命中以 Key 配置的规则。
	Members []string
}

// dupeMediumGroups 为 dupe 规则可选的媒介组（固定 6 类，顺序即界面展示顺序）。
//
// 为什么要归组：站点 YAML 的 mappings.medium 通常有 15~20 个标准键
// （如家园同时列了 medium.remux / medium.uhd_remux / medium.bluray_remux /
// medium.uhd_bluray_remux / medium.remux_tv / medium.uhd_remux_tv，其实都是 Remux），
// 逐个列出来既难选也难维护。这里收敛成 6 类，用户只需按类配规则，
// 更细的标准键由 mediumGroupKey 自动归类命中（见 rules.go:MatchRule）。
//
// ⚠️ 未列入任何组的媒介（如 medium.other / medium.dvd / medium.cd / 电子书类）
// 不会命中媒介规则，只能靠「兜底规则」接手；兜底关闭时这些媒介直接跳过校验。
var dupeMediumGroups = []dupeMediumGroupDefinition{
	{
		Key:     "medium.uhd_bluray",
		Label:   "UHD Blu-ray",
		Members: []string{"medium.uhd_bluray", "medium.uhd_diy"},
	},
	{
		Key:     "medium.bluray",
		Label:   "Blu-ray",
		Members: []string{"medium.bluray", "medium.bluray_diy"},
	},
	{
		Key:     "medium.hdtv",
		Label:   "HDTV",
		Members: []string{"medium.hdtv", "medium.uhdtv", "medium.tvrip"},
	},
	{
		Key:   "medium.encode",
		Label: "Encode",
		Members: []string{
			"medium.encode",
			"medium.encode_2160p",
			"medium.encode_1080p",
			"medium.encode_720p",
			"medium.bdrip",
		},
	},
	{
		Key:   "medium.remux",
		Label: "Remux",
		Members: []string{
			"medium.remux",
			"medium.uhd_remux",
			"medium.bluray_remux",
			"medium.uhd_bluray_remux",
			"medium.remux_tv",
			"medium.uhd_remux_tv",
		},
	},
	{
		Key:     "medium.webdl",
		Label:   "WEB",
		Members: []string{"medium.webdl", "medium.webrip"},
	},
}

// dupeMediumGroupKeyByMember 为「标准媒介键 → 组键」的索引（成员键大小写不敏感）。
var dupeMediumGroupKeyByMember = func() map[string]string {
	index := map[string]string{}
	for _, group := range dupeMediumGroups {
		for _, member := range group.Members {
			key := strings.ToLower(strings.TrimSpace(member))
			if key == "" {
				continue
			}
			index[key] = group.Key
		}
	}
	return index
}()

// mediumGroupKey 返回标准媒介键所属的规则组键。
// 参数/返回：medium 为标准媒介键（如 medium.uhd_remux）；返回组键；不属于任何组时返回空串。
// 副作用：无。
func mediumGroupKey(medium string) string {
	key := strings.ToLower(strings.TrimSpace(medium))
	if key == "" {
		return ""
	}
	return dupeMediumGroupKeyByMember[key]
}
