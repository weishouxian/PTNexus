package siteprofile

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
)

// 本文件提供「把抓到的页面落盘」的排查开关。
//
// 为什么需要：站点皮肤千奇百怪，同一个字段（做种积分）可能被标签/换行/内联标签拆成
// 好几段文本，光看解析结果无法判断原文长什么样，只能靠 PHP 源码猜。
// 打开开关后每份响应都写到磁盘，直接看原始 HTML 定位。
//
// 怎么打开（生产容器内，不改代码也不重启）：
//
//	mkdir /app/data/tmp/siteprofile-dump      # 存在即视为开启
//
// 也可用环境变量 PTN_SITEPROFILE_DUMP_DIR 指定其他目录（优先级更高）。
// 排查完记得删掉目录，否则每次刷新都会写盘。
const (
	debugDumpEnvKey    = "PTN_SITEPROFILE_DUMP_DIR"
	debugDumpMountDir  = "/app/data/tmp/siteprofile-dump"
	debugDumpMaxBytes  = 6 << 20
	debugDumpFilePerms = 0o644
)

// debugDumpDir 返回当前生效的落盘目录；未开启时返回空串。
// 参数/返回：无参数；返回目录路径（不存在或未开启则为空）。
// 失败场景：无。
// 副作用：可能创建目录。
func debugDumpDir() string {
	dir := strings.TrimSpace(os.Getenv(debugDumpEnvKey))
	if dir == "" {
		if info, err := os.Stat(debugDumpMountDir); err == nil && info.IsDir() {
			dir = debugDumpMountDir
		}
	}
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return dir
}

// dumpDebugBody 把一次响应的原始 body 写进落盘目录。
// 参数/返回：site 为站点代码，name 为地址标识（用它命名文件），body 为响应体。
// 失败场景：写盘失败只记一条警告，绝不影响抓取流程。
// 副作用：写文件。
func dumpDebugBody(site, name string, body []byte) {
	dir := debugDumpDir()
	if dir == "" || len(body) == 0 {
		return
	}
	if len(body) > debugDumpMaxBytes {
		body = body[:debugDumpMaxBytes]
	}
	file := filepath.Join(dir, sanitizeDumpName(site)+"_"+sanitizeDumpName(name)+".html")
	if err := os.WriteFile(file, body, debugDumpFilePerms); err != nil {
		logx.Warnf(logModule, "抓取页面落盘失败 file=%s err=%v", file, err)
		return
	}
	logx.Infof(logModule, "抓取页面已落盘 file=%s bytes=%d", file, len(body))
}

// sanitizeDumpName 把站点代码/地址里的路径符号换成下划线，避免生成子目录。
func sanitizeDumpName(raw string) string {
	replacer := strings.NewReplacer(
		"/", "_", "?", "_", "=", "_", "&", "_", " ", "_", ":", "_", "\\", "_",
	)
	name := strings.Trim(replacer.Replace(strings.TrimSpace(raw)), "_")
	if name == "" {
		return "page"
	}
	return name
}
