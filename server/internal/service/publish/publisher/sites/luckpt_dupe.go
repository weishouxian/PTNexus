package sites

import (
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// luckptDupeLogModule 为幸运站 dupe 相关日志的模块名。
const luckptDupeLogModule = "发布-dupe校验-幸运"

// BeforeUpload 在幸运站发布前执行 dupe 查重。
//
// 触发条件与判定口径同人人站（见 runSiteDupeCheck），站点差异只有检索方式：
// 幸运站 search_area 没有豆瓣 / TMDb 范围（仅 0=标题 / 1=简介 / 3=发布者 / 4=IMDb），
// 因此走「IMDb 优先 + 标题兜底」两段检索，具体见 dupe/luckpt.go。
func (luckptPublisher) BeforeUpload(input publisher.PublishInput, formFields map[string]string) (string, error) {
	return runSiteDupeCheck(luckptDupeLogModule, input, formFields)
}
