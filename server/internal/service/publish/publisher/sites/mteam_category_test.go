package sites

import "testing"

// TestResolveMTeamCategory 覆盖馒头分类落点，重点是「影剧/综艺」二维细分的档位边界。
//
// 站点影剧大类下没有 Remux 档：剧集 Remux 必须落 HD(402)，BluRay(438) 只给蓝光原盘。
// 该规则来自站点侧要求（2026-11 修正），此前 Remux 被并入 BluRay，属错值。
func TestResolveMTeamCategory(t *testing.T) {
	cfg := mteamDefaultConfig

	cases := []struct {
		name         string
		category     string
		medium       string
		resolution   string
		hasAnimation bool
		want         string
	}{
		// 影剧/综艺：Remux 各形态一律落 HD
		{"剧集 Remux", "tv", "remux", "r1080p", false, "402"},
		{"剧集 4K Remux", "tv", "uhd_remux", "r2160p", false, "402"},
		{"剧集 BluRay Remux", "tv", "bluray_remux", "r2160p", false, "402"},
		{"剧集 RemuxTV", "tv", "remux_tv", "r1080p", false, "402"},
		{"剧集 UHD RemuxTV", "tv", "uhd_remux_tv", "r2160p", false, "402"},
		// 影剧/综艺：其它档位不变
		{"剧集 蓝光原盘", "tv", "bluray", "r1080p", false, "438"},
		{"剧集 UHD 原盘", "tv", "uhd_bluray", "r2160p", false, "438"},
		{"剧集 WEB-DL", "tv", "webdl", "r1080p", false, "402"},
		{"剧集 DVD", "tv", "dvd", "sd", false, "435"},
		{"剧集 SD", "tv", "webdl", "sd", false, "403"},
		// 电影：Remux 有独立档
		{"电影 Remux", "movie", "remux", "r1080p", false, "439"},
		{"电影 4K Remux", "movie", "uhd_remux", "r2160p", false, "439"},
		{"电影 蓝光原盘", "movie", "bluray", "r1080p", false, "421"},
		// 动画：标签优先，蓝光源落 动画/BluRay
		{"动画 Remux 标签优先", "tv", "remux", "r1080p", true, "453"},
		{"动画 WEB-DL", "tv", "webdl", "r1080p", true, "405"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveMTeamCategory(cfg, tc.category, tc.medium, tc.resolution, tc.hasAnimation)
			if got != tc.want {
				t.Fatalf("resolveMTeamCategory(%q,%q,%q,anim=%v) = %q, want %q",
					tc.category, tc.medium, tc.resolution, tc.hasAnimation, got, tc.want)
			}
		})
	}
}
