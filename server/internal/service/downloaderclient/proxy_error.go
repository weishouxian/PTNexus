package downloaderclient

import (
	"net/http"
	"strings"
)

// proxyCandidateMissMarkers 描述盒子代理「候选路径本身不可用」时的提示片段。
// 这类失败属于候选维度（换一个候选路径可能就成功），因此统一归一为 400，调用方据此继续尝试下一个候选；
// 其余错误（代理不可达、鉴权失败、响应解析失败等）保持原状态码，调用方应立即中止候选循环。
//
// 背景：代理的媒体会话在 stat 失败时返回 HTTP 500（body 形如 failed to access media path: stat ...: no such file or directory），
// 而调用方只对 400 继续下一个候选。若不归一，多候选探测会在第一个候选上直接中断，
// 导致「原始路径不可达、映射后路径可达」这类部署永远走不到正确的候选。
var proxyCandidateMissMarkers = []string{
	"failed to access media path",
	"failed to scan directory",
	"failed to find target video file",
	"no such file or directory",
	"no such directory",
	"not a directory",
	"path does not exist",
	"paths not found",
	"cannot find the path",
	"cannot find file",
	"cannot find the file",
	"路径不存在",
	"找不到",
	"系统找不到指定的路径",
	"系统找不到指定的文件",
}

// NormalizeProxyCandidateStatus 把代理返回的失败状态码归一为「可继续下一个候选(400)」或「需立即中止(原状态码)」。
// 参数/返回：statusCode 为代理 HTTP 状态码（0 表示调用方自造码，例如 HTTP 200 + success=false）；message 为代理返回的错误文本；返回归一后的状态码。
// 失败场景：无。
// 副作用：无。
func NormalizeProxyCandidateStatus(statusCode int, message string) int {
	switch statusCode {
	case http.StatusBadRequest, http.StatusNotFound, http.StatusGone:
		return http.StatusBadRequest
	}
	lowered := strings.ToLower(strings.TrimSpace(message))
	if lowered == "" {
		return statusCode
	}
	for _, marker := range proxyCandidateMissMarkers {
		if strings.Contains(lowered, strings.ToLower(marker)) {
			return http.StatusBadRequest
		}
	}
	return statusCode
}

// newProxyHTTPError 构造「代理 HTTP 状态码非 2xx」的错误，并按候选维度归一状态码。
// 参数/返回：statusCode 为原始 HTTP 状态码；body 为响应体文本；返回归一后的代理错误。
// 失败场景：无。
// 副作用：无。
func newProxyHTTPError(statusCode int, body string) *ProxyAPIError {
	return &ProxyAPIError{
		StatusCode: NormalizeProxyCandidateStatus(statusCode, body),
		Message:    compactProxyBody(body),
	}
}

// newProxyResponseFailure 构造「代理明确返回失败」（HTTP 200 + success=false，或返回体缺关键内容）的错误。
// 参数/返回：message 为代理给出的失败原因；返回 400（候选未命中）或 500（其它失败）的代理错误。
// 失败场景：无。
// 副作用：无。
func newProxyResponseFailure(message string) *ProxyAPIError {
	statusCode := NormalizeProxyCandidateStatus(0, message)
	if statusCode == 0 {
		statusCode = http.StatusInternalServerError
	}
	return &ProxyAPIError{
		StatusCode: statusCode,
		Message:    compactProxyBody(message),
	}
}
