package iyuu

import "testing"

// TestMatchedSiteNicknames 校验「IYUU 站点 → 本地昵称」的匹配口径：
// site 字段映射优先，昵称兜底，不匹配的不返回。
func TestMatchedSiteNicknames(t *testing.T) {
	sites := []Site{
		{ID: 3, Nickname: "M-Team", Site: "m-team"},
		{ID: 120, Nickname: "财神", Site: "cspt"},
		{ID: 999, Nickname: "不存在站", Site: "unknown"},
	}
	local := []string{"馒头", "财神", "本地专属"}
	fieldToNickname := map[string]string{
		"m-team": "馒头",
		"cspt":   "财神",
	}

	got := MatchedSiteNicknames(sites, local, fieldToNickname)
	if len(got) != 2 {
		t.Fatalf("期望匹配到 2 个站，实际 %d: %+v", len(got), got)
	}
	for _, want := range []string{"馒头", "财神"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("缺少匹配结果 %s，实际 %+v", want, got)
		}
	}
	if _, ok := got["不存在站"]; ok {
		t.Fatalf("不该匹配到本地不存在的站点")
	}
}

// TestMatchedSiteNicknamesFallbackNickname 校验没有 site 字段映射时按昵称兜底。
func TestMatchedSiteNicknamesFallbackNickname(t *testing.T) {
	sites := []Site{{ID: 14, Nickname: "TTG", Site: "ttg-other"}}
	got := MatchedSiteNicknames(sites, []string{"TTG"}, map[string]string{})
	if _, ok := got["TTG"]; !ok {
		t.Fatalf("昵称兜底匹配失败: %+v", got)
	}
}
