<template>
  <div class="site-data-view">
    <div class="page-header glass-card glass-rounded">
      <div class="header-main">
        <h2 class="page-title">站点数据</h2>
        <div class="header-actions">
          <el-tooltip :content="scheduleSummary" placement="bottom">
            <el-button :icon="AlarmClock" @click="openSchedule">
              定时刷新{{ scheduleForm.enabled ? ` · ${scheduleTimesLabel}` : '' }}
            </el-button>
          </el-tooltip>
          <el-button type="primary" :icon="Refresh" :loading="refreshing" @click="refreshAll">
            {{ refreshing ? '正在抓取站点数据…' : '刷新数据' }}
          </el-button>
        </div>
      </div>
    </div>

    <el-tabs v-model="activeTab" class="data-tabs">
      <el-tab-pane label="账号累计数据" name="cumulative">
        <div class="tab-pane-body">
          <!-- 汇总卡单独折叠：收起的只是这五张卡，下方明细表照常显示 -->
          <div class="summary-head">
            <span class="summary-head-title">汇总</span>
            <span v-if="summaryCollapsed" class="summary-head-inline">
              <span
                >站点数 <b>{{ rows.length }}</b></span
              >
              <span class="is-ok"
                >成功 <b>{{ okCount }}</b></span
              >
              <span class="is-fail"
                >失败 <b>{{ failCount }}</b></span
              >
              <span
                >总做种数 <b>{{ totalSeeding }}</b></span
              >
              <span
                >总上传 <b>{{ formatBytes(totalUploaded) }}</b></span
              >
              <span
                >总下载 <b>{{ formatBytes(totalDownloaded) }}</b></span
              >
              <span
                >平均分享率 <b>{{ averageRatio }}</b></span
              >
            </span>
            <el-button
              link
              type="primary"
              class="collapse-toggle"
              :icon="summaryCollapsed ? ArrowDown : ArrowUp"
              @click="summaryCollapsed = !summaryCollapsed"
            >
              {{ summaryCollapsed ? '展开汇总' : '收起汇总' }}
            </el-button>
          </div>

          <!-- 站点数与总做种数同一行：grid 固定 5 列，两者相邻 -->
          <div v-show="!summaryCollapsed" class="summary-grid">
            <div class="summary-card glass-card glass-rounded">
              <span class="summary-label">站点数</span>
              <span class="summary-value">{{ rows.length }}</span>
              <span class="summary-sub"
                >成功 <b class="is-ok">{{ okCount }}</b> / 失败
                <b class="is-fail">{{ failCount }}</b></span
              >
            </div>
            <div class="summary-card glass-card glass-rounded">
              <span class="summary-label">总做种数</span>
              <span class="summary-value">{{ totalSeeding }}</span>
              <span class="summary-sub">站点侧在做种</span>
            </div>
            <div class="summary-card glass-card glass-rounded">
              <span class="summary-label">总上传量</span>
              <span class="summary-value">{{ formatBytes(totalUploaded) }}</span>
              <span class="summary-sub">仅统计成功的站点</span>
            </div>
            <div class="summary-card glass-card glass-rounded">
              <span class="summary-label">总下载量</span>
              <span class="summary-value">{{ formatBytes(totalDownloaded) }}</span>
              <span class="summary-sub">站点认定口径</span>
            </div>
            <div class="summary-card glass-card glass-rounded">
              <span class="summary-label">平均分享率</span>
              <span class="summary-value">{{ averageRatio }}</span>
              <span class="summary-sub">无限分享率不计入</span>
            </div>
          </div>

          <div class="table-card main-card glass-card glass-rounded">
            <div class="card-head">
              <div class="card-head-left">
                <span class="card-title">账号累计数据</span>
                <span class="card-hint">
                  按站点管理顺序排列；点站点名开首页，点刷新图标单站刷新
                </span>
              </div>
              <div class="card-head-right">
                <el-input
                  v-model="keyword"
                  placeholder="搜索站点 / 用户名 / 等级"
                  clearable
                  :prefix-icon="Search"
                  class="search-input"
                />
                <el-select
                  v-model="statusFilter"
                  placeholder="全部状态"
                  clearable
                  class="status-select"
                >
                  <el-option label="获取成功" value="ok" />
                  <el-option label="获取失败" value="fail" />
                  <el-option label="尚未获取" value="pending" />
                </el-select>
                <!-- 标签来自「站点管理」里给站点打的标签；多选时命中任一标签即显示 -->
                <el-select
                  v-model="tagFilter"
                  multiple
                  collapse-tags
                  collapse-tags-tooltip
                  clearable
                  placeholder="按标签筛选"
                  class="tag-select"
                  :disabled="!allTags.length"
                >
                  <el-option v-for="tag in allTags" :key="tag" :label="tag" :value="tag" />
                </el-select>
              </div>
            </div>
            <div class="table-body">
              <el-table
                v-loading="loading"
                :data="filteredRows"
                class="site-data-table"
                border
                height="100%"
              >
                <template #empty>
                  <el-empty
                    description="暂无站点数据：请先在站点管理里为站点配置 Cookie / Passkey，再点「刷新数据」"
                  />
                </template>

                <el-table-column prop="nickname" label="站点" min-width="150" sortable>
                  <template #default="scope">
                    <div class="site-cell">
                      <span class="site-name">
                        <!-- 站点名是链接：点击打开站点首页；刷新只走右边的图标，避免误触 -->
                        <a
                          v-if="siteHomeUrl(scope.row)"
                          class="site-link"
                          :href="siteHomeUrl(scope.row)"
                          target="_blank"
                          rel="noopener noreferrer"
                          :title="siteHomeUrl(scope.row)"
                        >
                          {{ scope.row.nickname || scope.row.site_code }}
                        </a>
                        <span v-else class="site-link is-muted">
                          {{ scope.row.nickname || scope.row.site_code }}
                        </span>
                        <el-tooltip content="刷新该站点数据" placement="top" :show-after="300">
                          <el-icon
                            :class="[
                              'site-refresh-icon',
                              {
                                'is-loading': isSiteRefreshing(scope.row),
                                'is-blocked': refreshing,
                              },
                            ]"
                            @click="refreshSite(scope.row)"
                          >
                            <Refresh />
                          </el-icon>
                        </el-tooltip>
                      </span>
                      <el-tooltip
                        v-if="scope.row.site_code"
                        content="点击编辑该站点配置"
                        placement="top"
                        :show-after="300"
                      >
                        <button
                          type="button"
                          class="site-code site-code-action"
                          @click.stop="openSiteEditor(scope.row)"
                        >
                          <el-icon><EditPen /></el-icon>
                          <span>{{ scope.row.site_code }}</span>
                        </button>
                      </el-tooltip>
                    </div>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="username"
                  label="用户名"
                  min-width="110"
                  show-overflow-tooltip
                />

                <el-table-column
                  prop="join_date"
                  label="注册时间"
                  width="120"
                  align="center"
                  sortable
                >
                  <template #default="scope">
                    <span class="time-text">{{ formatJoinDate(scope.row.join_date) }}</span>
                  </template>
                </el-table-column>

                <el-table-column label="等级" width="150" align="center">
                  <template #default="scope">
                    <el-tooltip
                      v-if="scope.row.user_class"
                      :content="`${scope.row.user_class}（点击查看该站等级体系）`"
                      placement="top"
                    >
                      <el-tag
                        :type="levelTagType(scope.row.user_class)"
                        size="small"
                        effect="light"
                        class="level-tag"
                        :class="{ 'no-levels': !hasLevels(scope.row) }"
                        @click="openLevels(scope.row)"
                      >
                        {{ scope.row.user_class }}
                      </el-tag>
                    </el-tooltip>
                    <span v-else class="muted">—</span>
                  </template>
                </el-table-column>

                <el-table-column label="保号等级" width="150" align="center">
                  <template #default="scope">
                    <el-tooltip :content="keepAliveTooltip(scope.row)" placement="top">
                      <button type="button" class="keep-cell" @click="openKeepPicker(scope.row)">
                        <el-tag
                          v-if="keepAliveLevelName(scope.row)"
                          size="small"
                          effect="plain"
                          class="keep-tag"
                          :class="{ 'is-manual': !!scope.row.keep_alive_level }"
                        >
                          {{ keepAliveLevelName(scope.row) }}
                        </el-tag>
                        <span v-else class="keep-empty">{{
                          hasLevels(scope.row) ? '未识别' : '—'
                        }}</span>
                        <el-icon v-if="hasLevels(scope.row)" class="keep-edit-icon"
                          ><EditPen
                        /></el-icon>
                      </button>
                    </el-tooltip>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="uploaded"
                  label="上传量"
                  min-width="110"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span class="mono">{{ formatBytes(scope.row.uploaded) }}</span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="downloaded"
                  label="下载量"
                  min-width="110"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span class="mono">{{ formatBytes(scope.row.downloaded) }}</span>
                  </template>
                </el-table-column>

                <el-table-column prop="ratio" label="分享率" width="100" align="right" sortable>
                  <template #default="scope">
                    <span :class="['ratio-value', ratioClass(scope.row)]">{{
                      formatRatio(scope.row.ratio)
                    }}</span>
                  </template>
                </el-table-column>

                <el-table-column prop="seeding" label="做种" width="90" align="right" sortable />
                <el-table-column prop="leeching" label="下载中" width="90" align="right" sortable />

                <el-table-column prop="bonus" label="魔力值" width="110" align="right" sortable>
                  <template #default="scope">
                    <span class="mono">{{ formatBonus(scope.row.bonus) }}</span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="seed_bonus"
                  label="做种积分"
                  width="110"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span class="mono">{{ formatBonus(scope.row.seed_bonus) }}</span>
                  </template>
                </el-table-column>

                <el-table-column label="状态" width="120" align="center">
                  <template #default="scope">
                    <el-tooltip
                      :content="scope.row.message || statusText(scope.row.status)"
                      placement="top"
                    >
                      <el-tag :type="statusTagType(scope.row.status)" size="small" effect="light">
                        {{ statusText(scope.row.status) }}
                      </el-tag>
                    </el-tooltip>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="fetched_at"
                  label="更新时间"
                  width="165"
                  align="center"
                  sortable
                >
                  <template #default="scope">
                    <span class="time-text">{{ scope.row.fetched_at || '—' }}</span>
                  </template>
                </el-table-column>
              </el-table>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <el-tab-pane label="当日增量" name="daily">
        <div class="tab-pane-body">
          <!-- 日期选择放在 Tab 顶部：一行控制下方两个饼图和明细表，避免只改表格不改图 -->
          <div class="tab-toolbar glass-card glass-rounded">
            <div class="toolbar-left">
              <el-date-picker
                v-model="dailyDate"
                type="date"
                size="small"
                placeholder="选择日期"
                value-format="YYYY-MM-DD"
                :disabled-date="disableFutureDate"
                :clearable="false"
                class="daily-date"
                @change="loadDaily"
              />
              <el-button-group>
                <el-button size="small" :icon="ArrowLeft" @click="shiftDailyDate(-1)">
                  前一天
                </el-button>
                <el-button
                  size="small"
                  :disabled="isTodayDate(dailyDate)"
                  @click="shiftDailyDate(1)"
                >
                  后一天
                  <el-icon class="el-icon--right"><ArrowRight /></el-icon>
                </el-button>
              </el-button-group>
              <el-button size="small" @click="setDailyDate('today')">今天</el-button>
              <el-button size="small" @click="setDailyDate('yesterday')">昨天</el-button>
            </div>
            <span class="toolbar-hint">
              按服务器时区（Asia/Shanghai）判定「当天」；日期只影响本 Tab 的饼图与明细
            </span>
          </div>

          <!-- 当日上传 / 下载按站点拆分，两个饼图并排 -->
          <div class="chart-row">
            <div class="chart-card glass-card glass-rounded">
              <div class="card-head">
                <div class="card-head-left">
                  <span class="card-title">当日上传量</span>
                  <span class="card-hint">按站点占比，只算有当日增量的站点</span>
                </div>
                <span class="chart-total is-up">{{ formatDeltaBytes(dailyTotals.uploaded) }}</span>
              </div>
              <div v-if="uploadPieRows.length" ref="uploadPieRef" class="pie-chart"></div>
              <el-empty
                v-else
                :description="dailyDate ? '当日暂无上传增量' : '还没有采样记录'"
                :image-size="56"
                class="pie-empty"
              />
            </div>
            <div class="chart-card glass-card glass-rounded">
              <div class="card-head">
                <div class="card-head-left">
                  <span class="card-title">当日下载量</span>
                  <span class="card-hint">按站点占比，只算有当日增量的站点</span>
                </div>
                <span class="chart-total is-down">{{
                  formatDeltaBytes(dailyTotals.downloaded)
                }}</span>
              </div>
              <div v-if="downloadPieRows.length" ref="downloadPieRef" class="pie-chart"></div>
              <el-empty
                v-else
                :description="dailyDate ? '当日暂无下载增量' : '还没有采样记录'"
                :image-size="56"
                class="pie-empty"
              />
            </div>
          </div>

          <div class="table-card daily-card glass-card glass-rounded">
            <div class="card-head">
              <div class="card-head-left">
                <span class="card-title">当日增量明细</span>
                <span class="card-hint">当天最后一次采样减去日初基线，跨天时在「基线」列标出</span>
              </div>
              <div class="card-head-right">
                <span class="card-date-hint">{{ dailyDate || '今天' }}</span>
                <el-button
                  size="small"
                  :icon="Refresh"
                  :loading="dailyLoading"
                  @click="loadDaily()"
                >
                  刷新本表
                </el-button>
              </div>
            </div>
            <div class="table-body">
              <el-table
                v-loading="dailyLoading"
                :data="dailyRows"
                class="daily-table"
                border
                height="100%"
                show-summary
                :summary-method="dailySummaryMethod"
              >
                <template #empty>
                  <el-empty :description="dailyEmptyText" />
                </template>

                <el-table-column prop="nickname" label="站点" min-width="150" sortable>
                  <template #default="scope">
                    <span class="site-name">{{ scope.row.nickname || scope.row.site_code }}</span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="uploaded_delta"
                  label="上传量"
                  min-width="120"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span :class="['mono', deltaClass(scope.row.uploaded_delta)]">
                      {{ formatDeltaBytes(scope.row.uploaded_delta) }}
                    </span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="downloaded_delta"
                  label="下载量"
                  min-width="120"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span :class="['mono', deltaClass(scope.row.downloaded_delta)]">
                      {{ formatDeltaBytes(scope.row.downloaded_delta) }}
                    </span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="seed_bonus_delta"
                  label="做种积分"
                  min-width="120"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span :class="['mono', deltaClass(scope.row.seed_bonus_delta)]">
                      {{ formatDeltaNumber(scope.row.seed_bonus_delta) }}
                    </span>
                  </template>
                </el-table-column>

                <el-table-column
                  prop="bonus_delta"
                  label="魔力值"
                  min-width="120"
                  align="right"
                  sortable
                >
                  <template #default="scope">
                    <span :class="['mono', deltaClass(scope.row.bonus_delta)]">
                      {{ formatDeltaNumber(scope.row.bonus_delta) }}
                    </span>
                  </template>
                </el-table-column>

                <el-table-column label="基线" width="150" align="center">
                  <template #default="scope">
                    <el-tooltip
                      v-if="scope.row.has_baseline"
                      :content="`累计值对比基线：${scope.row.base_date} 的最后一次采样`"
                      placement="top"
                    >
                      <span
                        :class="[
                          'baseline-text',
                          { 'is-cross-day': scope.row.base_date !== scope.row.stat_date },
                        ]"
                      >
                        {{ baselineText(scope.row) }}
                      </span>
                    </el-tooltip>
                    <span v-else class="muted">无基线</span>
                  </template>
                </el-table-column>

                <el-table-column label="采样" width="130" align="center">
                  <template #default="scope">
                    <span class="time-text"
                      >{{ scope.row.samples }} 次 · {{ shortTime(scope.row.last_at) }}</span
                    >
                  </template>
                </el-table-column>
              </el-table>
            </div>
          </div>
        </div>
      </el-tab-pane>

      <!-- 上传趋势：近 N 天总上传增量 + 自选站点折线，支持按天/按月 -->
      <el-tab-pane label="上传趋势" name="trend">
        <div class="tab-pane-body">
          <div class="tab-toolbar trend-toolbar glass-card glass-rounded">
            <div class="trend-toolbar-row">
              <!-- 预设跟粒度绑定：按天看「近 N 天」，按月看「近 N 月 / 全部」，两套各自记忆 -->
              <el-radio-group
                v-if="trendGranularity === 'day'"
                v-model="trendPreset"
                size="small"
                @change="onTrendPresetChange"
              >
                <el-radio-button label="7">近 7 天</el-radio-button>
                <el-radio-button label="30">近 30 天</el-radio-button>
                <el-radio-button label="90">近 90 天</el-radio-button>
                <el-radio-button label="custom">自定义</el-radio-button>
              </el-radio-group>
              <el-radio-group v-else v-model="trendPreset" size="small" @change="onTrendPresetChange">
                <el-radio-button label="6m">近 6 个月</el-radio-button>
                <el-radio-button label="12m">近 1 年</el-radio-button>
                <el-radio-button label="all">全部时间</el-radio-button>
                <el-radio-button label="custom">自定义</el-radio-button>
              </el-radio-group>
              <el-date-picker
                v-if="trendPreset === 'custom'"
                v-model="trendRange"
                type="daterange"
                size="small"
                unlink-panels
                range-separator="至"
                start-placeholder="开始日期"
                end-placeholder="结束日期"
                value-format="YYYY-MM-DD"
                :disabled-date="disableFutureDate"
                class="trend-range"
                @change="loadTrend()"
              />
              <el-radio-group v-model="trendGranularity" size="small" @change="onTrendGranularityChange">
                <el-radio-button label="day">按天</el-radio-button>
                <el-radio-button label="month">按月</el-radio-button>
              </el-radio-group>
              <el-select
                v-model="trendSites"
                multiple
                filterable
                collapse-tags
                collapse-tags-tooltip
                size="small"
                placeholder="叠加站点曲线（可多选）"
                class="trend-site-select"
              >
                <el-option
                  v-for="option in trendSiteOptions"
                  :key="option.site_code"
                  :label="option.nickname"
                  :value="option.site_code"
                />
              </el-select>
              <el-button size="small" :icon="Refresh" :loading="trendLoading" @click="loadTrend()">
                刷新
              </el-button>
            </div>
            <div class="trend-summary">
              <span>区间总上传 <b class="is-up">{{ formatBytes(trendSummary.total) }}</b></span>
              <span>{{ trendGranularity === 'month' ? '月均' : '日均' }} <b>{{ formatBytes(trendSummary.average) }}</b></span>
              <span v-if="trendSummary.peakLabel"
                >峰值 <b>{{ formatBytes(trendSummary.peak) }}</b>
                <span class="muted">（{{ trendSummary.peakLabel }}）</span></span
              >
              <span class="muted"
                >有采样 {{ trendSummary.covered }}/{{ trendSummary.buckets }} 个{{ trendSummary.unitLabel }}</span
              >
            </div>
          </div>

          <div class="chart-card trend-card glass-card glass-rounded">
            <div class="card-head">
              <div class="card-head-left">
                <span class="card-title">上传增量趋势</span>
                <span class="card-hint">
                  增量口径与「当日增量」一致（当天最后一次采样 − 日初基线）；折线断开表示那天没有采样
                </span>
              </div>
              <span v-if="trendData" class="card-range">{{ trendData.from }} ~ {{ trendData.to }}</span>
            </div>
            <div v-show="trendHasData" ref="trendChartRef" class="trend-chart"></div>
            <el-empty
              v-if="!trendHasData && !trendLoading"
              description="所选区间还没有采样数据：服务每天刷新一次后即可积累，历史数据从上线当天开始记录"
              :image-size="64"
            />
          </div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-dialog
      v-model="levelDialogVisible"
      :title="`${levelDialogSite} · 等级体系`"
      width="1080px"
      top="5vh"
      destroy-on-close
      class="level-dialog"
    >
      <div class="level-stepper">
        <div
          v-for="(level, index) in levelDialogLevels"
          :key="`${level.name}-${index}`"
          class="level-step"
          :class="[`is-${levelStepStatus(index)}`, { 'is-selected': index === activeLevelIndex }]"
          :title="`${level.name}（点击查看该等级的升级进度）`"
          @click="selectedLevelIndex = index"
        >
          <div class="step-circle">{{ index + 1 }}</div>
          <div class="step-name">{{ levelShortName(level.name) }}</div>
          <el-tag size="small" :type="levelStepTagType(index)" effect="light" class="step-tag">
            {{ levelStepLabel(index) }}
          </el-tag>
          <el-tag v-if="level.keep_alive" size="small" effect="dark" class="step-keep-tag">
            保号
          </el-tag>
        </div>
      </div>

      <div v-if="activeLevel" class="next-level-card">
        <div class="next-level-head">
          <div class="next-level-target">
            <el-icon class="target-icon">
              <Aim />
            </el-icon>
            <span class="next-level-label">{{ activeLevelTitle }}</span>
            <strong class="next-level-name">{{ activeLevel.name }}</strong>
          </div>
          <el-tag :type="activeLevelReached ? 'success' : 'warning'" effect="light" class="eta-tag">
            <el-icon>
              <Timer />
            </el-icon>
            {{ etaText }}
          </el-tag>
        </div>

        <div v-if="activePath" class="next-level-body">
          <div class="progress-side">
            <el-progress
              type="dashboard"
              :percentage="activePath.percent"
              :width="132"
              :stroke-width="10"
              :color="activePath.percent === 100 ? '#22c55e' : '#0ea5e9'"
            >
              <template #default="{ percentage }">
                <span class="progress-value">{{ percentage }}%</span>
              </template>
            </el-progress>
            <div class="progress-caption">总体进度</div>
            <div class="progress-main">{{ activePath.percent }}%</div>
            <div class="progress-sub">
              已完成 {{ activePath.done }} 项 · 待完成
              {{ Math.max(activePath.total - activePath.done, 0) }} 项
            </div>
          </div>

          <div class="requirement-side">
            <div v-if="upgradePaths.length > 1" class="path-switcher">
              <span class="path-hint">该站有多种升级方式，满足任意一种即可：</span>
              <el-button
                v-for="(path, index) in upgradePaths"
                :key="path.name"
                size="small"
                :type="index === activePathIndex ? 'primary' : 'default'"
                plain
                @click="activePathIndex = index"
              >
                {{ path.name }} · {{ path.percent }}%
              </el-button>
            </div>

            <div class="requirement-head">
              <span>项目</span>
              <span>目标要求</span>
              <span>当前进度</span>
            </div>
            <template v-if="activePath.items.length">
              <div v-for="item in activePath.items" :key="item.key" class="requirement-row">
                <span class="requirement-item">
                  <el-icon>
                    <component :is="metricIcons[item.icon] || Document" />
                  </el-icon>
                  {{ item.label }}
                </span>
                <span class="requirement-target">{{ item.targetText }}</span>
                <span
                  class="requirement-current"
                  :class="{ 'is-met': item.met === true, 'is-unknown': item.met === null }"
                >
                  {{ item.currentText }}
                </span>
              </div>
            </template>
            <div v-else class="requirement-empty">
              站点没有给出可结构化的升级条件，原文：{{ activeLevel.requirements || '—' }}
            </div>
          </div>
        </div>
      </div>
      <el-empty v-else description="已经是等级表的最后一级，或该站没有等级数据" />

      <el-collapse class="level-raw-collapse">
        <el-collapse-item title="完整等级表（含升级条件与权限原文）" name="raw">
          <div v-if="levelDialogHiddenGroups > 0" class="level-table-note">
            已隐藏 {{ levelDialogHiddenGroups }} 个特殊用户组（贵宾 / 工作组 / 管理组 / Rainbow 等），它们不靠做种量升级
          </div>
          <el-table
            :data="levelDialogLevels"
            size="small"
            border
            max-height="42vh"
            :row-class-name="levelRowClass"
            class="level-table"
          >
            <template #empty>
              <el-empty description="该站点没有可用的等级数据，刷新后再试" />
            </template>
            <el-table-column prop="name" label="等级" width="220">
              <template #default="scope">
                <span
                  :class="[
                    'level-name',
                    { 'is-current': isCurrentLevel(scope.row), 'is-keep': scope.row.keep_alive },
                  ]"
                >
                  {{ scope.row.name }}
                </span>
                <el-tag
                  v-if="isCurrentLevel(scope.row)"
                  size="small"
                  type="success"
                  effect="dark"
                  class="level-flag"
                >
                  当前
                </el-tag>
                <el-tag
                  v-if="scope.row.keep_alive"
                  size="small"
                  effect="dark"
                  class="level-flag keep-flag"
                >
                  保号
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column prop="requirements" label="升级条件" min-width="260">
              <template #default="scope">
                <span class="level-text">{{ scope.row.requirements || '—' }}</span>
              </template>
            </el-table-column>
            <el-table-column prop="privileges" label="权限" min-width="300">
              <template #default="scope">
                <span class="level-text">{{ scope.row.privileges || '—' }}</span>
              </template>
            </el-table-column>
          </el-table>
        </el-collapse-item>
      </el-collapse>
    </el-dialog>

    <!-- 定时刷新：每天在若干个指定时刻自动抓取全部站点；时刻按服务器时区（Asia/Shanghai）解释 -->
    <el-dialog
      v-model="scheduleVisible"
      title="站点数据定时刷新"
      width="480px"
      top="20vh"
      class="schedule-dialog"
    >
      <el-form label-width="88px">
        <el-form-item label="启用">
          <el-switch v-model="scheduleForm.enabled" />
          <span class="form-hint inline">开启后每天在这些时刻自动抓取全部站点</span>
        </el-form-item>
        <el-form-item label="执行时间">
          <div class="schedule-times">
            <div
              v-for="(_, index) in scheduleForm.times"
              :key="index"
              class="schedule-time-row"
            >
              <el-time-select
                v-model="scheduleForm.times[index]"
                start="00:00"
                step="00:15"
                end="23:45"
                placeholder="选择时间"
                class="schedule-time-select"
              />
              <el-button
                link
                type="danger"
                :icon="Delete"
                :disabled="scheduleForm.times.length <= 1"
                class="schedule-time-remove"
                @click="removeScheduleTime(index)"
              />
            </div>
            <el-button
              size="small"
              :icon="Plus"
              :disabled="scheduleForm.times.length >= maxScheduleTimes"
              @click="addScheduleTime"
            >
              添加时间
            </el-button>
          </div>
        </el-form-item>
      </el-form>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="按服务器时区（Asia/Shanghai）执行；到点后抓取全部站点并写入当天快照，抓取失败的站点会在状态列标红。一天可以设置多个时刻（最多 12 个）。"
      />
      <template #footer>
        <el-button @click="scheduleVisible = false">取消</el-button>
        <el-button type="primary" :loading="scheduleSaving" @click="saveSchedule">保存</el-button>
      </template>
    </el-dialog>

    <!-- 保号等级手动指定：自动识别靠站点 FAQ 的权限文案，写法千奇百怪识别不全，
         这里让用户直接点选该站真实的保号等级；保存后刷新数据也不会被覆盖。 -->
    <el-dialog
      v-model="keepPickerVisible"
      :title="`设置保号等级 · ${keepPickerSite}`"
      width="460px"
      top="24vh"
      destroy-on-close
      class="keep-picker-dialog"
    >
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="默认按站点「权限」列里的「永远保留账号 / 账号永久保留」自动识别；手动指定后以你的选择为准，后续刷新数据不会覆盖。"
      />
      <el-select
        v-model="keepPickerValue"
        filterable
        placeholder="选择保号等级"
        class="keep-picker-select"
      >
        <el-option
          :label="`自动识别（按站点权限判定${keepPickerAutoName ? '：' + keepPickerAutoName : ''}）`"
          :value="AUTO_KEEP_ALIVE"
        />
        <el-option
          v-for="(level, index) in keepPickerLevels"
          :key="level.name"
          :label="`${index + 1}. ${level.name}`"
          :value="level.name"
        />
      </el-select>
      <p class="keep-picker-hint">
        默认按站点「权限」列自动识别（当前：{{
          keepPickerAutoName || '未识别到'
        }}）；选择「自动识别」即恢复站点文案的判定结果。
      </p>
      <template #footer>
        <el-button @click="keepPickerVisible = false">取消</el-button>
        <el-button type="primary" :loading="keepPickerSaving" @click="saveKeepPicker"
          >保存</el-button
        >
      </template>
    </el-dialog>

    <!-- 站点就地编辑：入口是站点列下方的站点标识，只提交基础配置白名单，
         不会覆盖标签 / Dupe 规则 / 禁转名单等「站点管理」页维护的字段。 -->
    <el-dialog
      v-model="siteEditorVisible"
      :title="`编辑站点 · ${siteEditorForm.nickname || siteEditorCode}`"
      width="640px"
      top="8vh"
      destroy-on-close
      class="site-editor-dialog"
    >
      <div v-loading="siteEditorLoading" class="site-editor-body">
        <el-form label-width="92px" label-position="left">
          <el-form-item label="站点标识">
            <el-input v-model="siteEditorCode" disabled />
            <div class="form-hint">站点标识由配置文件决定，不能在页面修改</div>
          </el-form-item>
          <el-form-item label="站点名称">
            <el-input v-model="siteEditorForm.nickname" maxlength="60" placeholder="站点显示名称" />
          </el-form-item>
          <el-form-item label="站点地址">
            <el-input v-model="siteEditorForm.base_url" placeholder="https://example.com" />
            <div class="form-hint">站点换域名后必须同步修改，否则抓取会失败</div>
          </el-form-item>
          <el-form-item label="Cookie">
            <el-input
              v-model="siteEditorForm.cookie"
              type="textarea"
              :rows="4"
              resize="vertical"
              placeholder="浏览器开发者工具里复制的完整 Cookie"
            />
          </el-form-item>
          <el-form-item label="Passkey">
            <el-input
              v-model="siteEditorForm.passkey"
              placeholder="部分站点（如杜比、HDtime）发种需要"
            />
          </el-form-item>
          <el-form-item label="可发种">
            <el-switch v-model="siteEditorForm.can_publish" />
            <span class="form-hint inline">关闭后该站点不会出现在发布目标里</span>
          </el-form-item>
          <el-form-item label="健康检测">
            <el-switch v-model="siteEditorForm.check_cookie_enabled" />
            <span class="form-hint inline">定时检测 Cookie 是否失效并提醒</span>
          </el-form-item>
          <el-form-item label="发种方式">
            <el-radio-group v-model="siteEditorForm.publish_mode">
              <el-radio :value="0">正式发种</el-radio>
              <el-radio :value="1">候选申请</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="显示顺序">
            <el-input-number
              v-model="siteEditorForm.sort_order"
              :min="0"
              :max="9999"
              controls-position="right"
              class="sort-order-input"
            />
            <span class="form-hint inline">与站点管理页一致，数值小的排在前面</span>
          </el-form-item>
        </el-form>
        <el-alert
          type="info"
          :closable="false"
          show-icon
          title="这里只改抓取与发布相关的基础配置；标签、Dupe 查重规则、禁转名单等请到「站点管理」页编辑。"
        />
      </div>
      <template #footer>
        <el-button @click="siteEditorVisible = false">取消</el-button>
        <el-button type="primary" :loading="siteEditorSaving" @click="saveSiteEditor"
          >保存</el-button
        >
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch, type Component } from 'vue'
import axios from 'axios'
import * as echarts from 'echarts'
import type { EChartsType } from 'echarts'
import {
  Aim,
  AlarmClock,
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Calendar,
  Coin,
  Delete,
  Document,
  Download,
  EditPen,
  MagicStick,
  Plus,
  Refresh,
  Search,
  Timer,
  TrendCharts,
  Upload,
} from '@element-plus/icons-vue'
import { ElMessage } from '@/utils/uiNotify'
import { ensureSiteOrderLoaded } from '@/utils/siteOrder'
import {
  buildUpgradePaths,
  type RequirementProfile,
  type UpgradePath,
} from '@/utils/levelRequirements'
import { findCurrentLevelIndex, normalizeLevel, splitLevelsForUser } from '@/utils/levelFilter'

type UserLevelRow = {
  name: string
  order: number
  requirements: string
  privileges: string
  /** 保号等级：站点「账号保留规则」里「该等级及以上用户会永远保留账号」的起点，前端用蓝色标出 */
  keep_alive: boolean
}

type SiteProfileRow = {
  site_code: string
  nickname: string
  base_url: string
  user_id: string
  username: string
  user_class: string
  uploaded: number
  downloaded: number
  /** -1 表示无限分享率（站点显示 ∞） */
  ratio: number
  bonus: number
  /** 做种积分（与魔力值是两套体系，部分站叫「做种得分」） */
  seed_bonus: number
  seeding: number
  leeching: number
  invites: number
  join_date: string
  source: string
  status: string
  message: string
  fetched_at: string
  updated_at: string
  /** 站点等级体系（来自 FAQ / 规则页，抓不到时是 NexusPHP 标准表） */
  levels?: UserLevelRow[]
  /** 用户手动指定的保号等级名（空串 = 按站点权限自动识别） */
  keep_alive_level?: string
  /** 站点权限**自动识别**出的保号等级名（不受手动设置影响，用于提示会回到哪一级） */
  keep_alive_auto?: string
  /** 站点在「站点管理」里打的标签（sites.tags） */
  tags?: string[]
}

/** 当日增量表的一行：累计值 + 相对日初基线的增量（null 表示当天无基线） */
type DailyStatRow = {
  site_code: string
  nickname: string
  stat_date: string
  uploaded: number
  downloaded: number
  bonus: number
  seed_bonus: number
  seeding: number
  uploaded_delta: number | null
  downloaded_delta: number | null
  bonus_delta: number | null
  seed_bonus_delta: number | null
  seeding_delta: number | null
  /** 基线所属日期（一般是昨天；服务几天没跑时会更早） */
  base_date: string
  has_baseline: boolean
  samples: number
  last_at: string
}

type DailySummary = {
  date: string
  has_baseline: boolean
  rows: DailyStatRow[]
  total_uploaded_delta: number
  total_downloaded_delta: number
  total_bonus_delta: number
  total_seed_bonus_delta: number
}

/** 上传趋势：某个站点的一条折线（values 里 null 表示该时间桶没采到数据） */
type TrendSiteSeries = {
  site_code: string
  nickname: string
  values: (number | null)[]
  total: number
}

/** 上传趋势接口返回 */
type TrendData = {
  from: string
  to: string
  granularity: 'day' | 'month'
  buckets: string[]
  labels: string[]
  total: (number | null)[]
  total_sum: number
  sites: TrendSiteSeries[]
  days: number
}

const rows = ref<SiteProfileRow[]>([])
const loading = ref(false)
const refreshing = ref(false)
/** 汇总卡默认收起：只留一行关键指标，把高度让给下方站点列表（列表本身不折叠） */
const summaryCollapsed = ref(true)
// 单站刷新中的站点（按 site_code 索引），用于行内转圈并阻止重复点击
const siteRefreshing = ref<Record<string, boolean>>({})
const keyword = ref('')
const statusFilter = ref('')
/** 标签筛选（多选，命中任一标签即显示；空数组 = 不筛选） */
const tagFilter = ref<string[]>([])

// 当日增量表：日期为空表示「今天」，由后端按服务器时区判定（避免浏览器时区与库时区不一致）
const dailyDate = ref('')
const dailyLoading = ref(false)
const dailyRows = ref<DailyStatRow[]>([])
const dailyTotals = ref({
  uploaded: 0,
  downloaded: 0,
  bonus: 0,
  seed_bonus: 0,
})

/** 当前 Tab：账号累计数据 / 当日增量 / 上传趋势（图表只在对应 Tab 可见时才需要计算尺寸） */
const activeTab = ref<'cumulative' | 'daily' | 'trend'>('cumulative')

// 当日上传 / 下载的站点占比饼图
const uploadPieRef = ref<HTMLDivElement | null>(null)
const downloadPieRef = ref<HTMLDivElement | null>(null)
let uploadPieChart: EChartsType | null = null
let downloadPieChart: EChartsType | null = null

/** 饼图最多单独列出多少个站点，其余合并成「其他 N 站」，避免图例刷屏 */
const PIE_TOP_N = 8
type PieSlice = { name: string; value: number }
const PIE_COLORS = [
  '#409eff',
  '#67c23a',
  '#e6a23c',
  '#f56c6c',
  '#909399',
  '#8e44ad',
  '#16a085',
  '#d35400',
  '#2c82c9',
  '#c0392b',
]

/** 把当日增量按站点拆成饼图切片（只保留正增量，降序取前 N + 其他） */
const buildPieSlices = (kind: 'uploaded' | 'downloaded'): PieSlice[] => {
  const slices = dailyRows.value
    .map((row) => ({
      name: row.nickname || row.site_code,
      value: Math.max(0, (kind === 'uploaded' ? row.uploaded_delta : row.downloaded_delta) || 0),
    }))
    .filter((item) => item.value > 0)
    .sort((a, b) => b.value - a.value)
  if (slices.length <= PIE_TOP_N) return slices
  const rest = slices.slice(PIE_TOP_N)
  return [
    ...slices.slice(0, PIE_TOP_N),
    { name: `其他 ${rest.length} 站`, value: rest.reduce((sum, item) => sum + item.value, 0) },
  ]
}

const uploadPieRows = computed(() => buildPieSlices('uploaded'))
const downloadPieRows = computed(() => buildPieSlices('downloaded'))

/** 读 CSS 变量，让饼图文字/描边跟随亮暗主题 */
const cssColor = (name: string, fallback: string): string => {
  const value = getComputedStyle(document.documentElement).getPropertyValue(name).trim()
  return value || fallback
}

const buildPieOption = (name: string, slices: PieSlice[]) => ({
  color: PIE_COLORS,
  tooltip: {
    trigger: 'item',
    formatter: (params: unknown) => {
      const item = params as { marker?: string; name?: string; value?: number; percent?: number }
      return `${item.marker ?? ''}${item.name ?? ''}<br/>${formatBytes(item.value ?? 0)}（${item.percent ?? 0}%）`
    },
  },
  legend: {
    type: 'scroll',
    orient: 'vertical',
    right: 4,
    top: 'middle',
    itemWidth: 8,
    itemHeight: 8,
    itemGap: 6,
    textStyle: { fontSize: 11, color: cssColor('--el-text-color-regular', '#606266') },
    formatter: (label: string) => (label.length > 11 ? `${label.slice(0, 11)}…` : label),
  },
  series: [
    {
      name,
      type: 'pie',
      radius: ['46%', '70%'],
      center: ['32%', '52%'],
      avoidLabelOverlap: true,
      label: { show: false },
      labelLine: { show: false },
      itemStyle: {
        borderColor: cssColor('--el-bg-color-overlay', 'rgba(255, 255, 255, 0.9)'),
        borderWidth: 1,
      },
      data: slices,
    },
  ],
})

/** 销毁某个饼图实例（数据为空时容器会被 v-if 移除） */
const disposePie = (chart: EChartsType | null): null => {
  chart?.dispose()
  return null
}

/**
 * 按当前增量数据重绘两个饼图。
 * 说明：饼图容器在「账号累计数据」Tab 下是隐藏的，隐藏时宽度为 0，所以只在这个 Tab 激活时才实例化。
 */
const renderPies = async () => {
  await nextTick()
  if (activeTab.value !== 'daily') return
  if (uploadPieRef.value && uploadPieRows.value.length > 0) {
    uploadPieChart = uploadPieChart ?? echarts.init(uploadPieRef.value)
    uploadPieChart.setOption(buildPieOption('当日上传量', uploadPieRows.value), true)
  } else {
    uploadPieChart = disposePie(uploadPieChart)
  }
  if (downloadPieRef.value && downloadPieRows.value.length > 0) {
    downloadPieChart = downloadPieChart ?? echarts.init(downloadPieRef.value)
    downloadPieChart.setOption(buildPieOption('当日下载量', downloadPieRows.value), true)
  } else {
    downloadPieChart = disposePie(downloadPieChart)
  }
}

const resizePies = () => {
  uploadPieChart?.resize()
  downloadPieChart?.resize()
}

/** 窗口尺寸变化时统一重算所有图表（趋势图在别的 Tab 下宽度为 0，resize 也不会出错） */
const resizeCharts = () => {
  resizePies()
  trendChart?.resize()
}

// 切到增量 / 趋势 Tab 时容器才有宽度，这里补一次绘制
watch(activeTab, (tab) => {
  if (tab === 'daily') {
    void renderPies()
  }
  if (tab === 'trend') {
    if (!trendData.value) {
      void loadTrend()
    } else {
      void renderTrend()
    }
  }
})

// ───────────────────────── 日期工具（当日增量） ─────────────────────────

/** 按本地时区把 Date 格式化成 YYYY-MM-DD（不能用 toISOString，会因时区偏移差一天） */
const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${year}-${month}-${day}`
}

const todayLocalDate = (): string => formatLocalDate(new Date())

/** 是否已经是今天（用于禁用「后一天」） */
const isTodayDate = (value: string): boolean => !value || value >= todayLocalDate()

/** 日期前后跳动一天；不跨到未来 */
const shiftDailyDate = (offset: number) => {
  const base = dailyDate.value ? new Date(`${dailyDate.value}T00:00:00`) : new Date()
  if (Number.isNaN(base.getTime())) return
  base.setDate(base.getDate() + offset)
  const next = formatLocalDate(base)
  if (next > todayLocalDate()) return
  dailyDate.value = next
  void loadDaily(next)
}

/** 快捷跳到今天 / 昨天 */
const setDailyDate = (kind: 'today' | 'yesterday') => {
  const target = new Date()
  if (kind === 'yesterday') target.setDate(target.getDate() - 1)
  const value = formatLocalDate(target)
  dailyDate.value = value
  void loadDaily(value)
}

// ───────────────────────── 上传趋势 ─────────────────────────

const TREND_SITE_KEY = 'ptnexus.siteData.trend.sites'
/** 旧版单 key（早期只有按天一套预设），仅用于迁移 */
const TREND_PRESET_KEY = 'ptnexus.siteData.trend.preset'
const TREND_PRESET_DAY_KEY = 'ptnexus.siteData.trend.preset.day'
const TREND_PRESET_MONTH_KEY = 'ptnexus.siteData.trend.preset.month'
const TREND_GRANULARITY_KEY = 'ptnexus.siteData.trend.granularity'

/** 按天预设：近 N 天 / 自定义 */
type TrendDayPreset = '7' | '30' | '90' | 'custom'
/** 按月预设：按天那种「近 90 天」在月粒度下只有 3 个点，所以换成月/年/全部 */
type TrendMonthPreset = '6m' | '12m' | 'all' | 'custom'
type TrendPreset = TrendDayPreset | TrendMonthPreset

const readStoredTrendSites = (): string[] => {
  try {
    const parsed = JSON.parse(localStorage.getItem(TREND_SITE_KEY) || '[]')
    return Array.isArray(parsed) ? parsed.filter((item) => typeof item === 'string') : []
  } catch {
    return []
  }
}

const readStoredTrendGranularity = (): 'day' | 'month' =>
  localStorage.getItem(TREND_GRANULARITY_KEY) === 'month' ? 'month' : 'day'

const readStoredDayPreset = (): TrendDayPreset => {
  const stored =
    localStorage.getItem(TREND_PRESET_DAY_KEY) || localStorage.getItem(TREND_PRESET_KEY)
  return stored === '7' || stored === '30' || stored === '90' || stored === 'custom' ? stored : '30'
}

const readStoredMonthPreset = (): TrendMonthPreset => {
  const stored = localStorage.getItem(TREND_PRESET_MONTH_KEY)
  return stored === '6m' || stored === '12m' || stored === 'all' || stored === 'custom'
    ? stored
    : '6m'
}

/** 粒度：按天 / 按月（预设是两套，所以先定粒度再定预设） */
const trendGranularity = ref<'day' | 'month'>(readStoredTrendGranularity())
/** 趋势图区间预设：按天 7/30/90、按月 6m/12m/all，切换粒度时自动换成对应那套里上次选的 */
const trendPreset = ref<TrendPreset>(
  trendGranularity.value === 'month' ? readStoredMonthPreset() : readStoredDayPreset(),
)
/** 自定义区间（YYYY-MM-DD 两元素） */
const trendRange = ref<[string, string] | null>(null)
/** 用户选择要叠加显示曲线的站点（按站点代码存 localStorage） */
const trendSites = ref<string[]>(readStoredTrendSites())
const trendLoading = ref(false)
const trendData = ref<TrendData | null>(null)
const trendChartRef = ref<HTMLDivElement | null>(null)
let trendChart: EChartsType | null = null

/**
 * 站点下拉：账号数据表的全部站点 ∪ 趋势数据里出现过的站点。
 * 合起来是为了「换区间后选择不丢」——某个站点在新区间没数据时，它仍要留在下拉里可选，
 * 否则 el-select 会把它渲染成一个孤立的站点代码标签。
 */
const trendSiteOptions = computed(() => {
  const map = new Map<string, string>()
  for (const row of rows.value) {
    if (row.site_code) map.set(row.site_code, row.nickname || row.site_code)
  }
  for (const item of trendData.value?.sites || []) {
    if (item.site_code) map.set(item.site_code, item.nickname || item.site_code)
  }
  return Array.from(map, ([site_code, nickname]) => ({ site_code, nickname })).sort((a, b) =>
    a.nickname.localeCompare(b.nickname, 'zh-Hans-CN'),
  )
})

/** 所选区间内没有任何采样点时不画图（全 null） */
const trendHasData = computed(() =>
  Boolean(trendData.value?.buckets?.length) &&
  (trendData.value?.total || []).some((value) => value !== null),
)

/** 顶部汇总：区间合计 / 日均 / 峰值及日期 / 有采样的时间桶数 */
const trendSummary = computed(() => {
  const empty = {
    total: 0,
    average: 0,
    peak: 0,
    peakLabel: '',
    covered: 0,
    buckets: 0,
    unitLabel: '天',
  }
  const data = trendData.value
  if (!data) return empty
  const buckets = data.buckets || []
  const values = data.total || []
  const covered: number[] = []
  values.forEach((value, index) => {
    if (value === null || value === undefined) return
    covered.push(index)
  })
  const total = covered.reduce((sum, index) => sum + (values[index] || 0), 0)
  // 日均按「有采样的时间桶」算，别把没跑服务的日子当成 0 拉低平均
  const average = covered.length ? total / covered.length : 0
  let peakIndex = -1
  covered.forEach((index) => {
    if (peakIndex < 0 || (values[index] || 0) > (values[peakIndex] || 0)) peakIndex = index
  })
  return {
    total,
    average,
    peak: peakIndex >= 0 ? values[peakIndex] || 0 : 0,
    peakLabel: peakIndex >= 0 ? (data.labels?.[peakIndex] || data.buckets?.[peakIndex] || '') : '',
    covered: covered.length,
    buckets: buckets.length,
    unitLabel: data.granularity === 'month' ? '月' : '天',
  }
})

/**
 * 把预设/自定义区间换算成接口参数。
 * 按月时起点对齐到「N-1 个月前的 1 号」，保证每个桶都是完整自然月（月中起点会让首月变矮）；
 * 「全部时间」把 from 交给后端用 all 表示（真正起点只有后端知道）。
 */
const resolveTrendQuery = (): { from: string; to: string } => {
  const today = new Date()
  const to = formatLocalDate(today)
  if (trendPreset.value === 'custom') {
    return trendRange.value?.length === 2
      ? { from: trendRange.value[0], to: trendRange.value[1] }
      : { from: '', to }
  }
  if (trendGranularity.value === 'month') {
    if (trendPreset.value === 'all') return { from: 'all', to }
    const months = trendPreset.value === '12m' ? 12 : 6
    const start = new Date(today.getFullYear(), today.getMonth() - (months - 1), 1)
    return { from: formatLocalDate(start), to }
  }
  const days = Number(trendPreset.value) || 30
  const start = new Date(today)
  start.setDate(today.getDate() - (days - 1))
  return { from: formatLocalDate(start), to: formatLocalDate(today) }
}

/** 构造折线图配置：总增量一条粗线，选中的站点各一条细线 */
const buildTrendOption = (data: TrendData) => {
  const selected = trendSites.value
    .map((code, index) => {
      const site = data.sites.find((item) => item.site_code === code)
      if (!site) return null
      return {
        name: site.nickname || site.site_code,
        type: 'line' as const,
        smooth: true,
        showSymbol: false,
        connectNulls: false,
        data: site.values,
        lineStyle: { width: 1.5 },
        color: PIE_COLORS[(index + 1) % PIE_COLORS.length],
      }
    })
    .filter((item): item is NonNullable<typeof item> => item !== null)

  const textColor = cssColor('--el-text-color-secondary', '#909399')
  const lineColor = cssColor('--el-border-color-lighter', '#ebeef5')
  const series = [
    {
      name: '全部站点',
      type: 'line' as const,
      smooth: true,
      showSymbol: false,
      connectNulls: false,
      data: data.total,
      lineStyle: { width: 2.5 },
      areaStyle: { opacity: 0.12 },
      color: PIE_COLORS[0],
      z: 10,
    },
    ...selected,
  ]
  return {
    color: PIE_COLORS,
    grid: { left: 12, right: 18, top: 34, bottom: 8, containLabel: true },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'line' },
      formatter: (params: unknown) => {
        const list = Array.isArray(params) ? params : [params]
        const rows = list.map((item) => {
          const point = item as { marker?: string; seriesName?: string; value?: number | null; axisValue?: string }
          const value = point.value
          const text = value === null || value === undefined ? '无采样' : formatBytes(value)
          return `${point.marker ?? ''}${point.seriesName ?? ''}：${text}`
        })
        const title = (list[0] as { axisValue?: string } | undefined)?.axisValue ?? ''
        return [`<b>${title}</b>`, ...rows].join('<br/>')
      },
    },
    legend: {
      type: 'scroll',
      top: 0,
      itemWidth: 10,
      itemHeight: 10,
      textStyle: { fontSize: 11, color: cssColor('--el-text-color-regular', '#606266') },
    },
    xAxis: {
      type: 'category',
      boundaryGap: false,
      data: data.labels,
      axisLabel: {
        color: textColor,
        fontSize: 11,
        hideOverlap: true,
        rotate: data.labels.length > 20 ? 35 : 0,
      },
      axisLine: { lineStyle: { color: lineColor } },
    },
    yAxis: {
      type: 'value',
      axisLabel: { color: textColor, fontSize: 11, formatter: (value: number) => formatBytes(value, 1) },
      splitLine: { lineStyle: { color: lineColor, type: 'dashed' } },
    },
    series,
  }
}

/** 重绘趋势图（非趋势 Tab 时容器宽度为 0，不实例化） */
const renderTrend = async () => {
  await nextTick()
  if (activeTab.value !== 'trend') return
  const data = trendData.value
  if (!data || !trendHasData.value || !trendChartRef.value) {
    trendChart?.dispose()
    trendChart = null
    return
  }
  trendChart = trendChart ?? echarts.init(trendChartRef.value)
  trendChart.setOption(buildTrendOption(data), true)
  trendChart.resize()
}

/** 拉取趋势数据（区间来自预设或自定义日期范围） */
const loadTrend = async () => {
  if (trendPreset.value === 'custom' && !(trendRange.value?.length === 2)) return
  const query = resolveTrendQuery()
  trendLoading.value = true
  try {
    const response = await axios.get('/api/sites/profiles/trend', {
      params: { ...query, granularity: trendGranularity.value },
    })
    const data = response.data?.data as TrendData | undefined
    trendData.value = data ?? null
    // 站点被删掉时才清掉选择；只是「本区间没数据」的站点保留勾选，换回原区间还能显示
    const existing = new Set(rows.value.map((row) => row.site_code))
    if (existing.size) {
      trendSites.value = trendSites.value.filter((code) => existing.has(code))
    }
    void renderTrend()
  } catch (error) {
    ElMessage.error(`加载上传趋势失败：${errorReason(error)}`)
    console.error('加载上传趋势失败:', error)
  } finally {
    trendLoading.value = false
  }
}

/** 切换区间预设：自定义要等用户选完日期再请求；两套预设分别记忆 */
const onTrendPresetChange = () => {
  if (trendGranularity.value === 'month') {
    localStorage.setItem(TREND_PRESET_MONTH_KEY, trendPreset.value)
  } else {
    localStorage.setItem(TREND_PRESET_DAY_KEY, trendPreset.value)
  }
  if (trendPreset.value === 'custom' && !(trendRange.value?.length === 2)) {
    // 还没选日期：把旧区间的图清掉，别让用户以为「自定义」等于当前图
    trendData.value = null
    void renderTrend()
    return
  }
  void loadTrend()
}

/** 自定义区间是否超过「按天」的 366 天上限（按月允许更长，切回按天时要用） */
const customTrendRangeOverDayLimit = (): boolean => {
  if (trendRange.value?.length !== 2) return false
  const start = new Date(`${trendRange.value[0]}T00:00:00`).getTime()
  const end = new Date(`${trendRange.value[1]}T00:00:00`).getTime()
  if (Number.isNaN(start) || Number.isNaN(end)) return false
  return Math.round((end - start) / 86400000) + 1 > 366
}

const onTrendGranularityChange = () => {
  localStorage.setItem(TREND_GRANULARITY_KEY, trendGranularity.value)
  // 预设是两套：切粒度就换成该粒度上次选的（没记录过则按天=近30天、按月=近6个月）
  const next = trendGranularity.value === 'month' ? readStoredMonthPreset() : readStoredDayPreset()
  // 按月能选很长的自定义区间，切回按天会撞「最多 366 天」的上限 —— 直接回落到近 30 天，
  // 而不是让用户看到一个 400 报错。
  if (trendGranularity.value === 'day' && next === 'custom' && customTrendRangeOverDayLimit()) {
    trendPreset.value = '30'
    localStorage.setItem(TREND_PRESET_DAY_KEY, '30')
    void loadTrend()
    return
  }
  trendPreset.value = next
  if (next === 'custom' && !(trendRange.value?.length === 2)) {
    trendData.value = null
    void renderTrend()
    return
  }
  void loadTrend()
}

// 站点勾选只影响前端叠加的曲线：数据一次拉全，勾选后立即重绘，不用重新请求
watch(trendSites, (value) => {
  localStorage.setItem(TREND_SITE_KEY, JSON.stringify(value))
  void renderTrend()
})

// 等级详情弹层
const levelDialogVisible = ref(false)
const levelDialogSite = ref('')
const levelDialogLevels = ref<UserLevelRow[]>([])
/** 因「特殊用户组」被隐藏的等级数量（>0 时在完整表上方提示） */
const levelDialogHiddenGroups = ref(0)
const levelDialogUserClass = ref('')
const levelDialogProfile = ref<SiteProfileRow | null>(null)
const activePathIndex = ref(0)

const metricIcons: Record<string, Component> = {
  uploaded: Upload,
  downloaded: Download,
  ratio: TrendCharts,
  join: Calendar,
  seed: Coin,
  bonus: MagicStick,
  published: Document,
  seeding: Download,
}

/** 全部站点标签（去重后按拼音/字母序），供筛选下拉使用 */
const allTags = computed(() => {
  const seen = new Set<string>()
  for (const row of rows.value) {
    for (const tag of row.tags || []) {
      const trimmed = tag.trim()
      if (trimmed) seen.add(trimmed)
    }
  }
  return Array.from(seen).sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))
})

/** 下拉里已选中的标签若在数据里消失（站点改了标签），清掉避免筛出空列表 */
watch(allTags, (tags) => {
  if (!tagFilter.value.length) return
  const available = new Set(tags)
  const kept = tagFilter.value.filter((tag) => available.has(tag))
  if (kept.length !== tagFilter.value.length) tagFilter.value = kept
})

const filteredRows = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  const tags = tagFilter.value
  return rows.value.filter((row) => {
    if (statusFilter.value && row.status !== statusFilter.value) return false
    if (tags.length) {
      const rowTags = (row.tags || []).map((tag) => tag.trim().toLowerCase())
      // 多选为「任一命中」，符合按标签浏览的习惯；要精确交集可只选一个标签
      if (!tags.some((tag) => rowTags.includes(tag.toLowerCase()))) return false
    }
    if (!kw) return true
    return [row.nickname, row.site_code, row.username, row.user_class]
      .filter(Boolean)
      .some((value) => String(value).toLowerCase().includes(kw))
  })
})

const okRows = computed(() => rows.value.filter((row) => row.status === 'ok'))
const okCount = computed(() => okRows.value.length)
const failCount = computed(() => rows.value.filter((row) => row.status === 'fail').length)

const totalUploaded = computed(() =>
  okRows.value.reduce((sum, row) => sum + (row.uploaded || 0), 0),
)
const totalDownloaded = computed(() =>
  okRows.value.reduce((sum, row) => sum + (row.downloaded || 0), 0),
)
const totalSeeding = computed(() => okRows.value.reduce((sum, row) => sum + (row.seeding || 0), 0))

const averageRatio = computed(() => {
  const values = okRows.value
    .map((row) => row.ratio)
    .filter((ratio) => ratio >= 0 && Number.isFinite(ratio))
  if (values.length === 0) return '—'
  const avg = values.reduce((sum, value) => sum + value, 0) / values.length
  return avg.toFixed(2)
})

const formatBytes = (bytes: number | null | undefined, decimals = 2): string => {
  if (!bytes || bytes <= 0) return '0'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), sizes.length - 1)
  return `${parseFloat((bytes / Math.pow(k, i)).toFixed(decimals))} ${sizes[i]}`
}

const formatRatio = (ratio: number | null | undefined): string => {
  if (ratio === null || ratio === undefined) return '—'
  if (ratio < 0) return '∞'
  return ratio.toFixed(2)
}

const ratioClass = (row: SiteProfileRow): string => {
  if (row.status !== 'ok' || row.ratio < 0) return ''
  if (row.ratio < 1) return 'ratio-low'
  if (row.ratio < 2) return 'ratio-mid'
  return 'ratio-good'
}

const formatBonus = (bonus: number | null | undefined): string => {
  if (!bonus) return '0'
  return bonus >= 1000 ? Math.round(bonus).toLocaleString() : bonus.toFixed(1)
}

/** 注册时间：站点格式五花八门（2025-11-13 / 2025/11/13 / 2025-11-13 08:30），统一显示成 YYYY-MM-DD */
const formatJoinDate = (value: string | null | undefined): string => {
  const text = String(value || '').trim()
  if (!text) return '—'
  // 接口站（朱雀）给的是 unix 时间戳，秒/毫秒都有可能，先转成本地日期再展示
  if (/^\d{9,13}$/.test(text)) {
    const raw = Number(text)
    const date = new Date(text.length >= 12 ? raw : raw * 1000)
    if (!Number.isNaN(date.getTime())) {
      const month = String(date.getMonth() + 1).padStart(2, '0')
      const day = String(date.getDate()).padStart(2, '0')
      return `${date.getFullYear()}-${month}-${day}`
    }
    return text
  }
  const match = /^(\d{4})[-/](\d{1,2})[-/](\d{1,2})/.exec(text)
  if (!match) return text
  const month = match[2].padStart(2, '0')
  const day = match[3].padStart(2, '0')
  return `${match[1]}-${month}-${day}`
}

/** 增量（体积）：null 表示无基线，0 表示确实没变化，正数带 + 号便于扫视 */
const formatDeltaBytes = (value: number | null | undefined): string => {
  if (value === null || value === undefined) return '—'
  if (value === 0) return '0'
  const sign = value < 0 ? '-' : '+'
  return `${sign}${formatBytes(Math.abs(value))}`
}

/** 增量（计数/积分）：null 表示无基线 */
const formatDeltaNumber = (value: number | null | undefined): string => {
  if (value === null || value === undefined) return '—'
  if (value === 0) return '0'
  const abs = Math.abs(value)
  const text = abs >= 1000 ? Math.round(abs).toLocaleString() : abs.toFixed(1)
  return `${value < 0 ? '-' : '+'}${text}`
}

/** 增量配色：正增长绿色、负增长（站点扣量）红色 */
const deltaClass = (value: number | null | undefined): string => {
  if (value === null || value === undefined || value === 0) return ''
  return value < 0 ? 'delta-negative' : 'delta-positive'
}

/** 两个 YYYY-MM-DD 之间相差的天数 */
const daysBetween = (from: string, to: string): number => {
  const start = new Date(`${from}T00:00:00`).getTime()
  const end = new Date(`${to}T00:00:00`).getTime()
  if (!Number.isFinite(start) || !Number.isFinite(end)) return 0
  return Math.round((end - start) / 86400000)
}

/** 基线展示：跨多天时明确标出，避免把几天的量误读成一天 */
const baselineText = (row: DailyStatRow): string => {
  if (!row.has_baseline || !row.base_date) return '—'
  const days = daysBetween(row.base_date, row.stat_date)
  if (days > 1) return `${row.base_date}（跨 ${days} 天）`
  return row.base_date
}

/** 采样时间只留时分，表格里更紧凑 */
const shortTime = (value: string): string => {
  if (!value) return ''
  const match = value.match(/\s(\d{2}:\d{2})/)
  return match ? match[1] : value
}

/** 日期选择器禁止选未来日期（未来没有数据） */
const disableFutureDate = (date: Date): boolean => date.getTime() > Date.now() + 86400000

const dailyEmptyText = computed(() =>
  dailyDate.value
    ? `${dailyDate.value} 没有采样记录，当天点过「刷新数据」后才会产生`
    : '还没有采样记录，点击「刷新数据」开始抓取',
)

/** 当日增量表底部的合计行（只合计有基线的站点） */
const dailySummaryMethod = (param: { columns: Array<{ property?: string }> }): string[] =>
  param.columns.map((column, index) => {
    if (index === 0) return '合计'
    switch (column.property) {
      case 'uploaded_delta':
        return formatDeltaBytes(dailyTotals.value.uploaded)
      case 'downloaded_delta':
        return formatDeltaBytes(dailyTotals.value.downloaded)
      case 'seed_bonus_delta':
        return formatDeltaNumber(dailyTotals.value.seed_bonus)
      case 'bonus_delta':
        return formatDeltaNumber(dailyTotals.value.bonus)
      default:
        return ''
    }
  })

const statusText = (status: string): string => {
  switch (status) {
    case 'ok':
      return '获取成功'
    case 'fail':
      return '获取失败'
    case 'skip':
      return '未配置凭据'
    default:
      return '尚未获取'
  }
}

const statusTagType = (status: string): 'success' | 'danger' | 'info' | 'warning' => {
  switch (status) {
    case 'ok':
      return 'success'
    case 'fail':
      return 'danger'
    case 'skip':
      return 'warning'
    default:
      return 'info'
  }
}

const hasLevels = (row: SiteProfileRow): boolean =>
  Array.isArray(row.levels) && row.levels.length > 0

/** 等级标签配色：按 NexusPHP 标准等级序列由低到高区分 */
const levelTagType = (name: string): 'success' | 'warning' | 'danger' | 'info' | 'primary' => {
  const key = (name || '').toLowerCase()
  if (key.includes('master')) return 'primary'
  if (key.includes('ultimate') || key.includes('extreme')) return 'warning'
  if (key.includes('veteran') || key.includes('insane')) return 'danger'
  if (key.includes('crazy')) return 'warning'
  if (key.includes('elite')) return 'primary'
  if (key.includes('power')) return 'success'
  if (key.includes('peasant')) return 'danger'
  return 'info'
}

/** 该站的保号等级名称（无等级数据时返回空串） */
const keepAliveLevelName = (row: SiteProfileRow): string =>
  row.levels?.find((level) => level.keep_alive)?.name || ''

/** 保号等级单元格的悬停说明：区分手动指定/自动识别，并提示可点击修改 */
const keepAliveTooltip = (row: SiteProfileRow): string => {
  if (!hasLevels(row)) return '该站点还没有等级数据，刷新后即可设置保号等级'
  const name = keepAliveLevelName(row)
  const manual = (row.keep_alive_level || '').trim()
  // 手动值可能因站点改名而失效：此时行内仍显示自动结果，必须说明清楚，不能假装是手动选的那一级。
  if (manual && normalizeLevel(manual) !== normalizeLevel(name)) {
    return `手动指定：${manual}（当前等级表中已不存在，暂按自动识别：${name || '无'}）· 点击可修改`
  }
  if (!name) return '未从站点权限识别到保号等级，点击可手动指定'
  const source = manual ? '手动指定' : '按站点权限自动识别'
  return `保号等级：${name}（${source}；该等级及以上账号永久保留）· 点击可修改`
}

/** 保号等级手动指定弹窗的状态 */
const keepPickerVisible = ref(false)
const keepPickerSite = ref('')
const keepPickerCode = ref('')
const keepPickerValue = ref('')
const keepPickerLevels = ref<UserLevelRow[]>([])
const keepPickerAutoName = ref('')
const keepPickerSaving = ref(false)

/** 下拉里的「恢复自动识别」哨兵值：空字符串在 el-select 里选中态显示不稳，用哨兵更可靠 */
const AUTO_KEEP_ALIVE = '__auto__'

const openKeepPicker = (row: SiteProfileRow): void => {
  if (!hasLevels(row)) {
    ElMessage.info('该站点还没有等级数据，点击站点名后的刷新图标抓取后再设置')
    return
  }
  keepPickerSite.value = row.nickname || row.site_code
  keepPickerCode.value = row.site_code
  keepPickerLevels.value = row.levels || []
  keepPickerValue.value = row.keep_alive_level || AUTO_KEEP_ALIVE
  keepPickerAutoName.value = row.keep_alive_auto || ''
  keepPickerVisible.value = true
}

/** 保存手动保号等级（选「自动识别」= 清空覆盖，回到站点权限判定） */
const saveKeepPicker = async (): Promise<void> => {
  if (!keepPickerCode.value) return
  keepPickerSaving.value = true
  try {
    const level = keepPickerValue.value === AUTO_KEEP_ALIVE ? '' : keepPickerValue.value
    await axios.post('/api/sites/profiles/keep-alive', { site: keepPickerCode.value, level })
    keepPickerVisible.value = false
    ElMessage.success(level ? `已将 ${level} 设为保号等级` : '已恢复按站点权限自动识别')
    // 重新拉列表，保证行内标记与服务端一致
    await loadProfiles()
  } catch (error) {
    ElMessage.error(`保存保号等级失败：${errorReason(error)}`)
  } finally {
    keepPickerSaving.value = false
  }
}

const openLevels = (row: SiteProfileRow): void => {
  if (!hasLevels(row)) {
    ElMessage.info('该站点还没有等级数据，点击「刷新数据」后再试')
    return
  }
  levelDialogSite.value = row.nickname || row.site_code
  levelDialogUserClass.value = row.user_class || ''
  // VIP / 工作组 / 管理组不是靠做种量升上去的等级，默认不展示；当前账号在该组时只留它自己
  const visible = splitLevelsForUser(row.levels || [], row.user_class || '')
  levelDialogLevels.value = visible.levels
  levelDialogHiddenGroups.value = visible.hidden
  levelDialogProfile.value = row
  // 每次打开都回到「下一等级」，避免沿用上一次点选的高等级
  selectedLevelIndex.value = -1
  activePathIndex.value = 0
  levelDialogVisible.value = true
}

/** 当前等级在等级表中命中的唯一行下标（-1 表示没匹配上，此时不高亮任何行） */
const currentLevelIndex = computed<number>(() =>
  findCurrentLevelIndex(levelDialogLevels.value, levelDialogUserClass.value),
)

const isCurrentLevel = (level: UserLevelRow): boolean => {
  if (currentLevelIndex.value < 0) return false
  return levelDialogLevels.value.indexOf(level) === currentLevelIndex.value
}

/** 等级阶梯：已达成 / 当前 / 下一等级 / 待达成。 */
const levelStepStatus = (index: number): 'done' | 'current' | 'next' | 'waiting' => {
  const current = currentLevelIndex.value
  if (current < 0) return index === 0 ? 'next' : 'waiting'
  if (index === current) return 'current'
  if (index < current) return 'done'
  if (index === current + 1) return 'next'
  return 'waiting'
}

const levelStepLabel = (index: number): string => {
  const status = levelStepStatus(index)
  return status === 'done'
    ? '已达成'
    : status === 'current'
      ? '当前'
      : status === 'next'
        ? '下一等级'
        : '待达成'
}

const levelStepTagType = (index: number): 'info' | 'success' | 'primary' => {
  const status = levelStepStatus(index)
  return status === 'current' ? 'success' : status === 'next' ? 'primary' : 'info'
}

/** FAQ 里的等级名常带括号别名，阶梯上只显示主名，完整名放 title。 */
const levelShortName = (name: string): string => {
  const short = name.replace(/[（(][^）)]*[）)]/g, '').trim()
  return short || name
}

const nextLevelIndex = computed(() =>
  currentLevelIndex.value >= 0 ? currentLevelIndex.value + 1 : 0,
)

/** 用户在阶梯上点选的等级；-1 表示跟随「下一等级」自动定位 */
const selectedLevelIndex = ref(-1)

/** 进度卡当前展示的等级：点选优先，未点选时取下一等级；越界时夹到等级表范围内 */
const activeLevelIndex = computed<number>(() => {
  const total = levelDialogLevels.value.length
  if (total === 0) return -1
  const desired = selectedLevelIndex.value >= 0 ? selectedLevelIndex.value : nextLevelIndex.value
  return Math.min(Math.max(desired, 0), total - 1)
})

const activeLevel = computed<UserLevelRow | null>(
  () => levelDialogLevels.value[activeLevelIndex.value] || null,
)

/** 点选的等级是否已经达成（不高于当前等级） */
const activeLevelReached = computed<boolean>(() => {
  const current = currentLevelIndex.value
  if (current < 0 || activeLevelIndex.value < 0) return false
  return activeLevelIndex.value <= current
})

/** 进度卡标题：当前等级 / 已达成 / 下一等级 / 距当前 N 级 */
const activeLevelTitle = computed<string>(() => {
  const current = currentLevelIndex.value
  const index = activeLevelIndex.value
  if (index < 0 || current < 0) return '目标等级'
  if (index === current) return '当前等级'
  if (index < current) return '已达成'
  return index === current + 1 ? '下一等级' : `距当前 ${index - current} 级`
})

const requirementProfile = computed<RequirementProfile>(() => {
  const row = levelDialogProfile.value
  return {
    uploaded: row?.uploaded || 0,
    downloaded: row?.downloaded || 0,
    ratio: row?.ratio ?? 0,
    bonus: row?.bonus || 0,
    seed_bonus: row?.seed_bonus || 0,
    join_date: row?.join_date || '',
    seeding: row?.seeding || 0,
    invites: row?.invites || 0,
  }
})

/** 把站点原文升级条件拆成结构化指标；有多条路线时各自独立计算进度。 */
const upgradePaths = computed<UpgradePath[]>(() =>
  buildUpgradePaths(activeLevel.value?.requirements || '', requirementProfile.value),
)

watch(
  upgradePaths,
  (paths) => {
    if (!paths.length) {
      activePathIndex.value = 0
      return
    }
    let best = 0
    paths.forEach((path, index) => {
      if (path.percent > paths[best].percent) best = index
    })
    activePathIndex.value = best
  },
  { immediate: true },
)

const activePath = computed<UpgradePath | null>(
  () => upgradePaths.value[activePathIndex.value] || upgradePaths.value[0] || null,
)

const etaText = computed(() => {
  if (activeLevelReached.value) return '已达成'
  // 注册时间按周（多数 NexusPHP）或按天（青蛙）写，两种都认
  const joinItem = activePath.value?.items.find(
    (item) =>
      (item.key === 'join_weeks' || item.key === 'join_days') &&
      item.met === false &&
      item.remainingText,
  )
  return joinItem ? `预计还需 ${joinItem.remainingText}` : '预计时间待补充'
})

const levelRowClass = ({ row }: { row: UserLevelRow }): string => {
  const classes: string[] = []
  if (isCurrentLevel(row)) classes.push('level-row-current')
  if (row.keep_alive) classes.push('level-row-keep')
  return classes.join(' ')
}

const loadProfiles = async () => {
  loading.value = true
  try {
    const response = await axios.get('/api/sites/profiles')
    const data = response.data?.data
    rows.value = Array.isArray(data) ? data : []
  } catch (error) {
    ElMessage.error('加载站点数据失败')
    console.error('加载站点数据失败:', error)
  } finally {
    loading.value = false
  }
}

/**
 * 加载指定日期的当日增量。
 * 参数：picked 为日期选择器传来的 YYYY-MM-DD；不传则用当前选择（空表示今天，由后端按服务器时区判定）。
 */
const loadDaily = async (picked?: string | null) => {
  dailyLoading.value = true
  try {
    const target = typeof picked === 'string' ? picked : dailyDate.value
    const response = await axios.get('/api/sites/profiles/daily', {
      params: target ? { date: target } : {},
    })
    const data = response.data?.data as DailySummary | undefined
    if (!data) {
      dailyRows.value = []
      void renderPies()
      return
    }
    dailyRows.value = Array.isArray(data.rows) ? data.rows : []
    // 后端会把「今天」解析成具体日期，回写后日期选择器才有值可显示。
    dailyDate.value = data.date || target
    dailyTotals.value = {
      uploaded: data.total_uploaded_delta || 0,
      downloaded: data.total_downloaded_delta || 0,
      bonus: data.total_bonus_delta || 0,
      seed_bonus: data.total_seed_bonus_delta || 0,
    }
    // 数据变了就重绘饼图（在累计 Tab 下会被 renderPies 自己跳过）
    void renderPies()
  } catch (error) {
    ElMessage.error('加载当日增量失败')
    console.error('加载当日增量失败:', error)
  } finally {
    dailyLoading.value = false
  }
}

/** 站点首页地址：补齐协议，供站点名链接使用（base_url 可能是 hdsky.me 这种裸域名） */
const siteHomeUrl = (row: SiteProfileRow): string => {
  const raw = String(row.base_url || '')
    .trim()
    .replace(/\/+$/, '')
  if (!raw) return ''
  return /^https?:\/\//i.test(raw) ? raw : `https://${raw}`
}

/** 该站点是否正在单站刷新 */
const isSiteRefreshing = (row: SiteProfileRow): boolean =>
  Boolean(row.site_code) && Boolean(siteRefreshing.value[row.site_code])

/**
 * 把刷新接口返回的站点数据合并回现有表格。
 * 参数：list 为接口返回的合并视图。
 * 说明：只按 site_code 覆盖对应行（新站追加），这样单站刷新不会让整表重排、滚动位置不会跳。
 */
const applyProfiles = (list: SiteProfileRow[]) => {
  const indexed = new Map(rows.value.map((row) => [row.site_code, row]))
  list.forEach((item) => {
    if (!item || !item.site_code) return
    const existed = indexed.get(item.site_code)
    if (existed) {
      Object.assign(existed, item)
      return
    }
    rows.value.push(item)
    indexed.set(item.site_code, item)
  })
}

/** 从 axios 异常里取出可读的错误原因（优先后端 message） */
const errorReason = (error: unknown): string => {
  const err = error as {
    response?: { data?: { message?: unknown } }
    code?: unknown
    message?: unknown
  }
  const backendMessage = err?.response?.data?.message
  if (typeof backendMessage === 'string' && backendMessage.trim()) return backendMessage.trim()
  if (err?.code === 'ECONNABORTED') return '请求超时，站点长时间没有响应'
  if (typeof err?.message === 'string' && err.message.trim()) return err.message.trim()
  return '未知错误'
}

/**
 * 单站刷新：点击表格里的站点名时只抓这一站。
 * 参数：row 为表格当前行。
 * 失败场景：站点未配置凭据、Cookie 失效、页面结构变更、超时、接口报错 —— 都会弹错误提示，
 * 并把后端返回的具体原因（status=fail 的 message）带出来，同时行内状态标签同步变红。
 * 副作用：向该站点发起 HTTP 请求并写库；成功会写当天快照，因此顺带刷新「当日增量」表。
 */
const refreshSite = async (row: SiteProfileRow) => {
  const code = String(row.site_code || '').trim()
  if (!code || refreshing.value || siteRefreshing.value[code]) return
  const name = row.nickname || code
  siteRefreshing.value[code] = true
  try {
    const response = await axios.post(
      '/api/sites/profiles/refresh',
      { site_codes: [code] },
      { timeout: 120000 },
    )
    const data = response.data?.data
    if (!Array.isArray(data)) {
      ElMessage.error(`${name} 获取失败：服务未返回站点数据`)
      return
    }
    applyProfiles(data as SiteProfileRow[])
    const updated = (data as SiteProfileRow[]).find((item) => item.site_code === code)
    if (!updated) {
      ElMessage.error(`${name} 获取失败：未找到该站点的配置，请检查站点管理里的站点代码`)
      return
    }
    if (updated.status === 'ok') {
      await loadDaily(dailyDate.value || undefined)
      ElMessage.success(`${name} 数据已更新`)
      return
    }
    if (updated.status === 'skip') {
      ElMessage.warning(`${name} 未配置 Cookie / Passkey，无法获取数据`)
      return
    }
    const reason = String(updated.message || '').trim() || '站点未返回可解析的账号数据'
    ElMessage({
      type: 'error',
      message: `${name} 获取失败：${reason}`,
      duration: 8000,
      showClose: true,
    })
  } catch (error) {
    ElMessage({
      type: 'error',
      message: `${name} 获取失败：${errorReason(error)}`,
      duration: 8000,
      showClose: true,
    })
    console.error('单站刷新失败:', error)
  } finally {
    delete siteRefreshing.value[code]
  }
}

const refreshAll = async () => {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const response = await axios.post('/api/sites/profiles/refresh', {})
    const data = response.data?.data
    if (Array.isArray(data)) {
      // 顺序即站点管理页的顺序（后端按 sort_order 返回），不在这里重排。
      rows.value = data as SiteProfileRow[]
    } else {
      await loadProfiles()
    }
    const failed = rows.value.filter((row) => row.status === 'fail').length
    // 抓取成功会写当天快照，顺便把「当日增量」表刷新成最新值。
    await loadDaily(dailyDate.value || undefined)
    if (failed > 0) {
      ElMessage.warning(`站点数据已刷新，${failed} 个站点获取失败`)
    } else {
      ElMessage.success('站点数据已刷新')
    }
  } catch (error) {
    ElMessage.error('刷新站点数据失败')
    console.error('刷新站点数据失败:', error)
  } finally {
    refreshing.value = false
  }
}

// 定时刷新：每天按服务器时区（Asia/Shanghai）在若干个时刻自动抓取全部站点。
const scheduleVisible = ref(false)
const scheduleSaving = ref(false)
const scheduleLoaded = ref(false)
/** 一天最多允许几个刷新点（与后端 maxSiteDataRefreshTimes 保持一致） */
const maxScheduleTimes = 12
/** times 里可以有多个 HH:mm：用户加几个点就每天刷几次 */
const scheduleForm = ref<{ enabled: boolean; times: string[] }>({ enabled: false, times: ['06:00'] })

const scheduleTimesLabel = computed(() =>
  scheduleForm.value.times.filter(Boolean).join('、'),
)

const scheduleSummary = computed(() =>
  scheduleForm.value.enabled
    ? `每天 ${scheduleTimesLabel.value || '—'} 自动刷新全部站点`
    : '设置每天自动刷新的时间点（当前未启用）',
)

/** 新增一个刷新点：默认取上一个点 +6 小时，撞了就填 12:00 */
const addScheduleTime = () => {
  const times = scheduleForm.value.times
  if (times.length >= maxScheduleTimes) return
  const last = times[times.length - 1] || '06:00'
  const [hour, minute] = last.split(':').map((part) => Number(part) || 0)
  const nextHour = (hour + 6) % 24
  let candidate = `${`${nextHour}`.padStart(2, '0')}:${`${minute}`.padStart(2, '0')}`
  if (times.includes(candidate)) candidate = '12:00'
  times.push(candidate)
}

const removeScheduleTime = (index: number) => {
  const times = scheduleForm.value.times
  if (times.length <= 1) return
  times.splice(index, 1)
}

const loadSchedule = async () => {
  try {
    const response = await axios.get('/api/config/site_data_refresh')
    const data = response.data?.data as
      | { enabled?: boolean; time?: string; times?: string[] }
      | undefined
    if (data) {
      // 兼容老配置：只有 time 时当成单个刷新点
      const times =
        Array.isArray(data.times) && data.times.length
          ? [...data.times]
          : [data.time || '06:00']
      scheduleForm.value = { enabled: Boolean(data.enabled), times }
    }
    scheduleLoaded.value = true
  } catch (error) {
    console.error('加载定时刷新配置失败:', error)
  }
}

const openSchedule = () => {
  scheduleVisible.value = true
  if (!scheduleLoaded.value) void loadSchedule()
}

const saveSchedule = async () => {
  // 时间点允许被用户清空（el-time-select 可清），提交前先过滤，至少要留一个
  const times = scheduleForm.value.times.map((item) => (item || '').trim()).filter(Boolean)
  if (!times.length) {
    ElMessage.warning('至少保留一个执行时间')
    return
  }
  scheduleSaving.value = true
  try {
    const response = await axios.post('/api/config/site_data_refresh', {
      enabled: scheduleForm.value.enabled,
      // times 是新字段；time 继续带上第一项，旧版本后端也能用
      times,
      time: times[0],
    })
    const data = response.data?.data as
      | { enabled?: boolean; time?: string; times?: string[] }
      | undefined
    if (data) {
      const saved =
        Array.isArray(data.times) && data.times.length ? [...data.times] : [data.time || times[0]]
      scheduleForm.value = { enabled: Boolean(data.enabled), times: saved }
    }
    ElMessage.success(
      scheduleForm.value.enabled
        ? `已设置为每天 ${scheduleTimesLabel.value} 自动刷新`
        : '已关闭定时刷新',
    )
    scheduleVisible.value = false
  } catch (error) {
    ElMessage.error(`保存定时刷新失败：${errorReason(error)}`)
  } finally {
    scheduleSaving.value = false
  }
}

type SiteEditorForm = {
  nickname: string
  base_url: string
  special_tracker_domain: string
  cookie: string
  passkey: string
  can_publish: boolean
  check_cookie_enabled: boolean
  /** 0=正式发种，1=候选申请 */
  publish_mode: number
  sort_order: number
}

/** 站点编辑弹窗的表单初始值（空态，避免模板里出现 undefined） */
const emptySiteEditorForm = (): SiteEditorForm => ({
  nickname: '',
  base_url: '',
  special_tracker_domain: '',
  cookie: '',
  passkey: '',
  can_publish: true,
  check_cookie_enabled: true,
  publish_mode: 0,
  sort_order: 0,
})

const siteEditorVisible = ref(false)
const siteEditorLoading = ref(false)
const siteEditorSaving = ref(false)
/** 正在编辑的站点标识（sites.site，不随名称/域名变化） */
const siteEditorCode = ref('')
const siteEditorForm = ref<SiteEditorForm>(emptySiteEditorForm())
/** 打开弹窗时的快照：用于判断是否真的改了（没改就不发请求、提示「未修改」） */
const siteEditorOriginal = ref<SiteEditorForm>(emptySiteEditorForm())

/** 开关字段归一：接口可能给 0/1、true/false 或 null（null 表示用站点默认值） */
const toBoolFlag = (value: unknown, fallback: boolean): boolean => {
  if (value === null || value === undefined || value === '') return fallback
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed !== 0 : fallback
}

/** 把后端站点配置行转成表单（字段缺失时按站点默认值兜底） */
const formFromSite = (site: Record<string, unknown>): SiteEditorForm => ({
  nickname: String(site.nickname ?? '').trim(),
  base_url: String(site.base_url ?? '').trim(),
  special_tracker_domain: String(site.special_tracker_domain ?? '').trim(),
  cookie: String(site.cookie ?? ''),
  passkey: String(site.passkey ?? ''),
  can_publish: toBoolFlag(site.can_publish, true),
  check_cookie_enabled: toBoolFlag(site.check_cookie_enabled, true),
  publish_mode: Number(site.publish_mode ?? 0) === 1 ? 1 : 0,
  sort_order: Number(site.sort_order ?? 0) || 0,
})

/**
 * 打开站点编辑弹窗（入口：站点列下方的站点标识）。
 * 参数：row 为表格当前行。
 * 说明：单独拉一次 /api/sites/detail 拿完整配置，而不是用列表里的展示字段 ——
 * 列表只有昵称/域名/状态，编辑需要 Cookie、Passkey、可发种等原始值。
 * 失败场景：站点不存在、接口报错都会提示并关闭弹窗。
 */
const openSiteEditor = async (row: SiteProfileRow): Promise<void> => {
  const code = String(row.site_code || row.nickname || '').trim()
  if (!code) {
    ElMessage.warning('该站点缺少站点标识，无法编辑')
    return
  }
  siteEditorCode.value = code
  siteEditorForm.value = emptySiteEditorForm()
  siteEditorOriginal.value = emptySiteEditorForm()
  siteEditorVisible.value = true
  siteEditorLoading.value = true
  try {
    const response = await axios.get('/api/sites/detail', { params: { site: code } })
    const site = response.data?.site as Record<string, unknown> | undefined
    if (!site) {
      siteEditorVisible.value = false
      ElMessage.error(response.data?.message || '未找到该站点的配置')
      return
    }
    const form = formFromSite(site)
    siteEditorForm.value = form
    siteEditorOriginal.value = { ...form }
  } catch (error) {
    siteEditorVisible.value = false
    ElMessage.error(`读取站点配置失败：${errorReason(error)}`)
  } finally {
    siteEditorLoading.value = false
  }
}

/**
 * 保存站点编辑弹窗。
 * 说明：只提交基础配置（白名单字段），后端用「部分更新」写库，
 * 标签 / Dupe 规则 / 禁转名单等不会被覆盖。
 * 副作用：更新 sites 表；改排序会强制刷新全局站点顺序缓存，改名称/域名会同步当前行。
 */
const saveSiteEditor = async (): Promise<void> => {
  if (siteEditorSaving.value) return
  const form = siteEditorForm.value
  const nickname = form.nickname.trim()
  if (!nickname) {
    ElMessage.warning('站点名称不能为空')
    return
  }
  if (JSON.stringify(form) === JSON.stringify(siteEditorOriginal.value)) {
    siteEditorVisible.value = false
    ElMessage.info('配置未修改')
    return
  }

  siteEditorSaving.value = true
  try {
    const response = await axios.post('/api/sites/patch', {
      site: siteEditorCode.value,
      nickname,
      base_url: form.base_url.trim(),
      special_tracker_domain: form.special_tracker_domain.trim(),
      cookie: form.cookie.trim(),
      passkey: form.passkey.trim(),
      can_publish: form.can_publish ? 1 : 0,
      check_cookie_enabled: form.check_cookie_enabled ? 1 : 0,
      publish_mode: form.publish_mode,
      sort_order: form.sort_order,
    })
    if (!response.data?.success) {
      ElMessage.error(response.data?.message || '保存失败')
      return
    }

    const before = siteEditorOriginal.value
    const identityChanged =
      before.nickname !== nickname ||
      before.base_url !== form.base_url.trim() ||
      before.cookie !== form.cookie.trim() ||
      before.passkey !== form.passkey.trim()
    const orderChanged = before.sort_order !== form.sort_order

    // 行内同步，避免整表重拉造成排序/滚动位置跳动。
    const row = rows.value.find((item) => item.site_code === siteEditorCode.value)
    if (row) {
      row.nickname = nickname
      row.base_url = form.base_url.trim()
    }
    if (orderChanged) {
      await ensureSiteOrderLoaded(true)
    }
    siteEditorVisible.value = false
    ElMessage.success(
      identityChanged ? '站点配置已更新，可点刷新图标重新抓取该站数据' : '站点配置已更新',
    )
  } catch (error) {
    ElMessage.error(`保存失败：${errorReason(error)}`)
  } finally {
    siteEditorSaving.value = false
  }
}

onMounted(() => {
  window.addEventListener('resize', resizeCharts)
  loadProfiles()
  loadDaily()
  loadSchedule()
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', resizeCharts)
  uploadPieChart = disposePie(uploadPieChart)
  downloadPieChart = disposePie(downloadPieChart)
  trendChart?.dispose()
  trendChart = null
})
</script>

<style scoped>
.site-data-view {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  padding: 12px;
  box-sizing: border-box;
}

.glass-rounded {
  border-radius: 12px;
}

.page-header {
  padding: 14px 18px;
  flex-shrink: 0;
}

.header-main {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  flex-wrap: wrap;
}

.page-title {
  margin: 0;
  font-size: 18px;
  font-weight: 600;
}

.header-actions {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.search-input {
  width: 220px;
}

.status-select {
  width: 140px;
}

/* 标签筛选：多选后要能放下两个标签 + 计数，给得比状态下拉宽一点 */
.tag-select {
  width: 190px;
}

/* 两个 Tab 撑满剩余高度：header 固定，content 滚动区交给表格自己 */
.data-tabs {
  display: flex;
  flex-direction: column;
  flex: 1 1 0;
  min-height: 0;
}

.data-tabs :deep(.el-tabs__header) {
  flex-shrink: 0;
  margin-bottom: 8px;
}

.data-tabs :deep(.el-tabs__content) {
  flex: 1 1 0;
  min-height: 0;
}

.data-tabs :deep(.el-tab-pane) {
  height: 100%;
  box-sizing: border-box;
}

.tab-pane-body {
  display: flex;
  flex-direction: column;
  gap: 12px;
  height: 100%;
  min-height: 0;
}

/* 站点数与总做种数固定同一行（5 列），窄屏再折行 */
.summary-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-shrink: 0;
  padding: 0 6px;
  min-height: 24px;
  /* 收紧与汇总卡之间的间距，避免多出一整段空白 */
  margin-bottom: -6px;
}

.summary-head-title {
  font-size: 13px;
  font-weight: 600;
  color: var(--el-text-color-regular);
}

/* 收起后把五个关键指标压成一行文字，空间让给明细表 */
.summary-head-inline {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 14px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
  flex: 1 1 auto;
  min-width: 0;
}

/* 展开/收起汇总按钮固定贴右：收起时靠 inline 指标撑开，展开时靠 margin-left 顶到同一位置，
   两种状态的按钮位置与外观保持一致。 */
.collapse-toggle {
  margin-left: auto;
  flex: 0 0 auto;
}

.summary-head-inline b {
  font-weight: 600;
  color: var(--el-text-color-primary);
}

/* 成功/失败用语义色，收起状态下也能一眼看出有没有站点抓取失败 */
.summary-head-inline .is-ok b {
  color: var(--el-color-success);
}

.summary-head-inline .is-fail b {
  color: var(--el-color-danger);
}

.summary-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
  flex-shrink: 0;
}

.summary-card {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 16px;
}

.summary-label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.summary-value {
  font-size: 22px;
  font-weight: 600;
  line-height: 1.2;
}

.summary-sub {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

/* 卡片里的成功/失败数也上语义色，和收起后的汇总行保持一致 */
.summary-sub .is-ok {
  color: var(--el-color-success);
  font-weight: 600;
}

.summary-sub .is-fail {
  color: var(--el-color-danger);
  font-weight: 600;
}

.table-card {
  display: flex;
  flex-direction: column;
  flex: 1 1 0;
  min-height: 0;
  padding: 8px;
  overflow: hidden;
}

/* 主表列多、需要更多可视高度；增量表列少，给它略小的份额 */
.table-card.main-card {
  flex-grow: 1.35;
}

.card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 2px 6px 8px;
  flex-shrink: 0;
  flex-wrap: wrap;
}

.card-head-left,
.card-head-right {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.card-title {
  font-size: 13px;
  font-weight: 600;
}

.card-hint {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

.table-body {
  flex: 1;
  min-height: 0;
}

/* 当日上传 / 下载两张饼图并排；高度固定，剩下的空间留给增量明细表 */
.chart-row {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  flex: 0 0 auto;
}

.chart-card {
  display: flex;
  flex-direction: column;
  min-width: 0;
  padding: 8px;
}

.chart-card .card-head {
  padding: 2px 4px 4px;
}

.chart-total {
  font-size: 13px;
  font-weight: 600;
  white-space: nowrap;
}

.chart-total.is-up {
  color: var(--el-color-success);
}

.chart-total.is-down {
  color: var(--el-color-primary);
}

.pie-chart {
  width: 100%;
  height: 190px;
}

.pie-empty {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 190px;
  padding: 0;
}

.daily-date {
  width: 148px;
}

/* Tab 顶部工具栏：日期选择、区间/粒度/站点筛选都放这里，一行控制整个 Tab */
.tab-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  flex-wrap: wrap;
  padding: 8px 12px;
  flex-shrink: 0;
}

.toolbar-left {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.toolbar-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.card-date-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

/* 上传趋势 */
.trend-toolbar {
  flex-direction: column;
  align-items: stretch;
  gap: 8px;
  padding: 10px 12px;
}

.trend-toolbar-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.trend-range {
  max-width: 260px;
}

.trend-site-select {
  min-width: 240px;
  max-width: 360px;
}

.trend-summary {
  display: flex;
  align-items: center;
  gap: 18px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.trend-summary b {
  font-size: 13px;
  color: var(--el-text-color-primary);
}

.trend-summary b.is-up {
  color: var(--el-color-success);
}

.trend-card {
  flex: 1;
  min-height: 0;
}

.trend-chart {
  flex: 1;
  min-height: 280px;
  width: 100%;
}

.card-range {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  white-space: nowrap;
}

.site-data-table,
.daily-table {
  width: 100%;
  height: 100%;
  background: transparent;
}

.site-cell {
  display: flex;
  flex-direction: column;
  gap: 2px;
  line-height: 1.3;
}

.site-name {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-weight: 500;
}

/* 站点名 = 跳首页的链接 */
.site-link {
  color: var(--el-color-primary);
  text-decoration: none;
  cursor: pointer;
}

.site-link:hover {
  text-decoration: underline;
}

.site-link.is-muted {
  color: var(--el-text-color-regular);
  cursor: default;
}

.site-link.is-muted:hover {
  text-decoration: none;
}

/* 刷新只认这个图标：单独点击才抓数据，抓取中转圈、全量刷新中置灰 */
.site-refresh-icon {
  flex-shrink: 0;
  padding: 2px;
  border-radius: 4px;
  font-size: 12px;
  color: var(--el-text-color-placeholder);
  cursor: pointer;
  transition:
    color 0.15s ease,
    background-color 0.15s ease;
}

.site-refresh-icon:hover {
  color: var(--el-color-primary);
  background: rgba(64, 158, 255, 0.14);
}

.site-refresh-icon.is-loading {
  color: var(--el-color-primary);
  cursor: progress;
}

.site-refresh-icon.is-blocked {
  color: var(--el-text-color-disabled);
  cursor: not-allowed;
}

.site-refresh-icon.is-blocked:hover {
  color: var(--el-text-color-disabled);
  background: transparent;
}

.site-code {
  font-size: 11px;
  color: var(--el-text-color-secondary);
}

/* 站点标识 = 编辑入口：低调的小按钮，悬停才显色，避免抢站点名的视觉焦点 */
.site-code-action {
  display: inline-flex;
  align-items: center;
  align-self: flex-start;
  gap: 3px;
  padding: 0 4px;
  margin-left: -4px;
  border: none;
  border-radius: 4px;
  background: transparent;
  font-family: inherit;
  font-size: 11px;
  line-height: 1.6;
  cursor: pointer;
  transition:
    color 0.15s ease,
    background-color 0.15s ease;
}

.site-code-action .el-icon {
  font-size: 11px;
}

.site-code-action:hover {
  color: var(--el-color-primary);
  background: rgba(64, 158, 255, 0.12);
}

.site-editor-body {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.site-editor-body .form-hint {
  width: 100%;
  margin-top: 2px;
  font-size: 12px;
  line-height: 1.5;
  color: var(--el-text-color-secondary);
}

.site-editor-body .form-hint.inline {
  width: auto;
  margin-top: 0;
  margin-left: 8px;
}

.site-editor-body .sort-order-input {
  width: 140px;
}

.muted {
  color: var(--el-text-color-placeholder);
}

/* 表格里的等级标签：可点击查看该站完整等级体系；站点自定义等级名较长，超长省略 */
.level-tag {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
  cursor: pointer;
}

.level-tag.no-levels {
  cursor: help;
  opacity: 0.75;
}

/* 保号等级统一用蓝色（用户明确要求：保号线必须一眼可见） */
.keep-tag {
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
  color: #1a73e8;
  border-color: #66a9ff;
  background: rgba(64, 158, 255, 0.12);
  font-weight: 600;
}

/* 手动指定的保号等级：虚线边框 + 右侧小笔图标，一眼区分「站点文案识别」与「人工设定」 */
.keep-tag.is-manual {
  border-style: dashed;
  border-color: #1a73e8;
}

/* 保号等级单元格：整块可点击，悬停才显示编辑图标，避免表格看起来满是按钮 */
.keep-cell {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  max-width: 100%;
  padding: 2px 4px;
  border: none;
  border-radius: 6px;
  background: transparent;
  cursor: pointer;
  transition: background 0.15s ease;
}

.keep-cell:hover {
  background: rgba(64, 158, 255, 0.1);
}

.keep-cell .keep-empty {
  color: var(--el-text-color-placeholder);
  font-size: 12px;
}

.keep-edit-icon {
  font-size: 12px;
  color: #1a73e8;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.keep-cell:hover .keep-edit-icon {
  opacity: 0.85;
}

.keep-picker-select {
  width: 100%;
  margin-top: 12px;
}

.keep-picker-hint {
  margin: 8px 0 0;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.level-stepper {
  display: flex;
  align-items: flex-start;
  gap: 0;
  overflow-x: auto;
  padding: 12px 8px 10px;
  margin-bottom: 14px;
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 12px;
  background: var(--el-fill-color-lighter);
}

.level-step {
  position: relative;
  flex: 0 0 82px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  text-align: center;
  padding: 2px 2px 4px;
  border-radius: 10px;
  cursor: pointer;
  transition:
    background-color 0.15s ease,
    box-shadow 0.15s ease;
}

.level-step:hover {
  background: var(--el-fill-color);
}

/* 当前进度卡对应的等级：描边 + 浅底，避免和「当前等级」的绿色状态混淆 */
.level-step.is-selected {
  background: var(--el-color-primary-light-9);
  box-shadow: inset 0 0 0 2px var(--el-color-primary);
}

.level-step:not(:first-child)::before {
  content: '';
  position: absolute;
  top: 13px;
  right: 50%;
  width: 100%;
  height: 3px;
  background: #d0d7de;
  z-index: 0;
}

.level-step.is-done::before,
.level-step.is-current::before {
  background: #0ea5e9;
}

.step-circle {
  position: relative;
  z-index: 1;
  width: 26px;
  height: 26px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  border: 2px solid #cbd5e1;
  background: #fff;
  color: #64748b;
  font-size: 12px;
  font-weight: 700;
}

.is-done .step-circle {
  border-color: #0ea5e9;
  background: #0ea5e9;
  color: #fff;
}

.is-current .step-circle {
  border-color: #0ea5e9;
  background: #22c55e;
  color: #fff;
  box-shadow: 0 0 0 3px rgba(14, 165, 233, 0.25);
}

.is-next .step-circle {
  border: 2px solid #0ea5e9;
  color: #0284c7;
}

.step-name {
  max-width: 78px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 11px;
  line-height: 1.3;
  color: var(--el-text-color-regular);
}

.is-current .step-name {
  color: #16a34a;
  font-weight: 600;
}

.is-next .step-name {
  color: #0284c7;
  font-weight: 600;
}

.step-tag {
  margin: 0;
  height: 17px;
  padding: 0 4px;
  font-size: 10px;
  line-height: 15px;
}

.step-keep-tag {
  position: absolute;
  top: 0;
  right: 2px;
  height: 16px;
  padding: 0 4px;
  font-size: 10px;
  line-height: 14px;
  background: #1a73e8;
  border-color: #1a73e8;
}

.next-level-card {
  border: 1px solid var(--el-border-color-lighter);
  border-radius: 14px;
  overflow: hidden;
  margin-bottom: 12px;
}

.next-level-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 20px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.next-level-target {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.target-icon {
  font-size: 20px;
  color: #0ea5e9;
}

.next-level-label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.next-level-name {
  font-size: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.eta-tag {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.next-level-body {
  display: flex;
  gap: 24px;
  padding: 18px 20px 22px;
}

.progress-side {
  flex: 0 0 190px;
  display: flex;
  flex-direction: column;
  align-items: center;
  border-right: 1px solid var(--el-border-color-lighter);
  padding-right: 18px;
}

.progress-value {
  font-size: 22px;
  font-weight: 700;
  color: #0ea5e9;
}

.progress-caption {
  margin-top: 8px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.progress-main {
  margin-top: 2px;
  font-size: 22px;
  font-weight: 700;
}

.progress-sub {
  margin-top: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.requirement-side {
  flex: 1;
  min-width: 0;
}

.path-switcher {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-bottom: 12px;
}

.path-hint {
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.requirement-head,
.requirement-row {
  display: grid;
  grid-template-columns: 150px minmax(140px, 1fr) minmax(140px, 1fr);
  align-items: center;
  gap: 12px;
}

.requirement-head {
  padding: 8px 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  background: var(--el-fill-color-lighter);
  border-radius: 6px;
}

.requirement-row {
  min-height: 44px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--el-border-color-lighter);
  font-size: 13px;
}

.requirement-item {
  display: flex;
  align-items: center;
  gap: 8px;
  font-weight: 500;
}

.requirement-target {
  color: var(--el-text-color-regular);
}

.requirement-current {
  font-weight: 600;
  color: var(--el-text-color-primary);
}

.requirement-current.is-met {
  color: #16a34a;
}

.requirement-current.is-unknown {
  color: var(--el-text-color-placeholder);
  font-weight: 400;
}

.requirement-empty {
  padding: 16px 12px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}

.level-raw-collapse {
  margin-top: 12px;
}

.level-table {
  font-size: 12px;
}

.level-table-note {
  margin: 0 0 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.level-name {
  font-weight: 500;
}

.level-name.is-current {
  color: var(--el-color-success);
  font-weight: 700;
}

.level-name.is-keep {
  color: #1a73e8;
  font-weight: 700;
}

.level-flag {
  margin-left: 6px;
}

.keep-flag {
  background: #1a73e8;
  border-color: #1a73e8;
  color: #fff;
}

.level-text {
  font-size: 12px;
  line-height: 1.6;
  color: var(--el-text-color-regular);
  /* 北洋园（classes.php）等站的升级条件/权限是一行一条，后端按换行拼接，要保留换行 */
  white-space: pre-line;
}

/* 当前等级行（绿底）与保号等级行（蓝色左边框 + 浅蓝底），保号优先级更高 */
:deep(.level-row-current) {
  background: rgba(103, 194, 58, 0.08) !important;
}

:deep(.level-row-keep) {
  background: rgba(64, 158, 255, 0.1) !important;
}

:deep(.level-row-keep td:first-child) {
  border-left: 3px solid #1a73e8;
}

/* 弹层被 teleport 到 body，scoped 样式管不到，单独用全局选择器限制宽度 */
:global(.level-dialog) {
  max-width: 94vw;
}

.mono {
  font-variant-numeric: tabular-nums;
}

.ratio-value {
  font-variant-numeric: tabular-nums;
}

.ratio-low {
  color: var(--el-color-danger);
  font-weight: 600;
}

.ratio-mid {
  color: var(--el-color-warning);
}

.ratio-good {
  color: var(--el-color-success);
}

/* 当日增量：正增长绿色、负增长（站点扣量）红色 */
.delta-positive {
  color: var(--el-color-success);
}

.delta-negative {
  color: var(--el-color-danger);
  font-weight: 600;
}

.baseline-text {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

/* 基线不是昨天（服务几天没跑）时提示这条增量实际跨了多天 */
.baseline-text.is-cross-day {
  color: var(--el-color-warning);
  font-weight: 600;
}

.time-text {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

@media (max-width: 1200px) {
  .summary-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}

@media (max-width: 900px) {
  .summary-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }

  .chart-row {
    grid-template-columns: minmax(0, 1fr);
  }

  .search-input {
    flex: 1;
    min-width: 160px;
  }

  .next-level-body {
    flex-direction: column;
    gap: 14px;
  }

  .progress-side {
    flex: none;
    width: 100%;
    flex-direction: row;
    flex-wrap: wrap;
    justify-content: center;
    border-right: none;
    border-bottom: 1px solid var(--el-border-color-lighter);
    padding: 0 0 14px;
  }

  .progress-caption {
    width: 100%;
    text-align: center;
  }

  .requirement-head,
  .requirement-row {
    grid-template-columns: minmax(90px, 1fr) minmax(100px, 1fr) minmax(100px, 1fr);
    gap: 6px;
  }
}

/* 定时刷新弹窗 */
.schedule-times {
  display: flex;
  flex-direction: column;
  gap: 8px;
  align-items: flex-start;
}

.schedule-time-row {
  display: flex;
  align-items: center;
  gap: 6px;
}

.schedule-time-remove {
  padding: 0 4px;
}

.schedule-time-select {
  width: 180px;
}

.schedule-dialog .form-hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  line-height: 1.5;
}

.schedule-dialog .form-hint.inline {
  margin-left: 10px;
}
</style>
