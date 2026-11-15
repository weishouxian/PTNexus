package sites

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// TestDownloadRousiImageFallsBackToRange 校验整段下载被对端提前断流时，能靠 Range 分片把图拉全。
// 场景对应线上：img.seedvault.cn / pixhost.to 传到几十 KB 就断流，但带 Range 的分片请求正常。
func TestDownloadRousiImageFallsBackToRange(t *testing.T) {
	payload := bytes.Repeat([]byte("PIXEL"), 700*1024) // 3.5 MB，足够跨多个分片
	cutAt := 64 * 1024

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rangeHeader := strings.TrimSpace(r.Header.Get("Range"))
		if rangeHeader == "" {
			// 整段请求：先写 64KB 再断流，模拟对端 RST。
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(payload[:cutAt])
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			panic(http.ErrAbortHandler)
		}

		start, end, ok := parseTestRange(rangeHeader, int64(len(payload)))
		if !ok {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Type", "image/png")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : end+1])
	}))
	defer server.Close()

	data, err := downloadRousiImageToBytes(server.URL, "", "", server.URL+"/a.png", "")
	if err != nil {
		t.Fatalf("分片续传应当成功，实际报错: %v", err)
	}
	if len(data) != len(payload) {
		t.Fatalf("图片不完整: got %d want %d", len(data), len(payload))
	}
	if !bytes.Equal(data, payload) {
		t.Fatal("分片拼接内容与原始图片不一致")
	}
}

// TestDownloadRousiImageRejectsNoRange 校验图床忽略 Range（回 200）时给出明确错误，不静默返回半张图。
func TestDownloadRousiImageRejectsNoRange(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 1024)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.TrimSpace(r.Header.Get("Range")) == "" {
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			_, _ = w.Write(payload[:16])
			panic(http.ErrAbortHandler)
		}
		// 明确忽略 Range，按整段返回。
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	if _, err := downloadRousiImageToBytes(server.URL, "", "", server.URL+"/b.png", ""); err == nil {
		t.Fatal("图床不支持 Range 时应当报错")
	} else if !strings.Contains(err.Error(), "Range") {
		t.Fatalf("错误信息应说明 Range 不可用，实际: %v", err)
	}
}

// TestParseRousiContentRangeTotal 校验总长度解析。
func TestParseRousiContentRangeTotal(t *testing.T) {
	cases := map[string]int64{
		"bytes 0-1048575/3955182": 3955182,
		"bytes 0-1023/*":          -1,
		"":                        -1,
		"garbage":                 -1,
	}
	for header, want := range cases {
		if got := parseRousiContentRangeTotal(header); got != want {
			t.Fatalf("parseRousiContentRangeTotal(%q)=%d want %d", header, got, want)
		}
	}
}

// parseTestRange 解析测试用的 Range 头，返回闭区间 [start, end]。
func parseTestRange(header string, total int64) (int64, int64, bool) {
	value := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(header), "bytes="))
	parts := strings.SplitN(value, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || start < 0 || start >= total {
		return 0, 0, false
	}
	end := total - 1
	if raw := strings.TrimSpace(parts[1]); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		if parsed < end {
			end = parsed
		}
	}
	if end < start {
		return 0, 0, false
	}
	return start, end, true
}
