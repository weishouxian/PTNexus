package dupe

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/platform/netproxy"
	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

// 共用检索骨架的默认参数。
const (
	sharedRequestTimeout = 60 * time.Second
	sharedBodyLimit      = 4 * 1024 * 1024
)

// searchPlan 描述一次站点检索如何发起。
// 站点差异全部收敛在这里：人人站只有一种检索方式，幸运站需要「IMDb 优先 + 标题兜底」两段。
type searchPlan struct {
	// Label 为该段检索的中文说明，用于日志（如「IMDb ID 检索」「标题兜底检索」）。
	Label string
	// Search 为搜索关键字；为空表示该段检索不可用。
	Search string
	// SearchArea 为站点搜索范围枚举（人人站 2；幸运站 4=IMDb / 0=标题）。
	SearchArea string
	// SearchAreaParam 为搜索范围参数名，来自站点 YAML dupe_check.search_area_param（默认 search_area）。
	SearchAreaParam string
	// SearchPath 为搜索页路径，来自站点 YAML dupe_check.search_path（默认 torrents.php）。
	SearchPath string
	// Filters 为站点筛选参数。
	Filters map[string]string
}

// siteFetcher 抽象「抓搜索页 + 解析候选」，供各站点实现。
type siteFetcher func(query Query, searchURL string) ([]Candidate, error)

// checkOutcome 汇总一次 dupe 校验的结论。
type checkOutcome struct {
	Result Result
	Detail string
	Err    error
}

// runSearchPlans 按计划依次执行检索并汇总判定。
// 参数/返回：siteCode 为站点标识（用于读取检索端点配置）；query 为检索输入；plans 为检索计划；
// fetch 负责抓取与解析；logModule 为日志模块名。返回命中结论、过程日志与错误。
// 失败场景：任一段检索失败即整体失败（返回 error，调用方按「可重试」处理）——
// 检索失败绝不能当成「无重复」放行，否则会漏过真正的 dupe。
// 副作用：向目标站点发起 GET 请求。
func runSearchPlans(siteCode string, query Query, plans []searchPlan, fetch siteFetcher, logModule string) checkOutcome {
	detailLines := make([]string, 0, 6)
	appendDetail := func(format string, args ...any) {
		detailLines = append(detailLines, fmt.Sprintf(format, args...))
	}

	// 用站点配置补全搜索页路径与范围参数名（未配置时用 NexusPHP 默认值）。
	for idx := range plans {
		plans[idx] = applySiteSearchEndpoint(siteCode, plans[idx])
	}

	if query.TorrentSizeBytes <= 0 {
		appendDetail("dupe 校验跳过：无法确定待发布种子体积")
		return checkOutcome{Detail: strings.Join(detailLines, "\n")}
	}
	trimmedBase := strings.TrimRight(strings.TrimSpace(query.BaseURL), "/")
	if trimmedBase == "" {
		appendDetail("dupe 校验跳过：目标站点缺少 base_url")
		return checkOutcome{Detail: strings.Join(detailLines, "\n")}
	}

	executed := 0
	for _, plan := range plans {
		search := strings.TrimSpace(plan.Search)
		if search == "" {
			continue
		}
		searchURL := buildSearchURL(trimmedBase, plan, search, plan.Filters)
		if searchURL == "" {
			continue
		}
		executed++
		if executed > 1 {
			appendDetail("dupe 追加%s（上一段未命中）", plan.Label)
		} else {
			appendDetail("dupe %s: 关键字=%s URL=%s", plan.Label, search, searchURL)
		}

		candidates, err := fetch(query, searchURL)
		if err != nil {
			appendDetail("dupe 检索失败: %v", err)
			return checkOutcome{Detail: strings.Join(detailLines, "\n"), Err: err}
		}

		// ⚠️ 筛选参数一旦与站点实际不符，部分站点会返回 0 条（而不是忽略该参数）。
		// 若就此判定「无重复」，会把「筛错了」当成「查不到」而放行真正的重复。
		// 因此带筛选却 0 候选时，去掉筛选再查一次；命中则以无筛选那一次为准。
		if len(candidates) == 0 && len(plan.Filters) > 0 {
			retryURL := buildSearchURL(trimmedBase, plan, search, nil)
			appendDetail("dupe 带筛选检索为 0 条，去掉筛选重试: %s", retryURL)
			retryCandidates, retryErr := fetch(query, retryURL)
			if retryErr != nil {
				appendDetail("dupe 去掉筛选重试失败: %v", retryErr)
				return checkOutcome{Detail: strings.Join(detailLines, "\n"), Err: retryErr}
			}
			appendDetail("dupe 去掉筛选后命中候选 %d 条", len(retryCandidates))
			candidates = retryCandidates
			searchURL = retryURL
		} else {
			appendDetail("dupe 检索命中候选 %d 条", len(candidates))
		}

		for idx := range candidates {
			candidate := candidates[idx]
			if !TeamKeysMatch(query.Title, candidate.Title) {
				continue
			}
			if !SizeWithinTolerance(query.TorrentSizeBytes, candidate.SizeBytes, query.SizeToleranceBytes) {
				continue
			}
			appendDetail("dupe 判定命中: %s", DescribeDupeMatch(query, candidate, query.BaseURL))
			logx.Infof(logModule, "命中 dupe keyword=%s candidate=%s", search, candidate.Title)
			return checkOutcome{
				Result: Result{
					Matched:    &candidate,
					SearchURL:  searchURL,
					MatchedURL: buildTorrentDetailURL(trimmedBase, candidate.TorrentID),
				},
				Detail: strings.Join(detailLines, "\n"),
			}
		}
	}

	if executed == 0 {
		appendDetail("dupe 校验跳过：没有可用于检索的外部 ID 或标题")
		return checkOutcome{Detail: strings.Join(detailLines, "\n")}
	}
	appendDetail("dupe 判定：未发现重复（制作组相同且体积差在容差内的候选不存在）")
	return checkOutcome{Detail: strings.Join(detailLines, "\n")}
}

// buildSearchURL 按站点配置拼接 NexusPHP 风格搜索 URL。
// 参数/返回：baseURL 为站点根地址；plan 提供搜索页路径与范围参数名；search 为关键字；
// filters 为筛选参数（传 nil 表示不带筛选）。返回完整 URL；参数不足时返回空串。
// 副作用：无。
func buildSearchURL(baseURL string, plan searchPlan, search string, filters map[string]string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	trimmedSearch := strings.TrimSpace(search)
	if trimmedBase == "" || trimmedSearch == "" {
		return ""
	}
	searchPath := strings.Trim(strings.TrimSpace(plan.SearchPath), "/")
	if searchPath == "" {
		searchPath = defaultDupeSearchPath
	}
	areaParam := strings.TrimSpace(plan.SearchAreaParam)
	if areaParam == "" {
		areaParam = defaultDupeSearchAreaParam
	}

	values := map[string]string{
		"search":         trimmedSearch,
		areaParam:        strings.TrimSpace(plan.SearchArea),
		"search_mode":    "0",
		"incldead":       "0",
		"spstate":        "0",
		"inclbookmarked": "0",
		"mytorrent":      "0",
	}
	for key, value := range filters {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		values[trimmedKey] = trimmedValue
	}
	return appendQuery(trimmedBase+"/"+searchPath, values)
}

// 各 NexusPHP 站点的默认搜索页路径与搜索范围参数名（站点 YAML 未声明时使用）。
const (
	defaultDupeSearchPath      = "torrents.php"
	defaultDupeSearchAreaParam = "search_area"
)

// applySiteSearchEndpoint 用站点 YAML 的 dupe_check 配置补全检索端点信息。
// 参数/返回：siteCode 为站点标识；plan 为待补全的检索计划；返回补全后的计划。
// 说明：已接入站点都用 torrents.php + search_area，默认值即正确值；
// 这里读配置是为了让 YAML 里声明的 search_path / search_area_param 真正生效，
// 避免出现「配了却不读」的死配置。
// 副作用：加载站点配置（有缓存）。
func applySiteSearchEndpoint(siteCode string, plan searchPlan) searchPlan {
	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil {
		return plan
	}
	if path := strings.TrimSpace(siteCfg.DupeCheck.SearchPath); path != "" {
		plan.SearchPath = path
	}
	if param := strings.TrimSpace(siteCfg.DupeCheck.SearchAreaParam); param != "" {
		plan.SearchAreaParam = param
	}
	return plan
}

// httpGetText 发起一次搜索页 GET 请求并返回正文。
// 参数/返回：query 为检索输入（提供 base_url/cookie/user-agent）；searchURL 为完整 URL。
// 返回响应正文与错误。
// 失败场景：请求失败、响应状态异常时返回错误。
// 副作用：向目标站点发起 GET 请求。
func httpGetText(query Query, searchURL string) (string, error) {
	client := newDupeHTTPClient()
	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("创建 dupe 检索请求失败: %w", err)
	}
	req.Header.Set("User-Agent", firstNonEmptyValue(query.UserAgent, defaultDupeUserAgent))
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,zh-TW;q=0.8,zh-HK;q=0.7,en-US;q=0.6,en;q=0.5")
	req.Header.Set("Referer", strings.TrimRight(strings.TrimSpace(query.BaseURL), "/")+"/torrents.php")
	if cookie := strings.TrimSpace(query.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("dupe 检索请求失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, sharedBodyLimit))
	if err != nil {
		return "", fmt.Errorf("读取 dupe 检索响应失败: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("dupe 检索响应状态异常: %d", resp.StatusCode)
	}
	return string(body), nil
}

// rejectUnexpectedPage 拦截「非预期页面」，避免把异常页面当成「无重复」。
// 参数/返回：body 为响应正文；marker 为该站正常结果页必须包含的标记（正则）。
// 失败场景：命中登录页、人机验证页或缺少结果表时返回错误。
// 副作用：无。
func rejectUnexpectedPage(body string, resultTableMarker string, loginMarkers []string) error {
	if looksLikeLoginPage(body, loginMarkers) {
		return fmt.Errorf("dupe 检索被重定向到登录页，请检查目标站点 Cookie 是否有效")
	}
	// 站点限流常以 HTTP 200 返回提示页（无任何结果行）。这种页面必须当失败处理：
	// 若按「无结果」返回，会把检索失败误判成「不存在重复」而放行发布。
	if looksLikeVerificationError(body) {
		return fmt.Errorf("dupe 检索被站点限流（HTTP 200 但返回人机验证/访问限制提示页），无法确认是否重复；请降低发种频率或稍后重试")
	}
	if resultTableMarker != "" && !strings.Contains(body, resultTableMarker) {
		return fmt.Errorf("dupe 检索返回的页面不含结果列表，无法确认是否重复（可能是站点返回了非预期页面）")
	}
	return nil
}

// defaultDupeUserAgent 为检索请求的默认 UA，与浏览器一致，避免被判定为异常客户端。
const defaultDupeUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:157.0) Gecko/20100101 Firefox/157.0"

// looksLikeVerificationError 判断响应是否为限流/人机验证提示页。
func looksLikeVerificationError(body string) bool {
	limit := len(body)
	if limit > 8192 {
		limit = 8192
	}
	sample := body[:limit]
	for _, marker := range []string{
		"人机验证", "验证未通过", "Access Denied", "访问过于频繁", "请求过于频繁",
		"Just a moment", "Checking your browser", "cf_chl", "Attention Required",
	} {
		if strings.Contains(sample, marker) {
			return true
		}
	}
	return false
}

// looksLikeLoginPage 判断响应是否为登录页。
func looksLikeLoginPage(body string, extraMarkers []string) bool {
	limit := len(body)
	if limit > 8192 {
		limit = 8192
	}
	sample := body[:limit]
	lower := strings.ToLower(sample)
	if !strings.Contains(lower, "<html") && !strings.HasPrefix(strings.TrimSpace(lower), "<!doctype") {
		return false
	}
	markers := append([]string{`name="username"`, "login.php?returnto=", `action="login.php"`}, extraMarkers...)
	for _, marker := range markers {
		if strings.Contains(lower, strings.ToLower(marker)) {
			return true
		}
	}
	return false
}

func newDupeHTTPClient() *http.Client {
	transport := &http.Transport{
		DialContext:         (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        32,
		IdleConnTimeout:     60 * time.Second,
		TLSHandshakeTimeout: 30 * time.Second,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
	}
	netproxy.ConfigureTransport(transport)
	return &http.Client{
		Timeout:   sharedRequestTimeout,
		Transport: transport,
	}
}
