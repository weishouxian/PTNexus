package sitehealth

import (
	"path/filepath"
	"testing"

	"github.com/pt-nexus/server/internal/config"
)

// newTestService 构造一个指向临时 config.json 的检测服务，便于隔离测试配置。
func newTestService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	cfg, err := config.NewManager(config.RuntimePaths{ConfigFile: filepath.Join(dir, "config.json")})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return NewService(nil, nil, cfg)
}

func TestIntervalHours(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  float64
	}{
		{"未设置", nil, 0},
		{"整数", 12, 12},
		{"小数", 0.5, 0.5},
		{"字符串小数", "1.5", 1.5},
		{"字符串整数", "24", 24},
		{"非法字符串", "abc", 0},
		{"负数", -1, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestService(t)
			cfg := s.cfg.Get()
			if tc.value == nil {
				delete(cfg, "site_health")
			} else {
				cfg["site_health"] = map[string]any{"interval_hours": tc.value}
			}
			if err := s.cfg.Save(cfg); err != nil {
				t.Fatalf("Save: %v", err)
			}
			if got := s.IntervalHours(); got != tc.want {
				t.Errorf("IntervalHours() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsExplicitlyFalse(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  bool
	}{
		{"bool false", false, true},
		{"bool true", true, false},
		{"int 0", 0, true},
		{"int 1", 1, false},
		{"int64 0", int64(0), true},
		{"float64 0", float64(0), true},
		{"字符串 0", "0", true},
		{"字符串 false", "false", true},
		{"字符串 1", "1", false},
		{"nil", nil, false},
		{"缺失(默认开启)", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isExplicitlyFalse(tc.value); got != tc.want {
				t.Errorf("isExplicitlyFalse(%v) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}
