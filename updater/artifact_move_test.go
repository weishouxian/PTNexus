package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 覆盖 moveDirPortable / copyDirTreePortable / isMountPointPath：
// 这些函数是「baseDir 迁移」在非标准文件系统上的兜底路径，2026-10-11 为
// `rename /app/server ...: invalid cross-device link` 的线上故障而引入。

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func TestCopyDirTreePortablePreservesTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "server")
	writeTestFile(t, filepath.Join(src, "server"), "#!/bin/sh\necho hi\n", 0o755)
	writeTestFile(t, filepath.Join(src, "sites_data.json"), "{}", 0o644)
	writeTestFile(t, filepath.Join(src, "configs", "site.yaml"), "a: 1", 0o644)
	writeTestFile(t, filepath.Join(src, "dist", "index.html"), "<html/>", 0o644)

	dst := filepath.Join(root, "server.image.probe")
	if err := copyDirTreePortable(src, dst); err != nil {
		t.Fatalf("copyDirTreePortable: %v", err)
	}

	for _, rel := range []string{"server", "sites_data.json", filepath.Join("configs", "site.yaml"), filepath.Join("dist", "index.html")} {
		want, err := os.ReadFile(filepath.Join(src, rel))
		if err != nil {
			t.Fatalf("read src %s: %v", rel, err)
		}
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("read dst %s: %v", rel, err)
		}
		if string(want) != string(got) {
			t.Fatalf("%s 内容不一致: want=%q got=%q", rel, want, got)
		}
	}

	srcSt, err := os.Stat(filepath.Join(src, "server"))
	if err != nil {
		t.Fatal(err)
	}
	dstSt, err := os.Stat(filepath.Join(dst, "server"))
	if err != nil {
		t.Fatal(err)
	}
	if dstSt.Mode().Perm() != srcSt.Mode().Perm() {
		t.Fatalf("权限位未保留: src=%v dst=%v", srcSt.Mode(), dstSt.Mode())
	}

	// 复制不得破坏源目录
	if _, err := os.Stat(filepath.Join(src, "server")); err != nil {
		t.Fatalf("源目录被破坏: %v", err)
	}
}

func TestMoveDirPortableRenamePath(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "a")
	writeTestFile(t, filepath.Join(src, "f.txt"), "x", 0o644)

	dst := filepath.Join(root, "b")
	fallback, err := moveDirPortable(src, dst)
	if err != nil {
		t.Fatalf("moveDirPortable: %v", err)
	}
	if fallback {
		t.Fatalf("同目录迁移不应走复制回退")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源目录应已被移走: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "f.txt")); err != nil {
		t.Fatalf("目标目录内容缺失: %v", err)
	}
}

func TestMoveDirPortableRejectsInvalidInput(t *testing.T) {
	root := t.TempDir()
	if _, err := moveDirPortable("", filepath.Join(root, "b")); err == nil {
		t.Fatalf("空路径应报错")
	}
	if _, err := moveDirPortable(filepath.Join(root, "nope"), filepath.Join(root, "b")); err == nil {
		t.Fatalf("源不存在时应报错")
	}
}

func TestIsMountPointPathTempDirIsNotMountPoint(t *testing.T) {
	// 普通目录（含非 Linux 环境，无 /proc/self/mountinfo）不得被误判为挂载点，
	// 否则会错误地阻断正常的运行时迁移。
	if isMountPointPath(t.TempDir()) {
		t.Fatalf("普通临时目录不应判定为挂载点")
	}
}

func TestIsMountPointPathDetectsRootOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("仅在 Linux 上校验 mountinfo 解析")
	}
	if _, err := os.Stat("/proc/self/mountinfo"); err != nil {
		t.Skip("无 /proc/self/mountinfo")
	}
	if !isMountPointPath("/") {
		t.Fatalf("根目录应被识别为挂载点")
	}
}

// TestMoveDirPortableCrossDeviceFallback 用 tmpfs（/dev/shm）制造真实的
// EXDEV「invalid cross-device link」，验证会退回「复制 + 删除」且内容完整。
func TestMoveDirPortableCrossDeviceFallback(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("仅在 Linux 上验证跨设备回退")
	}
	if info, err := os.Stat("/dev/shm"); err != nil || !info.IsDir() {
		t.Skip("/dev/shm 不可用，无法制造跨设备场景")
	}

	srcRoot := t.TempDir()
	src := filepath.Join(srcRoot, "server")
	writeTestFile(t, filepath.Join(src, "server"), "binary", 0o755)
	writeTestFile(t, filepath.Join(src, "configs", "site.yaml"), "a: 1", 0o644)

	dstRoot, err := os.MkdirTemp("/dev/shm", "ptnexus-probe-")
	if err != nil {
		t.Skipf("无法在 /dev/shm 建目录: %v", err)
	}
	defer os.RemoveAll(dstRoot)
	dst := filepath.Join(dstRoot, "server.image.probe")

	// 先确认这确实是跨设备场景，否则该用例没有意义
	if err := os.Rename(src, dst); err == nil {
		_ = os.Rename(dst, src)
		t.Skip("当前 /dev/shm 与临时目录同设备，无法制造 EXDEV")
	} else if !isDirMoveFallbackErr(err) {
		t.Fatalf("预期识别为跨设备错误，实际: %v", err)
	}

	fallback, err := moveDirPortable(src, dst)
	if err != nil {
		t.Fatalf("跨设备迁移失败: %v", err)
	}
	if !fallback {
		t.Fatalf("应走复制回退路径")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("源目录应已被删除: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "server"))
	if err != nil {
		t.Fatalf("目标缺少可执行文件: %v", err)
	}
	if string(got) != "binary" {
		t.Fatalf("内容不一致: %q", got)
	}
	st, err := os.Stat(filepath.Join(dst, "server"))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o100 == 0 {
		t.Fatalf("可执行位丢失: %v", st.Mode())
	}
}
