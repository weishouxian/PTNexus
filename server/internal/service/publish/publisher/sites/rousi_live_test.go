//go:build rousilive

package sites

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// 该测试只用于人工核对真实站点契约，默认不参与编译（go test -tags rousilive）。
// 期望结果：站点返回 invalid_torrent_submission（说明 multipart 结构、CSRF、Cookie 全部被接受），
// 若返回 contract_validation_failed 则说明请求体格式与站点契约不符。
func TestRousiLiveContract(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("http_proxy", "")

	cookie := strings.TrimSpace(os.Getenv("ROUSI_COOKIE"))
	if cookie == "" {
		t.Skip("未设置 ROUSI_COOKIE")
	}

	dir := t.TempDir()
	torrentPath := dir + "/probe.torrent"
	if err := os.WriteFile(torrentPath, []byte("d4:infod4:name1:xee"), 0o644); err != nil {
		t.Fatalf("写入种子失败: %v", err)
	}

	// 1x1 JPEG，用于验证 screenshots part 的格式能否被站点接受。
	const jpegB64 = "/9j/4AAQSkZJRgABAQEAYABgAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/wAALCAABAAEBAREA/8QAFAABAAAAAAAAAAAAAAAAAAAACf/EABQQAQAAAAAAAAAAAAAAAAAAAAD/2gAIAQEAAD8AKp//2Q=="

	result, err := PublishRousi(publisher.PublishInput{
		TargetName:  "肉丝",
		SiteCode:    "rousi",
		BaseURL:     "https://rousi.pro",
		TorrentPath: torrentPath,
		Title:       "契约自检 Probe Title",
		Subtitle:    "契约自检副标题",
		TargetInfo: map[string]any{
			"site":     "rousi",
			"passkey":  cookie,
			"nickname": "肉丝",
		},
		UploadData: map[string]any{
			"type": "category.movie",
			"tags": []any{"tag.剧情", "tag.动作"},
			"images": []any{
				"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(mustDecodeB64(jpegB64)),
			},
			"standardized_params": map[string]any{
				"region":     "source.china",
				"resolution": "resolution.r1080p",
			},
		},
	})

	t.Logf("err=%v", err)
	t.Logf("detail:\n%s", result.AttemptDetailLog)

	if err == nil {
		t.Fatalf("预期失败（坏种子），实际成功: %s", result.PublishURL)
	}
	if strings.Contains(result.AttemptDetailLog, "contract_validation_failed") {
		t.Fatalf("站点判定请求体格式不符契约，multipart 需要调整")
	}
	if !strings.Contains(result.AttemptDetailLog, "invalid_torrent_submission") {
		t.Fatalf("未命中预期的种子校验错误，请人工核对日志")
	}
}

func mustDecodeB64(value string) []byte {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		panic(err)
	}
	return raw
}
