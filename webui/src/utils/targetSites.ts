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
