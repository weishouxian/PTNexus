package publisher

import "testing"

// TestIsOfferPublishMode 验证「发种方式」开关只认候选值，且对各类驱动返回的类型都成立。
// 开关真源是站点设置页写入的 sites.publish_mode，经 GetSiteByName 的整行扫描后会以
// int64 / []byte / string 等不同形态进入 TargetInfo，这里必须都能正确识别；
// 其余一切取值（缺失、nil、脏值、bool false）都必须落回正式发种。
func TestIsOfferPublishMode(t *testing.T) {
	cases := []struct {
		name       string
		targetInfo map[string]any
		want       bool
	}{
		{name: "缺失 targetInfo", targetInfo: nil, want: false},
		{name: "空 map", targetInfo: map[string]any{}, want: false},
		{name: "字段缺失", targetInfo: map[string]any{"site": "ttg"}, want: false},
		{name: "nil 值", targetInfo: map[string]any{"publish_mode": nil}, want: false},
		{name: "int 0 正式发种", targetInfo: map[string]any{"publish_mode": 0}, want: false},
		{name: "int 1 候选", targetInfo: map[string]any{"publish_mode": 1}, want: true},
		{name: "int64 1 候选（MySQL 扫描）", targetInfo: map[string]any{"publish_mode": int64(1)}, want: true},
		{name: "int64 0 正式发种", targetInfo: map[string]any{"publish_mode": int64(0)}, want: false},
		{name: "float64 1 候选（JSON 反序列化）", targetInfo: map[string]any{"publish_mode": float64(1)}, want: true},
		{name: "bool true 候选", targetInfo: map[string]any{"publish_mode": true}, want: true},
		{name: "bool false 正式发种", targetInfo: map[string]any{"publish_mode": false}, want: false},
		{name: "[]byte 1 候选", targetInfo: map[string]any{"publish_mode": []byte("1")}, want: true},
		{name: "[]byte 0 正式发种", targetInfo: map[string]any{"publish_mode": []byte("0")}, want: false},
		{name: "字符串 offer 候选", targetInfo: map[string]any{"publish_mode": "offer"}, want: true},
		{name: "字符串 1 候选", targetInfo: map[string]any{"publish_mode": "1"}, want: true},
		{name: "字符串 upload 正式发种", targetInfo: map[string]any{"publish_mode": "upload"}, want: false},
		{name: "非法值回落正式发种", targetInfo: map[string]any{"publish_mode": "yes"}, want: false},
		{name: "未知类型回落正式发种", targetInfo: map[string]any{"publish_mode": struct{}{}}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsOfferPublishMode(tc.targetInfo); got != tc.want {
				t.Fatalf("IsOfferPublishMode(%v) = %v, want %v", tc.targetInfo["publish_mode"], got, tc.want)
			}
		})
	}
}
