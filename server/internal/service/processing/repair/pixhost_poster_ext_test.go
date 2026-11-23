package repair

import (
	"os"
	"strings"
	"testing"
)

// jpegMagic / pngMagic 只保留够 http.DetectContentType 识别的文件头。
var (
	jpegMagic = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'}
	pngMagic  = []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00}
)

// 末日图床扩展名白名单只认 jpg/jpeg/png/gif/webp/bmp；
// 旧实现用 mime.ExtensionsByType 取第一个，容器内会随机拿到 .jpe/.jfif 导致「不支持的文件类型」。
func TestPosterImageExtensionNeverReturnsJpeOrJfif(t *testing.T) {
	inputs := []struct {
		name        string
		contentType string
		data        []byte
		want        string
	}{
		{"jpeg 标准", "image/jpeg", jpegMagic, ".jpg"},
		{"jpeg 带参数", "image/jpeg; charset=binary", jpegMagic, ".jpg"},
		{"jpeg 大写", "IMAGE/JPEG", jpegMagic, ".jpg"},
		{"pjpeg", "image/pjpeg", jpegMagic, ".jpg"},
		{"png", "image/png", pngMagic, ".png"},
		{"webp", "image/webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), ".webp"},
		{"gif", "image/gif", []byte("GIF89a\x00\x00"), ".gif"},
		{"类型为空时按文件头嗅探", "", jpegMagic, ".jpg"},
		{"octet-stream 时按文件头嗅探", "application/octet-stream", pngMagic, ".png"},
		{"完全不可识别兜底 jpg", "application/octet-stream", []byte("not an image"), ".jpg"},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			got := posterImageExtension(tc.contentType, tc.data)
			if got != tc.want {
				t.Fatalf("posterImageExtension(%q) = %q, want %q", tc.contentType, got, tc.want)
			}
			if got == ".jpe" || got == ".jfif" {
				t.Fatalf("扩展名 %q 会被末日图床拒绝（白名单不含 jpe/jfif）", got)
			}
		})
	}
}

// 临时海报文件必须以图床可接受的扩展名落盘 —— 上传时文件名直接取 filepath.Base(tmpPath)。
func TestWritePosterTempFileUsesUploadableExtension(t *testing.T) {
	for _, contentType := range []string{"image/jpeg", "image/jpeg; charset=binary", "image/png"} {
		path, err := writePosterTempFile(jpegMagic, contentType)
		if err != nil {
			t.Fatalf("writePosterTempFile(%q) err = %v", contentType, err)
		}
		base := strings.ToLower(path)
		if !strings.HasSuffix(base, ".jpg") && !strings.HasSuffix(base, ".png") {
			t.Fatalf("临时文件名 %q 的扩展名不被末日图床接受", base)
		}
		if strings.HasSuffix(base, ".jpe") || strings.HasSuffix(base, ".jfif") {
			t.Fatalf("临时文件名 %q 会被末日图床拒绝", base)
		}
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("临时文件应存在: %v", statErr)
		}
		_ = os.Remove(path)
	}
}
