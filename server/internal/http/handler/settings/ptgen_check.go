package settings

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	processingrepair "github.com/pt-nexus/server/internal/service/processing/repair"
)

// PostPTGenTest 检测各 PTGen 节点对指定豆瓣 ID 的可用性。
// 参数/返回：请求体需包含 douban_id，支持裸 ID 或完整豆瓣链接；返回各节点状态、HTTP 状态码、耗时与内容摘要。
// 失败场景：豆瓣 ID 缺失或无法解析时返回 400。
// 副作用：对每个内置节点发起一次真实外部请求（含已停用节点，便于确认是否恢复）。
func (h *Handler) PostPTGenTest(c *gin.Context) {
	payload := map[string]any{}
	if err := c.ShouldBindJSON(&payload); err != nil && !strings.Contains(strings.ToLower(err.Error()), "eof") {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请求体格式错误"})
		return
	}

	doubanID := processingrepair.NormalizeDoubanIDInput(handlerToString(payload["douban_id"], ""))
	if doubanID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "请输入有效的豆瓣 ID 或豆瓣链接"})
		return
	}

	crossSeed := map[string]any{}
	if h.settings != nil {
		if loaded := h.settings.GetCrossSeedSettings(); loaded != nil {
			crossSeed = loaded
		}
	}
	csptToken := strings.TrimSpace(handlerToString(crossSeed["cspt_ptgen_token"], ""))

	results := processingrepair.TestPTGenNodes(doubanID, csptToken, crossSeed["ptgen_nodes"])
	successCount := 0
	for _, item := range results {
		if item.Status == processingrepair.PTGenTestStatusSuccess {
			successCount++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":         true,
		"douban_id":       doubanID,
		"douban_url":      "https://movie.douban.com/subject/" + doubanID + "/",
		"cspt_configured": csptToken != "",
		"success_count":   successCount,
		"total_count":     len(results),
		"nodes":           results,
	})
}
