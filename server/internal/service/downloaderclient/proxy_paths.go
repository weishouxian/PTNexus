package downloaderclient

import (
	pathpkg "path"
	"strings"
)

// BuildProxyPathCandidates 生成盒子代理请求使用的路径候选列表。
// 参数/返回：savePaths 为按优先级排列的保存路径根（约定「下载器原始路径」在前、「路径映射后的本地路径」在后），
// torrentName/contentName 为候选子路径，preferExactPath=true 时把每个根原样作为候选（下载器已给出完整内容路径的场景）；
// 返回去重后的候选路径，顺序即尝试顺序。
// 失败场景：无。
// 副作用：无。
//
// 说明：下载器返回的是「下载器容器内路径」（例如 Transmission 的 /downloads/complete），
// 而盒子代理既可能运行在下载器同机（此时容器内路径可用，取决于挂载方式），
// 也可能只能看到宿主机路径；因此这里同时给出「原始路径」与「路径映射后的路径」两套候选，
// 由调用方按顺序探测，避免只赌其中一种而全部失败。
func BuildProxyPathCandidates(savePaths []string, torrentName, contentName string, preferExactPath bool) []string {
	trimmedTorrentName := strings.TrimSpace(torrentName)
	trimmedContentName := strings.TrimSpace(contentName)

	candidates := make([]string, 0, len(savePaths)*4)
	seen := make(map[string]struct{}, len(savePaths)*4)
	appendCandidate := func(candidate string) {
		normalized := normalizeProxyRemotePath(candidate)
		if normalized == "" {
			return
		}
		key := normalizeProxyPathForCompare(normalized)
		if _, exists := seen[key]; exists {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, normalized)
	}

	for _, rawSavePath := range savePaths {
		trimmedSavePath := strings.TrimSpace(rawSavePath)
		if trimmedSavePath == "" {
			continue
		}
		if preferExactPath {
			appendCandidate(trimmedSavePath)
		}
		if trimmedTorrentName != "" {
			appendCandidate(joinProxyRemotePath(trimmedSavePath, trimmedTorrentName))
		}
		if trimmedContentName != "" && !strings.EqualFold(trimmedContentName, trimmedTorrentName) {
			appendCandidate(joinProxyRemotePath(trimmedSavePath, trimmedContentName))
		}
		appendCandidate(trimmedSavePath)
	}
	return candidates
}

func joinProxyRemotePath(base, name string) string {
	normalizedBase := normalizeProxyRemotePath(base)
	normalizedName := strings.Trim(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"), "/")
	if normalizedBase == "" {
		return normalizedName
	}
	if normalizedName == "" {
		return normalizedBase
	}
	return pathpkg.Join(normalizedBase, normalizedName)
}

func normalizeProxyRemotePath(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.ReplaceAll(trimmed, "\\", "/")
}

// normalizeProxyPathForCompare 归一化路径用于候选去重比较（统一分隔符、压缩重复斜杠、去掉结尾斜杠）。
// 参数/返回：value 为待归一化路径；返回可直接做等值比较的字符串。
// 失败场景：无。
// 副作用：无。
func normalizeProxyPathForCompare(value string) string {
	normalized := normalizeProxyRemotePath(value)
	for strings.Contains(normalized, "//") {
		normalized = strings.ReplaceAll(normalized, "//", "/")
	}
	return strings.TrimRight(normalized, "/")
}
