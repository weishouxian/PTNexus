package repository

import (
	"strings"
	"testing"
)

// TestNormalizeSitePublishMode 验证 sites.publish_mode 的归一规则。
// 候选模式是例外路径，任何缺失/脏值都必须落回正式发种（0），
// 否则一个有直发权限的账号会被误降级成「只能提交候选」。
func TestNormalizeSitePublishMode(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  int
	}{
		{name: "nil 默认正式发种", value: nil, want: SitePublishModeUpload},
		{name: "缺失默认正式发种", value: "", want: SitePublishModeUpload},
		{name: "int 0", value: 0, want: SitePublishModeUpload},
		{name: "int 1 候选", value: 1, want: SitePublishModeOffer},
		{name: "int64 1 候选", value: int64(1), want: SitePublishModeOffer},
		{name: "字符串 1 候选", value: "1", want: SitePublishModeOffer},
		{name: "[]byte 1 候选", value: []byte("1"), want: SitePublishModeOffer},
		{name: "非法值 2 回落正式发种", value: 2, want: SitePublishModeUpload},
		{name: "非法字符串 回落正式发种", value: "offer", want: SitePublishModeUpload},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeSitePublishMode(tc.value); got != tc.want {
				t.Fatalf("normalizeSitePublishMode(%v) = %d, want %d", tc.value, got, tc.want)
			}
		})
	}
}

// TestSiteBackupUpdateArgsMatchSQL 校验备份写回语句的占位符与参数一一对应。
// 站点备份是「整行 UPDATE」，加列时最容易漏改其中一侧 —— 漏了 SQL 会直接报错，
// 漏了参数则会把参数错位写进别的列，所以这里用数量与列名双重断言兜住。
func TestSiteBackupUpdateArgsMatchSQL(t *testing.T) {
	sql := siteBackupUpdateSQL("`group`")
	if got, want := strings.Count(sql, "?"), len((siteBackupValues{}).updateArgs(1)); got != want {
		t.Fatalf("占位符 %d 个，参数 %d 个，必须一致\nSQL:\n%s", got, want, sql)
	}
	for _, column := range []string{"publish_mode", "can_publish", "sort_order"} {
		if !strings.Contains(sql, column+" = ?") {
			t.Fatalf("写回语句缺少列 %s:\n%s", column, sql)
		}
	}
}

// TestSiteBackupExportsPublishMode 验证站点备份导出包含发种方式，且候选模式能原样带出。
// 缺这个字段会导致「备份 → 恢复」后候选站变回正式发种，进而在没有直发权限的账号上直接发种失败。
func TestSiteBackupExportsPublishMode(t *testing.T) {
	exported := siteBackupValues{PublishMode: SitePublishModeOffer}.asExportMap()
	if got := exported["publish_mode"]; got != SitePublishModeOffer {
		t.Fatalf("导出 publish_mode = %v, want %d", got, SitePublishModeOffer)
	}

	row := siteBackupValuesFromRow(map[string]any{"publish_mode": int64(1)})
	if row.PublishMode != SitePublishModeOffer {
		t.Fatalf("库内 publish_mode=1 解析为 %d，want %d", row.PublishMode, SitePublishModeOffer)
	}
	imported := siteBackupValuesFromImport(map[string]any{"publish_mode": "1"})
	if imported.PublishMode != SitePublishModeOffer {
		t.Fatalf("导入 publish_mode=1 解析为 %d，want %d", imported.PublishMode, SitePublishModeOffer)
	}

	// 「仅补空」合并：库内仍是默认（正式发种）时才用备份值填充。
	merged, changed := mergeSiteBackup(siteBackupValues{}, siteBackupValues{PublishMode: SitePublishModeOffer})
	if !changed || merged.PublishMode != SitePublishModeOffer {
		t.Fatalf("库内为默认值时应补成候选，got changed=%v mode=%d", changed, merged.PublishMode)
	}
	kept, _ := mergeSiteBackup(siteBackupValues{PublishMode: SitePublishModeOffer}, siteBackupValues{PublishMode: SitePublishModeUpload})
	if kept.PublishMode != SitePublishModeOffer {
		t.Fatalf("库内已显式设为候选时导入不应覆盖，got %d", kept.PublishMode)
	}
}
