package sites

import (
	"errors"
	"regexp"
	"strings"
)

var (
	// TTG 的主标题是裸 <h1>（没有 id="top"），公共提取器的 reTopTitle 匹配不到，
	// 会退到 <title> 拿到「TTG :: 种子 "..." 的详情」这类脏标题，所以站点自己解析。
	reTTGMainTitle = regexp.MustCompile(`(?is)<h1[^>]*>(.*?)</h1>`)
	// TTG 把「简述」整块塞在 <div id="kt_d"> 里，声明/剧情/参数各用一块 <table class="main"> 包裹。
	reTTGQuoteTable = regexp.MustCompile(`(?is)<table[^>]*class=["']?main["']?[^>]*>.*?</table>`)
	// 简介里的图片：TTG 截图列表是「缩略图 <img> + 外链原图 <a>」，优先取 <a href>。
	reTTGImageAnchor = regexp.MustCompile(`(?is)<a[^>]*href=["']([^"']+)["'][^>]*>\s*<img`)
	reTTGImageSrc    = regexp.MustCompile(`(?is)<img[^>]*src=["']([^"']+)["']`)
	// tu.totheglory.im 的缩略图带 _thumb 后缀，换成原图才适合当作品截图。
	reTTGThumbSuffix = regexp.MustCompile(`(?i)_thumb(\.(?:jpe?g|png|webp|gif))$`)
	// 促销尾注（30%/50%/免费说明）不属于简介正文。
	reTTGPromoNote = regexp.MustCompile(`(?is)本种子的下载流量计为[^<]*`)
	// 豆瓣资料块的起始行（◎译　　名 / ◎片　　名 / ◎简　　介 …）。
	reTTGDoubanLine = regexp.MustCompile(`(?m)^[◎❁]\s*[^\s]`)
	reTTGBreakTag   = regexp.MustCompile(`(?i)<br\s*/?>`)
	reTTGAnyTag     = regexp.MustCompile(`(?is)<[^>]+>`)
	reTTGMultiNL    = regexp.MustCompile(`\n{3,}`)
)

// ttgDeclarationKeywords 判定「简述」首块是不是发布者声明。
// 说明：TTG 的声明通常写成一句话（禁转/DIY 素材来源/首发说明），与后面长篇剧情文本靠长度也能分开。
var ttgDeclarationKeywords = []string{"声明", "禁转", "严禁", "本资源", "请勿", "转载", "首发", "DIY", "感谢"}

// ttgMediaInfoMarkers 判定一块文本是不是真正的 MediaInfo。
// 说明：TTG 常见的「TAGLiNE/GENRE/ViDEO CODEC...」是 release info 排版，不是 MediaInfo，
// 必须用 MediaInfo 独有的字段名区分，避免把它塞进 MediaInfo 字段后发到目标站。
var ttgMediaInfoMarkers = []string{"Unique ID", "Complete name", "CompleteName", "Overall bit rate", "MediaInfo"}

// ExtractTTG 提取 TTG（ToTheGlory）详情页参数。
// 参数/返回：输入详情页上下文与运行期依赖，返回统一 SeedData。
// 失败场景：详情页为空、关键字段几乎全部缺失。
// 副作用：无。
func ExtractTTG(input Input, runtime Runtime) (SeedData, error) {
	page := strings.TrimSpace(input.PageHTML)
	if page == "" {
		return SeedData{}, errors.New("详情页内容为空")
	}

	title := extractTTGTitle(page, runtime)
	if title == "" {
		title = strings.TrimSpace(input.FallbackTitle)
	}

	descrHTML := ""
	if runtime.ExtractElementInnerHTMLByID != nil {
		descrHTML = runtime.ExtractElementInnerHTMLByID(page, "div", "kt_d")
		// 老版 TTG 模板与部分改编模板把简述放在 #kdescr，兜底再找一次。
		if strings.TrimSpace(descrHTML) == "" {
			descrHTML = runtime.ExtractElementInnerHTMLByID(page, "div", "kdescr")
		}
		if strings.TrimSpace(descrHTML) == "" {
			descrHTML = runtime.ExtractElementInnerHTMLByID(page, "td", "kdescr")
		}
	}

	statement, restHTML := extractTTGDeclaration(descrHTML, runtime)
	body := extractTTGBody(restHTML, runtime)
	mediainfo := extractTTGMediaInfo(descrHTML, runtime)
	poster, screens := extractTTGPosterAndScreens(descrHTML, runtime)

	linkText := strings.Join([]string{page, body, statement}, "\n")
	imdbLink, doubanLink, tmdbLink := "", "", ""
	if runtime.NormalizeExternalLink != nil {
		if runtime.ReIMDbLink != nil {
			imdbLink = runtime.NormalizeExternalLink(runtime.ReIMDbLink.FindString(linkText), runtime.ReIMDbLink)
		}
		if runtime.ReDoubanLink != nil {
			doubanLink = runtime.NormalizeExternalLink(runtime.ReDoubanLink.FindString(linkText), runtime.ReDoubanLink)
		}
		if runtime.ReTMDbLink != nil {
			tmdbLink = runtime.NormalizeExternalLink(runtime.ReTMDbLink.FindString(linkText), runtime.ReTMDbLink)
		}
	}

	inferred := map[string]string{}
	if runtime.InferStandardizedValues != nil {
		inferred = runtime.InferStandardizedValues(title, mediainfo, body)
	}
	// 类型优先取详情页「类型」行（ttg.yaml 的 source_parsers.standard_keys 负责映射），
	// 拿不到才用标题推断值兜底。
	typeKey := ""
	if runtime.ExtractBasicInfoType != nil {
		typeKey = strings.TrimSpace(runtime.ExtractBasicInfoType(page, input.SiteCode))
	}
	if typeKey == "" {
		typeKey = strings.TrimSpace(inferred["type"])
	}

	team := strings.TrimSpace(inferred["team"])
	if runtime.ExtractTeamFromPage != nil && runtime.NormalizeTeamKey != nil {
		if teamFromPage := runtime.ExtractTeamFromPage(page); teamFromPage != "" {
			if teamKey := runtime.NormalizeTeamKey(teamFromPage); teamKey != "" && teamKey != "team.other" {
				team = teamKey
			}
		}
	}

	tags := []string{}
	if runtime.ExtractTagsFromPage != nil {
		tags = append(tags, runtime.ExtractTagsFromPage(page)...)
	}
	if runtime.MergeExplicitSourceTags != nil {
		tags = runtime.MergeExplicitSourceTags(tags)
	}

	data := SeedData{
		Title:     title,
		Subtitle:  "",
		MediaInfo: strings.TrimSpace(mediainfo),
		Intro: IntroData{
			Statement:   strings.TrimSpace(statement),
			Poster:      strings.TrimSpace(poster),
			Body:        strings.TrimSpace(body),
			Screenshots: strings.TrimSpace(toBBCodeImages(screens)),
		},
		Type:       typeKey,
		Medium:     strings.TrimSpace(inferred["medium"]),
		VideoCodec: strings.TrimSpace(inferred["video_codec"]),
		AudioCodec: strings.TrimSpace(inferred["audio_codec"]),
		Resolution: strings.TrimSpace(inferred["resolution"]),
		Team:       team,
		Source:     strings.TrimSpace(inferred["source"]),
		Tags:       tags,
		IMDbLink:   strings.TrimSpace(imdbLink),
		DoubanLink: strings.TrimSpace(doubanLink),
		TMDbLink:   strings.TrimSpace(tmdbLink),
	}

	if runtime.BuildSourceParams != nil {
		data.SourceParams = runtime.BuildSourceParams(data)
	}
	data = data.Normalize(input.FallbackTitle)

	if strings.TrimSpace(data.Title) == "" && strings.TrimSpace(data.Intro.Body) == "" &&
		strings.TrimSpace(data.Intro.Statement) == "" && strings.TrimSpace(data.Intro.Screenshots) == "" {
		return data, errors.New("TTG 详情页关键字段全部为空")
	}
	return data, nil
}

// extractTTGTitle 从页面 <h1> 取主标题。
func extractTTGTitle(pageHTML string, runtime Runtime) string {
	match := reTTGMainTitle.FindStringSubmatch(pageHTML)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(sanitizeTTGText(match[1], runtime, false))
}

// extractTTGDeclaration 取「简述」首块声明，同时返回剥掉该块后的 HTML（供正文解析）。
// 参数/返回：descrHTML 为 #kt_d 内部 HTML；返回声明文本与剩余 HTML。
// 失败场景：没有符合声明特征的块时返回空声明与原 HTML。
// 副作用：无。
func extractTTGDeclaration(descrHTML string, runtime Runtime) (string, string) {
	trimmed := strings.TrimSpace(descrHTML)
	if trimmed == "" {
		return "", ""
	}
	for _, loc := range reTTGQuoteTable.FindAllStringIndex(trimmed, -1) {
		text := strings.TrimSpace(sanitizeTTGText(trimmed[loc[0]:loc[1]], runtime, true))
		if !isTTGDeclarationText(text) {
			continue
		}
		rest := trimmed[:loc[0]] + trimmed[loc[1]:]
		return text, rest
	}
	return "", trimmed
}

// isTTGDeclarationText 判断一段文本是否像发布者声明（短、单块、含声明关键词）。
func isTTGDeclarationText(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if len([]rune(trimmed)) > 200 || strings.Count(trimmed, "\n") > 3 {
		return false
	}
	for _, keyword := range ttgDeclarationKeywords {
		if strings.Contains(trimmed, keyword) {
			return true
		}
	}
	return false
}

// extractTTGBody 取简介正文：优先豆瓣资料块（◎译名/◎简介…），没有则整块简述兜底。
func extractTTGBody(descrHTML string, runtime Runtime) string {
	text := sanitizeTTGText(descrHTML, runtime, true)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	// 促销尾注要按原始 HTML 先剥掉，避免它和正文粘在同一行。
	text = reTTGPromoNote.ReplaceAllString(text, "")
	if loc := reTTGDoubanLine.FindStringIndex(text); loc != nil && loc[0] < len(text) {
		text = text[loc[0]:]
	}
	return normalizeTTGBodyText(text)
}

// extractTTGMediaInfo 从简述里挑真正的 MediaInfo 文本块。
// 说明：只认 Unique ID / Complete name / MediaInfo 等 MediaInfo 独有字段，
// TTG 里常见的 TAGLiNE/GENRE 排版属 release info，不能当 MediaInfo。
func extractTTGMediaInfo(descrHTML string, runtime Runtime) string {
	text := sanitizeTTGText(descrHTML, runtime, true)
	if strings.TrimSpace(text) == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	for idx, line := range lines {
		if !containsAnyKeyword(line, ttgMediaInfoMarkers) {
			continue
		}
		// 从命中行往前收 1 行（通常 MediaInfo 首行是 "General"），往后收到空行为止。
		start := idx
		if start > 0 && strings.EqualFold(strings.TrimSpace(lines[start-1]), "general") {
			start--
		}
		end := idx + 1
		for end < len(lines) && strings.TrimSpace(lines[end]) != "" {
			end++
		}
		block := strings.TrimSpace(strings.Join(lines[start:end], "\n"))
		if runtime.IsLikelyMediaInfoText != nil && !runtime.IsLikelyMediaInfoText(block) {
			continue
		}
		return block
	}
	return ""
}

// extractTTGPosterAndScreens 从简述里取海报与截图（缩略图还原为原图）。
func extractTTGPosterAndScreens(descrHTML string, runtime Runtime) (string, []string) {
	trimmed := strings.TrimSpace(descrHTML)
	if trimmed == "" {
		return "", nil
	}

	urls := make([]string, 0, 16)
	for _, match := range reTTGImageAnchor.FindAllStringSubmatch(trimmed, -1) {
		if len(match) >= 2 {
			urls = append(urls, match[1])
		}
	}
	for _, match := range reTTGImageSrc.FindAllStringSubmatch(trimmed, -1) {
		if len(match) >= 2 {
			urls = append(urls, match[1])
		}
	}

	poster := ""
	screens := make([]string, 0, 12)
	seen := make(map[string]struct{}, len(urls))
	for _, raw := range urls {
		url := normalizeTTGImageURL(raw)
		if url == "" {
			continue
		}
		lower := strings.ToLower(url)
		// 站点图标/表情不算内容图。
		if strings.Contains(lower, "/pic/") || strings.Contains(lower, "smilies") {
			continue
		}
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		if poster == "" && isLikelyPosterURL(url) {
			poster = "[img]" + url + "[/img]"
			continue
		}
		if !looksLikeImageURL(lower) {
			continue
		}
		screens = append(screens, url)
	}
	return poster, screens
}

// normalizeTTGImageURL 归一化图片地址：去掉缩略图后缀、协议相对前缀与首尾空白。
func normalizeTTGImageURL(raw string) string {
	url := strings.TrimSpace(raw)
	if url == "" {
		return ""
	}
	if strings.HasPrefix(url, "//") {
		url = "https:" + url
	}
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return ""
	}
	if reTTGThumbSuffix.MatchString(url) {
		url = reTTGThumbSuffix.ReplaceAllString(url, "$1")
	}
	return url
}

// looksLikeImageURL 判断地址是否以常见图片扩展名结尾（允许带查询串）。
func looksLikeImageURL(lower string) bool {
	cleaned := lower
	if idx := strings.IndexAny(cleaned, "?#"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp"} {
		if strings.HasSuffix(cleaned, ext) {
			return true
		}
	}
	return false
}

func containsAnyKeyword(text string, keywords []string) bool {
	lower := strings.ToLower(text)
	for _, keyword := range keywords {
		if strings.Contains(lower, strings.ToLower(keyword)) {
			return true
		}
	}
	return false
}

// sanitizeTTGText 把 HTML 片段归一化为纯文本（优先复用公共实现，保持一致口径）。
func sanitizeTTGText(input string, runtime Runtime, keepLineBreak bool) string {
	if runtime.SanitizeHTMLText != nil {
		return runtime.SanitizeHTMLText(input, keepLineBreak)
	}
	text := input
	if keepLineBreak {
		text = reTTGBreakTag.ReplaceAllString(text, "\n")
	}
	text = reTTGAnyTag.ReplaceAllString(text, "")
	return normalizeTTGBodyText(text)
}

// normalizeTTGBodyText 收敛空行与首尾空白。
func normalizeTTGBodyText(text string) string {
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			if len(out) == 0 || out[len(out)-1] == "" {
				continue
			}
			out = append(out, "")
			continue
		}
		out = append(out, trimmed)
	}
	joined := strings.TrimSpace(strings.Join(out, "\n"))
	return strings.TrimSpace(reTTGMultiNL.ReplaceAllString(joined, "\n\n"))
}
