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
			name: "标准 NexusPHP 仅有 userdetails 链接",
			body: `<a href="userdetails.php?id=123">普通用户</a>`,
			wantID:   "123",
			wantName: "普通用户",
		},
		{
			name: "myUserId 属性顺序颠倒（value 在前）",
			body: `<input type="hidden" value="777" id="myUserId">`,
			wantID:   "777",
			wantName: "",
		},
		{
			name: "userdetails 链接但无 id 参数（仅用户名，仍判已登录）",
			body: `<a href="userdetails.php" class="name">只有用户名</a>`,
			wantID:   "",
			wantName: "只有用户名",
		},
		{
			name: "user.php?id=N 变体",
			body: `<a href="user.php?id=888">非标准站用户</a>`,
			wantID:   "888",
			wantName: "非标准站用户",
		},
		{
			name: "无用户信息",
			body: `<html><body>hello</body></html>`,
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
