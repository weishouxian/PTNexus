package handler

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/siteprofile"
)

// 站点刷新整体超时：站点多时按 6 并发抓，最坏情况也要留足余量；
// 单站超时由 service 内部控制在 45s，这里是整体上限。
const siteProfileRefreshTimeout = 6 * time.Minute

// SiteDataHandler 提供「站点数据」页所需的账号数据查询与刷新接口。
type SiteDataHandler struct {
	service *siteprofile.Service
}

// NewSiteDataHandler 创建站点数据接口处理器。
func NewSiteDataHandler(service *siteprofile.Service) *SiteDataHandler {
	return &SiteDataHandler{service: service}
}

// List 返回站点 + 已抓取账号数据的合并列表。
// 参数/返回：无请求参数；返回 {"success":true,"data":[...]}。
// 失败场景：数据库查询失败返回 500。
// 副作用：无（只读）。
func (h *SiteDataHandler) List(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": []any{}})
		return
	}
	profiles, err := h.service.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "获取站点数据失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": profiles})
}

// Daily 返回指定日期各站点的当日增量（上传/下载/做种积分/魔力值）。
// 参数/返回：query 支持 date=YYYY-MM-DD（空为今天，也接受 today / yesterday）；
// 返回 {"success":true,"data":{"date":"…","rows":[…],"total_uploaded_delta":…}}。
// 失败场景：日期格式非法返回 400，查询失败返回 500。
// 副作用：无（只读）。
//
// 说明：增量按「当天最新采样 - 当天基线」计算，基线是当天第一次采样前锁定的上一条更早快照，
// 因此一天内多次刷新不会让增量缩水；当天没有基线（首次启用）时增量字段为 null。
func (h *SiteDataHandler) Daily(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"date": "", "rows": []any{}}})
		return
	}
	summary, err := h.service.Daily(c.Query("date"))
	if err != nil {
		if errors.Is(err, siteprofile.ErrInvalidDailyDate) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "获取当日增量失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": summary})
}

// SetKeepAlive 保存用户手动指定的保号等级。
// 参数/返回：body {"site":"hdsky","level":"影帝(Ultimate User)"}；level 传空表示恢复自动识别。
// 成功返回 {"success":true,"data":{"site":"hdsky","level":"…"}}，前端按返回值更新该行。
// 失败场景：站点缺参数 400、站点还没抓取数据 400、等级不在等级表里 400、写库失败 500。
// 副作用：只更新 site_profiles.keep_alive_level 一列，不触发任何抓取。
func (h *SiteDataHandler) SetKeepAlive(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "站点数据服务未初始化"})
		return
	}
	var payload struct {
		Site      string `json:"site"`
		SiteCode  string `json:"site_code"`
		Level     string `json:"level"`
		LevelName string `json:"level_name"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求参数格式错误"})
		return
	}
	site := strings.TrimSpace(firstNonEmptyString(payload.SiteCode, payload.Site))
	if site == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少站点代码"})
		return
	}
	level := strings.TrimSpace(firstNonEmptyString(payload.LevelName, payload.Level))
	if err := h.service.SetKeepAliveLevel(site, level); err != nil {
		switch {
		case errors.Is(err, siteprofile.ErrProfileMissing), errors.Is(err, siteprofile.ErrLevelUnknown),
			errors.Is(err, repository.ErrSiteProfileNotFound):
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "保存保号等级失败: " + err.Error()})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"site": site, "level": level}})
}

// firstNonEmptyString 返回第一个非空字符串（兼容前端两种字段写法）。
func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// Trend 返回上传增量趋势（折线图数据）。
// 参数/返回：query 支持 from/to（YYYY-MM-DD，默认近 30 天）、granularity（day/month）；
// 返回 {"success":true,"data":{"from":…,"buckets":[…],"total":[…],"sites":[…]}}。
// 失败场景：参数非法返回 400，查询失败返回 500。
// 副作用：无（只读）。
func (h *SiteDataHandler) Trend(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "站点数据服务未初始化"})
		return
	}
	data, err := h.service.UploadTrend(c.Query("from"), c.Query("to"), c.Query("granularity"))
	if err != nil {
		if errors.Is(err, siteprofile.ErrInvalidTrendRange) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "获取上传趋势失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": data})
}

// Refresh 抓取站点账号数据并落库。
// 参数/返回：body 支持 {"site_codes":["hdsky",...]} 或 {"sites":["站点昵称"]}；为空表示刷新全部。
// 返回刷新后的合并列表，前端直接用返回值覆盖表格。
// 失败场景：站点列表读取失败、整体取消/超时。
// 副作用：向多个目标站点发起 HTTP 请求并写库。
//
// 说明：用 WithoutCancel 脱钩客户端连接 —— 用户切页/刷新浏览器不应把已经发出的抓取中断在半路，
// 否则会出现「一半站点有新数据、一半还是旧的」。
func (h *SiteDataHandler) Refresh(c *gin.Context) {
	if h == nil || h.service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "站点数据服务未初始化"})
		return
	}

	var payload struct {
		SiteCodes []string `json:"site_codes"`
		Sites     []string `json:"sites"`
	}
	// body 允许为空：bind 失败时按「刷新全部」处理，不因前端少传字段报 400。
	_ = c.ShouldBindJSON(&payload)
	codes := append([]string{}, payload.SiteCodes...)
	codes = append(codes, payload.Sites...)
	for _, queryCode := range c.QueryArray("site") {
		codes = append(codes, strings.Split(queryCode, ",")...)
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), siteProfileRefreshTimeout)
	defer cancel()

	profiles, err := h.service.Refresh(ctx, codes)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "刷新站点数据失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    profiles,
		"message": "站点数据刷新完成",
	})
}
