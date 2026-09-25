package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pt-nexus/server/internal/repository"
	"github.com/pt-nexus/server/internal/service/bangumi"
)

// BangumiHandler 提供番组数据（bangumi-data）的查询与同步接口。
type BangumiHandler struct {
	svc *bangumi.Service
}

// NewBangumiHandler 创建番组数据 handler。
// 参数/返回：svc 为番组数据服务；返回 handler 实例。
func NewBangumiHandler(svc *bangumi.Service) *BangumiHandler {
	return &BangumiHandler{svc: svc}
}

// bangumiItemView 是返回给前端的番组条目视图，把内部 JSON 文本列展开为结构化字段。
type bangumiItemView struct {
	ID             int64                        `json:"id"`
	BangumiID      string                       `json:"bangumi_id"`
	Title          string                       `json:"title"`
	TitleZH        string                       `json:"title_zh"`
	TitleTranslate map[string][]string          `json:"title_translate"`
	Type           string                       `json:"type"`
	Lang           string                       `json:"lang"`
	OfficialSite   string                       `json:"official_site"`
	Begin          string                       `json:"begin"`
	End            string                       `json:"end"`
	Broadcast      string                       `json:"broadcast"`
	Comment        string                       `json:"comment"`
	Sites          []repository.BangumiSiteLink `json:"sites"`
	TmdbID         string                       `json:"tmdb_id"`
	MalID          string                       `json:"mal_id"`
	AnidbID        string                       `json:"anidb_id"`
	AniListID      string                       `json:"anilist_id"`
}

// List 分页返回番组条目，支持 keyword / type / lang 过滤。
// 参数/返回：查询参数 keyword、type、lang、page、page_size；返回 items 与 total。
// 失败场景：仓储未初始化或查询失败时返回 500。
// 副作用：仅读取 bangumi_items 表。
func (h *BangumiHandler) List(c *gin.Context) {
	if h == nil || h.svc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "服务未初始化"})
		return
	}

	page, _ := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page", "1")))
	pageSize, _ := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("page_size", "20")))
	filter := repository.BangumiListFilter{
		Keyword:  strings.TrimSpace(c.Query("keyword")),
		ItemType: strings.TrimSpace(c.Query("type")),
		Lang:     strings.TrimSpace(c.Query("lang")),
		Page:     page,
		PageSize: pageSize,
	}

	rows, total, err := h.svc.List(filter)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	items := make([]bangumiItemView, 0, len(rows))
	for _, row := range rows {
		items = append(items, bangumiItemView{
			ID:             row.ID,
			BangumiID:      row.BangumiID,
			Title:          row.Title,
			TitleZH:        row.TitleZH,
			TitleTranslate: repository.ParseBangumiTitleTranslate(row.TitleTransJSON),
			Type:           row.ItemType,
			Lang:           row.Lang,
			OfficialSite:   row.OfficialSite,
			Begin:          row.BeginAt,
			End:            row.EndAt,
			Broadcast:      row.Broadcast,
			Comment:        row.Comment,
			Sites:          repository.ParseBangumiSites(row.SitesJSON),
			TmdbID:         row.TmdbID,
			MalID:          row.MalID,
			AnidbID:        row.AnidbID,
			AniListID:      row.AniListID,
		})
	}

	normalizedPage := filter.Page
	if normalizedPage < 1 {
		normalizedPage = 1
	}
	normalizedSize := filter.PageSize
	if normalizedSize < 1 || normalizedSize > 200 {
		normalizedSize = 20
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"items":     items,
		"total":     total,
		"page":      normalizedPage,
		"page_size": normalizedSize,
	})
}

// Status 返回同步状态与类别统计。
// 参数/返回：无请求参数；返回 meta（含上次同步时间）、type_counts、total 与 running。
// 失败场景：仓储查询失败时返回 500。
// 副作用：仅读取 bangumi_sync_meta 与 bangumi_items 表。
func (h *BangumiHandler) Status(c *gin.Context) {
	if h == nil || h.svc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "服务未初始化"})
		return
	}

	meta, err := h.svc.Meta()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	counts, total, err := h.svc.Counts()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"meta":        meta,
		"type_counts": counts,
		"total":       total,
		"running":     h.svc.Running(),
	})
}

// Sync 手动触发一次同步（异步执行，结果通过 Status 轮询获取）。
// 参数/返回：无请求参数；成功启动返回 success=true，已在同步中返回 409。
// 失败场景：服务未初始化或已有同步在执行。
// 副作用：后台下载数据源并整表替换 bangumi_items。
func (h *BangumiHandler) Sync(c *gin.Context) {
	if h == nil || h.svc == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "服务未初始化"})
		return
	}
	if !h.svc.TriggerSyncAsync() {
		c.JSON(http.StatusConflict, gin.H{"success": false, "running": true, "message": "同步正在进行中，请稍后再试"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "running": true, "message": "已开始同步，请稍候刷新"})
}
