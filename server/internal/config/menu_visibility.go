package config

import (
	"os"
	"strings"
)

// 菜单显隐开关：把部分功能入口从导航菜单中隐藏，避免干扰普通用户。
// 约定：
// 1. 默认全部隐藏，只有环境变量被显式设置为真值时才显示对应入口；
// 2. 仅影响前端导航菜单的渲染，路由与后端接口保持可用，
//    直接输入链接（如 /auto-seed）仍能打开页面。
const (
	// ShowAutoSeedMenuEnv 控制「自动发种」菜单是否显示，默认隐藏。
	ShowAutoSeedMenuEnv = "PTNEXUS_SHOW_AUTO_SEED"
)

// MenuVisibility 描述前端导航菜单的显隐开关集合。
type MenuVisibility struct {
	AutoSeed bool `json:"auto_seed"`
}

// ResolveMenuVisibility 从环境变量解析菜单显隐开关。
// 参数/返回：无参数；返回各菜单项的显示状态，未配置或取值非真值时返回 false。
// 失败场景：环境变量缺失或取值不合法时按隐藏处理。
// 副作用：无，仅读取进程环境变量。
func ResolveMenuVisibility() MenuVisibility {
	return MenuVisibility{
		AutoSeed: envFlagEnabled(ShowAutoSeedMenuEnv),
	}
}

// envFlagEnabled 判断环境变量是否为真值（1/true/yes/on，忽略大小写与首尾空白）。
func envFlagEnabled(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
