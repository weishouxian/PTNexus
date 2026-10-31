package sites

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pt-nexus/server/internal/service/colaptapi"
)

// ExtractColapt 通过可乐PT 的 JSON API 提取种子参数。
//
// 站点是纯 SPA：详情页 /torrents/{id} 未登录会 302 到 /login，PageHTML 里没有任何可解析字段，
// 因此这里完全忽略 input.PageHTML，改为读 /api/v1/torrents/{id}，
// 再用 media_id（形如 "rc17"）补作品侧的外链与产地（这一步失败不阻断主流程）。
//
// 参数/返回：input.Cookie 填浏览器登录态（首选，读 /torrents/* 网页版接口）；
// input.Passkey 填站点 API Key（仅兜底，站点的 Key 无读权限，需站方开通自动化授权才可用）。
// 失败场景：缺少凭据、种子 ID 非法、站点拒绝或接口异常。
// 副作用：发起 2~3 次 HTTP 请求。
func ExtractColapt(input Input, runtime Runtime) (SeedData, error) {
	client, err := colaptapi.NewClientWithAuth(input.BaseURL, input.Passkey, input.Cookie)
	if err != nil {
		return SeedData{}, err
	}
	torrentID := colaptapi.ExtractTorrentID(input.TorrentID)
	if torrentID == "" {
		return SeedData{}, fmt.Errorf("可乐站种子 ID 无效：%s", strings.TrimSpace(input.TorrentID))
	}

	torrent, err := client.Torrent(torrentID)
	if err != nil {
		return SeedData{}, err
	}
	data := buildColaptSeedData(torrent)

	// 作品信息只用来补外链/产地/海报，失败时保留种子侧已提取的字段。
	if strings.TrimSpace(torrent.MediaID) != "" {
		if media, mediaErr := client.Media(torrent.MediaID); mediaErr == nil {
			applyColaptMedia(&data, media)
		}
	}
	return data, nil
}

// buildColaptSeedData 把站点种子详情映射为统一提取结构。
// 口径说明：站点的 source 是「片源类型」不是产地，因此不写入 Source（产地）；
// 媒介/编码交给后续标准化推断（标题 + MediaInfo），避免把站点自由文本硬塞进标准字典。
func buildColaptSeedData(torrent colaptapi.Torrent) SeedData {
	data := SeedData{
		Title:      strings.TrimSpace(torrent.ReleaseName),
		Subtitle:   strings.TrimSpace(torrent.Subtitle),
		MediaInfo:  strings.TrimSpace(torrent.MediaInfo),
		Team:       strings.TrimSpace(torrent.ReleaseGroup),
		Type:       colaptTypeKey(torrent.ScopeCategory()),
		Resolution: colaptResolutionValue(torrent.Resolution),
	}
	data.Intro.Body = strings.TrimSpace(torrent.Description)
	data.Intro.Screenshots = colaptScreenshotsBBCode(torrent.Screenshots)

	tags := colaptTags(torrent.Tags)
	tags = appendUniqueTags(tags, colaptSubtitleTags(torrent.Subtitles)...)
	data.Tags = tags
	return data
}

// applyColaptMedia 用作品信息补齐外链、产地、海报与类型。
//
// 类型以作品分类为真源：站点把分类挂在作品上，而 TorrentDetail.release_scope 只描述
// 发布范围（季/集），电影作品的 release_scope 直接是 null（契约 ReleaseScope 的 oneOf 含 null），
// 因此仅凭 scope 判不出电影，必须回落到 media.category。
func applyColaptMedia(data *SeedData, media colaptapi.Media) {
	if data == nil {
		return
	}
	if category := strings.TrimSpace(media.Category); category != "" {
		// 作品分类存在时以它为准；"other" 映射为空串，表示不做类型断言。
		data.Type = colaptTypeKey(category)
	}
	if data.DoubanLink == "" && strings.TrimSpace(media.DoubanID) != "" {
		data.DoubanLink = "https://movie.douban.com/subject/" + strings.TrimSpace(media.DoubanID) + "/"
	}
	if data.IMDbLink == "" && strings.TrimSpace(media.IMDbID) != "" {
		data.IMDbLink = "https://www.imdb.com/title/" + strings.TrimSpace(media.IMDbID) + "/"
	}
	if data.TMDbLink == "" && strings.TrimSpace(media.TMDbURL) != "" {
		data.TMDbLink = strings.TrimSpace(media.TMDbURL)
	}
	if data.Source == "" {
		data.Source = colaptCountries(media.Countries)
	}
	if data.Intro.Poster == "" && strings.TrimSpace(media.PosterURL) != "" {
		data.Intro.Poster = "[img]" + strings.TrimSpace(media.PosterURL) + "[/img]"
	}
	// 站点种子简介常常只有一句「XX官组作品」，正文为空或明显是套话时用作品简介兜底。
	if synopsis := strings.TrimSpace(media.Synopsis); synopsis != "" && utf8.RuneCountInString(strings.TrimSpace(data.Intro.Body)) < 12 {
		data.Intro.Body = synopsis
	}
}

// colaptTypeKey 把站点作品分类映射成标准化类型键。
// 映射依据：站点分类只有 movie/tv/anime/variety/other，对应内部 category.* 键。
func colaptTypeKey(category string) string {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "movie":
		return "category.movie"
	case "tv":
		return "category.tv_series"
	case "anime":
		return "category.animation"
	case "variety":
		return "category.tv_shows"
	default:
		return ""
	}
}

// colaptResolutionValue 归一分辨率取值（站点直接给 2160p/1080p 这类标准写法）。
func colaptResolutionValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "2160p", "1080p", "1080i", "720p", "576p", "480p":
		return lower
	default:
		return trimmed
	}
}

// colaptTags 过滤站点派生标签。
// 站点标签里 1080P/1080i/4K 与「分辨率」字段重复，保留其余（原盘/HDR/杜比/国语/粤语/中字…）。
func colaptTags(raw []string) []string {
	tags := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		switch strings.ToLower(trimmed) {
		case "1080p", "1080i", "4k":
			continue
		}
		tags = appendUniqueTags(tags, trimmed)
	}
	return tags
}

// colaptSubtitleTags 把字幕语言码转成标签（站点标签缺失时的兜底）。
func colaptSubtitleTags(codes []string) []string {
	tags := make([]string, 0, len(codes))
	for _, code := range codes {
		switch strings.ToLower(strings.TrimSpace(code)) {
		case "zh":
			tags = appendUniqueTags(tags, "中字")
		case "en":
			tags = appendUniqueTags(tags, "英字")
		case "ja":
			tags = appendUniqueTags(tags, "日字")
		}
	}
	return tags
}

// colaptScreenshotsBBCode 把截图地址拼成 BBCode 图片块，与其它站点提取器的口径保持一致。
func colaptScreenshotsBBCode(urls []string) string {
	blocks := make([]string, 0, len(urls))
	for _, url := range urls {
		trimmed := strings.TrimSpace(url)
		if trimmed == "" {
			continue
		}
		blocks = append(blocks, "[img]"+trimmed+"[/img]")
	}
	return strings.Join(blocks, "\n")
}

// colaptCountries 把作品产地列表拼成产地字段值。
func colaptCountries(countries []string) string {
	values := make([]string, 0, len(countries))
	for _, country := range countries {
		trimmed := strings.TrimSpace(country)
		if trimmed == "" {
			continue
		}
		values = appendUniqueTags(values, trimmed)
	}
	return strings.Join(values, "/")
}

// appendUniqueTags 追加去重标签。
func appendUniqueTags(target []string, values ...string) []string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		exists := false
		for _, existing := range target {
			if strings.EqualFold(strings.TrimSpace(existing), trimmed) {
				exists = true
				break
			}
		}
		if !exists {
			target = append(target, trimmed)
		}
	}
	return target
}
