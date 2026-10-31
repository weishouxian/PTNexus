// Package colaptapi 提供可乐PT（www.colapt.com）与 PTNexus 交互所需的公共能力：
// 站点识别、种子 ID 解析、详情页地址与 JSON API 客户端（种子详情/作品/下载授权）。
//
// 站点形态（2026-10 实测）：
//   - 前端是 Vite 打包的纯 SPA，详情页 /torrents/{id} 未登录直接 302 到 /login，
//     页面里既没有种子下载链接，也没有服务端渲染的标题/简介字段，HTML 解析完全用不上；
//   - 后端是自研 JSON API，统一前缀 /api/v1，契约（openapi 3.4）内嵌在 SPA 的 session-*.js 里；
//   - 鉴权只认账号级永久 API Key（请求头 X-API-Key），浏览器 Cookie 对接口无效；
//   - 下载 .torrent 分两步：先 POST /automation/torrents/{id}/authorizations 拿授权，
//     再 GET /automation/torrents/{id}/download?authorization_id=<授权ID>。
package colaptapi

import (
	"regexp"
	"strings"
)

const (
	// DefaultBaseURL 是站点根地址。
	DefaultBaseURL = "https://www.colapt.com"
	// APIPrefix 是站点 JSON API 的统一前缀。
	APIPrefix = "/api/v1"
	// DefaultAPIKeyHeader 是站点约定的鉴权请求头。
	DefaultAPIKeyHeader = "X-API-Key"
	// UserAgent 是请求站点时使用的 UA。
	UserAgent = "PTNexus/1.0 (+https://github.com/weishouxian/PTNexus)"
)

var (
	// siteNeedles 是识别可乐PT 的关键词（站点代码/根地址/详情页地址任一命中即可）。
	siteNeedles = []string{"colapt", "cola", "可乐"}
	// rePlainID 匹配纯数字种子 ID。
	rePlainID = regexp.MustCompile(`^\d{1,20}$`)
	// reTorrentPath 匹配详情页路由 /torrents/{id}。
	reTorrentPath = regexp.MustCompile(`(?i)/torrents/(\d{1,20})`)
	// reAPITorrentPath 匹配接口路径 /api/v1/torrents/{id} 与 /api/v1/automation/torrents/{id}。
	reAPITorrentPath = regexp.MustCompile(`(?i)/(?:automation/)?torrents/(\d{1,20})`)
)

// IsSite 判断给定站点代码/根地址/详情页地址是否属于可乐PT。
// 参数/返回：三个参数任传入即可；命中返回 true。
// 失败场景：不适用。
// 副作用：无。
func IsSite(siteCode, baseURL, detailURL string) bool {
	for _, needle := range siteNeedles {
		for _, value := range []string{siteCode, baseURL, detailURL} {
			if strings.Contains(strings.ToLower(value), strings.ToLower(needle)) {
				return true
			}
		}
	}
	return false
}

// ExtractTorrentID 从详情页地址、接口地址或纯数字中解析种子 ID。
// 参数/返回：value 支持 6211、/torrents/6211、https://www.colapt.com/torrents/6211、
// /api/v1/automation/torrents/6211；无法解析时返回空串。
// 失败场景：不适用。
// 副作用：无。
func ExtractTorrentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if rePlainID.MatchString(trimmed) {
		return trimmed
	}
	if match := reTorrentPath.FindStringSubmatch(trimmed); len(match) == 2 {
		return match[1]
	}
	if match := reAPITorrentPath.FindStringSubmatch(trimmed); len(match) == 2 {
		return match[1]
	}
	return ""
}

// NormalizeBaseURL 归一站点根地址（补协议、去掉尾部斜杠与 /api/v1 后缀）。
// 参数/返回：baseURL 为空时回退 DefaultBaseURL；站点表里存的常是 www.colapt.com 这种无协议写法。
// 失败场景：不适用。
// 副作用：无。
func NormalizeBaseURL(baseURL string) string {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return DefaultBaseURL
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		trimmed = "https://" + trimmed
		lower = strings.ToLower(trimmed)
	}
	trimmed = strings.TrimRight(trimmed, "/")
	for strings.HasSuffix(strings.ToLower(trimmed), APIPrefix) {
		trimmed = strings.TrimRight(trimmed[:len(trimmed)-len(APIPrefix)], "/")
	}
	return trimmed
}

// APIBase 返回站点 JSON API 的基地址（根地址 + /api/v1）。
// 参数/返回：baseURL 可传根地址或已带 /api/v1 的地址，重复后缀会被去掉。
// 失败场景：不适用。
// 副作用：无。
func APIBase(baseURL string) string {
	return NormalizeBaseURL(baseURL) + APIPrefix
}

// DetailURL 拼接种子详情页地址。
// 参数/返回：id 为种子 ID 或已是 http(s) 地址时原样返回。
// 失败场景：不适用。
// 副作用：无。
func DetailURL(baseURL, id string) string {
	trimmed := strings.TrimSpace(id)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return trimmed
	}
	torrentID := ExtractTorrentID(trimmed)
	if torrentID == "" {
		return ""
	}
	return NormalizeBaseURL(baseURL) + "/torrents/" + torrentID
}
