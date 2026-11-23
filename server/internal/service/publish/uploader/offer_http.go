package uploader

import (
	"bytes"
	"fmt"
	"html"
	"io"
	"net/http"
	neturl "net/url"
	"regexp"
	"strings"
	"time"
)

var (
	// 候选链接形态：NexusPHP 站点常见 offers.php?id=N / offer.php?id=N，
	// TTG 实际是 offer.php?vtype=for&id=N（是 offer.php 单数，offers.php 会 404）。
	// 这里捕获整条 href 而不是只取 id，交给 extractOfferPublishURL 原样保留路径与参数。
	reOfferDetailID = regexp.MustCompile(`(?is)offers?\.php\?[^"'\s<>]*\bid=(\d+)`)
	// reOfferRelativeDetail 匹配「相对详情链接」：CHDBits 的候选列表行是
	// <a href="?id=57326&amp;off_details=1">，没有文件名，只有查询串。
	reOfferRelativeDetail = regexp.MustCompile(`(?is)href\s*=\s*["']?\?id=(\d+)(?:&amp;|&)off_details=1`)
	// reOfferBareQuery 匹配「Location 里只有查询串」的形态：?id=57326&off_details=1。
	// 只对以 ? 开头的输入使用，避免在整页 HTML 里抓到列表第一条。
	reOfferBareQuery = regexp.MustCompile(`(?is)^\?id=(\d+)(?:&amp;|&)off_details=1`)
	reOfferSuccess   = regexp.MustCompile(`(?is)申请成功|候选成功|提交成功|提交已成功|等待审核|待审核|等待.*审核`)
	// reHTMLTableRow 用于按行切分候选列表页：一行对应一个候选，避免跨行误命中别人的链接。
	reHTMLTableRow = regexp.MustCompile(`(?is)<tr\b`)
)

// OfferSubmitResult 描述候选提交结果。
type OfferSubmitResult struct {
	// OfferURL 站点返回的候选详情链接；站点只提示成功而未给链接时为空串。
	OfferURL string
	// DetailLog 提交过程细节日志，供 workflow 汇总返回给前端。
	DetailLog string
}

// TrySubmitOfferForm 提交一次站点「候选/申请上传」表单（不带种子文件）。
//
// 参数/返回：formPageURL 为候选表单页（GET 用于取隐藏字段），submitURL 为提交端点，
// baseURL/cookie 为站点信息，formFields 为业务字段；返回候选链接与过程日志。
// 失败场景：读取表单页失败、HTTP 请求失败、站点返回拒绝提示时返回 error（错误信息里带站点原因）。
// 副作用：向目标站点发起 GET + POST 请求。
func TrySubmitOfferForm(formPageURL, submitURL, baseURL, cookie string, formFields map[string]string, listPath string) (OfferSubmitResult, error) {
	formPageURL = strings.TrimSpace(formPageURL)
	submitURL = strings.TrimSpace(submitURL)
	detailLines := []string{
		"正在提交候选申请（不上传种子文件）...",
		fmt.Sprintf("候选表单页: %s", formPageURL),
		fmt.Sprintf("提交端点: %s", submitURL),
	}
	buildDetail := func() string { return strings.Join(detailLines, "\n") }

	client := newUploadHTTPClient()
	mergedFields := mergeOfferFormHiddenFields(client, formPageURL, cookie, formFields, &detailLines)
	if len(mergedFields) == 0 {
		detailLines = append(detailLines, "候选表单字段为空，放弃提交")
		return OfferSubmitResult{DetailLog: buildDetail()}, fmt.Errorf("候选表单字段为空")
	}

	form := neturl.Values{}
	for key, value := range mergedFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		form.Set(key, value)
	}
	payload := []byte(form.Encode())

	var resp *http.Response
	var err error
	for attempt := 1; attempt <= uploadNetworkRetryAttempts; attempt++ {
		req, buildErr := http.NewRequest(http.MethodPost, submitURL, bytes.NewReader(payload))
		if buildErr != nil {
			detailLines = append(detailLines, fmt.Sprintf("创建 HTTP 请求失败: %v", buildErr))
			return OfferSubmitResult{DetailLog: buildDetail()}, buildErr
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
		req.Header.Set("Cookie", cookie)
		if formPageURL != "" {
			req.Header.Set("Referer", formPageURL)
		}

		resp, err = client.Do(req)
		if err == nil {
			break
		}
		detailLines = append(detailLines, fmt.Sprintf("请求失败 (第 %d/%d 次): %v", attempt, uploadNetworkRetryAttempts, err))
		if !ShouldRetryUploadNetworkError(err) || attempt >= uploadNetworkRetryAttempts {
			return OfferSubmitResult{DetailLog: buildDetail()}, err
		}
		backoff := time.Duration(attempt) * 2 * time.Second
		detailLines = append(detailLines, fmt.Sprintf("检测到网络波动，%.0f 秒后重试...", backoff.Seconds()))
		time.Sleep(backoff)
	}
	if err != nil {
		return OfferSubmitResult{DetailLog: buildDetail()}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	bodyText := string(respBody)

	detailLines = append(detailLines, fmt.Sprintf("响应状态: %d %s", resp.StatusCode, strings.TrimSpace(resp.Status)))

	if isUploadForbiddenStatus(resp.StatusCode) {
		err = buildUploadForbiddenError(resp.StatusCode, bodyText, extractUploadNotice(bodyText))
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return OfferSubmitResult{DetailLog: buildDetail()}, err
	}

	pageTitle := extractHTMLTitleText(bodyText)
	if strings.TrimSpace(pageTitle) != "" {
		detailLines = append(detailLines, fmt.Sprintf("页面标题: %s", pageTitle))
	}
	notice := extractUploadNotice(bodyText)

	// 失败判定：标题或提示明确表达失败/拒绝。NexusPHP 候选失败页形如
	// <h1>申请失败！</h1><p>don't leave any fields blank</p>，extractUploadNotice 已能拼出「标题+原因」。
	if notice != "" && (containsUploadNoticeErrorKeyword(pageTitle) || containsUploadNoticeErrorKeyword(notice)) {
		err = fmt.Errorf("候选提交被站点拒绝: %s", notice)
		detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
		return OfferSubmitResult{DetailLog: buildDetail()}, err
	}

	// 站点提交成功后通常是 302，真正有用的信息在 Location 里（body 往往只有页脚）。
	// 上传客户端刻意不跟随重定向（http.ErrUseLastResponse），所以必须自己看 Location。
	location := strings.TrimSpace(resp.Header.Get("Location"))
	if location != "" {
		detailLines = append(detailLines, fmt.Sprintf("Location: %s", redactURLQuery(location)))
	}

	submittedName := offerSubmittedName(formFields)
	// 解析顺序很重要：站点把「候选区列表页」直接当响应返回时，页面里有别人几十条候选链接，
	// 先扫 body 会把别人的候选当成自己的。所以只认两条确定性线索：
	//   1) Location 明确指向某条候选详情；
	//   2) 在候选区列表里按提交名称匹配到的那一行。
	offerURL := extractOfferPublishURL(baseURL, location)
	if offerURL == "" {
		// 响应正文若恰好只带一条候选详情链接，说明站点是「直接回详情」的形态（TTG 成功页），
		// 可以安全采用；若带多条则说明正文是候选区列表页，必须先按名称定位，否则会串到别人的候选。
		offerURL = extractSoleOfferPublishURL(baseURL, bodyText)
	}
	if offerURL == "" {
		// 按站点声明的候选区路径回查（其次才用 Location），只做一次 GET，
		// 失败不影响「提交成功」的结论。
		listCandidates := make([]string, 0, 2)
		if trimmedListPath := strings.TrimSpace(listPath); trimmedListPath != "" {
			listCandidates = append(listCandidates, trimmedListPath)
		}
		if location != "" {
			listCandidates = append(listCandidates, location)
		}
		for _, candidate := range listCandidates {
			if offerURL = resolveOfferURLFromListPage(client, candidate, baseURL, cookie, submittedName, &detailLines); offerURL != "" {
				break
			}
		}
	}
	if offerURL == "" && strings.TrimSpace(listPath) == "" {
		// 未配置候选区路径时只能退回扫响应正文（TTG 这类成功后直接回详情链接的站点）。
		offerURL = extractOfferPublishURL(baseURL, bodyText)
	}

	if offerURL != "" {
		detailLines = append(detailLines, fmt.Sprintf("解析候选链接: %s", offerURL))
		return OfferSubmitResult{OfferURL: offerURL, DetailLog: buildDetail()}, nil
	}
	if reOfferSuccess.MatchString(bodyText) || (resp.StatusCode >= 200 && resp.StatusCode < 400) {
		// 站点只给成功提示（或 200/302 且无错误提示）但没回候选链接：按提交成功处理，
		// 后续可用「候选区」列表核对。
		detailLines = append(detailLines, "站点未返回候选链接，按提交成功处理（可在候选区核对）")
		if summary := summarizeResponseBody(bodyText); summary != "" {
			detailLines = append(detailLines, fmt.Sprintf("站点响应摘要: %s", summary))
		}
		return OfferSubmitResult{DetailLog: buildDetail()}, nil
	}

	snippet := summarizeResponseBody(bodyText)
	if snippet == "" {
		snippet = "<empty response body>"
	}
	err = fmt.Errorf("HTTP %d 候选提交失败: %s", resp.StatusCode, snippet)
	appendUploadRawResponse(&detailLines, bodyText)
	detailLines = append(detailLines, fmt.Sprintf("尝试结论: %v", err))
	return OfferSubmitResult{DetailLog: buildDetail()}, err
}

// mergeOfferFormHiddenFields 读取候选表单页的隐藏字段，与业务字段合并（业务字段优先）。
// 参数/返回：client 可复用；formPageURL 为候选表单页；cookie 为登录态；formFields 为业务字段；
// detailLines 收集过程日志；返回合并后的字段表。
// 失败场景：表单页读取失败时只记日志并返回业务字段（部分站点候选表单没有隐藏字段）。
// 副作用：发起一次 GET 请求。
func mergeOfferFormHiddenFields(client *http.Client, formPageURL, cookie string, formFields map[string]string, detailLines *[]string) map[string]string {
	merged := map[string]string{}
	if strings.TrimSpace(formPageURL) != "" {
		hidden, err := fetchOfferFormHiddenFields(client, formPageURL, cookie)
		if err != nil {
			if detailLines != nil {
				*detailLines = append(*detailLines, fmt.Sprintf("读取候选表单隐藏字段失败，继续直接提交: %v", err))
			}
		} else if len(hidden) > 0 {
			for key, value := range hidden {
				merged[key] = strings.TrimSpace(value)
			}
			if detailLines != nil {
				*detailLines = append(*detailLines, fmt.Sprintf("已合并候选表单隐藏字段: %d 个", len(hidden)))
			}
		}
	}
	for key, value := range formFields {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		merged[trimmedKey] = strings.TrimSpace(value)
	}
	return merged
}

// fetchOfferFormHiddenFields 抓取候选表单页并提取其中的隐藏 input。
// 参数/返回：pageURL 为候选表单页地址；返回 name→value 映射。
// 失败场景：网络错误、非 2xx/3xx 状态时返回 error。
// 副作用：发起一次 GET 请求。
func fetchOfferFormHiddenFields(client *http.Client, pageURL, cookie string) (map[string]string, error) {
	if client == nil {
		client = newUploadHTTPClient()
	}
	req, err := http.NewRequest(http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return nil, fmt.Errorf("候选表单页响应状态异常: %d", resp.StatusCode)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}

	hidden := map[string]string{}
	for _, input := range reHiddenInputTag.FindAllString(string(content), -1) {
		attrs := parseHTMLAttributes(input)
		if !strings.EqualFold(strings.TrimSpace(attrs["type"]), "hidden") {
			continue
		}
		name := strings.TrimSpace(attrs["name"])
		if name == "" {
			continue
		}
		hidden[name] = strings.TrimSpace(attrs["value"])
	}
	return hidden, nil
}

// extractOfferPublishURL 从候选提交响应里提取候选详情链接（归一化为绝对地址）。
// 参数/返回：baseURL 用于补全相对链接；text 为响应正文；未命中返回空串。
// 失败场景：无链接或 id 非法时返回空串。
// 副作用：无。
func extractOfferPublishURL(baseURL string, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if match := reOfferDetailID.FindString(trimmed); strings.TrimSpace(match) != "" {
		return absoluteOfferURL(baseURL, match)
	}
	// CHDBits 等站的列表行只有 ?id=N&off_details=1，没有文件名，需要补回 offers.php 路径。
	if m := reOfferBareQuery.FindStringSubmatch(trimmed); len(m) > 1 {
		base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if base == "" {
			return ""
		}
		return base + "/offers.php?id=" + m[1] + "&off_details=1"
	}
	if m := reOfferRelativeDetail.FindStringSubmatch(trimmed); len(m) > 1 {
		base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
		if base == "" {
			return ""
		}
		return base + "/offers.php?id=" + m[1] + "&off_details=1"
	}
	return ""
}

// extractSoleOfferPublishURL 只在响应正文恰好包含一条候选详情链接时返回它。
// 参数/返回：baseURL 用于补全相对链接；text 为响应正文；0 条或多条都返回空串。
// 说明：多链接意味着正文是「候选区列表页」，直接取第一条会把别人的候选当成自己的，
// 这种情况必须交给按名称回查（resolveOfferURLFromListPage）。
// 副作用：无。
func extractSoleOfferPublishURL(baseURL, text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	detailMatches := reOfferDetailID.FindAllString(trimmed, -1)
	relativeMatches := reOfferRelativeDetail.FindAllStringSubmatch(trimmed, -1)
	switch len(detailMatches) + len(relativeMatches) {
	case 1:
		if len(detailMatches) == 1 {
			return absoluteOfferURL(baseURL, detailMatches[0])
		}
		if m := relativeMatches[0]; len(m) > 1 {
			base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
			if base == "" {
				return ""
			}
			return base + "/offers.php?id=" + m[1] + "&off_details=1"
		}
		return ""
	default:
		return ""
	}
}

// absoluteOfferURL 把站点返回的相对/绝对地址补成绝对地址。
// 参数/返回：baseURL 为站点根地址；raw 为 href 或 Location 值；返回绝对地址（无法解析时返回空串）。
// 说明：不能把路径写死成 offers.php —— 各站形态不同（TTG 是 offer.php?vtype=for&id=N），
// 硬编码会把能打开的真实链接改成 404。
// 副作用：无。
func absoluteOfferURL(baseURL, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lowered := strings.ToLower(trimmed)
	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return trimmed
	}
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return ""
	}
	return base + "/" + strings.TrimLeft(trimmed, "/")
}

// offerSubmittedName 取本次提交的候选名称（各站字段名可能是 name，故按值兜底匹配）。
// 参数/返回：formFields 为已装配的候选字段；返回名称（取不到时为空串）。
// 副作用：无。
func offerSubmittedName(formFields map[string]string) string {
	if value := strings.TrimSpace(formFields["name"]); value != "" {
		return value
	}
	for key, value := range formFields {
		if strings.EqualFold(strings.TrimSpace(key), "name") && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// resolveOfferURLFromListPage 在候选区列表页里按名称找回刚提交的候选链接。
// 参数/返回：client 可复用；pageURL 为列表页（Location 或站点配置的候选区路径）；
// baseURL/cookie 为站点信息；name 为提交的候选名称；返回候选项详情链接（找不到返回空串）。
// 失败场景：pageURL 为空、请求失败、页面里没有同名候选时返回空串（只记日志，不影响提交结论）。
// 副作用：发起一次 GET 请求。
func resolveOfferURLFromListPage(client *http.Client, pageURL, baseURL, cookie, name string, detailLines *[]string) string {
	absolute := absoluteOfferURL(baseURL, pageURL)
	if absolute == "" || strings.TrimSpace(name) == "" {
		return ""
	}
	if client == nil {
		client = newUploadHTTPClient()
	}
	req, err := http.NewRequest(http.MethodGet, absolute, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		if detailLines != nil {
			*detailLines = append(*detailLines, fmt.Sprintf("候选区回查失败（忽略，不影响提交结论）: %v", err))
		}
		return ""
	}
	defer resp.Body.Close()
	content, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if readErr != nil || resp.StatusCode < 200 || resp.StatusCode >= 400 {
		if detailLines != nil {
			*detailLines = append(*detailLines, fmt.Sprintf("候选区回查响应异常（忽略）: %d", resp.StatusCode))
		}
		return ""
	}
	if detailLines != nil {
		*detailLines = append(*detailLines, fmt.Sprintf("候选区回查: %s", redactURLQuery(absolute)))
	}
	return findOfferURLByName(baseURL, string(content), name)
}

// findOfferURLByName 在候选列表页 HTML 中按行定位同名候选并取出其详情链接。
// 参数/返回：baseURL 为站点根地址；pageHTML 为列表页源码；name 为提交的候选名称；返回详情链接或空串。
// 说明：按 <tr> 切行、按名称前 40 个字符比对 —— 列表页展示的名称会被截断/改写副标题，
// 全等匹配几乎必然落空；跨行匹配则可能命中别的候选，因此必须先切行再比名称。
// 副作用：无。
func findOfferURLByName(baseURL, pageHTML, name string) string {
	key := offerNameMatchKey(name)
	if key == "" {
		return ""
	}
	for _, chunk := range reHTMLTableRow.Split(pageHTML, -1) {
		// 行文本不截断（行首可能有「支持/反对」按钮文字），只把提交名称截成比对键。
		text := normalizeOfferNameText(stripHTMLTags(chunk))
		if text == "" || !strings.Contains(text, key) {
			continue
		}
		if href := extractOfferPublishURL(baseURL, chunk); href != "" {
			return href
		}
	}
	return ""
}

// normalizeOfferNameText 归一候选名称文本：方括号换空格、折叠连续空白。
// 参数/返回：text 为候选名称或列表页行文本；返回归一后的文本。
// 说明：列表页展示的副标题会被截断或改写，所以比对只做「归一 + 前缀包含」，不做全等。
// 副作用：无。
func normalizeOfferNameText(text string) string {
	normalized := strings.NewReplacer("[", " ", "]", " ").Replace(text)
	return strings.Join(strings.Fields(normalized), " ")
}

// offerNameMatchKey 生成名称比对键：归一后取前 40 个字符。
// 参数/返回：text 为候选名称；返回比对键（太长的名称在列表页会被截断，只能比前缀）。
// 副作用：无。
func offerNameMatchKey(text string) string {
	runes := []rune(normalizeOfferNameText(text))
	if len(runes) > 40 {
		runes = runes[:40]
	}
	return string(runes)
}

// extractHTMLTitleText 取 HTML <title> 纯文本。
func extractHTMLTitleText(bodyText string) string {
	match := reHTMLTitle.FindStringSubmatch(bodyText)
	if len(match) < 2 {
		return ""
	}
	return strings.Join(strings.Fields(stripHTMLTags(match[1])), " ")
}

// stripHTMLTags 去掉标签并反转义实体。
func stripHTMLTags(text string) string {
	return strings.Join(strings.Fields(html.UnescapeString(reStripHTMLTags.ReplaceAllString(text, " "))), " ")
}
