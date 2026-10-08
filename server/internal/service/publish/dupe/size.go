package dupe

import (
	"fmt"
	"math"
	"strings"
)

// sizeUnits 为体积展示单位，按量级从大到小排列。
var sizeUnits = []struct {
	suffix string
	scale  float64
}{
	{"TiB", 1 << 40},
	{"GiB", 1 << 30},
	{"MiB", 1 << 20},
	{"KiB", 1 << 10},
}

// FormatSize 把字节数格式化为便于日志阅读的体积文本。
// 参数/返回：size 为字节数；返回形如 "57.07 GiB" 的文本。
// 副作用：无。
func FormatSize(size int64) string {
	if size < 0 {
		size = 0
	}
	value := float64(size)
	for _, unit := range sizeUnits {
		if value >= unit.scale {
			return fmt.Sprintf("%.2f %s", value/unit.scale, unit.suffix)
		}
	}
	if size == 0 {
		return "0 B"
	}
	return fmt.Sprintf("%d B", size)
}

// FormatSizeMB 把字节数格式化为以 MB 为单位的文本（1 MB = 1024² 字节，与站点管理页口径一致）。
// 参数/返回：size 为字节数；返回形如 "1024 MB" / "5.2 MB" 的文本。
// 说明：站点管理页的「体积容差」以 MB 录入与展示，dupe 判定提示沿用同一单位，
// 这样「体积差」与「容差」可直接对照阅读；小于 0.05 MB 的差值退回 FormatSize 的自动单位，
// 避免被显示成 0.0 MB 丢掉精度。
// 副作用：无。
func FormatSizeMB(size int64) string {
	if size <= 0 {
		return "0 MB"
	}
	value := float64(size) / float64(1<<20)
	if value < 0.05 {
		return FormatSize(size)
	}
	if math.Abs(value-math.Round(value)) < 0.05 {
		return fmt.Sprintf("%.0f MB", math.Round(value))
	}
	return fmt.Sprintf("%.1f MB", value)
}

// buildTorrentDetailURL 拼接 NexusPHP 详情页链接。
// 参数/返回：baseURL 为站点根地址；torrentID 为种子 ID。
// 返回详情页 URL，参数缺失时返回空串。
// 副作用：无。
func buildTorrentDetailURL(baseURL string, torrentID string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	trimmedID := strings.TrimSpace(torrentID)
	if trimmedBase == "" || trimmedID == "" {
		return ""
	}
	return fmt.Sprintf("%s/details.php?id=%s", trimmedBase, trimmedID)
}
