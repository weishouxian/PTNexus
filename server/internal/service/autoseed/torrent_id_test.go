package autoseed

import "testing"

// TTG 的详情页是 /t/{id}/，路径里没有 id= 参数；
// RSS 与种子 comment 给的都是这个地址，取不到 ID 会让后续做种详情查询落到 stableID 上。
func TestInferTorrentIDFromURL(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://totheglory.im/t/841600/", "841600"},
		{"https://totheglory.im/dl/841600/4e2cca4b983ba8039a0440a5eb6261ee", "841600"},
		{"https://pterclub.net/details.php?id=12345", "12345"},
		{"https://www.hxpt.org/torrent/23930", "23930"},
		{"23930", "23930"},
	}

	for _, item := range cases {
		if got := inferTorrentIDFromURL(item.url); got != item.want {
			t.Fatalf("解析 %q 期望 %q，实际 %q", item.url, item.want, got)
		}
	}
}
