package mteamapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProfile 覆盖 /member/profile 的三种响应包裹形态：result、data、以及昵称字段回退。
func TestProfile(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantID     string
		wantName   string
		wantErrSub string
	}{
		{
			name:     "result 包裹（Apifox 文档形态）",
			body:     `{"msg":"success","result":{"id":"32001","nickname":"fenglaile","account":"feng"}}`,
			wantID:   "32001",
			wantName: "fenglaile",
		},
		{
			name:     "data 包裹（MT-AutoCheckIn 形态）",
			body:     `{"code":"0","data":{"id":"32001","username":"feng"}}`,
			wantID:   "32001",
			wantName: "feng",
		},
		{
			name:     "昵称缺失时回退 account",
			body:     `{"code":0,"data":{"id":"9","account":"MTeam"}}`,
			wantID:   "9",
			wantName: "MTeam",
		},
		{
			name:       "令牌无效（code 非 0）",
			body:       `{"code":"1","message":"key 无效"}`,
			wantErrSub: "key 无效",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/member/profile" {
					t.Errorf("path = %q, want /api/member/profile", r.URL.Path)
				}
				if r.Method != http.MethodPost {
					t.Errorf("method = %q, want POST（M-Team API 只支持 POST）", r.Method)
				}
				if got := r.Header.Get("x-api-key"); got != "test-token" {
					t.Errorf("x-api-key = %q, want test-token", got)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			// server.URL 形如 http://127.0.0.1:port，APIBaseFor 找不到 api. 前缀会退回 DefaultAPIBase。
			// 因此直接用环境变量覆盖，把 api 根指到测试服务。
			t.Setenv("MTEAM_API_BASE", server.URL)

			profile, err := Profile("https://kp.m-team.cc", "test-token", 0)
			if tc.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Fatalf("err = %v, want contains %q", err, tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if profile.ID != tc.wantID {
				t.Errorf("ID = %q, want %q", profile.ID, tc.wantID)
			}
			if profile.DisplayName() != tc.wantName {
				t.Errorf("DisplayName = %q, want %q", profile.DisplayName(), tc.wantName)
			}
		})
	}
}
