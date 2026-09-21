// Package mteamapi 提供 M-Team（馒头）站点与 PTNexus 交互所需的公共能力：
// API Token 解析、站点识别、种子 ID 提取与下载直链换取。
//
// 发布适配器（publish/publisher/sites）与源站下载（acquire/fetch）都依赖本包，
// 避免两条链路各自实现一份解析逻辑而产生行为漂移。
package mteamapi

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Credential 表示一个候选凭据及其来源标签。
// 来源标签用于日志诊断，便于发现用户填错了栏位（Passkey 栏 / Cookie 栏）。
type Credential struct {
	Source string
	Value  string
}

// tokenPattern 匹配控制台生成的存取令牌（UUID 形态）。
var tokenPattern = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// siteNeedles 为站点识别用的特征串（站点代码、域名、中文别名）。
var siteNeedles = []string{"m-team", "mteam", "馒头"}

var (
	reDetailID = regexp.MustCompile(`(?i)/detail/(\d+)`)
	reQueryID  = regexp.MustCompile(`(?i)[?&]id=(\d+)`)
)

// ExtractToken 从用户填写的原始文本中提取 API 存取令牌，取不到时返回空串。
//
// 支持形态（按优先级）：
//   - 键值形态：x-api-key: xxx / x-api-key=xxx / apikey=xxx / token=xxx（可混在 cookie 串中）
//   - 混杂文本中的 UUID（误粘贴 curl 命令、含多余字符时兜底）
//   - 纯令牌：整串不含空白、分号与等号
//
// 明确返回空的场景：填的是网页 Cookie（形如 c_secure_uid=xxx; c_secure_pass=yyy），
// 这种串里既没有 api key 键名也没有 UUID，必须拒绝，否则会整串当令牌发出、站点报「key無效」。
func ExtractToken(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	// 1) 键值形态：按 ; / & / 换行切段后比对键名
	segments := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == ';' || r == '&' || r == '\n' || r == '\r'
	})
	for _, segment := range segments {
		seg := strings.TrimSpace(segment)
		segLower := strings.ToLower(seg)
		for _, prefix := range []string{
			"x-api-key=", "x-api-key:", "apikey=", "apikey:", "api_key=", "api_key:",
			"access_token=", "access_token:", "token=", "token:",
		} {
			if strings.HasPrefix(segLower, prefix) {
				if value := strings.Trim(strings.TrimSpace(seg[len(prefix):]), `"'`); value != "" {
					return value
				}
			}
		}
	}

	// 2) 混杂文本中捞 UUID（例如整段 curl 命令）
	if match := tokenPattern.FindString(trimmed); match != "" {
		return match
	}

	// 3) 纯令牌：不含空白、分号与等号时原样返回（令牌不一定是 UUID 形态）
	if !strings.ContainsAny(trimmed, " \t;=&") {
		return strings.Trim(trimmed, `"'`)
	}

	return ""
}

// ResolveToken 按给定顺序尝试候选凭据，返回第一个能解析出令牌的结果。
// 全部取不到时返回零值 Credential（Value 为空串）。
func ResolveToken(candidates []Credential) Credential {
	for _, candidate := range candidates {
		if token := ExtractToken(candidate.Value); token != "" {
			return Credential{Source: candidate.Source, Value: token}
		}
	}
	return Credential{}
}

// Fingerprint 生成令牌的脱敏指纹，用于日志核对（只暴露前 8 位与长度）。
func Fingerprint(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return "(空)"
	}
	if len(trimmed) <= 8 {
		return fmt.Sprintf("(长度 %d)", len(trimmed))
	}
	return fmt.Sprintf("%s…(长度 %d)", trimmed[:8], len(trimmed))
}

// IsSite 判断给定站点代码/地址是否属于 M-Team。
// 参数/返回：siteCode 为站点代码，baseURL/detailURL 为站点地址；命中返回 true。
// 失败场景：不适用。
// 副作用：无。
func IsSite(siteCode, baseURL, detailURL string) bool {
	for _, raw := range []string{siteCode, baseURL, detailURL} {
		lower := strings.ToLower(strings.TrimSpace(raw))
		if lower == "" {
			continue
		}
		for _, needle := range siteNeedles {
			if strings.Contains(lower, needle) {
				return true
			}
		}
	}
	return false
}

// ExtractTorrentID 从详情页地址或裸 ID 中提取数字种子 ID。
// 参数/返回：value 可为 https://kp.m-team.cc/detail/{id}、含 id= 的地址或纯数字；未命中返回空串。
// 失败场景：输入为空或无法识别 ID。
// 副作用：无。
func ExtractTorrentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	for _, pattern := range []*regexp.Regexp{reDetailID, reQueryID} {
		if match := pattern.FindStringSubmatch(trimmed); len(match) >= 2 {
			return match[1]
		}
	}
	if _, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return trimmed
	}
	return ""
}
