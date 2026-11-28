package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/service"
	"github.com/pt-nexus/server/internal/service/settings"
)

type ConfigHandler struct {
	settings *service.SettingsService
}

func NewConfigHandler(settings *service.SettingsService) *ConfigHandler {
	return &ConfigHandler{settings: settings}
}

func (h *ConfigHandler) GetSourcePriority(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.settings.GetSourcePriority()})
}

func (h *ConfigHandler) SaveSourcePriority(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	rawPriority := []any{}
	if raw, exists := payload["source_priority"]; exists {
		parsed, ok := raw.([]any)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "源站点优先级必须是数组"})
			return
		}
		rawPriority = parsed
	}
	if err := h.settings.SaveSourcePriority(rawPriority); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "源站点优先级配置已保存"})
}

func (h *ConfigHandler) GetBatchFetchFilters(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.settings.GetBatchFetchFilters()})
}

func (h *ConfigHandler) SaveBatchFetchFilters(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	filters, ok := payload["batch_fetch_filters"].(map[string]any)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "批量获取筛选条件必须是对象"})
		return
	}
	if err := h.settings.SaveBatchFetchFilters(filters); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "批量获取筛选条件配置已保存"})
}

func (h *ConfigHandler) GetCrossSeedReviewFilter(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.settings.GetCrossSeedReviewFilter()})
}

func (h *ConfigHandler) SaveCrossSeedReviewFilter(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	filter := ""
	if raw, exists := payload["review_filter"]; exists {
		parsed, ok := raw.(string)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "检查状态筛选必须是字符串"})
			return
		}
		filter = parsed
	}
	if err := h.settings.SaveCrossSeedReviewFilter(filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "转种数据检查状态筛选配置已保存"})
}

func (h *ConfigHandler) GetTags(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.settings.GetTagsConfig()})
}

func (h *ConfigHandler) SaveTags(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if err := h.settings.SaveTagsConfig(payload); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "标签配置已保存"})
}

// GetMenuVisibility 返回前端导航菜单的显隐开关。
// 参数/返回：无参数；data 为 key -> 是否显示，未保存的菜单项按默认显示；
// env 为受环境变量控制的菜单项状态（false 表示设置页里也不展示该项）。
// 失败场景：无（配置缺失时按默认口径返回）。
// 副作用：无，仅读取内存配置。
func (h *ConfigHandler) GetMenuVisibility(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    h.settings.GetMenuVisibility(),
		"env":     h.settings.MenuVisibilityEnv(),
	})
}

// SaveMenuVisibility 保存前端导航菜单的显隐开关。
// 参数/返回：body 为 {"items": {"home": true, ...}}；返回保存后的完整显隐配置。
// 失败场景：请求体格式错误返回 400，配置写入失败返回 500。
// 副作用：写入应用配置并持久化。
func (h *ConfigHandler) SaveMenuVisibility(c *gin.Context) {
	var payload struct {
		Items map[string]bool `json:"items"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if err := h.settings.SaveMenuVisibility(payload.Items); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "菜单显示设置已保存",
		"data":    h.settings.GetMenuVisibility(),
	})
}

// GetSiteDataRefresh 返回站点数据页的定时刷新配置。
// 参数/返回：无参数；data 为 {"enabled":bool,"time":"HH:mm"}，未配置时默认关闭、06:00。
// 失败场景：无（配置缺失按默认口径返回）。
// 副作用：无，仅读取内存配置。
func (h *ConfigHandler) GetSiteDataRefresh(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.settings.GetSiteDataRefresh()})
}

// SaveSiteDataRefresh 保存站点数据页的定时刷新配置。
// 参数/返回：body 为 {"enabled":true,"time":"06:00"}；返回保存后的配置。
// 失败场景：请求体格式错误或时间不是 HH:mm 返回 400，写入失败返回 500。
// 副作用：写入应用配置并持久化，后台调度器下一分钟起按新配置执行。
func (h *ConfigHandler) SaveSiteDataRefresh(c *gin.Context) {
	var payload settings.SiteDataRefreshConfig
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	if err := h.settings.SaveSiteDataRefresh(payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "定时刷新设置已保存",
		"data":    h.settings.GetSiteDataRefresh(),
	})
}
