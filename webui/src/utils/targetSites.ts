/**
 * 目标站点选择的共享持久化记忆。
 *
 * 「多站批量发种」与「定时发种」的目标站点选择共享同一份 localStorage 记忆：
 * 任一处改动，另一处下次打开时默认带出相同选择，避免两边各选一套、反复重选。
 *
 * 注意：这里存的是站点昵称（name），不是站点代码（site）；两处 UI 都用 name 展示与勾选。
 */

const STORAGE_KEY = 'ptnexus:selected_target_sites'

/** 读取最近一次选择的目标站点（站点昵称数组）。无记忆时返回空数组。 */
export function loadSelectedTargetSites(): string[] {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const parsed = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []
    return parsed.filter((item): item is string => typeof item === 'string' && item.trim() !== '')
  } catch {
    return []
  }
}

/** 写入最近一次选择的目标站点（站点昵称数组）。空数组会清空记忆。 */
export function saveSelectedTargetSites(sites: string[]): void {
  try {
    if (!Array.isArray(sites) || sites.length === 0) {
      localStorage.removeItem(STORAGE_KEY)
      return
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(sites))
  } catch {
    // localStorage 不可用（隐私模式/超限）时静默降级，不影响发种主流程。
  }
}

/**
 * 按站点列表顺序（即「站点管理」里的排序序号）重排已选站点。
 *
 * 多站发布的实际顺序由后端按 sites.sort_order 决定，这里只是让面板里的选择顺序、
 * localStorage 记忆顺序和后端保持一致，避免用户看着勾选顺序猜发布顺序。
 * 不在 siteNames 里的站点保持原有相对顺序，排在最后。
 */
export function orderTargetSitesBySiteList(names: string[], siteNames: string[]): string[] {
  if (!Array.isArray(names) || names.length <= 1 || !Array.isArray(siteNames) || siteNames.length === 0) {
    return Array.isArray(names) ? names.slice() : []
  }
  const rank = new Map<string, number>()
  siteNames.forEach((name, index) => {
    if (!rank.has(name)) rank.set(name, index)
  })
  const known: string[] = []
  const unknown: string[] = []
  names.forEach((name) => {
    if (rank.has(name)) known.push(name)
    else unknown.push(name)
  })
  known.sort((a, b) => (rank.get(a) ?? 0) - (rank.get(b) ?? 0))
  return [...known, ...unknown]
}
