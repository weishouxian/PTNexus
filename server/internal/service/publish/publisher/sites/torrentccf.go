package sites

import (
	"fmt"
	"strings"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// tccfDocCategory 是他吹吹风（TCCF / torrentccf / et8.org）「纪录片」分类的站点取值。
// 站点发种页 type 只有两个大类：624 纪录片，以及 628-635 的 Elearning（电子书/杂志/漫画/有声书/公开课/视频教程）。
// 用户明确要求「只能发布纪录片」，故非纪录片资源在入口直接拒绝，不进入分类映射。
const tccfDocCategory = "624"

// resolveTCCFRestriction 判断当前发布参数是否不属于他吹吹风允许发布的纪录片。
// 参数/返回：input 为统一发布输入；命中限制时返回拒绝原因，允许发布时返回空串。
// 判定口径：标准化 type 必须为纪录片（category.documentaries，兼容 category.documentary 单数形态）。
// 说明：站点 type 里虽也有 Elearning 电子书/学习类，但用户限定本站只发纪录片，其余一律确定性拒绝，
// 走 *publisher.PreCheckError（参数重试必然同样失败，且尚未向站点发起任何请求）。
func resolveTCCFRestriction(input publisher.PublishInput) string {
	categoryKey := ""
	if std, ok := input.UploadData["standardized_params"].(map[string]any); ok && std != nil {
		categoryKey = strings.TrimSpace(toStringAny(std["type"], ""))
	}
	lower := strings.ToLower(categoryKey)
	if lower == "category.documentaries" || lower == "category.documentary" {
		return ""
	}
	actual := categoryKey
	if actual == "" {
		actual = "（未识别）"
	}
	return fmt.Sprintf("他吹吹风仅接收纪录片资源：当前类型为 %s，已停止发布", actual)
}

// PublishTCCF 将种子发布到他吹吹风（TCCF，et8.org，标准 NexusPHP 表单）。
// 参数/返回：input 为统一发布输入；返回发布结果（详情页链接、是否已存在、过程日志）。
// 失败场景：缺少 base_url / cookie 时返回 error；非纪录片资源返回 *publisher.PreCheckError；
// 站点返回校验错误时同样返回 error。
// 副作用：读取本地种子文件并向 et8.org 发起 multipart 上传请求。
//
// 本站复用 publisher.PublishPublic 的标准六维度流程（medium/codec/audiocodec/standard/team 均有对应 select），
// 唯一站点级差异是「只能发纪录片」，因此在 delegate 之前先做发布前校验。
func PublishTCCF(input publisher.PublishInput) (publisher.PublishResult, error) {
	// 站点硬性规则：他吹吹风仅接收纪录片资源，命中即拒绝，不做分类改写后强行发布。
	// 这是发布前校验（在读取种子与任何网络请求之前），返回 *publisher.PreCheckError，
	// 上层会按「预检查限制」确定性跳过，而不是失败重试。
	if reason := resolveTCCFRestriction(input); reason != "" {
		return publisher.PublishResult{AttemptDetailLog: "发布前校验失败: " + reason}, publisher.NewPreCheckError(reason)
	}

	return publisher.PublishPublic(input)
}
