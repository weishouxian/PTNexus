package repair

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// FetchPageWithTimeout 按 GET 方式请求页面文本。
func FetchPageWithTimeout(url string) (string, error) {
	return FetchPageWithMethod(url, http.MethodGet)
}

// FetchPageWithMethod 使用指定 HTTP 方法请求页面文本。
func FetchPageWithMethod(url, method string) (string, error) {
	body, _, err := FetchPageWithMethodStatus(url, method)
	return body, err
}

// FetchPageWithMethodStatus 使用指定 HTTP 方法请求页面文本，并返回 HTTP 状态码。
// 参数/返回：url 为请求地址；method 为 HTTP 方法（默认 GET）；返回响应文本、状态码与错误。
// 失败场景：请求失败或状态码非 2xx 时返回错误，同时尽量带回响应体预览供诊断。
// 副作用：发起外部网络请求。
func FetchPageWithMethodStatus(url, method string) (string, int, error) {
	trimmed := strings.TrimSpace(url)
	if trimmed == "" {
		return "", 0, fmt.Errorf("empty url")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" {
		method = http.MethodGet
	}

	client := &http.Client{Timeout: 12 * time.Second}
	req, err := http.NewRequest(method, trimmed, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json,text/plain,*/*;q=0.8,text/html;q=0.6,application/xhtml+xml,application/xml;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return string(preview), resp.StatusCode, fmt.Errorf("http status %d", resp.StatusCode)
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", resp.StatusCode, err
	}
	return string(content), resp.StatusCode, nil
}

// NormalizeExternalLink 按正则提取并规范外部链接。
func NormalizeExternalLink(link string, pattern *regexp.Regexp) string {
	trimmed := strings.TrimSpace(link)
	if trimmed == "" {
		return ""
	}
	if pattern == nil {
		return trimmed
	}
	matched := pattern.FindString(trimmed)
	if matched == "" {
		return trimmed
	}
	return strings.TrimRight(matched, "/")
}

// ExtractScreenshotURLsFromSource 从 source_info 中提取截图 URL，并剔除海报 URL。
func ExtractScreenshotURLsFromSource(sourceInfo map[string]any) []string {
	if sourceInfo == nil {
		return []string{}
	}
	segments := []string{
		toStringAny(sourceInfo["screenshots"], ""),
		toStringAny(sourceInfo["body"], ""),
		toStringAny(sourceInfo["statement"], ""),
		toStringAny(sourceInfo["description"], ""),
	}

	posterSet := map[string]struct{}{}
	for _, item := range ExtractImageURLsFromText(toStringAny(sourceInfo["poster"], "")) {
		posterSet[item] = struct{}{}
	}

	result := make([]string, 0)
	for _, segment := range segments {
		for _, imageURL := range ExtractImageURLsFromText(segment) {
			if _, isPoster := posterSet[imageURL]; isPoster {
				continue
			}
			result = appendUniqueStringLocal(result, imageURL)
		}
	}
	return result
}
