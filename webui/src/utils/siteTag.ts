/**
 * 站点标签的通用归一与匹配（配色见 siteTagColor.ts）。
 *
 * 标签入库时按「忽略大小写去重」，所以任何跨站点的标签比较都必须走同一套 key 归一，
 * 否则会出现「电影」与「电影 」被当成两个标签的情况。
 */

/** 标签 → 比较用 key（去首尾空白 + 小写）。 */
export const siteTagKey = (tag: unknown): string =>
  String(tag ?? '')
    .trim()
    .toLowerCase()

/** 一组标签 → key 数组（丢弃空标签）。 */
export const siteTagKeys = (tags: unknown): string[] => {
  if (!Array.isArray(tags)) return []
  return tags.map(siteTagKey).filter((key) => key !== '')
}

/** 站点是否命中给定标签 key 集合中的任意一个（多标签筛选取「任一命中」）。 */
export const siteMatchesAnyTagKey = (tags: unknown, wantedKeys: Set<string>): boolean => {
  if (wantedKeys.size === 0) return true
  return siteTagKeys(tags).some((key) => wantedKeys.has(key))
}
