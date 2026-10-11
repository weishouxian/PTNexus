package sites

import (
	"fmt"
	"strings"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// hxptDocCategory 是好学（HXPT / hxpt / www.hxpt.org）「纪录片」分类的站点取值。
// 站点发种页 type 含教育分类（401 学前教育 402 小学部 403 初中部 404 高职部 405 高中部 406 教育）
// 与 410 纪录片。用户明确要求「只能发布纪录片」，故非纪录片资源在入口直接拒绝。
const hxptDocCategory = "410"

// resolveHXPTRestriction 判断当前发布参数是否不属于好学允许发布的纪录片。
// 参数/返回：input 为统一发布输入；命中限制时返回拒绝原因，允许发布时返回空串。
// 判定口径：标准化 type 必须为纪录片（category.documentaries，兼容 category.documentary 单数形态）。
// 说明：站点 type 里虽也有教育分类，但用户限定本站只发纪录片，其余一律确定性拒绝，
// 走 *publisher.PreCheckError（参数重试必然同样失败，且尚未向站点发起任何请求）。
func resolveHXPTRestriction(input publisher.PublishInput) string {
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
	return fmt.Sprintf("好学仅接收纪录片资源：当前类型为 %s，已停止发布", actual)
}

// PublishHXPT 将种子发布到好学（HXPT，www.hxpt.org，标准 NexusPHP 表单）。
// 参数/返回：input 为统一发布输入；返回发布结果（详情页链接、是否已存在、过程日志）。
// 失败场景：缺少 base_url / cookie 时返回 error；非纪录片资源返回 *publisher.PreCheckError；
// 站点返回校验错误时同样返回 error。
// 副作用：读取本地种子文件并向 www.hxpt.org 发起 multipart 上传请求。
//
// 本站复用 publisher.PublishPublic 的标准六维度流程。站点字段名虽是标准 NexusPHP 六维度
// （medium/codec/audiocodec/standard/team 均有对应 select），但其中多数字段被二次定义为
// 「教育」语义（audiocodec_sel=年级、codec_sel=文件格式、source_sel=发布年份、processing_sel=教材版本），
// 这些字段在 yaml 里不配置映射、由运行时跳过。唯一站点级差异是「只能发纪录片」，
// 因此在 delegate 之前先做发布前校验。
func PublishHXPT(input publisher.PublishInput) (publisher.PublishResult, error) {
	// 站点硬性规则：好学仅接收纪录片资源，命中即拒绝，不做分类改写后强行发布。
	// 这是发布前校验（在读取种子与任何网络请求之前），返回 *publisher.PreCheckError，
	// 上层会按「预检查限制」确定性跳过，而不是失败重试。
	if reason := resolveHXPTRestriction(input); reason != "" {
		return publisher.PublishResult{AttemptDetailLog: "发布前校验失败: " + reason}, publisher.NewPreCheckError(reason)
	}

	return publisher.PublishPublic(input)
}
