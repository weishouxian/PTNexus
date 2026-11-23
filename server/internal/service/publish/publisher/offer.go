package publisher

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
	publishuploader "github.com/pt-nexus/server/internal/service/publish/uploader"
)

// 发种方式取值，与数据库 sites.publish_mode 一一对应。
// 这里单独定义一份而不是 import repository：发布层不该反向依赖数据访问层。
const (
	// PublishModeUpload 正式发种：提交种子文件（默认）。
	PublishModeUpload = 0
	// PublishModeOffer 候选申请：不提交种子文件，站点审核通过后才获得直发权限。
	PublishModeOffer = 1
)

// IsOfferPublishMode 判断目标站是否配置为候选（offer）发种方式。
// 参数/返回：targetInfo 为目标站配置（sites 表整行，含 publish_mode 列）；命中候选模式返回 true。
// 失败场景：配置缺失、字段类型不认识或取值非法时按正式发种处理（返回 false）。
// 说明：候选是例外路径，判断方向必须 fail-safe —— 宁可当正式发种，也不能把有直发权限的账号降级成候选。
// 副作用：无。
func IsOfferPublishMode(targetInfo map[string]any) bool {
	if targetInfo == nil {
		return false
	}
	value, exists := targetInfo["publish_mode"]
	if !exists || value == nil {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case int:
		return typed == PublishModeOffer
	case int8:
		return int(typed) == PublishModeOffer
	case int16:
		return int(typed) == PublishModeOffer
	case int32:
		return int(typed) == PublishModeOffer
	case int64:
		return int(typed) == PublishModeOffer
	case uint8:
		return int(typed) == PublishModeOffer
	case uint16:
		return int(typed) == PublishModeOffer
	case uint32:
		return int(typed) == PublishModeOffer
	case uint64:
		return typed == PublishModeOffer
	case float32:
		return int(typed) == PublishModeOffer
	case float64:
		return int(typed) == PublishModeOffer
	case []byte:
		return strings.TrimSpace(string(typed)) == "1"
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(typed))
		return trimmed == "1" || trimmed == "offer"
	}
	return false
}

// OfferFormAdjuster 是站点级候选字段修正回调：在基础字段组装完成、提交之前调用。
// 用于补齐站点专属规则（如 TTG 的 type 由分类+媒介+分辨率合并计算）。
type OfferFormAdjuster func(input PublishInput, formFields map[string]string)

// 候选表单的默认字段名（NexusPHP 候选页惯例）。站点可用 configs/<site>.yaml 的 offer.fields 覆盖。
const (
	defaultOfferFormPath   = "offer.php"
	defaultOfferSubmitPath = "takeoffer.php"
	offerFieldName         = "name"
	offerFieldPicture      = "picture"
	offerFieldDescription  = "msg"
	offerFieldCategory     = "type"
	offerFieldSeedingEnv   = "seeding_env"
)

var (
	offerBBCodeImagePattern = regexp.MustCompile(`(?is)\[img\](.*?)\[/img\]`)
	offerPlainImagePattern  = regexp.MustCompile(`(?i)https?://[^\s"'\[\]]+\.(?:jpg|jpeg|png|webp)`)
)

// PublishOffer 执行「候选 / 申请上传」发布流程。
//
// 与正式发种的区别：候选表单没有种子文件字段，站点审核通过后用户才获得直发权限。
// 因此这里不读种子文件、不做 dupe 查重；解析出的候选链接也不带直链下载地址。
//
// 参数/返回：input 由 workflow 统一构建；adjust 为可选的站点级字段修正回调。
// 返回候选链接、是否已存在（恒为 false）、提交字段与过程日志。
// 失败场景：站点配置缺失、cookie 为空、字段组装失败、站点拒绝候选时返回 error。
// 副作用：向目标站点发起 GET（候选表单页）与 POST（提交端点）请求。
func PublishOffer(input PublishInput, adjust OfferFormAdjuster) (PublishResult, error) {
	targetName := strings.TrimSpace(input.TargetName)
	if targetName == "" {
		targetName = "目标站点"
	}
	siteCode := strings.TrimSpace(input.SiteCode)
	baseURL := strings.TrimRight(strings.TrimSpace(input.BaseURL), "/")
	cookie := strings.TrimSpace(input.Cookie)

	logLines := make([]string, 0, 6)
	appendLog := func(text string) {
		if trimmed := strings.TrimSpace(text); trimmed != "" {
			logLines = append(logLines, trimmed)
		}
	}
	buildDetail := func() string { return strings.Join(logLines, "\n") }

	if baseURL == "" {
		return PublishResult{}, fmt.Errorf("目标站点缺少 base_url")
	}
	if cookie == "" {
		return PublishResult{}, fmt.Errorf("目标站点缺少 cookie，候选提交需要登录态")
	}

	siteCfg, cfgErr := publishmapping.LoadSitePublishConfig(siteCode)
	if cfgErr != nil {
		return PublishResult{}, fmt.Errorf("加载站点发布配置失败 site=%s err=%w", siteCode, cfgErr)
	}
	offerCfg := publishmapping.SiteOfferConfig{}
	if siteCfg != nil {
		offerCfg = siteCfg.Offer
	}
	formPath := strings.TrimSpace(offerCfg.FormPath)
	if formPath == "" {
		formPath = defaultOfferFormPath
	}
	submitPath := strings.TrimSpace(offerCfg.SubmitPath)
	if submitPath == "" {
		submitPath = defaultOfferSubmitPath
	}

	formFields, buildErr := assembleOfferFormFields(input, siteCfg, adjust)
	if len(formFields) > 0 {
		appendLog(fmt.Sprintf("候选字段: %s", summarizeOfferFields(formFields)))
	}
	if omitted := offerOmittedFieldNames(offerCfg); len(omitted) > 0 {
		appendLog(fmt.Sprintf("候选不提交字段（站点可留空）: %s", strings.Join(omitted, "、")))
	}
	if buildErr != nil {
		return PublishResult{UploadFormFields: formFields, AttemptDetailLog: buildDetail()}, buildErr
	}

	if input.UploadData != nil {
		if ui := strings.TrimSpace(offerToString(input.UploadData["mediainfo"])); ui != "" {
			appendLog("候选不提交种子文件，MediaInfo 仅随简介正文提交")
		}
	}

	formPageURL := baseURL + "/" + strings.TrimLeft(formPath, "/")
	submitURL := baseURL + "/" + strings.TrimLeft(submitPath, "/")

	result, submitErr := publishuploader.TrySubmitOfferForm(
		formPageURL, submitURL, baseURL, cookie, formFields, strings.TrimSpace(offerCfg.ListPath),
	)
	appendLog(result.DetailLog)
	if submitErr != nil {
		return PublishResult{UploadFormFields: formFields, AttemptDetailLog: buildDetail()}, submitErr
	}

	if strings.TrimSpace(result.OfferURL) != "" {
		appendLog(fmt.Sprintf("候选状态：已提交到 %s，等待站点审核", targetName))
		appendLog(fmt.Sprintf("候选链接: %s", result.OfferURL))
	} else {
		appendLog(fmt.Sprintf("候选状态：已提交到 %s，等待站点审核（站点未返回候选链接，可在候选区核对）", targetName))
	}

	return PublishResult{
		PublishURL:       strings.TrimSpace(result.OfferURL),
		UploadFormFields: formFields,
		AttemptDetailLog: buildDetail(),
	}, nil
}

// assembleOfferFormFields 组装候选提交字段（不发起网络请求，便于单测）。
//
// 两种候选表形态都支持：
//   - 精简型（TTG）：只有名称/海报/简介/分类/做种条件，offer.copy_upload_fields 不开启，
//     此时仅提交分类字段，其余维度（媒介/编码/分辨率）不提交；
//   - 完整型（CHDBits）：候选表就是发种表去掉种子文件，开启 copy_upload_fields 后
//     分类/媒介/编码/音频/分辨率/地区/制作组/标签/固定字段全部复用发种映射结果。
//
// 参数/返回：input 为发布输入；siteCfg 为站点发布配置（可为 nil）；adjust 为站点级修正回调。
// 返回最终字段表与错误（名称为空时返回错误，但仍回传已组装字段用于排查）。
// 副作用：无（纯计算）。
func assembleOfferFormFields(input PublishInput, siteCfg *publishmapping.SitePublishConfig, adjust OfferFormAdjuster) (map[string]string, error) {
	offerCfg := publishmapping.SiteOfferConfig{}
	if siteCfg != nil {
		offerCfg = siteCfg.Offer
	}
	resolveField := func(mappingKey, fallback string) string {
		if resolved := strings.TrimSpace(offerCfg.Fields[mappingKey]); resolved != "" {
			return resolved
		}
		return fallback
	}

	// 站点若声明了独立副标题字段（如 CHDBits 的 small_descr），副标题单独提交；
	// 没声明（如 TTG）则沿用「拼进名称的方括号」写法。
	subtitleField := resolveField("subtitle", "")
	nameField := resolveField("name", offerFieldName)
	pictureField := resolveField("picture", offerFieldPicture)
	descriptionField := resolveField("description", offerFieldDescription)
	imdbField := resolveField("imdb_url", "")

	formFields := map[string]string{}
	if name := buildOfferName(input, subtitleField == ""); name != "" {
		formFields[nameField] = name
	}
	if picture := resolveOfferPicture(input.UploadData); picture != "" {
		formFields[pictureField] = applyOfferPictureScheme(picture, offerCfg.PictureScheme)
	}
	if description := strings.TrimSpace(input.Description); description != "" {
		formFields[descriptionField] = description
	}
	if subtitleField != "" {
		if subtitle := cleanOfferSubtitle(input.Subtitle); subtitle != "" {
			formFields[subtitleField] = subtitle
		}
	}
	if imdbField != "" {
		if imdbLink := strings.TrimSpace(input.IMDbLink); imdbLink != "" {
			formFields[imdbField] = imdbLink
		}
	}

	// 维度字段复用正式发种的映射结果（category/medium/codec/audio/resolution/processing/team/标签/static）。
	mapped := publishmapping.ResolveBasicPublishMappings(strings.TrimSpace(input.SiteCode), input.UploadData)
	if offerCfg.CopyUploadFields {
		for key, value := range mapped {
			trimmedKey := strings.TrimSpace(key)
			trimmedValue := strings.TrimSpace(value)
			if trimmedKey == "" || trimmedValue == "" {
				continue
			}
			formFields[trimmedKey] = trimmedValue
		}
	} else {
		// 精简型候选页只接受分类字段：媒介/编码/分辨率在页面上没有对应控件，
		// 提交过去会被站点当成非法参数或直接忽略，反而干扰排查。
		categoryField := resolveField("category", offerFieldCategory)
		for key, value := range mapped {
			if strings.TrimSpace(key) != categoryField {
				continue
			}
			if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
				formFields[categoryField] = trimmedValue
			}
		}
	}

	// 固定值（如 TTG 候选页的做种条件 seeding_env）由站点配置声明。
	// 完整型候选表已通过 static_fields 覆盖同一批字段，这里再写一次是幂等的，也兼容只配 offer.static 的站点。
	for logical, value := range offerCfg.Static {
		field := resolveField(logical, logical)
		if strings.TrimSpace(field) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		formFields[field] = strings.TrimSpace(value)
	}

	if adjust != nil {
		adjust(input, formFields)
	}

	// 站点声明「不填」的字段最后统一删掉：候选表单有些栏目留空才不触发校验，
	// 放在 adjust 之后是为了连站点回调补写的值一起清掉。
	for _, field := range offerOmittedFieldNames(offerCfg) {
		delete(formFields, field)
	}

	if strings.TrimSpace(formFields[nameField]) == "" {
		return formFields, fmt.Errorf("候选名称（%s）为空，无法提交", nameField)
	}
	return formFields, nil
}

// offerOmittedFieldNames 把 offer.omit_fields 归一成实际提交字段名（去空、去重）。
//
// 取值既可以写逻辑字段名（picture），也可以直接写站点字段名：逻辑名会按 offer.fields 换算，
// 这样即使站点把海报栏映射成别的名字（image 等），声明 picture 也能删对。
//
// 参数/返回：offerCfg 为站点候选配置；返回待删除的提交字段名列表（无声明时返回 nil）。
// 副作用：无。
func offerOmittedFieldNames(offerCfg publishmapping.SiteOfferConfig) []string {
	if len(offerCfg.OmitFields) == 0 {
		return nil
	}
	result := make([]string, 0, len(offerCfg.OmitFields))
	seen := make(map[string]struct{}, len(offerCfg.OmitFields))
	for _, raw := range offerCfg.OmitFields {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		field := trimmed
		if mapped := strings.TrimSpace(offerCfg.Fields[trimmed]); mapped != "" {
			field = mapped
		}
		if _, exists := seen[field]; exists {
			continue
		}
		seen[field] = struct{}{}
		result = append(result, field)
	}
	return result
}

// buildOfferName 组装候选名称。
//
// appendSubtitle 为 true（站点没有独立副标题字段，如 TTG）时按「英文名 [中文名]」拼接；
// 为 false（站点有独立副标题栏，如 CHDBits 的 small_descr）时只提交主标题，副标题另栏提交。
func buildOfferName(input PublishInput, appendSubtitle bool) string {
	title := strings.Join(strings.Fields(strings.TrimSpace(input.Title)), " ")
	subtitle := strings.Join(strings.Fields(strings.TrimSpace(input.Subtitle)), " ")
	if title == "" {
		return strings.TrimSpace(strings.NewReplacer("[", "", "]", "").Replace(subtitle))
	}
	if !appendSubtitle || subtitle == "" {
		return title
	}
	if strings.Contains(title, "[") {
		// 主标题里已经有中文名，候选再拼会重复
		return title
	}
	subtitle = strings.NewReplacer("[", "", "]", "").Replace(subtitle)
	if strings.TrimSpace(subtitle) == "" {
		return title
	}
	return title + " [" + strings.TrimSpace(subtitle) + "]"
}

// cleanOfferSubtitle 归一独立副标题栏的取值：剥掉站点不认的方括号、折叠空白。
// 说明：CHDBits 等站的 small_descr 会由站点自己按副标题样式展示，再包一层 [] 会显示成两层括号。
// 副作用：无。
func cleanOfferSubtitle(subtitle string) string {
	trimmed := strings.NewReplacer("[", "", "]", "").Replace(strings.TrimSpace(subtitle))
	return strings.Join(strings.Fields(trimmed), " ")
}

// resolveOfferPicture 从发布数据里取海报 URL（支持 BBCode [img] 与纯 URL 两种形态）。
func resolveOfferPicture(uploadData map[string]any) string {
	if uploadData == nil {
		return ""
	}
	candidates := make([]string, 0, 3)
	for _, key := range []string{"poster", "poster_url", "cover"} {
		if value := strings.TrimSpace(offerToString(uploadData[key])); value != "" {
			candidates = append(candidates, value)
		}
	}
	intro, _ := uploadData["intro"].(map[string]any)
	if intro != nil {
		if value := strings.TrimSpace(offerToString(intro["poster"])); value != "" {
			candidates = append(candidates, value)
		}
	}

	for _, candidate := range candidates {
		if match := offerBBCodeImagePattern.FindStringSubmatch(candidate); len(match) > 1 {
			if url := strings.TrimSpace(match[1]); url != "" {
				return url
			}
		}
		if match := offerPlainImagePattern.FindString(candidate); match != "" {
			return strings.TrimSpace(match)
		}
	}
	return ""
}

// applyOfferPictureScheme 按站点要求改写海报地址的协议前缀。
//
// 背景：彩虹岛（CHDBits）候选提交端点的海报校验只认 http:// 开头的地址 ——
// 同一张 jpg，https:// 会被回「图片必须是jpg, gif 或png格式。」，换成 http:// 才放行，
// 站点并不回源检查图片真实格式，所以报错文案与图片扩展名无关，很容易被误判成「图片不是 jpg」。
//
// 参数/返回：pictureURL 为解析出的海报地址；scheme 为站点声明的前缀（http/https），空串表示原样返回。
// 失败场景：地址为空或不是 http(s) 绝对地址时原样返回（相对路径、data: 等不做改写）。
// 副作用：无。
func applyOfferPictureScheme(pictureURL, scheme string) string {
	trimmed := strings.TrimSpace(pictureURL)
	target := strings.ToLower(strings.TrimSpace(scheme))
	if trimmed == "" {
		return trimmed
	}
	lowered := strings.ToLower(trimmed)
	switch target {
	case "http":
		if strings.HasPrefix(lowered, "https://") {
			return "http://" + trimmed[len("https://"):]
		}
	case "https":
		if strings.HasPrefix(lowered, "http://") {
			return "https://" + trimmed[len("http://"):]
		}
	}
	return trimmed
}

// summarizeOfferFields 生成候选字段摘要（按字段名排序，长文本截断，避免日志被简介正文淹没）。
// 参数/返回：formFields 为最终提交字段；返回单行摘要。
// 副作用：无。
func summarizeOfferFields(formFields map[string]string) string {
	if len(formFields) == 0 {
		return ""
	}
	keys := make([]string, 0, len(formFields))
	for key := range formFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := strings.TrimSpace(formFields[key])
		if value == "" {
			continue
		}
		// 简介/正文往往是整段 BBCode，日志里只留开头用于确认「有没有带上」。
		if len([]rune(value)) > 60 {
			value = truncateRunes(value, 60)
		}
		parts = append(parts, fmt.Sprintf("%s=%s", key, value))
	}
	return strings.Join(parts, " | ")
}

// offerToString 把任意值转成字符串（nil 返回空串）。
func offerToString(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprintf("%v", value)
}

// truncateRunes 按字符截断文本并追加省略号。
func truncateRunes(text string, limit int) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}
