package uploader

import (
	"fmt"
	neturl "net/url"
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/service/descclean"
	processingmedia "github.com/pt-nexus/server/internal/service/processing/media"
	processingtagging "github.com/pt-nexus/server/internal/service/processing/tagging"
)

var (
	rePublishURLAbsolute = regexp.MustCompile(`(?is)https?://[^"'\s>]+/(?:details\.php\?id=\d+|offers\.php\?id=\d+|torrent/[0-9a-fA-F\-]{36})`)
	rePublishURLRelative = regexp.MustCompile(`(?is)/(?:details\.php\?id=\d+|offers\.php\?id=\d+|torrent/[0-9a-fA-F\-]{36})`)
	// pterclub 等站点在“该种子已存在”页面会在表格中给出详情页链接，页面可能包含多个 details 链接，优先取该表格内的。
	rePublishURLExistingTable = regexp.MustCompile(`(?is)<table[^>]*class=["'][^"']*torrent-exists-tbl[^"']*["'][^>]*>.*?<a[^>]*href=["']([^"']*(?:details\.php\?[^"']*id=\d+|offers\.php\?[^"']*id=\d+|torrent/[0-9a-fA-F\-]{36})[^"']*)["']`)
)

// yemapt 声明转换相关正则：将 BBCode 声明（[quote][b][color][size]...[/quote]）转为 Markdown 块引用+加粗。
var (
	reYemaPTQuoteBlock = regexp.MustCompile(`(?is)\[quote\](.*?)\[/quote\]`)
	// 仅剥离结构性标签（b/color/size/quote），保留 [u] 等源站内容标记（DIY 小组等）。
	reYemaPTStripTags = regexp.MustCompile(`(?i)\[(?:b|/b|color|/color|size|/size|quote|/quote)\b[^\]]*\]`)
)

// DetectRestrictedTags 检测上传参数中的禁转/限转/分集标签。
func DetectRestrictedTags(uploadData map[string]any) []string {
	standardized := map[string]any{}
	if item, ok := uploadData["standardized_params"].(map[string]any); ok {
		standardized = item
	}
	rawTags := append(parseStringArray(standardized["tags"]), parseStringArray(uploadData["tags"])...)
	return processingtagging.DetectRestrictedTags(rawTags)
}

// TrimDescriptionAtMovieParams 若简介包含【影片参数】参数段落标记，则删除该标记及其之后的全部内容（含标记本身），仅保留其前面的影片介绍。
// 说明：实现委托给 descclean 包（抓取向量与发种向共用同一份逻辑，单一真源）。
// 参数/返回：desc 为原始简介；未命中标记时原样返回。
func TrimDescriptionAtMovieParams(desc string) string {
	return descclean.TrimDescriptionAtMovieParams(desc)
}

// BuildUploadDescription 按固定顺序拼接发布描述正文。
// 参数/返回：siteCode 为目标站点 code（用于处理少数站点的 MediaInfo 内嵌规则）；uploadData 为发布参数。
// 失败场景：无失败场景，字段缺失时自动跳过。
// 副作用：无。
func BuildUploadDescription(siteCode string, uploadData map[string]any) string {
	intro := map[string]any{}
	if item, ok := uploadData["intro"].(map[string]any); ok && item != nil {
		intro = item
	}

	if strings.EqualFold(strings.TrimSpace(siteCode), "pterclub") {
		return TrimDescriptionAtMovieParams(buildPTerClubUploadDescription(uploadData, intro))
	}

	// yemapt 为自研 Markdown 发种站，声明需用 Markdown 块引用+加粗（> **...**）呈现，
	// 而其它站点沿用 BBCode 声明，因此在此单独处理，避免影响其它站。
	if strings.EqualFold(strings.TrimSpace(siteCode), "yemapt") {
		return TrimDescriptionAtMovieParams(buildYemaPTUploadDescription(uploadData, intro))
	}

	statement := pickDescriptionSection(uploadData, intro, "statement")
	poster := pickDescriptionSection(uploadData, intro, "poster")
	body := pickDescriptionSection(uploadData, intro, "body")
	screenshots := pickDescriptionSection(uploadData, intro, "screenshots")
	mediainfo := strings.TrimSpace(toStringAny(uploadData["mediainfo"], ""))

	parts := make([]string, 0, 5)
	for _, section := range []string{statement, poster, body} {
		if strings.TrimSpace(section) != "" {
			parts = append(parts, section)
		}
	}

	if shouldInlineMediainfo(siteCode) && mediainfo != "" {
		parts = append(parts, "[quote]"+mediainfo+"[/quote]")
	}

	if screenshots != "" {
		parts = append(parts, screenshots)
	}

	if len(parts) == 0 {
		return strings.TrimSpace(toStringAny(uploadData["subtitle"], ""))
	}
	return TrimDescriptionAtMovieParams(strings.Join(parts, "\n"))
}

func buildPTerClubUploadDescription(uploadData map[string]any, intro map[string]any) string {
	statement := pickDescriptionSection(uploadData, intro, "statement")
	poster := pickDescriptionSection(uploadData, intro, "poster")
	body := pickDescriptionSection(uploadData, intro, "body")
	screenshots := pickDescriptionSection(uploadData, intro, "screenshots")
	mediainfo := strings.TrimSpace(toStringAny(uploadData["mediainfo"], ""))
	bdinfo := strings.TrimSpace(toStringAny(uploadData["bdinfo"], ""))

	parts := make([]string, 0, 6)
	for _, section := range []string{statement, poster, body} {
		if strings.TrimSpace(section) != "" {
			parts = append(parts, section)
		}
	}

	if mediaBlock := buildPTerClubMediaBlock(mediainfo); mediaBlock != "" {
		parts = append(parts, mediaBlock)
	}
	if bdinfoBlock := buildPTerClubBDInfoBlock(bdinfo); bdinfoBlock != "" {
		parts = append(parts, bdinfoBlock)
	}

	if strings.TrimSpace(screenshots) != "" {
		parts = append(parts, screenshots)
	}

	if len(parts) == 0 {
		return strings.TrimSpace(toStringAny(uploadData["subtitle"], ""))
	}
	return strings.Join(parts, "\n")
}

func buildPTerClubMediaBlock(mediaText string) string {
	trimmed := strings.TrimSpace(mediaText)
	if trimmed == "" {
		return ""
	}

	isMediainfo, isBDInfo, _ := processingmedia.ValidateMediaInfoFormat(trimmed)
	switch {
	case isBDInfo:
		return "[hide=bdinfo]" + trimmed + "[/hide]"
	case isMediainfo:
		return "[hide=mediainfo]" + trimmed + "[/hide]"
	default:
		return "[hide=mediainfo]" + trimmed + "[/hide]"
	}
}

func buildPTerClubBDInfoBlock(mediaText string) string {
	trimmed := strings.TrimSpace(mediaText)
	if trimmed == "" {
		return ""
	}
	return "[hide=bdinfo]" + trimmed + "[/hide]"
}

// buildYemaPTUploadDescription 构建 yemapt 发种简介：声明由 BBCode 转为 Markdown 块引用+加粗（> **...**），
// 其它段落（海报/正文/截图）保持原样拼接，mediainfo 由 adapter 作为独立字段发送，不内联。
func buildYemaPTUploadDescription(uploadData map[string]any, intro map[string]any) string {
	statement := pickDescriptionSection(uploadData, intro, "statement")
	poster := pickDescriptionSection(uploadData, intro, "poster")
	body := pickDescriptionSection(uploadData, intro, "body")
	screenshots := pickDescriptionSection(uploadData, intro, "screenshots")

	parts := make([]string, 0, 4)
	if wrapped := convertYemaPTStatement(statement); wrapped != "" {
		parts = append(parts, wrapped)
	}
	if strings.TrimSpace(poster) != "" {
		parts = append(parts, poster)
	}
	if strings.TrimSpace(body) != "" {
		parts = append(parts, body)
	}
	if strings.TrimSpace(screenshots) != "" {
		parts = append(parts, screenshots)
	}

	if len(parts) == 0 {
		return strings.TrimSpace(toStringAny(uploadData["subtitle"], ""))
	}
	return strings.Join(parts, "\n")
}

// convertYemaPTStatement 将 BBCode 声明（可能包含多个 [quote] 块）转为 yemapt 接受的 Markdown 块引用+加粗。
// 每个 [quote] 块转换为一段「> ** 内容 **」，块内换行保留为「> 」前缀的续行；
// 结构性标签 [b]/[color]/[size]/[quote] 被剥离，[u] 等源站内容标记予以保留。
func convertYemaPTStatement(statement string) string {
	trimmed := strings.TrimSpace(statement)
	if trimmed == "" {
		return ""
	}

	blocks := make([]string, 0, 2)
	if matches := reYemaPTQuoteBlock.FindAllStringSubmatch(trimmed, -1); len(matches) > 0 {
		for _, m := range matches {
			blocks = append(blocks, m[1])
		}
	} else {
		blocks = append(blocks, trimmed)
	}

	out := make([]string, 0, len(blocks))
	for _, block := range blocks {
		inner := reYemaPTStripTags.ReplaceAllString(block, "")
		inner = strings.TrimSpace(inner)
		if inner == "" {
			continue
		}
		lines := strings.Split(inner, "\n")
		cleaned := make([]string, 0, len(lines))
		for _, ln := range lines {
			s := strings.TrimSpace(ln)
			// 去掉可能残留的 ** 标记，避免重复包裹
			s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(s, "**"), "**"))
			if s != "" {
				cleaned = append(cleaned, s)
			}
		}
		if len(cleaned) == 0 {
			continue
		}
		out = append(out, "> **"+strings.Join(cleaned, "\n> ")+"**")
	}
	return strings.Join(out, "\n")
}

func pickDescriptionSection(uploadData map[string]any, intro map[string]any, key string) string {
	fromTop := strings.TrimSpace(toStringAny(uploadData[key], ""))
	if fromTop != "" {
		return fromTop
	}
	return strings.TrimSpace(toStringAny(intro[key], ""))
}

func shouldInlineMediainfo(siteCode string) bool {
	switch strings.ToLower(strings.TrimSpace(siteCode)) {
	case "audiences", "btschool", "carpt", "kufei", "lemon", "oshen", "ptskit", "sewerpt", "ttg", "upxin", "zmpt", "xdypt", "pthome":
		return true
	default:
		return false
	}
}

// ExtractPublishURLFromText 从上传响应文本中提取详情页/offer 链接。
func ExtractPublishURLFromText(baseURL, text string) string {
	if match := rePublishURLExistingTable.FindStringSubmatch(text); len(match) >= 2 {
		if publishURL := NormalizePublishURLWithOfferSupport(baseURL, match[1]); publishURL != "" {
			return strings.TrimSpace(publishURL)
		}
	}
	if match := rePublishURLAbsolute.FindString(text); match != "" {
		return strings.TrimSpace(match)
	}
	if match := rePublishURLRelative.FindString(text); match != "" {
		return strings.TrimRight(baseURL, "/") + match
	}
	return ""
}

// NormalizePublishURL 标准化发布 URL 到绝对链接。
func NormalizePublishURL(baseURL, candidate string) string {
	trimmed := strings.TrimSpace(candidate)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "http://") || strings.HasPrefix(strings.ToLower(trimmed), "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return strings.TrimRight(baseURL, "/") + trimmed
	}
	if strings.Contains(trimmed, "details.php") || strings.Contains(trimmed, "/torrent/") {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(trimmed, "/")
	}
	if parsed, err := neturl.Parse(trimmed); err == nil && parsed.Path != "" {
		return strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(parsed.String(), "/")
	}
	return ""
}

func parseStringArray(value any) []string {
	switch typed := value.(type) {
	case nil:
		return []string{}
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			trimmed := strings.TrimSpace(toStringAny(item, ""))
			if trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case string:
		raw := strings.TrimSpace(typed)
		if raw == "" {
			return []string{}
		}
		if strings.Contains(raw, ",") {
			items := strings.Split(raw, ",")
			out := make([]string, 0, len(items))
			for _, item := range items {
				trimmed := strings.TrimSpace(item)
				if trimmed != "" {
					out = append(out, trimmed)
				}
			}
			return out
		}
		return []string{raw}
	default:
		return []string{}
	}
}

func toStringAny(value any, fallback string) string {
	switch typed := value.(type) {
	case nil:
		return fallback
	case string:
		return typed
	case []byte:
		return string(typed)
	case int:
		return fmt.Sprintf("%d", typed)
	case int8:
		return fmt.Sprintf("%d", typed)
	case int16:
		return fmt.Sprintf("%d", typed)
	case int32:
		return fmt.Sprintf("%d", typed)
	case int64:
		return fmt.Sprintf("%d", typed)
	case uint:
		return fmt.Sprintf("%d", typed)
	case uint8:
		return fmt.Sprintf("%d", typed)
	case uint16:
		return fmt.Sprintf("%d", typed)
	case uint32:
		return fmt.Sprintf("%d", typed)
	case uint64:
		return fmt.Sprintf("%d", typed)
	case float32:
		return fmt.Sprintf("%v", typed)
	case float64:
		return fmt.Sprintf("%v", typed)
	case bool:
		if typed {
			return "true"
		}
		return "false"
	default:
		return fallback
	}
}
