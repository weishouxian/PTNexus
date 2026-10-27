package sites

import (
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

const ssdPublishLogModule = "发布-SSD"

// ssdPublisher 定义 SSD（不可说，springsunday.net）在公共表单发布流程上的差异步骤。
// 站点形态：海报（url_poster）、截图（url_vimages）、媒体信息（Media_BDInfo）是独立必填字段，
// 简介（descr）只放声明区块，因此不能复用默认的「全部拼进 descr」描述拼装。
type ssdPublisher struct {
	publicSiteDefaults
}

// PublishSSD 执行 SSD 站点特殊发布流程（海报/截图/媒体信息字段分离，直接正式发种）。
// 参数/返回：input 提供站点信息、发布数据与通用字段；返回发布详情页 URL、是否疑似“种子已存在”、用于自动编辑的表单字段，以及发布过程日志。
// 失败场景：同 Public 发布器；字段缺失会自动降级为空，不中断发布。
// 副作用：读取本地种子文件并向目标站点发起上传请求；可选写入 data/tmp/torrents 参数落盘。
func PublishSSD(input publisher.PublishInput) (publisher.PublishResult, error) {
	return publishWithPublicSite(input, ssdPublisher{})
}

func (ssdPublisher) AttemptPrefix(input publisher.PublishInput) string {
	return "检测到 SSD（不可说）站点：启用海报/截图/媒体信息字段分离流程"
}

// BuildDescription 简介只保留声明区块（statement），海报/截图/媒体信息不进 descr。
func (ssdPublisher) BuildDescription(input publisher.PublishInput) string {
	return strings.TrimSpace(resolveUploadSection(input.UploadData, "statement"))
}

// AdjustFormFields 执行站点字段纠偏：
//  1. 标题规范：SSD 要求主标题用「点号分隔」形态，源标题可能是空格分隔
//     （如 "Operation Carwash ... 1080p WEB-DL H.264 AAC 2.0-CSWEB"），
//     在此把主标题字段里的空格统一替换为点号。只改主标题，不动副标题/简介等其他字段。
//  2. 国配标签：产地为中国（大陆/香港/台湾）时移除「国配」checkbox（见 applySSDMandarinRule）。
func (ssdPublisher) AdjustFormFields(input publisher.PublishInput, formFields map[string]string) {
	if formFields == nil {
		return
	}
	siteCfg, _ := publishmapping.LoadSitePublishConfig(strings.TrimSpace(input.SiteCode))
	titleField := "name"
	if siteCfg != nil {
		titleField = firstNonEmpty(siteCfg.FormFields["name"], titleField)
	}
	if strings.TrimSpace(formFields[titleField]) != "" {
		formFields[titleField] = normalizeSSDTitle(formFields[titleField])
	}
	applySSDMandarinRule(input.UploadData, siteCfg, formFields)
}

// applySSDMandarinRule 在产地为中国（大陆/香港/台湾）时移除「国配」标签。
//
// 站点把「国配」放在独立 checkbox `mandarin` 上（value=1，页面标签「国配 / 国语配音」）。
// 华语片的中文音轨是原声、不是配音，再打「国配」属于误标，故按产地直接剔除该字段。
// 字段名优先从站点 checkbox_tags 里「国配/国语」对应项读取，避免站点改名后硬编码失效。
// 失败场景：uploadData 无标准化参数、产地非中国（大陆/香港/台湾）时直接返回。
// 副作用：命中并已提交该字段时从 formFields 删除（未提交则不动）。
func applySSDMandarinRule(uploadData map[string]any, siteCfg *publishmapping.SitePublishConfig, formFields map[string]string) {
	if formFields == nil || !isSSDChineseOrigin(uploadData) {
		return
	}
	field := "mandarin"
	if siteCfg != nil {
		for _, key := range []string{"tag.国配", "tag.国语", "国配", "国语"} {
			if mapped := strings.TrimSpace(siteCfg.CheckboxTags[key]); mapped != "" {
				field = mapped
				break
			}
		}
	}
	if _, exists := formFields[field]; !exists {
		return
	}
	delete(formFields, field)
	logx.Infof(ssdPublishLogModule, "产地为中国（含港台），已移除国配标签 field=%s", field)
}

// ssdChineseOriginKeys 是「产地属于中国」的标准化值集合（大陆 + 香港 + 台湾）。
// 香港/台湾在站点上是独立产地项，但按业务口径都算华语原声区，不额外打「国配」。
var ssdChineseOriginKeys = map[string]struct{}{
	"source.china":           {},
	"source.hongkong":        {},
	"source.taiwan":          {},
	"source.hongkong_taiwan": {},
}

// isSSDChineseOrigin 判断标准化产地是否属于中国（大陆/香港/台湾）。
// 说明：键值大小写不敏感；source.hongkong_taiwan 是港台复合产地，一并纳入。
func isSSDChineseOrigin(uploadData map[string]any) bool {
	if uploadData == nil {
		return false
	}
	standardized, _ := uploadData["standardized_params"].(map[string]any)
	if standardized == nil {
		return false
	}
	key := strings.ToLower(strings.TrimSpace(toStringAny(standardized["source"], "")))
	if key == "" {
		return false
	}
	_, ok := ssdChineseOriginKeys[key]
	return ok
}

// normalizeSSDTitle 把标题里的空格替换为点号，并压缩连续空白。
func normalizeSSDTitle(title string) string {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return trimmed
	}
	// 归一化各类空白（含不换行空格）为普通空格，再统一替换为点号。
	normalized := strings.Join(strings.Fields(trimmed), " ")
	return strings.ReplaceAll(normalized, " ", ".")
}

// BuildExtraFormFields 构造 SSD 独立的海报（url_poster）、截图（url_vimages）、媒体信息（Media_BDInfo）字段。
// 这三个字段在通用 Public 发布器里只有 media info 会被填进 technical_info，海报与截图不会自动填充，
// 故在此按站点字段名独立提交。
func (ssdPublisher) BuildExtraFormFields(input publisher.PublishInput) (map[string]string, error) {
	uploadData := input.UploadData
	siteCfg, _ := publishmapping.LoadSitePublishConfig(strings.TrimSpace(input.SiteCode))
	resolveFieldName := func(mappingKey string, fallback string) string {
		if siteCfg == nil {
			return fallback
		}
		for _, key := range []string{mappingKey, fallback} {
			if resolved := strings.TrimSpace(siteCfg.FormFields[key]); resolved != "" {
				return resolved
			}
		}
		return fallback
	}

	extra := map[string]string{}

	// 海报：从 poster/cover 区块取第一张图，剥掉 [img] BBCode。
	if poster := resolveSSDPoster(input); poster != "" {
		extra[resolveFieldName("picture", "url_poster")] = poster
	}

	// 截图：从 screenshots 区块提取全部图片 URL，每行一个（站点 textarea 一图一行）。
	if shots := resolveSSDScreenshots(uploadData); shots != "" {
		extra[resolveFieldName("screenshots", "url_vimages")] = shots
	}

	// 媒体信息：独立 textarea，取 MediaInfo 原文（优先级 input.MediaInfo > uploadData.mediainfo）。
	if media := strings.TrimSpace(firstNonEmpty(input.MediaInfo, toStringAny(uploadData["mediainfo"], ""))); media != "" {
		extra[resolveFieldName("technical_info", "Media_BDInfo")] = media
	}

	return extra, nil
}

// resolveSSDPoster 提取海报字段值（纯 URL，去掉 [img] BBCode 包裹）。
func resolveSSDPoster(input publisher.PublishInput) string {
	candidates := []string{
		resolveUploadSection(input.UploadData, "poster"),
		resolveUploadSection(input.UploadData, "cover"),
	}
	for _, candidate := range candidates {
		images := extractImageURLsFromText(candidate)
		if len(images) > 0 {
			return strings.TrimSpace(images[0])
		}
	}
	return ""
}

// resolveSSDScreenshots 提取截图字段值：全部图片 URL，每行一个。
func resolveSSDScreenshots(uploadData map[string]any) string {
	raw := resolveUploadSection(uploadData, "screenshots")
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	images := extractImageURLsFromText(raw)
	if len(images) == 0 {
		return ""
	}
	return strings.Join(images, "\n")
}
