package sites

import (
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// audiencesDupeLogModule 为人人站 dupe 相关日志的模块名。
const audiencesDupeLogModule = "发布-dupe校验-人人"

// BeforeUpload 在人人站发布前执行 dupe 查重。
//
// 触发条件（两者同时满足）：
//  1. 站点设置里开启了「dupe 校验」（dupe_check_enabled）；
//  2. 站点 YAML 声明了 dupe 能力（configs/audiences.yaml 的 dupe_check.enabled）。
//
// 判定口径：以豆瓣 / IMDb ID 加「类型 + 媒介 + 分辨率 + 音频编码 + 视频编码」检索站点，
// 若候选中「标题提取的制作组相同」且「体积差在容差之内」，则认定为重复种子并拒绝发布。
// 具体判定与失败语义见 runSiteDupeCheck。
func (audiencesPublisher) BeforeUpload(input publisher.PublishInput, formFields map[string]string) (string, error) {
	return runSiteDupeCheck(audiencesDupeLogModule, input, formFields)
}
