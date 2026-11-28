package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/reseed"
)

// ReseedHandler 处理 IYUU 指定站点辅种相关接口。
type ReseedHandler struct {
	service *reseed.Service
}

// NewReseedHandler 创建辅种处理器。
func NewReseedHandler(service *reseed.Service) *ReseedHandler {
	return &ReseedHandler{service: service}
}

// Status 返回辅种页初始化状态：Token 是否配置、可选站点、下载器、路径与运行状态。
// 参数/返回：无参数；HTTP 200 返回状态对象。
// 失败场景：数据库读取失败时相关字段为空，不影响 200。
// 副作用：只读配置与数据库。
func (h *ReseedHandler) Status(c *gin.Context) {
	status := h.service.Status(c.Request.Context())
	status["paths"] = h.service.DefaultPaths()
	c.JSON(http.StatusOK, status)
}

// Run 启动一次指定站点辅种任务。
// 参数/返回：请求体见 reseed.Params；成功返回 task_id，失败返回 400/409。
// 失败场景：未配置 IYUU Token、未选站点、所选站点未设置 Cookie/Passkey、任务已在进行中。
// 副作用：异步执行辅种。
func (h *ReseedHandler) Run(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	result, status := h.service.Run(payload)
	c.JSON(status, result)
}

// Progress 查询辅种任务进度。
// 参数/返回：查询参数 task_id；返回任务对象或 404。
// 失败场景：缺少 task_id 或任务不存在。
// 副作用：无。
func (h *ReseedHandler) Progress(c *gin.Context) {
	taskID := strings.TrimSpace(c.Query("task_id"))
	if taskID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 task_id 参数"})
		return
	}
	task, ok := h.service.Progress(taskID)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "任务不存在或已过期"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "task": task})
}

// Stop 请求停止当前辅种任务。
// 参数/返回：无参数；返回是否已下发停止标记。
// 失败场景：没有运行中的任务。
// 副作用：写入停止标记，当前种子处理完后退出。
func (h *ReseedHandler) Stop(c *gin.Context) {
	if !h.service.Stop() {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "当前没有正在执行的辅种任务"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "已请求停止，当前种子处理完后退出"})
}

// SetDefaultTags 保存默认辅种标签：辅种页保存后，「一种多站」行内辅种也会带上同一组标签。
// 参数/返回：请求体 {"tags": ["站点/财神"]}；成功返回保存后的标签。
// 失败场景：请求体格式错误、tags 不是数组、配置写入失败。
// 副作用：写入全局配置（与设置中心其他保存动作同一路径）。
func (h *ReseedHandler) SetDefaultTags(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}
	raw, exists := payload["tags"]
	if !exists {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "缺少 tags 参数（传空数组表示清空）"})
		return
	}
	items, ok := raw.([]any)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "tags 必须是字符串数组"})
		return
	}
	tags := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			tags = append(tags, text)
		}
	}
	if err := h.service.SetDefaultTags(tags); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "保存默认标签失败: " + err.Error()})
		return
	}
	saved := h.service.DefaultTags()
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": defaultTagsMessage(saved),
		"data":    saved,
	})
}

// defaultTagsMessage 按保存结果给出回执文案（清空时也说明清楚）。
// 参数/返回：tags 为保存后的标签列表；返回提示文本。
// 失败场景：无。
// 副作用：无。
func defaultTagsMessage(tags []string) string {
	if len(tags) == 0 {
		return "已清空默认标签：辅种不再自动打标签"
	}
	return "默认标签已保存：" + strings.Join(tags, "、") + "（辅种页与「一种多站」一键辅种都会带上）"
}

// Records 分页查询辅种记录。
// 参数/返回：查询参数 page/page_size/target_site/status/keyword；返回记录列表与总数。
// 失败场景：数据库查询失败返回 500。
// 副作用：只读数据库。
func (h *ReseedHandler) Records(c *gin.Context) {
	page, _ := strconv.Atoi(strings.TrimSpace(c.Query("page")))
	pageSize, _ := strconv.Atoi(strings.TrimSpace(c.Query("page_size")))
	filter := repository.ReseedRecordFilter{
		Page:       page,
		PageSize:   pageSize,
		TargetSite: strings.TrimSpace(c.Query("target_site")),
		Status:     strings.TrimSpace(c.Query("status")),
		Keyword:    strings.TrimSpace(c.Query("keyword")),
		TaskID:     strings.TrimSpace(c.Query("task_id")),
	}
	rows, total, err := h.service.Records(filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "message": "读取辅种记录失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      rows,
		"total":     total,
		"page":      filter.Page,
		"page_size": filter.PageSize,
	})
}
