<template>
  <div class="bangumi-view">
    <div class="sync-panel glass-table">
      <div class="sync-info">
        <div class="sync-title">
          <span>Bangumi 番组数据</span>
          <el-tag :type="statusTagType" size="small" effect="light">{{ statusTagText }}</el-tag>
        </div>
        <div class="sync-meta">
          <span>最近同步：{{ lastSyncText }}</span>
          <span>条目数：{{ total }}</span>
          <span>数据源版本：{{ meta.version || '-' }}</span>
          <span>下次自动同步：{{ meta.next_sync_at || '-' }}</span>
          <span v-if="meta.duration_ms">耗时：{{ meta.duration_ms }} ms</span>
        </div>
        <div v-if="meta.message" class="sync-message" :class="{ 'is-error': meta.status === 'failed' }">
          {{ meta.message }}
        </div>
      </div>
      <div class="sync-actions">
        <el-button type="primary" :loading="syncing" :disabled="syncing" @click="triggerSync">
          {{ syncing ? '同步中…' : '立即同步' }}
        </el-button>
      </div>
    </div>

    <div class="search-and-controls glass-table">
      <el-input
        v-model="searchQuery"
        placeholder="搜索标题/中文名/Bangumi ID/TMDb ID/MAL ID..."
        clearable
        class="search-input"
        style="width: 340px; margin-right: 15px"
        @keyup.enter="applyFilters"
      />
      <el-select
        v-model="typeFilter"
        placeholder="类型"
        clearable
        style="width: 130px; margin-right: 12px"
        @change="applyFilters"
      >
        <el-option label="全部类型" value="" />
        <el-option label="TV" value="tv" />
        <el-option label="剧场版" value="movie" />
        <el-option label="OVA" value="ova" />
        <el-option label="网络动画" value="web" />
      </el-select>
      <el-select
        v-model="langFilter"
        placeholder="语言"
        clearable
        style="width: 130px; margin-right: 12px"
        @change="applyFilters"
      >
        <el-option label="全部语言" value="" />
        <el-option label="日语" value="ja" />
        <el-option label="简体中文" value="zh-Hans" />
        <el-option label="英语" value="en" />
      </el-select>
      <el-button type="primary" plain @click="applyFilters">查询</el-button>
      <el-button style="margin-left: 8px" @click="clearFilters">清空</el-button>
      <el-button style="margin-left: 8px" @click="refreshAll">刷新</el-button>
    </div>

    <div class="table-container glass-table">
      <el-table :data="items" v-loading="loading" style="width: 100%" size="small">
        <el-table-column label="中文名" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="cell-with-copy">
              <span class="cell-text" :class="{ 'empty-text': !row.title_zh }">
                {{ row.title_zh || '暂无中文名' }}
              </span>
              <el-button
                v-if="row.title_zh"
                text
                size="small"
                :icon="CopyDocument"
                title="复制中文名"
                @click="copyText(row.title_zh, '中文名')"
              />
            </div>
          </template>
        </el-table-column>
        <el-table-column label="原名" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            <div class="cell-with-copy">
              <span class="cell-text">{{ row.title }}</span>
              <el-button
                text
                size="small"
                :icon="CopyDocument"
                title="复制原名"
                @click="copyText(row.title, '原名')"
              />
            </div>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="96">
          <template #default="{ row }">
            <el-tag size="small" :type="typeTagType(row.type)">{{ typeLabel(row.type) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="语言" width="96">
          <template #default="{ row }">
            <span :class="{ 'empty-text': !row.lang }">{{ langLabel(row.lang) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="播出日期" width="200">
          <template #default="{ row }">
            <span :class="{ 'empty-text': !row.begin }">{{ formatDateRange(row.begin, row.end) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="站点链接" min-width="280">
          <template #default="{ row }">
            <div class="site-links">
              <el-button
                v-for="link in primarySites(row.sites)"
                :key="link.site + link.id"
                link
                type="primary"
                size="small"
                @click="openLink(link.url)"
              >
                {{ linkTitle(link) }}
                <span v-if="link.id" class="site-link-id">{{ link.id }}</span>
              </el-button>
              <el-popover
                v-if="row.sites.length > maxPrimarySites"
                placement="top"
                trigger="hover"
                width="320"
              >
                <template #reference>
                  <el-button link size="small" type="info">
                    更多 ({{ row.sites.length }})
                  </el-button>
                </template>
                <div class="popover-links">
                  <el-button
                    v-for="link in row.sites"
                    :key="'all-' + link.site + link.id"
                    link
                    type="primary"
                    size="small"
                    @click="openLink(link.url)"
                  >
                    {{ linkTitle(link) }}
                    <span v-if="link.id" class="site-link-id">{{ link.id }}</span>
                  </el-button>
                </div>
              </el-popover>
              <span v-if="!row.sites.length" class="empty-text">无</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90" fixed="right">
          <template #default="{ row }">
            <el-button type="primary" link :icon="View" @click="openDetail(row)">详情</el-button>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-container">
        <el-pagination
          v-model:current-page="page"
          v-model:page-size="pageSize"
          :total="total"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next, jumper"
          @current-change="fetchList"
          @size-change="handleSizeChange"
        />
      </div>
    </div>

    <el-dialog
      v-model="detailVisible"
      title="番组条目详情"
      width="70%"
      append-to-body
      class="bangumi-detail-dialog"
    >
      <div v-if="selectedItem" class="detail-body">
        <div class="detail-toolbar">
          <el-button type="primary" size="small" :icon="CopyDocument" @click="copyDetail(selectedItem)">
            复制条目信息
          </el-button>
          <el-button
            v-if="selectedItem.official_site"
            size="small"
            :icon="Link"
            @click="openLink(selectedItem.official_site)"
          >
            打开官网
          </el-button>
        </div>

        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="中文名">{{ selectedItem.title_zh || '暂无' }}</el-descriptions-item>
          <el-descriptions-item label="原名">{{ selectedItem.title }}</el-descriptions-item>
          <el-descriptions-item label="类型">{{ typeLabel(selectedItem.type) }}</el-descriptions-item>
          <el-descriptions-item label="语言">{{ langLabel(selectedItem.lang) }}</el-descriptions-item>
          <el-descriptions-item label="播出日期">
            {{ formatDateRange(selectedItem.begin, selectedItem.end) || '未知' }}
          </el-descriptions-item>
          <el-descriptions-item label="Bangumi ID">
            {{ selectedItem.bangumi_id || '无' }}
          </el-descriptions-item>
          <el-descriptions-item label="TMDb ID">{{ selectedItem.tmdb_id || '无' }}</el-descriptions-item>
          <el-descriptions-item label="MAL ID">{{ selectedItem.mal_id || '无' }}</el-descriptions-item>
          <el-descriptions-item label="AniDB ID">{{ selectedItem.anidb_id || '无' }}</el-descriptions-item>
          <el-descriptions-item label="AniList ID">{{ selectedItem.anilist_id || '无' }}</el-descriptions-item>
          <el-descriptions-item label="播出规则" :span="2">
            {{ selectedItem.broadcast || '无' }}
          </el-descriptions-item>
          <el-descriptions-item label="备注" :span="2">
            {{ selectedItem.comment || '无' }}
          </el-descriptions-item>
        </el-descriptions>

        <div class="detail-section" v-if="allTitles(selectedItem).length">
          <div class="detail-section-title">其他译名</div>
          <div class="title-list">
            <div v-for="entry in allTitles(selectedItem)" :key="entry.lang" class="title-row">
              <span class="title-lang">{{ langLabel(entry.lang) }}</span>
              <span>{{ entry.values.join(' / ') }}</span>
            </div>
          </div>
        </div>

        <div class="detail-section" v-if="selectedItem.sites.length">
          <div class="detail-section-title">站点链接</div>
          <div class="link-list">
            <div v-for="link in selectedItem.sites" :key="'d-' + link.site + link.id" class="link-row">
              <span class="link-site">{{ linkTitle(link) }}</span>
              <span class="link-id">{{ link.id }}</span>
              <el-button
                v-if="link.url"
                link
                type="primary"
                size="small"
                :icon="Link"
                @click="openLink(link.url)"
              >
                打开
              </el-button>
              <el-button
                link
                type="info"
                size="small"
                :icon="CopyDocument"
                @click="copyText(link.url || link.id, '链接')"
              >
                复制
              </el-button>
            </div>
          </div>
        </div>
      </div>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import axios from 'axios'
import { ElMessage } from 'element-plus'
import { CopyDocument, Link, View } from '@element-plus/icons-vue'

interface BangumiSiteLink {
  site: string
  title: string
  type: string
  id: string
  url: string
}

interface BangumiItem {
  id: number
  bangumi_id: string
  title: string
  title_zh: string
  title_translate: Record<string, string[]>
  type: string
  lang: string
  official_site: string
  begin: string
  end: string
  broadcast: string
  comment: string
  sites: BangumiSiteLink[]
  tmdb_id: string
  mal_id: string
  anidb_id: string
  anilist_id: string
}

interface BangumiSyncMeta {
  status: string
  message: string
  last_sync_at: string
  last_attempt_at: string
  next_sync_at: string
  item_count: number
  duration_ms: number
  source_url: string
  version: string
}

const items = ref<BangumiItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const searchQuery = ref('')
const typeFilter = ref('')
const langFilter = ref('')
const loading = ref(false)

const meta = ref<BangumiSyncMeta>({
  status: 'idle',
  message: '',
  last_sync_at: '',
  last_attempt_at: '',
  next_sync_at: '',
  item_count: 0,
  duration_ms: 0,
  source_url: '',
  version: '',
})
const running = ref(false)
const syncing = computed(() => running.value || meta.value.status === 'running')

const detailVisible = ref(false)
const selectedItem = ref<BangumiItem | null>(null)

// 站点链接优先展示顺序，其余站点收进「更多」浮层。
const preferredSiteOrder = ['bangumi', 'tmdb', 'mal', 'aniList', 'anidb', 'bilibili', 'bangumi_moe', 'dmhy', 'mikan']
const maxPrimarySites = 5

const typeLabels: Record<string, string> = {
  tv: 'TV',
  movie: '剧场版',
  ova: 'OVA',
  web: '网络动画',
}

const langLabels: Record<string, string> = {
  ja: '日语',
  'zh-Hans': '简体中文',
  'zh-Hant': '繁体中文',
  en: '英语',
}

function typeLabel(value: string): string {
  if (!value) return '未知'
  return typeLabels[value] || value
}

function typeTagType(value: string): 'primary' | 'success' | 'warning' | 'info' | 'danger' {
  switch (value) {
    case 'movie':
      return 'warning'
    case 'ova':
      return 'info'
    case 'web':
      return 'success'
    default:
      return 'primary'
  }
}

function langLabel(value: string): string {
  if (!value) return '未知'
  return langLabels[value] || value
}

function linkTitle(link: BangumiSiteLink): string {
  return link.title || link.site
}

// formatDate 取 ISO 时间的 UTC 日期部分：bangumi-data 统一按 UTC 存储，取 UTC 日期即上游标注的播出日期。
function formatDate(raw: string): string {
  const trimmed = (raw || '').trim()
  if (trimmed.length < 10) return ''
  return trimmed.slice(0, 10)
}

function formatDateRange(begin: string, end: string): string {
  const start = formatDate(begin)
  const finish = formatDate(end)
  if (start && finish && start !== finish) return `${start} ~ ${finish}`
  return start || finish || ''
}

// primarySites 按偏好顺序返回前若干个站点链接。
function primarySites(sites: BangumiSiteLink[]): BangumiSiteLink[] {
  if (!sites || sites.length === 0) return []
  const ordered: BangumiSiteLink[] = []
  for (const code of preferredSiteOrder) {
    const matched = sites.find((link) => link.site === code)
    if (matched) ordered.push(matched)
  }
  for (const link of sites) {
    if (!ordered.includes(link)) ordered.push(link)
  }
  return ordered.slice(0, maxPrimarySites)
}

// allTitles 汇总条目除默认中文名外的其他译名，便于详情页核对。
function allTitles(item: BangumiItem): Array<{ lang: string; values: string[] }> {
  const translate = item.title_translate || {}
  return Object.keys(translate)
    .filter((key) => Array.isArray(translate[key]) && translate[key].length > 0)
    .map((key) => ({ lang: key, values: translate[key].filter((value) => (value || '').trim() !== '') }))
    .filter((entry) => entry.values.length > 0)
}

const lastSyncText = computed(() => meta.value.last_sync_at || '尚未同步')

const statusTagType = computed<'primary' | 'success' | 'warning' | 'info' | 'danger'>(() => {
  switch (meta.value.status) {
    case 'running':
      return 'warning'
    case 'success':
      return 'success'
    case 'failed':
      return 'danger'
    default:
      return 'info'
  }
})

const statusTagText = computed(() => {
  switch (meta.value.status) {
    case 'running':
      return '同步中'
    case 'success':
      return '同步正常'
    case 'failed':
      return '同步失败'
    default:
      return '尚未同步'
  }
})

async function copyText(text: string, label: string) {
  if (!text) {
    ElMessage.warning('内容为空，无法复制')
    return
  }
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
    } else {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
    }
    ElMessage.success(`${label} 已复制`)
  } catch {
    ElMessage.error('复制失败')
  }
}

function copyDetail(item: BangumiItem) {
  const lines = [
    `中文名: ${item.title_zh || 'N/A'}`,
    `原名: ${item.title || 'N/A'}`,
    `类型: ${typeLabel(item.type)}`,
    `语言: ${langLabel(item.lang)}`,
    `播出日期: ${formatDateRange(item.begin, item.end) || 'N/A'}`,
    `Bangumi ID: ${item.bangumi_id || 'N/A'}`,
    `TMDb ID: ${item.tmdb_id || 'N/A'}`,
    `MAL ID: ${item.mal_id || 'N/A'}`,
    `AniDB ID: ${item.anidb_id || 'N/A'}`,
    `AniList ID: ${item.anilist_id || 'N/A'}`,
    `官网: ${item.official_site || 'N/A'}`,
  ]
  copyText(lines.join('\n'), '条目信息')
}

function openLink(url: string) {
  if (!url) {
    ElMessage.warning('链接为空，无法打开')
    return
  }
  window.open(url, '_blank', 'noopener')
}

function openDetail(row: BangumiItem) {
  selectedItem.value = row
  detailVisible.value = true
}

async function fetchList() {
  loading.value = true
  try {
    const params = new URLSearchParams()
    params.set('page', String(page.value))
    params.set('page_size', String(pageSize.value))
    if (searchQuery.value.trim()) params.set('keyword', searchQuery.value.trim())
    if (typeFilter.value) params.set('type', typeFilter.value)
    if (langFilter.value) params.set('lang', langFilter.value)

    const response = await axios.get(`/api/bangumi/items?${params.toString()}`)
    items.value = response.data.items || []
    total.value = response.data.total || 0
  } catch (error) {
    const message = axios.isAxiosError(error) ? error.message : String(error)
    ElMessage.error('获取番组数据失败: ' + message)
  } finally {
    loading.value = false
  }
}

async function fetchStatus() {
  try {
    const response = await axios.get('/api/bangumi/status')
    if (response.data?.meta) {
      meta.value = { ...meta.value, ...response.data.meta }
    }
    running.value = response.data?.running === true
    const serverTotal = Number(response.data?.total || 0)
    if (!searchQuery.value.trim() && !typeFilter.value && !langFilter.value && serverTotal > 0) {
      total.value = serverTotal
    }
  } catch (error) {
    const message = axios.isAxiosError(error) ? error.message : String(error)
    console.error('获取同步状态失败:', message)
  }
}

let pollTimer: number | null = null
let pollCount = 0

function stopPolling() {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer)
    pollTimer = null
  }
}

// startPolling 同步期间轮询状态，完成后停止并刷新列表。
function startPolling() {
  stopPolling()
  pollCount = 0
  pollTimer = window.setInterval(async () => {
    pollCount += 1
    await fetchStatus()
    if (meta.value.status !== 'running' && !running.value) {
      stopPolling()
      if (meta.value.status === 'success') {
        ElMessage.success(meta.value.message || '同步完成')
        fetchList()
      } else if (meta.value.status === 'failed') {
        ElMessage.error(meta.value.message || '同步失败')
      }
      return
    }
    // 最长轮询 5 分钟，避免异常情况下无限请求。
    if (pollCount >= 100) {
      stopPolling()
      ElMessage.warning('同步仍在进行中，请稍后手动刷新查看结果')
    }
  }, 3000)
}

async function triggerSync() {
  if (syncing.value) return
  running.value = true
  try {
    const response = await axios.post('/api/bangumi/sync')
    if (response.data?.success) {
      ElMessage.success('已开始同步，请稍候')
      startPolling()
    } else {
      running.value = false
      ElMessage.warning(response.data?.message || '触发同步失败')
    }
  } catch (error) {
    running.value = false
    if (axios.isAxiosError(error) && error.response?.status === 409) {
      ElMessage.warning('同步正在进行中，请稍后再试')
      running.value = true
      startPolling()
      return
    }
    const message = axios.isAxiosError(error) ? error.message : String(error)
    ElMessage.error('触发同步失败: ' + message)
  }
}

function applyFilters() {
  page.value = 1
  fetchList()
}

function clearFilters() {
  searchQuery.value = ''
  typeFilter.value = ''
  langFilter.value = ''
  page.value = 1
  fetchList()
}

function handleSizeChange() {
  page.value = 1
  fetchList()
}

function refreshAll() {
  fetchStatus()
  fetchList()
}

onMounted(async () => {
  await fetchStatus()
  if (meta.value.status === 'running' || running.value) {
    startPolling()
  }
  await fetchList()
})
</script>

<style scoped>
.bangumi-view {
  padding: 15px;
  display: flex;
  flex-direction: column;
  gap: 15px;
  height: 100%;
  overflow: auto;
}

.sync-panel {
  padding: 15px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.sync-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.sync-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 16px;
  font-weight: 600;
}

.sync-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 18px;
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.sync-message {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.sync-message.is-error {
  color: var(--el-color-danger);
}

.sync-actions {
  display: flex;
  align-items: center;
}

.search-and-controls {
  padding: 15px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px 0;
}

.table-container {
  padding: 15px;
  border-radius: 8px;
}

.cell-with-copy {
  display: flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
}

.cell-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.empty-text {
  color: var(--el-text-color-placeholder);
}

.site-links {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 2px 8px;
}

/* 站点链接按钮内附带的条目 ID，弱化颜色以突出站点名。 */
.site-link-id {
  margin-left: 4px;
  font-variant-numeric: tabular-nums;
  color: var(--el-text-color-regular);
}

.popover-links {
  display: flex;
  flex-wrap: wrap;
  gap: 2px 10px;
}

.pagination-container {
  margin-top: 15px;
  display: flex;
  justify-content: flex-end;
}

.bangumi-detail-dialog {
  max-height: 85%;
  overflow-y: auto;
  padding-right: 10px;
}

.detail-body {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.detail-toolbar {
  display: flex;
  gap: 8px;
}

.detail-section-title {
  font-weight: 600;
  margin-bottom: 8px;
}

.title-list,
.link-list {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.title-row,
.link-row {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 13px;
}

.title-lang,
.link-site {
  min-width: 90px;
  color: var(--el-text-color-secondary);
}

.link-id {
  color: var(--el-text-color-secondary);
  font-family: monospace;
}
</style>
