package torrentdata

import (
	"regexp"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
)

// publishStatusReasonLimit 为「失败原因」摘要的最大字符数，超出部分截断（完整日志走发种日志页）。
const publishStatusReasonLimit = 200

// publishResultLinePattern 匹配发布流程写入 publish_logs.logs 的结果行，例如：
// 「发布结果：成功发布到 TTG」「发布结果：发布到 TTG 失败: HTTP 200 上传失败: 上传失败！数据缺失」。
var publishResultLinePattern = regexp.MustCompile(`发布结果[：:]\s*(.+)`)

// siteErrorLinePattern 匹配站点回传的错误提示行，例如「站点错误提示: 上传失败！数据缺失」。
var siteErrorLinePattern = regexp.MustCompile(`站点错误提示[：:]\s*(.+)`)

// 发布状态分组：前端按分组决定站点标签颜色与汇总列计数。
const (
	publishGroupSuccess = "success" // 发布成功（success / edited）
	publishGroupExists  = "exists"  // 站点已存在（exists）
	publishGroupFailed  = "failed"  // 发布失败（failed / pre_check_limit）
	publishGroupPending = "pending" // 排队/执行中（queued / running / dispatched）
	publishGroupVoid    = "void"    // 已作废（invalidated，种子被删）
	publishGroupOther   = "other"   // 未知状态兜底
)

// publishStatusGroup 把 publish_logs.status 归入展示分组。
// 参数/返回：status 为原始状态字符串；返回分组常量。
// 失败场景：无（未识别状态归 other）。
// 副作用：无。
func publishStatusGroup(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "success", "edited":
		return publishGroupSuccess
	case "exists":
		return publishGroupExists
	case "failed", "pre_check_limit":
		return publishGroupFailed
	case "queued", "running", "dispatched":
		return publishGroupPending
	case repository.PublishLogStatusInvalidated:
		return publishGroupVoid
	default:
		return publishGroupOther
	}
}

// extractPublishReason 从发布日志文本中提取一段可直接展示的失败原因。
// 参数/返回：logs 为 publish_logs.logs 片段；返回截断后的原因文本，取不到时返回空串。
// 失败场景：无。
// 副作用：无。
//
// 提取优先级（依据线上日志实测形态）：
// ① 最后一条「发布结果：…」——成功/失败流程都会写，信息最聚焦；
// ② 最后一条「站点错误提示：…」——站点直接拒绝时才有；
// ③ 最后一个非空且非 `---` 分隔的行——兜底（如「未到可发种时间」这类单行日志）。
func extractPublishReason(logs string) string {
	trimmed := strings.TrimSpace(logs)
	if trimmed == "" {
		return ""
	}

	lines := strings.Split(trimmed, "\n")
	if value := lastPatternCapture(lines, publishResultLinePattern); value != "" {
		return truncateRunes(value, publishStatusReasonLimit)
	}
	if value := lastPatternCapture(lines, siteErrorLinePattern); value != "" {
		return truncateRunes(value, publishStatusReasonLimit)
	}

	for index := len(lines) - 1; index >= 0; index-- {
		line := strings.TrimSpace(lines[index])
		if line == "" || strings.HasPrefix(line, "---") {
			continue
		}
		return truncateRunes(line, publishStatusReasonLimit)
	}
	return ""
}

// lastPatternCapture 返回多行文本中最后一条命中正则的捕获组内容。
// 参数/返回：lines 为文本行；pattern 需含一个捕获组；返回去空后的内容，未命中返回空串。
// 失败场景：无。
// 副作用：无。
func lastPatternCapture(lines []string, pattern *regexp.Regexp) string {
	for index := len(lines) - 1; index >= 0; index-- {
		matches := pattern.FindStringSubmatch(lines[index])
		if len(matches) < 2 {
			continue
		}
		if value := strings.TrimSpace(matches[1]); value != "" {
			return value
		}
	}
	return ""
}

// truncateRunes 按字符（非字节）截断字符串，避免截断中文时产生乱码。
// 参数/返回：value 为原字符串；limit 为最大字符数（<=0 时不截断）。
// 失败场景：无。
// 副作用：无。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// stringSliceValue 从 any 值中取出字符串切片（列表行里 hashes 由服务层构造为 []string）。
// 参数/返回：value 为待解析值；返回去空后的字符串切片，无法解析时返回空切片。
// 失败场景：无。
// 副作用：无。
func stringSliceValue(value any) []string {
	raw, ok := value.([]string)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// attachPublishStatus 为一页「一种多站」数据回填各目标站点的发布状态。
// 参数/返回：items 为本页行数据（原地修改）；无返回值。
// 失败场景：仓储未注入或查询失败时仅记录日志，不阻断列表（发布状态列留空）。
// 副作用：只读 publish_logs。
//
// 回填字段：
//   - publish_sites：该种子各目标站点的发布状态列表（失败优先排序，供展开行内嵌明细表与标签着色）；
//   - publish_summary：按分组计数的汇总（供列表「发布」列展示）。
func (s *TorrentDataService) attachPublishStatus(items []map[string]any) {
	if s == nil || s.publishLogRepo == nil || len(items) == 0 {
		return
	}

	hashes := make([]string, 0, len(items)*2)
	for _, item := range items {
		for _, hash := range stringSliceValue(item["hashes"]) {
			if trimmed := strings.TrimSpace(hash); trimmed != "" {
				hashes = append(hashes, trimmed)
			}
		}
	}
	if len(hashes) == 0 {
		return
	}

	statusByHash, err := s.publishLogRepo.PublishSiteStatusByHashes(hashes, 0)
	if err != nil {
		logx.Warnf(torrentDataLogModule, "加载种子发布状态失败 err=%v", err)
		return
	}
	if len(statusByHash) == 0 {
		return
	}

	for _, item := range items {
		// 同一行可能聚合了多个 hash（同名同体积但 hash 不同），按站点合并：
		// 代表状态取优先级更高的一条（成功 > 已存在 > 排队 > 失败 > 作废），发布次数累加。
		merged := map[string]repository.PublishSiteStatus{}
		for _, hash := range stringSliceValue(item["hashes"]) {
			for _, record := range statusByHash[strings.ToLower(strings.TrimSpace(hash))] {
				site := strings.TrimSpace(record.TargetSite)
				if site == "" {
					continue
				}
				previous, exists := merged[site]
				if !exists {
					merged[site] = record
					continue
				}
				totalCount := previous.Count + record.Count
				if publishStatusRank(record.Status) < publishStatusRank(previous.Status) {
					record.Count = totalCount
					merged[site] = record
					continue
				}
				previous.Count = totalCount
				merged[site] = previous
			}
		}
		if len(merged) == 0 {
			continue
		}

		records := make([]map[string]any, 0, len(merged))
		summary := map[string]int{}
		for site, record := range merged {
			group := publishStatusGroup(record.Status)
			summary[group]++
			records = append(records, map[string]any{
				"site":       site,
				"status":     record.Status,
				"group":      group,
				"count":      record.Count,
				"last_at":    record.LastAt,
				"reason":     extractPublishReason(record.Logs),
				"result_url": record.ResultURL,
				"cost_ms":    record.CostMS,
				"log_id":     record.LogID,
			})
		}
		summary["total"] = len(records)

		item["publish_sites"] = sortPublishRecords(records)
		item["publish_summary"] = summary
	}
}

// publishStatusRank 返回状态的展示优先级（数值越小越优先）。
// 用于同一目标站点出现多条状态时挑选代表状态：成功 > 已存在 > 排队中 > 失败 > 作废 > 未知。
// 参数/返回：status 为原始状态；返回优先级数值。
// 失败场景：无。
// 副作用：无。
func publishStatusRank(status string) int {
	switch publishStatusGroup(status) {
	case publishGroupSuccess:
		return 0
	case publishGroupExists:
		return 1
	case publishGroupPending:
		return 2
	case publishGroupFailed:
		return 3
	case publishGroupVoid:
		return 4
	default:
		return 5
	}
}

// sortPublishRecords 按「失败优先」排序发布状态记录，便于展开行第一眼看到问题站点。
// 参数/返回：records 为待排序记录；返回排好序的新切片（不修改入参）。
// 失败场景：无。
// 副作用：无。
func sortPublishRecords(records []map[string]any) []map[string]any {
	sorted := make([]map[string]any, len(records))
	copy(sorted, records)

	order := func(record map[string]any) int {
		switch stringValue(record["group"], "") {
		case publishGroupFailed:
			return 0
		case publishGroupExists:
			return 1
		case publishGroupSuccess:
			return 2
		case publishGroupPending:
			return 3
		case publishGroupVoid:
			return 4
		default:
			return 5
		}
	}
	// 记录数量很小（每行最多十几个站点），插入排序足够且无需引入 sort 包的 Less 闭包开销。
	for i := 1; i < len(sorted); i++ {
		current := sorted[i]
		currentOrder := order(current)
		j := i - 1
		for j >= 0 && order(sorted[j]) > currentOrder {
			sorted[j+1] = sorted[j]
			j--
		}
		sorted[j+1] = current
	}
	return sorted
}
