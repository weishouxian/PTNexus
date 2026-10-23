package torrentdata

import (
	"strings"
	"testing"
)

// 线上 UBits 403 的真实日志（截断 HTML 部分），「发布结果」行在最后。
const ubitsForbiddenLogs = `--- [步骤2] 开始发布种子到 UBits ---
发布字段摘要: site=ubits config=/app/server/configs/ubits.yaml category=type:402 region=source_sel[4]:5 tags=2 checkbox_tags=0
上传尝试: https://ubits.club/takeupload.php (文件字段: file)
正在上传种子文件...
读取上传页隐藏字段失败，继续直接提交: 上传页响应状态异常: 403（请检查站点 Cookie 是否已失效，或当前账号是否具备发种权限）
响应状态: 403 403 Forbidden
站点响应摘要: 返回 HTML 页面 (title=Just a moment...): <!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title></head></html>
尝试结论: HTTP 403 上传失败: 站点返回 Cloudflare 人机校验页面（Cookie 失效或请求被拦截时会出现），请检查站点 Cookie 是否已失效，或当前账号是否具备发种权限
发布结果：发布到 UBits 失败: HTTP 403 上传失败: 站点返回 Cloudflare 人机校验页面（Cookie 失效或请求被拦截时会出现），请检查站点 Cookie 是否已失效，或当前账号是否具备发种权限
--- [步骤2] 任务执行完毕 ---`

// TestExtractPublishReasonPrefersResultLine 关键行在尾部时，必须取「发布结果」而不是过程行。
func TestExtractPublishReasonPrefersResultLine(t *testing.T) {
	got := extractPublishReason(ubitsForbiddenLogs)
	want := "发布到 UBits 失败: HTTP 403 上传失败: 站点返回 Cloudflare 人机校验页面（Cookie 失效或请求被拦截时会出现），请检查站点 Cookie 是否已失效，或当前账号是否具备发种权限"
	if got != want {
		t.Fatalf("原因提取不符合预期\n got: %q\nwant: %q", got, want)
	}
}

// TestExtractPublishReasonIgnoresHeadOnlyTruncation 模拟早期「按头部截断」留下的日志片段：
// 只有过程行，没有「发布结果」——此时兜底也不得把 HTML 塞给前端。
func TestExtractPublishReasonIgnoresHeadOnlyTruncation(t *testing.T) {
	head := strings.Join([]string{
		"--- [步骤2] 开始发布种子到 UBits ---",
		"响应状态: 403 403 Forbidden",
		"站点响应摘要: 返回 HTML 页面 (title=Just a moment...): <!DOCTYPE html><html lang=\"en-US\">",
	}, "\n")

	got := extractPublishReason(head)
	if strings.Contains(strings.ToLower(got), "<!doctype") || strings.Contains(got, "<html") {
		t.Fatalf("兜底原因不应包含 HTML 片段, got: %q", got)
	}
	if got != "站点响应摘要: 返回 HTML 页面 (title=Just a moment...)" {
		t.Fatalf("兜底原因不符合预期, got: %q", got)
	}
}

// TestExtractPublishReasonStripsEmbeddedHTML 老日志把整页 HTML 写进了「发布结果」行，需截掉 HTML 尾巴。
func TestExtractPublishReasonStripsEmbeddedHTML(t *testing.T) {
	logs := strings.Join([]string{
		"尝试结论: HTTP 403 上传失败: 返回 HTML 页面 (title=Just a moment...)",
		"发布结果：发布到 UBits 失败: HTTP 403 上传失败: 返回 HTML 页面 (title=Just a moment...): <!DOCTYPE html><html lang=\"en-US\"><head><title>Just a moment...</title></head></html>",
		"--- [步骤2] 任务执行完毕 ---",
	}, "\n")

	got := extractPublishReason(logs)
	want := "发布到 UBits 失败: HTTP 403 上传失败: 返回 HTML 页面 (title=Just a moment...)"
	if got != want {
		t.Fatalf("HTML 清洗结果不符合预期\n got: %q\nwant: %q", got, want)
	}
}

// TestExtractPublishReasonSiteErrorLine 「站点错误提示」行优先于兜底行。
func TestExtractPublishReasonSiteErrorLine(t *testing.T) {
	logs := strings.Join([]string{
		"站点错误提示: 上传失败！数据缺失",
		"响应状态: 200 OK",
	}, "\n")

	if got := extractPublishReason(logs); got != "上传失败！数据缺失" {
		t.Fatalf("站点错误提示提取不符合预期, got: %q", got)
	}
}

// TestPublishStatusGroup 校验状态分组（前端标签配色与汇总计数依赖它）。
func TestPublishStatusGroup(t *testing.T) {
	cases := map[string]string{
		"success":         publishGroupSuccess,
		"edited":          publishGroupSuccess,
		"exists":          publishGroupExists,
		"failed":          publishGroupFailed,
		"pre_check_limit": publishGroupFailed,
		"queued":          publishGroupPending,
		"running":         publishGroupPending,
		"invalidated":     publishGroupVoid,
		"whatever":        publishGroupOther,
	}
	for status, want := range cases {
		if got := publishStatusGroup(status); got != want {
			t.Fatalf("publishStatusGroup(%q)=%q, want %q", status, got, want)
		}
	}
}
