package migrationflow

import (
	"encoding/json"
	"strings"

	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	processingpersist "github.com/pt-nexus/server/internal/service/processing/persist"
	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

const publishLogReAddDownloaderModule = "发布-重新添加到下载器"

// ReAddPublishLogToDownloader 对「发布成功、但自动添加到下载器失败」的记录重试添加下载任务。
// 参数/返回：payload 支持两种定位方式——① id/log_id（发种日志页）；② task_id + target_site（转种面板「完成发布」步骤）；
// 可带 url/publishURL（详情页地址）、downloader_id、save_path 覆盖原值；返回接口响应与状态码。
// 失败场景：日志未初始化 500；id 方式下日志不存在 404；缺少详情页地址 / 非已发布状态 / 定位参数缺失 400；添加失败时透传原因。
// 副作用：向目标下载器添加任务；命中日志行时把最新结果回写该行的 auto_add_result（并补写 downloader_id）。
//
// 说明：重试沿用「发布后自动添加」的同一套逻辑（详情页 URL → 反查站点 → 下载种子 → 加入下载器），
// 因此标签/分类/限速、站点标签与评论重写等行为与正常发布完全一致，且不会再次向站点发种。
func (s *MigrateService) ReAddPublishLogToDownloader(payload map[string]any) (map[string]any, int) {
	if s == nil || s.publishLogRepo == nil {
		return map[string]any{"success": false, "message": "发种日志未初始化"}, 500
	}

	entry, failStatus, failMessage := s.resolveReAddLogEntry(payload)
	if failStatus != 0 {
		return map[string]any{"success": false, "message": failMessage}, failStatus
	}

	// 详情页地址：优先请求体（转种面板手上就有 url），其次日志行记录的发布结果地址。
	detailURL := firstNonEmptyString(
		strings.TrimSpace(processingshared.ToString(payload["url"], "")),
		strings.TrimSpace(processingshared.ToString(payload["publishURL"], "")),
	)
	if entry != nil {
		detailURL = firstNonEmptyString(detailURL, strings.TrimSpace(entry.ResultURL))
	}
	if detailURL == "" {
		return map[string]any{"success": false, "message": "缺少发布详情地址，无法重新添加下载器"}, 400
	}

	downloaderID := firstNonEmptyString(
		strings.TrimSpace(processingshared.ToString(payload["downloader_id"], "")),
		strings.TrimSpace(processingshared.ToString(payload["downloaderId"], "")),
	)
	targetSiteLabel := firstNonEmptyString(
		strings.TrimSpace(processingshared.ToString(payload["target_site"], "")),
		strings.TrimSpace(processingshared.ToString(payload["site_nickname"], "")),
	)
	if entry != nil {
		downloaderID = firstNonEmptyString(downloaderID, strings.TrimSpace(entry.DownloaderID))
		targetSiteLabel = firstNonEmptyString(targetSiteLabel, strings.TrimSpace(entry.TargetSite))
	}

	addPayload := map[string]any{
		"url":          detailURL,
		"publishURL":   detailURL,
		"targetSite":   targetSiteLabel,
		"siteNickname": targetSiteLabel,
	}
	if downloaderID != "" {
		addPayload["downloaderId"] = downloaderID
	} else {
		// 没有指定下载器时，退回全局默认下载器（cross_seed.default_downloader）。
		addPayload["useDefaultDownloader"] = true
	}
	if savePath := firstNonEmptyString(
		strings.TrimSpace(processingshared.ToString(payload["save_path"], "")),
		strings.TrimSpace(processingshared.ToString(payload["savePath"], "")),
	); savePath != "" {
		addPayload["savePath"] = savePath
	}

	addResult, addStatus := s.AddToDownloader(addPayload)
	if addResult == nil {
		return map[string]any{"success": false, "message": "重新添加到下载器失败"}, 500
	}
	if addStatus >= 400 {
		message := strings.TrimSpace(processingshared.ToString(addResult["message"], ""))
		if message == "" {
			message = "重新添加到下载器失败"
		}
		return map[string]any{"success": false, "message": message}, addStatus
	}

	// 无论成功还是被预检查拦下（limit_reached），都回写最新结果，
	// 否则页面刷新后会继续显示上一次的失败原因。
	effectiveDownloaderID := strings.TrimSpace(processingshared.ToString(addResult["downloader_id"], ""))
	if entry != nil && entry.ID > 0 {
		if encoded, marshalErr := json.Marshal(addResult); marshalErr == nil {
			if updateErr := s.publishLogRepo.UpdateDownloaderResultByID(entry.ID, effectiveDownloaderID, string(encoded)); updateErr != nil {
				logx.Warnf(publishLogReAddDownloaderModule, "回写重新添加结果失败 log_id=%d err=%v", entry.ID, updateErr)
			}
		} else {
			logx.Warnf(publishLogReAddDownloaderModule, "序列化重新添加结果失败 log_id=%d err=%v", entry.ID, marshalErr)
		}
	}

	succeeded := processingpersist.BoolFromAny(addResult["success"])
	message := strings.TrimSpace(processingshared.ToString(addResult["message"], ""))
	if message == "" {
		message = "已重新添加到下载器"
	}
	logx.Infof(
		publishLogReAddDownloaderModule,
		"重新添加完成 log_id=%d target_site=%s downloader_id=%s success=%v message=%s",
		resolvedLogID(entry),
		targetSiteLabel,
		effectiveDownloaderID,
		succeeded,
		message,
	)

	return map[string]any{
		"success":          succeeded,
		"message":          message,
		"auto_add_result":  addResult,
		"downloader_id":    effectiveDownloaderID,
		"downloader_name":  strings.TrimSpace(processingshared.ToString(addResult["downloader_name"], "")),
		"hash":             strings.TrimSpace(processingshared.ToString(addResult["hash"], "")),
		"log_id":           resolvedLogID(entry),
		"log_record_found": entry != nil,
	}, 200
}

// resolveReAddLogEntry 定位要回写结果的发种日志行。
// 参数/返回：payload 为请求参数；返回命中的日志行（可能为 nil）、失败状态码与失败原因。
// 失败场景：日志仓储异常 500；显式传 id 但记录不存在 404；命中记录状态不属于已发布家族 400；两种定位参数都缺失 400。
// 副作用：读取 publish_logs。
//
// 规则：传了 id 就必须命中（发种日志页入口）；只传 task_id + target_site 时属于转种面板入口，
// 找不到记录不视为错误——面板手上已有发布结果（url 直接随请求带回），照常添加即可，只是没有可回写的日志行。
func (s *MigrateService) resolveReAddLogEntry(payload map[string]any) (*repository.PublishLogEntry, int, string) {
	if logID := resolveReAddLogID(payload); logID > 0 {
		entry, found, err := s.publishLogRepo.GetByID(logID)
		if err != nil {
			return nil, 500, "读取发种日志失败: " + err.Error()
		}
		if !found || entry == nil {
			return nil, 404, "未找到对应的发种日志"
		}
		if !isSuccessfulPublishLogStatus(entry.Status) {
			return nil, 400, "仅支持已发布成功的记录重新添加到下载器"
		}
		return entry, 0, ""
	}

	taskID := strings.TrimSpace(processingshared.ToString(payload["task_id"], ""))
	targetSite := strings.TrimSpace(processingshared.ToString(payload["target_site"], ""))
	if taskID == "" || targetSite == "" {
		return nil, 400, "缺少日志 ID 或 task_id + target_site"
	}

	entry, found, err := s.publishLogRepo.FindLatestByTaskAndSite(taskID, targetSite)
	if err != nil {
		// 兜底定位失败只影响结果回写，不阻断本次添加。
		logx.Warnf(publishLogReAddDownloaderModule, "按 task_id 定位日志失败 task_id=%s target_site=%s err=%v", taskID, targetSite, err)
		return nil, 0, ""
	}
	if !found || entry == nil {
		return nil, 0, ""
	}
	if !isSuccessfulPublishLogStatus(entry.Status) {
		return nil, 400, "仅支持已发布成功的记录重新添加到下载器"
	}
	return entry, 0, ""
}

// resolvedLogID 返回命中日志行的主键（未命中时为 0）。
// 参数/返回：entry 为日志行；返回主键 ID。
// 失败场景：无。
// 副作用：无。
func resolvedLogID(entry *repository.PublishLogEntry) uint64 {
	if entry == nil {
		return 0
	}
	return entry.ID
}

// resolveReAddLogID 从请求体中解析日志主键。
// 参数/返回：payload 为请求参数；返回解析到的正整数 ID，解析失败时返回 0。
// 失败场景：payload 为空或字段缺失/非法时返回 0。
// 副作用：无。
func resolveReAddLogID(payload map[string]any) uint64 {
	if payload == nil {
		return 0
	}
	for _, key := range []string{"id", "log_id", "logId"} {
		raw := processingshared.ToString(payload[key], "")
		if value := parsePositiveUint64(raw); value > 0 {
			return value
		}
	}
	return 0
}

// parsePositiveUint64 把字符串解析为正整数（浮点字符串也接受，避免 JSON 数字被解成 float64）。
// 参数/返回：raw 为待解析字符串；成功返回正数值，否则返回 0。
// 失败场景：空串、非数字、<=0 时返回 0。
// 副作用：无。
func parsePositiveUint64(raw string) uint64 {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	if idx := strings.Index(trimmed, "."); idx >= 0 {
		trimmed = trimmed[:idx]
	}
	value := uint64(0)
	for _, ch := range trimmed {
		if ch < '0' || ch > '9' {
			return 0
		}
		value = value*10 + uint64(ch-'0')
	}
	return value
}
