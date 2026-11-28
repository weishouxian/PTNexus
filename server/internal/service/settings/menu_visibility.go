package settings

import "github.com/pt-nexus/server/internal/config"

// 导航菜单项：键名与前端 App.vue 的菜单一一对应，顺序即设置页里的展示顺序。
// 未出现在用户配置里的键按「显示」处理，只有显式保存为 false 才隐藏。
var menuVisibilityKeys = []string{
	"home",
	"info",
	"torrents",
	"reseed",
	"scheduled_seeding",
	"auto_seed",
	"publish_progress",
	"publish_logs",
	"sites",
	"site_data",
	"resource_info",
	"bangumi",
	"settings",
}

// MenuVisibilityKeys 返回导航菜单项的键名（供前端渲染设置项时对齐顺序）。
func MenuVisibilityKeys() []string {
	keys := make([]string, len(menuVisibilityKeys))
	copy(keys, menuVisibilityKeys)
	return keys
}

// GetMenuVisibility 返回导航菜单的显隐配置。
// 参数/返回：返回 key -> 是否显示；未配置的菜单项默认显示。
// 失败场景：配置缺失或结构异常时按默认值返回，不报错。
// 副作用：无，仅读取内存配置。
//
// 优先级：环境变量硬门禁 > 用户在设置页保存的值 > 默认显示。
// 「自动发种」必须在 PTNEXUS_SHOW_AUTO_SEED 为真值 **且** 设置页里开启才显示，
// 环境变量未开启时设置页里也不会出现该项。
func (s *SettingsService) GetMenuVisibility() map[string]bool {
	result := make(map[string]bool, len(menuVisibilityKeys))
	for _, key := range menuVisibilityKeys {
		result[key] = true
	}
	envFlags := MenuVisibilityEnvFlags()
	// 「自动发种」历史上靠环境变量控制且默认隐藏：没在设置页改过时保持原行为。
	result["auto_seed"] = envFlags["auto_seed"]

	cfg := s.cfg.Get()
	ui, ok := cfg["ui_settings"].(map[string]any)
	if !ok {
		return result
	}
	saved, ok := ui["menu_visibility"].(map[string]any)
	if !ok {
		return result
	}
	for key, value := range saved {
		if _, known := result[key]; !known {
			continue
		}
		// 环境变量没开时，用户保存的 true 也不能把入口放出来。
		if allowed, controlled := envFlags[key]; controlled && !allowed {
			continue
		}
		result[key] = toBool(value, true)
	}
	return result
}

// MenuVisibilityEnvFlags 返回由环境变量控制的菜单项当前状态（true=允许出现）。
// 参数/返回：返回 key -> 是否允许出现；未受环境变量控制的菜单项不出现。
// 失败场景：无。
// 副作用：无，仅读取进程环境变量。
func MenuVisibilityEnvFlags() map[string]bool {
	return map[string]bool{
		"auto_seed": config.ResolveMenuVisibility().AutoSeed,
	}
}

// MenuVisibilityEnv 供 handler 直接透出环境变量控制状态。
func (s *SettingsService) MenuVisibilityEnv() map[string]bool {
	return MenuVisibilityEnvFlags()
}

// SaveMenuVisibility 保存导航菜单的显隐配置。
// 参数/返回：items 为 key -> 是否显示；返回错误表示写入失败。
// 失败场景：配置保存失败时返回错误。
// 副作用：写入配置文件并持久化；未提交的菜单项保持原值。
// 被环境变量关闭的菜单项（如未开启 PTNEXUS_SHOW_AUTO_SEED 时的 auto_seed）直接忽略。
func (s *SettingsService) SaveMenuVisibility(items map[string]bool) error {
	envFlags := MenuVisibilityEnvFlags()
	cfg := s.cfg.Get()
	ui := ensureMap(cfg, "ui_settings")
	saved := ensureMap(ui, "menu_visibility")
	for key, visible := range items {
		if !isMenuVisibilityKey(key) {
			continue
		}
		if allowed, controlled := envFlags[key]; controlled && !allowed {
			continue
		}
		saved[key] = visible
	}
	ui["menu_visibility"] = saved
	cfg["ui_settings"] = ui
	return s.cfg.Save(cfg)
}

func isMenuVisibilityKey(key string) bool {
	for _, known := range menuVisibilityKeys {
		if known == key {
			return true
		}
	}
	return false
}
