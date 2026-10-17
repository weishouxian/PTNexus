package tagging

import "strings"

// StandardEpisodeTag 是「分集」受限标签的标准形式。
// 分集与其余受限标签（禁转/限转）性质不同：禁转/限转由用户在界面上确认后放行，
// 而分集默认禁止发布，只在站点 YAML 显式声明 allow_episode_publish 时才允许。
const StandardEpisodeTag = "tag.分集"

var restrictedStandardTags = map[string]string{
	"禁转":     "tag.禁转",
	"tag.禁转": "tag.禁转",
	"限转":     "tag.限转",
	"tag.限转": "tag.限转",
	"分集":     StandardEpisodeTag,
	"tag.分集": StandardEpisodeTag,
}

// IsEpisodeRestrictedTag 判断标准受限标签是否为「分集」。
// 参数/返回：tag 为 DetectRestrictedTags 返回的标准标签；命中分集返回 true。
// 说明：禁转/限转与分集在发布链路上走两套放行规则（前者可由用户确认放行，后者只认站点配置），
// 必须靠这个谓词区分，不要再写第二份字符串比较。
func IsEpisodeRestrictedTag(tag string) bool {
	return strings.TrimSpace(tag) == StandardEpisodeTag
}

// DetectRestrictedTags 检测标签列表中的禁转/限转/分集标签，并返回去重后的标准标签。
func DetectRestrictedTags(tags []string) []string {
	result := make([]string, 0, 3)
	seen := make(map[string]struct{}, 3)
	for _, raw := range tags {
		mapped, ok := restrictedStandardTags[strings.TrimSpace(raw)]
		if !ok || strings.TrimSpace(mapped) == "" {
			continue
		}
		if _, exists := seen[mapped]; exists {
			continue
		}
		seen[mapped] = struct{}{}
		result = append(result, mapped)
	}
	return result
}
