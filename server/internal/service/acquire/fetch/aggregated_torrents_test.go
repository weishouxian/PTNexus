package fetch

import "testing"

// 批量获取种子数据时，torrents.details 是多站混合格式：NexusPHP 的 details.php?id=、
// 好学 /torrent/{id}、可乐/肉丝 /torrents/{id}、YemaPT /#/torrent/detail/{id}、
// M-Team /detail/{id}、TTG /t/{id}/，以及部分站点直接存裸 ID。
// 曾因只认 id= 与 details.php，其余形态退化成 hash，导致站点侧报「种子 ID 无效：<hash>」。
func TestExtractTorrentIDFromDetails(t *testing.T) {
	const infoHash = "74f861e41e29e88e4f1a92471143f91b58c8f3c8"

	cases := []struct {
		name     string
		details  string
		fallback string
		want     string
	}{
		{
			name:    "NexusPHP details.php",
			details: "https://hdsky.me/details.php?id=625955",
			want:    "625955",
		},
		{
			name:    "可乐 /torrents/{id}",
			details: "https://www.colapt.com/torrents/33377",
			want:    "33377",
		},
		{
			name:    "肉丝 PeerGo /torrents/{id}",
			details: "https://rousi.pro/torrents/28914",
			want:    "28914",
		},
		{
			name:    "好学 /torrent/{id}",
			details: "https://www.hxpt.org/torrent/123456",
			want:    "123456",
		},
		{
			name:    "YemaPT hash 路由 /#/torrent/detail/{id}",
			details: "https://www.yemapt.org/#/torrent/detail/14888",
			want:    "14888",
		},
		{
			name:    "M-Team /detail/{id}",
			details: "https://kp.m-team.cc/detail/1275770",
			want:    "1275770",
		},
		{
			name:    "TTG /t/{id}/",
			details: "https://totheglory.im/t/841600/",
			want:    "841600",
		},
		{
			name:    "裸数字 ID",
			details: "361482",
			want:    "361482",
		},
		{
			name:    "UUID 详情页",
			details: "https://rousi.pro/torrent/3f2b1c9e-8a44-4d21-9f7b-0c1d2e3f4a5b",
			want:    "3f2b1c9e-8a44-4d21-9f7b-0c1d2e3f4a5b",
		},
		{
			name:    "查询参数 torrent_id",
			details: "https://pt.example.com/torrents.php?torrent_id=9988&hit=1",
			want:    "9988",
		},
		{
			name:     "details 为空时用非 hash 兜底",
			details:  "",
			fallback: "5211314",
			want:     "5211314",
		},
		{
			name:     "details 解析失败时不能把 infohash 当 ID",
			details:  "https://pt.example.com/some/unknown/path",
			fallback: infoHash,
			want:     "",
		},
		{
			name:     "details 为空且只有 infohash 时返回空",
			details:  "   ",
			fallback: infoHash,
			want:     "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractTorrentIDFromDetails(tc.details, tc.fallback); got != tc.want {
				t.Fatalf("extractTorrentIDFromDetails(%q, %q)=%q，期望 %q", tc.details, tc.fallback, got, tc.want)
			}
		})
	}
}

// 批量获取选源站：能解析出 ID 的站点才进候选，全站解析失败时返回 matched=false。
func TestSelectSourceByPrioritySkipsSitesWithoutID(t *testing.T) {
	const infoHash = "74f861e41e29e88e4f1a92471143f91b58c8f3c8"
	rows := []map[string]any{
		{"sites": "HDClone", "details": "https://pt.hdclone.top/details.php?id=52886", "hash": infoHash},
		{"sites": "可乐", "details": "https://www.colapt.com/torrents/33377", "hash": infoHash},
	}

	site, torrentID, matched := selectSourceByPriority(rows, []string{"可乐", "HDClone"})
	if !matched {
		t.Fatal("matched = false，期望命中可乐")
	}
	if site != "可乐" || torrentID != "33377" {
		t.Fatalf("site=%q torrentID=%q，期望 可乐/33377", site, torrentID)
	}

	unknownRows := []map[string]any{
		{"sites": "某站", "details": "https://pt.example.com/some/path", "hash": infoHash},
	}
	if _, _, matched := selectSourceByPriority(unknownRows, []string{"某站"}); matched {
		t.Fatal("matched = true，但该站点 details 解析不出 ID，应视为不可用")
	}
}
