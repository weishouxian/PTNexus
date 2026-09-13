package fetch

import "time"

// FetchBinaryWithCookie 使用 Cookie 抓取二进制内容（导出包装，供跨包反查复用）。
// 参数/返回：targetURL 为目标地址，referer 为引用页，cookie 为站点登录态，timeout 为请求超时；返回响应字节。
// 失败场景：网络错误、HTTP 非 2xx、命中登录页时返回 ErrSourceCookieExpired。
// 副作用：发起网络请求。
func FetchBinaryWithCookie(targetURL, referer, cookie string, timeout time.Duration) ([]byte, error) {
	return fetchBinaryWithCookie(targetURL, referer, cookie, timeout)
}

// BuildDirectDownloadURL 根据站点根地址与种子 ID 构造直链下载地址（导出包装）。
// 参数/返回：baseURL 为站点根地址，siteCode 为站点代码，detailURL 为详情页地址，torrentID 为种子 ID/URL，passkey 为站点密钥；返回直链，无法构造时返回空串。
// 失败场景：不适用（失败以空串表示）。
// 副作用：无。
func BuildDirectDownloadURL(baseURL, siteCode, detailURL, torrentID, passkey string) string {
	return buildDirectDownloadURL(baseURL, siteCode, detailURL, torrentID, passkey)
}

// ExtractDownloadCandidatesFromDetail 从详情页 HTML 中提取下载链接候选（导出包装）。
// 参数/返回：html 为页面文本，baseURL 为站点根地址，siteCode 为站点代码，detailURL 为详情页地址；返回候选下载链接列表。
// 失败场景：不适用（无匹配时返回空列表）。
// 副作用：无。
func ExtractDownloadCandidatesFromDetail(html, baseURL, siteCode, detailURL string) []string {
	return extractDownloadCandidatesFromDetail(html, baseURL, siteCode, detailURL)
}

// IsLikelyTorrent 判断响应内容是否为 torrent 文件（导出包装）。
// 参数/返回：content 为响应字节；返回是否为合法 torrent 内容。
// 失败场景：不适用。
// 副作用：无。
func IsLikelyTorrent(content []byte) bool {
	return isLikelyTorrent(content)
}
