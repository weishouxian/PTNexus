package sitehealth

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pt-nexus/server/internal/config"
	"github.com/pt-nexus/server/internal/platform/logx"
	"github.com/pt-nexus/server/internal/repository"
	publishdupe "github.com/pt-nexus/server/internal/service/publish/dupe"
)

const healthLogModule = "站点健康检测"

// Service 后台定时检测站点 Cookie/Passkey 有效性的服务。
// 按配置的间隔（小时，支持小数）遍历所有填了 Cookie 或 Passkey 的站点，
// 复用 publishdupe.CheckCookie 判定有效性；对「失效」或「获取不到用户信息」的站点写入站内提醒。
type Service struct {
	siteRepo  *repository.SiteRepository
	alertRepo *repository.SiteAlertRepository
	cfg       *config.Manager

	stopCh    chan struct{}
	doneCh    chan struct{}
	triggerCh chan struct{}
	once      sync.Once
}

// NewService 创建站点健康检测服务。
func NewService(siteRepo *repository.SiteRepository, alertRepo *repository.SiteAlertRepository, cfg *config.Manager) *Service {
	return &Service{
		siteRepo:  siteRepo,
		alertRepo: alertRepo,
		cfg:       cfg,
		stopCh:    make(chan struct{}),
		doneCh:    make(chan struct{}),
		triggerCh: make(chan struct{}, 1),
	}
}

// Start 启动后台检测协程（sync.Once 保证只启动一次）。
func (s *Service) Start() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		go s.run()
		logx.Infof(healthLogModule, "站点健康检测服务已启动")
	})
}

// Stop 停止后台协程（幂等，供优雅退出）。
func (s *Service) Stop() {
	if s == nil {
		return
	}
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// TriggerCheck 手动触发一次全量检测（一键检测按钮）。
func (s *Service) TriggerCheck() {
	if s == nil {
		return
	}
	select {
	case s.triggerCh <- struct{}{}:
	default:
		// 已有一次检测在排队，忽略重复触发。
	}
}

// IntervalHours 从配置读取检测间隔（小时，支持小数）。
// 返回 0 表示关闭定时检测；负数/非法值同样视为关闭。
func (s *Service) IntervalHours() float64 {
	if s == nil || s.cfg == nil {
		return 0
	}
	raw := s.cfg.Get()["site_health"]
	intervalHours, ok := raw.(map[string]any)
	if !ok || intervalHours == nil {
		return 0
	}
	var result float64
	switch v := intervalHours["interval_hours"].(type) {
	case float64:
		result = v
	case int:
		result = float64(v)
	case int64:
		result = float64(v)
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0
		}
		result = f
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0
		}
		result = f
	default:
		return 0
	}
	if result < 0 {
		return 0
	}
	return result
}

func (s *Service) run() {
	defer close(s.doneCh)

	// 基础轮询粒度 1 分钟；每次 tick 检查「距上次检测是否已到间隔」。
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	var lastCheck time.Time

	for {
		select {
		case <-s.stopCh:
			return
		case <-s.triggerCh:
			s.checkAll()
			lastCheck = time.Now()
		case <-ticker.C:
			interval := s.IntervalHours()
			if interval <= 0 {
				// 定时检测关闭：只保留手动一键检测，不自动跑。
				continue
			}
			d := time.Duration(interval * float64(time.Hour))
			if time.Since(lastCheck) < d {
				continue
			}
			s.checkAll()
			lastCheck = time.Now()
		}
	}
}

// checkAll 遍历所有站点，对填了 Cookie 或 Passkey 的站点做有效性检测，失效则写提醒；
// 检测完成后写入一条汇总提醒（检测了多少站、多少失效）。
func (s *Service) checkAll() {
	if s == nil || s.siteRepo == nil {
		return
	}
	sites, err := s.siteRepo.ListSites("all")
	if err != nil {
		logx.Warnf(healthLogModule, "读取站点列表失败 err=%v", err)
		return
	}

	checked := 0
	failed := 0
	for _, site := range sites {
		siteCode := strings.TrimSpace(toString(site["site"]))
		nickname := strings.TrimSpace(toString(site["nickname"]))
		if nickname == "" {
			nickname = siteCode
		}
		baseURL := strings.TrimSpace(toString(site["base_url"]))
		cookie := strings.TrimSpace(toString(site["cookie"]))
		passkey := strings.TrimSpace(toString(site["passkey"]))

		// 只检测项目已支持且「参与发种/转种」的站点（migration ∈ {1,2,3}）：
		// migration=0 的站点仅登记在册、不参与发种/转种，用户不会给它们配凭据并期望被检测，
		// 与前端「已有支持站点」页的筛选口径保持一致；用户自定义添加的站点也不参与。
		if !config.IsSupportedSite(siteCode) || !config.SiteIsActive(siteCode) {
			continue
		}

		// 站点关闭了 Cookie 检测开关，跳过（用户显式要求不检测该站点）。
		if isExplicitlyFalse(site["check_cookie_enabled"]) {
			continue
		}

		// 未配置任何凭据的站点跳过。
		if cookie == "" && passkey == "" {
			continue
		}
		if baseURL == "" {
			continue
		}

		checked++
		valid, reason, user, checkErr := publishdupe.CheckCookie(baseURL, siteCode, cookie, passkey)
		if checkErr != nil {
			// 网络/协议层失败也视为需要提醒（拿不到用户信息）。
			failed++
			s.record(siteCode, nickname, "error", "检测失败："+checkErr.Error())
			continue
		}
		if !valid {
			// 站点前置 Cloudflare 人机校验时，服务器端过不去，Cookie 本身可能有效——
			// 这不是「失效」，不应刷告警，跳过即可。
			if reason == publishdupe.CookieCheckCloudflareReason {
				continue
			}
			failed++
			s.record(siteCode, nickname, "warning", "Cookie/令牌失效或已过期，请更新凭据")
			continue
		}
		if strings.TrimSpace(user.UserID) == "" && strings.TrimSpace(user.Username) == "" {
			failed++
			s.record(siteCode, nickname, "warning", "凭据可用但未能获取到用户信息")
			continue
		}
	}

	// 处理完发送一条汇总提醒，让用户一眼看到本轮检测的总体结果。
	s.recordSummary(checked, failed)
}

// recordSummary 写入一条本轮检测的汇总提醒（不带具体站点，用于头部栏整体提示）。
func (s *Service) recordSummary(checked, failed int) {
	if s == nil || s.alertRepo == nil {
		return
	}
	level := "info"
	summary := fmt.Sprintf("本轮检测 %d 个站点，全部正常", checked)
	if failed > 0 {
		level = "warning"
		summary = fmt.Sprintf("本轮检测 %d 个站点，%d 个站点失效或异常，请查看详情", checked, failed)
	}
	if err := s.alertRepo.Add("", "", level, summary); err != nil {
		logx.Warnf(healthLogModule, "写入检测汇总提醒失败 err=%v", err)
	}
}

// record 写入一条提醒（同站点同级别消息去重，避免每轮重复刷提醒）。
func (s *Service) record(siteCode, nickname, level, message string) {
	if s == nil || s.alertRepo == nil {
		return
	}
	if err := s.alertRepo.Add(siteCode, nickname, level, message); err != nil {
		logx.Warnf(healthLogModule, "写入站点提醒失败 site=%s err=%v", siteCode, err)
	}
}

// toString 安全地把 any 转成 string（nil/非字符串返回空串）。
func toString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	case nil:
		return ""
	default:
		return ""
	}
}

// isExplicitlyFalse 判断站点「关闭 Cookie 检测」开关是否被显式关闭。
// 只认明确的 false/0/"0"/"false"，其余（含缺失、nil、true、非零）均视为开启，避免误跳。
func isExplicitlyFalse(v any) bool {
	switch t := v.(type) {
	case bool:
		return !t
	case int:
		return t == 0
	case int64:
		return t == 0
	case int32:
		return t == 0
	case float64:
		return t == 0
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "0" || s == "false"
	case []byte:
		s := strings.TrimSpace(strings.ToLower(string(t)))
		return s == "0" || s == "false"
	default:
		return false
	}
}
