package mapping

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/pt-nexus/server/internal/config"
	"gopkg.in/yaml.v3"
)

// SitePublishConfig 表示站点发布映射配置。
type SitePublishConfig struct {
	SourcePath         string
	UploadFileField    string
	FormFields         map[string]string
	Mappings           map[string]map[string]string
	GenreOptionsByType map[string][]string
	Anonymous          SiteAnonymousConfig
	// TagRequires 定义标签联动规则：命中 key 标签时必须同时提交 value 中的标签。
	TagRequires map[string][]string
	// StaticFields 定义站点固定字段：无语义映射来源的字段（如必填的"来源性质"select）直接提交固定值。
	StaticFields map[string]string
	// CheckboxTags 定义"独立 checkbox 型"标签字段：标准标签值 → 站点 checkbox 字段名。
	// 部分老 NexusPHP 站点每个标签是独立命名的 checkbox（value=yes），无法用 tags[] 数组表达。
	CheckboxTags map[string]string
	// CheckboxTagValue 独立 checkbox 命中时提交的值，默认 yes。
	CheckboxTagValue string
	// TypeByTag 定义"标签决定分类"规则：命中标准标签时，分类字段固定用对应标准类型值。
	// 用于站点硬性要求（如 AGSV 带"动画"标签的种子必须选"动漫"分类）。
	TypeByTag map[string]string
	// DupeCheck 定义 dupe 校验的站点参数：检索维度到搜索参数的命名规则。
	// 为空表示该站点未实现 dupe 校验（即使站点开了开关也不会执行）。
	DupeCheck SiteDupeCheckConfig
	// InfoSourceTag 非空时，发布前会在种子 info 字典内写入 source=该值，
	// 使目标站种子的 infohash 与源站不同（下载器里成为独立任务，标签/统计互不覆盖）。
	// 文件列表与 piece 数据保持不变，可指向同一份文件直接校验做种。
	InfoSourceTag string
	// Offer 候选（申请上传）表单配置。
	// 是否走候选**不由 YAML 决定**：权限是按账号的，同一站点 code 可能挂多个账号，
	// 因此开关存在数据库 sites.publish_mode，由「站点设置」页维护；YAML 只描述候选表单长什么样。
	Offer SiteOfferConfig
}

// SiteOfferConfig 描述站点「候选/申请上传」表单的提交参数。
//
// 候选表单与正式发种表单是两套完全不同的页面：没有种子文件字段、提交端点是 takeoffer.php 之类，
// 字段名也不同（简介叫 msg、海报叫 picture、做种条件叫 seeding_env）。因此单独一张表描述。
type SiteOfferConfig struct {
	// FormPath 候选表单页路径（相对 base_url），默认 offer.php。
	FormPath string
	// SubmitPath 候选提交端点路径，默认 takeoffer.php。
	SubmitPath string
	// ListPath 候选区列表页路径（如 TTG 的 viewoffers.php），用于提交成功后按名称回查候选链接。
	// 站点提交端点只回 302 且 body 不带链接时，这里配了才会多走一次 GET；留空则跳过回查。
	ListPath string
	// Fields 逻辑字段 → 站点字段名，缺省用内置默认（name/picture/msg/type/seeding_env）。
	// 常用逻辑字段：name / picture / description / subtitle / imdb_url / category / seeding_env。
	Fields map[string]string
	// Static 固定提交值（如 seeding_env 的做种条件），按逻辑字段名声明。
	Static map[string]string
	// CopyUploadFields 为 true 时，候选表单视为「发种表单去掉种子文件」的同一张表：
	// 分类、媒介、编码、音频、分辨率、地区、制作组、标签、static_fields 全部复用发种映射结果。
	// CHDBits（offers.php?add_offer=1）属于这类；TTG 候选页只有 5 个字段，保持 false 只提交分类。
	CopyUploadFields bool
	// PictureScheme 指定海报字段强制使用的协议前缀，取值 "http" / "https"；留空表示原样提交。
	// 彩虹岛实测（2026-11-23）：候选提交端点的海报校验只认 http:// 开头的地址，
	// https:// 会被判成「图片必须是jpg, gif 或png格式。」（同一张 jpg，换成 http 才放行；
	// 报错与图片真实格式无关，站点并不回源检查）。留空的站点（TTG 等）保持原样。
	PictureScheme string
	// OmitFields 声明「根本不提交」的字段，取值可写逻辑字段名（picture / description …）
	// 也可直接写站点字段名，装配完成后统一删除。
	// 用于「候选表单留空即可、填了反而要过校验」的栏目：彩虹岛的海报栏就是典型 ——
	// 留空能过，填了还要满足 http:// 前缀规则，所以这里直接不提交比降级协议更稳。
	OmitFields []string
}

// SiteDupeCheckConfig 描述站点 dupe 检索所需的参数命名规则。
//
// 搜索页的筛选参数名与上传表单字段名通常不同（例如上传用 medium_sel，搜索用 medium12），
// 且维度取值已是站点值（由 mappings 换算而来），因此这里只声明「维度 → 参数名模板」的映射，
// 取值直接复用 mappings 的结果，避免在两处各配一份站点取值。
type SiteDupeCheckConfig struct {
	// Enabled 标记该站点是否实现 dupe 校验。
	Enabled bool
	// SearchPath 为站点搜索页路径（相对 base_url），如 torrents.php。
	SearchPath string
	// UploadPath 为站点上传页路径（相对 base_url），如 upload.php。
	// 仅当站点某些维度的映射写作 @index:N（按上传页下拉框选项索引取值）时才需要：
	// dupe 校验发生在上传页被解析之前，此刻只有索引占位符，需抓上传页才能换算成真实选项值。
	// 实测 hdhome 的 medium/分辨率/视频编码/音频编码 都是这种写法。
	UploadPath string
	// SearchAreaParam 为搜索范围参数名（多数 NexusPHP 站点为 search_area）。
	SearchAreaParam string
	// SearchAreaValue 为搜索范围取值（单一值，供只支持一种检索方式的站点使用）。
	SearchAreaValue string
	// SearchAreas 按检索类型给出各自的搜索范围取值，键为 douban / imdb / title。
	// 站点实测差异很大：人人 search_area=2 豆瓣与 IMDb 都命中；幸运只有 0/1/3/4（无豆瓣范围）；
	// 猫站与我堡有豆瓣范围（5），家园与彩虹岛没有。因此把「哪种检索可用」交给配置声明。
	// 非空时优先于 SearchAreaValue。
	SearchAreas map[string]string
	// ParamTemplates 定义各维度的搜索参数名模板，支持 {value} 占位符。
	// 例如 medium: "medium{value}" 会把站点取值 12 渲染为 medium12=1。
	ParamTemplates map[string]string
}

// SiteAnonymousConfig 表示站点匿名发布字段配置。
type SiteAnonymousConfig struct {
	Field            string
	EnabledValue     string
	DisabledValue    string
	OmitWhenDisabled bool
}

var publishConfigCache sync.Map

// LoadSitePublishConfig 读取并缓存站点发布配置。
func LoadSitePublishConfig(siteCode string) (*SitePublishConfig, error) {
	trimmed := strings.ToLower(strings.TrimSpace(siteCode))
	if trimmed == "" {
		return nil, errors.New("site code 为空")
	}
	if cached, ok := publishConfigCache.Load(trimmed); ok {
		if cfg, ok := cached.(*SitePublishConfig); ok {
			return cfg, nil
		}
	}

	paths := config.ResolveRuntimePaths()
	candidates := []string{
		filepath.Join(paths.BaseDir, "configs", trimmed+".yaml"),
	}

	var data []byte
	var readErr error
	for _, candidate := range candidates {
		content, err := os.ReadFile(candidate)
		if err == nil {
			data = content
			readErr = nil
			break
		}
		readErr = err
	}
	if len(data) == 0 {
		if readErr == nil {
			readErr = errors.New("文件不存在")
		}
		return nil, fmt.Errorf("读取站点配置失败: %w", readErr)
	}

	raw, err := unmarshalYAMLMapAllowDuplicateKeys(data)
	if err != nil {
		return nil, err
	}
	cfg := &SitePublishConfig{
		SourcePath:         strings.TrimSpace(candidatePath(candidates, data)),
		UploadFileField:    strings.TrimSpace(toStringAny(raw["upload_file_field"])),
		FormFields:         mapStringMap(raw["form_fields"]),
		Mappings:           mapStringNestedMap(raw["mappings"]),
		GenreOptionsByType: mapStringSliceNestedMap(raw["genre_options_by_type"]),
		Anonymous:          mapAnonymousConfig(raw["anonymous"]),
		TagRequires:        mapStringSliceNestedMap(raw["tag_requires"]),
		StaticFields:       mapStringMap(raw["static_fields"]),
		CheckboxTags:       mapStringMap(raw["checkbox_tags"]),
		CheckboxTagValue:   strings.TrimSpace(toStringAny(raw["checkbox_tag_value"])),
		TypeByTag:          mapStringMap(raw["type_by_tag"]),
		DupeCheck:          mapDupeCheckConfig(raw["dupe_check"]),
		InfoSourceTag:      strings.TrimSpace(toStringAny(raw["info_source_tag"])),
		Offer:              mapOfferConfig(raw["offer"]),
	}
	publishConfigCache.Store(trimmed, cfg)
	return cfg, nil
}

func candidatePath(candidates []string, data []byte) string {
	if len(data) == 0 {
		return ""
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func mapAnonymousConfig(value any) SiteAnonymousConfig {
	item, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]interface{}); ok {
			item = direct
		} else {
			return SiteAnonymousConfig{}
		}
	}
	return SiteAnonymousConfig{
		Field:            strings.TrimSpace(toStringAny(item["field"])),
		EnabledValue:     strings.TrimSpace(toStringAny(item["enabled_value"])),
		DisabledValue:    strings.TrimSpace(toStringAny(item["disabled_value"])),
		OmitWhenDisabled: toBoolAny(item["omit_when_disabled"]),
	}
}

// mapDupeCheckConfig 解析站点 dupe 校验配置。
// 参数/返回：value 为 YAML 中的 dupe_check 节点；返回解析后的配置（缺省时 Enabled 为 false）。
// 副作用：无。
func mapDupeCheckConfig(value any) SiteDupeCheckConfig {
	item, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]interface{}); ok {
			item = direct
		} else {
			return SiteDupeCheckConfig{}
		}
	}
	templates := mapStringMap(item["param_templates"])
	if !toBoolAny(item["enabled"]) || len(templates) == 0 {
		return SiteDupeCheckConfig{}
	}
	return SiteDupeCheckConfig{
		Enabled:         true,
		SearchPath:      strings.TrimSpace(toStringAny(item["search_path"])),
		UploadPath:      strings.TrimSpace(toStringAny(item["upload_path"])),
		SearchAreaParam: strings.TrimSpace(toStringAny(item["search_area_param"])),
		SearchAreaValue: strings.TrimSpace(toStringAny(item["search_area_value"])),
		SearchAreas:     mapStringMap(item["search_areas"]),
		ParamTemplates:  templates,
	}
}

// mapOfferConfig 解析站点候选（offer）提交配置。
// 参数/返回：value 为 YAML 中的 offer 节点；返回解析后的配置，缺省路径由调用方兜底。
// 副作用：无。
func mapOfferConfig(value any) SiteOfferConfig {
	item, ok := value.(map[string]any)
	if !ok {
		return SiteOfferConfig{}
	}
	return SiteOfferConfig{
		FormPath:         strings.TrimSpace(toStringAny(item["form_path"])),
		SubmitPath:       strings.TrimSpace(toStringAny(item["submit_path"])),
		ListPath:         strings.TrimSpace(toStringAny(item["list_path"])),
		Fields:           mapStringMap(item["fields"]),
		Static:           mapStringMap(item["static"]),
		CopyUploadFields: toBoolAny(item["copy_upload_fields"]),
		PictureScheme:    strings.TrimSpace(toStringAny(item["picture_scheme"])),
		OmitFields:       toStringSlice(item["omit_fields"]),
	}
}

func mapStringMap(value any) map[string]string {
	result := map[string]string{}
	item, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]interface{}); ok {
			for key, raw := range direct {
				text := strings.TrimSpace(toStringAny(raw))
				if text != "" {
					result[key] = text
				}
			}
		}
		return result
	}
	for key, raw := range item {
		text := strings.TrimSpace(toStringAny(raw))
		if text != "" {
			result[key] = text
		}
	}
	return result
}

func mapStringNestedMap(value any) map[string]map[string]string {
	result := map[string]map[string]string{}
	item, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]interface{}); ok {
			for key, raw := range direct {
				result[key] = mapStringMap(raw)
			}
		}
		return result
	}
	for key, raw := range item {
		result[key] = mapStringMap(raw)
	}
	return result
}

func mapStringSliceNestedMap(value any) map[string][]string {
	result := map[string][]string{}
	item, ok := value.(map[string]any)
	if !ok {
		if direct, ok := value.(map[string]interface{}); ok {
			for key, raw := range direct {
				result[key] = toStringSlice(raw)
			}
		}
		return result
	}
	for key, raw := range item {
		result[key] = toStringSlice(raw)
	}
	return result
}

func toStringSlice(value any) []string {
	switch typed := value.(type) {
	case nil:
		return nil
	case []string:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if trimmed := strings.TrimSpace(toStringAny(item)); trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	default:
		if trimmed := strings.TrimSpace(toStringAny(value)); trimmed != "" {
			return []string{trimmed}
		}
		return nil
	}
}

// PickMappedValue 将标准值映射为站点字段值。
func PickMappedValue(mapping map[string]string, standardized string) string {
	trimmed := strings.TrimSpace(standardized)
	if trimmed == "" {
		return ""
	}
	if len(mapping) == 0 {
		return ""
	}
	if mapped, ok := mapping[trimmed]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped)
	}
	if mapped, ok := mapping["default"]; ok && strings.TrimSpace(mapped) != "" {
		return strings.TrimSpace(mapped)
	}
	return ""
}

func unmarshalYAMLMapAllowDuplicateKeys(data []byte) (map[string]any, error) {
	document := yaml.Node{}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return map[string]any{}, nil
	}
	value, err := yamlNodeToAny(document.Content[0])
	if err != nil {
		return nil, err
	}
	raw, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("站点配置根节点不是对象")
	}
	return raw, nil
}

func yamlNodeToAny(node *yaml.Node) (any, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return map[string]any{}, nil
		}
		return yamlNodeToAny(node.Content[0])
	case yaml.MappingNode:
		result := map[string]any{}
		for idx := 0; idx+1 < len(node.Content); idx += 2 {
			keyNode := node.Content[idx]
			valueNode := node.Content[idx+1]
			key := strings.TrimSpace(keyNode.Value)
			if key == "" {
				continue
			}
			value, err := yamlNodeToAny(valueNode)
			if err != nil {
				return nil, err
			}
			// 对齐 Python yaml.safe_load：重复键后值覆盖前值。
			result[key] = value
		}
		return result, nil
	case yaml.SequenceNode:
		result := make([]any, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlNodeToAny(child)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.ScalarNode:
		return yamlScalarToAny(node), nil
	case yaml.AliasNode:
		if node.Alias == nil {
			return nil, nil
		}
		return yamlNodeToAny(node.Alias)
	default:
		return nil, nil
	}
}

func yamlScalarToAny(node *yaml.Node) any {
	value := strings.TrimSpace(node.Value)

	switch node.Tag {
	case "!!null":
		return nil
	case "!!bool":
		if parsed, err := strconv.ParseBool(strings.ToLower(value)); err == nil {
			return parsed
		}
		return strings.EqualFold(value, "true")
	case "!!int":
		if parsed, err := strconv.ParseInt(value, 0, 64); err == nil {
			return parsed
		}
		return value
	case "!!float":
		if parsed, err := strconv.ParseFloat(value, 64); err == nil {
			return parsed
		}
		return value
	default:
		return node.Value
	}
}

func toStringAny(value any) string {
	switch typed := value.(type) {
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
		if value == nil {
			return ""
		}
		return fmt.Sprintf("%v", value)
	}
}

func toBoolAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		lower := strings.ToLower(strings.TrimSpace(typed))
		return lower == "1" || lower == "true" || lower == "yes" || lower == "y"
	case int:
		return typed != 0
	case int8:
		return typed != 0
	case int16:
		return typed != 0
	case int32:
		return typed != 0
	case int64:
		return typed != 0
	case uint:
		return typed != 0
	case uint8:
		return typed != 0
	case uint16:
		return typed != 0
	case uint32:
		return typed != 0
	case uint64:
		return typed != 0
	case float32:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}
