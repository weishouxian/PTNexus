package settings

import (
	"path/filepath"
	"testing"

	"github.com/pt-nexus/server/internal/config"
)

func TestMenuVisibilityDefaultsAndSave(t *testing.T) {
	t.Setenv(config.ShowAutoSeedMenuEnv, "false")
	manager, err := config.NewManager(config.RuntimePaths{
		ConfigFile: filepath.Join(t.TempDir(), "config.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSettingsService(manager)

	got := svc.GetMenuVisibility()
	if len(got) != len(menuVisibilityKeys) {
		t.Fatalf("应返回全部 %d 个菜单键，实际 %d", len(menuVisibilityKeys), len(got))
	}
	for _, key := range menuVisibilityKeys {
		want := key != "auto_seed"
		if got[key] != want {
			t.Errorf("默认 %q 应为 %v，实际 %v", key, want, got[key])
		}
	}

	if err := svc.SaveMenuVisibility(map[string]bool{
		"home":        false,
		"auto_seed":   true,
		"unknown_key": false,
	}); err != nil {
		t.Fatal(err)
	}

	got = svc.GetMenuVisibility()
	if got["home"] {
		t.Error("保存后首页菜单应隐藏")
	}
	if !got["auto_seed"] {
		t.Error("保存后自动发种菜单应显示（用户配置覆盖环境变量默认值）")
	}
	if !got["info"] {
		t.Error("未提交的菜单项应保持默认显示")
	}
	if _, exists := got["unknown_key"]; exists {
		t.Error("未知菜单键不应写入配置")
	}
}
