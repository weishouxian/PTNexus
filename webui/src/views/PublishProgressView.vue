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
        <el-tooltip
          content="跳过等待时间：把「计划时间最早且还没到点」的那一波待发布任务立刻发出（范围＝顶部所选下载器）。等待预检查或可发种时间的任务不在此列，会照常等待。"
          placement="bottom"
          :hide-after="0"
        >
          <el-button
            size="small"
            type="primary"
            :icon="Promotion"
            :loading="promotingWave"
            @click="publishNextWave"
          >
            发布下一波
          </el-button>
        </el-tooltip>
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
        <el-option label="已存在" value="exists" />
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

        <el-table-column label="大小" width="100" align="center">
          <template #default="scope">
            <span :title="scope.row.size ? `${scope.row.size} 字节` : ''">
              {{ scope.row.size_formatted || '-' }}
            </span>
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
                content="立即发布的任务：已登记进度，由实时发布流程按波次执行（不在队列调度器里）；支持在此单站立即发布或取消"
                placement="top"
                :hide-after="0"
              >
                <el-tag
                  :type="taskStatusTagType(scope.row.status)"
                  size="small"
                  :class="{ 'status-tag-exists': scope.row.status === 'exists' }"
                >
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

        <el-table-column label="操作" width="245" align="center" fixed="right">
          <template #default="scope">
            <div class="action-buttons">
              <el-tooltip
                :disabled="!isRowActionable(scope.row)"
                content="跳过等待时间，立刻发布这条任务（范围仅此一条，不影响其它站点）"
                placement="top"
                :hide-after="0"
              >
                <span class="publish-now-button-wrap">
                  <el-button
                    size="small"
                    type="success"
                    :disabled="!isRowActionable(scope.row)"
                    :loading="publishingTaskId === Number(scope.row.id)"
                    @click="publishNow(scope.row)"
                  >
                    立即发布
                  </el-button>
                </span>
              </el-tooltip>
              <el-button
                size="small"
                type="primary"
                style="margin-left: 5px"
                :disabled="!scope.row.id"
                @click="openPublishLogs(scope.row)"
              >
                日志
              </el-button>
              <el-tooltip
                :disabled="scope.row.status !== 'dispatched'"
                content="取消该站点任务：发布 runner 轮到此站点时会自动跳过"
                placement="top"
                :hide-after="0"
              >
                <span class="cancel-button-wrap">
                  <el-button
                    size="small"
                    type="danger"
                    style="margin-left: 5px"
                    :disabled="!isRowActionable(scope.row)"
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

      <el-dialog
        v-model="logDialogVisible"
        :title="`发布日志 · ${currentLogTitle}`"
        width="720px"
        append-to-body
        destroy-on-close
      >
        <div v-loading="logLoading" class="publish-log-dialog">
          <template v-if="currentLog">
            <div class="publish-log-meta">
              <span class="meta-item">状态：<el-tag size="small" :type="publishLogStatusType(currentLog.status)">{{ publishLogStatusText(currentLog.status) }}</el-tag></span>
              <span v-if="currentLog.target_site" class="meta-item">目标站：{{ currentLog.target_site }}</span>
              <span v-if="currentLog.cost_ms" class="meta-item">耗时：{{ currentLog.cost_ms }} ms</span>
            </div>
            <div v-if="currentLog.result_url" class="publish-log-url">
              详情页：<el-link type="primary" :href="currentLog.result_url" target="_blank" rel="noopener">{{ currentLog.result_url }}</el-link>
            </div>
            <pre class="publish-log-content">{{ currentLog.logs || '（无日志内容）' }}</pre>
          </template>
          <el-empty v-else description="暂无发布日志（该任务尚未执行或日志未生成）" :image-size="80" />
        </div>
      </el-dialog>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { ElMessageBox } from 'element-plus'
import { Refresh, Promotion } from '@element-plus/icons-vue'
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
  /** 种子体积（字节）与人类可读文本，由后端按 context_json 的 Hash 关联 torrents.size 得到；0/空表示未取到。 */
  size?: number | null
  size_formatted?: string | null
}

type StatusCounts = {
  total: number
  queued: number
  dispatched: number
  running: number
  success: number
  failed: number
  cancelled: number
  exists: number
}

// 「待发布」在库里有两种来源：加入队列的 queued、立即发布登记的 dispatched（同样在等待执行）。
const STATUS_FILTER_VALUES: Record<string, string[]> = {
  waiting: ['queued', 'dispatched'],
  running: ['running'],
  success: ['success'],
  exists: ['exists'],
  failed: ['failed'],
  cancelled: ['cancelled'],
}

const globalDownloader = useGlobalDownloaderStore()

const loading = ref(false)
const error = ref('')
// promotingWave：正在「发布下一波」；publishingTaskId：正在单条「立即发布」的任务 ID（0 表示无）。
const promotingWave = ref(false)
const publishingTaskId = ref(0)

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
  exists: 0,
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
  { key: 'exists', label: '已存在' },
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
  if (key === 'exists') return counts.exists
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
  if (status === 'exists') return '已存在'
  if (status === 'failed') return '发布失败'
  if (status === 'cancelled') return '已取消'
  return status || '未知'
}

// isRowActionable 判断行是否可执行「立即发布/取消」（queued 队列任务与 dispatched 立即发布登记均支持）。
const isRowActionable = (row: QueueTaskRow) => {
  const status = (row.status || '').trim()
  return status === 'queued' || status === 'dispatched'
}

const taskStatusTagType = (status: string) => {
  if (status === 'success') return 'success'
  if (status === 'exists') return 'warning'
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

// 解析后端的时间格式，返回两行展示文本。
// 后端 AfterFind 钩子保证返回 "YYYY-MM-DD HH:MM:SS"；RFC3339 兜底防漏网路径。
const formatDateTimeTwoLines = (raw?: string | null) => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return '-'

  const parts = trimmed.split(' ')
  if (parts.length >= 2 && /^\d{4}-\d{2}-\d{2} /.test(trimmed)) {
    return `${parts[0]}\n${parts[1]}`
  }

  // RFC3339（2026-09-27T11:25:24+08:00 / Z）→ Date → 浏览器本地时区格式化
  const d = new Date(trimmed)
  if (!Number.isNaN(d.getTime())) {
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}\n${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }

  return trimmed
}

const shortTime = (raw?: string | null) => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return ''
  const parts = trimmed.split(' ')
  if (parts.length >= 2 && /^\d{4}-\d{2}-\d{2} /.test(trimmed)) {
    return parts[1] || ''
  }
  const d = new Date(trimmed)
  if (!Number.isNaN(d.getTime())) {
    const pad = (n: number) => String(n).padStart(2, '0')
    return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
  }
  return trimmed
}

// 解析队列时间文本（后端保证 "2006-01-02 15:04:05" 北京墙钟）为 Date 对象。
// 必须拼 +08:00 偏移后让 new Date 解析，否则 new Date(year, ...) 按浏览器时区解析，
// 浏览器不在东八区时相对时间会差 8 小时。
const parseQueueTime = (raw?: string | null): Date | null => {
  const trimmed = (raw || '').trim()
  if (!trimmed) return null

  const matched = trimmed.match(/^(\d{4})-(\d{2})-(\d{2})[ T](\d{2}):(\d{2}):(\d{2})/)
  if (matched) {
    // 拼 ISO 8601 带偏移格式，让 JS 引擎按北京时区解析为正确的时间戳
    const iso = `${matched[1]}-${matched[2]}-${matched[3]}T${matched[4]}:${matched[5]}:${matched[6]}+08:00`
    const parsed = new Date(iso)
    return Number.isNaN(parsed.getTime()) ? null : parsed
  }

  // 已带时区的 RFC3339 直接解析
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
  if (!plannedAt) return '等待队列执行'

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
      exists: Number(counts.exists || 0),
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

// resolveRequestError 统一提取接口错误信息，优先使用后端返回的 message/error。
const resolveRequestError = (e: unknown, fallback: string) => {
  if (axios.isAxiosError(e)) {
    const data = e.response?.data as { message?: string; error?: string } | undefined
    return data?.message || data?.error || e.message || fallback
  }
  return e instanceof Error ? e.message : fallback
}

// publishNextWave 跳过等待时间，把计划时间最早的一波待发布任务立刻发出（范围＝顶部全局下载器）。
const publishNextWave = async () => {
  if (promotingWave.value) return
  promotingWave.value = true
  try {
    const response = await axios.post('/api/migrate/publish_queue/next_wave', {
      downloader_ids: resolveDownloaderScope(),
    })
    const data = response.data || {}
    if (data.success === false) {
      throw new Error(data.message || '发布下一波失败')
    }

    const promoted = Number(data.promoted || 0)
    if (promoted > 0) {
      ElMessage.success(data.message || `已发布下一波（${promoted} 个目标站）`)
    } else {
      ElMessage.info(data.message || '当前没有等待中的下一波')
    }

    await fetchTasks()
    if (promoted > 0) {
      // 队列领取有极短延迟，稍后再拉一次以便看到「发布中」。
      window.setTimeout(() => void fetchTasks({ silent: true }), 1200)
    }
  } catch (e: unknown) {
    ElMessage.error(resolveRequestError(e, '发布下一波失败'))
  } finally {
    promotingWave.value = false
  }
}

// publishNow 单条任务跳过等待，立刻交给队列执行（不影响其它波次）。
const publishNow = async (row: QueueTaskRow) => {
  const id = Number(row.id)
  if (id <= 0 || !isRowActionable(row) || publishingTaskId.value === id) return

  publishingTaskId.value = id
  try {
    const response = await axios.post(`/api/migrate/publish_queue/tasks/${id}/publish_now`)
    const data = response.data || {}
    if (data.success === false) {
      throw new Error(data.message || '立即发布失败')
    }

    ElMessage.success(data.message || '已提交立即发布')
    await fetchTasks()
    window.setTimeout(() => void fetchTasks({ silent: true }), 1200)
  } catch (e: unknown) {
    ElMessage.error(resolveRequestError(e, '立即发布失败'))
  } finally {
    publishingTaskId.value = 0
  }
}

const cancelTask = async (row: QueueTaskRow) => {
  const id = Number(row.id)
  if (id <= 0 || !isRowActionable(row)) return
  const isDispatched = (row.status || '').trim() === 'dispatched'

  try {
    await ElMessageBox.confirm(
      isDispatched
        ? `确认取消该站点任务？该站点会被跳过，批次内其它站点不受影响。\n${row.target_site || ''} · ${row.title || ''}`
        : `确认取消该队列任务？\n${row.target_site || ''} · ${row.title || ''}`,
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
    ElMessage.error(resolveRequestError(e, '取消失败'))
  }
}

const logDialogVisible = ref(false)
const logLoading = ref(false)
const currentLog = ref<Record<string, any> | null>(null)
const currentLogTitle = ref('')

// 查看该种子（队列任务）的实际发布日志：直接内联弹窗展示，不再跳转发种日志菜单。
const openPublishLogs = async (row: QueueTaskRow) => {
  currentLogTitle.value = `${row.target_site || ''} · ${row.title || ''}`.trim() || `任务 #${row.id}`
  currentLog.value = null
  logDialogVisible.value = true
  logLoading.value = true
  try {
    const params = new URLSearchParams()
    if (row.group_id) params.set('queue_group_id', String(row.group_id))
    if (row.target_site) params.set('target_site', String(row.target_site))
    const qs = params.toString()
    const response = await axios.get(`/api/publish_logs/by_queue_task/${Number(row.id)}${qs ? '?' + qs : ''}`)
    const data = response.data || {}
    if (data.success === false) {
      throw new Error(data.message || '获取发布日志失败')
    }
    currentLog.value = data.data || null
  } catch (e: unknown) {
    ElMessage.error(resolveRequestError(e, '获取发布日志失败'))
    currentLog.value = null
  } finally {
    logLoading.value = false
  }
}

// publishLogStatusText 把发种日志状态映射为中文展示文案。
const publishLogStatusText = (status?: string): string => {
  switch (String(status || '').trim()) {
    case 'success':
      return '发布成功'
    case 'edited':
      return '发布后编辑'
    case 'exists':
      return '种子已存在'
    case 'failed':
      return '发布失败'
    case 'pre_check_limit':
      return '预检查限制'
    case 'invalidated':
      return '已作废'
    case 'queued':
      return '等待发布'
    case 'running':
      return '发布中'
    default:
      return String(status || '未知')
  }
}

// publishLogStatusType 把发种日志状态映射为 el-tag 的颜色类型。
const publishLogStatusType = (status?: string): '' | 'success' | 'warning' | 'info' | 'danger' => {
  switch (String(status || '').trim()) {
    case 'success':
    case 'edited':
    case 'exists':
      return 'success'
    case 'failed':
    case 'invalidated':
      return 'danger'
    case 'pre_check_limit':
      return 'warning'
    case 'queued':
    case 'running':
      return 'info'
    default:
      return 'info'
  }
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

.publish-now-button-wrap {
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

.publish-log-dialog {
  min-height: 120px;
}

.publish-log-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 12px;
  margin-bottom: 10px;
  font-size: 13px;
  color: #606266;
}

.publish-log-url {
  margin-bottom: 10px;
  font-size: 13px;
  color: #606266;
  word-break: break-all;
}

.publish-log-content {
  margin: 0;
  padding: 12px;
  max-height: 420px;
  overflow: auto;
  background-color: #f7f8fa;
  border: 1px solid #ebeef5;
  border-radius: 6px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-all;
  color: #303133;
}

/* 下载器发布进度列表：「已存在」状态用黄色字体展示（金黄花色，浅黄底+黄边）。 */
.status-tag-exists {
  --el-tag-text-color: #d48806;
  --el-tag-bg-color: #fffbe6;
  --el-tag-border-color: #ffe58f;
}
</style>
