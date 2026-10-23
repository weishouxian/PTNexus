package repository

import "testing"

// TestPublishLogTailExpr 日志摘要必须取尾部：MySQL/PostgreSQL 用 RIGHT，SQLite 用负起点 SUBSTR。
// 取头部会把写在末尾的「发布结果：…」行切掉，前端「发布记录」就只剩下过程行。
func TestPublishLogTailExpr(t *testing.T) {
	cases := []struct {
		dbType string
		want   string
	}{
		{"mysql", "RIGHT(COALESCE(logs, ''), 800)"},
		{"MySQL", "RIGHT(COALESCE(logs, ''), 800)"},
		{"postgres", "RIGHT(COALESCE(logs, ''), 800)"},
		{"sqlite", "SUBSTR(COALESCE(logs, ''), -800)"},
		{"", "RIGHT(COALESCE(logs, ''), 800)"},
	}
	for _, tc := range cases {
		if got := publishLogTailExpr(tc.dbType, "logs", 800); got != tc.want {
			t.Fatalf("publishLogTailExpr(%q)=%q, want %q", tc.dbType, got, tc.want)
		}
	}
}
