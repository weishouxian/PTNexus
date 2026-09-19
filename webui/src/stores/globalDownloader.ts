import { defineStore } from 'pinia'
import { ref } from 'vue'
import axios from 'axios'
import type { Downloader } from '@/types'

// 顶部「全局下载器」选择：跨页面共享当前关注的下载器。
// 约定：
// 1. 空字符串表示“全部下载器”，此时各页面不额外限定下载器范围。
// 2. 选择结果服务端持久化（ui_settings.global_downloader），换设备打开后保持一致。
// 3. 页面通过 watch selectedDownloaderId 实现“顶部切换即生效”。
export const useGlobalDownloaderStore = defineStore('globalDownloader', () => {
  const selectedDownloaderId = ref<string>('')
  const downloaders = ref<Downloader[]>([])
  const isSelectionLoaded = ref(false)
  const isDownloadersLoaded = ref(false)

  // 并发去重：顶部与页面可能同时触发首次加载，避免重复请求。
  let selectionPromise: Promise<string> | null = null
  let downloadersPromise: Promise<Downloader[]> | null = null

  // 读取服务端保存的全局下载器选择。
  // 参数/返回：forceRefresh 为 true 时强制重新请求；返回当前选中的下载器 ID（可能为空）。
  // 失败场景：请求失败时保留内存中的旧值，不抛出异常。
  // 副作用：发起 GET /api/ui_settings/global_downloader 请求。
  const loadSelection = async (forceRefresh = false): Promise<string> => {
    if (!forceRefresh && isSelectionLoaded.value) {
      return selectedDownloaderId.value
    }
    if (!forceRefresh && selectionPromise) {
      return selectionPromise
    }

    selectionPromise = (async () => {
      try {
        const response = await axios.get('/api/ui_settings/global_downloader')
        const raw = response.data?.downloader_id
        selectedDownloaderId.value = typeof raw === 'string' ? raw.trim() : ''
      } catch (e) {
        console.error('加载全局下载器选择失败:', e)
      } finally {
        isSelectionLoaded.value = true
      }
      return selectedDownloaderId.value
    })()

    try {
      return await selectionPromise
    } finally {
      selectionPromise = null
    }
  }

  // 拉取下载器列表（顶部下拉选项）。
  // 参数/返回：forceRefresh 为 true 时强制重新请求；返回下载器数组。
  // 失败场景：请求失败时返回已有缓存（可能为空数组）。
  // 副作用：发起 GET /api/all_downloaders 请求。
  const fetchDownloaders = async (forceRefresh = false): Promise<Downloader[]> => {
    if (!forceRefresh && isDownloadersLoaded.value) {
      return downloaders.value
    }
    if (!forceRefresh && downloadersPromise) {
      return downloadersPromise
    }

    downloadersPromise = (async () => {
      try {
        const response = await axios.get('/api/all_downloaders')
        downloaders.value = Array.isArray(response.data) ? response.data : []
        isDownloadersLoaded.value = true
      } catch (e) {
        console.error('获取下载器列表失败:', e)
      }
      return downloaders.value
    })()

    try {
      return await downloadersPromise
    } finally {
      downloadersPromise = null
    }
  }

  // 设置全局下载器选择（空字符串代表“全部下载器”）。
  // 参数/返回：id 为目标下载器 ID；返回服务端保存后的值。
  // 失败场景：保存失败时回滚为上一个值并向上抛出，交由调用方提示。
  // 副作用：发起 POST /api/ui_settings/global_downloader 请求并先本地更新。
  const setSelectedDownloaderId = async (id: string): Promise<string> => {
    const normalized = (id || '').trim()
    const previous = selectedDownloaderId.value
    if (normalized === previous) {
      return previous
    }

    selectedDownloaderId.value = normalized
    isSelectionLoaded.value = true
    try {
      await axios.post('/api/ui_settings/global_downloader', { downloader_id: normalized })
    } catch (e) {
      selectedDownloaderId.value = previous
      throw e
    }
    return normalized
  }

  // 清空缓存标记，供“全局刷新”等场景强制重新拉取列表与选择。
  const invalidate = () => {
    isDownloadersLoaded.value = false
    isSelectionLoaded.value = false
  }

  return {
    selectedDownloaderId,
    downloaders,
    isSelectionLoaded,
    isDownloadersLoaded,
    loadSelection,
    fetchDownloaders,
    setSelectedDownloaderId,
    invalidate,
  }
})
