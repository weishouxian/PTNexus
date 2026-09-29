package publisher

import (
	"errors"
	"strings"
)

// PreCheckError 表示站点发布前校验未通过（资源类型 / 媒介等硬性限制，如北洋园不接收动漫、我堡禁止 Remux）。
//
// 语义上属于「确定性拒绝」：适配器在向站点发起任何上传请求之前返回该错误，站点上不会新增种子。
// workflow 据此把结果标记为 pre_check + limit_reached，定时发种调度器会把该次发布改判为「跳过」
// 并立即处理下一个种子，而不是按普通失败进入重试队列（同样的参数重试必然同样失败）。
type PreCheckError struct {
	// Reason 为面向用户的拒绝原因，会直接出现在发布日志与预检查提示里。
	Reason string
}

// Error 实现 error 接口；Reason 为空时返回通用文案。
func (e *PreCheckError) Error() string {
	if e == nil {
		return "发布前校验未通过"
	}
	if reason := strings.TrimSpace(e.Reason); reason != "" {
		return reason
	}
	return "发布前校验未通过"
}

// NewPreCheckError 构造发布前校验错误。
// 参数/返回：reason 为拒绝原因；返回可直接作为 error 返回值的 *PreCheckError。
// 副作用：无。
func NewPreCheckError(reason string) error {
	return &PreCheckError{Reason: reason}
}

// AsPreCheckError 判断错误链中是否存在发布前校验错误，并返回其拒绝原因。
// 参数/返回：err 为适配器返回的错误；命中时返回原因与 true，否则返回空串与 false。
// 副作用：无。
func AsPreCheckError(err error) (string, bool) {
	var target *PreCheckError
	if errors.As(err, &target) {
		return target.Error(), true
	}
	return "", false
}
