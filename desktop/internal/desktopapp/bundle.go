package desktopapp

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// bundleFS 内嵌随桌面应用发布的只读资源（站点清单与站点映射）。
// 目的：让「单文件 exe」在没有外挂 sites_data.json / configs 的情况下也能完整运行。
//
// 说明：bundle/ 目录由 scripts/package-desktop.sh 的 sync-bundle 阶段从
// server/sites_data.json 与 server/configs/ 同步而来，构建前会自动刷新。
//
//go:embed all:bundle
var bundleFS embed.FS

// bundledResourcesDirName 是内嵌资源在用户目录下的展开目录名。
const bundledResourcesDirName = "resources"

// MaterializeBundledResources 把内嵌资源展开到用户目录，返回展开后的根目录路径。
//
// 策略：每次启动全量覆盖。内置站点映射属于「随 exe 发布的默认值」，
// 以 exe 为准可避免升级后残留旧映射导致发种字段错位。
// 需要覆盖时请使用 PTNEXUS_RESOURCE_DIR 指向自定义目录。
//
// 失败场景：目录创建或文件写入失败时返回 error，由调用方决定是否降级。
func MaterializeBundledResources(userBaseDir string) (string, error) {
	base := strings.TrimSpace(userBaseDir)
	if base == "" {
		return "", fmt.Errorf("missing user base dir")
	}

	target := filepath.Join(base, bundledResourcesDirName)
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", fmt.Errorf("create bundled resources dir failed: %w", err)
	}

	root, err := fs.Sub(bundleFS, "bundle")
	if err != nil {
		return "", fmt.Errorf("open embedded bundle failed: %w", err)
	}

	walkErr := fs.WalkDir(root, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}

		dest := filepath.Join(target, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}

		// bdinfo 是上百 MB 的可执行文件，内容随版本稳定。已展开且大小一致时跳过重写，
		// 否则每次启动都要重写整个 bdinfo 目录，首屏会明显变慢。
		// 站点映射（configs/sites_data.json）仍全量覆盖，确保默认值永远跟随 exe 版本。
		if isBDInfoBundlePath(path) {
			if info, infoErr := entry.Info(); infoErr == nil {
				if existing, ok := fileSize(dest); ok && existing == info.Size() {
					return nil
				}
			}
		}

		payload, readErr := fs.ReadFile(root, path)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(dest, payload, bundleFilePerm(path))
	})
	if walkErr != nil {
		return "", fmt.Errorf("materialize bundled resources failed: %w", walkErr)
	}

	return target, nil
}

// isBDInfoBundlePath 判断内嵌路径是否属于 BDInfo 工具目录。
func isBDInfoBundlePath(path string) bool {
	return path == "bdinfo" || strings.HasPrefix(path, "bdinfo/") || strings.HasPrefix(path, "bdinfo\\")
}

// bundleFilePerm 返回内嵌文件展开后的权限。
// bdinfo 下是可直接执行的外部程序，补上可执行位；其余资源保持只读。
func bundleFilePerm(path string) os.FileMode {
	if isBDInfoBundlePath(path) {
		return 0o755
	}
	return 0o644
}

// fileSize 返回文件大小；不存在或是目录时返回 false。
func fileSize(path string) (int64, bool) {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0, false
	}
	return info.Size(), true
}
