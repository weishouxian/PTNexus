package repository

import "errors"

// 番组条目检索入口：转种流程在获取种子信息时按 TMDB ID / 标题匹配 bgm.tv 条目，
// 这里把检索能力挂到迁移仓储上，复用同一个数据库连接与番组仓储实现。

// FindBangumiItemsByTmdbID 按 TMDB 站点 ID 检索番组条目。
// 参数/返回：tmdbID 为 TMDB 数字 ID；返回按播出时间倒序的条目（默认上限 10 条）。
// 失败场景：仓储未初始化或查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
func (r *MigrateRepository) FindBangumiItemsByTmdbID(tmdbID string) ([]BangumiItem, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("migrate repo is nil")
	}
	return NewBangumiRepository(r.store).FindItemsByTmdbID(tmdbID, bangumiTmdbMatchLimit)
}

// FindBangumiItemsByTitles 按标题集合检索番组条目。
// 参数/返回：titles 为候选标题（按可信度降序）；返回精确/译名命中的条目（默认上限 20 条）。
// 失败场景：仓储未初始化或查询失败时返回错误。
// 副作用：仅读取 bangumi_items 表。
func (r *MigrateRepository) FindBangumiItemsByTitles(titles []string) ([]BangumiItem, error) {
	if r == nil || r.store == nil {
		return nil, errors.New("migrate repo is nil")
	}
	return NewBangumiRepository(r.store).FindItemsByTitles(titles, bangumiTitleMatchLimit)
}
