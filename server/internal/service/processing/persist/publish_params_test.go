package persist

import (
	"strings"
	"testing"
)

// TestBuildCompletePublishParamsTrimsMovieParams 验证读取期清洗：
// 历史入库的简介正文/声明若含【影片参数】段，组装发布参数时应被截断。
func TestBuildCompletePublishParamsTrimsMovieParams(t *testing.T) {
	rawBody := "四年前，13岁的芬尼杀死绑架他的凶手并成功逃脱。\n" +
		"[color=blue][b]【影片参数】[/b][/color]\n" +
		"[b][color=blue]【截图赏析】[/color][/b]：\n" +
		"[url=https://img.hdsky.me/image/T9FgQI][/url]"
	rawStatement := "[quote]声明内容[/quote]\n[b][color=blue]【影片参数】[/color][/b]\nmediainfo"

	params := BuildCompletePublishParams(map[string]any{
		"title":     "黑色电话2",
		"body":      rawBody,
		"statement": rawStatement,
	})

	intro, ok := params["intro"].(map[string]any)
	if !ok {
		t.Fatalf("intro 字段类型异常: %#v", params["intro"])
	}

	body, _ := intro["body"].(string)
	if strings.Contains(body, "影片参数") || strings.Contains(body, "截图赏析") || strings.Contains(body, "img.hdsky.me") {
		t.Errorf("body 未被截断: %q", body)
	}
	if !strings.Contains(body, "四年前，13岁的芬尼杀死绑架他的凶手并成功逃脱。") {
		t.Errorf("body 丢失了影片介绍内容: %q", body)
	}

	statement, _ := intro["statement"].(string)
	if strings.Contains(statement, "影片参数") || strings.Contains(statement, "mediainfo") {
		t.Errorf("statement 未被截断: %q", statement)
	}
}

// TestBuildCompletePublishParamsKeepsNonStringIntro 验证非字符串简介值原样传递，不 panic。
func TestBuildCompletePublishParamsKeepsNonStringIntro(t *testing.T) {
	params := BuildCompletePublishParams(map[string]any{
		"title": "测试",
		"body":  nil,
	})
	intro, ok := params["intro"].(map[string]any)
	if !ok {
		t.Fatalf("intro 字段类型异常: %#v", params["intro"])
	}
	if intro["body"] != nil {
		t.Errorf("非字符串 body 应原样保留，实际=%#v", intro["body"])
	}
}

// TestCleanDescriptionValue 验证清洗辅助函数的字符串/非字符串分支。
func TestCleanDescriptionValue(t *testing.T) {
	if got := cleanDescriptionValue("正文\n[b][color=blue]【影片参数】[/color][/b]\nxx"); got != "正文" {
		t.Errorf("cleanDescriptionValue 字符串分支异常: %#v", got)
	}
	if got := cleanDescriptionValue(nil); got != nil {
		t.Errorf("cleanDescriptionValue nil 分支应返回 nil，实际=%#v", got)
	}
	if got := cleanDescriptionValue(123); got != 123 {
		t.Errorf("cleanDescriptionValue 非字符串分支应原样返回，实际=%#v", got)
	}
}
