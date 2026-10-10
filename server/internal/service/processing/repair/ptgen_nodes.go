package repair

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/platform/logx"
)

// PTGenNodeSetting 表示单个 PTGen 节点的启停配置；切片顺序即节点优先级。
type PTGenNodeSetting struct {
	ID      string
	Enabled bool
}

// PTGenNodeDefinition 描述内置 PTGen 节点的元数据与请求地址构建规则。
// BuildURL 的第二个返回值非空时表示该节点本次不可用（例如缺少 Token 或缺少豆瓣 ID）。
type PTGenNodeDefinition struct {
	ID             string
	Name           string
	Description    string
	NeedToken      bool
	DefaultEnabled bool
	BuildURL       func(resourceURL, doubanID, csptToken string) (string, string)
}

// PTGenNodeTestResult 描述单个节点的检测结果。
type PTGenNodeTestResult struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	NeedToken       bool   `json:"need_token"`
	Enabled         bool   `json:"enabled"`
	Status          string `json:"status"`
	URL             string `json:"url"`
	HTTPStatus      int    `json:"http_status"`
	Method          string `json:"method"`
	ElapsedMS       int64  `json:"elapsed_ms"`
	Error           string `json:"error"`
	ResponsePreview string `json:"response_preview"`
	FormatLength    int    `json:"format_length"`
	PosterURL       string `json:"poster_url"`
	IntroPreview    string `json:"intro_preview"`
	IMDb            string `json:"imdb"`
	Douban          string `json:"douban"`
	TMDb            string `json:"tmdb"`
}

// 检测状态取值：success 成功 / failed 失败 / skipped 跳过（缺少必要参数）。
const (
	PTGenTestStatusSuccess = "success"
	PTGenTestStatusFailed  = "failed"
	PTGenTestStatusSkipped = "skipped"
)

var ptgenDoubanIDPattern = regexp.MustCompile(`\d{5,}`)

// builtinPTGenNodeDefinitions 是内置 PTGen 节点清单，切片顺序即默认优先级。
// 默认启停基于生产环境实测结论：dpdns 域名已失效，homeqian/iyuu/tju 长期返回异常。
var builtinPTGenNodeDefinitions = []PTGenNodeDefinition{
	{
		ID:             "cspt",
		Name:           "财神 PTGen",
		Description:    "cspt.top 官方接口，需在「转种设置」中配置 API Token（每日 100+ 次，随等级提升）",
		NeedToken:      true,
		DefaultEnabled: true,
		BuildURL: func(resourceURL, _ string, csptToken string) (string, string) {
			token := strings.TrimSpace(csptToken)
			if token == "" {
				return "", "未配置财神 PTGen API Token"
			}
			return fmt.Sprintf(
				"https://cspt.top/api/ptgen/query/%s?url=%s",
				neturl.PathEscape(token),
				neturl.QueryEscape(resourceURL),
			), ""
		},
	},
	{
		ID:             "workers",
		Name:           "PT-Nexus 自建 (Workers)",
		Description:    "Cloudflare Workers 部署的 PTGen 实例，海外网络访问稳定",
		DefaultEnabled: true,
		BuildURL: func(resourceURL, _ string, _ string) (string, string) {
			return fmt.Sprintf("https://pt-nexus-ptgen.1395251710.workers.dev/api?url=%s", neturl.QueryEscape(resourceURL)), ""
		},
	},
	{
		ID:             "dpdns",
		Name:           "PT-Nexus 自建 (dpdns)",
		Description:    "原 sqing33.dpdns.org 域名已失效，DNS 解析不存在",
		DefaultEnabled: false,
		BuildURL: func(resourceURL, _ string, _ string) (string, string) {
			return fmt.Sprintf("https://pt-nexus-ptgen.sqing33.dpdns.org/api?url=%s", neturl.QueryEscape(resourceURL)), ""
		},
	},
	{
		ID:             "homeqian",
		Name:           "HomeQian PTGen",
		Description:    "第三方公益节点，接口常返回内部错误",
		DefaultEnabled: false,
		BuildURL: func(resourceURL, _ string, _ string) (string, string) {
			return fmt.Sprintf("https://ptgen.homeqian.top/?url=%s", neturl.QueryEscape(resourceURL)), ""
		},
	},
	{
		ID:             "iyuu",
		Name:           "IYUU PTGen",
		Description:    "第三方公益节点，常见返回「未查询到数据」",
		DefaultEnabled: false,
		BuildURL: func(resourceURL, _ string, _ string) (string, string) {
			return fmt.Sprintf("https://api.iyuu.cn/App.Movie.Ptgen?url=%s", neturl.QueryEscape(resourceURL)), ""
		},
	},
	{
		ID:             "tju",
		Name:           "TJU PTGen",
		Description:    "天津大学公益节点，仅支持豆瓣 ID，近期返回 502",
		DefaultEnabled: false,
		BuildURL: func(_, doubanID string, _ string) (string, string) {
			id := strings.TrimSpace(doubanID)
			if id == "" {
				return "", "该节点仅支持豆瓣 ID，缺少豆瓣 ID"
			}
			return fmt.Sprintf("https://ptgen.tju.pt/infogen?site=douban&sid=%s", neturl.QueryEscape(id)), ""
		},
	},
}

// ResolvePTGenNodeSettings 解析 cross_seed.ptgen_nodes 配置，返回全量节点启停列表。
// 参数/返回：raw 为配置中的节点数组（元素含 id/enabled）；返回顺序即优先级，配置中缺失的内置节点按默认值追加到末尾。
// 失败场景：raw 非法时退化为全默认配置。
// 副作用：无。
func ResolvePTGenNodeSettings(raw any) []PTGenNodeSetting {
	type parsedSetting struct {
		id      string
		enabled bool
	}
	parsed := make([]parsedSetting, 0)
	seen := map[string]struct{}{}
	for _, item := range ptgenAnySlice(raw) {
		node, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id := strings.TrimSpace(toStringAny(node["id"], ""))
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		if _, exists := ptGenNodeDefinitionByID(id); !exists {
			continue
		}
		seen[id] = struct{}{}
		parsed = append(parsed, parsedSetting{id: id, enabled: toBoolAny(node["enabled"])})
	}

	result := make([]PTGenNodeSetting, 0, len(builtinPTGenNodeDefinitions))
	for _, item := range parsed {
		result = append(result, PTGenNodeSetting{ID: item.id, Enabled: item.enabled})
	}
	// 配置中未涉及的内置节点按默认优先级与默认启停补齐，保证新增节点自动生效。
	for _, def := range builtinPTGenNodeDefinitions {
		if _, exists := seen[def.ID]; exists {
			continue
		}
		result = append(result, PTGenNodeSetting{ID: def.ID, Enabled: def.DefaultEnabled})
	}
	return result
}

// EnrichPTGenNodeSettings 将配置中的节点数组补全为带元数据的完整列表，供前端展示。
func EnrichPTGenNodeSettings(raw any) []any {
	resolved := ResolvePTGenNodeSettings(raw)
	result := make([]any, 0, len(resolved))
	for index, item := range resolved {
		def, ok := ptGenNodeDefinitionByID(item.ID)
		if !ok {
			continue
		}
		result = append(result, map[string]any{
			"id":              def.ID,
			"name":            def.Name,
			"description":     def.Description,
			"need_token":      def.NeedToken,
			"default_enabled": def.DefaultEnabled,
			"enabled":         item.Enabled,
			"order":           index + 1,
		})
	}
	return result
}

// NormalizePTGenNodeSettingsPayload 校验并归一前端提交的节点配置，仅保留 id 与 enabled。
func NormalizePTGenNodeSettingsPayload(raw any) []any {
	resolved := ResolvePTGenNodeSettings(raw)
	result := make([]any, 0, len(resolved))
	for _, item := range resolved {
		result = append(result, map[string]any{"id": item.ID, "enabled": item.Enabled})
	}
	return result
}

// PTGenNodeSettingsFromRootConfig 从根配置中解析 PTGen 节点启停配置。
func PTGenNodeSettingsFromRootConfig(rootConfig map[string]any) []PTGenNodeSetting {
	if rootConfig == nil {
		return DefaultPTGenNodeSettings()
	}
	crossSeed, ok := rootConfig["cross_seed"].(map[string]any)
	if !ok {
		return DefaultPTGenNodeSettings()
	}
	return ResolvePTGenNodeSettings(crossSeed["ptgen_nodes"])
}

// DefaultPTGenNodeSettings 返回内置节点的默认启停配置。
func DefaultPTGenNodeSettings() []PTGenNodeSetting {
	return ResolvePTGenNodeSettings(nil)
}

// PTGenCandidate 表示一次可用的 PTGen 请求目标。
type PTGenCandidate struct {
	NodeID   string
	NodeName string
	URL      string
}

// BuildPTGenCandidates 按启停配置与优先级构建 PTGen 候选请求列表。
// 参数/返回：resourceURL 为待查询资源链接；doubanURL 用于提取豆瓣 ID；csptToken 为财神令牌；settings 为节点启停配置。
// 失败场景：resourceURL 为空或全部节点不可用时返回空列表。
// 副作用：无。
func BuildPTGenCandidates(resourceURL, doubanURL, csptToken string, settings []PTGenNodeSetting) []PTGenCandidate {
	resource := strings.TrimSpace(resourceURL)
	if resource == "" {
		return nil
	}
	if len(settings) == 0 {
		settings = DefaultPTGenNodeSettings()
	}
	doubanID := extractDoubanID(doubanURL)

	candidates := make([]PTGenCandidate, 0, len(settings))
	seen := map[string]struct{}{}
	for _, setting := range settings {
		if !setting.Enabled {
			continue
		}
		def, ok := ptGenNodeDefinitionByID(setting.ID)
		if !ok || def.BuildURL == nil {
			continue
		}
		target, reason := def.BuildURL(resource, doubanID, csptToken)
		target = strings.TrimSpace(target)
		if target == "" {
			if reason != "" {
				logPTGenNodeSkip(def, reason)
			}
			continue
		}
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		candidates = append(candidates, PTGenCandidate{NodeID: def.ID, NodeName: def.Name, URL: target})
	}
	return candidates
}

// TestPTGenNodes 并发检测全部内置节点对指定豆瓣 ID 的可用性。
// 参数/返回：doubanID 为豆瓣 ID；csptToken 为财神令牌；rawSettings 为当前节点启停配置（仅用于回显 enabled）。
// 失败场景：豆瓣 ID 为空时所有节点返回 skipped。
// 副作用：会对每个节点发起一次真实外部请求（含已停用节点，便于确认其是否恢复）。
func TestPTGenNodes(doubanID, csptToken string, rawSettings any) []PTGenNodeTestResult {
	normalizedID := NormalizeDoubanIDInput(doubanID)
	resourceURL := ""
	if normalizedID != "" {
		resourceURL = fmt.Sprintf("https://movie.douban.com/subject/%s/", normalizedID)
	}

	enabledByID := map[string]bool{}
	for _, item := range ResolvePTGenNodeSettings(rawSettings) {
		enabledByID[item.ID] = item.Enabled
	}

	results := make([]PTGenNodeTestResult, len(builtinPTGenNodeDefinitions))
	var waitGroup sync.WaitGroup
	for index, def := range builtinPTGenNodeDefinitions {
		result := PTGenNodeTestResult{
			ID:          def.ID,
			Name:        def.Name,
			Description: def.Description,
			NeedToken:   def.NeedToken,
			Enabled:     enabledByID[def.ID],
		}
		if resourceURL == "" {
			result.Status = PTGenTestStatusSkipped
			result.Error = "缺少有效的豆瓣 ID"
			results[index] = result
			continue
		}
		if def.BuildURL == nil {
			result.Status = PTGenTestStatusSkipped
			result.Error = "节点未配置请求地址"
			results[index] = result
			continue
		}
		target, reason := def.BuildURL(resourceURL, normalizedID, csptToken)
		target = strings.TrimSpace(target)
		if target == "" {
			result.Status = PTGenTestStatusSkipped
			result.Error = firstNonEmpty(reason, "节点当前不可用")
			results[index] = result
			continue
		}
		result.URL = target
		results[index] = result

		waitGroup.Add(1)
		go func(idx int, nodeID, requestURL string) {
			defer waitGroup.Done()
			outcome := fetchPTGenOutcome(requestURL)
			item := results[idx]
			item.HTTPStatus = outcome.HTTPStatus
			item.Method = outcome.Method
			item.ElapsedMS = outcome.ElapsedMS
			item.ResponsePreview = outcome.ResponsePreview
			if strings.TrimSpace(outcome.Error) != "" {
				item.Status = PTGenTestStatusFailed
				item.Error = outcome.Error
			} else {
				item.Status = PTGenTestStatusSuccess
				poster, intro, imdb, douban, tmdb := parseFormatContent(outcome.Format, outcome.IMDb, outcome.Douban, outcome.TMDb)
				item.FormatLength = len([]rune(outcome.Format))
				item.PosterURL = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(poster), "[img]"), "[/img]")
				item.IntroPreview = CompactLogText(intro, 200)
				item.IMDb = imdb
				item.Douban = douban
				item.TMDb = tmdb
			}
			logx.Infof(
				ptgenLogModule,
				"节点检测完成 节点=%s(%s) 状态=%s HTTP=%d 耗时=%dms 错误=%s",
				item.Name,
				nodeID,
				item.Status,
				item.HTTPStatus,
				item.ElapsedMS,
				CompactLogText(item.Error, 160),
			)
			results[idx] = item
		}(index, def.ID, target)
	}
	waitGroup.Wait()

	return results
}

// NormalizeDoubanIDInput 从用户输入中提取豆瓣 ID，支持裸 ID 与完整链接。
func NormalizeDoubanIDInput(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if id := extractDoubanID(trimmed); id != "" {
		return id
	}
	return ptgenDoubanIDPattern.FindString(trimmed)
}

func ptGenNodeDefinitionByID(id string) (PTGenNodeDefinition, bool) {
	trimmed := strings.TrimSpace(id)
	for _, def := range builtinPTGenNodeDefinitions {
		if def.ID == trimmed {
			return def, true
		}
	}
	return PTGenNodeDefinition{}, false
}

func ptgenAnySlice(value any) []any {
	if typed, ok := value.([]any); ok {
		return typed
	}
	return nil
}

func logPTGenNodeSkip(def PTGenNodeDefinition, reason string) {
	logx.Debugf(ptgenLogModule, "跳过 PTGen 节点 节点=%s(%s) 原因=%s", def.Name, def.ID, reason)
}

// ptgenOutcome 描述一次 PTGen 请求的完整结果，含状态码与失败预览，供检测与主链路共用。
type ptgenOutcome struct {
	Format          string
	IMDb            string
	Douban          string
	TMDb            string
	HTTPStatus      int
	Method          string
	ElapsedMS       int64
	ResponsePreview string
	Error           string
}

// fetchPTGenOutcome 按候选节点依次尝试可用的请求方法，返回首个可用结果及诊断信息。
// 参数/返回：requestURL 为节点请求地址；返回格式化内容、外链、状态码、耗时与失败原因。
// 失败场景：所有方法均请求失败或响应无法解析时，Error 非空。
// 副作用：发起一次或多次外部网络请求。
func fetchPTGenOutcome(requestURL string) (outcome ptgenOutcome) {
	startedAt := time.Now()
	defer func() {
		outcome.ElapsedMS = time.Since(startedAt).Milliseconds()
	}()

	methods := preferredPTGenMethods(requestURL)
	errMessages := make([]string, 0, len(methods))
	for _, method := range methods {
		body, status, requestErr := FetchPageWithMethodStatus(requestURL, method)
		if status > 0 {
			outcome.HTTPStatus = status
		}
		outcome.Method = method
		if requestErr != nil {
			outcome.ResponsePreview = CompactLogText(strings.TrimSpace(body), 200)
			errMessages = append(errMessages, fmt.Sprintf("%s请求失败: %v", method, requestErr))
			if status == 0 {
				// 网络层错误（DNS 解析失败 / 超时 / 连接被拒），换请求方法不会改善，直接结束以免多等一轮超时。
				break
			}
			continue
		}

		format, imdb, douban, tmdb, parseErr := parsePTGenResponse(requestURL, method, body)
		if parseErr != nil {
			outcome.ResponsePreview = CompactLogText(strings.TrimSpace(body), 200)
			errMessages = append(errMessages, fmt.Sprintf("%s响应无效: %v", method, parseErr))
			continue
		}
		outcome.Format = format
		outcome.IMDb = imdb
		outcome.Douban = douban
		outcome.TMDb = tmdb
		return outcome
	}

	if len(errMessages) > 0 {
		outcome.Error = strings.Join(errMessages, " | ")
	} else {
		outcome.Error = "no request method available"
	}
	return outcome
}
