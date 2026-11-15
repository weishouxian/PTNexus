package fetch

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildSyntheticTorrent 构造一个最小可解析的 v1 种子（单文件、私有），用于验证 info 改写。
func buildSyntheticTorrent(t *testing.T) []byte {
	t.Helper()
	pieces := make([]byte, 20)
	for i := range pieces {
		pieces[i] = byte(i + 1)
	}
	var info bytes.Buffer
	info.WriteString("d6:lengthi1024e4:name8:test.bin12:piece lengthi16384e6:pieces20:")
	info.Write(pieces)
	info.WriteString("7:privatei1ee")

	var buf bytes.Buffer
	buf.WriteByte('d')
	buf.WriteString(fmt.Sprintf("8:announce%d:%s", len("https://example.org/announce"), "https://example.org/announce"))
	buf.WriteString("7:comment4:test")
	buf.WriteString("4:info")
	buf.Write(info.Bytes())
	buf.WriteByte('e')
	return buf.Bytes()
}

// TestRewriteTorrentInfoSourceChangesHash 验证：写入 source 后 infohash 变化，
// 但文件列表 / piece 数据 / private 标记等全部原样保留，且结果仍是合法 bencode。
func TestRewriteTorrentInfoSourceChangesHash(t *testing.T) {
	original := buildSyntheticTorrent(t)

	origHash, err := TorrentInfoHash(original)
	if err != nil {
		t.Fatalf("原始 infohash 计算失败: %v", err)
	}
	origMeta, err := ParseTorrentContentMeta(original)
	if err != nil {
		t.Fatalf("原始种子解析失败: %v", err)
	}

	rewritten, err := RewriteTorrentInfoSource(original, "rousi.pro")
	if err != nil {
		t.Fatalf("写入 source 失败: %v", err)
	}

	newHash, err := TorrentInfoHash(rewritten)
	if err != nil {
		t.Fatalf("改写后 infohash 计算失败: %v", err)
	}
	if newHash == origHash {
		t.Fatalf("infohash 没有变化，仍是 %s", newHash)
	}
	t.Logf("infohash: %s → %s ✓", origHash, newHash)

	newMeta, err := ParseTorrentContentMeta(rewritten)
	if err != nil {
		t.Fatalf("改写后解析失败（字节非法）: %v", err)
	}
	if newMeta.Meta.InfoHash != newHash {
		t.Fatalf("项目解析的 infohash 与手算不一致: %s vs %s", newMeta.Meta.InfoHash, newHash)
	}
	if newMeta.Meta.Name != origMeta.Meta.Name {
		t.Fatalf("文件名被改动: %s → %s", origMeta.Meta.Name, newMeta.Meta.Name)
	}
	if newMeta.Meta.Size != origMeta.Meta.Size {
		t.Fatalf("体积被改动: %d → %d", origMeta.Meta.Size, newMeta.Meta.Size)
	}

	// info 字典应当只是多了一个 source 键，其余字节一模一样。
	start, end, err := torrentInfoSpan(rewritten)
	if err != nil {
		t.Fatalf("定位 info 失败: %v", err)
	}
	wantInfo := "d6:lengthi1024e4:name8:test.bin12:piece lengthi16384e6:pieces20:" +
		string(func() []byte {
			b := make([]byte, 20)
			for i := range b {
				b[i] = byte(i + 1)
			}
			return b
		}()) + "7:privatei1e6:source9:rousi.proe"
	if got := string(rewritten[start:end]); got != wantInfo {
		t.Fatalf("info 字典不符合预期\n期望=%q\n实际=%q", wantInfo, got)
	}

	// 幂等：同样 tag 再写一次，hash 与字节都不变。
	same, err := RewriteTorrentInfoSource(rewritten, "rousi.pro")
	if err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}
	sameHash, _ := TorrentInfoHash(same)
	if sameHash != newHash || !bytes.Equal(same, rewritten) {
		t.Fatalf("同一 tag 重复写入结果不稳定: %s vs %s", sameHash, newHash)
	}

	// 换 tag：source 应被替换而不是追加，hash 再次变化。
	other, err := RewriteTorrentInfoSource(rewritten, "ptnexus")
	if err != nil {
		t.Fatalf("替换 source 失败: %v", err)
	}
	otherHash, _ := TorrentInfoHash(other)
	if otherHash == newHash {
		t.Fatalf("替换 tag 后 infohash 未变化: %s", otherHash)
	}
	if n := strings.Count(string(other), "6:source"); n != 1 {
		t.Fatalf("source 字段出现 %d 次（应为 1 次）", n)
	}

	// 空 tag：原样返回，hash 不变。
	untouched, err := RewriteTorrentInfoSource(original, "   ")
	if err != nil {
		t.Fatalf("空 tag 处理失败: %v", err)
	}
	untouchedHash, _ := TorrentInfoHash(untouched)
	if untouchedHash != origHash || !bytes.Equal(untouched, original) {
		t.Fatalf("空 tag 不应改动种子: %s vs %s", untouchedHash, origHash)
	}

	// 顶层字段也必须在（announce/comment 不能被丢）。
	if !strings.Contains(string(rewritten), "https://example.org/announce") {
		t.Fatal("announce 字段丢失")
	}
	if !strings.Contains(string(rewritten), "7:comment") {
		t.Fatal("comment 字段丢失")
	}
}

// TestRewriteTorrentInfoSourceInvalid 验证非法输入给出错误而不是静默返回坏字节。
func TestRewriteTorrentInfoSourceInvalid(t *testing.T) {
	if _, err := RewriteTorrentInfoSource(nil, "rousi.pro"); err == nil {
		t.Fatal("空内容应报错")
	}
	if _, err := RewriteTorrentInfoSource([]byte("d7:comment4:teste"), "rousi.pro"); err == nil {
		t.Fatal("缺少 info 应报错")
	}
	if _, err := RewriteTorrentInfoSource([]byte("d4:infoi1ee"), "rousi.pro"); err == nil {
		t.Fatal("info 不是字典应报错")
	}
}
