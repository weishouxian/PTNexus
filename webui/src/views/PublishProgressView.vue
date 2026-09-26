<template>
  <div class="publish-progress-view">
    <el-alert
      v-if="error"
      :title="error"
      type="error"
      show-icon
      :closable="false"
      style="margin: 0; border-radius: 0"
    />

    <div class="status-overview glass-table">
      <div class="overview-items">
        <button
          v-for="item in overviewItems"
          :key="item.key"
          type="button"
          class="overview-item"
          :class="[`overview-item--${item.key || 'all'}`, { 'is-active': isStatusActive(item.key) }]"
          @click="toggleStatusFilter(item.key)"
        >
          <span class="overview-label">{{ item.label }}</span>
          <span class="overview-value">{{ resolveStatusCount(item.key) }}</span>
        </button>
      </div>

      <div class="overview-actions">
        <span class="refresh-hint">
          {{ autoRefresh ? `${autoRefreshInterval} 秒自动刷新` : '已暂停自动刷新' }}
        </span>
        <el-switch v-model="autoRefresh" size="small" />
        <el-select v-model="autoRefreshInterval" size="small" style="width: 92px">
          <el-option :value="3" label="3 秒" />
          <el-option :value="5" label="5 秒" />
          <el-option :value="10" label="10 秒" />
          <el-option :value="30" label="30 秒" />
        </el-select>
        <el-button size="small" :icon="Refresh" :loading="loading" @click="fetchTasks()">
          刷新
        </el-button>
      </div>
    </div>

    <div class="search-and-controls glass-table">
      <el-input
        v-model="searchQuery"
        placeholder="搜索标题/副标题/种子ID/站点..."
        clearable
        style="width: 260px; margin-right: 12px"
        @keyup.enter="applyFilters"
      />

      <el-select
        v-model="statusFilter"
        multiple
        collapse-tags
        collapse-tags-tooltip
        placeholder="发布状态"
        clearable
        style="width: 210px; margin-right: 12px"
        @change="applyFilters"
      >
        <el-option label="待发布" value="waiting" />
        <el-option label="发布中" value="running" />
        <el-option label="已发布" value="success" />
        <el-option label="发布失败" value="failed" />
        <el-option label="已取消" value="cancelled" />
      </el-select>

      <el-select
        v-model="sceneFilter"
        placeholder="场景"
        clearable
        style="width: 130px; margin-right: 12px"
        @change="applyFilters"
      >
        <el-option label="一种多站" value="multi_site" />
        <el-option label="一站多种" value="multi_torrent" />
      </el-select>

      <el-input
        v-model="targetSiteFilter"
        placeholder="目标站点"
        clearable
        style="width: 130px; margin-right: 12px"
        @keyup.enter="applyFilters"
      />

      <el-input
        v-model="queueGroupFilter"
        placeholder="队列分组ID"
        clearable
        style="width: 170px; margin-right: 12px"
        @keyup.enter="applyFilters"
      />

      <el-button type="primary" plain @click="applyFilters">查询</el-button>
      <el-button type="danger" plain style="margin-left: 8px" @click="clearFilters">清空</el-button>

      <div class="pagination-controls" v-if="total > 0">
        <el-pagination
          v-model:current-page="currentPage"
          v-model:page-size="pageSize"
          :page-sizes="[10, 20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          background
          @size-change="handleSizeChange"
          @current-change="handleCurrentChange"
        />
      </div>
    </div>

    <div class="table-container">
      <el-table
        :data="rows"
        row-key="id"
        v-loading="loading"
        border
        style="width: 100%"
        height="100%"
        empty-text="当前筛选下暂无发布队列任务"
        class="glass-table"
      >
        <el-table-column label="种子" min-width="320">
          <template #default="scope">
            <div class="title-cell">
              <div class="subtitle-line" :title="scope.row.subtitle || ''">
                {{ scope.row.subtitle || '' }}
              </div>
              <div class="main-title-line" :title="scope.row.title || ''">
                {{ scope.row.title || '(未获取到标题)' }}
              </div>
              <div class="meta-line">
                <span v-if="scope.row.source_site">{{ scope.row.source_site }} → {{ scope.row.target_site }}</span>
                <span v-else>{{ scope.row.target_site || '-' }}</span>
                <span v-if="scope.row.torrent_id">· {{ scope.row.torrent_id }}</span>
              </div>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="下载器" width="120" align="center">
          <template #default="scope">
            <div class="status-tags">
              <el-tag
                size="small"
                :style="downloaderTagStyle(scope.row.downloader_id)"
              >
                {{ formatDownloader(scope.row.downloader_id) }}
              </el-tag>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="加入队列" width="145" align="center">
          <template #default="scope">
            <div class="datetime-cell">{{ formatDateTimeTwoLines(scope.row.created_at) }}</div>
          </template>
        </el-table-column>

        <el-table-column label="预计发布时间" width="165" align="center">
          <template #header>
            <el-tooltip
              content="任务真正可执行的时间：取计划时间与下次可运行时间中较晚者；到点后由后台队列调度器领取执行。填了下载器「发布节奏」的任务会按波次错开这个时间。"
              placement="top"
              :hide-after="0"
            >
              <span class="header-with-tip">预计发布时间</span>
            </el-tooltip>
          </template>
          <template #default="scope">
            <div class="time-cell">
              <div class="datetime-cell">
                {{ formatDateTimeTwoLines(scope.row.effective_scheduled_at) }}
              </div>
              <div class="cell-hint">{{ plannedTimeHint(scope.row) }}</div>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="发布状态" width="110" align="center">
          <template #default="scope">
            <div class="status-tags">
              <el-tooltip
                :disabled="scope.row.status !== 'dispatched'"
                content="立即发布的任务：已登记进度，由实时发布流程按波次执行（不在队列调度器里，取消请回发布面板）"
                placement="top"
                :hide-after="0"
              >
                <el-tag :type="taskStatusTagType(scope.row.status)" size="small">
                  {{ formatTaskStatus(scope.row.status) }}
                </el-tag>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="实际发布时间" width="165" align="center">
          <template #header>
            <el-tooltip
              content="后台队列真正开始发布该任务的时间；第二行是发布结束时间。重试时会重新计时。"
              placement="top"
              :hide-after="0"
            >
              <span class="header-with-tip">实际发布时间</span>
            </el-tooltip>
          </template>
          <template #default="scope">
            <div class="time-cell">
              <div
                class="datetime-cell"
                :class="{ 'is-muted': !scope.row.started_at }"
              >
                {{ formatDateTimeTwoLines(scope.row.started_at) }}
              </div>
              <div class="cell-hint">{{ actualTimeHint(scope.row) }}</div>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="重试" width="80" align="center">
          <template #default="scope">
            <span class="retry-text">{{ Number(scope.row.attempt_count || 0) }}</span>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="150" align="center" fixed="right">
          <template #default="scope">
            <div class="action-buttons">
              <el-button
                size="small"
                type="primary"
                :disabled="!scope.row.group_id"
                @click="openPublishLogs(scope.row)"
              >
                日志
              </el-button>
              <el-tooltip
                :disabled="scope.row.status !== 'dispatched'"
                content="立即发布的任务无法在此取消，请回发布面板取消该批任务"
                placement="top"
                :hide-after="0"
              >
                <span class="cancel-button-wrap">
                  <el-button
                    size="small"
                    type="danger"
                    style="margin-left: 5px"
                    :disabled="scope.row.status !== 'queued'"
                    @click="cancelTask(scope.row)"
                  >
                    取消
                  </el-button>
                </span>
              </el-tooltip>
            </div>
          </template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import axios from 'axios'
import { useGlobalDownloaderStore } from '@/stores/globalDownloader'
import type { Downloader } from '@/types'
import { ElMessage } from '@/utils/uiNotify'

type QueueTaskRow = {
  id: number
  group_id?: string | null
  status: string
  task_id?: string | null
  trigger?: string | null
  scene?: string | null
  torrent_id?: string | null
  source_site?: string | null
  target_site?: string | null
  downloader_id?: string | null
  title?: string | null
  subtitle?: string | null
  attempt_count?: number
  scheduled_at?: string | null
  next_run_at?: string | null
  started_at?: string | null
  finished_at?: string | null
  effective_scheduled_at?: string | null
  last_error?: string | null
  created_at?: string | null
  updated_at?: string | null
}

type StatusCounts = {
  total: number
  queued: number
  dispatched: number
  running: number
  success: number
  failed: number
  cancelled: number
}

// 「待发布」在库里有两种来源：加入队列的 queued、立即发布登记的 dispatched（同样在等待执行）。
const STATUS_FILTER_VALUES: Record<string, string[]> = {
  waiting: ['queued', 'dispatched'],
  running: ['running'],
  success: ['success'],
  failed: ['failed'],
  cancelled: ['cancelled'],
}

const router = useRouter()
const globalDownloader = useGlobalDownloaderStore()

const loading = ref(false)
const error = ref('')

const rows = ref<QueueTaskRow[]>([])
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

const searchQuery = ref('')
const statusFilter = ref<string[]>([])
const sceneFilter = ref('')
const targetSiteFilter = ref('')
const queueGroupFilter = ref('')

const statusCounts = ref<StatusCounts>({
  total: 0,
  queued: 0,
  dispatched: 0,
  running: 0,
  success: 0,
  failed: 0,
  cancelled: 0,
})

const autoRefresh = ref(true)
const autoRefreshInterval = ref(5)

// nowTick 每秒推进一次，用于「预计发布时间」的相对时间提示（仅在存在待发布任务时更新）。
const nowTick = ref(Date.now())

let fetchSeq = 0
let clockTimer: number | null = null
let refreshTimer: number | null = null

const overviewItems = [
  { key: '', label: '全部' },
  { key: 'waiting', label: '待发布' },
  { key: 'running', label: '发布中' },
  { key: 'success', label: '已发布' },
  { key: 'failed', label: '发布失败' },
  { key: 'cancelled', label: '已取消' },
]

const allDownloaders = computed<Downloader[]>(() => globalDownloader.downloaders || [])

// 下载器范围只取顶部菜单的「全局下载器」选择：'' 表示全部下载器。
const resolveDownloaderScope = (): string[] => {
  const id = (globalDownloader.selectedDownloaderId || '').trim()
  return id ? [id] : []
}

const resolveStatusCount = (key: string) => {
  const counts = statusCounts.value
  if (key === 'waiting') return counts.queued + counts.dispatched
  if (key === 'running') return counts.running
  if (key === 'success') return counts.success
  if (key === 'failed') return counts.failed
  if (key === 'cancelled') return counts.cancelled
  return counts.total
}

const isStatusActive = (key: string) =>
  key === '' ? statusFilter.value.length === 0 : statusFilter.value.includes(key)

const toggleStatusFilter = (key: string) => {
  if (key === '') {
    if (statusFilter.value.length === 0) return
    statusFilter.value = []
  } else if (statusFilter.value.includes(key)) {
    statusFilter.value = statusFilter.value.filter((item) => item !== key)
  } else {
    statusFilter.value = [...statusFilter.value, key]
  }
  void applyFilters()
}

const formatTaskStatus = (status: string) => {
  if (status === 'queued') return '待发布'
  if (status === 'dispatched') return '待发布'
  if (status === 'running') return '发布中'
  if (status === 'success') return '已发布'
  if (status === 'failed') return '发布失败'
  if (status === 'cancelled') return '已取消'
  return status || '未知'
}

const taskStatusTagType = (status: string) => {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'warning'
  return 'info'
}

const formatDownloader = (downloaderId?: string | null) => {
  const key = (downloaderId || '').trim()
  if (!key) return '默认'
  const matched = allDownloaders.value.find((item) => item.id === key)
  return matched?.name || key
}

const downloaderTagStyle = (downloaderId?: string | null) => {
  const key = (downloaderId || '').trim()
  if (!key) return {}
  const matched = allDownloaders.value.find((item) => item.id === key)
  const color = String(matched?.color || '').trim()
  if (!color) return {}
  return {
    '--el-tag-bg-color': color,
    '--el-tag-border-color': color,
    '--el-tag-text-color': '#ffffff',
  }
}

// 解析后端的时间格式（YYYY-MM-DD HH:MM:SS，本地时区），返回两行展示文本。
const formatDateTimeTwoLines = (raw?: string | null) => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return '-'

  const parts = trimmed.split(' ')
  if (parts.length >= 2) {
    return `${parts[0]}\n${parts[1]}`
  }
  return trimmed
}

const shortTime = (raw?: string | null) => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return ''
  const parts = trimmed.split(' ')
  return parts.length >= 2 ? parts[1] || '' : trimmed
}

const parseQueueTime = (raw?: string | null): Date | null => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return null

  const matched = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/)
  if (matched) {
    return new Date(
      Number(matched[1]),
      Number(matched[2]) - 1,
      Number(matched[3]),
      Number(matched[4]),
      Number(matched[5]),
      Number(matched[6]),
    )
  }

  const parsed = new Date(trimmed)
  return Number.isNaN(parsed.getTime()) ? null : parsed
}

const formatRelative = (target: Date, now: Date) => {
  const diffMs = target.getTime() - now.getTime()
  const absMs = Math.abs(diffMs)
  const minute = 60 * 1000
  const hour = 60 * minute
  const day = 24 * hour

  if (absMs < 45 * 1000) return diffMs >= 0 ? '即将' : '刚过'

  let text = ''
  if (absMs < hour) {
    text = `${Math.max(1, Math.round(absMs / minute))} 分钟`
  } else if (absMs < day) {
    text = `${Math.floor(absMs / hour)} 小时`
  } else {
    text = `${Math.floor(absMs / day)} 天`
  }
  return diffMs >= 0 ? `${text}后` : `${text}前`
}

const plannedTimeHint = (row: QueueTaskRow) => {
  const status = (row.status || '').trim()
  if (status === 'success' || status === 'failed' || status === 'cancelled') {
    return row.finished_at ? `完成于 ${shortTime(row.finished_at)}` : ''
  }
  if (status === 'running') {
    return '已开始发布'
  }

  const plannedAt = parseQueueTime(row.effective_scheduled_at)
  if (!plannedAt) return '立即发布'

  const now = new Date(nowTick.value)
  if (plannedAt.getTime() - now.getTime() <= 1000) {
    return row.attempt_count ? '等待重试' : '排队中'
  }
  return `约 ${formatRelative(plannedAt, now)}`
}

const actualTimeHint = (row: QueueTaskRow) => {
  const status = (row.status || '').trim()
  if (row.finished_at) {
    return `完成 ${shortTime(row.finished_at)}`
  }
  if (status === 'running') return '进行中…'
  if (status === 'failed' && row.last_error) return '发布失败'
  return ''
}

const fetchTasks = async (options: { silent?: boolean } = {}) => {
  const silent = options.silent === true
  const currentFetchSeq = ++fetchSeq
  if (!silent) {
    loading.value = true
    error.value = ''
  }

  try {
    const response = await axios.get('/api/migrate/publish_queue/tasks', {
      params: {
        page: currentPage.value,
        page_size: pageSize.value,
        search: searchQuery.value.trim(),
        status: statusFilter.value
          .flatMap((key) => STATUS_FILTER_VALUES[key] || [])
          .join(','),
        scene: sceneFilter.value,
        target_site: targetSiteFilter.value.trim(),
        queue_group_id: queueGroupFilter.value.trim(),
        downloader_ids: resolveDownloaderScope().join(','),
      },
    })

    if (!response.data?.success) {
      throw new Error(response.data?.message || '获取发布队列失败')
    }
    if (currentFetchSeq !== fetchSeq) return

    const list = Array.isArray(response.data.data) ? (response.data.data as QueueTaskRow[]) : []
    const totalCount = Number(response.data.total || 0)

    // 自动刷新/翻页后当前页可能已越界，回退到最后一个有效页。
    if (list.length === 0 && totalCount > 0) {
      const maxPage = Math.max(1, Math.ceil(totalCount / pageSize.value))
      if (currentPage.value > maxPage) {
        currentPage.value = maxPage
        await fetchTasks({ silent })
        return
      }
    }

    rows.value = list
    total.value = totalCount

    const counts = response.data.status_counts || {}
    statusCounts.value = {
      total: Number(counts.total || 0),
      queued: Number(counts.queued || 0),
      dispatched: Number(counts.dispatched || 0),
      running: Number(counts.running || 0),
      success: Number(counts.success || 0),
      failed: Number(counts.failed || 0),
      cancelled: Number(counts.cancelled || 0),
    }

    nowTick.value = Date.now()
    if (error.value) {
      error.value = ''
    }
  } catch (e: unknown) {
    if (currentFetchSeq !== fetchSeq) return
    if (!silent) {
      const message = axios.isAxiosError(e)
        ? ((e.response?.data as { message?: string; error?: string } | undefined)?.message ||
          (e.response?.data as { error?: string } | undefined)?.error ||
          e.message)
        : e instanceof Error
          ? e.message
          : '获取发布队列失败'
      error.value = message
    }
  } finally {
    if (!silent && currentFetchSeq === fetchSeq) {
      loading.value = false
    }
  }
}

const applyFilters = async () => {
  currentPage.value = 1
  await fetchTasks()
}

const clearFilters = async () => {
  searchQuery.value = ''
  statusFilter.value = []
  sceneFilter.value = ''
  targetSiteFilter.value = ''
  queueGroupFilter.value = ''
  await applyFilters()
}

const handleSizeChange = async (size: number) => {
  pageSize.value = size
  currentPage.value = 1
  await fetchTasks()
}

const handleCurrentChange = async (page: number) => {
  currentPage.value = page
  await fetchTasks()
}

const cancelTask = async (row: QueueTaskRow) => {
  const id = Number(row.id)
  if (id <= 0 || (row.status || '').trim() !== 'queued') return

  try {
    await ElMessageBox.confirm(
      `确认取消该队列任务？\n${row.target_site || ''} · ${row.title || ''}`,
      '取消发布任务',
      { confirmButtonText: '确定', cancelButtonText: '取消', type: 'warning' },
    )

    const response = await axios.delete(`/api/migrate/publish_queue/tasks/${id}`)
    if (response.data?.success === false) {
      throw new Error(response.data?.message || '取消失败')
    }

    ElMessage.success(response.data?.message || '已取消该队列任务')
    await fetchTasks()
  } catch (e: unknown) {
    if (e === 'cancel' || e === 'close') return
    const message = axios.isAxiosError(e)
      ? ((e.response?.data as { message?: string; error?: string } | undefined)?.message ||
        (e.response?.data as { error?: string } | undefined)?.error ||
        e.message)
      : e instanceof Error
        ? e.message
        : '取消失败'
    ElMessage.error(message)
  }
}

const openPublishLogs = (row: QueueTaskRow) => {
  const query: Record<string, string> = {}
  if (row.group_id) query.queue_group_id = String(row.group_id)
  if (row.target_site) query.target_site = String(row.target_site)
  router.push({ path: '/publish-logs', query })
}

const startClock = () => {
  if (clockTimer !== null) return
  clockTimer = window.setInterval(() => {
    // 仅在存在待发布任务时才推进时间，避免无谓重渲染。
    if (rows.value.some((row) => (row.status || '').trim() === 'queued')) {
      nowTick.value = Date.now()
    }
  }, 1000)
}

const setupRefreshTimer = () => {
  if (refreshTimer !== null) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
  if (!autoRefresh.value) return
  const intervalMs = Math.max(1, Number(autoRefreshInterval.value) || 5) * 1000
  refreshTimer = window.setInterval(() => {
    void fetchTasks({ silent: true })
  }, intervalMs)
}

onMounted(async () => {
  try {
    await globalDownloader.fetchDownloaders()
  } catch {
    // 下载器列表加载失败不影响任务列表展示（下载器列回退显示 ID）。
  }
  await globalDownloader.loadSelection()

  await fetchTasks()
  startClock()
  setupRefreshTimer()
})

onUnmounted(() => {
  if (clockTimer !== null) {
    window.clearInterval(clockTimer)
    clockTimer = null
  }
  if (refreshTimer !== null) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
})

watch(
  () => globalDownloader.selectedDownloaderId,
  async (next, previous) => {
    if ((next || '').trim() === (previous || '').trim()) return
    currentPage.value = 1
    await fetchTasks()
  },
)

watch([autoRefresh, autoRefreshInterval], () => {
  setupRefreshTimer()
})
</script>

<style scoped>
.publish-progress-view {
  height: 100%;
  display: flex;
  flex-direction: column;
  padding: 0;
  box-sizing: border-box;
}

.status-overview {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 15px;
  background-color: #ffffff;
  border-bottom: 1px solid #ebeef5;
  flex-wrap: wrap;
}

.overview-items {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.overview-item {
  display: flex;
  align-items: baseline;
  gap: 6px;
  padding: 4px 10px;
  border: 1px solid #e4e7ed;
  border-radius: 6px;
  background-color: #fafafa;
  color: #606266;
  cursor: pointer;
  font-family: inherit;
  font-size: 13px;
  line-height: 1.4;
  transition: all 0.15s ease;
}

.overview-item:hover {
  border-color: #c0c4cc;
  background-color: #f5f7fa;
}

.overview-item.is-active {
  border-color: #409eff;
  background-color: #ecf5ff;
  color: #409eff;
}

.overview-label {
  font-size: 12px;
}

.overview-value {
  font-size: 16px;
  font-weight: 600;
}

.overview-item--waiting .overview-value {
  color: #909399;
}

.overview-item--running .overview-value {
  color: #e6a23c;
}

.overview-item--success .overview-value {
  color: #67c23a;
}

.overview-item--failed .overview-value {
  color: #f56c6c;
}

.overview-item--cancelled .overview-value {
  color: #909399;
}

.overview-item--waiting.is-active .overview-value,
.overview-item--running.is-active .overview-value,
.overview-item--success.is-active .overview-value,
.overview-item--failed.is-active .overview-value,
.overview-item--cancelled.is-active .overview-value,
.overview-item--all.is-active .overview-value {
  color: inherit;
}

.overview-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.refresh-hint {
  font-size: 12px;
  color: #909399;
  white-space: nowrap;
}

.search-and-controls {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 0;
  padding: 10px 15px;
  background-color: #ffffff;
  border-bottom: 1px solid #ebeef5;
}

.pagination-controls {
  flex: 1;
  display: flex;
  justify-content: flex-end;
  min-width: 320px;
}

.table-container {
  flex: 1;
  overflow: hidden;
  min-height: 300px;
}

.table-container :deep(.el-table) {
  height: 100%;
}

.table-container :deep(.el-table__body-wrapper) {
  overflow-y: auto;
}

.table-container :deep(.el-table__header-wrapper) {
  overflow-x: hidden;
}

.datetime-cell {
  white-space: pre-line;
  line-height: 1.25;
  font-size: 12px;
}

.datetime-cell.is-muted {
  color: #c0c4cc;
}

.time-cell {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 2px;
}

.cell-hint {
  font-size: 11px;
  color: #909399;
  line-height: 1.2;
}

.status-tags {
  display: flex;
  justify-content: center;
  align-items: center;
  width: 100%;
  height: 100%;
}

.action-buttons {
  display: flex;
  justify-content: center;
  align-items: center;
  width: 100%;
  height: 100%;
}

.retry-text {
  font-size: 12px;
  color: #606266;
}

.cancel-button-wrap {
  display: inline-block;
}

.header-with-tip {
  cursor: help;
  border-bottom: 1px dashed #c0c4cc;
}

.title-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.subtitle-line {
  font-size: 12px;
  color: #909399;
  line-height: 1.2;
}

.main-title-line {
  font-size: 13px;
  line-height: 1.25;
  white-space: normal;
}

.meta-line {
  display: flex;
  gap: 6px;
  font-size: 11px;
  color: #a8abb2;
  line-height: 1.2;
}

@media (max-width: 900px) {
  .status-overview {
    align-items: flex-start;
    flex-direction: column;
  }

  .pagination-controls {
    justify-content: flex-start;
  }
}
</style>
