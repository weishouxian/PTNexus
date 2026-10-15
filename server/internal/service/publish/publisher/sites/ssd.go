package sites

import (
	"strings"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
	"github.com/pt-nexus/server/internal/service/publish/publisher"
)

// ssdPublisher 定义 SSD（不可说，springsunday.net）在公共表单发布流程上的差异步骤。
// 站点形态：海报（url_poster）、截图（url_vimages）、媒体信息（Media_BDInfo）是独立必填字段，
// 简介（descr）只放声明区块，因此不能复用默认的「全部拼进 descr」描述拼装。
type ssdPublisher struct {
	publicSiteDefaults
}

// PublishSSD 执行 SSD 站点特殊发布流程（海报/截图/媒体信息字段分离，候选提交）。
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
