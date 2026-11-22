package dupe

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractCookieUser(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantID   string
		wantName string
	}{
		{
			name: "OurBits 页头（myUserId 隐藏字段 + userdetails 链接）",
			body: `<input type="hidden" name="userId" id="myUserId" value="61948">` +
				`<a href="userdetails.php?id=61948" class='VeteranUser_Name'><b>fenglaile</b></a>`,
			wantID:   "61948",
			wantName: "fenglaile",
		},
		{
			name:     "标准 NexusPHP 仅有 userdetails 链接",
			body:     `<a href="userdetails.php?id=123">普通用户</a>`,
			wantID:   "123",
			wantName: "普通用户",
		},
		{
			name:     "myUserId 属性顺序颠倒（value 在前）",
			body:     `<input type="hidden" value="777" id="myUserId">`,
			wantID:   "777",
			wantName: "",
		},
		{
			name:     "userdetails 链接但无 id 参数（仅用户名，仍判已登录）",
			body:     `<a href="userdetails.php" class="name">只有用户名</a>`,
			wantID:   "",
			wantName: "只有用户名",
		},
		{
			name:     "user.php?id=N 变体",
			body:     `<a href="user.php?id=888">非标准站用户</a>`,
			wantID:   "888",
			wantName: "非标准站用户",
		},
		{
			name:     "无用户信息",
			body:     `<html><body>hello</body></html>`,
			wantID:   "",
			wantName: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractCookieUser(tc.body)
			if got.UserID != tc.wantID {
				t.Errorf("UserID = %q, want %q", got.UserID, tc.wantID)
			}
			if got.Username != tc.wantName {
				t.Errorf("Username = %q, want %q", got.Username, tc.wantName)
			}
		})
	}
}

// TestCheckMTeamToken 覆盖 M-Team 令牌校验：令牌填在 Passkey 栏时也能取到（与发种同款口径），
// 有效时返回用户 ID 与昵称。
func TestCheckMTeamToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/member/profile" {
			t.Errorf("path = %q, want /api/member/profile", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"0","result":{"id":"32001","nickname":"fenglaile"}}`))
	}))
	defer server.Close()

	t.Setenv("MTEAM_API_BASE", server.URL)

	// 令牌填在 Passkey 栏（cookie 栏为空）——正是用户「Passkey 填 UUID」的场景。
	valid, reason, user, err := CheckCookie("kp.m-team.cc", "m-team", "", "12345678-1234-1234-1234-123456789abc")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !valid {
		t.Errorf("valid = false, want true (reason=%q)", reason)
	}
	if user.UserID != "32001" {
		t.Errorf("UserID = %q, want 32001", user.UserID)
	}
	if user.Username != "fenglaile" {
		t.Errorf("Username = %q, want fenglaile", user.Username)
	}
	if !strings.Contains(reason, "令牌有效") {
		t.Errorf("reason = %q, want contains 令牌有效", reason)
	}
}

// TestCheckMTeamTokenInCookie 覆盖令牌填在 Cookie 栏的场景（向后兼容）。
func TestCheckMTeamTokenInCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":"0","result":{"id":"9","nickname":"MTeam"}}`))
	}))
	defer server.Close()

	t.Setenv("MTEAM_API_BASE", server.URL)

	valid, _, user, err := CheckCookie("kp.m-team.cc", "m-team", "12345678-1234-1234-1234-123456789abc", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !valid || user.UserID != "9" || user.Username != "MTeam" {
		t.Errorf("got valid=%v user=%+v, want valid=true id=9 name=MTeam", valid, user)
	}
}

// TestCheckMTeamTokenInvalid 覆盖令牌形态不合法（如填了站点 Passkey / Cookie）的场景。
func TestCheckMTeamTokenInvalid(t *testing.T) {
	// 32 位十六进制会被 ExtractToken 判为站点 Passkey 而拒绝，不应发出网络请求。
	valid, _, _, err := CheckCookie("kp.m-team.cc", "m-team", "", "abcdef0123456789abcdef0123456789")
	if err == nil {
		t.Fatalf("expected err for site-passkey-like token, got valid=%v", valid)
	}
	if valid {
		t.Errorf("valid = true, want false")
	}
	if !strings.Contains(err.Error(), "存取令牌") {
		t.Errorf("err = %q, want contains 存取令牌", err)
	}
}

// hxptTestServer 起一个模拟好学 /api/v1/me 的测试服务。
// 参数：status 为 HTTP 状态码，body 为响应正文。
func hxptTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			t.Errorf("path = %q, want /api/v1/me", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestCheckHxptCookieValid 覆盖好学正常登录态：HTTP 200 + code=0 + 用户信息。
// 关键点是不能再走 /index.php（已 301 到 /login 的 SPA 壳），必须命中 /api/v1/me。
func TestCheckHxptCookieValid(t *testing.T) {
	server := hxptTestServer(t, http.StatusOK,
		`{"code":0,"message":"ok","data":{"id":10959,"username":"fenglaile","class_name":"Extreme User"}}`)
	defer server.Close()

	valid, reason, user, err := CheckCookie(server.URL, "hxpt", "c_secure_pass=x; flux_token=y", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !valid {
		t.Fatalf("valid = false, want true (reason=%q)", reason)
	}
	if user.UserID != "10959" || user.Username != "fenglaile" {
		t.Errorf("user = %+v, want id=10959 name=fenglaile", user)
	}
}

// TestCheckHxptCookieUnauthorized 覆盖 flux_token 过期/无效：HTTP 401 + code=2001。
func TestCheckHxptCookieUnauthorized(t *testing.T) {
	server := hxptTestServer(t, http.StatusUnauthorized,
		`{"code":2001,"data":null,"message":"未认证"}`)
	defer server.Close()

	valid, reason, _, err := CheckCookie(server.URL, "hxpt", "c_secure_pass=x; flux_token=y", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if valid {
		t.Fatalf("valid = true, want false")
	}
	if !strings.Contains(reason, "flux_token") {
		t.Errorf("reason = %q, want contains flux_token（提示重新登录）", reason)
	}
}

// TestCheckHxptCookieMissingFluxToken 覆盖只粘了旧 NexusPHP 字段（缺 flux_token）的 Cookie：
// 文案要直接点明缺少 flux_token，而不是笼统说 Cookie 失效。
func TestCheckHxptCookieMissingFluxToken(t *testing.T) {
	server := hxptTestServer(t, http.StatusUnauthorized,
		`{"code":2001,"data":null,"message":"未认证"}`)
	defer server.Close()

	_, reason, _, err := CheckCookie(server.URL, "hxpt", "c_secure_pass=only-legacy", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(reason, "缺少 flux_token") {
		t.Errorf("reason = %q, want contains 缺少 flux_token", reason)
	}
}

// TestCheckHxptCookieNoUserInfo 覆盖接口通但没解析到用户信息（异常响应结构）：按失效处理。
func TestCheckHxptCookieNoUserInfo(t *testing.T) {
	server := hxptTestServer(t, http.StatusOK, `{"code":0,"message":"ok","data":{}}`)
	defer server.Close()

	valid, reason, _, err := CheckCookie(server.URL, "hxpt", "flux_token=y", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if valid {
		t.Fatalf("valid = true, want false (reason=%q)", reason)
	}
}

// TestCheckHxptCookieEmpty 覆盖空 Cookie：应直接报错，且不发起请求。
func TestCheckHxptCookieEmpty(t *testing.T) {
	_, _, _, err := CheckCookie("www.hxpt.org", "hxpt", "   ", "")
	if err == nil {
		t.Fatal("expected err for empty cookie")
	}
	if !strings.Contains(err.Error(), "Cookie 为空") {
		t.Errorf("err = %q, want contains Cookie 为空", err)
	}
}

// colaptTestServer 模拟可乐站的 GET /api/v1/me。
// wantCookie 非空时校验请求头带上了该片段（用于确认 Cookie 原样透传）。
func colaptTestServer(t *testing.T, status int, body string, wantCookie string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			t.Errorf("path = %q, want /api/v1/me", r.URL.Path)
		}
		if got := r.Header.Get("Cookie"); wantCookie != "" && !strings.Contains(got, wantCookie) {
			t.Errorf("Cookie 头 = %q, want 含 %q", got, wantCookie)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

// TestCheckColaptCookieValid 覆盖可乐站正常会话：200 + id/username。
func TestCheckColaptCookieValid(t *testing.T) {
	server := colaptTestServer(t, http.StatusOK,
		`{"id":101,"username":"fenglaile","status":"active","session":{"csrf_token":"t"}}`, "reelcore_session=")
	defer server.Close()

	valid, reason, user, err := CheckCookie(server.URL, "colapt", "reelcore_session=abc; reelcore_session_marker=1", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !valid {
		t.Fatalf("valid = false, want true (reason=%q)", reason)
	}
	if user.UserID != "101" || user.Username != "fenglaile" {
		t.Errorf("user = %+v, want id=101 name=fenglaile", user)
	}
}

// TestCheckColaptCookieUnauthorized 覆盖会话过期：401 应提示缺少/更新 reelcore_session。
func TestCheckColaptCookieUnauthorized(t *testing.T) {
	server := colaptTestServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthenticated","message":"未认证"}}`, "")
	defer server.Close()

	valid, reason, _, err := CheckCookie(server.URL, "colapt", "reelcore_session=expired", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if valid {
		t.Fatal("valid = true, want false")
	}
	if !strings.Contains(reason, "reelcore_session") {
		t.Errorf("reason = %q, want contains reelcore_session", reason)
	}
}

// TestCheckColaptCookieMissingSession 覆盖粘了旧 NexusPHP 字段的 Cookie：文案要点明缺 reelcore_session。
func TestCheckColaptCookieMissingSession(t *testing.T) {
	server := colaptTestServer(t, http.StatusUnauthorized, `{"error":{"code":"unauthenticated"}}`, "")
	defer server.Close()

	_, reason, _, err := CheckCookie(server.URL, "colapt", "c_secure_uid=1; c_secure_pass=2", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !strings.Contains(reason, "缺少 reelcore_session") {
		t.Errorf("reason = %q, want contains 缺少 reelcore_session", reason)
	}
}

// TestCheckColaptCookieEmpty 覆盖空 Cookie：直接报错，不发起请求。
func TestCheckColaptCookieEmpty(t *testing.T) {
	_, _, _, err := CheckCookie("www.colapt.com", "colapt", "  ", "")
	if err == nil {
		t.Fatal("expected err for empty cookie")
	}
	if !strings.Contains(err.Error(), "Cookie 为空") {
		t.Errorf("err = %q, want contains Cookie 为空", err)
	}
}
