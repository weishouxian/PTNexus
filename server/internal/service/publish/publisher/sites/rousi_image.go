package sites

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // 注册 PNG 解码器：站点截图常见 PNG，压到 2 MiB 以内需要先能解码
	"math"
	"os"
)

// 站点对单张图片的体积上限是 2 MiB（前端文案：单张图片不能超过 2 MiB）。
// 生产镜像里带静态 ffmpeg，纯 Go 压缩只在桌面端 / 无 ffmpeg 的环境下兜底。
var (
	rousiGoCompressLongEdges = []int{0, 2560, 1920, 1600, 1280, 1024, 800, 640, 480}
	rousiGoCompressQualities = []int{88, 82, 76, 70, 64, 58, 52, 46, 40, 34, 28}
)

// compressRousiImageToJPEG 用纯 Go 把图片压成不超过 maxBytes 的 JPEG。
// 策略：按长边逐级降采样，每一档再逐级降质，一旦体积达标就落盘返回。
// 参数/返回：inputPath 为原图路径，outputPath 为输出路径，maxBytes 为体积上限；成功返回 true。
// 失败场景：文件不可读、格式无法用标准库解码（如 WebP）、所有阶梯都压不进上限。
func compressRousiImageToJPEG(inputPath string, outputPath string, maxBytes int64) bool {
	file, err := os.Open(inputPath)
	if err != nil {
		return false
	}
	defer file.Close()

	src, _, err := image.Decode(file)
	if err != nil {
		// WebP 标准库解不了，交给调用方按「原图透传」或跳过处理。
		return false
	}

	for _, longEdge := range rousiGoCompressLongEdges {
		frame := src
		if longEdge > 0 {
			frame = resizeRousiImageLongEdge(src, longEdge)
		}
		for _, quality := range rousiGoCompressQualities {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, frame, &jpeg.Options{Quality: quality}); err != nil {
				break
			}
			if int64(buf.Len()) > maxBytes {
				continue
			}
			if err := os.WriteFile(outputPath, buf.Bytes(), 0o644); err != nil {
				return false
			}
			return true
		}
	}
	return false
}

// resizeRousiImageLongEdge 把图片按长边等比缩到不超过 maxEdge（双线性采样）。
// 说明：标准库没有缩放能力，这里手写双线性插值，避免为此引入第三方依赖。
func resizeRousiImageLongEdge(src image.Image, maxEdge int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || maxEdge <= 0 {
		return src
	}
	longEdge := width
	if height > longEdge {
		longEdge = height
	}
	if longEdge <= maxEdge {
		return src
	}

	scale := float64(maxEdge) / float64(longEdge)
	targetWidth := int(math.Round(float64(width) * scale))
	targetHeight := int(math.Round(float64(height) * scale))
	if targetWidth < 1 {
		targetWidth = 1
	}
	if targetHeight < 1 {
		targetHeight = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		srcY := (float64(y)+0.5)/scale - 0.5
		y0 := int(math.Floor(srcY))
		fy := srcY - float64(y0)
		if y0 < 0 {
			y0, fy = 0, 0
		}
		y1 := y0 + 1
		if y1 >= height {
			y1 = height - 1
		}
		for x := 0; x < targetWidth; x++ {
			srcX := (float64(x)+0.5)/scale - 0.5
			x0 := int(math.Floor(srcX))
			fx := srcX - float64(x0)
			if x0 < 0 {
				x0, fx = 0, 0
			}
			x1 := x0 + 1
			if x1 >= width {
				x1 = width - 1
			}

			c00 := rousiToRGBA(src.At(bounds.Min.X+x0, bounds.Min.Y+y0))
			c10 := rousiToRGBA(src.At(bounds.Min.X+x1, bounds.Min.Y+y0))
			c01 := rousiToRGBA(src.At(bounds.Min.X+x0, bounds.Min.Y+y1))
			c11 := rousiToRGBA(src.At(bounds.Min.X+x1, bounds.Min.Y+y1))
			dst.SetRGBA(x, y, rousiBilinearRGBA(c00, c10, c01, c11, fx, fy))
		}
	}
	return dst
}

// rousiToRGBA 把任意色彩模型转成 8 位 RGBA。
func rousiToRGBA(value color.Color) color.RGBA {
	return color.RGBAModel.Convert(value).(color.RGBA)
}

// rousiBilinearRGBA 按 fx/fy 做双线性混色，输出不透明像素。
func rousiBilinearRGBA(c00 color.RGBA, c10 color.RGBA, c01 color.RGBA, c11 color.RGBA, fx float64, fy float64) color.RGBA {
	mix := func(leftTop uint8, rightTop uint8, leftBottom uint8, rightBottom uint8) uint8 {
		top := float64(leftTop)*(1-fx) + float64(rightTop)*fx
		bottom := float64(leftBottom)*(1-fx) + float64(rightBottom)*fx
		value := top*(1-fy) + bottom*fy
		if value < 0 {
			value = 0
		}
		if value > 255 {
			value = 255
		}
		return uint8(value + 0.5)
	}
	return color.RGBA{
		R: mix(c00.R, c10.R, c01.R, c11.R),
		G: mix(c00.G, c10.G, c01.G, c11.G),
		B: mix(c00.B, c10.B, c01.B, c11.B),
		A: 255,
	}
}
