package fetch

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// bencodeSpan 记录 bencode 字典里一个键值对「值」的字节区间（相对所属字节切片）。
type bencodeSpan struct {
	key   string
	start int
	end   int
}

// parseBencodeDictSpans 解析当前位置的字典，返回每个键值对的值区间，不复制值内容。
// 参数/返回：p 的 idx 需指向 'd'；返回按原始顺序排列的键值对区间。
// 失败场景：bencode 非法或字典未闭合。
// 副作用：推进 p.idx。
func parseBencodeDictSpans(p *bdecodeParser) ([]bencodeSpan, error) {
	if err := p.expect('d'); err != nil {
		return nil, err
	}
	spans := make([]bencodeSpan, 0, 12)
	for p.idx < len(p.data) && p.data[p.idx] != 'e' {
		keyBytes, err := p.parseBytes()
		if err != nil {
			return nil, err
		}
		start := p.idx
		if _, err := p.parseValue(); err != nil {
			return nil, err
		}
		spans = append(spans, bencodeSpan{key: string(keyBytes), start: start, end: p.idx})
	}
	if err := p.expect('e'); err != nil {
		return nil, err
	}
	return spans, nil
}

// torrentInfoSpan 定位顶层 info 字典的字节区间。
// 参数/返回：content 为 .torrent 内容；返回 [start,end) 与 error。
// 失败场景：bencode 非法或缺少 info。
// 副作用：无。
func torrentInfoSpan(content []byte) (int, int, error) {
	if len(content) == 0 {
		return 0, 0, errors.New("torrent 内容为空")
	}
	p := &bdecodeParser{data: content}
	spans, err := parseBencodeDictSpans(p)
	if err != nil {
		return 0, 0, fmt.Errorf("解析 torrent 顶层结构失败: %w", err)
	}
	for _, span := range spans {
		if span.key == "info" {
			return span.start, span.end, nil
		}
	}
	return 0, 0, errors.New("torrent 缺少 info 字段")
}

// TorrentInfoHash 计算 torrent 的 v1 infohash（info 字典原始字节的 SHA1，小写十六进制）。
// 参数/返回：content 为 .torrent 内容；返回 infohash 与 error。
// 失败场景：bencode 非法或缺少 info。
// 副作用：无。
func TorrentInfoHash(content []byte) (string, error) {
	start, end, err := torrentInfoSpan(content)
	if err != nil {
		return "", err
	}
	sum := sha1.Sum(content[start:end])
	return strings.ToLower(hex.EncodeToString(sum[:])), nil
}

// RewriteTorrentInfoSource 在 info 字典内写入（或替换）source 字段，用于让同一份数据的
// 目标站种子与源站种子拥有不同的 infohash（下载器里是两个独立任务，标签/统计互不影响）。
//
// 关键约束：只增删 info 字典里的普通键，所有已有键的值（含 pieces、files、length）都用原始字节
// 原样搬运，因此文件列表、分块校验完全不变——下载器指向同一批文件即可直接校验做种，无需重新下载。
//
// 参数/返回：content 为 .torrent 内容；source 为要写入 info 的 source 值（空串表示不改动，原样返回）。
// 失败场景：bencode 非法、缺少 info、info 不是字典。
// 副作用：无（纯字节变换）。
func RewriteTorrentInfoSource(content []byte, source string) ([]byte, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return content, nil
	}

	p := &bdecodeParser{data: content}
	outer, err := parseBencodeDictSpans(p)
	if err != nil {
		return nil, fmt.Errorf("解析 torrent 顶层结构失败: %w", err)
	}

	infoFound := false
	for _, span := range outer {
		if span.key == "info" {
			infoFound = true
			break
		}
	}
	if !infoFound {
		return nil, errors.New("torrent 缺少 info 字段")
	}

	start, end, err := torrentInfoSpan(content)
	if err != nil {
		return nil, err
	}
	infoBytes := content[start:end]
	if len(infoBytes) == 0 || infoBytes[0] != 'd' {
		return nil, errors.New("torrent info 不是字典，无法写入 source")
	}

	infoSpans, err := parseBencodeDictSpans(&bdecodeParser{data: infoBytes})
	if err != nil {
		return nil, fmt.Errorf("解析 info 字典失败: %w", err)
	}

	// bencode 要求字典键按字节序排列：去掉旧 source 后排序，再按序插入新的 source。
	kept := make([]bencodeSpan, 0, len(infoSpans)+1)
	for _, span := range infoSpans {
		if span.key == "source" {
			continue
		}
		kept = append(kept, span)
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].key < kept[j].key })

	var infoBuf bytes.Buffer
	infoBuf.Grow(len(infoBytes) + len(source) + 16)
	infoBuf.WriteByte('d')
	written := false
	for _, span := range kept {
		if !written && "source" < span.key {
			infoBuf.Write(encodeBencodeString("source"))
			infoBuf.Write(encodeBencodeString(source))
			written = true
		}
		infoBuf.Write(encodeBencodeString(span.key))
		infoBuf.Write(infoBytes[span.start:span.end])
	}
	if !written {
		infoBuf.Write(encodeBencodeString("source"))
		infoBuf.Write(encodeBencodeString(source))
	}
	infoBuf.WriteByte('e')
	newInfo := infoBuf.Bytes()

	// 顶层同样按字节序重组，除 info 外全部用原始字节。
	sortedOuter := append([]bencodeSpan(nil), outer...)
	sort.SliceStable(sortedOuter, func(i, j int) bool { return sortedOuter[i].key < sortedOuter[j].key })

	var buf bytes.Buffer
	buf.Grow(len(content) + len(newInfo) + 16)
	buf.WriteByte('d')
	for _, span := range sortedOuter {
		buf.Write(encodeBencodeString(span.key))
		if span.key == "info" {
			buf.Write(newInfo)
			continue
		}
		buf.Write(content[span.start:span.end])
	}
	buf.WriteByte('e')
	return buf.Bytes(), nil
}
