package desktopapp

import (
	"os"
	"path/filepath"
	"testing"
)

// 回归测试：单文件 exe 曾因 bundle 未同步 bdinfo 而报「未找到 BDInfo 可执行文件」。
// 该用例会展开上百 MB 资源，`-short` 下跳过。
func TestMaterializeIncludesBDInfo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bundle materialization in short mode")
	}

	dir := t.TempDir()
	root, err := MaterializeBundledResources(dir)
	if err != nil {
		t.Fatalf("materialize failed: %v", err)
	}

	for _, name := range []string{"BDInfo.exe", "BDInfoDataSubstractor.exe", "lzfse.dll"} {
		p := filepath.Join(root, "bdinfo", "windows", name)
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
		t.Logf("%s size=%d", name, info.Size())
		if info.Size() == 0 {
			t.Fatalf("empty file: %s", p)
		}
	}

	// 站点资源仍需存在
	if _, err := os.Stat(filepath.Join(root, "sites_data.json")); err != nil {
		t.Fatalf("missing sites_data.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configs", "global_mappings.yaml")); err != nil {
		t.Fatalf("missing global_mappings.yaml: %v", err)
	}
}
