package sites

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// 站点把截图区第一张当封面，所以上传顺序必须是「海报 → 截图」，且海报只取一张。
func TestCollectRousiImageSourcesCoverFirst(t *testing.T) {
	uploadData := map[string]any{
		"intro": map[string]any{
			"poster":      "[img]https://img.example.com/poster-a.jpg[/img][img]https://img.example.com/poster-b.jpg[/img]",
			"screenshots": "[img]https://img.example.com/shot-1.jpg[/img]\n[img]https://img.example.com/shot-2.jpg[/img]",
		},
		"images": []any{"data:image/png;base64,AAAA", "https://img.example.com/shot-1.jpg"},
	}

	grouped := collectRousiImageSources(uploadData)
	if len(grouped.Cover) != 1 {
		t.Fatalf("封面应只取一张，实际 %d: %#v", len(grouped.Cover), grouped.Cover)
	}
	if grouped.Cover[0].URL != "https://img.example.com/poster-a.jpg" {
		t.Fatalf("封面应取简介海报的第一张，实际 %q", grouped.Cover[0].URL)
	}

	ordered := flattenRousiImageSources(grouped)
	if len(ordered) != 4 {
		t.Fatalf("去重后应有 4 张（封面 1 + 截图 3），实际 %d: %#v", len(ordered), ordered)
	}
	if ordered[0].URL != grouped.Cover[0].URL {
		t.Fatalf("第一张必须是封面，实际 %q", ordered[0].URL)
	}
	if ordered[1].URL != "data:image/png;base64,AAAA" {
		t.Fatalf("本地生成的截图应排在简介截图之前，实际 %q", ordered[1].URL)
	}
}

// 简介里没有海报时，第一张截图自动兼任封面。
func TestCollectRousiImageSourcesFallbackToScreenshot(t *testing.T) {
	uploadData := map[string]any{
		"intro": map[string]any{
			"screenshots": "[img]https://img.example.com/shot-1.jpg[/img]",
		},
	}

	grouped := collectRousiImageSources(uploadData)
	if len(grouped.Cover) != 0 {
		t.Fatalf("无海报时封面来源应为空，实际 %#v", grouped.Cover)
	}
	ordered := flattenRousiImageSources(grouped)
	if len(ordered) != 1 || ordered[0].URL != "https://img.example.com/shot-1.jpg" {
		t.Fatalf("截图应原样保留并可当封面，实际 %#v", ordered)
	}
}

// 纯 Go 兜底压缩：超过 2 MiB 的原图必须压进上限，且产物仍是可解码的 JPEG。
func TestCompressRousiImageToJPEGUnderLimit(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.png")
	targetPath := filepath.Join(dir, "target.jpg")

	width, height := 2400, 1600
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	random := rand.New(rand.NewSource(20261114))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			canvas.SetRGBA(x, y, color.RGBA{
				R: uint8(random.Intn(256)),
				G: uint8(random.Intn(256)),
				B: uint8(random.Intn(256)),
				A: 255,
			})
		}
	}

	file, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("创建测试图片失败: %v", err)
	}
	if err := png.Encode(file, canvas); err != nil {
		file.Close()
		t.Fatalf("编码测试图片失败: %v", err)
	}
	file.Close()

	info, err := os.Stat(sourcePath)
	if err != nil {
		t.Fatalf("读取测试图片失败: %v", err)
	}
	if info.Size() <= rousiScreenshotMaxBytes {
		t.Fatalf("测试图片需要大于上限才有意义，实际 %d 字节", info.Size())
	}

	if !compressRousiImageToJPEG(sourcePath, targetPath, rousiScreenshotMaxBytes) {
		t.Fatalf("压缩失败：原图 %d 字节未能压进 %d 字节", info.Size(), rousiScreenshotMaxBytes)
	}

	compressed, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("读取压缩结果失败: %v", err)
	}
	if int64(len(compressed)) > rousiScreenshotMaxBytes {
		t.Fatalf("压缩后仍超限：%d > %d", len(compressed), rousiScreenshotMaxBytes)
	}
	if _, err := jpeg.Decode(bytes.NewReader(compressed)); err != nil {
		t.Fatalf("压缩结果不是合法 JPEG: %v", err)
	}
}
