package dupe

import (
	"fmt"
	"strings"
)

// CheckCookie 校验站点 Cookie 是否有效。
//
// 参数/返回：baseURL 为站点根地址（裸域名或带协议）；cookie 为站点 Cookie 串；
// 返回 (valid, reason, err)：
//   - valid 为 true 表示 Cookie 有效（访问到了正常页面，而非登录页）；
//   - reason 为人类可读的判定说明；
//   - err 非空表示网络/协议层失败（连不上、超时、非 2xx 等），此时无法下结论。
//
// 判定方式：带 Cookie 访问站点首页（index.php，NexusPHP 通用入口），
// 命中登录页特征（name="username" / login.php 等）即视为 Cookie 失效。
// 与 dupe 检索共用同一套「登录页识别」口径，避免各写一份判定规则。
//
// 副作用：向目标站点发起一次 GET 请求。
func CheckCookie(baseURL, cookie string) (valid bool, reason string, err error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return false, "", fmt.Errorf("站点缺少域名（base_url），无法校验 Cookie")
	}
	if strings.TrimSpace(cookie) == "" {
		return false, "", fmt.Errorf("Cookie 为空，无法校验")
	}

	normalized := normalizeSiteBaseURLForCheck(trimmedBase)
	checkURL := normalized + "/index.php"

	body, getErr := httpGetText(Query{BaseURL: normalized, Cookie: cookie}, checkURL)
	if getErr != nil {
		return false, "", getErr
	}

	if looksLikeLoginPage(body, nil) {
		return false, "页面跳转到登录页，Cookie 已失效或未登录", nil
	}
	if looksLikeVerificationError(body) {
		return false, "站点返回人机验证/访问限制页，暂时无法确认 Cookie 有效性", nil
	}
	return true, "Cookie 有效（成功访问到站点页面）", nil
}

// normalizeSiteBaseURLForCheck 补全协议并去掉尾部斜杠（与 dupe 检索口径一致）。
func normalizeSiteBaseURLForCheck(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return "https://" + trimmed
	}
	return trimmed
}
