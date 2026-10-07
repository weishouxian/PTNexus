<template>
  <div class="step-container results-container">
    <!-- 进度条显示 -->
    <div class="progress-section">
      <div class="progress-item" v-if="publishProgress.total > 0">
        <div class="progress-label">发布进度:</div>
        <el-progress
          :percentage="Math.round((publishProgress.current / publishProgress.total) * 100)"
          :show-text="true"
          :stroke-width="8"
        />
        <div class="progress-text">
          {{ publishProgress.current }} / {{ publishProgress.total }}
        </div>
      </div>
      <div class="progress-item" v-if="downloaderProgress.total > 0">
        <div class="progress-label">下载器添加进度:</div>
        <el-progress
          :percentage="Math.round((downloaderProgress.current / downloaderProgress.total) * 100)"
          :show-text="true"
          :stroke-width="8"
        />
        <div class="progress-text">
          {{ downloaderProgress.current }} / {{ downloaderProgress.total }}
        </div>
      </div>

      <!-- 🚫 发种限制提示 -->
      <div class="limit-alert-section" v-if="limitAlert.visible">
        <div class="limit-alert">
          <div class="limit-alert-content">
            <div class="limit-alert-title">{{ limitAlert.title }}</div>
            <div class="limit-alert-message">{{ limitAlert.message }}</div>
          </div>
        </div>
      </div>
    </div>

    <!-- 分波发布（设置了发种间隔）时的整体进度汇总 -->
    <div class="publish-wave-summary" v-if="publishWaveSummary">
      <el-icon class="wave-summary-icon"><InfoFilled /></el-icon>
      <span class="wave-summary-text">
        已发布 {{ publishWaveSummary.published }} 个站点，剩余
        {{ publishWaveSummary.remaining }} 个站点已在队列中待发布
      </span>
      <span class="wave-summary-hint">
        每 {{ publishWaveSummary.perWave }} 个站点一波<template
          v-if="publishWaveSummary.intervalMinutes > 0"
          >，波间隔 {{ publishWaveSummary.intervalMinutes }} 分钟</template
        >
      </span>
    </div>

    <div class="results-rows-container">
      <div v-for="(row, rowIndex) in groupedResults" :key="rowIndex" class="results-row">
        <div class="row-sites">
          <div
            v-for="result in row"
            :key="result.siteName"
            class="result-card"
            :class="{
              'is-success': result.displayStatus === 'success',
              'is-warning': result.displayStatus === 'warning',
              'is-error': result.displayStatus === 'error',
              'is-waiting': result.displayStatus === 'waiting',
              'is-publishing': result.displayStatus === 'publishing',
              'is-paused': result.displayStatus === 'paused',
            }"
          >
            <div class="card-icon">
              <el-icon v-if="result.displayStatus === 'success'" color="#67C23A" :size="32">
                <CircleCheckFilled />
              </el-icon>
              <el-icon v-else-if="result.displayStatus === 'warning'" color="#E6A23C" :size="32">
                <Warning />
              </el-icon>
              <el-icon v-else-if="result.displayStatus === 'error'" color="#F56C6C" :size="32">
                <CircleCloseFilled />
              </el-icon>
              <el-icon
                v-else-if="result.displayStatus === 'publishing'"
                color="#409EFF"
                :size="32"
                class="loading-icon"
              >
                <Loading />
              </el-icon>
              <el-icon v-else :color="result.displayStatus === 'paused' ? '#E6A23C' : '#FFB6C1'" :size="32">
                <Clock />
              </el-icon>
              <div v-if="result.isExisted" class="existed-tags">
                <el-tag type="warning" size="small">已存在</el-tag>
                <el-tag v-if="result.auto_edit_result?.success" type="success" size="small">已编辑</el-tag>
              </div>
            </div>
            <h4 class="card-title">{{ result.siteName }}</h4>
            <div v-if="result.displayStatus === 'waiting'" class="status-tag">
              <el-tag size="small" class="waiting-tag">{{ result.message || '等待中' }}</el-tag>
            </div>
            <div v-else-if="result.displayStatus === 'publishing'" class="status-tag">
              <el-tag type="primary" size="small">发布中</el-tag>
            </div>
            <div v-else-if="result.displayStatus === 'paused'" class="status-tag">
              <el-tag type="warning" size="small">已暂停</el-tag>
            </div>

            <div v-else-if="result.displayStatus === 'warning'" class="status-tag">
              <el-tag type="warning" size="small">添加失败</el-tag>
            </div>

            <!-- dupe 查重拦截：给出「查重地址」与「重复种子」两个可点击入口 -->
            <div v-if="result.dupe_blocked" class="dupe-blocked">
              <el-tag type="danger" size="small">dupe 发布失败</el-tag>
              <div class="dupe-links">
                <el-link
                  v-if="result.dupe_search_url"
                  type="primary"
                  :href="result.dupe_search_url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  查重地址
                </el-link>
                <el-link
                  v-if="result.dupe_torrent_url"
                  type="danger"
                  :href="result.dupe_torrent_url"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  重复种子
                </el-link>
              </div>
              <el-button
                type="danger"
                size="small"
                plain
                :loading="forceRepublishingSite === result.siteName"
                @click="forceRepublishSite(result.siteName)"
              >
                仍要发布
              </el-button>
            </div>

            <!-- 下载器添加状态（dupe 拦截时改由上方区块展示，避免被误读成「下载器添加失败」） -->
            <div class="downloader-status" v-else-if="result.downloaderStatus">
              <div class="status-icon">
                <el-icon v-if="result.downloaderStatus.success" color="#67C23A" :size="16">
                  <CircleCheckFilled />
                </el-icon>
                <el-icon v-else color="#F56C6C" :size="16">
                  <CircleCloseFilled />
                </el-icon>
              </div>
              <span
                class="status-text"
                :class="{
                  success: result.downloaderStatus.success,
                  error: !result.downloaderStatus.success,
                }"
              >
                {{
                  result.downloaderStatus.success
                    ? `种子已添加到'${result.downloaderStatus.downloaderName}'`
                    : '添加失败'
                }}
              </span>
            </div>

            <!-- 操作按钮 -->
            <div class="card-extra">
              <el-button type="primary" size="small" @click="showSiteLog(result.siteName, result.logs)">
                查看日志
              </el-button>
              <a
                v-if="result.success && result.url"
                :href="filterUploadedParam(result.url)"
                target="_blank"
                rel="noopener noreferrer"
                style="transform: translateY(-1px)"
              >
                <el-button type="success" size="small"> 查看种子 </el-button>
              </a>
            </div>
          </div>
        </div>
        <div class="row-action">
          <el-button
            type="warning"
            :icon="Refresh"
            size="large"
            @click="openAllSitesInRow(row)"
            :disabled="!hasValidUrlsInRow(row)"
            class="open-all-button"
          >
            <div class="button-subtitle">打开{{ getValidUrlsCount(row) }}个站点</div>
          </el-button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import {
  CircleCheckFilled,
  CircleCloseFilled,
  Clock,
  InfoFilled,
  Loading,
  Refresh,
  Warning,
} from '@element-plus/icons-vue'
import { useCrossSeedPanelContext } from './crossSeedPanelContext'

const {
  publishProgress,
  downloaderProgress,
  limitAlert,
  publishWaveSummary,
  groupedResults,
  showSiteLog,
  filterUploadedParam,
  hasValidUrlsInRow,
  openAllSitesInRow,
  getValidUrlsCount,
  forceRepublishSite,
  forceRepublishingSite,
} = useCrossSeedPanelContext()
</script>

