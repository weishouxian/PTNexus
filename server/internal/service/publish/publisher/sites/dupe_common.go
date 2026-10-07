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
// 参数/返回：logModule 为日志模块名；input 为发布输入；formFields 为最终表单字段（已含站点映射值）。
// 返回过程日志与错误。
// 失败场景：命中重复时返回 *publisher.PreCheckError（确定性拒绝，上层改判跳过）；
// 检索失败（Cookie 失效 / 站点限流 / 网络异常）时返回普通 error（上层走重试）——
// 这两类语义不可混：把「查不到」当成「不重复」会漏过真正的 dupe。
// 副作用：可能向目标站点发起 1~2 次 GET 请求（仅在开关开启且站点已声明 dupe 能力时）。
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

	torrentSize, err := resolveTorrentTotalSize(input)
	if err != nil {
		detail := fmt.Sprintf("dupe 校验未执行：%v", err)
		logx.Warnf(logModule, "site=%s %s", strings.TrimSpace(input.SiteCode), detail)
		return detail, nil
	}

	query := publishdupe.Query{
		BaseURL:            input.BaseURL,
		Cookie:             input.Cookie,
		DoubanID:           doubanLinkFromUploadData(input.UploadData, input.DoubanLink),
		IMDbID:             input.IMDbLink,
		TMDbID:             tmdbLinkFromUploadData(input.UploadData),
		Filters:            publishdupe.BuildSearchFilters(input.SiteCode, formFields),
		Title:              strings.TrimSpace(input.Title),
		TorrentSizeBytes:   torrentSize,
		SizeToleranceBytes: settings.SizeToleranceBytes,
	}

	result, detail, checkErr := publishdupe.CheckBySite(input.SiteCode, query)
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
