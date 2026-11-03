package dupe

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/service/mteamapi"
)

// CookieCheckCloudflareReason 是 CheckCookie 在「站点返回 Cloudflare 人机校验页」时返回的 reason 文本。
// 调用方（如 sitehealth 定时检测）据此识别「凭据可能有效、只是被 CF 拦下无法在线校验」这一不确定状态，
// 从而不把它误判为「Cookie 失效」而刷告警。
const CookieCheckCloudflareReason = "该站点有 Cloudflare 人机校验，服务器端无法直接通过；Cookie 本身可能有效，请在浏览器中登录确认"

// CheckCookie 校验站点 Cookie（或 M-Team API 令牌）是否有效。
//
// 参数/返回：baseURL 为站点根地址（裸域名或带协议）；siteCode 为站点代码（用于识别
// 非 NexusPHP 站点，可为空）；credential 为站点 Cookie 串；passkey 为站点 Passkey 栏
// 内容（M-Team 会把存取令牌填在这栏，需一并参与取令牌）；
// 返回 (valid, reason, user, err)：
//   - valid 为 true 表示凭据有效（访问到了正常页面 / 令牌调通了接口）；
//   - reason 为人类可读的判定说明；
//   - user 为解析出的当前登录用户信息（UserID/Username，可能为空）；
//   - err 非空表示网络/协议层失败（连不上、超时、非 2xx 等），此时无法下结论。
//
// 判定方式分两类：
//   - M-Team：站点用 x-api-key 令牌鉴权，官方**禁止 Cookie 访问接口**（违者可能被封号），
//     故改走 /member/profile 令牌通道，既合规又能直接拿到用户 ID 与昵称；
//     令牌按发种同款口径（Passkey 栏 → Cookie 栏）解析，避免两栏口径不一致。
//   - 其他站点：带 Cookie 访问站点首页（index.php，NexusPHP 通用入口），
//     以「能否从页面解析出当前登录用户信息（用户 ID 或用户名）」作为唯一有效性判据：
//     解析到用户信息即视为 Cookie 有效；否则视为失效（登录页/未登录/验证页等一律无效）。
//     不再依赖登录页/人机验证等启发式特征，避免因页面结构差异导致误判。
//
// 副作用：向目标站点发起一次 GET 请求。
func CheckCookie(baseURL, siteCode, credential, passkey string) (valid bool, reason string, user CookieUser, err error) {
	// M-Team 走 x-api-key 令牌通道，不再用 Cookie 访问网页（规避封号风险）。
	if mteamapi.IsSite(siteCode, baseURL, "") {
		return checkMTeamToken(baseURL, passkey, credential)
	}

	// YemaPT 是自研 API 站点（UmiJS SPA + 自研 REST API），没有 NexusPHP 的 /index.php，
	// 走 /api/user/profile 拿当前用户信息（success=true 且 data.id/name 非空即有效）。
	if isYemaPTSite(siteCode, baseURL) {
		return checkYemaPTCookie(baseURL, credential)
	}

	// 朱雀（TNode/API 站点）是 SPA + 自研 API，/index.php 只返回 SPA 壳、无 NexusPHP 登录态特征，
	// 走 /api/user/getSecurityInfo 拿当前用户信息（需 x-csrf-token，从 /torrent/upload 页面取）。
	if isZhuqueSite(siteCode, baseURL) {
		return checkZhuqueCookie(baseURL, credential)
	}

	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return false, "", CookieUser{}, fmt.Errorf("站点缺少域名（base_url），无法校验 Cookie")
	}
	if strings.TrimSpace(credential) == "" {
		return false, "", CookieUser{}, fmt.Errorf("Cookie 为空，无法校验")
	}

	normalized := normalizeSiteBaseURLForCheck(trimmedBase)
	checkURL := normalized + "/index.php"

	body, statusCode, getErr := fetchCookieCheckPage(Query{BaseURL: normalized, Cookie: credential}, checkURL)
	if getErr != nil {
		return false, "", CookieUser{}, getErr
	}
	// 站点前置 Cloudflare（如织梦 zmpt.cc、UBits）时，服务端裸 HTTP 客户端过不了人机校验，
	// 会拿到 403 + 挑战页；此时 Cookie 本身可能有效，不能误判为「Cookie 失效」。
	if statusCode >= 400 {
		if looksLikeCloudflareChallenge(body) {
			return false, CookieCheckCloudflareReason, CookieUser{}, nil
		}
		return false, fmt.Sprintf("Cookie 校验响应状态异常: %d（请检查 Cookie 是否已失效）", statusCode), CookieUser{}, nil
	}

	// 唯一判据：能否解析出当前登录用户信息（用户 ID 或用户名）。
	user = extractCookieUser(body)
	if user.UserID == "" && user.Username == "" {
		return false, "Cookie 已失效或未登录（未能获取到用户信息）", CookieUser{}, nil
	}
	return true, "Cookie 有效（已获取到用户信息）", user, nil
}

// fetchCookieCheckPage 发起一次 Cookie 校验请求，返回响应正文与状态码。
// 与 dupe 检索的 httpGetText 分离：Cookie 校验需要拿到状态码与正文以区分
// 「站点拒绝（403/CF 挑战）」与「网络失败」，且报错文案不应复用 dupe 措辞。
// 副作用：向目标站点发起一次 GET 请求。
func fetchCookieCheckPage(query Query, checkURL string) (body string, statusCode int, err error) {
	client := newDupeHTTPClient()
	req, err := http.NewRequest(http.MethodGet, checkURL, nil)
	if err != nil {
		return "", 0, fmt.Errorf("创建 Cookie 校验请求失败: %w", err)
	}
	req.Header.Set("User-Agent", firstNonEmptyValue(query.UserAgent, defaultDupeUserAgent))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,zh-TW;q=0.8,zh-HK;q=0.7,en-US;q=0.6,en;q=0.5")
	if cookie := strings.TrimSpace(query.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("Cookie 校验请求失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, sharedBodyLimit))
	if err != nil {
		return "", 0, fmt.Errorf("读取 Cookie 校验响应失败: %w", err)
	}
	return string(data), resp.StatusCode, nil
}

// looksLikeCloudflareChallenge 判断响应正文是否为 Cloudflare 人机校验/拦截页。
// 与 uploader 包同口径（识别 challenges.cloudflare.com / Just a moment 等特征），
// 因 uploader 的实现未导出，这里在 dupe 包内保留一份等价实现供 Cookie 校验使用。
func looksLikeCloudflareChallenge(text string) bool {
	sample := text
	if len(sample) > 8192 {
		sample = sample[:8192]
	}
	lower := strings.ToLower(sample)
	if strings.TrimSpace(lower) == "" {
		return false
	}
	for _, marker := range []string{
		"challenges.cloudflare.com",
		"/cdn-cgi/challenge-platform",
		"cf_chl_opt",
		"cf-chl-",
		"just a moment",
		"checking your browser",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// checkMTeamToken 校验 M-Team 存取令牌：调 /member/profile 拿到用户信息。
// 令牌按发种同款口径（Passkey 栏 → Cookie 栏）解析；返回的 user.UserID/Username
// 分别对应用户 ID 与昵称（/账号名）。
func checkMTeamToken(baseURL, passkey, cookie string) (bool, string, CookieUser, error) {
	if strings.TrimSpace(passkey) == "" && strings.TrimSpace(cookie) == "" {
		return false, "", CookieUser{}, fmt.Errorf("M-Team 令牌为空，无法校验")
	}

	// 与发种适配器 resolveMTeamToken 同款优先级：Passkey 栏 → Cookie 栏。
	cred := mteamapi.ResolveToken([]mteamapi.Credential{
		{Source: "Passkey 栏", Value: passkey},
		{Source: "Cookie 栏", Value: cookie},
	})
	if cred.Value == "" {
		return false, "", CookieUser{}, fmt.Errorf("未识别到 M-Team API 存取令牌（需在控制台→实验室→存取令牌生成，非站点 Passkey/Cookie）")
	}

	profile, err := mteamapi.Profile(baseURL, cred.Value, 0)
	if err != nil {
		return false, "", CookieUser{}, err
	}

	user := CookieUser{UserID: profile.ID, Username: profile.DisplayName()}
	return true, "M-Team 令牌有效（已通过 /member/profile 校验）", user, nil
}

// yemaPTProfileResponse 为 YemaPT /api/user/profile 的响应结构（只声明校验所需字段）。
type yemaPTProfileResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"data"`
}

// isYemaPTSite 判断站点是否为 YemaPT（自研 API 站点）。
// 与 torrent_fetch.go 的 isYemaPTSource 同口径：siteCode/baseURL 含 "yemapt" 即命中。
func isYemaPTSite(siteCode, baseURL string) bool {
	for _, raw := range []string{siteCode, baseURL} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(raw)), "yemapt") {
			return true
		}
	}
	return false
}

// checkYemaPTCookie 校验 YemaPT Cookie：调 /api/user/profile 拿当前用户信息。
// 判据：success=true 且 data.id / data.name 至少一个非空即视为有效；
// success=false（未登录/无 cookie）或拿不到用户信息则视为失效。
func checkYemaPTCookie(baseURL, cookie string) (bool, string, CookieUser, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return false, "", CookieUser{}, fmt.Errorf("YemaPT 缺少域名（base_url），无法校验 Cookie")
	}
	if strings.TrimSpace(cookie) == "" {
		return false, "", CookieUser{}, fmt.Errorf("YemaPT Cookie 为空，无法校验")
	}
	normalized := normalizeSiteBaseURLForCheck(trimmedBase)
	checkURL := normalized + "/api/user/profile"

	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, checkURL, nil)
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("创建 YemaPT Cookie 校验请求失败: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:128.0) Gecko/20100101 Firefox/128.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("YemaPT Cookie 校验请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, sharedBodyLimit))
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("读取 YemaPT Cookie 校验响应失败: %w", err)
	}

	var parsed yemaPTProfileResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, "", CookieUser{}, fmt.Errorf("YemaPT 响应解析失败: %w", err)
	}
	if !parsed.Success {
		return false, "YemaPT Cookie 已失效或未登录（接口返回 success=false）", CookieUser{}, nil
	}
	if parsed.Data.ID == 0 && strings.TrimSpace(parsed.Data.Name) == "" {
		return false, "YemaPT Cookie 可用但未能获取到用户信息", CookieUser{}, nil
	}

	user := CookieUser{
		UserID:   strconv.Itoa(parsed.Data.ID),
		Username: strings.TrimSpace(parsed.Data.Name),
	}
	return true, "YemaPT Cookie 有效（已通过 /api/user/profile 校验）", user, nil
}

// zhuqueSecurityInfoResponse 为朱雀 /api/user/getSecurityInfo 的响应结构（只声明校验所需字段）。
type zhuqueSecurityInfoResponse struct {
	Status int `json:"status"`
	Data   struct {
		ID       int    `json:"id"`
		Username string `json:"username"`
	} `json:"data"`
}

// isZhuqueSite 判断站点是否为朱雀（TNode/API 站点）。
// 与 publisher 引擎同口径：siteCode/baseURL 含 "zhuque" 即命中。
func isZhuqueSite(siteCode, baseURL string) bool {
	for _, raw := range []string{siteCode, baseURL} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(raw)), "zhuque") {
			return true
		}
	}
	return false
}

// checkZhuqueCookie 校验朱雀 Cookie：先访问 /torrent/upload 页面取 x-csrf-token，
// 再带 token 调 /api/user/getSecurityInfo 拿当前用户信息。
// 判据：status=200 且 data.id / data.username 至少一个非空即视为有效；
// 401（NEED_AUTH）或拿不到用户信息则视为失效。
func checkZhuqueCookie(baseURL, cookie string) (bool, string, CookieUser, error) {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return false, "", CookieUser{}, fmt.Errorf("朱雀缺少域名（base_url），无法校验 Cookie")
	}
	if strings.TrimSpace(cookie) == "" {
		return false, "", CookieUser{}, fmt.Errorf("朱雀 Cookie 为空，无法校验")
	}
	normalized := normalizeSiteBaseURLForCheck(trimmedBase)

	client := &http.Client{Timeout: 20 * time.Second}
	csrfToken, csrfErr := fetchZhuqueCSRFTokenForCheck(client, normalized, cookie)
	if csrfErr != nil {
		// 拿不到 CSRF token 可能是未登录被重定向到登录页；按失效处理而非网络错误。
		return false, "朱雀 Cookie 已失效或未登录（获取 CSRF Token 失败）", CookieUser{}, nil
	}

	checkURL := normalized + "/api/user/getSecurityInfo"
	req, err := http.NewRequest(http.MethodGet, checkURL, nil)
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("创建朱雀 Cookie 校验请求失败: %w", err)
	}
	req.Header.Set("User-Agent", defaultDupeUserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", normalized+"/user/rss")
	req.Header.Set("Origin", normalized)
	req.Header.Set("x-csrf-token", csrfToken)
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("朱雀 Cookie 校验请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, sharedBodyLimit))
	if err != nil {
		return false, "", CookieUser{}, fmt.Errorf("读取朱雀 Cookie 校验响应失败: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized {
		return false, "朱雀 Cookie 已失效或未登录（接口返回 401 NEED_AUTH）", CookieUser{}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("朱雀 Cookie 校验响应状态异常: %d", resp.StatusCode), CookieUser{}, nil
	}

	var parsed zhuqueSecurityInfoResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return false, "", CookieUser{}, fmt.Errorf("朱雀响应解析失败: %w", err)
	}
	if parsed.Status != 200 {
		return false, "朱雀 Cookie 已失效或未登录（接口返回 status != 200）", CookieUser{}, nil
	}
	if parsed.Data.ID == 0 && strings.TrimSpace(parsed.Data.Username) == "" {
		return false, "朱雀 Cookie 可用但未能获取到用户信息", CookieUser{}, nil
	}

	user := CookieUser{
		UserID:   strconv.Itoa(parsed.Data.ID),
		Username: strings.TrimSpace(parsed.Data.Username),
	}
	return true, "朱雀 Cookie 有效（已通过 /api/user/getSecurityInfo 校验）", user, nil
}

// fetchZhuqueCSRFTokenForCheck 访问 /torrent/upload 页面，从 meta 标签提取 x-csrf-token，
// 供 Cookie 校验时携带（与发种适配器 fetchZhuqueCSRFToken 同口径，但独立实现避免跨包依赖）。
func fetchZhuqueCSRFTokenForCheck(client *http.Client, baseURL, cookie string) (string, error) {
	pageURL := strings.TrimRight(baseURL, "/") + "/torrent/upload"
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", defaultDupeUserAgent)
	req.Header.Set("Cookie", cookie)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, sharedBodyLimit))
	if err != nil {
		return "", err
	}
	text := string(body)

	if m := reZhuqueCSRFTokenMeta.FindStringSubmatch(text); len(m) >= 2 {
		if token := strings.TrimSpace(m[1]); token != "" {
			return token, nil
		}
	}
	return "", fmt.Errorf("朱雀上传页未找到 x-csrf-token")
}

// 匹配朱雀（TNode）页面 meta 标签里的 x-csrf-token（name 在前或 content 在前两种形态）。
var reZhuqueCSRFTokenMeta = regexp.MustCompile(`(?is)<meta[^>]*\bname\s*=\s*["']?x-csrf-token["']?[^>]*\bcontent\s*=\s*["']([^"']+)["']`)

// CookieUser 从站点页面解析出的当前登录用户信息。
type CookieUser struct {
	UserID   string `json:"user_id,omitempty"`
	Username string `json:"username,omitempty"`
}

// 匹配 NexusPHP 页头隐藏字段 <input type="hidden" name="userId" id="myUserId" value="61948">。
var reCookieMyUserId = regexp.MustCompile(`(?is)<input[^>]*\b(?:id|name)\s*=\s*["']?myUserId["']?[^>]*\bvalue\s*=\s*["'](\d+)["']`)

// 匹配 userdetails.php 链接，允许 value 属性顺序与 id/name 颠倒（部分站点先写 value 再写 id）。
var reCookieMyUserIdAlt = regexp.MustCompile(`(?is)<input[^>]*\bvalue\s*=\s*["'](\d+)["'][^>]*\b(?:id|name)\s*=\s*["']?myUserId["']?`)

// 匹配 NexusPHP 页头用户链接 <a href="userdetails.php?id=61948" ...>用户名</a>。
// 与 checker/nexusphp_edit_permission.go 同口径：href 含 userdetails.php 即视为用户链接，
// 不强制 ?id= 存在；id 参数存在时提取为 UserID，链接文本为 Username。
var reCookieUserLink = regexp.MustCompile(`(?is)<a[^>]*\bhref\s*=\s*["'][^"']*userdetails\.php(?:[^"']*\bid=(\d+))?[^"']*["'][^>]*>(.*?)</a>`)

// 匹配其它常见用户主页链接（部分非标准 NexusPHP 站用 user.php / users.php 等路由）。
var reCookieAltUserLink = regexp.MustCompile(`(?is)<a[^>]*\bhref\s*=\s*["'][^"']*(?:user|users)\.php\?id=(\d+)[^"']*["'][^>]*>(.*?)</a>`)

// extractCookieUser 从站点首页 HTML 中提取当前登录用户的 ID 与用户名。
// 优先取隐藏字段 myUserId 的 value（最权威）；其次解析 userdetails.php 链接（id 参数为用户 ID、链接文本为用户名）；
// 再兜底 user.php/users.php?id=N 链接。只要解析出 UserID 或 Username 任一即可判定已登录。
func extractCookieUser(body string) CookieUser {
	var user CookieUser

	// 1) 隐藏字段 myUserId（NexusPHP 的 OurBits 变体）：value 即当前用户 ID。
	if m := reCookieMyUserId.FindStringSubmatch(body); m != nil {
		user.UserID = strings.TrimSpace(m[1])
	} else if m := reCookieMyUserIdAlt.FindStringSubmatch(body); m != nil {
		user.UserID = strings.TrimSpace(m[1])
	}

	// 2) 页头用户链接 userdetails.php（标准 NexusPHP）：id 参数为用户 ID，链接文本为用户名。
	if m := reCookieUserLink.FindStringSubmatch(body); m != nil {
		if user.UserID == "" {
			user.UserID = strings.TrimSpace(m[1])
		}
		if name := stripHTMLTags(m[2]); name != "" {
			user.Username = name
		}
	}

	// 3) 兜底：user.php/users.php?id=N 链接。
	if m := reCookieAltUserLink.FindStringSubmatch(body); m != nil {
		if user.UserID == "" {
			user.UserID = strings.TrimSpace(m[1])
		}
		if user.Username == "" {
			if name := stripHTMLTags(m[2]); name != "" {
				user.Username = name
			}
		}
	}

	return user
}

// stripHTMLTags 去掉标签与常见空白实体，返回纯文本用户名。
func stripHTMLTags(s string) string {
	tag := regexp.MustCompile(`(?is)<[^>]*>`)
	out := tag.ReplaceAllString(s, " ")
	out = strings.ReplaceAll(out, "&nbsp;", " ")
	out = strings.ReplaceAll(out, "&amp;", "&")
	return strings.Join(strings.Fields(out), " ")
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
