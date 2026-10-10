import { computed, ref, type ComputedRef, type Ref, type WritableComputedRef } from 'vue'
import axios from 'axios'
import { ElMessageBox } from 'element-plus'
import { ElNotification } from '@/utils/uiNotify'
import { openSSE, type EventSourceLike } from '@/desktop/sse'

import type {
  AutoAddResult,
  LimitAlert,
  ProgressCounter,
  PublishDisplayResult,
  PublishDisplayStatus,
  PublishWaveSummary,
  ReverseMappings,
  StandardParamKey,
  TitleComponent,
  TorrentData,
} from '../crossSeedPanelContext'
import type { DownloaderListItem, WorkingTorrent } from './types'

export type PanelEmit = (event: 'complete' | 'cancel' | 'close-with-refresh') => void

export type RawPublishResult = Omit<PublishDisplayResult, 'displayStatus'>

export type PublishFlowDeps = {
  emit: PanelEmit
  publishScene: string

  sourceSite: ComputedRef<string>
  torrent: ComputedRef<WorkingTorrent | null>

  activeStep: Ref<number>
  isScrolledToBottom: Ref<boolean>

  isLoading: Ref<boolean>
  isEnqueueing: Ref<boolean>

  taskId: Ref<string | null>
  torrentData: Ref<TorrentData>
  reverseMappings: Ref<ReverseMappings>

  publishBatchId: Ref<string | null>
  publishBatchEventSource: Ref<EventSourceLike | null>

  publishProgress: Ref<ProgressCounter>
  downloaderProgress: Ref<ProgressCounter>
  limitAlert: Ref<LimitAlert>

  selectedTargetSites: Ref<string[]>

  autoAddExistingToDownloader: Ref<boolean>
  autoUpdateExistingTorrent: Ref<boolean>

  // 页面显式设置的发种间隔（分钟）：0 表示沿用下载器设置里的发布节奏。
  publishIntervalMinutes: Ref<number>

  downloaderList: Ref<DownloaderListItem[]>

  finalResultsList: Ref<RawPublishResult[]>
  publishResultsBySite: Ref<Record<string, RawPublishResult | undefined>>
  publishingSites: Ref<string[]>

  logContent: Ref<string>
  showLogCard: Ref<boolean>

  logProgressTaskId: Ref<string>
  showLogProgress: Ref<boolean>

  goToSelectSiteStep: () => Promise<void>

  invalidStandardParams: ComputedRef<Array<StandardParamKey | 'tags'>>
}

export type PublishFlowApi = {
  stopPublishBatchSSE: () => void
  handleLogProgressComplete: () => void
  handleLogProgressClose: () => void
  handlePublish: () => Promise<void>
  handleEnqueue: () => Promise<void>
  handlePreviousStep: () => void
  handleCancelClick: () => void
  handleScrollOrNextStep: () => void
  handleCompleteClick: () => void
  getMappedValue: (category: StandardParamKey) => string
  getMappedTags: () => string[]
  filteredTitleComponents: ComputedRef<TitleComponent[]>
  initialTitleComponents: ComputedRef<TitleComponent[]>
  unrecognizedValue: WritableComputedRef<string>
  filteredTags: ComputedRef<string[]>
  invalidTagsList: ComputedRef<string[]>
  isRestrictedTag: (tag: string) => boolean
  getTagType: (tag: string) => 'danger' | 'info'
  handleTagClose: (tag: string) => void
  isNextButtonDisabled: ComputedRef<boolean>
  nextButtonTooltipContent: ComputedRef<string>
  publishWaveSummary: ComputedRef<PublishWaveSummary | null>
  groupedResults: ComputedRef<PublishDisplayResult[][]>
  showSiteLog: (siteName: string, logs: string | undefined) => void
  filterUploadedParam: (url: string) => string
  hasValidUrlsInRow: (row: PublishDisplayResult[]) => boolean
  openAllSitesInRow: (row: PublishDisplayResult[]) => void
  getValidUrlsCount: (row: PublishDisplayResult[]) => number
  forceRepublishSite: (siteName: string) => Promise<void>
  forceRepublishingSite: Ref<string>
  /** 发布成功但自动加入下载器失败的站点：单站一键重新添加（不会再次发种）。 */
  reAddSiteToDownloader: (siteName: string, options?: { silent?: boolean }) => Promise<boolean>
  /** 一键把全部「加种失败」的站点逐个重新添加。 */
  reAddFailedSitesToDownloader: () => Promise<void>
  /** 正在重新添加的站点名，用于卡片按钮 loading。 */
  reAddingSites: Ref<string[]>
  /** 批量重新添加进行中。 */
  isReAddingFailedSites: Ref<boolean>
  /** 当前可一键重试的失败站点名列表（为空表示没有需要处理的站点）。 */
  failedDownloaderSites: ComputedRef<string[]>
}

export function createPublishFlow(deps: PublishFlowDeps): PublishFlowApi {
  const {
    emit,
    publishScene,
    sourceSite,
    torrent,
    activeStep,
    isScrolledToBottom,
    isLoading,
    isEnqueueing,
    taskId,
    torrentData,
    reverseMappings,
    publishBatchId,
    publishBatchEventSource,
    publishProgress,
    downloaderProgress,
    limitAlert,
    selectedTargetSites,
    autoAddExistingToDownloader,
    autoUpdateExistingTorrent,
    publishIntervalMinutes,
    downloaderList,
    finalResultsList,
    publishResultsBySite,
    publishingSites,
    logContent,
    showLogCard,
    logProgressTaskId,
    showLogProgress,
    goToSelectSiteStep,
    invalidStandardParams,
  } = deps

  const stopPublishBatchSSE = () => {
    if (publishBatchEventSource.value) {
      publishBatchEventSource.value.close()
      publishBatchEventSource.value = null
    }
    publishBatchId.value = null
  }

  type RawPublishResultServer = Partial<RawPublishResult> & Record<string, unknown>

  const normalizePublishResult = (siteName: string, raw: unknown): RawPublishResult => {
    const rawResult = (typeof raw === 'object' && raw !== null ? raw : {}) as RawPublishResultServer
    const logs = typeof rawResult.logs === 'string' ? rawResult.logs : ''
    const fallbackMessage = rawResult.success === false ? '发布失败' : '发布成功'
    const result: RawPublishResult = {
      ...rawResult,
      siteName,
      message: getCleanMessage(logs || fallbackMessage),
    }

    if (rawResult.is_existing_torrent === true) {
      result.isExisted = true
    } else if (logs && (logs.includes('种子已存在') || logs.includes('该种子已存在'))) {
      result.isExisted = true
    }

    // 🚫 发布前预检查限制
    if (rawResult.pre_check === true && rawResult.limit_reached === true) {
      result.downloaderStatus = {
        success: false,
        message: logs || '发布前预检查触发限制',
        downloaderName: '发布前限制',
      }
      return result
    }

    // 自动添加到下载器结果
    if (rawResult.auto_add_result) {
      const addResult = rawResult.auto_add_result
      let downloaderName = '自动检测'

      if (addResult.limit_reached) {
        downloaderName = '限制触发'
      } else if (addResult.downloader_id) {
        const downloader = downloaderList.value.find((d) => d.id === addResult.downloader_id)
        if (downloader) downloaderName = downloader.name
      }

      result.downloaderStatus = {
        success: addResult.success,
        message: addResult.message,
        downloaderName,
      }
    }

    return result
  }

  const rebuildFinalResultsList = () => {
    finalResultsList.value = selectedTargetSites.value
      .map((site) => publishResultsBySite.value[site])
      .filter((result): result is RawPublishResult => Boolean(result))
  }

  const rebuildProgress = () => {
    const results = Object.values(publishResultsBySite.value)
    publishProgress.value.current = results.length
    downloaderProgress.value.current = results.filter((r) => r?.auto_add_result?.success).length
  }

  // 正在强制执行「仍要发布」的站点名，用于按钮 loading（空串表示没有进行中的强制发布）。
  const forceRepublishingSite = ref('')

  // 对被 dupe 拦截的站点执行一次「仍要发布」：二次确认后带 skip_dupe_check 重新发布该站。
  // 标记只作用于本次请求，不会改动站点设置里的 dupe 开关。
  const forceRepublishSite = async (siteName: string) => {
    const currentTorrent = torrent.value
    if (!currentTorrent) {
      ElNotification({
        title: '参数错误',
        message: '当前种子为空，请刷新后重试',
        type: 'error',
        duration: 0,
        showClose: true,
      })
      return
    }

    try {
      await ElMessageBox.confirm(
        `站点「${siteName}」已判定为重复（同制作组且体积接近）。强制发布会再次上传，站点上可能出现重复种子，请确认。`,
        '仍要发布',
        {
          confirmButtonText: '仍要发布',
          cancelButtonText: '取消',
          type: 'warning',
        },
      )
    } catch {
      return
    }

    forceRepublishingSite.value = siteName
    try {
      const response = await axios.post('/api/migrate/publish', {
        task_id: taskId.value,
        upload_data: {
          ...torrentData.value,
          save_path: currentTorrent.save_path,
        },
        targetSite: siteName,
        sourceSite: sourceSite.value,
        downloaderId: currentTorrent.downloaderId,
        auto_add_to_downloader: true,
        auto_add_existing_to_downloader: autoAddExistingToDownloader.value,
        auto_update_existing_torrent: autoUpdateExistingTorrent.value,
        publish_scene: publishScene,
        // 后端据此跳过该站的 dupe 查重（单次生效）。
        skip_dupe_check: true,
      })

      publishResultsBySite.value[siteName] = normalizePublishResult(siteName, response.data)
      rebuildFinalResultsList()
      rebuildProgress()

      if (response.data?.success) {
        ElNotification({
          title: '强制发布成功',
          message: `${siteName} 已跳过查重并发布成功。`,
          type: 'success',
          duration: 0,
          showClose: true,
        })
      } else {
        ElNotification({
          title: '强制发布未成功',
          message: getCleanMessage(response.data?.logs || '发布失败，请查看日志。'),
          type: 'warning',
          duration: 0,
          showClose: true,
        })
      }
    } catch (error: unknown) {
      const message = axios.isAxiosError(error)
        ? String(
            (error.response?.data as { message?: string; logs?: string } | undefined)?.message ||
              (error.response?.data as { message?: string; logs?: string } | undefined)?.logs ||
              error.message,
          )
        : '请求失败，请检查网络或后端服务。'
      ElNotification({
        title: '强制发布失败',
        message,
        type: 'error',
        duration: 0,
        showClose: true,
      })
    } finally {
      forceRepublishingSite.value = ''
    }
  }

  const isBatchPublishing = ref(false)
  const batchPublishConcurrency = ref(1)
  // 当前批次是否按发种间隔分波发布；以及波间隔（分钟）。
  // 分波时波与波之间会串行等待间隔，剩余站点属于「已在队列中待发布」。
  const pacingActive = ref(false)
  const pacingIntervalMinutes = ref(0)

  // 页面显式设置的发种间隔（分钟）；0 表示不覆盖，沿用下载器设置里的发布节奏。
  // 上限与「设置 → 下载器」的间隔输入保持一致（1440 分钟 = 1 天）。
  const resolvePublishIntervalOverride = (): number => {
    const raw = Number(publishIntervalMinutes.value)
    if (!Number.isFinite(raw) || raw <= 0) return 0
    return Math.min(1440, Math.floor(raw))
  }

  const resetBatchPublishRuntime = () => {
    isBatchPublishing.value = false
    batchPublishConcurrency.value = 1
    pacingActive.value = false
    pacingIntervalMinutes.value = 0
  }

  const setBatchPublishRuntime = (siteCount: number, rawConcurrency: unknown) => {
    const parsed = Number(rawConcurrency)
    const safeConcurrency = Number.isFinite(parsed) && parsed > 0 ? Math.floor(parsed) : 1
    batchPublishConcurrency.value = Math.max(1, Math.min(siteCount, safeConcurrency))
    isBatchPublishing.value = true
  }

  const handlePublishBatch = async (): Promise<boolean> => {
    stopPublishBatchSSE()
    resetBatchPublishRuntime()

    activeStep.value = 3
    isLoading.value = true
    finalResultsList.value = []
    publishResultsBySite.value = {}
    publishingSites.value = []
    limitAlert.value = { visible: false, title: '', message: '' }
    logContent.value = ''

    const siteCount = selectedTargetSites.value.length
    publishProgress.value = { current: 0, total: siteCount }
    downloaderProgress.value = { current: 0, total: siteCount }

    ElNotification({
      title: '正在发布',
      message: `准备向 ${siteCount} 个站点发布种子...`,
      type: 'info',
      duration: 0,
    })

    const currentTorrent = torrent.value
    if (!currentTorrent) {
      ElNotification.closeAll()
      ElNotification.error({
        title: '参数错误',
        message: '当前种子为空，请刷新后重试',
        duration: 0,
        showClose: true,
      })
      isLoading.value = false
      return false
    }

    try {
      const startResponse = await axios.post('/api/migrate/publish_batch/start', {
        task_id: taskId.value,
        upload_data: {
          ...torrentData.value,
          save_path: currentTorrent.save_path,
        },
        targetSites: selectedTargetSites.value,
        sourceSite: sourceSite.value,
        downloaderId: currentTorrent.downloaderId,
        auto_add_to_downloader: true,
        auto_add_existing_to_downloader: autoAddExistingToDownloader.value,
        auto_update_existing_torrent: autoUpdateExistingTorrent.value,
        publish_scene: publishScene,
        // 页面显式设置的发种间隔：0 表示由后端沿用下载器设置里的发布节奏。
        publish_interval_minutes: resolvePublishIntervalOverride(),
      })

      if (!startResponse.data?.success || !startResponse.data?.batch_id) {
        throw new Error(startResponse.data?.message || '批量发布任务启动失败')
      }

      setBatchPublishRuntime(siteCount, startResponse.data?.concurrency)

      const pacingMinutes = Number(startResponse.data?.publish_interval_minutes || 0)
      pacingActive.value = pacingMinutes > 0
      pacingIntervalMinutes.value = pacingMinutes
      if (pacingMinutes > 0) {
        const overrideMinutes = resolvePublishIntervalOverride()
        ElNotification({
          title: overrideMinutes > 0 ? '已按页面设置的发种间隔执行' : '已按下载器发布节奏执行',
          message: `每 ${startResponse.data?.concurrency ?? 1} 个站点一波，波间隔 ${pacingMinutes} 分钟`,
          type: 'info',
          duration: 4000,
        })
      }
      publishBatchId.value = startResponse.data.batch_id
      publishBatchEventSource.value = openSSE(
        `/api/migrate/publish_batch/stream/${publishBatchId.value}`,
      )

      publishBatchEventSource.value.onmessage = async (event) => {
        try {
          const data = JSON.parse(event.data)

          switch (data.type) {
            case 'heartbeat':
            case 'connected':
            case 'complete':
              return

            case 'batch_stopped': {
              const reason = data.reason as string
              const message = data.message as string
              const title =
                reason === 'limit_reached'
                  ? '发种限制触发'
                  : reason === 'pre_check_limit'
                    ? '发布前限制触发'
                    : reason === 'cancelled'
                      ? '已取消'
                      : '批量发布已停止'

              limitAlert.value = {
                visible: true,
                title,
                message: message || '',
              }
              return
            }

            case 'site_started': {
              const siteName = data.siteName as string
              if (siteName && !publishingSites.value.includes(siteName)) {
                publishingSites.value.push(siteName)
              }
              return
            }

            case 'site_finished': {
              const siteName = data.siteName as string
              if (siteName) {
                const idx = publishingSites.value.indexOf(siteName)
                if (idx !== -1) publishingSites.value.splice(idx, 1)
              }

              publishResultsBySite.value[siteName] = normalizePublishResult(siteName, data.result)
              rebuildFinalResultsList()
              rebuildProgress()
              return
            }

            case 'batch_finished': {
              resetBatchPublishRuntime()
              stopPublishBatchSSE()
              ElNotification.closeAll()

              rebuildFinalResultsList()
              rebuildProgress()

              const results = finalResultsList.value
              const totalCount = selectedTargetSites.value.length
              const publishSuccessCount = results.filter((r) => r.success).length
              const addSuccessCount = results.filter((r) => r.downloaderStatus?.success).length

              ElNotification.success({
                title: '发布完成',
                message: `发布成功 ${publishSuccessCount} / ${totalCount}，下载器添加成功 ${addSuccessCount} / ${totalCount}。`,
              })

              const siteLogs = results.map((r) => {
                const logs = r.logs || 'No logs available.'
                let logEntry = `--- Log for ${r.siteName} ---\n${logs}`
                if (r.downloaderStatus) {
                  logEntry += `\n\n--- Downloader Status for ${r.siteName} ---`
                  logEntry += r.downloaderStatus.success
                    ? `\n✅ 成功: ${r.downloaderStatus.message}`
                    : `\n❌ 失败: ${r.downloaderStatus.message}`
                }
                return logEntry
              })
              logContent.value = siteLogs.join('\n\n')

              // 发布后不再触发种子数据同步：全量同步耗时长（多下载器可达数分钟）且会占用刷新互斥锁，
              // 使期间的手动刷新被拒。发布结果本身已落库，列表由 fetchData 读取即可；
              // 下载器侧新增种子交给定时同步覆盖。
              isLoading.value = false
              return
            }

            case 'error':
              throw new Error(data.message || '批量发布 SSE 错误')

            default:
              return
          }
        } catch (error) {
          console.error('批量发布 SSE 消息处理失败:', error)
        }
      }

      publishBatchEventSource.value.onerror = (error) => {
        console.error('批量发布 SSE 连接错误:', error)
        resetBatchPublishRuntime()
        stopPublishBatchSSE()
        ElNotification.closeAll()
        ElNotification.error({
          title: '连接错误',
          message: '批量发布进度连接中断，请稍后重试',
          duration: 0,
          showClose: true,
        })
        isLoading.value = false
      }

      return true
    } catch (error: unknown) {
      console.error('批量发布启动失败:', error)
      resetBatchPublishRuntime()
      stopPublishBatchSSE()
      ElNotification.closeAll()
      handleApiError(error, '批量发布启动失败')
      isLoading.value = false
      return false
    }
  }

  const handlePublishSerial = async () => {
    resetBatchPublishRuntime()
    activeStep.value = 3
    isLoading.value = true
    finalResultsList.value = []

    // 串行回退路径同样支持发种间隔：置为分波（每波 1 站）以便展示排队进度。
    const serialIntervalMinutes = resolvePublishIntervalOverride()
    pacingActive.value = serialIntervalMinutes > 0
    pacingIntervalMinutes.value = serialIntervalMinutes

    // Initialize progress tracking - 确保进度条立即显示
    const siteCount = selectedTargetSites.value.length
    publishProgress.value = { current: 0, total: siteCount }
    downloaderProgress.value = { current: 0, total: siteCount }

    ElNotification({
      title: '正在发布',
      message: `准备向 ${selectedTargetSites.value.length} 个站点发布种子...`,
      type: 'info',
      duration: 0,
    })

    const currentTorrent = torrent.value
    if (!currentTorrent) {
      ElNotification.closeAll()
      ElNotification.error({
        title: '参数错误',
        message: '当前种子为空，请刷新后重试',
        duration: 0,
        showClose: true,
      })
      isLoading.value = false
      return
    }

    const results = []

    for (const [siteIndex, siteName] of selectedTargetSites.value.entries()) {
      try {
        const response = await axios.post('/api/migrate/publish', {
          task_id: taskId.value,
          upload_data: {
            ...torrentData.value,
            save_path: currentTorrent.save_path, // 添加 save_path
          },
          targetSite: siteName,
          sourceSite: sourceSite.value,
          downloaderId: currentTorrent.downloaderId, // 新增：传递下载器ID
          auto_add_to_downloader: true, // 新增：启用自动添加
          auto_add_existing_to_downloader: autoAddExistingToDownloader.value,
          auto_update_existing_torrent: autoUpdateExistingTorrent.value,
          publish_scene: publishScene,
        })

        const fallbackMessage = response.data?.success === false ? '发布失败' : '发布成功'
        const result = {
          siteName,
          message: getCleanMessage(response.data.logs || fallbackMessage),
          ...response.data,
        }

        if (response.data?.is_existing_torrent === true) {
          result.isExisted = true
        } else if (
          response.data?.logs &&
          (response.data.logs.includes('种子已存在') || response.data.logs.includes('该种子已存在'))
        ) {
          result.isExisted = true
        }

        // 🚫 检查发种限制状态
        if (result.auto_add_result && result.auto_add_result.limit_reached) {
          // 提取限制信息用于突出显示
          const limitInfo = result.auto_add_result.message

          result.downloaderStatus = {
            success: false,
            message: result.auto_add_result.message,
            downloaderName: '限制触发',
            limit_reached: true,
          }

          results.push(result)
          finalResultsList.value = [...results]

          // 🚫 显示限制提示
          limitAlert.value = {
            visible: true,
            title: '发种限制触发',
            message: limitInfo,
          }

          // 在日志顶部突出显示限制信息
          logContent.value =
            `\n\n=== 🚫 发种限制触发 ===\n${limitInfo}\n\n=== 🛑 批量发布已停止 ===\n由于发种限制触发，后续 ${selectedTargetSites.value.length - results.length} 个站点发布已暂停。\n\n` +
            logContent.value

          // 显示限制通知
          ElNotification({
            title: '发种限制触发',
            message: `${siteName} 发布成功但因限制无法添加到下载器\n${limitInfo}\n后续站点发布已自动停止。`,
            type: 'warning',
            duration: 0,
            showClose: true,
          })

          // 跳出循环
          break
        }

        // 🚫 检查发布前预检查状态
        if (result.pre_check && result.limit_reached) {
          // 提取限制信息用于突出显示
          const limitInfo = result.message.replace('🚫 发布前预检查触发限制: ', '')

          result.downloaderStatus = {
            success: false,
            message: result.message,
            downloaderName: '发布前限制',
            limit_reached: true,
            pre_check: true,
          }

          results.push(result)
          finalResultsList.value = [...results]

          // 🚫 显示限制提示
          limitAlert.value = {
            visible: true,
            title: '发布前限制触发',
            message: limitInfo,
          }

          // 在日志顶部突出显示限制信息
          logContent.value =
            `\n\n=== 🚫 发种限制触发 ===\n${limitInfo}\n\n=== 🛑 批量发布已停止 ===\n由于发种限制触发，后续 ${selectedTargetSites.value.length - results.length} 个站点发布已暂停。\n\n` +
            logContent.value

          // 显示发布前限制通知
          ElNotification({
            title: '发布前限制触发',
            message: `${siteName} 因发种限制无法发布\n${limitInfo}\n后续站点发布已自动停止。`,
            type: 'warning',
            duration: 0,
            showClose: true,
          })

          // 跳出循环
          break
        }

        // 立即更新下载器状态
        if (result.auto_add_result) {
          // 获取实际的下载器名称
          let downloaderName = '自动检测'
          if (result.auto_add_result.downloader_id) {
            const downloader = downloaderList.value.find(
              (d) => d.id === result.auto_add_result.downloader_id,
            )
            if (downloader) {
              downloaderName = downloader.name
            }
          }

          result.downloaderStatus = {
            success: result.auto_add_result.success,
            message: result.auto_add_result.message,
            downloaderName: downloaderName,
          }

          // 立即更新下载器进度
          if (result.auto_add_result.success) {
            downloaderProgress.value.current++
          }
        }

        results.push(result)
        finalResultsList.value = [...results]

        if (result.success) {
          if (result.downloaderStatus?.success === false) {
            ElNotification.warning({
              title: `发布成功但添加失败 - ${siteName}`,
              message: result.downloaderStatus.message || '自动添加到下载器失败',
            })
          } else {
            ElNotification.success({
              title: `发布成功 - ${siteName}`,
              message: '种子已成功发布到该站点',
            })
          }
        }
      } catch (error: unknown) {
        const logs = axios.isAxiosError(error)
          ? (error.response?.data as { logs?: string; message?: string } | undefined)?.logs ||
            (error.response?.data as { message?: string } | undefined)?.message ||
            error.message
          : error instanceof Error
            ? error.message
            : String(error)
        const result = {
          siteName,
          success: false,
          logs,
          url: null,
          message: `发布到 ${siteName} 时发生错误，请查看日志。`,
          downloaderStatus: {
            success: false,
            message: '发布失败，无法添加到下载器',
            downloaderName: '错误',
          },
        }
        results.push(result)
        finalResultsList.value = [...results]
        ElNotification.error({
          title: `发布失败 - ${siteName}`,
          message: result.message,
        })
      }
      // Update publish progress
      publishProgress.value.current++
      // 站点之间的等待：页面设置了发种间隔时按间隔错峰（回退路径本就是逐站串行，等价并发 1），
      // 否则沿用原来的 1 秒缓冲；最后一个站点不再等待。
      const serialSiteList = selectedTargetSites.value
      if (siteIndex < serialSiteList.length - 1) {
        const intervalMinutes = resolvePublishIntervalOverride()
        if (intervalMinutes > 0) {
          ElNotification({
            title: '等待发种间隔',
            message: `将在 ${intervalMinutes} 分钟后发布下一站（共 ${serialSiteList.length} 站）`,
            type: 'info',
            duration: 3000,
          })
          await new Promise((resolve) => setTimeout(resolve, intervalMinutes * 60_000))
        } else {
          await new Promise((resolve) => setTimeout(resolve, 1000))
        }
      }
    }

    ElNotification.closeAll()
    const totalCount = selectedTargetSites.value.length
    const publishSuccessCount = results.filter((r) => r.success).length
    const addSuccessCount = results.filter((r) => r?.downloaderStatus?.success).length
    ElNotification.success({
      title: '发布完成',
      message: `发布成功 ${publishSuccessCount} / ${totalCount}，下载器添加成功 ${addSuccessCount} / ${totalCount}。`,
    })

    // 处理自动添加到下载器的结果
    logContent.value += '\n\n--- [自动添加任务结果] ---'
    const downloaderStatusMap: Record<
      string,
      { success: boolean; message: string; downloaderName: string }
    > = {}

    // 从 Python 返回的结果中提取 auto_add_result
    results.forEach((result) => {
      if (result.auto_add_result) {
        // 优先使用已经存在的 downloaderStatus 中的名称（已在上面正确设置）
        const existingDownloaderName = result.downloaderStatus?.downloaderName || '自动检测'

        downloaderStatusMap[result.siteName] = {
          success: result.auto_add_result.success,
          message: result.auto_add_result.message,
          downloaderName: existingDownloaderName,
        }
        const statusIcon = result.auto_add_result.success ? '✅' : '❌'
        const statusText = result.auto_add_result.success ? '成功' : '失败'
        logContent.value += `\n[${result.siteName}] ${statusIcon} ${statusText}: ${result.auto_add_result.message}`
      } else if (result.success && result.url) {
        // 如果没有 auto_add_result，说明可能跳过了自动添加
        logContent.value += `\n[${result.siteName}] ⚠️  未执行自动添加`
      }
    })
    logContent.value += '\n--- [自动添加任务结束] ---'

    const siteLogs = results.map((r) => {
      let logEntry = `--- Log for ${r.siteName} ---\n${r.logs || 'No logs available.'}`
      if (downloaderStatusMap[r.siteName]) {
        const status = downloaderStatusMap[r.siteName]
        logEntry += `\n\n--- Downloader Status for ${r.siteName} ---`
        if (status.success) {
          logEntry += `\n✅ 成功: ${status.message}`
        } else {
          logEntry += `\n❌ 失败: ${status.message}`
        }
      }
      return logEntry
    })
    logContent.value = siteLogs.join('\n\n')

    finalResultsList.value = results.map((result) => ({
      ...result,
      downloaderStatus: downloaderStatusMap[result.siteName],
    }))

    // 发布后不再触发种子数据同步（原因同批量发布分支：全量同步耗时长且会占用刷新互斥锁）
    isLoading.value = false
  }

  // 受限标签确认：若原始数据包含受限标签，弹出确认对话框
  const confirmRestrictedTags = async (): Promise<boolean> => {
    const originalRestricted = initialRestrictedTags.value
    const currentTags = torrentData.value.standardized_params.tags || []
    const stillHasRestricted = currentTags.some((tag) => isRestrictedTag(tag))

    if (originalRestricted.length === 0) {
      return true // 原本就没有受限标签，无需确认
    }

    // 构建确认消息
    const removedTags = originalRestricted.filter(
      (tag) => !currentTags.includes(tag),
    )
    if (removedTags.length > 0) {
      const tagLabels = [...new Set(
        removedTags.map((t) =>
          t.includes('禁转') ? '禁转' : t.includes('限转') ? '限转' : '分集',
        ),
      )].join('/')
      try {
        await ElMessageBox.confirm(
          `该种子原本包含「${tagLabels}」标签，你已将其移除。请确认源站已允许转种后再发布，违规转种可能导致封号。`,
          '受限标签确认',
          {
            confirmButtonText: '确认发布',
            cancelButtonText: '取消',
            type: 'warning',
          },
        )
        return true
      } catch {
        return false
      }
    }

    if (stillHasRestricted) {
      // 受限标签仍然存在，后端会拦截
      return true
    }

    return true
  }

  const handlePublish = async () => {
    // 受限标签确认
    const confirmed = await confirmRestrictedTags()
    if (!confirmed) return

    // 若原始数据包含受限标签，添加跳过标记让后端放行
    const hadRestricted = initialRestrictedTags.value.length > 0
    if (hadRestricted) {
      torrentData.value.skip_restricted_check = true
    }

    const started = await handlePublishBatch()
    if (!started) {
      await handlePublishSerial()
    }
  }

  const handleEnqueue = async () => {
    if (isEnqueueing.value || isLoading.value) return
    if (selectedTargetSites.value.length === 0) {
      ElNotification.warning({ title: '提示', message: '请先选择要发布的目标站点。' })
      return
    }

    // 受限标签确认
    const confirmed = await confirmRestrictedTags()
    if (!confirmed) return

    const currentTorrent = torrent.value
    if (!currentTorrent) {
      ElNotification.error({
        title: '参数错误',
        message: '当前种子为空，请刷新后重试',
        duration: 0,
        showClose: true,
      })
      return
    }

    // 若原始数据包含受限标签，添加跳过标记让后端放行
    const hadRestricted = initialRestrictedTags.value.length > 0
    if (hadRestricted) {
      torrentData.value.skip_restricted_check = true
    }

    isEnqueueing.value = true
    try {
      const response = await axios.post('/api/migrate/publish_queue/enqueue', {
        task_id: taskId.value,
        upload_data: {
          ...torrentData.value,
          save_path: currentTorrent.save_path,
        },
        targetSites: selectedTargetSites.value,
        sourceSite: sourceSite.value,
        downloaderId: currentTorrent.downloaderId,
        auto_add_to_downloader: true,
        auto_add_existing_to_downloader: autoAddExistingToDownloader.value,
        auto_update_existing_torrent: autoUpdateExistingTorrent.value,
        publish_scene: publishScene,
        // 页面显式设置的发种间隔：0 表示由后端沿用下载器设置里的发布节奏。
        publish_interval_minutes: resolvePublishIntervalOverride(),
      })

      if (!response.data?.success || !response.data?.group_id) {
        throw new Error(response.data?.message || '加入队列失败')
      }

      ElNotification.success({
        title: '已加入队列',
        message: `队列分组: ${response.data.group_id}（${response.data.count || 0} 个站点）`,
        duration: 3000,
      })
      emit('cancel')
    } catch (error: unknown) {
      handleApiError(error, '加入队列失败')
    } finally {
      isEnqueueing.value = false
    }
  }

  const handlePreviousStep = () => {
    if (activeStep.value > 0) {
      activeStep.value--
    }
  }

  // 处理取消按钮点击
  const handleCancelClick = () => {
    // 如果在步骤3（完成发布），触发带刷新的关闭
    if (activeStep.value === 3) {
      emit('close-with-refresh')
    } else {
      emit('cancel')
    }
  }

  const previewAutoScrollSpeedPxPerSecond = 1200

  // 滚动预览区域到底部（线性匀速滚动，便于连续浏览）
  const scrollPreviewToBottom = () => {
    const panelContent = document.querySelector('.panel-content')
    if (!panelContent) return
    const { scrollTop, scrollHeight, clientHeight } = panelContent
    const remaining = scrollHeight - scrollTop - clientHeight
    if (remaining <= 5) return

    // 固定滚动速度：当前速度提升为 2 倍（600 -> 1200 px/s）
    const duration = (remaining / previewAutoScrollSpeedPxPerSecond) * 1000
    const startTime = performance.now()
    const startScroll = panelContent.scrollTop
    const target = scrollHeight - clientHeight

    const animate = (currentTime: number) => {
      const elapsed = currentTime - startTime
      const progress = Math.min(elapsed / duration, 1)
      panelContent.scrollTop = startScroll + (target - startScroll) * progress
      if (progress < 1) {
        requestAnimationFrame(animate)
      }
    }
    requestAnimationFrame(animate)
  }

  // 步骤1：点击"下一步"按钮的处理（未到底先滚动，到底再跳转）
  const handleScrollOrNextStep = () => {
    if (isScrolledToBottom.value) {
      goToSelectSiteStep()
    } else {
      scrollPreviewToBottom()
    }
  }

  // 处理完成按钮点击
  const handleCompleteClick = () => {
    emit('complete')
  }

  const getCleanMessage = (logs: string): string => {
    if (!logs || logs === '发布成功') return '发布成功'
    if (logs === '发布失败') return '发布失败'
    if (logs.includes('种子已存在')) {
      return '种子已存在，发布成功'
    }
    const lines = logs
      .split('\n')
      .filter(
        (line) =>
          line &&
          !line.includes('--- [步骤') &&
          !line.includes('INFO - ---') &&
          !line.startsWith('详情页链接:') &&
          !line.startsWith('直链下载:'),
      )
    const cleanLines = lines.map((line) => line.replace(/^\d{2}:\d{2}:\d{2} - \w+ - /, ''))
    return cleanLines.filter(Boolean).pop() || (logs.includes('失败') ? '发布失败' : '发布成功')
  }

  const handleApiError = (error: unknown, defaultMessage: string) => {
    const message = axios.isAxiosError(error)
      ? (error.response?.data as { logs?: string; message?: string } | undefined)?.logs ||
        (error.response?.data as { message?: string } | undefined)?.message ||
        error.message ||
        defaultMessage
      : error instanceof Error
        ? error.message || defaultMessage
        : defaultMessage
    ElNotification.error({ title: '操作失败', message, duration: 0, showClose: true })
  }

  // 辅助函数：获取映射后的中文值
  const getMappedValue = (category: StandardParamKey): string => {
    const standardizedParams = torrentData.value.standardized_params
    const standardValue = standardizedParams[category]
    if (!standardValue) return 'N/A'

    const mappings = reverseMappings.value[category]
    return mappings?.[standardValue] || standardValue
  }

  // 辅助函数：获取映射后的标签列表
  const getMappedTags = () => {
    // 使用 filteredTags 计算属性来过滤掉空标签
    if (!filteredTags.value || !reverseMappings.value.tags) return []

    return filteredTags.value.map((tag: string) => {
      return reverseMappings.value.tags[tag] || tag
    })
  }

  // Computed properties for filtered title components
  const filteredTitleComponents = computed<TitleComponent[]>(() => {
    return torrentData.value.title_components.filter((param) => param.key !== '无法识别')
  })
  // 计算属性：过滤掉空标签
  const filteredTags = computed(() => {
    const tags = torrentData.value.standardized_params.tags
    return tags?.filter((tag) => tag && typeof tag === 'string' && tag.trim() !== '') || []
  })

  // 【新增】计算属性：专门用于找出并返回所有格式不正确的标签列表
  const invalidTagsList = computed(() => {
    // 定义支持中文和连字符的灵活正则表达式
    // \p{L} -> 匹配任何语言的字母 (包括中文)
    // \p{N} -> 匹配任何语言的数字
    // _-  -> 匹配下划线和连字符
    // u 标志 -> 启用 Unicode 支持
    const flexibleRegex = new RegExp(/^[\p{L}\p{N}_-]+\.[\p{L}\p{N}_+-]+$/u)

    // 从已过滤的标签中，再次过滤出不符合新正则的标签
    return filteredTags.value.filter((tag) => !flexibleRegex.test(tag))
  })

  const getTagType = (tag: string): 'danger' | 'info' => {
    if (isRestrictedTag(tag) || invalidTagsList.value.includes(tag)) {
      return 'danger'
    }
    return 'info'
  }
  // 计算属性：为未解析的标题提供初始参数框
  const initialTitleComponents = computed<TitleComponent[]>(() => {
    // 定义常见的标题参数键
    const commonKeys = [
      '主标题',
      '季集',
      '年份',
      '剧集状态',
      '发布版本',
      '分辨率',
      '片源平台',
      '媒介',
      '视频编码',
      '视频格式',
      'HDR格式',
      '色深',
      '帧率',
      '音频编码',
      '制作组',
    ]
    // 创建带有空值的初始参数数组
    return commonKeys.map((key) => ({
      key,
      value: '',
    }))
  })

  // 检查是否为受限标签（禁转或tag.禁转）
  const isRestrictedTag = (tag: string): boolean => {
    return (
      tag === '禁转' ||
      tag === 'tag.禁转' ||
      tag === '限转' ||
      tag === 'tag.限转' ||
      tag === '分集' ||
      tag === 'tag.分集'
    )
  }

  // 记录初始受限标签（用于发布时确认提示，在流程创建时快照）
  const initialRestrictedTags = ref<string[]>(
    (torrentData.value.standardized_params.tags || []).filter((tag) => isRestrictedTag(tag)),
  )

  const handleTagClose = (tagToRemove: string) => {
    const index = torrentData.value.standardized_params.tags.indexOf(tagToRemove)
    if (index > -1) {
      torrentData.value.standardized_params.tags.splice(index, 1)
    }
  }

  const unrecognizedValue = computed<string>({
    // Getter: 当模板需要读取值时调用
    get() {
      const unrecognized = torrentData.value.title_components.find(
        (param) => param.key === '无法识别',
      )
      return unrecognized ? unrecognized.value : '' // 返回找到的值，或者空字符串
    },
    // Setter: 当 v-model 试图修改值时调用
    set(newValue) {
      const index = torrentData.value.title_components.findIndex(
        (param) => param.key === '无法识别',
      )

      // 如果新输入的值是空的，就从数组里删除这个项目
      if (newValue === '' || newValue === null) {
        if (index !== -1) {
          torrentData.value.title_components.splice(index, 1)
        }
      } else {
        // 如果项目已存在，就更新它的值
        if (index !== -1) {
          torrentData.value.title_components[index].value = newValue
        } else {
          // 如果项目不存在，就创建一个新的推进数组
          torrentData.value.title_components.push({
            key: '无法识别',
            value: newValue,
          })
        }
      }
    },
  })

  // 计算属性：检查下一步按钮是否应该禁用
  const isNextButtonDisabled = computed(() => {
    // 1. 检查“无法识别”
    const unrecognized = torrentData.value.title_components.find(
      (param) => param.key === '无法识别',
    )
    const hasUnrecognized = unrecognized && unrecognized.value !== ''

    // 1.5 检查年份是否为空
    const yearComponent = torrentData.value.title_components.find(
      (param) => param.key === '年份',
    )
    const hasEmptyYear = !yearComponent || !yearComponent.value || yearComponent.value.trim() === ''
    if (hasEmptyYear) {
      return true
    }

    // 2. 检查禁转标签（仅警告，不阻止继续）

    // 3. 【新增】检查简介、海报、截图是否为空
    const intro = torrentData.value.intro
    const hasEmptyPoster = !intro.poster || intro.poster.trim() === ''
    const hasEmptyScreenshots = !intro.screenshots || intro.screenshots.trim() === ''
    const hasEmptyBody = !intro.body || intro.body.trim() === ''

    if (hasEmptyPoster || hasEmptyScreenshots || hasEmptyBody) {
      return true
    }

    // 3.5 检查简介正文完整性
    const introCompleteness = checkIntroCompleteness(intro.body)
    if (!introCompleteness.isComplete) {
      return true
    }

    // 4. 检查标准参数是否为空 (类型、媒介、视频编码、音频编码、分辨率)
    const params = torrentData.value.standardized_params
    const hasEmptyType = !params.type || params.type.trim() === ''
    const hasEmptyMedium = !params.medium || params.medium.trim() === ''
    const hasEmptyVideoCodec = !params.video_codec || params.video_codec.trim() === ''
    const hasEmptyAudioCodec = !params.audio_codec || params.audio_codec.trim() === ''
    const hasEmptyResolution = !params.resolution || params.resolution.trim() === ''

    if (
      hasEmptyType ||
      hasEmptyMedium ||
      hasEmptyVideoCodec ||
      hasEmptyAudioCodec ||
      hasEmptyResolution
    ) {
      return true
    }

    // 5. 检查制作组是否为空或为NOGROUP
    const team = torrentData.value.title_components.find((param) => param.key === '制作组')
    const hasEmptyTeam = !team || !team.value || team.value.trim() === ''
    const isNoGroup = team && team.value.trim().toUpperCase() === 'NOGROUP'

    if (hasEmptyTeam || isNoGroup) {
      return true
    }

    // 6. 检查 Mediainfo 是否为空或格式无效
    const mediaInfoText = torrentData.value.mediainfo || ''
    const hasInvalidMediaInfo = !mediaInfoText || mediaInfoText.trim() === ''

    if (!hasInvalidMediaInfo) {
      // 如果有内容，进一步检查格式有效性
      const isStandardMediainfo = _isValidMediainfo(mediaInfoText)
      const isBDInfo = _isValidBDInfo(mediaInfoText)
      if (!isStandardMediainfo && !isBDInfo) {
        return true
      }
    } else {
      // 如果为空，也禁用
      return true
    }

    // 6. 检查参数格式验证
    const hasInvalidStandardParams = invalidStandardParams.value.length > 0
    if (hasInvalidStandardParams) {
      return true
    }

    if (torrentData.value.screenshot_review_status === 'pending') {
      return true
    }

    if (hasUnrecognized) {
      return true
    }

    return false
  })

  // 计算属性：获取下一步按钮的提示文本
  const nextButtonTooltipContent = computed(() => {
    // 1. 检查是否存在"无法识别"的内容
    const unrecognized = torrentData.value.title_components.find(
      (param) => param.key === '无法识别',
    )
    if (unrecognized && unrecognized.value !== '') {
      return '存在无法识别的标题内容，请手动修正或删除'
    }

    // 1.5 检查年份是否为空
    const yearComponent = torrentData.value.title_components.find(
      (param) => param.key === '年份',
    )
    if (!yearComponent || !yearComponent.value || yearComponent.value.trim() === '') {
      return '年份不能为空'
    }

    // 3. 检查制作组是否为空或为NOGROUP
    const team = torrentData.value.title_components.find((param) => param.key === '制作组')
    const hasEmptyTeam = !team || !team.value || team.value.trim() === ''
    const isNoGroup = team && team.value.trim().toUpperCase() === 'NOGROUP'

    if (hasEmptyTeam) {
      return '无制作组，禁止发布'
    }

    if (isNoGroup) {
      return '制作组为NOGROUP，禁止发布'
    }

    // 4. 检查必填参数是否为空 (包含：简介信息 + 标准化参数)
    const params = torrentData.value.standardized_params
    const intro = torrentData.value.intro
    const missingFields: string[] = []

    // --- 检查简介信息 ---
    if (!intro.poster || intro.poster.trim() === '') missingFields.push('海报')
    if (!intro.screenshots || intro.screenshots.trim() === '') missingFields.push('截图')
    if (!intro.body || intro.body.trim() === '') missingFields.push('简介正文')

    // --- 检查 Mediainfo ---
    if (!torrentData.value.mediainfo || torrentData.value.mediainfo.trim() === '')
      missingFields.push('Mediainfo')

    // --- 检查标准化参数 ---
    if (!params.type || params.type.trim() === '') missingFields.push('类型')
    if (!params.medium || params.medium.trim() === '') missingFields.push('媒介')
    if (!params.video_codec || params.video_codec.trim() === '') missingFields.push('视频编码')
    if (!params.audio_codec || params.audio_codec.trim() === '') missingFields.push('音频编码')
    if (!params.resolution || params.resolution.trim() === '') missingFields.push('分辨率')

    if (missingFields.length > 0) {
      return `请补充必填项：${missingFields.join('、')}`
    }

    // 4.5 检查简介正文完整性
    const introCompleteness = checkIntroCompleteness(intro.body)
    if (!introCompleteness.isComplete) {
      const criticalFields = ['片名', '产地', '简介']
      const missingCriticalFields = criticalFields.filter((field) =>
        introCompleteness.missingFields.includes(field),
      )
      return `简介正文缺少必填字段：${missingCriticalFields.join('、')}`
    }

    // 4. 检查参数格式 (红框/正则验证)
    if (invalidStandardParams.value.length > 0) {
      const paramNameMap: Record<string, string> = {
        type: '类型',
        medium: '媒介',
        video_codec: '视频编码',
        audio_codec: '音频编码',
        resolution: '分辨率',
        team: '制作组',
        source: '产地',
        tags: '标签',
      }
      const invalidNames = invalidStandardParams.value
        .map((key) => paramNameMap[key] || key)
        .join('、')
      return `参数格式不正确 (${invalidNames})`
    }

    // 5. 检查 MediaInfo/BDInfo 格式有效性
    const mediaInfoText = torrentData.value.mediainfo || ''
    if (!_isValidMediainfo(mediaInfoText) && !_isValidBDInfo(mediaInfoText)) {
      return 'MediaInfo 或 BDInfo 格式无效'
    }

    if (torrentData.value.screenshot_review_status === 'pending') {
      return '当前视频未检测到字幕流，请检查截图是否截到了中文字幕'
    }

    return '准备就绪'
  })

  const _isMediaTextSectionHeaderLike = (line: string): boolean => {
    const trimmed = line.trim()
    if (!trimmed) return false
    return (
      /^(General|Video|Audio|Text|Menu|Chapters)(\s*#\d+)?$/i.test(trimmed) ||
      /^(DISC INFO|PLAYLIST REPORT|QUICK SUMMARY|VIDEO:|AUDIO:|SUBTITLES:|FILES:|CHAPTERS:|DISC SIZE)$/i.test(
        trimmed,
      )
    )
  }

  const _sanitizeMediaTextForValidation = (text: string): string => {
    const normalized = (text || '').replace(/\r\n/g, '\n').replace(/\r/g, '\n').trim()
    if (!normalized) return ''

    const lines = normalized.split('\n')
    const sanitized: string[] = []
    let skipContinuation = false

    for (const line of lines) {
      const trimmed = line.trim()

      if (skipContinuation) {
        if (!trimmed) {
          continue
        }
        if (_isMediaTextSectionHeaderLike(line)) {
          skipContinuation = false
        } else if (/^[\t ]/.test(line)) {
          continue
        } else {
          skipContinuation = false
        }
      }

      if (/^\s*Description\s*:/i.test(line)) {
        const idx = line.indexOf(':')
        sanitized.push(idx >= 0 ? line.slice(0, idx + 1) : line.trimEnd())
        skipContinuation = true
        continue
      }

      sanitized.push(line)
    }

    return sanitized.join('\n').trim()
  }

  // 辅助函数：检查是否为有效的 MediaInfo 格式
  // 辅助函数：检查是否包含禁止模式
  const _hasForbiddenPatterns = (text: string): boolean => {
    const forbiddenPatterns = [
      // BBCode 标签
      { pattern: /a^/, description: 'BBCode粗体标签' },
      { pattern: /a^/, description: 'BBCode颜色标签' },
      { pattern: /a^/, description: 'BBCode大小标签' },
      { pattern: /a^/, description: 'BBCode结束标签' },

      // 特殊符号
      { pattern: /★{2,}/, description: '连续的星星符号' },
      { pattern: /。{3,}/, description: '连续的中文句号' },
      { pattern: /…{2,}/, description: '连续的省略号' },
      { pattern: /……{2,}/, description: '连续的中文省略号' },
    ]

    for (const { pattern, description } of forbiddenPatterns) {
      if (pattern.test(text)) {
        console.log(`检测到禁止模式: ${description}`)
        return true
      }
    }
    return false
  }

  // 辅助函数：检查是否为有效的 MediaInfo 格式
  const _isValidMediainfo = (text: string): boolean => {
    const standardMediainfoKeywords = [
      'General',
      'Video',
      'Audio',
      'Complete name',
      'File size',
      'Duration',
      'Width',
      'Height',
    ]

    const sanitizedText = _sanitizeMediaTextForValidation(text)
    const matches = standardMediainfoKeywords.filter((keyword) => sanitizedText.includes(keyword))
    if (matches.length < 3) {
      return false
    }

    // 关键字验证通过后，检查禁止模式
    if (_hasForbiddenPatterns(sanitizedText)) {
      return false
    }

    return true
  }

  // 辅助函数：检查是否为有效的 BDInfo 格式
  const _isValidBDInfo = (text: string): boolean => {
    const bdInfoRequiredKeywords = ['DISC INFO', 'PLAYLIST REPORT']
    const bdInfoOptionalKeywords = [
      'VIDEO:',
      'AUDIO:',
      'SUBTITLES:',
      'FILES:',
      'Disc Label',
      'Disc Size',
      'BDInfo:',
      'Protection:',
      'Codec',
      'Bitrate',
      'Language',
      'Description',
    ]

    const sanitizedText = _sanitizeMediaTextForValidation(text)
    const requiredMatches = bdInfoRequiredKeywords.filter((keyword) =>
      sanitizedText.includes(keyword),
    ).length
    const optionalMatches = bdInfoOptionalKeywords.filter((keyword) =>
      sanitizedText.includes(keyword),
    ).length

    // 必须所有必要关键字都存在，或者至少有1个必要关键字且2个以上可选关键字
    const hasRequiredKeywords =
      requiredMatches === bdInfoRequiredKeywords.length ||
      (requiredMatches >= 1 && optionalMatches >= 2)

    if (!hasRequiredKeywords) {
      return false
    }

    // 关键字验证通过后，检查禁止模式
    if (_hasForbiddenPatterns(sanitizedText)) {
      return false
    }

    return true
  }

  // 辅助函数：检查简介正文完整性 (对应 Python check_intro_completeness)
  const checkIntroCompleteness = (
    bodyText: string,
  ): {
    isComplete: boolean
    missingFields: string[]
    foundFields: string[]
  } => {
    if (!bodyText || bodyText.trim() === '') {
      return { isComplete: false, missingFields: ['所有字段'], foundFields: [] }
    }

    const requiredPatterns = {
      片名: [
        /[◎❁]\s*片\s*名/i,
        /[◎❁]\s*译\s*名/i,
        /[◎❁]\s*标\s*题/i,
        /片名\s*[:：]/i,
        /译名\s*[:：]/i,
        /Title\s*[:：]/i,
      ],
      产地: [
        /[◎❁]\s*产\s*地/i,
        /[◎❁]\s*国\s*家/i,
        /[◎❁]\s*地\s*区/i,
        /制片国家\/地区\s*[:：]/i,
        /制片国家\s*[:：]/i,
        /国家\s*[:：]/i,
        /产地\s*[:：]/i,
        /Country\s*[:：]/i,
      ],
      简介: [
        /[◎❁]\s*简\s*介/i,
        /[◎❁]\s*剧\s*情/i,
        /[◎❁]\s*内\s*容/i,
        /简介\s*[:：]/i,
        /剧情\s*[:：]/i,
        /内容简介\s*[:：]/i,
        /Plot\s*[:：]/i,
        /Synopsis\s*[:：]/i,
      ],
    }

    const foundFields: string[] = []
    const missingFields: string[] = []

    for (const [fieldName, patterns] of Object.entries(requiredPatterns)) {
      let fieldFound = false
      for (const pattern of patterns) {
        if (pattern.test(bodyText)) {
          fieldFound = true
          break
        }
      }

      if (fieldFound) {
        foundFields.push(fieldName)
      } else {
        missingFields.push(fieldName)
      }
    }

    const criticalFields = ['片名', '产地', '简介']
    const isComplete = criticalFields.every((field) => foundFields.includes(field))

    return {
      isComplete,
      missingFields,
      foundFields,
    }
  }

  const showSiteLog = (siteName: string, logs: string | undefined) => {
    let siteLogContent = `--- Log for ${siteName} ---\n${logs || 'No logs available.'}`
    const siteResult = finalResultsList.value.find((result) => result.siteName === siteName)
    if (siteResult && siteResult.downloaderStatus) {
      const status = siteResult.downloaderStatus
      siteLogContent += `\n\n--- Downloader Status for ${siteName} ---`
      if (status.success) {
        siteLogContent += `\n✅ 成功: ${status.message}`
      } else {
        siteLogContent += `\n❌ 失败: ${status.message}`
      }
    }
    logContent.value = siteLogContent
    showLogCard.value = true
  }

  const publishDisplayResults = computed<PublishDisplayResult[]>(() => {
    const resultsBySite = new Map<string, RawPublishResult>()
    for (const result of finalResultsList.value) {
      resultsBySite.set(result.siteName, result)
    }

    const unfinishedSites = selectedTargetSites.value.filter(
      (siteName) => !resultsBySite.has(siteName),
    )
    const hasUnfinishedSites = finalResultsList.value.length < selectedTargetSites.value.length
    const isStopped = limitAlert.value.visible && hasUnfinishedSites
    const runningSites = new Set(
      publishingSites.value.filter((siteName) => !resultsBySite.has(siteName)),
    )

    // 分波发布：波与波之间会串行等待发种间隔，此刻没有任何站点在实际发布，
    // 剩余站点都在队列里等下一波。此时不做「发布中」补位，统一按「待发布」展示。
    const inWaveGap =
      pacingActive.value &&
      runningSites.size === 0 &&
      resultsBySite.size > 0 &&
      resultsBySite.size < selectedTargetSites.value.length

    if (isBatchPublishing.value && !isStopped && unfinishedSites.length > 0 && !inWaveGap) {
      const expectedRunningCount = Math.min(batchPublishConcurrency.value, unfinishedSites.length)
      let missingSlots = expectedRunningCount - runningSites.size

      if (missingSlots > 0) {
        for (const siteName of unfinishedSites) {
          if (runningSites.has(siteName)) continue
          runningSites.add(siteName)
          missingSlots--
          if (missingSlots === 0) break
        }
      }
    }

    // 设置了发种间隔（分波）时，未开始的站点是在队列里等下一波，文案用「待发布」。
    const waitingMessage = pacingActive.value ? '待发布' : '等待中'

    return selectedTargetSites.value.map((siteName) => {
      const existing = resultsBySite.get(siteName)
      if (existing) {
        let displayStatus: PublishDisplayStatus = existing.success ? 'success' : 'error'
        if (existing.success && existing.downloaderStatus?.success === false) {
          displayStatus = 'warning'
        }
        return {
          ...existing,
          displayStatus,
        }
      }

      let displayStatus: PublishDisplayStatus = 'waiting'
      if (runningSites.has(siteName)) {
        displayStatus = 'publishing'
      } else if (isStopped) {
        displayStatus = 'paused'
      }

      return {
        siteName,
        displayStatus,
        success: false,
        url: null,
        logs: '',
        message:
          displayStatus === 'publishing'
            ? '发布中...'
            : displayStatus === 'paused'
              ? '已暂停'
              : waitingMessage,
      }
    })
  })

  // 分波发布时的整体汇总：已发布 X 个站点，剩余 Y 个站点已在队列中待发布。
  // 仅在设置了发种间隔且已有站点发布完成（第一批波次结束）后出现。
  const publishWaveSummary = computed<PublishWaveSummary | null>(() => {
    // 批次被限制/取消中断时，剩余站点并非“排队待发布”，不展示汇总。
    if (!pacingActive.value || limitAlert.value.visible) return null
    const total = selectedTargetSites.value.length
    const published = publishProgress.value.current
    if (total <= 0 || published <= 0 || published >= total) return null
    return {
      published,
      remaining: total - published,
      total,
      perWave: Math.max(1, batchPublishConcurrency.value),
      intervalMinutes: pacingIntervalMinutes.value,
    }
  })

  // 分组结果，每行5个
  const groupedResults = computed<PublishDisplayResult[][]>(() => {
    const results = publishDisplayResults.value
    const grouped: PublishDisplayResult[][] = []
    for (let i = 0; i < results.length; i += 5) {
      grouped.push(results.slice(i, i + 5))
    }
    return grouped
  })

  const isResultWithUrl = (
    result: PublishDisplayResult,
  ): result is PublishDisplayResult & { url: string } =>
    result.success === true && typeof result.url === 'string' && result.url.length > 0

  // 检查行中是否有有效的URL
  const hasValidUrlsInRow = (row: PublishDisplayResult[]) => row.some(isResultWithUrl)

  // 获取行中有效URL的数量
  const getValidUrlsCount = (row: PublishDisplayResult[]) => row.filter(isResultWithUrl).length

  // 打开一行中所有有效的种子链接
  const openAllSitesInRow = (row: PublishDisplayResult[]) => {
    const validResults = row.filter(isResultWithUrl)

    if (validResults.length === 0) {
      ElNotification.warning({
        title: '无法打开',
        message: '该行没有可用的种子链接',
      })
      return
    }

    // 批量打开所有链接，并过滤掉URL中的uploaded参数
    validResults.forEach((result) => {
      const filteredUrl = filterUploadedParam(result.url)
      window.open(filteredUrl, '_blank', 'noopener,noreferrer')
    })

    ElNotification.success({
      title: '批量打开成功',
      message: `已打开 ${validResults.length} 个种子页面`,
    })
  }

  // 处理日志进度完成
  const handleLogProgressComplete = () => {
    console.log('日志进度处理完成')
  }

  // 处理日志进度窗口关闭
  const handleLogProgressClose = () => {
    showLogProgress.value = false
    logProgressTaskId.value = ''
  }

  // ===== 发布成功但自动加入下载器失败：一键重新添加（不会再发种） =====
  // 正在重新添加的站点名（卡片按钮 loading）；批量重试时逐个加入/移除。
  const reAddingSites = ref<string[]>([])
  const isReAddingFailedSites = ref(false)

  // 只有「发布成功」且「自动加种明确失败」才允许重试：
  // dupe 拦截、发布前限制、发布本身失败都不属于「加种失败」，不能走这条补救。
  const isDownloaderAddFailedResult = (result?: PublishDisplayResult | null): boolean => {
    if (!result) return false
    if (result.success !== true) return false
    if (result.dupe_blocked) return false
    return result.auto_add_result?.success === false
  }

  // 当前可一键重试的失败站点（沿用卡片展示顺序）。
  const failedDownloaderSites = computed<string[]>(() =>
    publishDisplayResults.value.filter(isDownloaderAddFailedResult).map((item) => item.siteName),
  )

  const markReAdding = (siteName: string, adding: boolean) => {
    reAddingSites.value = adding
      ? Array.from(new Set([...reAddingSites.value, siteName]))
      : reAddingSites.value.filter((name) => name !== siteName)
  }

  // 单站重新添加：复用发布成功后的详情页地址走「添加到下载器」逻辑，只下载站点种子入下载器。
  // 参数/返回：siteName 为目标站点；options.silent 用于批量重试时抑制单个站点的提示；返回 true 表示已成功加入下载器。
  const reAddSiteToDownloader = async (
    siteName: string,
    options: { silent?: boolean } = {},
  ): Promise<boolean> => {
    const silent = options.silent === true
    const existing = publishResultsBySite.value[siteName]
    const detailURL = String(existing?.url || '').trim()
    if (!existing || !detailURL) {
      if (!silent) {
        ElNotification.warning({
          title: '无法重新添加',
          message: `站点「${siteName}」缺少发布详情地址，请点「查看日志」确认发布结果。`,
        })
      }
      return false
    }

    markReAdding(siteName, true)
    try {
      const currentTorrent = torrent.value
      const response = await axios.post('/api/publish_logs/re_add_downloader', {
        task_id: taskId.value || '',
        target_site: siteName,
        url: detailURL,
        publishURL: detailURL,
        downloader_id: currentTorrent?.downloaderId || '',
        save_path: currentTorrent?.save_path || '',
      })
      const data = (response.data || {}) as Record<string, unknown>
      const addResult = (data.auto_add_result || data) as AutoAddResult

      // 回写卡片状态：normalizePublishResult 会据 auto_add_result 重算 downloaderStatus。
      publishResultsBySite.value[siteName] = normalizePublishResult(siteName, {
        ...existing,
        auto_add_result: addResult,
      })
      rebuildFinalResultsList()
      rebuildProgress()

      if (addResult.success !== true && !silent) {
        ElNotification.warning({
          title: '重新添加失败',
          message: `${siteName}：${String(addResult.message || '下载器未接受该任务')}`,
        })
      }
      return addResult.success === true
    } catch (error: unknown) {
      const message = axios.isAxiosError(error)
        ? (error.response?.data as { message?: string } | undefined)?.message || error.message
        : error instanceof Error
          ? error.message
          : '重新添加到下载器失败'
      if (!silent) {
        ElNotification.error({ title: '重新添加失败', message: `${siteName}：${message}` })
      }
      return false
    } finally {
      markReAdding(siteName, false)
    }
  }

  // 一键：把全部「加种失败」的站点逐个重试（串行，避免同时打满下载器）。
  const reAddFailedSitesToDownloader = async () => {
    const targets = [...failedDownloaderSites.value]
    if (targets.length === 0) return

    isReAddingFailedSites.value = true
    let succeeded = 0
    const failedSites: string[] = []
    try {
      for (const siteName of targets) {
        if (await reAddSiteToDownloader(siteName, { silent: true })) {
          succeeded += 1
        } else {
          failedSites.push(siteName)
        }
      }
    } finally {
      isReAddingFailedSites.value = false
    }

    if (failedSites.length === 0) {
      ElNotification.success({
        title: '已全部加入下载器',
        message: `${succeeded} 个站点已成功添加到下载器。`,
      })
    } else {
      ElNotification.warning({
        title: succeeded > 0 ? '部分站点添加失败' : '重新添加失败',
        message: `成功 ${succeeded} 个；仍失败：${failedSites.join('、')}。可点「查看日志」确认原因。`,
        duration: 0,
      })
    }
  }

  // 过滤URL中的uploaded参数
  const filterUploadedParam = (url: string): string => {
    if (!url) return url

    try {
      const normalizeRousiViewUrl = (urlObj: URL) => {
        if (urlObj.hostname === 'rousi.pro' && urlObj.pathname.startsWith('/api/v1/torrents/')) {
          urlObj.pathname = urlObj.pathname.replace('/api/v1/torrents/', '/torrent/')
        }
      }

      // 处理包含 |DIRECT_DOWNLOAD: 的复合链接
      if (url.includes('|DIRECT_DOWNLOAD:')) {
        // 分割链接，只保留前半部分的查看链接
        const viewUrl = url.split('|DIRECT_DOWNLOAD:')[0]
        const urlObj = new URL(viewUrl)
        normalizeRousiViewUrl(urlObj)
        urlObj.searchParams.delete('uploaded')
        return urlObj.toString()
      }

      // 处理普通链接
      const urlObj = new URL(url)
      normalizeRousiViewUrl(urlObj)
      urlObj.searchParams.delete('uploaded')
      return urlObj.toString()
    } catch (error) {
      // 如果URL格式不正确，返回原始URL
      console.warn('Invalid URL format:', url, error)
      return url
    }
  }

  return {
    stopPublishBatchSSE,
    handleLogProgressComplete,
    handleLogProgressClose,
    handlePublish,
    handleEnqueue,
    handlePreviousStep,
    handleCancelClick,
    handleScrollOrNextStep,
    handleCompleteClick,
    getMappedValue,
    getMappedTags,
    filteredTitleComponents,
    initialTitleComponents,
    unrecognizedValue,
    filteredTags,
    invalidTagsList,
    isRestrictedTag,
    getTagType,
    handleTagClose,
    isNextButtonDisabled,
    nextButtonTooltipContent,
    publishWaveSummary,
    groupedResults,
    showSiteLog,
    filterUploadedParam,
    hasValidUrlsInRow,
    openAllSitesInRow,
    getValidUrlsCount,
    forceRepublishSite,
    forceRepublishingSite,
    reAddSiteToDownloader,
    reAddFailedSitesToDownloader,
    reAddingSites,
    isReAddingFailedSites,
    failedDownloaderSites,
  }
}
