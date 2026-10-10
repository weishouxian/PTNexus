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
	// Detail 为判定过程日志（检索 URL、候选数量、逐项比对结果等），会一并写入发布日志。
	// 用于回答「为什么判定为重复 / 为什么跳过」，为空时不影响原有行为。
	Detail string
	// Meta 为结构化附加信息，供上层原样回传前端做富展示（如 dupe 的查重地址与重复种子详情页）。
	// 键名由各校验方自行约定；为空时不影响原有行为。
	Meta map[string]any
}

// dupe 校验回传前端的结构化键名。放在这里是为了让「后端产出」与「前端消费」有一处可对照的约定。
const (
	// PreCheckMetaDupeBlocked 标记本次拦截来自 dupe 查重（前端据此显示「dupe 发布失败」）。
	PreCheckMetaDupeBlocked = "dupe_blocked"
	// PreCheckMetaDupeSearchURL 本次命中 dupe 所用的检索地址。
	PreCheckMetaDupeSearchURL = "dupe_search_url"
	// PreCheckMetaDupeTorrentURL 命中的重复种子详情页地址。
	PreCheckMetaDupeTorrentURL = "dupe_torrent_url"
)

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

// NewPreCheckErrorWithDetail 构造带过程日志的发布前校验错误。
// 参数/返回：reason 为拒绝原因；detail 为判定过程日志（可为空）。
// 返回可直接作为 error 返回值的 *PreCheckError。
// 副作用：无。
func NewPreCheckErrorWithDetail(reason string, detail string) error {
	return &PreCheckError{Reason: reason, Detail: strings.TrimSpace(detail)}
}

// AsPreCheckError 判断错误链中是否存在发布前校验错误，并返回其拒绝原因。
// 参数/返回：err 为适配器返回的错误；命中时返回原因与 true，否则返回空串与 false。
// 副作用：无。
func AsPreCheckError(err error) (string, bool) {
	reason, _, ok := AsPreCheckErrorWithDetail(err)
	return reason, ok
}

// AsPreCheckErrorWithDetail 判断错误链中是否存在发布前校验错误，并返回拒绝原因与过程日志。
// 参数/返回：err 为适配器返回的错误；命中时返回原因、过程日志与 true。
// 失败场景：非 *PreCheckError 时返回空串、空串与 false。
// 副作用：无。
func AsPreCheckErrorWithDetail(err error) (string, string, bool) {
	var target *PreCheckError
	if errors.As(err, &target) {
		return target.Error(), target.Detail, true
	}
	return "", "", false
}

// NewPreCheckErrorWithMeta 构造带过程日志与结构化附加信息的发布前校验错误。
// 参数/返回：reason 为拒绝原因；detail 为判定过程日志（可为空）；meta 为回传前端的结构化信息（可为空）。
// 返回可直接作为 error 返回值的 *PreCheckError。
// 副作用：无。
func NewPreCheckErrorWithMeta(reason string, detail string, meta map[string]any) error {
	return &PreCheckError{
		Reason: reason,
		Detail: strings.TrimSpace(detail),
		Meta:   meta,
	}
}

// PreCheckErrorFrom 返回错误链中的 *PreCheckError，便于读取 Reason / Detail / Meta 三个字段。
// 参数/返回：err 为适配器返回的错误；命中时返回该错误对象与 true。
// 失败场景：错误链中不存在 *PreCheckError 时返回 nil 与 false。
// 副作用：无。
func PreCheckErrorFrom(err error) (*PreCheckError, bool) {
	var target *PreCheckError
	if errors.As(err, &target) {
		return target, true
	}
	return nil, false
}
