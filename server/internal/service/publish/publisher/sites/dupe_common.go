package sites

import (
	"fmt"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	acquirefetch "github.com/pt-nexus/server/internal/service/acquire/fetch"
	publishdupe "github.com/pt-nexus/server/internal/service/publish/dupe"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// runSiteDupeCheck 是各站点 dupe 校验钩子的共用实现。
//
// 参数/返回：logModule 为日志模块名；input 为发布输入；formFields 为最终表单字段（已含站点映射值）。
// 返回过程日志与错误。
//
// 判定维度由「站点设置里该媒介的规则」决定（dupe_rules）：
//   - 规则按待发布种子的标准媒介（standardized_params.medium）精确匹配；
//   - 未在站点设置里添加过的媒介**不做校验**，避免用户没配就被拦；
//   - 勾选的维度分两类：文件大小 / 制作组由客户端比对，媒介 / 分辨率 / 视频编码 / 音频编码
//     换算成站点检索筛选参数交给站点筛。
//
// 失败场景：命中重复时返回 *publisher.PreCheckError（确定性拒绝，上层改判跳过）；
//
//	检索失败（Cookie 失效 / 站点限流 / 网络异常）时返回普通 error（上层走重试）——
//	这两类语义不可混：把「查不到」当成「不重复」会漏过真正的 dupe。
//
// 副作用：可能向目标站点发起 1~2 次 GET 请求（仅在开关开启、站点已声明能力且该媒介配了规则时）。
func runSiteDupeCheck(logModule string, input publisher.PublishInput, formFields map[string]string) (string, error) {
	settings := publishdupe.ResolveSiteSettings(input.TargetInfo)
	supportsDupe := publishdupe.SiteSupportsDupe(input.SiteCode)

	// 站点压根没声明 dupe 能力：只写服务端日志，不往发布日志里塞信息
	//（绝大多数站点都属于这一类，避免刷屏）。
	if !supportsDupe {
		logx.Infof(logModule, "site=%s dupe 校验未执行：该站点未声明 dupe 校验能力", strings.TrimSpace(input.SiteCode))
		return "", nil
	}

	// 站点支持但开关没开：在发布日志里留一行，否则事后无法区分
	//「查过没重复」与「压根没查」——这正是排查时最容易踩的坑。
	if !settings.Enabled {
		detail := "dupe 校验：未启用（可在「站点设置」中开启后生效）"
		logx.Infof(logModule, "site=%s %s", strings.TrimSpace(input.SiteCode), detail)
		return detail, nil
	}

	// 用户已在前端对该站点的 dupe 拦截点了「仍要发布」：跳过查重，但要留可见痕迹，
	// 避免事后看不出这次是「确认过重复、仍强制发布」。
	if input.SkipDupeCheck {
		detail := "dupe 校验：已按你的确认跳过查重，强制发布（本次命中重复也会继续上传）"
		logx.Warnf(logModule, "site=%s 用户确认强制发布，跳过 dupe 校验", strings.TrimSpace(input.SiteCode))
		return detail, nil
	}

	// 按待发布种子的标准媒介取规则；没有单独配置的媒介走兜底规则（未开启兜底则跳过）。
	medium := publishdupe.SeedMediumFromPayload(input.UploadData)
	dims, ruleKey, hasRule := settings.MatchRule(medium)
	if !hasRule {
		shown := medium
		if strings.TrimSpace(shown) == "" {
			shown = "未知"
		}
		detail := fmt.Sprintf("dupe 校验：该媒介（%s）未配置查重规则且未开启兜底规则，已跳过（可在「站点设置」里为该媒介添加规则或开启兜底）", shown)
		logx.Infof(logModule, "site=%s medium=%s %s", strings.TrimSpace(input.SiteCode), medium, detail)
		return detail, nil
	}
	usingFallback := ruleKey == publishdupe.DupeFallbackMedium

	matchSize, matchTeam := publishdupe.ClientDimensions(dims)

	// 体积只在规则勾选了「文件大小」时才需要 —— 没勾就不必读种子文件。
	var torrentSize int64
	if matchSize {
		resolved, err := resolveTorrentTotalSize(input)
		if err != nil {
			detail := fmt.Sprintf("dupe 校验未执行：%v", err)
			logx.Warnf(logModule, "site=%s %s", strings.TrimSpace(input.SiteCode), detail)
			return detail, nil
		}
		torrentSize = resolved
	}

	query := publishdupe.Query{
		BaseURL:            input.BaseURL,
		Cookie:             input.Cookie,
		DoubanID:           doubanLinkFromUploadData(input.UploadData, input.DoubanLink),
		IMDbID:             input.IMDbLink,
		TMDbID:             tmdbLinkFromUploadData(input.UploadData),
		Title:              strings.TrimSpace(input.Title),
		TorrentSizeBytes:   torrentSize,
		SizeToleranceBytes: settings.SizeToleranceBytes,
		MatchSize:          matchSize,
		MatchTeam:          matchTeam,
		Dimensions:         dims,
	}

	// 规则勾选了站点并不支持的筛选维度时，该维度实际不会生效。必须显式提示，
	// 否则用户会以为「勾了就一定比过」，而实际是漏检（拦不住重复）。
	preDetails := make([]string, 0, 3)
	if unsupported := unsupportedFilterDimensions(input.SiteCode, dims); len(unsupported) > 0 {
		preDetails = append(preDetails, fmt.Sprintf(
			"⚠️ dupe 规则勾选了「%s」，但站点未声明对应检索参数，该维度本次无法生效",
			strings.Join(publishdupe.DupeDimensionLabels(unsupported), "、")))
	}

	// 筛选参数：部分站点（如家园）的维度映射是「上传页选项索引」，需先换算成真实选项值，
	// 这样媒介 / 编码 / 分辨率等维度才能参与检索（换算失败的维度会被跳过，不影响判定正确性）。
	filters, filterDetail := publishdupe.PrepareSearchFilters(input.SiteCode, query, formFields, dims)
	query.Filters = filters
	if trimmed := strings.TrimSpace(filterDetail); trimmed != "" {
		preDetails = append(preDetails, trimmed)
	}
	preDetails = append(preDetails, fmt.Sprintf("dupe 规则：%s 判定维度=%s", ruleScopeLabel(medium, ruleKey, usingFallback),
		strings.Join(publishdupe.DupeDimensionLabels(dims), "、")))

	result, detail, checkErr := publishdupe.CheckBySite(input.SiteCode, query)
	// 把规则与筛选换算说明并进过程日志，便于在「发布进度 → 日志」里看清这次到底比了哪些维度。
	if len(preDetails) > 0 {
		prefix := strings.Join(preDetails, "\n")
		if detail == "" {
			detail = prefix
		} else {
			detail = prefix + "\n" + detail
		}
	}
	if detail != "" {
		logx.Infof(logModule, "site=%s\n%s", strings.TrimSpace(input.SiteCode), detail)
	}
	if checkErr != nil {
		return detail, checkErr
	}
	if result.IsDupe() && result.Matched != nil {
		reason := result.Reason
		if strings.TrimSpace(reason) == "" {
			reason = publishdupe.DescribeDupeMatch(query, *result.Matched, input.BaseURL)
		}
		// 把检索 URL、候选数等判定过程一并带出，便于在发布日志里回溯「为什么判为重复」；
		// 同时回传结构化信息，让前端能把「查重地址 / 重复种子」渲染成可点击链接。
		meta := map[string]any{
			publisher.PreCheckMetaDupeBlocked:    true,
			publisher.PreCheckMetaDupeSearchURL:  strings.TrimSpace(result.SearchURL),
			publisher.PreCheckMetaDupeTorrentURL: strings.TrimSpace(result.MatchedURL),
		}
		return detail, publisher.NewPreCheckErrorWithMeta(reason, detail, meta)
	}
	return detail, nil
}

// ruleScopeLabel 生成「本次用的是哪条规则」的日志片段。
// 参数/返回：medium 为待发布种子的标准媒介；ruleKey 为命中的规则键；usingFallback 表示是否走兜底。
// 说明：区分「该媒介单独配了规则」与「走兜底」很关键 —— 事后排查时能一眼看出
// 是规则配错了，还是压根没配、被兜底接手了。
//
// 规则按 6 类媒介归组（见 dupe/medium_groups.go），种子的细分媒介（如 medium.uhd_remux）
// 会命中组键 medium.remux。两者不一致时把原始媒介一并写出来，避免看日志的人
// 误以为「我配的是 Remux，怎么种子的媒介变成 medium.remux 了」。
// 副作用：无。
func ruleScopeLabel(medium string, ruleKey string, usingFallback bool) string {
	seedMedium := strings.TrimSpace(medium)
	if usingFallback {
		shown := seedMedium
		if shown == "" {
			shown = "未知"
		}
		return fmt.Sprintf("兜底规则（媒介 %s 未单独配置）", shown)
	}
	key := strings.TrimSpace(ruleKey)
	if seedMedium != "" && seedMedium != key {
		return fmt.Sprintf("媒介=%s（种子 %s 归入该组）", key, seedMedium)
	}
	return fmt.Sprintf("媒介=%s", key)
}

// unsupportedFilterDimensions 返回规则勾选、但站点未声明检索参数的筛选维度。
// 参数/返回：siteCode 为站点标识；dims 为规则勾选的维度；返回不受支持的筛选维度。
// 副作用：加载站点配置（有缓存）。
func unsupportedFilterDimensions(siteCode string, dims []string) []string {
	filterDims := publishdupe.FilterDimensions(dims)
	if len(filterDims) == 0 {
		return nil
	}
	available := map[string]struct{}{}
	for _, dimension := range publishdupe.FilterDimensionsForSite(siteCode) {
		available[dimension] = struct{}{}
	}
	unsupported := make([]string, 0, len(filterDims))
	for _, dimension := range filterDims {
		if _, ok := available[dimension]; !ok {
			unsupported = append(unsupported, dimension)
		}
	}
	return unsupported
}

// resolveTorrentTotalSize 取待发布种子的载荷总体积。
// 参数/返回：input 为发布输入；返回体积（字节）与错误。
// 失败场景：种子路径为空或文件解析失败时返回错误。
// 副作用：读取磁盘上的 torrent 文件。
func resolveTorrentTotalSize(input publisher.PublishInput) (int64, error) {
	if size := publishdupe.TorrentTotalSizeFromPayload(input.UploadData); size > 0 {
		return size, nil
	}
	torrentPath := strings.TrimSpace(input.TorrentPath)
	if torrentPath == "" {
		return 0, fmt.Errorf("缺少种子文件路径，无法确定体积")
	}
	size, err := acquirefetch.ExtractTorrentTotalSizeFromFile(torrentPath)
	if err != nil {
		return 0, fmt.Errorf("读取种子体积失败: %v", err)
	}
	return size, nil
}

// doubanLinkFromUploadData 从发布参数中取豆瓣链接（兼容多种字段写法）。
func doubanLinkFromUploadData(uploadData map[string]any, fallback string) string {
	candidates := []string{"douban_link", "doubanLink", "douban", "pt_gen", "ptgen"}
	if value := firstUploadDataString(uploadData, candidates); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

// tmdbLinkFromUploadData 从发布参数中取 TMDb 链接（仅用于日志展示，站点检索不依赖它）。
func tmdbLinkFromUploadData(uploadData map[string]any) string {
	return firstUploadDataString(uploadData, []string{"tmdb_link", "tmdbLink", "tmdb"})
}

func firstUploadDataString(uploadData map[string]any, keys []string) string {
	if uploadData == nil {
		return ""
	}
	for _, key := range keys {
		if value := strings.TrimSpace(toStringAny(uploadData[key], "")); value != "" {
			return value
		}
	}
	if standardized, ok := uploadData["standardized_params"].(map[string]any); ok && standardized != nil {
		for _, key := range keys {
			if value := strings.TrimSpace(toStringAny(standardized[key], "")); value != "" {
				return value
			}
		}
	}
	return ""
}

// AttachSiteDupeCheck 在站点声明了 dupe 能力、且调用方尚未提供校验钩子时，挂上通用 dupe 校验钩子。
//
// 参数/返回：input 为发布输入；返回可能带上 BeforeUpload 的发布输入。
// 用途：并非每个站点都有专属发布器（如彩虹岛走通用 public 发布器），
// 这些站点没有地方实现 publicSiteBeforeUpload 接口，由本函数在分发前统一挂载。
// 已自带钩子的站点（人人/幸运/家园/猫站/我堡）不受影响——它们会在
// publishWithPublicSite 内用自己的实现覆盖，且此处只有在 BeforeUpload 为空时才介入。
// 副作用：无（仅修改入参副本）。
func AttachSiteDupeCheck(input publisher.PublishInput) publisher.PublishInput {
	if input.BeforeUpload != nil {
		return input
	}
	siteCode := strings.TrimSpace(input.SiteCode)
	if !publishdupe.SiteSupportsDupe(siteCode) {
		return input
	}
	logModule := publishdupe.LogModuleForSite(siteCode)
	if logModule == "" {
		logModule = defaultDupeLogModule
	}
	// 复制一份再闭包，避免闭包内引用被赋值的 input 自身。
	captured := input
	input.BeforeUpload = func(formFields map[string]string) (string, error) {
		return runSiteDupeCheck(logModule, captured, formFields)
	}
	return input
}

// defaultDupeLogModule 为未在 dupe 包登记日志模块名时的兜底。
const defaultDupeLogModule = "发布-dupe校验"
