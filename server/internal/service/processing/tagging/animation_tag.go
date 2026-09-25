package tagging

import "strings"

// 动漫标签判定：动漫属性由标签表达（标准标签带 tag. 前缀，站点原始标签为纯文本），此处统一识别。

// animationTagValues 动漫/动画标签的等价写法。
var animationTagValues = map[string]struct{}{
	"tag.动漫": {}, "tag.动画": {},
	"动漫": {}, "动画": {},
	"anime": {}, "animation": {},
}

// IsAnimationTag 判断单个标签是否为动漫/动画标签。
// 参数/返回：tag 为标签文本；命中返回 true。
// 失败场景：标签为空或无法识别时返回 false。
// 副作用：无。
func IsAnimationTag(tag string) bool {
	_, ok := animationTagValues[strings.ToLower(strings.TrimSpace(tag))]
	return ok
}

// HasAnimationTag 判断标签集合中是否包含动漫/动画标签。
// 参数/返回：tags 为标签切片；命中返回 true。
// 失败场景：标签集合为空或无法识别时返回 false。
// 副作用：无。
func HasAnimationTag(tags []string) bool {
	for _, tag := range tags {
		if IsAnimationTag(tag) {
			return true
		}
	}
	return false
}
