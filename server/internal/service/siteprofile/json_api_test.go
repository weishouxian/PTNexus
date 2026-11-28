package siteprofile

import "testing"

// M-Team 的接口域名与网页域不同：网页域 kp.m-team.cc 对 /api/* 直接 403，
// 之前「接口返回 HTTP 403（Cookie/令牌可能已失效）」就是请求打到了网页域。
func TestJSONRequestBaseMTeam(t *testing.T) {
	if got := jsonRequestBase(jsonChannelMTeam, "https://kp.m-team.cc"); got != mteamAPIBase {
		t.Errorf("M-Team 应走接口域名 %q，实际 %q", mteamAPIBase, got)
	}
	if got := jsonRequestBase(jsonChannelHxpt, "https://hxpt.example"); got != "https://hxpt.example" {
		t.Errorf("其它站点应沿用自己的域名，实际 %q", got)
	}
}

// M-Team 接口只回 role（1-9）这个数字，等级名/门槛/保号线按站点官方说明内置。
func TestMTeamLevels(t *testing.T) {
	levels := mteamLevels()
	if len(levels) != 9 {
		t.Fatalf("M-Team 应有 9 级，实际 %d", len(levels))
	}
	want := []string{
		"小卒(User)", "捕頭(Power User)", "知縣(Elite User)", "通判(Crazy User)",
		"知州(Insane User)", "府丞(Veteran User)", "府尹(Extreme User)",
		"總督(Ultimate User)", "大臣(mTorrent Master)",
	}
	for index, name := range want {
		if levels[index].Name != name {
			t.Errorf("第 %d 级应为 %q，实际 %q", index, name, levels[index].Name)
		}
	}
	// 保号只认「府尹(Extreme User)」：府丞的「封存時永久保號」只是封存豁免。
	for _, level := range levels {
		if level.KeepAlive != (level.Name == "府尹(Extreme User)") {
			t.Errorf("保号等级应为府尹(Extreme User)，实际 %q keep=%v", level.Name, level.KeepAlive)
		}
	}
	if levels[1].Requirements == "" || levels[1].Privileges != "同小卒(User)" {
		t.Errorf("捕頭的升级条件/权限未填：%+v", levels[1])
	}
}

func TestMTeamRoleName(t *testing.T) {
	cases := map[string]string{
		"1":  "小卒(User)",
		"7":  "府尹(Extreme User)",
		"9":  "大臣(mTorrent Master)",
		" 9": "大臣(mTorrent Master)",
		"0":  "",
		"":   "",
		"x":  "",
	}
	for role, want := range cases {
		if got := mteamRoleName(role); got != want {
			t.Errorf("role %q 应映射为 %q，实际 %q", role, want, got)
		}
	}
}
