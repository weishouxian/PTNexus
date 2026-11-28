package settings

import (
	"path/filepath"
	"testing"

	"github.com/pt-nexus/server/internal/config"
)

func TestSiteDataRefreshDefaultsAndSave(t *testing.T) {
	manager, err := config.NewManager(config.RuntimePaths{
		ConfigFile: filepath.Join(t.TempDir(), "config.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSettingsService(manager)

	got := svc.GetSiteDataRefresh()
	if got.Enabled {
		t.Error("定时刷新默认应关闭")
	}
	if got.Time != defaultSiteDataRefreshAt || len(got.Times) != 1 || got.Times[0] != defaultSiteDataRefreshAt {
		t.Errorf("默认时间应为 %s，实际 time=%s times=%v", defaultSiteDataRefreshAt, got.Time, got.Times)
	}

	// 旧前端只传 time：当成单个刷新点保存
	if err := svc.SaveSiteDataRefresh(SiteDataRefreshConfig{Enabled: true, Time: "07:30"}); err != nil {
		t.Fatal(err)
	}
	got = svc.GetSiteDataRefresh()
	if !got.Enabled || got.Time != "07:30" || len(got.Times) != 1 {
		t.Fatalf("保存后应为开启 07:30，实际 %+v", got)
	}

	// 空时间回落默认时刻，不应写入非法值
	if err := svc.SaveSiteDataRefresh(SiteDataRefreshConfig{Enabled: true, Time: "  "}); err != nil {
		t.Fatal(err)
	}
	if got = svc.GetSiteDataRefresh(); got.Time != defaultSiteDataRefreshAt {
		t.Errorf("空时间应回落默认值，实际 %s", got.Time)
	}

	// 非 HH:mm 格式必须拒绝，避免调度器永远匹配不上
	if err := svc.SaveSiteDataRefresh(SiteDataRefreshConfig{Enabled: true, Time: "7点"}); err == nil {
		t.Error("非法时间格式应返回错误")
	}
}

// TestSiteDataRefreshMultipleTimes 校验多个刷新点：去重、升序、兼容旧 time 字段。
func TestSiteDataRefreshMultipleTimes(t *testing.T) {
	manager, err := config.NewManager(config.RuntimePaths{
		ConfigFile: filepath.Join(t.TempDir(), "config.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSettingsService(manager)

	input := SiteDataRefreshConfig{Enabled: true, Times: []string{"20:00", " 06:00 ", "06:00", ""}}
	if err := svc.SaveSiteDataRefresh(input); err != nil {
		t.Fatal(err)
	}
	got := svc.GetSiteDataRefresh()
	if len(got.Times) != 2 || got.Times[0] != "06:00" || got.Times[1] != "20:00" {
		t.Fatalf("应去重并升序为 [06:00 20:00]，实际 %v", got.Times)
	}
	// 兼容字段 Time 必须等于第一项（旧版本读到的行为和以前一致）
	if got.Time != "06:00" {
		t.Fatalf("Time 应等于 Times[0]，实际 %s", got.Time)
	}

	// 任一非法值整体拒绝，不能悄悄丢弃一个点
	if err := svc.SaveSiteDataRefresh(SiteDataRefreshConfig{Enabled: true, Times: []string{"06:00", "25:00"}}); err == nil {
		t.Error("含非法刷新点应返回错误")
	}
}

// TestGetSiteDataRefreshLegacyTime 校验老配置（只有 time、没有 times）能被读成一个刷新点。
func TestGetSiteDataRefreshLegacyTime(t *testing.T) {
	manager, err := config.NewManager(config.RuntimePaths{
		ConfigFile: filepath.Join(t.TempDir(), "config.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := NewSettingsService(manager)
	cfg := svc.cfg.Get()
	cfg["ui_settings"] = map[string]any{
		"site_data_refresh": map[string]any{"enabled": true, "time": "18:15"},
	}
	if err := svc.cfg.Save(cfg); err != nil {
		t.Fatal(err)
	}
	got := svc.GetSiteDataRefresh()
	if !got.Enabled || len(got.Times) != 1 || got.Times[0] != "18:15" || got.Time != "18:15" {
		t.Fatalf("老配置应兼容成单个刷新点，实际 %+v", got)
	}
}
