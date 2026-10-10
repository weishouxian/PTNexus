package sites

import (
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

const pt52PublishLogModule = "发布-52PT"

// pt52IMDbIDPattern 匹配 IMDb ID：兼容 tt1234567 与裸数字 1234567（部分源站的 imdb 字段只给数字）。
var pt52IMDbIDPattern = regexp.MustCompile(`(?i)(?:tt)?(\d{5,})`)

// pt52Publisher 52PT（52pt.site）发布器。
// 说明：52PT 的 upload.php 是标准 NexusPHP 表单，但 medium_sel / team_sel 的选项把
// 「分辨率」「原盘是否带中文」「Maker 小组归属」编码进了取值，单靠 yaml 静态映射表达不了，
// 因此按站点补一层 AdjustFormFields 细分。其余流程完全复用公共上传底座。
type pt52Publisher struct {
	publicSiteDefaults
}

// PublishPT52 执行 52PT 站点发布流程。
func PublishPT52(input publisher.PublishInput) (publisher.PublishResult, error) {
	return publishWithPublicSite(input, pt52Publisher{})
}

func (pt52Publisher) LogModule() string {
	return pt52PublishLogModule
}

// AdjustFormFields 修正 52PT 表单字段。
// 参数/返回：input 提供发布数据（用于 IMDb 链接兜底）；formFields 为已映射好的表单字段。
// 失败场景：无（字段缺失时按原值返回）。
// 副作用：直接改写 formFields。
func (pt52Publisher) AdjustFormFields(input publisher.PublishInput, formFields map[string]string) {
	if formFields == nil {
		return
	}

	if strings.TrimSpace(formFields["url"]) == "" {
		if link := resolvePT52IMDbURL(input); link != "" {
			formFields["url"] = link
		}
	}

	refinePT52Medium(formFields)
	refinePT52Team(formFields)

	// 站点没有这几个字段：MediaInfo 已内嵌进简介，豆瓣/技术信息字段在本站不存在，
	// 主标题由服务端取 name，多余的 title 一并清理。
	delete(formFields, "technical_info")
	delete(formFields, "dburl")
	delete(formFields, "pt_gen")
	delete(formFields, "title")
}

// refinePT52Medium 按分辨率与标签细分 medium_sel。
// 说明：PTNexus 的 medium.* 只区分 bluray / uhd_bluray / remux（Remux 不区分分辨率），
// 而 52PT 的 medium_sel 需要区分：
//   - Remux：4=Blu-ray Remux（1080p） / 5=4K UHD Remux
//   - 原盘：11=Blu-ray原盘无中文 / 14=2K原盘中字（国粤语）；1=4K UHD无中文 / 15=4K原盘中字（国粤语）
//   - DIY：2=Blu-ray DIY / 12=4K UHD DIY；8K：13=8K UHD
func refinePT52Medium(formFields map[string]string) {
	medium := strings.TrimSpace(formFields["medium_sel"])
	resolution := strings.TrimSpace(formFields["standard_sel"])
	tags := collectPT52FormTags(formFields)

	hasDIY := tags["DIY"]
	hasChinese := tags["简中"] || tags["繁中"] || tags["简繁"] || tags["国语"] || tags["粤语"]

	switch medium {
	case "4": // Blu-ray Remux
		if resolution == "5" { // 4K/2160P
			formFields["medium_sel"] = "5"
		}
	case "1": // 4K UHD无中文
		switch {
		case resolution == "7": // 8K/4320
			formFields["medium_sel"] = "13"
		case hasDIY:
			formFields["medium_sel"] = "12"
		case hasChinese:
			formFields["medium_sel"] = "15"
		}
	case "11": // Blu-ray原盘无中文
		switch {
		case hasDIY:
			formFields["medium_sel"] = "2"
		case hasChinese:
			formFields["medium_sel"] = "14"
		}
	}
}

// refinePT52Team 细分 52PT 站内小组：DIY/原盘组（6）与 REMUX/重编码组（7）。
// 说明：站内两个小组同属标准制作组 team.pt52，靠媒介判断归属（Remux / Encode 归重编码组）。
func refinePT52Team(formFields map[string]string) {
	if strings.TrimSpace(formFields["team_sel"]) != "6" {
		return
	}
	switch strings.TrimSpace(formFields["medium_sel"]) {
	case "4", "5", "7": // Blu-ray Remux / 4K UHD Remux / Encode
		formFields["team_sel"] = "7"
	}
}

// collectPT52FormTags 收集 52PT tags[] 已映射的标签值集合。
func collectPT52FormTags(formFields map[string]string) map[string]bool {
	result := map[string]bool{}
	for key, value := range formFields {
		if !strings.HasPrefix(key, "tags[") {
			continue
		}
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		result[trimmed] = true
	}
	return result
}

// resolvePT52IMDbURL 为 52PT 的 url（IMDb链接）字段解析完整链接。
// 说明：publish_target 聚合外链时只读 imdb_link/imdbLink/imdb，转种面板只回传 imdb_id（tt+数字）时
// 站点侧会收不到 IMDb；此处把 imdb_id 一并纳入候选并补成完整链接。
func resolvePT52IMDbURL(input publisher.PublishInput) string {
	if link := strings.TrimSpace(input.IMDbLink); link != "" {
		return link
	}
	uploadData := input.UploadData
	if uploadData == nil {
		return ""
	}

	for _, key := range []string{"imdb_link", "imdbLink", "imdb"} {
		if value := strings.TrimSpace(toStringAny(uploadData[key], "")); value != "" {
			return normalizePT52IMDbURL(value)
		}
	}
	if standardized, ok := uploadData["standardized_params"].(map[string]any); ok && standardized != nil {
		for _, key := range []string{"imdb_link", "imdbLink", "imdb", "imdb_id"} {
			if value := strings.TrimSpace(toStringAny(standardized[key], "")); value != "" {
				return normalizePT52IMDbURL(value)
			}
		}
	}
	if value := strings.TrimSpace(toStringAny(uploadData["imdb_id"], "")); value != "" {
		return normalizePT52IMDbURL(value)
	}
	return ""
}

// normalizePT52IMDbURL 把裸 IMDb ID（tt 前缀或纯数字）补成完整链接，已是链接则原样返回。
func normalizePT52IMDbURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lowered := strings.ToLower(trimmed)
	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return trimmed
	}
	if match := pt52IMDbIDPattern.FindStringSubmatch(trimmed); len(match) > 1 {
		return "https://www.imdb.com/title/tt" + match[1] + "/"
	}
	return ""
}
