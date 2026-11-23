package extract

import (
	"os"
	"path/filepath"
	"testing"
)

// 使用真实 configs/global_mappings.yaml 校验制作组映射覆盖。
// 背景：站点的官方制作组若不进映射表，会被归一成 team.other，进而被官组致谢的
// exclude_teams 挡掉，导致声明为空（可乐 COLAWEB 就是这样丢的声明）。
func TestNormalizeTeamKeyUsesRealMappingTable(t *testing.T) {
	baseDir, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatalf("解析仓库 server 目录失败: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(baseDir, "configs", "global_mappings.yaml")); statErr != nil {
		t.Fatalf("找不到 global_mappings.yaml: %v", statErr)
	}
	t.Setenv("PTNEXUS_BASE_DIR", baseDir)
	t.Setenv("PTNEXUS_GLOBAL_MAPPINGS", filepath.Join(baseDir, "configs", "global_mappings.yaml"))

	cases := []struct {
		name string
		raw  string
		site string
		want string
	}{
		{name: "可乐官方 WEB 组", raw: "COLAWEB", site: "colapt", want: "team.colaweb"},
		{name: "可乐官方 WEB 组带前导横线", raw: "-COLAWEB", site: "colapt", want: "team.colaweb"},
		{name: "人人 ADWeb（已有映射不得回退）", raw: "ADWeb", site: "ttg", want: "team.adweb"},
		{name: "未登记制作组仍回落 team.other", raw: "NoSuchGroupXYZ", site: "colapt", want: "team.other"},
	}
	for _, tc := range cases {
		if got := NormalizeTeamKeyForSite(tc.raw, tc.site); got != tc.want {
			t.Fatalf("%s: want %q, got %q", tc.name, tc.want, got)
		}
	}
}
