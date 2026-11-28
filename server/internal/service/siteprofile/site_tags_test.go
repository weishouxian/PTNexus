package siteprofile

import (
	"reflect"
	"testing"
)

// siteTagList 要能吃下 sites 列表给的 []string、接口层可能出现的 []any、以及历史 JSON 串，
// 并且去空、大小写不敏感去重——否则前端筛选下拉会出现重复标签。
func TestSiteTagList(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want []string
	}{
		{"字符串数组", []string{"主站", "官组", " "}, []string{"主站", "官组"}},
		{"任意数组", []any{"主站", "", "官组"}, []string{"主站", "官组"}},
		{"JSON 串", `["主站","主站","Main"]`, []string{"主站", "Main"}},
		{"大小写去重", []string{"Main", "main"}, []string{"Main"}},
		{"普通串", "主站", []string{"主站"}},
		{"空串", "  ", nil},
		{"nil", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := siteTagList(tc.raw)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("siteTagList(%v) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
