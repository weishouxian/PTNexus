<template>
  <div class="reseed-view">
    <el-alert
      v-if="!tokenConfigured"
      class="token-alert"
      type="warning"
      show-icon
      :closable="false"
    >
      <template #title>
        <span class="token-alert-title">
          未配置 IYUU Token，无法执行辅种
          <el-button type="primary" link @click="goSettings">前往设置 → IYUU 设置</el-button>
        </span>
      </template>
    </el-alert>

    <el-card class="glass-table config-card" shadow="never">
      <template #header>
        <div class="card-header">
          <span>辅种配置</span>
          <span class="card-hint">把本地已在做种的资源，在指定站点重新做种（不会下载数据，只补种）</span>
        </div>
      </template>

      <el-form :inline="true" label-width="82px" class="config-form">
        <el-form-item label="目标站点">
          <div class="site-picker">
            <el-select
              v-model="form.sites"
              multiple
              filterable
              clearable
              collapse-tags
              collapse-tags-tooltip
              :max-collapse-tags="3"
              placeholder="选择要辅种的站点"
              class="site-select"
              :disabled="!tokenConfigured"
            >
              <el-option
                v-for="site in siteOptions"
                :key="site.nickname"
                :label="site.nickname"
                :value="site.nickname"
                :disabled="!site.can_reseed"
              >
                <el-tooltip
                  :disabled="!site.disabled_reason"
                  :content="site.disabled_reason"
                  placement="right"
                  :show-after="120"
                >
                  <span class="site-option" :class="{ 'is-disabled': !site.can_reseed }">
                    <span class="site-name">{{ site.nickname }}</span>
                    <span class="site-tags">
                      <el-tag v-if="site.has_passkey" size="small" type="success" effect="plain">passkey</el-tag>
                      <el-tag v-else-if="site.has_cookie" size="small" type="info" effect="plain">cookie</el-tag>
                      <el-tag v-if="!site.can_reseed" size="small" type="info" effect="dark">IYUU 未收录</el-tag>
                    </span>
                  </span>
                </el-tooltip>
              </el-option>
            </el-select>
            <el-button
              link
              type="primary"
              :disabled="!tokenConfigured || reseedableSiteCount === 0 || allSitesSelected"
              @click="selectAllSites"
            >
              全选
            </el-button>
            <el-button link type="danger" :disabled="!tokenConfigured || form.sites.length === 0" @click="clearSites">
              清空
            </el-button>
            <el-tooltip
              v-if="siteOptions.length > 0"
              :content="siteCountHint"
              placement="top"
            >
              <span class="site-count">可选 {{ reseedableSiteCount }} / {{ siteOptions.length }}</span>
            </el-tooltip>
            <el-tooltip v-else :content="noCredentialSiteHint" placement="top">
              <span class="site-count is-empty">暂无可选站点</span>
            </el-tooltip>
          </div>
        </el-form-item>

        <el-form-item label="指定种子">
          <div class="torrent-picker">
            <el-button :disabled="!tokenConfigured" @click="openTorrentPicker">
              {{ form.torrents.length ? `已选 ${form.torrents.length} 个种子` : '选择种子（试跑）' }}
            </el-button>
            <el-button
              v-if="form.torrents.length"
              link
              type="danger"
              :disabled="!tokenConfigured"
              @click="clearPickedTorrents"
            >
              清空
            </el-button>
            <el-tooltip content="留空则按下方条件批量辅种" placement="top">
              <el-icon class="hint-icon"><QuestionFilled /></el-icon>
            </el-tooltip>
          </div>
        </el-form-item>

        <el-form-item label="保存路径">
          <el-select
            v-model="form.paths"
            multiple
            filterable
            clearable
            collapse-tags
            collapse-tags-tooltip
            placeholder="全部路径"
            class="path-select"
            :disabled="!tokenConfigured || form.torrents.length > 0"
          >
            <el-option v-for="path in pathOptions" :key="path" :label="path" :value="path" />
          </el-select>
        </el-form-item>

        <el-form-item label="下载器">
          <el-select
            v-model="downloaderFieldValue"
            :clearable="form.torrents.length > 0"
            :placeholder="downloaderFieldPlaceholder"
            class="downloader-select"
            :disabled="!tokenConfigured || form.torrents.length === 0"
          >
            <el-option
              v-for="item in downloaderOptions"
              :key="item.id"
              :label="item.name"
              :value="item.id"
              :disabled="item.enabled === false"
            />
          </el-select>
          <el-tooltip :content="downloaderFieldHint" placement="top">
            <el-icon class="hint-icon"><QuestionFilled /></el-icon>
          </el-tooltip>
        </el-form-item>

        <el-form-item label="数量上限">
          <el-input-number
            v-model="form.limit"
            :min="1"
            :max="2000"
            :step="50"
            controls-position="right"
            class="limit-input"
            :disabled="form.torrents.length > 0"
          />
        </el-form-item>

        <el-form-item label="跳过校验">
          <el-switch v-model="form.skip_checking" :disabled="!tokenConfigured" />
          <el-tooltip
            content="开启后下载器不做完整性校验，直接做种；文件与站点不一致时建议关闭"
            placement="top"
          >
            <el-icon class="hint-icon"><QuestionFilled /></el-icon>
          </el-tooltip>
        </el-form-item>

        <el-form-item label="标签">
          <div class="tag-picker">
            <el-select
              v-model="form.tags"
              multiple
              filterable
              allow-create
              default-first-option
              clearable
              collapse-tags
              collapse-tags-tooltip
              :max-collapse-tags="3"
              placeholder="留空则不设置标签，可直接输入新标签"
              class="tag-select"
              :disabled="!tokenConfigured"
            >
              <el-option v-for="tag in tagOptions" :key="tag" :label="tag" :value="tag" />
            </el-select>
            <el-checkbox v-model="form.tag_by_site" :disabled="!tokenConfigured">按目标站名打标签（站点/站名）</el-checkbox>
            <el-tooltip
              content="勾选后每个种子额外打上「站点/目标站名」标签（如「站点/财神」），与设置里标签/分类设置的站点标签同名，可与上面的手动标签叠加"
              placement="top"
            >
              <el-icon class="hint-icon"><QuestionFilled /></el-icon>
            </el-tooltip>
          </div>
        </el-form-item>

        <el-form-item>
          <el-button
            type="primary"
            :loading="starting"
            :disabled="!tokenConfigured || running || form.sites.length === 0"
            @click="startReseed"
          >
            开始辅种
          </el-button>
          <el-button v-if="running" type="danger" plain @click="stopReseed">停止</el-button>
        </el-form-item>
      </el-form>

      <div v-if="form.torrents.length > 0" class="pin-hint">
        已指定 {{ form.torrents.length }} 个种子试跑：只处理这些种子，忽略数量上限与体积下限，保存路径取种子自身记录
      </div>
      <div v-else class="scope-hint" :class="{ warning: !globalDownloaderId }">
        <el-icon><InfoFilled /></el-icon>
        <span v-if="globalDownloaderId">
          未指定种子：只对右上角全局下载器「{{ globalDownloaderLabel }}」中的种子辅种，任务也加入该下载器。
        </span>
        <span v-else>
          未指定种子，右上角为「全部下载器」：将处理全部下载器中的种子。建议先在右上角选定下载器以限定范围。
        </span>
      </div>
    </el-card>

    <el-card v-if="task" class="glass-table progress-card" shadow="never">
      <template #header>
        <div class="card-header">
          <span>任务进度</span>
        <el-tag :type="taskTagType" size="small">
          {{ taskOutcome.label }}
        </el-tag>
        </div>
      </template>

      <el-progress
        :percentage="progressPercent"
        :status="task.isRunning ? '' : task.success ? 'success' : 'exception'"
        :stroke-width="14"
      />
      <div class="progress-message">{{ task.message || '等待任务更新…' }}</div>

      <div class="stat-grid">
        <div class="stat-item">
          <div class="stat-value">{{ task.total ?? 0 }}</div>
          <div class="stat-label">待处理组</div>
        </div>
        <div class="stat-item">
          <div class="stat-value">{{ statNumber('matched') }}</div>
          <div class="stat-label">命中组</div>
        </div>
        <div class="stat-item">
          <div class="stat-value">{{ statNumber('no_match') }}</div>
          <div class="stat-label">未命中组</div>
        </div>
        <div class="stat-item success">
          <div class="stat-value">{{ statNumber('added') }}</div>
          <div class="stat-label">新增做种</div>
        </div>
        <div class="stat-item">
          <div class="stat-value">{{ statNumber('duplicate') }}</div>
          <div class="stat-label">下载器已存在</div>
        </div>
        <div class="stat-item">
          <div class="stat-value">{{ statNumber('skipped') }}</div>
          <div class="stat-label">该站已做种</div>
        </div>
        <div class="stat-item warning">
          <div class="stat-value">{{ statNumber('mismatch') }}</div>
          <div class="stat-label">校验不一致</div>
        </div>
        <div class="stat-item danger">
          <div class="stat-value">{{ statNumber('auth_failed') }}</div>
          <div class="stat-label">凭据失效</div>
        </div>
        <div class="stat-item danger">
          <div class="stat-value">{{ statNumber('failed') }}</div>
          <div class="stat-label">失败</div>
        </div>
      </div>
    </el-card>

    <el-card class="glass-table records-card" shadow="never">
      <template #header>
        <div class="card-header">
          <span>辅种记录</span>
          <div class="record-filters">
            <el-select v-model="filters.target_site" placeholder="站点" clearable class="filter">
              <el-option v-for="site in recordSiteOptions" :key="site" :label="site" :value="site" />
            </el-select>
            <el-select v-model="filters.status" placeholder="状态" clearable class="filter">
              <el-option
                v-for="item in statusOptions"
                :key="item.value"
                :label="item.label"
                :value="item.value"
              />
            </el-select>
            <el-input
              v-model="filters.keyword"
              placeholder="搜索种子名"
              clearable
              class="search"
              @keyup.enter="applyFilters"
            />
            <el-button @click="applyFilters">筛选</el-button>
            <el-button :loading="recordsLoading" @click="fetchRecords">刷新</el-button>
          </div>
        </div>
      </template>

      <el-table
        v-loading="recordsLoading"
        :data="records"
        size="small"
        height="420"
        stripe
        empty-text="暂无辅种记录"
      >
        <el-table-column prop="created_at" label="时间" width="150" />
        <el-table-column prop="torrent_name" label="种子名" min-width="260">
          <template #default="{ row }">
            <el-tooltip :content="row.torrent_name" placement="top" :show-after="400">
              <span class="ellipsis">{{ row.torrent_name }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column prop="target_site" label="目标站" width="110" />
        <el-table-column prop="target_torrent_id" label="站点ID" width="90" />
        <el-table-column label="标签" width="150">
          <template #default="{ row }">
            <div v-if="tagList(row.tags).length" class="record-tags">
              <el-tag v-for="tag in tagList(row.tags)" :key="tag" size="small" effect="plain">
                {{ tag }}
              </el-tag>
            </div>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small" effect="light">
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="save_path" label="保存路径" min-width="180">
          <template #default="{ row }">
            <el-tooltip :content="row.save_path" placement="top" :show-after="400">
              <span class="ellipsis">{{ row.save_path }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column prop="message" label="说明" min-width="220">
          <template #default="{ row }">
            <el-tooltip :content="row.message" placement="top" :show-after="400">
              <span class="ellipsis">{{ row.message }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column label="链接" width="80">
          <template #default="{ row }">
            <el-link v-if="row.detail_url" type="primary" :href="row.detail_url" target="_blank">
              打开
            </el-link>
            <span v-else>-</span>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="filters.page"
          v-model:page-size="filters.page_size"
          :page-sizes="[20, 50, 100]"
          :total="total"
          layout="total, sizes, prev, pager, next"
          background
          @current-change="fetchRecords"
          @size-change="fetchRecords"
        />
      </div>
    </el-card>

    <el-dialog
      v-model="pickerVisible"
      title="选择要试跑的种子"
      width="960px"
      top="6vh"
      append-to-body
      class="torrent-picker-dialog"
    >
      <div class="picker-toolbar">
        <el-input
          v-model="pickerKeyword"
          placeholder="输入种子名关键词后回车搜索"
          clearable
          class="picker-search"
          :disabled="pickerLoading"
          @keyup.enter="searchPicker(1)"
          @clear="searchPicker(1)"
        >
          <template #append>
            <el-button :loading="pickerLoading" @click="searchPicker(1)">搜索</el-button>
          </template>
        </el-input>
        <el-button @click="pickCurrentPage">全选本页</el-button>
        <el-button @click="unpickCurrentPage">取消本页</el-button>
        <el-button link type="danger" @click="clearAllPicks">清空全部</el-button>
        <span class="picker-count">已选 {{ pickedCount }} 个（跨页/换关键词均保留）</span>
        <el-tag size="small" type="info" effect="plain">
          {{ globalDownloaderLabel ? `仅显示下载器：${globalDownloaderLabel}` : '下载器：全部（右上角可切换）' }}
        </el-tag>
      </div>

      <el-table
        v-loading="pickerLoading"
        :data="pickerRows"
        size="small"
        height="420"
        stripe
        empty-text="没有匹配的种子，换个关键词试试"
      >
        <el-table-column width="52">
          <template #default="{ row }">
            <el-checkbox
              :model-value="Boolean(pickerPicked[row.unique_id])"
              @change="togglePick(row)"
            />
          </template>
        </el-table-column>
        <el-table-column prop="name" label="种子名" min-width="320">
          <template #default="{ row }">
            <el-tooltip :content="row.name" placement="top" :show-after="400">
              <span class="ellipsis">{{ row.name }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
        <el-table-column prop="size_formatted" label="体积" width="110" />
        <el-table-column prop="state" label="状态" width="130">
          <template #default="{ row }">
            <span class="ellipsis">{{ row.state || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="站点数" width="80">
          <template #default="{ row }">{{ Array.isArray(row.sites) ? row.sites.length : 0 }}</template>
        </el-table-column>
        <el-table-column prop="save_path" label="保存路径" min-width="200">
          <template #default="{ row }">
            <el-tooltip :content="row.save_path" placement="top" :show-after="400">
              <span class="ellipsis">{{ row.save_path || '-' }}</span>
            </el-tooltip>
          </template>
        </el-table-column>
      </el-table>

      <div class="pagination-row">
        <el-pagination
          v-model:current-page="pickerPage"
          :page-size="pickerPageSize"
          :total="pickerTotal"
          layout="total, prev, pager, next"
          background
          @current-change="(page: number) => searchPicker(page)"
        />
      </div>

      <template #footer>
        <el-button @click="pickerVisible = false">取消</el-button>
        <el-button type="primary" @click="confirmPickedTorrents">确定（{{ pickedCount }}）</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import axios from 'axios'
import { ElMessage, ElMessageBox } from 'element-plus'
import { InfoFilled, QuestionFilled } from '@element-plus/icons-vue'
import { useGlobalDownloaderStore } from '@/stores/globalDownloader'

interface ReseedSiteRow {
  nickname: string
  site?: string
  base_url?: string
  has_cookie?: boolean
  has_passkey?: boolean
  can_reseed?: boolean
  disabled_reason?: string
}

interface DownloaderOption {
  id: string
  name: string
  type?: string
  enabled?: boolean
}

interface ReseedTask {
  task_id: string
  created_at?: string
  finished_at?: string | null
  isRunning: boolean
  success?: boolean | null
  message?: string
  total?: number
  processed?: number
  stats?: Record<string, unknown>
}

interface ReseedRecord {
  id: number
  created_at: string
  torrent_name: string
  size: number
  target_site: string
  target_torrent_id: string
  save_path: string
  tags?: string
  status: string
  message: string
  detail_url?: string
}

interface TorrentPickerItem {
  unique_id: string
  name: string
  size: number
  size_formatted?: string
  hashes?: string[]
  save_path?: string
  state?: string
  sites?: unknown[]
}

const TAG_STORAGE_KEY = 'ptnexus.reseed.tags'
const PICKER_PAGE_SIZE = 20

const router = useRouter()
// 顶部（页面右上角）的全局下载器选择：种子弹框按它筛选候选、辅种默认用它做目标下载器。
const globalDownloader = useGlobalDownloaderStore()

const POLL_INTERVAL_MS = 2000

const tokenConfigured = ref(false)
const siteOptions = ref<ReseedSiteRow[]>([])
// IYUU 支持站点列表是否取到：取不到时后端只按凭据判断，页面要说明这一点。
const iyuuSiteListKnown = ref(true)
// 被后端剔除的「未配置 Cookie / Passkey」站点数量，用于说明列表为何变短。
const siteHiddenCount = ref(0)
const pathOptions = ref<string[]>([])
const downloaderOptions = ref<DownloaderOption[]>([])
const recordSiteOptions = ref<string[]>([])

// 指定种子弹框：pickerPicked 按 unique_id 保存勾选结果，
// 跨分页/跨关键词都保留，避免只翻一页就丢掉之前选的种子。
const pickerVisible = ref(false)
const pickerLoading = ref(false)
const pickerKeyword = ref('')
const pickerRows = ref<TorrentPickerItem[]>([])
const pickerPicked = ref<Record<string, TorrentPickerItem>>({})
const pickerPage = ref(1)
const pickerPageSize = PICKER_PAGE_SIZE
const pickerTotal = ref(0)
// 已确定种子的 hash 缓存：远程分页后下拉已经没有对应行，只能靠这里取 hash。
const torrentHashMap = ref<Record<string, string[]>>({})
const torrentCache = ref<Record<string, TorrentPickerItem>>({})
const tagOptions = ref<string[]>([])

const starting = ref(false)
const running = ref(false)
const task = ref<ReseedTask | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

const records = ref<ReseedRecord[]>([])
const recordsLoading = ref(false)
const total = ref(0)

const form = reactive({
  sites: [] as string[],
  torrents: [] as string[],
  paths: [] as string[],
  downloader_id: '',
  limit: 200,
  skip_checking: true,
  tags: [] as string[],
  tag_by_site: false,
})

const filters = reactive({
  target_site: '',
  status: '',
  keyword: '',
  page: 1,
  page_size: 20,
})

const statusOptions = [
  { value: 'added', label: '新增做种' },
  { value: 'duplicate', label: '下载器已存在' },
  { value: 'skipped', label: '该站已做种' },
  { value: 'no_match', label: '未命中' },
  { value: 'mismatch', label: '校验不一致' },
  { value: 'auth_failed', label: '凭据失效' },
  { value: 'failed', label: '失败' },
]

const progressPercent = computed(() => {
  const current = task.value
  if (!current || !current.total) return 0
  return Math.min(100, Math.round(((current.processed ?? 0) / current.total) * 100))
})

const taskTagType = computed(() => {
  const current = task.value
  if (!current) return 'info'
  if (current.isRunning) return 'warning'
  return current.success ? taskOutcome.value.type : 'danger'
})

// taskOutcome 决定任务结束时显示什么结论：
// 全零统计不能算「已完成」，否则「什么都没发生」会被读成「辅种成功」。
const taskOutcome = computed<{ label: string; type: 'success' | 'info' | 'warning' | 'danger' }>(() => {
  const current = task.value
  if (!current) return { label: '空闲', type: 'info' }
  if (current.isRunning) return { label: '进行中', type: 'warning' }
  if (!current.success) return { label: '已停止/失败', type: 'danger' }
  const added = statNumber('added')
  const duplicate = statNumber('duplicate')
  const skipped = statNumber('skipped')
  const matched = statNumber('matched')
  const authFailed = statNumber('auth_failed')
  if (added > 0) return { label: '已完成', type: 'success' }
  if (authFailed > 0) return { label: '凭据失效', type: 'danger' }
  if (duplicate + skipped > 0) return { label: '无需新增（已存在）', type: 'info' }
  if (matched === 0) return { label: '未命中', type: 'warning' }
  return { label: '已命中未添加', type: 'warning' }
})

const statNumber = (key: string): number => {
  const value = task.value?.stats?.[key]
  const parsed = Number(value ?? 0)
  return Number.isFinite(parsed) ? parsed : 0
}

const statusLabel = (status: string): string => {
  return statusOptions.find((item) => item.value === status)?.label ?? status
}

// tagList 把记录里的逗号分隔标签拆成数组，供表格渲染。
const tagList = (raw?: string): string[] => {
  return String(raw ?? '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean)
}

const statusTagType = (status: string): 'success' | 'info' | 'warning' | 'danger' => {
  switch (status) {
    case 'added':
      return 'success'
    case 'duplicate':
      return 'info'
    case 'skipped':
      return 'warning'
    case 'no_match':
      return 'info'
    case 'auth_failed':
      return 'warning'
    default:
      return 'danger'
  }
}

const goSettings = () => {
  void router.push('/settings/general')
}

// 顶部全局下载器（页面右上角）的当前取值：空字符串表示「全部下载器」。
const globalDownloaderId = computed(() => (globalDownloader.selectedDownloaderId || '').trim())

const globalDownloaderLabel = computed(() => {
  const id = globalDownloaderId.value
  if (!id) return ''
  const matched = downloaderOptions.value.find((item) => item.id === id)
  return matched?.name || id
})

// 可辅种站点：siteOptions 只包含「已设置 Cookie 或 Passkey」的站点（后端已剔除无凭据站），
// can_reseed 再按 IYUU 是否收录判定。
const reseedableSites = computed(() => siteOptions.value.filter((site) => site.can_reseed !== false))
const reseedableSiteCount = computed(() => reseedableSites.value.length)

const siteCountHint = computed(() => {
  const base = '站点列表只含已设置 Cookie / Passkey 的站点'
  const hidden =
    siteHiddenCount.value > 0 ? `（另有 ${siteHiddenCount.value} 个站点未配置凭据已隐藏）` : ''
  return iyuuSiteListKnown.value
    ? `${base}${hidden}；灰显站点为 IYUU 未收录，鼠标悬停可看原因`
    : `${base}${hidden}；未能读取 IYUU 站点列表，本次只按凭据判断`
})

const noCredentialSiteHint = '没有任何站点配置 Cookie / Passkey，辅种需要登录站点下载种子文件，请先到「设置 → 站点」完善后再来'

const allSitesSelected = computed(() => {
  return reseedableSiteCount.value > 0 && form.sites.length >= reseedableSiteCount.value
})

// selectAllSites 一键把可辅种站点（有凭据且 IYUU 收录）全部选为目标站。
const selectAllSites = () => {
  form.sites = reseedableSites.value.map((site) => site.nickname)
}

const clearSites = () => {
  form.sites = []
}

// 批量模式（未指定种子）下，候选范围与目标下载器一律锁定为右上角的全局下载器；
// 指定种子试跑时才允许单独挑目标下载器。get/set 分开写，避免把全局值回写进表单。
const downloaderFieldValue = computed<string>({
  get: () => (form.torrents.length === 0 ? globalDownloaderId.value : form.downloader_id),
  set: (value: string) => {
    form.downloader_id = value || ''
  },
})

const downloaderFieldPlaceholder = computed(() => {
  if (form.torrents.length === 0) {
    return globalDownloaderId.value ? '跟随右上角全局下载器' : '全部下载器（右上角可指定）'
  }
  return '跟随种子记录'
})

const downloaderFieldHint = computed(() => {
  if (form.torrents.length === 0) {
    return globalDownloaderId.value
      ? `未指定种子：批量范围锁定为右上角全局下载器「${globalDownloaderLabel.value}」中的种子，需要换范围请在右上角切换`
      : '未指定种子：将处理全部下载器中的种子，建议先在右上角选定下载器'
  }
  return '指定种子试跑时可选目标下载器，留空则沿用种子自身的下载器'
})

const readStoredTags = (): string[] => {
  try {
    const raw = window.localStorage.getItem(TAG_STORAGE_KEY)
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    return Array.isArray(parsed) ? parsed.filter((item): item is string => typeof item === 'string') : []
  } catch {
    return []
  }
}

const collectTagOptions = () => {
  const pool = new Set<string>(readStoredTags())
  // 站点标签与设置里标签/分类设置同一口径：建议项是「站点/站名」，不是裸站名。
  reseedableSites.value.forEach((site) => {
    const nickname = String(site.nickname ?? '').trim()
    if (nickname) pool.add(`站点/${nickname}`)
  })
  records.value.forEach((row) => {
    String(row.tags ?? '')
      .split(',')
      .map((item) => item.trim())
      .filter(Boolean)
      .forEach((item) => pool.add(item))
  })
  tagOptions.value = Array.from(pool).sort((a, b) => a.localeCompare(b, 'zh-CN'))
}

const pickedCount = computed(() => Object.keys(pickerPicked.value).length)

// openTorrentPicker 打开选择种子的弹框，并把已确定的选中项还原到弹框里。
const openTorrentPicker = () => {
  pickerVisible.value = true
  const restored: Record<string, TorrentPickerItem> = {}
  form.torrents.forEach((id) => {
    const cached = torrentCache.value[id]
    if (cached) restored[id] = cached
  })
  pickerPicked.value = restored
  void searchPicker(1)
}

const clearPickedTorrents = () => {
  form.torrents = []
  torrentHashMap.value = {}
  torrentCache.value = {}
}

// searchPicker 分页查询本地种子库，供弹框表格展示。
// 参数/返回：page 为目标页码；无返回值，结果写入 pickerRows/pickerTotal。
// 失败场景：接口异常时保留旧列表并提示。
// 副作用：发起一次只读 HTTP 请求。
const searchPicker = async (page: number) => {
  const target = page > 0 ? page : 1
  pickerLoading.value = true
  try {
    const scope = globalDownloaderId.value
    const response = await axios.get('/api/data', {
      params: {
        page: target,
        pageSize: pickerPageSize,
        nameSearch: pickerKeyword.value.trim() || undefined,
        // 与「一种多站」一致：顶部选了下载器就只在该下载器范围内挑种子。
        downloader_filters: scope ? JSON.stringify([scope]) : undefined,
      },
    })
    const items: TorrentPickerItem[] = Array.isArray(response.data?.data) ? response.data.data : []
    pickerRows.value = items
    pickerTotal.value = Number(response.data?.total ?? 0)
    pickerPage.value = target
  } catch (error) {
    console.error('搜索种子失败:', error)
    ElMessage.error('读取种子列表失败')
  } finally {
    pickerLoading.value = false
  }
}

const togglePick = (row: TorrentPickerItem) => {
  const next = { ...pickerPicked.value }
  if (next[row.unique_id]) {
    delete next[row.unique_id]
  } else {
    next[row.unique_id] = row
  }
  pickerPicked.value = next
}

const pickCurrentPage = () => {
  const next = { ...pickerPicked.value }
  pickerRows.value.forEach((row) => {
    next[row.unique_id] = row
  })
  pickerPicked.value = next
}

const unpickCurrentPage = () => {
  const next = { ...pickerPicked.value }
  pickerRows.value.forEach((row) => {
    delete next[row.unique_id]
  })
  pickerPicked.value = next
}

const clearAllPicks = () => {
  pickerPicked.value = {}
}

// confirmPickedTorrents 把弹框勾选结果落到表单，并缓存 hash 供提交使用。
const confirmPickedTorrents = () => {
  const picked = Object.values(pickerPicked.value)
  form.torrents = picked.map((item) => item.unique_id)
  picked.forEach((item) => {
    torrentCache.value[item.unique_id] = item
    torrentHashMap.value[item.unique_id] = Array.isArray(item.hashes)
      ? item.hashes.map((hash) => String(hash).trim()).filter(Boolean)
      : []
  })
  pickerVisible.value = false
}

// selectedHashes 汇总所选种子的 infohash（分页/换关键词后依然可用）。
const selectedHashes = computed(() => {
  const result: string[] = []
  form.torrents.forEach((id) => {
    ;(torrentHashMap.value[id] ?? []).forEach((hash) => {
      const normalized = hash.trim().toLowerCase()
      if (normalized && !result.includes(normalized)) result.push(normalized)
    })
  })
  return result
})

const fetchStatus = async () => {
  try {
    const response = await axios.get('/api/reseed/status')
    if (!response.data?.success && response.data?.success !== undefined) {
      ElMessage.error(response.data?.message || '读取辅种状态失败')
      return
    }
    tokenConfigured.value = Boolean(response.data?.token_configured)
    siteOptions.value = Array.isArray(response.data?.sites) ? response.data.sites : []
    // 站点凭据/支持情况可能变了：把已选中但已不可辅种的站点剔除，避免提交后被后端拒。
    const selectable = new Set(
      siteOptions.value.filter((site) => site.can_reseed !== false).map((site) => site.nickname),
    )
    if (form.sites.some((name) => !selectable.has(name))) {
      form.sites = form.sites.filter((name) => selectable.has(name))
    }
    iyuuSiteListKnown.value = response.data?.iyuu_site_list_known !== false
    siteHiddenCount.value = Number(response.data?.site_hidden ?? 0) || 0
    pathOptions.value = Array.isArray(response.data?.paths) ? response.data.paths : []
    downloaderOptions.value = Array.isArray(response.data?.downloaders)
      ? response.data.downloaders
      : []
    // 目标下载器默认跟随右上角的全局选择（与列表页口径一致），
    // 顶部为「全部下载器」时才回落到每个种子自身的下载器。
    if (!form.downloader_id && globalDownloaderId.value) {
      const exists = downloaderOptions.value.some((item) => item.id === globalDownloaderId.value)
      if (exists) form.downloader_id = globalDownloaderId.value
    }
    running.value = Boolean(response.data?.running)

    // 页面刷新时若后台仍有任务在跑，接着轮询它的进度而不是显示成空闲。
    const currentTaskId = String(response.data?.current_task_id ?? '')
    if (running.value && currentTaskId) {
      task.value = {
        task_id: currentTaskId,
        isRunning: true,
        message: '任务在后台运行中，正在同步进度…',
      }
      startPolling()
    }
  } catch (error) {
    console.error('读取辅种状态失败:', error)
    ElMessage.error('读取辅种状态失败，请检查后端服务')
  }
}

const fetchRecords = async () => {
  recordsLoading.value = true
  try {
    const response = await axios.get('/api/reseed/records', {
      params: {
        page: filters.page,
        page_size: filters.page_size,
        target_site: filters.target_site || undefined,
        status: filters.status || undefined,
        keyword: filters.keyword || undefined,
      },
    })
    if (response.data?.success) {
      records.value = Array.isArray(response.data.data) ? response.data.data : []
      total.value = Number(response.data.total ?? 0)
      const sites = new Set<string>()
      records.value.forEach((row) => {
        if (row.target_site) sites.add(row.target_site)
      })
      recordSiteOptions.value = Array.from(sites).sort()
      collectTagOptions()
    } else {
      ElMessage.error(response.data?.message || '读取辅种记录失败')
    }
  } catch (error) {
    console.error('读取辅种记录失败:', error)
    ElMessage.error('读取辅种记录失败')
  } finally {
    recordsLoading.value = false
  }
}

const applyFilters = () => {
  filters.page = 1
  void fetchRecords()
}

const pollProgress = async () => {
  const taskId = task.value?.task_id
  if (!taskId) return
  try {
    const response = await axios.get('/api/reseed/progress', { params: { task_id: taskId } })
    if (response.data?.success && response.data.task) {
      task.value = response.data.task as ReseedTask
      running.value = Boolean(task.value.isRunning)
      if (!task.value.isRunning) {
        stopPolling()
        await fetchRecords()
        const message = task.value.message || '辅种任务已结束'
        const outcome = taskOutcome.value
        if (!task.value.success) {
          ElMessage.warning(message)
        } else if (outcome.type === 'success') {
          ElMessage.success(message)
        } else if (outcome.label === '未命中') {
          // 全零统计不是成功：用 warning 明确告诉用户「查到了，但目标站没有这个种子」。
          ElMessage.warning(message)
        } else {
          ElMessage.info(message)
        }
      }
    }
  } catch (error) {
    console.error('读取辅种进度失败:', error)
  }
}

const startPolling = () => {
  stopPolling()
  pollTimer = setInterval(() => {
    void pollProgress()
  }, POLL_INTERVAL_MS)
}

const stopPolling = () => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

const startReseed = async () => {
  if (form.sites.length === 0) {
    ElMessage.warning('请先选择目标站点')
    return
  }
  if (form.torrents.length > 0 && selectedHashes.value.length === 0) {
    ElMessage.warning('未能取到所选种子的 infohash，请重新搜索并选择种子')
    return
  }
  const pinned = form.torrents.length > 0
  if (!pinned && !downloaderFieldValue.value) {
    // 批量模式没有全局下载器限定 = 全库跑，量可能很大，先确认一次。
    try {
      await ElMessageBox.confirm(
        '右上角未选择全局下载器，本次将处理所有下载器中的种子，数量可能很多。建议先在右上角选定下载器再辅种。是否继续？',
        '未限定下载器',
        { confirmButtonText: '继续', cancelButtonText: '先去选择', type: 'warning' },
      )
    } catch {
      return
    }
  }
  starting.value = true
  try {
    const response = await axios.post('/api/reseed/run', {
      sites: form.sites,
      hashes: pinned ? selectedHashes.value : undefined,
      // 指定种子时不再叠加路径筛选，避免和种子自身保存路径冲突。
      paths: pinned ? [] : form.paths,
      downloader_id: downloaderFieldValue.value || undefined,
      limit: form.limit,
      skip_checking: form.skip_checking,
      tags: form.tags,
      tag_by_site: form.tag_by_site,
    })
    if (!response.data?.success) {
      ElMessage.error(response.data?.message || '启动辅种失败')
      return
    }
    window.localStorage.setItem(TAG_STORAGE_KEY, JSON.stringify(form.tags))
    collectTagOptions()
    task.value = {
      task_id: response.data.task_id,
      isRunning: true,
      message: response.data.message || '任务已启动',
      total: Number(response.data.total ?? 0),
      processed: 0,
      stats: {},
    }
    running.value = true
    ElMessage.success(response.data.message || '辅种任务已启动')
    startPolling()
  } catch (error: unknown) {
    const message = axios.isAxiosError(error)
      ? (error.response?.data as { message?: string } | undefined)?.message || error.message
      : error instanceof Error
        ? error.message
        : '启动辅种失败'
    ElMessage.error(message)
  } finally {
    starting.value = false
  }
}

const stopReseed = async () => {
  try {
    const response = await axios.post('/api/reseed/stop')
    if (response.data?.success) {
      ElMessage.info(response.data.message || '已请求停止')
    } else {
      ElMessage.warning(response.data?.message || '当前没有正在执行的任务')
    }
  } catch (error) {
    console.error('停止辅种失败:', error)
    ElMessage.error('停止辅种失败')
  }
}

onMounted(async () => {
  form.tags = readStoredTags()
  // 先读右上角的全局下载器，再拉状态，保证「默认目标下载器」拿到的是用户当前关注的那个。
  try {
    await globalDownloader.loadSelection()
  } catch (error) {
    console.error('读取全局下载器选择失败:', error)
  }
  // 初始化阶段不触发「跟随全局切换」的逻辑，直接记下当前值。
  lastGlobalDownloaderId = globalDownloaderId.value
  await fetchStatus()
  await fetchRecords()
})

// 右上角切换下载器即生效：弹框列表按新范围重查；
// 目标下载器若还是「跟随全局」的状态，也一起跟着改，避免挑种子的范围和加任务的下载器对不上。
let lastGlobalDownloaderId = globalDownloaderId.value
watch(globalDownloaderId, (next) => {
  const previous = lastGlobalDownloaderId
  lastGlobalDownloaderId = next
  if (!form.downloader_id || form.downloader_id === previous) {
    const exists = downloaderOptions.value.some((item) => item.id === next)
    form.downloader_id = exists ? next : ''
  }
  if (pickerVisible.value) {
    void searchPicker(1)
  }
})

onBeforeUnmount(() => {
  stopPolling()
})
</script>

<style scoped>
.reseed-view {
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  overflow-y: auto;
}

.token-alert {
  flex-shrink: 0;
}

.token-alert-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.card-header {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.card-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.config-form {
  display: flex;
  flex-wrap: wrap;
  row-gap: 4px;
}

.site-select {
  width: 320px;
}

.site-picker {
  display: flex;
  align-items: center;
  gap: 8px;
}

.tag-picker {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.path-select {
  width: 260px;
}

.torrent-picker {
  display: flex;
  align-items: center;
  gap: 8px;
}

.picker-toolbar {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.picker-search {
  width: 320px;
}

.picker-count {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.tag-select {
  width: 260px;
}

.downloader-select {
  width: 180px;
}

.limit-input {
  width: 130px;
}

.hint-icon {
  margin-left: 6px;
  color: var(--el-text-color-secondary);
  cursor: help;
}

.site-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.site-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.site-tags {
  display: flex;
  flex-shrink: 0;
  align-items: center;
  gap: 4px;
}

/* 置灰站点：文字压暗，但仍可悬停查看原因 */
.site-option.is-disabled .site-name {
  color: var(--el-text-color-disabled);
}

.site-count {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  cursor: help;
  white-space: nowrap;
}

.site-count.is-empty {
  color: var(--el-color-warning);
}

.pin-hint {
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-color-primary);
}

.scope-hint {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-top: 4px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.scope-hint.warning {
  color: var(--el-color-warning);
}

.record-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

.progress-card :deep(.el-card__body) {
  padding-top: 8px;
}

.progress-message {
  margin: 8px 0 12px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(110px, 1fr));
  gap: 10px;
}

.stat-item {
  padding: 10px 12px;
  border-radius: 8px;
  background: var(--el-fill-color-lighter);
  text-align: center;
}

.stat-value {
  font-size: 20px;
  font-weight: 600;
  line-height: 1.2;
}

.stat-label {
  margin-top: 2px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.stat-item.success .stat-value {
  color: var(--el-color-success);
}

.stat-item.warning .stat-value {
  color: var(--el-color-warning);
}

.stat-item.danger .stat-value {
  color: var(--el-color-danger);
}

.record-filters {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-left: auto;
  flex-wrap: wrap;
}

.filter {
  width: 130px;
}

.search {
  width: 200px;
}

.records-card :deep(.el-card__body) {
  padding-bottom: 8px;
}

.ellipsis {
  display: inline-block;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.pagination-row {
  display: flex;
  justify-content: flex-end;
  padding-top: 10px;
}

@media (max-width: 768px) {
  .site-select,
  .path-select,
  .tag-select,
  .downloader-select,
  .picker-search,
  .search,
  .site-picker,
  .tag-picker {
    width: 100%;
  }

  .record-filters {
    margin-left: 0;
    width: 100%;
  }
}
</style>
