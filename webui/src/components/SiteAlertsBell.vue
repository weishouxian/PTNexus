<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import { Bell } from '@element-plus/icons-vue'
import { ElMessageBox } from 'element-plus'
import axios from 'axios'
import { ElMessage } from '@/utils/uiNotify'

type SiteAlertItem = {
  id: number
  site_code: string
  nickname: string
  level: string
  message: string
  read: boolean
  created_at: string
}

const API_BASE_URL = '/api'

const siteAlerts = ref<SiteAlertItem[]>([])
const siteAlertsUnread = ref(0)

// 是否已登录（未登录时不该去拉提醒接口，避免 401）。
const hasToken = () => Boolean(localStorage.getItem('token'))

const fetchSiteAlerts = async () => {
  if (!hasToken()) return
  try {
    const response = await axios.get(`${API_BASE_URL}/sites/alerts`)
    siteAlerts.value = Array.isArray(response.data?.alerts) ? response.data.alerts : []
    siteAlertsUnread.value = Number(response.data?.unread ?? 0)
  } catch {
    // 忽略，铃铛仍可点开但可能为空。
  }
}

const handleMarkAlertsRead = async () => {
  try {
    await axios.post(`${API_BASE_URL}/sites/alerts/read`)
    siteAlerts.value = siteAlerts.value.map((a) => ({ ...a, read: true }))
    siteAlertsUnread.value = 0
  } catch {
    ElMessage.error('标记已读失败。')
  }
}

const handleClearAlerts = async () => {
  try {
    await ElMessageBox.confirm('确定清空全部站点提醒？此操作不可恢复。', '清空提醒', {
      confirmButtonText: '清空',
      cancelButtonText: '取消',
      type: 'warning',
    })
  } catch {
    return
  }
  try {
    await axios.post(`${API_BASE_URL}/sites/alerts/clear`)
    siteAlerts.value = []
    siteAlertsUnread.value = 0
  } catch {
    ElMessage.error('清空提醒失败。')
  }
}

// 级别 → tag 类型 / 文案。
const levelTagType = (level: string) => {
  if (level === 'error') return 'danger'
  if (level === 'info') return 'info'
  return 'warning'
}
const levelLabel = (level: string) => {
  if (level === 'error') return '错误'
  if (level === 'info') return '汇总'
  return '警告'
}
// 汇总提醒（site_code 为空）不显示站点名。
const isSummary = (alert: SiteAlertItem) => !alert.site_code

// 定时轮询：每 60 秒刷新一次未读数，让用户及时看到新提醒。
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  void fetchSiteAlerts()
  pollTimer = setInterval(() => {
    void fetchSiteAlerts()
  }, 60_000)
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})
</script>

<template>
  <el-popover placement="bottom-end" :width="360" trigger="click" @show="fetchSiteAlerts">
    <template #reference>
      <el-badge :value="siteAlertsUnread" :hidden="siteAlertsUnread === 0" :max="99">
        <el-button :icon="Bell" circle class="site-alerts-bell" />
      </el-badge>
    </template>
    <div class="site-alert-popover">
      <div class="site-alert-popover__head">
        <span>站点提醒</span>
        <div class="site-alert-popover__actions">
          <el-link
            v-if="siteAlerts.length"
            type="primary"
            :underline="false"
            @click="handleMarkAlertsRead"
          >
            全部已读
          </el-link>
          <el-link
            v-if="siteAlerts.length"
            type="danger"
            :underline="false"
            @click="handleClearAlerts"
          >
            清空
          </el-link>
        </div>
      </div>
      <el-empty v-if="!siteAlerts.length" description="暂无提醒" :image-size="60" />
      <div v-else class="site-alert-list">
        <div
          v-for="alert in siteAlerts"
          :key="alert.id"
          class="site-alert-item"
          :class="{ 'is-unread': !alert.read, 'is-summary': isSummary(alert) }"
        >
          <div class="site-alert-item__title">
            <span class="site-alert-item__name">
              {{ isSummary(alert) ? '检测汇总' : alert.nickname || alert.site_code }}
            </span>
            <el-tag :type="levelTagType(alert.level)" size="small">
              {{ levelLabel(alert.level) }}
            </el-tag>
          </div>
          <div class="site-alert-item__msg">{{ alert.message }}</div>
          <div class="site-alert-item__time">{{ alert.created_at }}</div>
        </div>
      </div>
    </div>
  </el-popover>
</template>

<style scoped>
.site-alerts-bell {
  width: 32px;
  height: 32px;
  padding: 0;
}

.site-alert-popover__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
  font-weight: 600;
  font-size: 13px;
  color: #303133;
}

.site-alert-popover__actions {
  display: flex;
  align-items: center;
  gap: 10px;
}

.site-alert-popover__actions :deep(.el-link__inner) {
  font-size: 12px;
}

.site-alert-list {
  max-height: 360px;
  overflow-y: auto;
}

.site-alert-item {
  padding: 8px 10px;
  border-radius: 6px;
  margin-bottom: 6px;
  background: #f5f7fa;
}

.site-alert-item.is-unread {
  background: #ecf5ff;
  border-left: 3px solid var(--el-color-primary, #409eff);
}

.site-alert-item.is-summary {
  background: #fdf6ec;
}

.site-alert-item.is-summary.is-unread {
  border-left: 3px solid var(--el-color-warning, #e6a23c);
}

.site-alert-item__title {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 4px;
}

.site-alert-item__name {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.site-alert-item__msg {
  font-size: 12px;
  color: #606266;
  line-height: 1.5;
  word-break: break-all;
}

.site-alert-item__time {
  font-size: 11px;
  color: #a8abb2;
  margin-top: 4px;
}
</style>
