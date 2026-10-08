package dupe

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"

	publishmapping "github.com/pt-nexus/server/internal/service/publish/mapping"
)

// 本文件解决「筛选维度的取值是上传页选项索引」的站点（当前为 hdhome）。
//
// 背景：hdhome 的 medium / 分辨率 / 视频编码 / 音频编码 映射写作 `@index:N`，
// 含义是「上传表单里该 select 的第 N 个选项」，真实提交值要等抓取上传页才确定。
// 而 dupe 校验发生在抓取上传页之前，此刻 formFields 里只有 `@index:1` 这类占位符。
//
// ⚠️ 实测（hdhome/upload.php）证明**索引不能当值用**：medium_sel 的第 1 项是
// 「UHD Blu-ray」真实值 10、第 2 项是「Blu-ray」真实值 1；audiocodec_sel 更是完全乱序
// （第 1 项 AAC=6、第 2 项 AC3/DD=15）。若把 1 当作 medium 值，实际筛的是 Blu-ray，
// 与待发布资源不符 → 可能把真正的重复筛掉而漏检。
//
// 因此这里专门抓一次上传页，把 `@index:N` 换算成真实选项值。
// 解析不出来时**保留占位符**，由 BuildSearchFilters 跳过该维度——
// 少筛只是候选多一点（最终判定靠制作组 + 体积容差），筛错才会漏检。

// ResolveIndexPlaceholders 把 formFields 中尚未解析的 `@index:N` 占位符解析为上传页真实选项值。
//
// 参数/返回：siteCode 为站点标识；query 提供 base_url/cookie/user-agent；formFields 为表单字段。
// 返回解析后的字段副本与过程说明（说明为空表示无需解析或无需提示）。
//
// 失败场景：站点未配置 upload_path、上传页请求失败或选项缺失时，
// **不返回错误而是降级**——保留占位符并由上层跳过对应维度，绝不因解析失败而中断发布。
// 副作用：存在占位符时向站点发起一次 GET 请求（获取上传页）。
func ResolveIndexPlaceholders(siteCode string, query Query, formFields map[string]string) (map[string]string, string) {
	pending := map[string]int{}
	for field, value := range formFields {
		if idx, ok := parseIndexMarker(value); ok {
			pending[field] = idx
		}
	}
	if len(pending) == 0 {
		return formFields, ""
	}

	siteCfg, err := publishmapping.LoadSitePublishConfig(siteCode)
	if err != nil || siteCfg == nil {
		return formFields, ""
	}
	uploadPath := strings.Trim(strings.TrimSpace(siteCfg.DupeCheck.UploadPath), "/")
	if uploadPath == "" {
		return formFields, "dupe 筛选：站点未配置 upload_path，未解析的 @index 维度已跳过"
	}
	baseURL := strings.TrimRight(strings.TrimSpace(query.BaseURL), "/")
	if baseURL == "" {
		return formFields, ""
	}

	page, fetchErr := httpGetText(query, baseURL+"/"+uploadPath)
	if fetchErr != nil {
		return formFields, fmt.Sprintf("dupe 筛选：读取上传页失败（%v），未解析的 @index 维度已跳过", fetchErr)
	}
	options := parseUploadSelectOptions(page)
	if len(options) == 0 {
		return formFields, "dupe 筛选：上传页未解析出下拉选项，未解析的 @index 维度已跳过"
	}

	resolved := make(map[string]string, len(formFields))
	for field, value := range formFields {
		resolved[field] = value
	}
	applied := make([]string, 0, len(pending))
	skipped := make([]string, 0, len(pending))
	for field, idx := range pending {
		values := options[field]
		if idx < 0 || idx >= len(values) || strings.TrimSpace(values[idx]) == "" {
			skipped = append(skipped, field)
			continue
		}
		resolved[field] = strings.TrimSpace(values[idx])
		applied = append(applied, fmt.Sprintf("%s(@index:%d→%s)", field, idx, values[idx]))
	}

	sort.Strings(applied)
	sort.Strings(skipped)
	switch {
	case len(applied) > 0 && len(skipped) > 0:
		return resolved, fmt.Sprintf("dupe 筛选：按上传页解析维度取值 %s；未解析 %s",
			strings.Join(applied, " "), strings.Join(skipped, " "))
	case len(applied) > 0:
		return resolved, "dupe 筛选：按上传页解析维度取值 " + strings.Join(applied, " ")
	default:
		return resolved, "dupe 筛选：@index 占位符未能解析为真实值，相应维度已跳过"
	}
}

// PrepareSearchFilters 构造站点检索的筛选参数。
//
// 参数/返回：siteCode 为站点标识；query 提供 base_url/cookie；formFields 为最终表单字段；
//
//	dimensions 为该媒介规则勾选的判定维度（只有勾选的筛选维度会进参数）。
//	返回筛选参数与过程说明（说明为空表示无需提示）。
//
// 说明：先按需解析 `@index:N` 占位符（可能额外请求一次上传页），再交给 BuildSearchFilters 生成参数。
// 解析失败的维度会被 BuildSearchFilters 跳过，因此本函数不会因为解析问题而中断发布。
// 副作用：存在 @index 占位符时可能向站点发起一次 GET 请求。
func PrepareSearchFilters(siteCode string, query Query, formFields map[string]string, dimensions []string) (map[string]string, string) {
	resolvedFields, detail := ResolveIndexPlaceholders(siteCode, query, formFields)
	return BuildSearchFilters(siteCode, resolvedFields, dimensions), detail
}

// parseIndexMarker 解析 `@index:N` 形式的选项索引占位符。
func parseIndexMarker(value string) (int, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "@index:") {
		return 0, false
	}
	idx, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(trimmed, "@index:")))
	if err != nil {
		return 0, false
	}
	return idx, true
}

// parseUploadSelectOptions 从上传页 HTML 解析「select name -> 各 option 的 value 列表」。
// 参数/返回：pageHTML 为上传页 HTML；返回映射表（解析失败时返回空表）。
// 说明：用 DOM 解析而不是正则，避免 select 内嵌 optgroup 等结构导致取值错位。
// 副作用：无。
func parseUploadSelectOptions(pageHTML string) map[string][]string {
	root, err := xhtml.Parse(strings.NewReader(pageHTML))
	if err != nil {
		return nil
	}
	result := map[string][]string{}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && strings.EqualFold(node.Data, "select") {
			if name := strings.TrimSpace(attrValue(node, "name")); name != "" {
				values := make([]string, 0, 8)
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == xhtml.ElementNode && strings.EqualFold(child.Data, "option") {
						values = append(values, strings.TrimSpace(attrValue(child, "value")))
					}
				}
				if len(values) > 0 {
					result[name] = values
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return result
}
