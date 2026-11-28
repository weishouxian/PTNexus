package settings

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// 站点数据定时刷新：每天在指定的若干个时刻自动抓取全部站点账号数据。
// 配置落 ui_settings.site_data_refresh，容器内 /etc/localtime 是 UTC，
// 所以调度器统一按 Asia/Shanghai 判断「到点」，这里的 HH:mm 也按该时区理解。
const (
	siteDataRefreshEnabledKey = "enabled"
	siteDataRefreshTimeKey    = "time"
	siteDataRefreshTimesKey   = "times"
	defaultSiteDataRefreshAt  = "06:00"
	// 一天最多允许几个刷新点：再多只会把站点抓爆，且调度器每分钟一次比对也没意义。
	maxSiteDataRefreshTimes = 12
)

// SiteDataRefreshConfig 描述「站点数据」页的定时刷新配置。
//
// Time 是单时间点时代的字段，保留它作兼容：老配置只存了 time，新配置同时写 times，
// 且 Time 始终等于 Times 的第一项，旧版本前端/旧备份读到的行为不变。
type SiteDataRefreshConfig struct {
	Enabled bool     `json:"enabled"`
	Time    string   `json:"time"`  // HH:mm，按 Asia/Shanghai 解释（= Times[0]）
	Times   []string `json:"times"` // HH:mm 列表，可配置多个刷新点
}

// GetSiteDataRefresh 读取站点数据定时刷新配置。
// 参数/返回：无参数；未配置时返回默认值（关闭、06:00）。
// 失败场景：配置缺失或结构异常按默认值返回，不报错。
// 副作用：无，仅读取内存配置。
func (s *SettingsService) GetSiteDataRefresh() SiteDataRefreshConfig {
	result := SiteDataRefreshConfig{Enabled: false, Time: defaultSiteDataRefreshAt, Times: []string{defaultSiteDataRefreshAt}}
	cfg := s.cfg.Get()
	ui, ok := cfg["ui_settings"].(map[string]any)
	if !ok {
		return result
	}
	saved, ok := ui["site_data_refresh"].(map[string]any)
	if !ok {
		return result
	}
	result.Enabled = toBool(saved[siteDataRefreshEnabledKey], false)
	times := toStringSlice(saved[siteDataRefreshTimesKey])
	if len(times) == 0 {
		// 老配置只有单个 time：升级后当成一个刷新点。
		times = []string{toString(saved[siteDataRefreshTimeKey], "")}
	}
	normalized := sanitizeRefreshTimes(times)
	if len(normalized) == 0 {
		normalized = []string{defaultSiteDataRefreshAt}
	}
	result.Times = normalized
	result.Time = normalized[0]
	return result
}

// SaveSiteDataRefresh 保存站点数据定时刷新配置。
// 参数/返回：input 为前端提交的开关与时刻列表（也接受只传旧字段 time）；返回错误表示参数非法或写入失败。
// 失败场景：任一时刻不是 HH:mm 格式、刷新点超过上限返回错误；配置保存失败返回错误。
// 副作用：写入配置文件并持久化。
func (s *SettingsService) SaveSiteDataRefresh(input SiteDataRefreshConfig) error {
	raw := input.Times
	if len(raw) == 0 && strings.TrimSpace(input.Time) != "" {
		raw = []string{input.Time}
	}
	times, err := normalizeRefreshTimes(raw)
	if err != nil {
		return err
	}
	cfg := s.cfg.Get()
	ui := ensureMap(cfg, "ui_settings")
	ui["site_data_refresh"] = map[string]any{
		siteDataRefreshEnabledKey: input.Enabled,
		siteDataRefreshTimeKey:    times[0],
		siteDataRefreshTimesKey:   times,
	}
	cfg["ui_settings"] = ui
	return s.cfg.Save(cfg)
}

// normalizeRefreshTimes 校验并规范化刷新点：去空、去重、按时间升序，空列表回落默认时刻。
// 参数/返回：raw 为前端提交的 HH:mm 列表；返回排好序的列表或错误。
// 失败场景：个数超过 maxSiteDataRefreshTimes、存在非 HH:mm 值。
// 副作用：无。
func normalizeRefreshTimes(raw []string) ([]string, error) {
	trimmed := make([]string, 0, len(raw))
	for _, item := range raw {
		if value := strings.TrimSpace(item); value != "" {
			trimmed = append(trimmed, value)
		}
	}
	if len(trimmed) == 0 {
		return []string{defaultSiteDataRefreshAt}, nil
	}
	if len(trimmed) > maxSiteDataRefreshTimes {
		return nil, fmt.Errorf("定时刷新最多支持 %d 个时间点", maxSiteDataRefreshTimes)
	}
	for _, value := range trimmed {
		if _, err := time.Parse("15:04", value); err != nil {
			return nil, fmt.Errorf("执行时间必须是 HH:mm 格式：%s", value)
		}
	}
	return sanitizeRefreshTimes(trimmed), nil
}

// sanitizeRefreshTimes 丢弃非法值、去重并升序排列（读取旧配置时也走这里）。
func sanitizeRefreshTimes(raw []string) []string {
	seen := make(map[string]bool, len(raw))
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		value := strings.TrimSpace(item)
		if value == "" {
			continue
		}
		// 顺带把 6:00 这种不规范写法规范成 06:00
		parsed, err := time.Parse("15:04", value)
		if err != nil {
			if parsed, err = time.Parse("15:4", value); err != nil {
				continue
			}
		}
		value = parsed.Format("15:04")
		if seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
