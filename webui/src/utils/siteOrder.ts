/**
 * 全站统一的「站点展示顺序」。
 *
 * 真源是「站点管理」页的排序（sites.sort_order，未排序=0 排最后），
 * 后端 /api/sites/status 已按该顺序返回，这里只做一次内存缓存 + 复用排序，
 * 保证所有展示站点列表的地方（筛选下拉、展开行站点标签、站点选择面板等）顺序一致。
 *
 * 用法：
 *   const sorted = computed(() => sortSitesByManagedOrder(allSites.value))
 * 并在组件 onMounted（或 App.vue 启动时）调用 ensureSiteOrderLoaded()。
 * sortSitesByManagedOrder 读的是响应式 ref，站点顺序加载完成后依赖它的 computed 会自动重算。
 */
import { ref } from 'vue'
import axios from 'axios'

/** 站点昵称顺序（与站点管理页一致）。 */
const managedSiteOrder = ref<string[]>([])

/** 归一化站点名：去空格 + 小写，仅用于匹配，不影响展示。 */
const normalizeSiteKey = (name: unknown): string => String(name ?? '').trim().toLowerCase()

let loadPromise: Promise<string[]> | null = null

/** 读取当前缓存的站点顺序（可能为空，表示尚未加载）。 */
export const getManagedSiteOrder = (): string[] => managedSiteOrder.value

/**
 * 确保站点顺序已加载（幂等，并发只发一次请求）。
 * 参数：force 为 true 时强制重新拉取（站点管理页调整排序后调用）。
 * 返回：站点昵称数组；请求失败时返回空数组（展示回落为原始顺序，不阻断页面）。
 */
export async function ensureSiteOrderLoaded(force = false): Promise<string[]> {
  if (!force && managedSiteOrder.value.length > 0) return managedSiteOrder.value
  if (!force && loadPromise) return loadPromise

  loadPromise = axios
    .get('/api/sites/status')
    .then((response) => {
      const payload = response.data
      const list = Array.isArray(payload) ? payload : Array.isArray(payload?.sites) ? payload.sites : []
      const names = list
        .map((item: unknown) =>
          typeof item === 'string' ? item : String((item as { name?: unknown })?.name ?? ''),
        )
        .map((name: string) => name.trim())
        .filter((name: string) => name !== '')
      managedSiteOrder.value = names
      return names
    })
    .catch(() => {
      loadPromise = null
      return managedSiteOrder.value
    })

  return loadPromise
}

/**
 * 按「站点管理」顺序重排站点名列表。
 * 参数：names 为待展示的站点昵称（可能来自接口、可能从数据里临时聚合）。
 * 返回：新数组；顺序 = 站点管理顺序；不在站点表里的站点保持原相对顺序排在最后。
 * 说明：站点顺序尚未加载时原样返回副本，加载完成后调用方 computed 会重算。
 */
export function sortSitesByManagedOrder<T extends string>(names: readonly T[] | undefined | null): T[] {
  const list = Array.isArray(names) ? names.slice() : []
  const order = managedSiteOrder.value
  if (list.length <= 1 || order.length === 0) return list

  const rank = new Map<string, number>()
  order.forEach((name, index) => {
    const key = normalizeSiteKey(name)
    if (key !== '' && !rank.has(key)) rank.set(key, index)
  })

  const known: T[] = []
  const unknown: T[] = []
  list.forEach((name) => {
    if (rank.has(normalizeSiteKey(name))) known.push(name)
    else unknown.push(name)
  })

  known.sort((a, b) => (rank.get(normalizeSiteKey(a)) ?? 0) - (rank.get(normalizeSiteKey(b)) ?? 0))
  return [...known, ...unknown]
}
