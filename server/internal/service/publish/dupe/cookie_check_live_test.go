//go:build hxptlive

package dupe

import (
	"os"
	"strings"
	"testing"
)

// TestHXPTLiveCheckCookie 用真实 Cookie 核对好学 Cookie 校验链路。
// 运行：HXPT_COOKIE='<整串 Cookie>' go test -tags hxptlive ./internal/service/publish/dupe/ -run TestHXPTLiveCheckCookie -v
// 期望：valid=true，reason 含「/api/v1/me」，并能拿到 UserID / Username。
func TestHXPTLiveCheckCookie(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("HXPT_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 HXPT_COOKIE，跳过好学生产接口核对")
	}

	valid, reason, user, err := CheckCookie("https://www.hxpt.org", "hxpt", cookie, "")
	if err != nil {
		t.Fatalf("校验请求失败: %v", err)
	}
	if !valid {
		t.Fatalf("期望有效，实际判定失效: %s", reason)
	}
	if user.UserID == "" && user.Username == "" {
		t.Fatalf("判定有效但没拿到用户信息: %+v", user)
	}
	t.Logf("校验通过: reason=%q user=%+v", reason, user)
}
