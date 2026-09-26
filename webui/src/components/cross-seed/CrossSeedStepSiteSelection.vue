<template>
  <div class="step-container site-selection-container">
    <h3 class="selection-title">请选择要发布的目标站点</h3>
    <p class="selection-subtitle">
      {{
        autoUpdateExistingTorrent
          ? '已存在的站点可被选择并更新。红色站点表示配置不完整。'
          : '已存在的站点已被自动禁用。红色站点表示配置不完整。'
      }}
    </p>

    <!-- 禁止转载警告 -->
    <el-alert
      v-if="isUbitsDisabled"
      type="error"
      :closable="false"
      style="width: 410px; margin: 0 auto"
    >
      <template #title>
        <span style="font-weight: 600">禁止转载</span>
      </template>
      <div>
        检测到制作组包含禁止转载的内容，已自动禁用 UBits 站点。<br />
        禁止转载的制作组：CMCT、CMCTV、HDSky、HDSWEB、HDS、HDSTV、HDSPad
      </div>
    </el-alert>

    <div class="select-all-container" style="margin-top: 16px">
      <div class="site-selection-toolbar">
        <div class="toolbar-button-row">
          <el-button-group>
            <el-button type="primary" @click="selectAllTargetSites">全选</el-button>
            <el-button type="info" @click="clearAllTargetSites">清空</el-button>
          </el-button-group>
        </div>
        <div class="toolbar-toggle-row">
          <div class="toolbar-toggle-item">
            <span class="toolbar-toggle-text">目标站点已存在时是否添加到下载器</span>
            <el-switch v-model="autoAddExistingToDownloader" @change="saveAutoAddExistingSetting" />
          </div>
          <div class="toolbar-toggle-item">
            <span class="toolbar-toggle-text auto-update-toggle-text"
              >目标站点已存在时是否更新种子信息</span
            >
            <el-switch
              v-model="autoUpdateExistingTorrent"
              @change="saveAutoUpdateExistingTorrentSetting"
            />
          </div>
        </div>
        <div class="toolbar-toggle-row toolbar-interval-row">
          <div class="toolbar-toggle-item">
            <el-tooltip placement="top" :hide-after="0">
              <template #content>
                <div style="max-width: 320px; line-height: 1.6">
                  同一条种子发往多个目标站时的错峰节奏。<br />
                  <b>填 0</b>：沿用「设置 → 下载器」里该下载器的发布节奏（默认行为）。<br />
                  <b>填大于 0</b>：本次发布按此间隔错峰，每 N 分钟发一波；每波发几个站仍取该下载器的「并发数」。<br />
                  对「立即发布」与「加入队列」都生效。
                </div>
              </template>
              <span class="toolbar-toggle-text interval-label">发种间隔时间</span>
            </el-tooltip>
            <el-input-number
              v-model="publishIntervalMinutes"
              :min="0"
              :max="1440"
              :step="1"
              size="small"
              controls-position="right"
              class="interval-input"
            />
            <span class="toolbar-toggle-text">分钟</span>
            <span v-if="publishIntervalMinutes > 0" class="interval-hint">
              已覆盖下载器节奏（每 {{ publishIntervalMinutes }} 分钟一波）
            </span>
          </div>
        </div>
      </div>
    </div>
    <div class="site-buttons-group">
      <el-button
        v-for="site in allSitesStatus.filter((s) => s.is_target)"
        :key="site.name"
        class="site-button"
        :type="getButtonType(site)"
        :plain="!site.has_cookie && site.name !== '肉丝'"
        :disabled="site.can_publish === false || !isTargetSiteSelectable(site.name)"
        @click="toggleSiteSelection(site.name)"
      >
        <span
          :class="{
            'site-name-highlight': isAutoUpdateHighlightSite(site),
            'site-name-highlight-selected':
              isAutoUpdateHighlightSite(site) && selectedTargetSites.includes(site.name),
          }"
          >{{ site.name }}</span
        >
        <el-tooltip
          v-if="site.can_publish === false"
          content="该站点已设置为不可发种，请在站点管理中修改"
          placement="top"
        >
          <el-icon style="margin-left: 4px; color: #909399">
            <InfoFilled />
          </el-icon>
        </el-tooltip>
        <el-tooltip
          v-else-if="site.name === 'ubits' && !isTargetSiteSelectable(site.name)"
          content="该制作组禁止转载到 uBits 站点"
          placement="top"
        >
          <el-icon style="margin-left: 4px; color: #f56c6c">
            <InfoFilled />
          </el-icon>
        </el-tooltip>
        <el-tooltip
          v-else-if="isTransferForbidden(site.name)"
          content="该种子的官种站已禁止转种到此站点"
          placement="top"
        >
          <el-icon style="margin-left: 4px; color: #f56c6c">
            <InfoFilled />
          </el-icon>
        </el-tooltip>
        <el-tooltip
          v-else-if="isIloliconSite(site) && !isCurrentSeedAnimationRelated"
          content="ilolicon 仅支持动漫/动画内容，当前种子已自动禁用"
          placement="top"
        >
          <el-icon style="margin-left: 4px; color: #f56c6c">
            <InfoFilled />
          </el-icon>
        </el-tooltip>
      </el-button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { InfoFilled } from '@element-plus/icons-vue'
import { useCrossSeedPanelContext } from './crossSeedPanelContext'

const {
  autoUpdateExistingTorrent,
  isUbitsDisabled,
  autoAddExistingToDownloader,
  saveAutoAddExistingSetting,
  saveAutoUpdateExistingTorrentSetting,
  selectAllTargetSites,
  clearAllTargetSites,
  allSitesStatus,
  getButtonType,
  isTargetSiteSelectable,
  isTransferForbidden,
  toggleSiteSelection,
  isAutoUpdateHighlightSite,
  selectedTargetSites,
  publishIntervalMinutes,
  isIloliconSite,
  isCurrentSeedAnimationRelated,
} = useCrossSeedPanelContext()
</script>
