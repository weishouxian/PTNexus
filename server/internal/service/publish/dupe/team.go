package dupe

import (
	"fmt"
	"strings"

	acquireextract "github.com/pt-nexus/server/internal/service/acquire/extract"
)

// TeamKeysMatch 判断两个标题提取出的制作组是否指向同一个制作组。
//
// 参数/返回：leftTitle/rightTitle 为两侧（待发布 / 站点已有）种子标题；
// 返回 true 表示两侧制作组相同；两侧都取不到制作组时返回 false（不因缺信息而误判 dupe）。
//
// 判定口径：统一走 extract.NormalizeTeamKey 映射为标准 team.* 键再比较，
// 以便把 CCat / Ourbits / 52pt 这类别名视为同一制作组。
// 注意：两侧都落成 team.other 时不能视为相同——那说明两侧制作组都无法识别，
// 属于「无信息」，按不命中处理，避免把无关种子误判成重复。
func TeamKeysMatch(leftTitle string, rightTitle string) bool {
	leftKey := TeamKeyFromTitle(leftTitle)
	rightKey := TeamKeyFromTitle(rightTitle)
	if leftKey == "" || rightKey == "" {
		return false
	}
	if isUnknownTeamKey(leftKey) || isUnknownTeamKey(rightKey) {
		return false
	}
	return leftKey == rightKey
}

// TeamKeyFromTitle 从主标题尾部提取制作组并归一化为标准 team.* 键。
// 参数/返回：title 为种子主标题；返回标准制作组键，取不到时返回空串。
// 失败场景：标题为空或尾部没有 `-XXX` 形式时返回空串。
// 副作用：无。
func TeamKeyFromTitle(title string) string {
	raw := rawTeamFromTitle(title)
	if raw == "" {
		return ""
	}
	key := strings.TrimSpace(acquireextract.NormalizeTeamKey(raw))
	if key == "" {
		return ""
	}
	return key
}

// RawTeamFromTitle 提取标题尾部的原始制作组文本（未归一化），用于日志展示。
func RawTeamFromTitle(title string) string {
	return rawTeamFromTitle(title)
}

func rawTeamFromTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return ""
	}
	// 只认标题里最后一个分段符之后的内容：制作组统一写作 `...-GroupName`。
	// 需排除的两种误判：
	//  1. `idx < 0` 表示没有 `-`；
	//  2. 结尾就是 `-`（`idx == len-1`）表示尾部没有内容。
	idx := strings.LastIndex(trimmed, "-")
	if idx <= 0 || idx >= len(trimmed)-1 {
		return ""
	}
	raw := strings.TrimSpace(trimmed[idx+1:])
	if raw == "" {
		return ""
	}
	return raw
}

func isUnknownTeamKey(key string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(key))
	return trimmed == "" || trimmed == "team.other"
}

// DescribeDupeMatch 生成命中 dupe 时的日志文案。
// 参数/返回：query 为本次检索输入；candidate 为命中的候选种子；baseURL 为站点根地址。
// 返回可直接写入发布日志的中文说明。
// 副作用：无。
func DescribeDupeMatch(query Query, candidate Candidate, baseURL string) string {
	leftTeam := RawTeamFromTitle(query.Title)
	rightTeam := RawTeamFromTitle(candidate.Title)
	detail := fmt.Sprintf("标题=%s 体积=%s", strings.TrimSpace(candidate.Title), FormatSize(candidate.SizeBytes))
	if link := buildTorrentDetailURL(baseURL, candidate.TorrentID); link != "" {
		detail += " 详情页=" + link
	}
	if leftTeam != "" || rightTeam != "" {
		detail += fmt.Sprintf(" 制作组=%s/%s", orDash(leftTeam), orDash(rightTeam))
	}
	return fmt.Sprintf(
		"命中 dupe：站点已存在相同条目（豆瓣 %s / IMDb %s / TMDb %s），%s，体积差 %s 在容差 %s 之内",
		orDash(query.DoubanID),
		orDash(query.IMDbID),
		orDash(query.TMDbID),
		detail,
		// 体积差与容差统一按 MB 展示，与站点管理页的「体积容差（MB）」口径一致，便于直接对照。
		FormatSizeMB(sizeDistance(query.TorrentSizeBytes, candidate.SizeBytes)),
		FormatSizeMB(query.SizeToleranceBytes),
	)
}

// sizeDistance 返回两个体积之间的绝对差。
func sizeDistance(left int64, right int64) int64 {
	if left >= right {
		return left - right
	}
	return right - left
}

// SizeWithinTolerance 判断两个体积的差是否落在容差之内（差 <= 容差 即视为相同）。
func SizeWithinTolerance(left int64, right int64, tolerance int64) bool {
	if tolerance < 0 {
		tolerance = 0
	}
	return sizeDistance(left, right) <= tolerance
}

func orDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return strings.TrimSpace(value)
}
