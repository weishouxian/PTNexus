<template>
  <el-dialog
    :model-value="visible"
    :title="isEdit ? '编辑任务' : '新建定时发种任务'"
    width="750px"
    :close-on-click-modal="false"
    @update:model-value="$emit('update:visible', $event)"
    @open="handleOpen"
  >
    <el-form
      ref="formRef"
      :model="form"
      :rules="formRules"
      label-width="100px"
      label-position="right"
    >
      <el-form-item v-if="isEdit" label="任务名称" prop="name">
        <el-input v-model="form.name" placeholder="请输入任务名称" clearable />
      </el-form-item>

      <!-- 种子选择：按钮触发弹窗 -->
      <el-form-item label="种子选择" prop="seeds">
        <div class="seed-select-area">
          <el-button type="primary" plain @click="seedDialogVisible = true">
            选择种子
            <el-tag v-if="form.seeds.length > 0" type="success" size="small" style="margin-left: 6px">
              {{ form.seeds.length }}
            </el-tag>
          </el-button>
          <span v-if="form.seeds.length > 0" class="seed-summary">
            已选 {{ form.seeds.length }} 个种子
          </span>
        </div>
        <!-- 已选种子预览列表 -->
        <div v-if="form.seeds.length > 0" class="selected-seeds-preview">
          <div
            v-for="(seed, index) in form.seeds"
            :key="`sel-${seed.site_name}-${seed.torrent_id}`"
            class="selected-seed-item"
          >
            <div class="selected-seed-info">
              <el-tag size="small" effect="plain">{{ seed.site_name }}</el-tag>
              <span class="selected-seed-title" :title="seed.title">{{ seed.title }}</span>
            </div>
            <el-button size="small" type="danger" text @click="removeSeed(index)">移除</el-button>
          </div>
        </div>
      </el-form-item>

      <!-- 目标站点：按钮网格选择 -->
      <el-form-item label="目标站点" prop="target_sites">
        <div class="site-selection-area">
          <div class="site-selection-toolbar">
            <el-button-group>
              <el-button size="small" type="primary" @click="selectAllSites">全选</el-button>
              <el-button size="small" type="info" @click="clearAllSites">清空</el-button>
            </el-button-group>
            <span class="site-selection-hint">
              仅列出「可发种 + 目标站/源站·目标站 + 已配置 Cookie 或 Passkey」的站点（共 {{ siteList.length }} 个）
            </span>
          </div>
          <SiteTagChips
            :sites="tagSiteItems"
            :selected="form.target_sites"
            @apply="applyTagSites"
            @remove="removeTagSites"
          />
          <div class="site-buttons-group" v-loading="sitesLoading">
            <el-button
              v-for="site in siteList"
              :key="site.name"
              class="site-button"
              :type="form.target_sites.includes(site.name) ? 'success' : 'default'"
              :disabled="!isUsableTargetSite(site)"
              @click="toggleSite(site.name)"
            >
              {{ site.name }}
            </el-button>
            <div v-if="siteList.length === 0 && !sitesLoading" class="site-empty">
              {{
                siteStatusLoaded
                  ? '暂无可用目标站点（需为可发种的目标站，且已配置 Cookie 或 Passkey）'
                  : '站点状态获取失败，请关闭弹窗后重试'
              }}
            </div>
          </div>
          <div v-if="unavailableSelectedSites.length" class="site-selection-warning">
            <span class="site-selection-warning-text">
              以下已选站点当前不满足上述条件，发布时会失败，建议移除：
            </span>
            <el-tag
              v-for="name in unavailableSelectedSites"
              :key="name"
              type="danger"
              size="small"
              closable
              @close="removeSite(name)"
            >
              {{ name }}
            </el-tag>
          </div>
        </div>
      </el-form-item>

      <el-form-item label="发种间隔" prop="interval_minutes">
        <div class="interval-input-group">
          <el-input-number
            v-model="intervalValue"
            :min="1"
            :max="9999"
            controls-position="right"
            style="width: 160px"
          />
          <el-select v-model="intervalUnit" style="width: 100px; margin-left: 8px">
            <el-option label="分钟" value="minutes" />
            <el-option label="小时" value="hours" />
          </el-select>
        </div>
      </el-form-item>

      <el-form-item label="开始时间">
        <el-date-picker
          v-model="form.start_time"
          type="datetime"
          placeholder="留空则立即开始"
          format="YYYY-MM-DD HH:mm"
          value-format="YYYY-MM-DD HH:mm:ss"
          style="width: 220px"
        />
        <span class="form-hint">留空则以当前时间作为发种开始时间</span>
      </el-form-item>

      <el-form-item label="循环发种">
        <el-switch v-model="form.loop_enabled" />
        <span class="form-hint">开启后，所有种子发布完毕将自动从头开始</span>
      </el-form-item>
    </el-form>

    <template #footer>
      <el-button @click="$emit('update:visible', false)">取消</el-button>
      <el-button type="primary" :loading="submitting" @click="handleSubmit">
        {{ isEdit ? '保存' : '创建' }}
      </el-button>
    </template>
  </el-dialog>

  <!-- 种子选择弹窗 -->
  <SeedSelectDialog
    v-model:visible="seedDialogVisible"
    :initial-selection="form.seeds"
    @confirm="handleSeedConfirm"
  />
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import type { FormInstance, FormRules } from 'element-plus'
import axios from 'axios'
import { ElMessage } from '@/utils/uiNotify'
import SiteTagChips from '@/components/SiteTagChips.vue'
import SeedSelectDialog from './SeedSelectDialog.vue'
import { loadSelectedTargetSites, saveSelectedTargetSites } from '@/utils/targetSites'

type SeedItem = {
  torrent_id: string
  site_name: string
  title: string
}

type TaskData = {
  id: number
  name: string
  status: string
  seeds_json: string
  target_sites_json: string
  interval_minutes: number
  loop_enabled: boolean
  [key: string]: unknown
}

const props = defineProps<{
  visible: boolean
  task: TaskData | null
}>()

const emit = defineEmits<{
  'update:visible': [value: boolean]
  saved: []
}>()

const isEdit = computed(() => !!props.task)

const formRef = ref<FormInstance | null>(null)

type FormModel = {
  name: string
  seeds: SeedItem[]
  target_sites: string[]
  interval_minutes: number
  loop_enabled: boolean
  start_time: string | null
}

const form = reactive<FormModel>({
  name: '',
  seeds: [],
  target_sites: [],
  interval_minutes: 30,
  loop_enabled: false,
  start_time: null,
})

const intervalValue = ref(30)
const intervalUnit = ref('minutes')

watch([intervalValue, intervalUnit], () => {
  form.interval_minutes = intervalUnit.value === 'hours'
    ? intervalValue.value * 60
    : intervalValue.value
})

const generateTimeBasedName = () => {
  const now = new Date()
  const pad = (n: number, w = 2) => String(n).padStart(w, '0')
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`
}

const formRules: FormRules = {
  seeds: [
    {
      validator: (_rule, _value, callback) => {
        if (form.seeds.length === 0) {
          callback(new Error('请至少选择一个种子'))
        } else {
          callback()
        }
      },
      trigger: 'change',
    },
  ],
  target_sites: [
    {
      validator: (_rule, _value, callback) => {
        if (form.target_sites.length === 0) {
          callback(new Error('请至少选择一个目标站点'))
        } else {
          callback()
        }
      },
      trigger: 'change',
    },
  ],
  interval_minutes: [
    {
      validator: (_rule, _value, callback) => {
        if (form.interval_minutes < 1) {
          callback(new Error('发种间隔不能小于 1 分钟'))
        } else {
          callback()
        }
      },
      trigger: 'change',
    },
  ],
}

// ---- 种子选择弹窗 ----
const seedDialogVisible = ref(false)

const handleSeedConfirm = (seeds: SeedItem[]) => {
  form.seeds = seeds
}

const removeSeed = (index: number) => {
  form.seeds.splice(index, 1)
}

// ---- 目标站点按钮网格 ----
type SiteItem = {
  name: string
  can_publish: boolean
  /** 角色含「目标站」（migration = 2 或 3） */
  is_target: boolean
  /** 已配置 Cookie */
  has_cookie: boolean
  /** 已配置 Passkey */
  has_passkey: boolean
  tags: string[]
}
const siteList = ref<SiteItem[]>([])
const sitesLoading = ref(false)
/** 站点状态是否取到（用于区分「确实没有可用站点」与「状态接口失败」） */
const siteStatusLoaded = ref(false)

// 定时发种的目标站必须同时满足三条：
//   ① 可发种（can_publish）② 角色含「目标站」（migration 2 或 3）③ 已配置 Cookie 或 Passkey。
// 少了任意一条，任务跑起来都会卡在发布阶段（站点不接受发布 / 没有凭据无法上传），
// 所以这里直接把它们挡在可选清单之外，而不是列出来再让用户踩坑。
const isUsableTargetSite = (site: SiteItem): boolean =>
  site.can_publish && site.is_target && (site.has_cookie || site.has_passkey)

// 已选中、但当前不在可用清单里的站点（历史任务遗留，或站点配置后来被改掉）。
// 不静默丢弃 —— 保存时仍会带上它们，所以这里显式列出来让用户决定是否移除。
const unavailableSelectedSites = computed(() =>
  form.target_sites.filter((name) => !siteList.value.some((site) => site.name === name)),
)

// 传给标签快捷选择组件的站点清单（不可用 => disabled）。
const tagSiteItems = computed(() =>
  siteList.value.map((site) => ({
    name: site.name,
    tags: site.tags,
    disabled: !isUsableTargetSite(site),
  })),
)

// 点击标签：把该标签下所有站点追加到已选（不清掉已有选择）。
const applyTagSites = (names: string[]) => {
  if (!names.length) return
  const merged = new Set(form.target_sites)
  names.forEach((name) => merged.add(name))
  form.target_sites = Array.from(merged)
  saveSelectedTargetSites(form.target_sites)
}

// 再次点击已全选的标签：把这批站点移出已选，其余选择保留。
const removeTagSites = (names: string[]) => {
  if (!names.length) return
  const dropped = new Set(names)
  form.target_sites = form.target_sites.filter((name) => !dropped.has(name))
  saveSelectedTargetSites(form.target_sites)
}

const toggleSite = (site: string) => {
  const idx = form.target_sites.indexOf(site)
  if (idx > -1) {
    form.target_sites.splice(idx, 1)
  } else {
    form.target_sites.push(site)
  }
  saveSelectedTargetSites(form.target_sites)
}

// 从上一步「不可用已选站点」里移除（语义明确，不必借用 toggleSite 的取反逻辑）
const removeSite = (site: string) => {
  form.target_sites = form.target_sites.filter((name) => name !== site)
  saveSelectedTargetSites(form.target_sites)
}

const selectAllSites = () => {
  form.target_sites = siteList.value.map((s) => s.name)
  saveSelectedTargetSites(form.target_sites)
}

const clearAllSites = () => {
  form.target_sites = []
  saveSelectedTargetSites(form.target_sites)
}

const fetchSiteList = async () => {
  sitesLoading.value = true
  try {
    const [listRes, statusRes] = await Promise.all([
      axios.get('/api/sites_list'),
      axios.get('/api/sites/status'),
    ])

    // /api/sites/status 是站点可用性的真源：能否发种、角色是否含目标、有没有凭据、标签。
    type SiteStatus = {
      canPublish: boolean
      isTarget: boolean
      hasCookie: boolean
      hasPasskey: boolean
      tags: string[]
    }
    const statusMap = new Map<string, SiteStatus>()
    if (Array.isArray(statusRes.data)) {
      for (const s of statusRes.data) {
        const name = String(s?.name || '').trim()
        if (!name) continue
        statusMap.set(name, {
          canPublish: s.can_publish !== false,
          isTarget: s.is_target === true,
          hasCookie: s.has_cookie === true,
          hasPasskey: s.has_passkey === true,
          tags: Array.isArray(s.tags) ? s.tags : [],
        })
      }
    }
    siteStatusLoaded.value = statusMap.size > 0

    const toSiteItem = (name: string): SiteItem => {
      const status = statusMap.get(name)
      return {
        name,
        can_publish: status?.canPublish ?? false,
        is_target: status?.isTarget ?? false,
        has_cookie: status?.hasCookie ?? false,
        has_passkey: status?.hasPasskey ?? false,
        tags: status?.tags ?? [],
      }
    }

    // /api/sites_list 返回 { source_sites: [...], target_sites: [...] }。
    // target_sites 已按角色筛过（migration 2/3），但还可能缺凭据或不可发种，
    // 所以这里再用 isUsableTargetSite 收紧一层 —— 见该函数的注释。
    // 列表项可能是站点昵称字符串，也可能是带 site/nickname 字段的对象，两种都兼容。
    const pickSiteName = (raw: unknown, keys: string[]): string => {
      if (typeof raw === 'string') return raw
      if (raw && typeof raw === 'object') {
        const record = raw as Record<string, unknown>
        for (const key of keys) {
          const value = record[key]
          if (typeof value === 'string' && value.trim() !== '') return value
        }
      }
      return ''
    }

    const rawNames: string[] = Array.isArray(listRes.data?.target_sites)
      ? listRes.data.target_sites.map((s: unknown) => pickSiteName(s, ['site', 'nickname']))
      : Array.isArray(listRes.data)
        ? listRes.data.map((s: unknown) => pickSiteName(s, ['name']))
        : []

    const usableSites = rawNames
      .map((name) => name.trim())
      .filter((name) => name !== '')
      .map(toSiteItem)
      .filter(isUsableTargetSite)
    // 站点昵称理论上唯一，这里仍按名字去重：重复项会让 v-for 的 key 冲突
    siteList.value = Array.from(new Map(usableSites.map((site) => [site.name, site])).values())

    // 新建任务且尚未手动选过站点时，带出「最近一次选择」的目标站点记忆（与批量发种共享同一份），
    // 并只保留当前可用清单里的站点，避免带出失效选择。
    if (!props.task && form.target_sites.length === 0) {
      const remembered = loadSelectedTargetSites()
      if (remembered.length > 0) {
        const available = new Set(siteList.value.map((site) => site.name))
        form.target_sites = remembered.filter((name) => available.has(name))
      }
    }
  } catch (e) {
    console.error('获取站点列表失败:', e)
    siteStatusLoaded.value = false
  } finally {
    sitesLoading.value = false
  }
}

// ---- 表单生命周期 ----
const resetForm = () => {
  form.name = generateTimeBasedName()
  form.seeds = []
  form.target_sites = []
  form.interval_minutes = 30
  form.loop_enabled = false
  form.start_time = null
  intervalValue.value = 30
  intervalUnit.value = 'minutes'
}

const populateFormFromTask = (task: TaskData) => {
  form.name = task.name
  try {
    const seeds = JSON.parse(task.seeds_json)
    form.seeds = Array.isArray(seeds) ? seeds : []
  } catch {
    form.seeds = []
  }
  try {
    const sites = JSON.parse(task.target_sites_json)
    form.target_sites = Array.isArray(sites) ? sites : []
  } catch {
    form.target_sites = []
  }
  form.interval_minutes = task.interval_minutes
  form.loop_enabled = task.loop_enabled

  if (task.interval_minutes >= 60 && task.interval_minutes % 60 === 0) {
    intervalValue.value = task.interval_minutes / 60
    intervalUnit.value = 'hours'
  } else {
    intervalValue.value = task.interval_minutes
    intervalUnit.value = 'minutes'
  }
}

const handleOpen = () => {
  resetForm()
  if (props.task) {
    populateFormFromTask(props.task)
  }
  fetchSiteList()
}

const submitting = ref(false)

const handleSubmit = async () => {
  if (!formRef.value) return
  try {
    await formRef.value.validate()
  } catch {
    return
  }

  submitting.value = true
  try {
    const payload = {
      name: form.name,
      seeds: form.seeds.map((s) => ({
        torrent_id: s.torrent_id,
        site_name: s.site_name,
        title: s.title,
      })),
      target_sites: form.target_sites,
      interval_minutes: form.interval_minutes,
      loop_enabled: form.loop_enabled,
      ...(form.start_time ? { start_time: form.start_time } : {}),
    }

    let response
    if (isEdit.value && props.task) {
      response = await axios.put(`/api/scheduled-seed/tasks/${props.task.id}`, payload)
    } else {
      response = await axios.post('/api/scheduled-seed/tasks', payload)
    }

    if (!response.data?.success) {
      throw new Error(response.data?.message || '操作失败')
    }

    ElMessage.success(isEdit.value ? '任务已更新' : '任务已创建')
    emit('update:visible', false)
    emit('saved')
  } catch (e: unknown) {
    const message = axios.isAxiosError(e)
      ? ((e.response?.data as { message?: string; error?: string } | undefined)?.message ||
        (e.response?.data as { error?: string } | undefined)?.error ||
        e.message)
      : e instanceof Error
        ? e.message
        : '操作失败'
    ElMessage.error(message)
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.seed-select-area {
  display: flex;
  align-items: center;
  gap: 12px;
}

.seed-summary {
  font-size: 13px;
  color: #606266;
}

.selected-seeds-preview {
  margin-top: 10px;
  max-height: 180px;
  overflow-y: auto;
  border: 1px solid #ebeef5;
  border-radius: 4px;
}

.selected-seed-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 6px 10px;
  border-bottom: 1px solid #f0f0f0;
  font-size: 13px;
}

.selected-seed-item:last-child {
  border-bottom: none;
}

.selected-seed-info {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  flex: 1;
}

.selected-seed-title {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: #303133;
}

/* 站点按钮网格 */
.site-selection-area {
  width: 100%;
}

.site-selection-toolbar {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
  margin-bottom: 10px;
}

.site-selection-hint {
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
}

.site-selection-warning {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid #fde2e2;
  border-radius: 4px;
  background-color: #fef0f0;
}

.site-selection-warning-text {
  color: #f56c6c;
  font-size: 12px;
  line-height: 1.5;
}

.site-buttons-group {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  min-height: 40px;
}

.site-button {
  min-width: 100px;
}

.site-empty {
  color: #909399;
  font-size: 13px;
  padding: 12px 0;
}

.interval-input-group {
  display: flex;
  align-items: center;
}

.form-hint {
  margin-left: 12px;
  font-size: 12px;
  color: #909399;
}
</style>
