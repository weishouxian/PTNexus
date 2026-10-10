package migrationflow

import (
	"strings"
	"time"

	processingshared "github.com/pt-nexus/server/internal/service/processing/shared"
)

// publishPacingMaxConcurrency 限制下载器发布节奏的并发上限，与下载器设置界面（1-20）保持一致。
const publishPacingMaxConcurrency = 20

// publishPacingMaxIntervalMinutes 限制页面可设置的发种间隔上限，与下载器设置界面（0-1440）保持一致。
const publishPacingMaxIntervalMinutes = 1440

// resolvePayloadPublishIntervalMinutes 从发布 payload 中解析页面显式设置的发种间隔（分钟）。
// 参数/返回：payload 读取 publish_interval_minutes；缺失或 <=0 返回 0，表示不覆盖下载器节奏；
// 超过上限（publishPacingMaxIntervalMinutes）按上限收敛。
// 失败场景：无。
// 副作用：无。
func resolvePayloadPublishIntervalMinutes(payload map[string]any) int {
	if payload == nil {
		return 0
	}
	minutes := int(processingshared.ToFloat(payload["publish_interval_minutes"]))
	if minutes <= 0 {
		return 0
	}
	if minutes > publishPacingMaxIntervalMinutes {
		minutes = publishPacingMaxIntervalMinutes
	}
	return minutes
}

// resolvePublishPacing 解析本次发布最终生效的节奏（间隔 + 并发数）。
// 参数/返回：downloaderID 用于读取下载器配置；overrideMinutes 为页面显式设置的发种间隔（分钟）。
// 返回 interval（>0 表示启用分波）与并发数（>=1）。
// 规则：overrideMinutes > 0 时用它作为间隔，并发仍取该下载器配置的「并发数」（未配置则为 1）；
// 否则完全沿用下载器配置——其「分钟间隔」<=0 时返回 (0, 1)，表示不接管批量并发策略。
// 失败场景：无。
// 副作用：只读配置。
func (s *MigrateService) resolvePublishPacing(downloaderID string, overrideMinutes int) (time.Duration, int) {
	if overrideMinutes > 0 {
		_, concurrency := s.resolveDownloaderPublishPacing(downloaderID)
		return time.Duration(overrideMinutes) * time.Minute, concurrency
	}
	return s.resolveDownloaderPublishPacing(downloaderID)
}

// pacingIntervalSourceLabel 返回节奏间隔的来源，仅用于日志区分「页面设置」与「下载器设置」。
// 参数/返回：overrideMinutes 为页面显式设置的发种间隔；>0 返回 "panel"，否则返回 "downloader"。
// 失败场景：无。
// 副作用：无。
func pacingIntervalSourceLabel(overrideMinutes int) string {
	if overrideMinutes > 0 {
		return "panel"
	}
	return "downloader"
}

// resolvePublishPacingDownloaderID 从发布 payload 中解析用于读取发布节奏的下载器 ID。
// 参数/返回：payload 支持 downloaderId / downloader_id 两种键名；都缺省时回退到跨站默认下载器配置。
// 失败场景：无；解析不到时返回空字符串，调用方按"未启用节奏"处理。
// 副作用：仅在 payload 未带下载器时读取一次配置。
func (s *MigrateService) resolvePublishPacingDownloaderID(payload map[string]any) string {
	if payload != nil {
		if id := strings.TrimSpace(processingshared.ToString(payload["downloaderId"], "")); id != "" {
			return id
		}
		if id := strings.TrimSpace(processingshared.ToString(payload["downloader_id"], "")); id != "" {
			return id
		}
	}
	if s == nil {
		return ""
	}
	return strings.TrimSpace(s.resolveDefaultPublishDownloaderID())
}

// resolveDownloaderPublishPacing 读取指定下载器配置中的发布节奏。
// 参数/返回：downloaderID 为下载器 id；返回 interval（>0 表示启用节奏）与并发数（>=1，上限 publishPacingMaxConcurrency）。
// 失败场景：服务/配置不可用、未匹配到该下载器或该下载器 interval<=0 时返回 (0, 1)，表示不接管并发策略。
// 副作用：只读配置，不写入。
func (s *MigrateService) resolveDownloaderPublishPacing(downloaderID string) (time.Duration, int) {
	concurrency := 1
	downloaderID = strings.TrimSpace(downloaderID)
	if s == nil || s.cfg == nil || downloaderID == "" {
		return 0, concurrency
	}

	root := map[string]any{}
	if cfg := s.cfg.Get(); cfg != nil {
		root = cfg
	}

	for _, item := range downloaderConfigList(root["downloaders"]) {
		if strings.TrimSpace(processingshared.ToString(item["id"], "")) != downloaderID {
			continue
		}
		if value := int(processingshared.ToFloat(item["publish_concurrency"])); value > 0 {
			concurrency = value
		}
		if concurrency > publishPacingMaxConcurrency {
			concurrency = publishPacingMaxConcurrency
		}
		minutes := int(processingshared.ToFloat(item["publish_interval_minutes"]))
		if minutes <= 0 {
			return 0, concurrency
		}
		return time.Duration(minutes) * time.Minute, concurrency
	}

	return 0, concurrency
}

// downloaderConfigList 归一化下载器配置列表，兼容 []any 与 []map[string]any 两种形态。
// 参数/返回：raw 为 rootConfig["downloaders"]；返回可用的下载器配置项切片。
// 失败场景：类型不符时返回空切片。
// 副作用：无。
func downloaderConfigList(raw any) []map[string]any {
	out := make([]map[string]any, 0, 4)
	switch list := raw.(type) {
	case []any:
		for _, item := range list {
			if mapped, ok := item.(map[string]any); ok && mapped != nil {
				out = append(out, mapped)
			}
		}
	case []map[string]any:
		for _, item := range list {
			if item != nil {
				out = append(out, item)
			}
		}
	}
	return out
}
