package sites

import (
	"fmt"
	"strings"

	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
)

// isolateInfoHashByTag 在种子 info 字典内写入 source=tag，让目标站种子与源站种子拥有不同的 infohash。
//
// 为什么需要：自研 API 站（肉丝 PeerGo、好学 hxpt）会把上传的 .torrent 原样保存，
// 只替换 announce（带上本站 passkey）。因此「发布到目标站的种子」与「源站种子」是同一个 infohash，
// 下载器里已经有源站任务时，再加一次会因「任务已存在」失败——
// 典型表现就是「把源站的种子删了就能添加上了」。
//
// 只增删 info 字典里的普通键，所有已有键的值（含 pieces、files、length）都用原始字节原样搬运，
// 因此文件列表与分块校验完全不变：下载器指向同一批文件即可直接校验做种，无需重新下载。
//
// 参数/返回：torrentFile 为源站种子字节；tag 为要写入 info 的 source 值（空串表示不改动，原样返回）；
// 返回（可能被改写的）种子字节与一行说明日志。
// 失败场景：种子结构非法时保持源站字节不变，说明里给出原因（不阻断发布）。
// 副作用：无（纯字节变换）。
func isolateInfoHashByTag(torrentFile []byte, tag string) ([]byte, string) {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return torrentFile, "info hash 隔离: 未配置 info_source_tag，保持与源站相同的 infohash（下载器里会与源站共用同一个任务）"
	}

	before, _ := acquirefetch.TorrentInfoHash(torrentFile)
	rewritten, err := acquirefetch.RewriteTorrentInfoSource(torrentFile, tag)
	if err != nil {
		return torrentFile, fmt.Sprintf("info hash 隔离失败（继续用源站种子，infohash 不变）: %v", err)
	}
	after, _ := acquirefetch.TorrentInfoHash(rewritten)
	if before != "" && before == after {
		return rewritten, fmt.Sprintf("info hash 隔离异常: 写入 source=%s 后 infohash 未变化（%s）", tag, after)
	}
	return rewritten, fmt.Sprintf("info hash 隔离: info.source=%s，infohash %s → %s（与源站不同，下载器里是独立任务）", tag, before, after)
}
