<template>
  <div class="menu-visibility-settings">
    <div class="settings-card glass-card glass-rounded glass-transparent-header glass-transparent-body">
      <div class="card-header">
        <div class="header-content">
          <el-icon class="header-icon">
            <Menu />
          </el-icon>
          <h3>菜单显示</h3>
        </div>
        <div class="header-actions">
          <el-button size="small" :loading="loading" @click="loadVisibility">重新载入</el-button>
          <el-button type="primary" size="small" :loading="saving" @click="saveVisibility">
            保存
          </el-button>
        </div>
      </div>

      <div class="card-content">
        <el-alert
          type="info"
          :closable="false"
          show-icon
          class="visibility-tip"
        >
          只控制导航菜单是否展示，不影响直接访问页面链接。「设置」菜单会始终保留，避免隐藏后无法恢复。
          「自动发种」需部署环境设置 <code>PTNEXUS_SHOW_AUTO_SEED=true</code> 才会出现在本列表，
          并在此处开启后才会显示在导航菜单里。
        </el-alert>

        <div class="visibility-list" v-loading="loading">
          <div v-for="item in visibleMenuItems" :key="item.key" class="visibility-row">
            <div class="visibility-label">
              <span class="menu-name">{{ item.label }}</span>
              <code class="menu-path">{{ item.path }}</code>
            </div>
            <el-switch
              v-model="visibility[item.key]"
              :disabled="item.key === 'settings'"
              :inline-prompt="false"
              active-text="显示"
              inactive-text="隐藏"
            />
          </div>
        </div>

        <div class="footer-actions">
          <el-button size="small" @click="showAll">全部显示</el-button>
          <el-button size="small" @click="restoreDefaults">恢复默认</el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import axios from 'axios'
import { Menu } from '@element-plus/icons-vue'
import { ElMessage } from '@/utils/uiNotify'

interface MenuItem {
  key: string
  label: string
  path: string
}

// 顺序与顶部导航一致；settings 只展示不开放开关。
const menuItems: MenuItem[] = [
  { key: 'home', label: '首页', path: '/' },
  { key: 'info', label: '流量统计', path: '/info' },
  { key: 'torrents', label: '一种多站', path: '/torrents' },
  { key: 'reseed', label: '辅种', path: '/reseed' },
  { key: 'scheduled_seeding', label: '定时发种', path: '/scheduled-seeding' },
  { key: 'auto_seed', label: '自动发种', path: '/auto-seed' },
  { key: 'publish_progress', label: '下载器发布进度', path: '/publish-progress' },
  { key: 'publish_logs', label: '发种日志', path: '/publish-logs' },
  { key: 'sites', label: '做种检索', path: '/sites' },
  { key: 'site_data', label: '站点数据', path: '/site-data' },
  { key: 'resource_info', label: '资源信息', path: '/resource-info' },
  { key: 'bangumi', label: '番组数据', path: '/bangumi' },
  { key: 'settings', label: '设置', path: '/settings' },
]

const visibility = reactive<Record<string, boolean>>({})
// envFlags[key] === false 表示该菜单项由部署环境关闭：导航不显示，本列表里也不出现。
const envFlags = ref<Record<string, boolean>>({})
const loading = ref(false)
const saving = ref(false)

const visibleMenuItems = computed(() =>
  menuItems.filter((item) => envFlags.value[item.key] !== false),
)

const defaultValue = (key: string) => key !== 'auto_seed' && key !== 'settings'

const loadVisibility = async () => {
  loading.value = true
  try {
    const response = await axios.get('/api/config/menu_visibility')
    const saved = response.data?.data ?? {}
    const env = response.data?.env
    envFlags.value = env && typeof env === 'object' ? { ...env } : {}
    for (const item of visibleMenuItems.value) {
      visibility[item.key] = item.key === 'settings' ? true : saved[item.key] ?? defaultValue(item.key)
    }
  } catch (error) {
    console.error('加载菜单显示配置失败:', error)
    ElMessage.error('加载菜单显示配置失败')
  } finally {
    loading.value = false
  }
}

const showAll = () => {
  for (const item of visibleMenuItems.value) {
    visibility[item.key] = true
  }
}

const restoreDefaults = () => {
  for (const item of visibleMenuItems.value) {
    visibility[item.key] = defaultValue(item.key) || item.key === 'settings'
  }
}

const saveVisibility = async () => {
  saving.value = true
  try {
    await axios.post('/api/config/menu_visibility', {
      items: { ...visibility },
    })
    ElMessage.success('菜单显示设置已保存')
    window.dispatchEvent(new CustomEvent('ptn:menu-visibility-changed'))
  } catch (error) {
    console.error('保存菜单显示配置失败:', error)
    ElMessage.error('保存菜单显示配置失败')
  } finally {
    saving.value = false
  }
}

onMounted(() => {
  void loadVisibility()
})
</script>

<style scoped>
.menu-visibility-settings {
  padding: 20px;
  height: 100%;
  overflow: auto;
}

.settings-card {
  max-width: 860px;
}

.card-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 20px;
}

.header-content {
  display: flex;
  align-items: center;
  gap: 8px;
}

.header-icon {
  font-size: 18px;
  color: var(--el-color-primary);
}

.header-content h3 {
  margin: 0;
  font-size: 16px;
}

.header-actions {
  display: flex;
  gap: 8px;
}

.card-content {
  padding: 18px 20px 22px;
}

.visibility-tip {
  margin-bottom: 16px;
}

.visibility-list {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 8px;
  overflow: hidden;
}

.visibility-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  min-height: 48px;
  padding: 8px 14px;
}

.visibility-row + .visibility-row {
  border-top: 1px solid var(--el-border-color-lighter);
}

.visibility-label {
  display: flex;
  align-items: center;
  gap: 12px;
  min-width: 0;
}

.menu-name {
  font-size: 14px;
}

.menu-path {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.footer-actions {
  display: flex;
  gap: 8px;
  margin-top: 16px;
}

@media (max-width: 768px) {
  .menu-visibility-settings {
    padding: 12px;
  }

  .card-header {
    flex-direction: column;
    align-items: flex-start;
  }

  .menu-path {
    display: none;
  }
}
</style>
