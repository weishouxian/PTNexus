package torrentdata

import (
	"errors"
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/service/acquire/fetch"
)

// 种子地址反查：根据下载器种子的 info_hash 在匹配到的 PT 站内搜索同名种子，
// 逐一下载候选 .torrent 并比对 info_hash，命中后返回该站的下载直链。

const torrentURLResolveLogModule = "种子地址反查"

const (
	torrentURLResolveSearchTimeout = 30 * time.Second
	torrentURLResolveDownloadTO    = 60 * time.Second
	torrentURLResolveMaxSites      = 3
	torrentURLResolveMaxCandidates = 5
)

var (
	reHexHash             = regexp.MustCompile(`^[0-9a-f]{40}$`)
	reSearchPageDownload  = regexp.MustCompile(`download\.php\?id=(\d+)`)
	reSiteNameSplitters   = regexp.MustCompile(`[,，、/|;；]+`)
	reSearchNameSanitizer = regexp.MustCompile(`[^0-9A-Za-z\u4e00-\u9fa5.\- ]+`)
)

// TorrentURLResolveRequest 表示一次种子地址反查请求。
// 参数/返回：hash 为 40 位 info_hash；name 为种子名；sites 为已识别站点昵称（分隔符任意）；trackers/detail/comment 用于 tracker 兜底匹配。
// 失败场景：不适用。
// 副作用：无。
type TorrentURLResolveRequest struct {
	Hash     string   `json:"hash"`
	Name     string   `json:"name"`
	Sites    string   `json:"sites"`
	Trackers []string `json:"trackers"`
	Detail   string   `json:"detail"`
	Comment  string   `json:"comment"`
}

// TorrentURLResolveResult 表示反查成功后的结果。
// 参数/返回：site 为命中的站点昵称；torrent_url 为站点下载直链；detail_url 为详情页地址。
// 失败场景：不适用。
// 副作用：无。
type TorrentURLResolveResult struct {
	Site       string `json:"site"`
	TorrentURL string `json:"torrent_url"`
	DetailURL  string `json:"detail_url"`
}

// ResolveTorrentURL 按候选站点搜索并校验 info_hash，返回种子下载直链。
// 参数/返回：req 为反查请求；返回结果负载与 HTTP 状态码（200 成功/未命中，400 参数非法）。
// 失败场景：参数非法、无候选站点、站点请求失败或未命中时以 success=false 返回原因。
// 副作用：会向候选站点发起搜索页与 .torrent 下载网络请求。
func (s *TorrentDataService) ResolveTorrentURL(req TorrentURLResolveRequest) (map[string]any, int) {
	hash := strings.ToLower(strings.TrimSpace(req.Hash))
	if !reHexHash.MatchString(hash) {
		return resolveFailPayload("缺少有效的 info_hash（40 位十六进制）"), 400
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return resolveFailPayload("缺少种子名称，无法进行站内搜索"), 400
	}

	candidateSites := s.resolveCandidateSites(req)
	if len(candidateSites) == 0 {
		return resolveFailPayload("未能识别种子所属站点：站点列表与 tracker 均未匹配到已配置站点"), 200
	}
	logx.Infof(torrentURLResolveLogModule, "开始反查 hash=%s name=%s 候选站点=%v", hash, name, candidateSites)

	reasons := make([]string, 0, len(candidateSites))
	for _, siteName := range candidateSites {
		result, reason, err := s.resolveTorrentURLForSite(siteName, hash, name)
		if err != nil {
			if errors.Is(err, fetch.ErrSourceCookieExpired) {
				logx.Warnf(torrentURLResolveLogModule, "站点 Cookie 失效 site=%s hash=%s", siteName, hash)
				return resolveFailPayload(fmt.Sprintf("站点「%s」%v", siteName, err)), 200
			}
			logx.Warnf(torrentURLResolveLogModule, "站点反查失败 site=%s hash=%s err=%v", siteName, hash, err)
			reasons = append(reasons, fmt.Sprintf("%s: %v", siteName, err))
			continue
		}
		if result != nil {
			logx.Infof(torrentURLResolveLogModule, "反查命中 site=%s hash=%s torrent_url=%s", siteName, hash, result.TorrentURL)
			return map[string]any{"success": true, "data": result}, 200
		}
		reasons = append(reasons, fmt.Sprintf("%s: %s", siteName, reason))
	}

	message := "候选站点中未找到相同 info_hash 的种子"
	if len(reasons) > 0 {
		message = "反查未命中：" + strings.Join(reasons, "；")
	}
	logx.Warnf(torrentURLResolveLogModule, "反查未命中 hash=%s name=%s reasons=%v", hash, name, reasons)
	return resolveFailPayload(message), 200
}

// resolveCandidateSites 汇总候选站点昵称：优先 tracker/详情匹配，其次使用请求携带的站点列表。
// 参数/返回：req 为反查请求；返回去重后的站点昵称列表（最多 3 个）。
// 失败场景：不适用。
// 副作用：无。
func (s *TorrentDataService) resolveCandidateSites(req TorrentURLResolveRequest) []string {
	result := make([]string, 0, torrentURLResolveMaxSites)
	seen := map[string]struct{}{}
	appendSite := func(name string) {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			return
		}
		if _, exists := seen[trimmed]; exists {
			return
		}
		seen[trimmed] = struct{}{}
		if len(result) < torrentURLResolveMaxSites {
			result = append(result, trimmed)
		}
	}

	if identities, err := s.repo.ListSiteIdentities(); err == nil && len(identities) > 0 {
		matcher := newRefreshSiteMatcher(identities)
		if matched := matcher.Match(req.Trackers, req.Detail, req.Comment); matched != "" {
			appendSite(matched)
		}
	}
	for _, part := range reSiteNameSplitters.Split(req.Sites, -1) {
		if len(result) >= torrentURLResolveMaxSites {
			break
		}
		appendSite(part)
	}
	return result
}

// resolveTorrentURLForSite 在单个站点内搜索种子名并逐候选比对 info_hash。
// 参数/返回：siteName 为站点昵称，hash 为目标 info_hash，name 为种子名；返回命中结果、未命中原因与错误。
// 失败场景：站点配置缺失、Cookie 失效、搜索页请求失败时返回错误；搜索成功但无候选/未命中时 result 为 nil。
// 副作用：发起网络请求。
func (s *TorrentDataService) resolveTorrentURLForSite(siteName, hash, name string) (*TorrentURLResolveResult, string, error) {
	siteRow, err := s.repo.GetSiteConnectionByName(siteName)
	if err != nil {
		return nil, "", fmt.Errorf("站点配置读取失败: %w", err)
	}
	baseURL := fetch.NormalizeSiteBaseURL(toStringValue(siteRow["base_url"]))
	if baseURL == "" {
		return nil, "", fmt.Errorf("站点缺少 base_url，无法构造搜索地址")
	}
	cookie := strings.TrimSpace(toStringValue(siteRow["cookie"]))
	passkey := strings.TrimSpace(toStringValue(siteRow["passkey"]))
	siteCode := strings.ToLower(strings.TrimSpace(toStringValue(siteRow["site"])))
	if cookie == "" && passkey == "" {
		return nil, "", fmt.Errorf("缺少 cookie/passkey，无法访问站点")
	}

	searchURL := fmt.Sprintf("%s/torrents.php?search=%s&search_area=0&search_mode=0", baseURL, neturl.QueryEscape(buildSearchKeyword(name)))
	html, err := fetch.FetchPageWithCookie(searchURL, cookie, torrentURLResolveSearchTimeout)
	if err != nil {
		return nil, "", fmt.Errorf("搜索页请求失败: %w", err)
	}
	ids := collectSearchCandidateIDs(html)
	if len(ids) == 0 {
		return nil, fmt.Sprintf("搜索页未解析到下载候选（关键词：%s）", buildSearchKeyword(name)), nil
	}
	logx.Infof(torrentURLResolveLogModule, "搜索候选 site=%s hash=%s 候选数=%d", siteName, hash, len(ids))

	for _, id := range ids {
		detailURL := fmt.Sprintf("%s/details.php?id=%s", baseURL, id)
		result, err := s.tryDownloadCandidate(siteCode, baseURL, detailURL, id, passkey, cookie, hash)
		if err != nil {
			logx.Warnf(torrentURLResolveLogModule, "候选校验失败 site=%s id=%s err=%v", siteName, id, err)
			continue
		}
		if result != nil {
			result.Site = siteName
			return result, "", nil
		}
	}
	return nil, fmt.Sprintf("已检查 %d 个候选，均与目标 info_hash 不一致", len(ids)), nil
}

// tryDownloadCandidate 下载单个候选种子的 .torrent 并比对 info_hash。
// 参数/返回：返回命中结果（info_hash 一致）或 nil（不一致/不可用）；返回错误仅表示下载异常。
// 失败场景：下载失败、内容非 torrent、bencode 解析失败。
// 副作用：发起网络请求。
func (s *TorrentDataService) tryDownloadCandidate(siteCode, baseURL, detailURL, torrentID, passkey, cookie, hash string) (*TorrentURLResolveResult, error) {
	candidates := make([]string, 0, 2)
	if direct := fetch.BuildDirectDownloadURL(baseURL, siteCode, detailURL, torrentID, passkey); direct != "" {
		candidates = append(candidates, direct)
	}
	for _, candidate := range candidates {
		body, err := fetch.FetchBinaryWithCookie(candidate, detailURL, cookie, torrentURLResolveDownloadTO)
		if err != nil {
			return nil, err
		}
		if !fetch.IsLikelyTorrent(body) {
			return nil, fmt.Errorf("候选内容不是 torrent 文件: %s", candidate)
		}
		meta, err := fetch.ParseTorrentMeta(body)
		if err != nil {
			return nil, fmt.Errorf("torrent 解析失败: %w", err)
		}
		if strings.EqualFold(meta.InfoHash, hash) {
			return &TorrentURLResolveResult{
				TorrentURL: candidate,
				DetailURL:  detailURL,
			}, nil
		}
	}
	return nil, nil
}

// buildSearchKeyword 清理种子名生成站内搜索关键词：去除特殊符号并截断到 120 字符。
// 参数/返回：name 为原始种子名；返回清洗后的关键词。
// 失败场景：不适用。
// 副作用：无。
func buildSearchKeyword(name string) string {
	trimmed := strings.TrimSpace(name)
	cleaned := reSearchNameSanitizer.ReplaceAllString(trimmed, " ")
	cleaned = strings.Join(strings.Fields(cleaned), " ")
	if len([]rune(cleaned)) > 120 {
		runes := []rune(cleaned)
		if cut := strings.LastIndex(cleaned, " "); cut > 0 && cut < len(cleaned) {
			cleaned = strings.TrimSpace(cleaned[:cut])
		}
		if len([]rune(cleaned)) > 120 {
			cleaned = string(runes[:120])
		}
	}
	if cleaned == "" {
		cleaned = trimmed
	}
	return cleaned
}

// collectSearchCandidateIDs 从搜索页 HTML 中收集去重后的种子 ID 候选。
// 参数/返回：html 为搜索页文本；返回最多 5 个种子 ID。
// 失败场景：不适用（无匹配时返回空列表）。
// 副作用：无。
func collectSearchCandidateIDs(html string) []string {
	matches := reSearchPageDownload.FindAllStringSubmatch(html, -1)
	ids := make([]string, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		id := strings.TrimSpace(match[1])
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= torrentURLResolveMaxCandidates {
			break
		}
	}
	return ids
}

// resolveFailPayload 构造反查失败响应负载。
// 参数/返回：message 为失败原因；返回标准失败负载。
// 失败场景：不适用。
// 副作用：无。
func resolveFailPayload(message string) map[string]any {
	return map[string]any{"success": false, "message": message}
}

// toStringValue 从 map 中读取字符串值（nil 安全）。
// 参数/返回：value 为任意值，空值返回空串。
// 失败场景：不适用。
// 副作用：无。
func toStringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", typed))
	}
}
