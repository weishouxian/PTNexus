package workflow

import "testing"

// 发种入口的声明兜底：库里存量的纯文本声明（如源站「XX官组作品，感谢原制作者发布。」）
// 必须在组装描述前补齐 [quote] 包裹，顶层 key 与 intro 子 map 两个来源都要覆盖。
func TestEnsureUploadDataStatementQuote(t *testing.T) {
	uploadData := map[string]any{
		"statement": "顶层官组作品，感谢原制作者发布。",
		"intro": map[string]any{
			"statement": "Audiences官组作品，感谢原制作者发布。",
			"body":      "◎片　　名　x",
		},
	}

	got := ensureUploadDataStatementQuote(uploadData)
	if got["statement"] != "[quote]顶层官组作品，感谢原制作者发布。[/quote]" {
		t.Fatalf("顶层声明未补包裹: %q", got["statement"])
	}
	intro, _ := got["intro"].(map[string]any)
	if intro["statement"] != "[quote]Audiences官组作品，感谢原制作者发布。[/quote]" {
		t.Fatalf("intro 声明未补包裹: %q", intro["statement"])
	}

	// 幂等：再调用一次不应出现嵌套包裹。
	again := ensureUploadDataStatementQuote(got)
	if again["statement"] != "[quote]顶层官组作品，感谢原制作者发布。[/quote]" {
		t.Fatalf("重复调用产生了嵌套或变化: %q", again["statement"])
	}

	// intro 里只有 body 时不能被改动。
	onlyBody := map[string]any{"intro": map[string]any{"body": "正文"}}
	if out := ensureUploadDataStatementQuote(onlyBody); out["intro"].(map[string]any)["body"] != "正文" {
		t.Fatalf("无声明时不应改动 intro: %#v", out["intro"])
	}

	// nil 入参不 panic，返回可用的空 map。
	if out := ensureUploadDataStatementQuote(nil); out == nil || len(out) != 0 {
		t.Fatalf("nil 入参应返回空 map，got %#v", out)
	}
}
