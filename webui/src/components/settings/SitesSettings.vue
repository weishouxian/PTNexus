<template>
  <div class="cookie-view-container">
    <!-- 1. 顶部固定区域 -->
    <div class="top-actions cookie-actions glass-pagination">
      <el-form :model="cookieCloudForm" inline class="cookie-cloud-form">
        <el-form-item label="CookieCloud">
          <el-input
            v-model="cookieCloudForm.url"
            placeholder="http://127.0.0.1:8088"
            clearable
            style="width: 200px"
          ></el-input>
        </el-form-item>
        <el-form-item label="KEY">
          <el-input
            v-model="cookieCloudForm.key"
            placeholder="KEY (UUID)"
            clearable
            style="width: 100px"
          ></el-input>
        </el-form-item>
        <el-form-item label="端对端密码">
          <el-input
            v-model="cookieCloudForm.e2e_password"
            type="password"
            show-password
            placeholder="端对端加密密码"
            clearable
            style="width: 125px"
          ></el-input>
        </el-form-item>
        <el-form-item>
          <!-- [修改] 合并后的按钮 -->
          <el-button
            type="primary"
            size="large"
            @click="handleSaveAndSync"
            :loading="isCookieActionLoading"
          >
            <el-icon>
              <Refresh />
            </el-icon>
            <span>同步Cookie</span>
          </el-button>
          <el-link
            href="https://github.com/weishouxian/PTNexus/releases/latest/download/pt-nexus-browser-extension.zip"
            target="_blank"
            rel="noopener noreferrer"
            type="primary"
            class="browser-extension-link"
          >
            下载浏览器插件
          </el-link>
        </el-form-item>
      </el-form>

      <div class="right-action-group">
        <template v-if="isSortMode">
          <el-button type="success" @click="handleSaveSortOrder" :loading="isSortSaving">
            保存排序
          </el-button>
          <el-button @click="cancelSortMode">取消</el-button>
        </template>
        <template v-else>
          <el-button
            :type="isSortMode ? 'primary' : 'default'"
            @click="enterSortMode"
            :icon="Rank"
          >
            排序
          </el-button>
          <el-button
            :type="selectedSites.length ? 'primary' : 'default'"
            :disabled="selectedSites.length === 0"
            :icon="PriceTag"
            @click="openBatchTagDialog"
          >
            批量打标签{{ selectedSites.length ? `（${selectedSites.length}）` : '' }}
          </el-button>
          <el-button :icon="Upload" :loading="isImporting" @click="triggerImport">导入</el-button>
          <el-button :icon="Download" :loading="isExporting" @click="handleExport">导出</el-button>
          <el-input
            v-model="searchQuery"
            placeholder="搜索站点昵称/标识/官组"
            clearable
            :prefix-icon="Search"
            class="search-input"
          />
        </template>
      </div>
      <!-- 站点配置导入：隐藏的原生文件选择框，由「导入」按钮触发 -->
      <input
        ref="importFileInput"
        type="file"
        accept="application/json,.json"
        class="hidden-file-input"
        @change="handleImportFileChange"
      />
    </div>

    <!-- 标签筛选条件：位于表头上方，可多选；命中任一标签（或未设置标签）的站点即显示（与搜索、站点范围筛选为 AND） -->
    <div v-if="!isSortMode" class="tag-filter-row glass-pagination">
      <span class="tag-filter-row-label">标签筛选</span>
      <el-select
        v-model="activeTagFilters"
        multiple
        filterable
        clearable
        collapse-tags
        collapse-tags-tooltip
        placeholder="选择标签（可多选，命中任一即显示）"
        class="tag-filter-select"
      >
        <template #tag="{ data, deleteTag }">
          <span
            v-for="item in data"
            :key="String(item.value)"
            class="site-tag-chip site-tag-chip--closable"
            :class="{ 'site-tag-chip--unset': String(item.value) === TAG_FILTER_NO_TAG }"
            :style="
              String(item.value) === TAG_FILTER_NO_TAG
                ? undefined
                : siteTagStyle(String(item.value))
            "
          >
            {{ item.currentLabel ?? item.value }}
            <el-icon class="site-tag-chip-close" @click.stop="deleteTag($event, item)">
              <Close />
            </el-icon>
          </span>
        </template>
        <el-option v-for="tag in allTagOptions" :key="tag" :label="tag" :value="tag">
          <span class="site-tag-chip" :style="siteTagStyle(tag)">{{ tag }}</span>
        </el-option>
        <!-- 特殊筛选项：一个标签都没设置的站点（不是真实标签，用灰色虚线胶囊区分） -->
        <el-option
          :key="TAG_FILTER_NO_TAG"
          :label="TAG_FILTER_NO_TAG_LABEL"
          :value="TAG_FILTER_NO_TAG"
        >
          <span class="site-tag-chip site-tag-chip--unset">{{ TAG_FILTER_NO_TAG_LABEL }}</span>
        </el-option>
      </el-select>
      <span v-if="activeTagFilters.length" class="tag-filter-row-hint">
        {{ tagFilterHint }}
      </span>
    </div>

    <!-- 2. 中间可滚动内容区域 -->
    <div class="settings-view" v-loading="isSitesLoading">
      <el-table
        :data="isSortMode ? dragSortedSites : paginatedSites"
        class="settings-table glass-table"
        :class="{ 'sort-mode-table': isSortMode }"
        height="100%"
        :row-class-name="getRowClassName"
        @sort-change="handleSortChange"
        @row-click="handleRowClick"
        @selection-change="handleSelectionChange"
        :default-sort="defaultSort"
        :row-style="{ cursor: 'pointer' }"
        row-key="id"
        ref="sitesTableRef"
      >
        <el-table-column
          v-if="!isSortMode"
          type="selection"
          width="46"
          align="center"
          fixed="left"
        />
        <el-table-column v-if="isSortMode" label="" width="50" align="center" class-name="drag-handle-col">
          <template #default="scope">
            <span
              class="drag-handle"
              draggable="true"
              @dragstart="onDragStart($event, scope.$index)"
              @dragover.prevent="onDragOver($event, scope.$index)"
              @drop="onDrop($event, scope.$index)"
              @dragend="onDragEnd"
              :data-index="scope.$index"
            >
              <el-icon :size="16"><Rank /></el-icon>
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="nickname"
          label="站点昵称"
          width="100"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        />
        <el-table-column
          prop="sort_order"
          label="排序"
          width="70"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <span v-if="Number(scope.row.sort_order) > 0">{{ scope.row.sort_order }}</span>
            <span v-else style="color: var(--el-text-color-placeholder)">-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="support_role"
          label="支持"
          width="100"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <span v-if="getSiteRole(scope.row) === 'both'" class="role-tag role-both">
              源站/目标站
            </span>
            <span v-else-if="getSiteRole(scope.row) === 'source'" class="role-tag role-source">
              源站
            </span>
            <span v-else-if="getSiteRole(scope.row) === 'target'" class="role-tag role-target">
              目标站
            </span>
          </template>
        </el-table-column>
        <el-table-column
          prop="can_publish"
          label="可发种"
          width="80"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <el-tag v-if="scope.row.can_publish" type="success" size="small">是</el-tag>
            <el-tag v-else type="info" size="small">否</el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="dupe_check_enabled"
          label="Dupe 校验"
          width="100"
          align="center"
        >
          <template #default="scope">
            <span
              v-if="!DUPE_CHECK_SUPPORTED_SITES.includes(String(scope.row.site || '').toLowerCase())"
              style="color: var(--el-text-color-placeholder)"
              >-</span
            >
            <el-tooltip
              v-else-if="scope.row.dupe_check_enabled"
              :content="dupeRuleSummary(scope.row)"
              placement="top"
            >
              <el-tag
                :type="dupeTagType(scope.row)"
                size="small"
              >
                {{ dupeTagText(scope.row) }}
              </el-tag>
            </el-tooltip>
            <el-tag v-else type="info" size="small">关</el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="site"
          label="站点标识"
          width="100"
          show-overflow-tooltip
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        />
        <el-table-column
          prop="base_url"
          label="基础URL"
          width="150"
          show-overflow-tooltip
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        />
        <el-table-column
          prop="group"
          label="官组"
          show-overflow-tooltip
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        />
        <el-table-column prop="tags" label="标签" min-width="150">
          <template #default="scope">
            <div v-if="scope.row.tags?.length" class="site-tag-list">
              <span
                v-for="tag in scope.row.tags"
                :key="tag"
                class="site-tag-chip"
                :style="siteTagStyle(tag)"
              >
                {{ tag }}
              </span>
            </div>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column prop="forbidden_transfer_sites" label="禁转站点" min-width="150">
          <template #default="scope">
            <div v-if="scope.row.forbidden_transfer_sites?.length" class="site-tag-list">
              <el-tag
                v-for="target in scope.row.forbidden_transfer_sites"
                :key="target"
                size="small"
                type="warning"
              >
                {{ target }}
              </el-tag>
            </div>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="speed_limit"
          label="限速"
          width="100"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <div
              style="
                display: flex;
                justify-content: center;
                align-items: center;
                width: 100%;
                height: 100%;
              "
            >
              <el-tag v-if="scope.row.speed_limit == 0" type="error" size="small">
                当前不限速，重启恢复默认限速
              </el-tag>
              <el-tag v-else-if="scope.row.speed_limit > 999" type="success" size="small">
                不限速
              </el-tag>
              <el-tag v-else type="primary" size="small"> {{ scope.row.speed_limit }} MB/s </el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          prop="ratio_threshold"
          label="分享率阈值"
          width="120"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <el-tag v-if="scope.row.ratio_threshold" size="small" type="warning">
              ≥ {{ scope.row.ratio_threshold }}
            </el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="seed_speed_limit"
          label="出种限速"
          width="110"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <el-tag
              v-if="scope.row.ratio_threshold && scope.row.seed_speed_limit !== null"
              size="small"
              type="info"
            >
              {{ scope.row.seed_speed_limit }} MB/s
            </el-tag>
            <span v-else>-</span>
          </template>
        </el-table-column>
        <el-table-column
          prop="has_cookie"
          label="Cookie"
          width="100"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <el-tag v-if="scope.row.site === 'rousi'" type="info"> 无需配置 </el-tag>
            <el-tag v-else :type="scope.row.has_cookie ? 'success' : 'danger'">
              {{ scope.row.has_cookie ? '已配置' : '未配置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column
          prop="has_passkey"
          label="Passkey"
          width="100"
          align="center"
          sortable="custom"
          :sort-orders="['ascending', 'descending']"
        >
          <template #default="scope">
            <el-tag
              v-if="['hddolby', 'm-team', 'hdtime', 'rousi'].includes(scope.row.site)"
              :type="scope.row.has_passkey ? 'success' : 'danger'"
            >
              {{ scope.row.has_passkey ? '已配置' : '未配置' }}
            </el-tag>
            <el-tag v-else type="success"> 自动获取 </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="180" align="center" fixed="right">
          <template #default="scope">
            <el-button type="primary" :icon="Edit" link @click="handleOpenDialog(scope.row)">
              编辑
            </el-button>
            <el-button type="danger" :icon="Delete" link @click.stop="handleDelete(scope.row)">
              删除
            </el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <!-- 3. 底部固定区域 -->
    <div class="settings-footer glass-pagination" v-show="!isSortMode">
      <el-radio-group v-model="siteFilter" @change="handleFilterChange">
        <el-radio-button label="existing_supported">已有支持站点</el-radio-button>
        <el-radio-button label="supported">所有支持站点</el-radio-button>
        <el-radio-button label="all">所有站点</el-radio-button>
      </el-radio-group>
      <div class="pagination-container">
        <div class="page-size-text">{{ pagination.pageSize }} 条/页</div>
        <el-pagination
          v-model:current-page="pagination.currentPage"
          v-model:page-size="pagination.pageSize"
          :total="pagination.total"
          layout="total, prev, pager, next, jumper"
          background
        />
      </div>
    </div>

    <!-- 编辑站点对话框 -->
    <el-dialog
      v-model="dialogVisible"
      title="编辑站点"
      width="700px"
      :close-on-click-modal="false"
      class="site-edit-dialog"
    >
      <el-form :model="siteForm" ref="siteFormRef" label-width="140px" label-position="left">
        <el-form-item label="站点标识" prop="site" required>
          <el-input v-model="siteForm.site" placeholder="例如：pt" disabled></el-input>
          <div class="form-tip">站点标识不可修改。</div>
        </el-form-item>
        <el-form-item label="站点昵称" prop="nickname" required>
          <el-input v-model="siteForm.nickname" placeholder="例如：PT站"></el-input>
        </el-form-item>
        <el-form-item label="基础URL" prop="base_url">
          <el-input v-model="siteForm.base_url" placeholder="例如：pt.com"></el-input>
          <div class="form-tip">用于拼接种子详情页链接。</div>
        </el-form-item>
        <el-form-item label="Tracker域名" prop="special_tracker_domain">
          <el-input
            v-model="siteForm.special_tracker_domain"
            placeholder="例如：pt-tracker.com"
          ></el-input>
          <div class="form-tip">
            如果站点的Tracker域名与主域名的二级域名（则域名去掉前缀后缀部分）不同，请在此填写。
          </div>
        </el-form-item>
        <el-form-item label="关联官组" prop="group">
          <el-input v-model="siteForm.group" placeholder="例如：PT, PTWEB"></el-input>
          <div class="form-tip">用于识别种子所属发布组，多个组用英文逗号(,)分隔。</div>
        </el-form-item>
        <el-form-item label="标签" prop="tags">
          <el-select
            v-model="siteForm.tags"
            multiple
            filterable
            clearable
            allow-create
            default-first-option
            collapse-tags
            collapse-tags-tooltip
            placeholder="输入后回车即可新增标签，如：电影、通用"
            style="width: 100%"
          >
            <template #tag="{ data, deleteTag }">
              <span
                v-for="item in data"
                :key="String(item.value)"
                class="site-tag-chip site-tag-chip--closable"
                :style="siteTagStyle(String(item.value))"
              >
                {{ item.currentLabel ?? item.value }}
                <el-icon class="site-tag-chip-close" @click.stop="deleteTag($event, item)">
                  <Close />
                </el-icon>
              </span>
            </template>
            <el-option v-for="tag in allTagOptions" :key="tag" :label="tag" :value="tag">
              <span class="site-tag-chip" :style="siteTagStyle(tag)">{{ tag }}</span>
            </el-option>
          </el-select>
          <div class="form-tip">
            给站点打标签（可多个）。发布时可按标签一键勾选同一标签下的所有站点；列表页可在表头上方按标签筛选。
          </div>
        </el-form-item>
        <el-form-item label="禁转站点" prop="forbidden_transfer_sites">
          <el-select
            v-model="siteForm.forbidden_transfer_sites"
            multiple
            filterable
            clearable
            collapse-tags
            collapse-tags-tooltip
            placeholder="选择禁止转种的目标站点"
            style="width: 100%"
          >
            <el-option
              v-for="target in transferSiteOptions"
              :key="String(target.id ?? target.site)"
              :label="target.nickname || target.site"
              :value="target.nickname || target.site"
            />
          </el-select>
          <div class="form-tip">种子的制作组归属本站时，将禁止转种到所选站点，可多选。</div>
        </el-form-item>
        <el-form-item label="可发种站" prop="can_publish">
          <el-switch v-model="siteForm.can_publish" />
          <div class="form-tip">关闭后，该站点在发种选择时将置灰不可选择。</div>
        </el-form-item>
        <el-form-item label="多音轨策略" prop="audio_track_policy">
          <el-select v-model="siteForm.audio_track_policy" style="width: 100%">
            <el-option :value="0" label="未设置（取第一条音轨）" />
            <el-option :value="1" label="第一条音轨" />
            <el-option :value="2" label="码率最高" />
            <el-option :value="3" label="规格最高" />
          </el-select>
          <div class="form-tip">
            发布时种子含多条音轨，按此策略选出一条作为音频编码并同步修改发种标题。未设置时默认取第一条音轨。
          </div>
        </el-form-item>
        <el-form-item v-if="isDupeCheckVisible" label="Dupe 校验" prop="dupe_check_enabled">
          <el-switch v-model="siteForm.dupe_check_enabled" />
          <div class="form-tip">
            开启后，发布前会用 {{ dupeSearchIdLabel }} 加类型、媒介、分辨率、音视频编码到站点检索；
            若已存在「制作组相同且体积差在 {{ formatDupeTolerance(siteForm.dupe_size_tolerance_bytes) }} 以内」的种子，则判定为重复并拒绝发布，
            <strong>判为重复的种子不会添加到下载器</strong>，避免重复下载与重复做种。默认关闭。
          </div>
        </el-form-item>
        <el-form-item
          v-if="isDupeCheckVisible && siteForm.dupe_check_enabled"
          label="体积容差"
          prop="dupe_size_tolerance_bytes"
        >
          <el-input-number
            v-model="siteForm.dupe_size_tolerance_mb"
            :min="0"
            :max="102400"
            :step="64"
            :precision="0"
            style="width: 100%"
          />
          <div class="form-tip">
            单位 MB。默认 {{ DEFAULT_DUPE_TOLERANCE_MB }} MB；设为 0 表示要求体积完全一致才算重复。
          </div>
        </el-form-item>
        <el-form-item
          v-if="isDupeCheckVisible && siteForm.dupe_check_enabled"
          label="查重规则"
        >
          <div class="dupe-rules">
            <div class="form-tip">
              按媒介分别设置判定维度：<strong>只对下面添加过的媒介做查重</strong>，未添加的媒介走兜底规则（未开启兜底则跳过）。
              规则已按媒介区分，因此「<strong>媒介</strong>」恒为判定维度（已锁定、不可取消）；
              勾选「分辨率 / 视频编码 / 音频编码」会作为站点检索条件，勾选「文件大小 / 制作组」由本系统逐条比对。
            </div>
            <el-table
              :data="dupeRuleRows"
              size="small"
              class="dupe-rules-table"
              empty-text="尚未添加媒介 —— 该站点不会执行 dupe 校验"
            >
              <el-table-column label="媒介" min-width="110">
                <template #default="{ row }">
                  <div class="dupe-rule-medium">{{ dupeMediumLabel(row.medium) }}</div>
                  <div class="dupe-rule-key">{{ row.medium }}</div>
                </template>
              </el-table-column>
              <el-table-column label="判定维度" min-width="250">
                <template #default="{ row }">
                  <el-checkbox-group v-model="row.dimensions" size="small">
                    <el-checkbox
                      v-for="dim in dupeOptions?.dimensions || []"
                      :key="dim.value"
                      :value="dim.value"
                      :disabled="!dim.supported || dim.value === DUPE_LOCKED_DIMENSION"
                    >
                      {{ dim.label }}
                    </el-checkbox>
                  </el-checkbox-group>
                </template>
              </el-table-column>
              <el-table-column label="" width="60" align="right">
                <template #default="{ row }">
                  <el-button link type="danger" @click="removeDupeRule(row)">删除</el-button>
                </template>
              </el-table-column>
            </el-table>
            <div class="dupe-rule-actions">
              <el-select
                v-model="pendingDupeMedium"
                filterable
                clearable
                size="small"
                placeholder="选择要添加的媒介"
                :loading="isDupeOptionsLoading"
                class="dupe-medium-select"
              >
                <el-option-group v-for="group in dupeMediumGroups" :key="group.value" :label="group.label">
                  <el-option
                    v-for="item in group.items"
                    :key="item.medium"
                    :label="item.label"
                    :value="item.medium"
                  />
                </el-option-group>
              </el-select>
              <el-button size="small" type="primary" :disabled="!pendingDupeMedium" @click="addDupeRule">
                添加媒介
              </el-button>
              <el-button
                size="small"
                :disabled="!(dupeOptions?.mediums?.length ?? 0)"
                @click="addAllDupeMediums"
              >
                添加全部媒介
              </el-button>
            </div>
            <div v-if="unsupportedRuleDimensions.length" class="form-tip dupe-rule-warning">
              ⚠️ 该站点没有声明「{{ unsupportedRuleDimensions.join('、') }}」的检索参数，这几项勾选后不会生效（已置灰）。
            </div>
            <div v-else-if="dupeOptions && !dupeOptions.mediums.length" class="form-tip dupe-rule-warning">
              ⚠️ 没读到该站点的媒介清单，无法在此添加规则（请检查站点 YAML 的 mappings.medium）。
            </div>
            <div class="dupe-fallback">
              <div class="dupe-fallback-head">
                <el-switch v-model="dupeFallbackEnabled" size="small" />
                <span class="dupe-fallback-title">兜底规则</span>
                <span class="dupe-fallback-desc">
                  开启后，未在上面单独添加的媒介（含媒介无法识别时）都按这条规则查重；关闭则这些媒介直接跳过、不校验。
                </span>
              </div>
              <el-checkbox-group
                v-if="dupeFallbackEnabled"
                v-model="dupeFallbackDimensions"
                size="small"
                class="dupe-fallback-dims"
              >
                <el-checkbox
                  v-for="dim in dupeOptions?.dimensions || []"
                  :key="dim.value"
                  :value="dim.value"
                  :disabled="!dim.supported"
                >
                  {{ dim.label }}
                </el-checkbox>
              </el-checkbox-group>
            </div>
          </div>
        </el-form-item>
        <el-form-item label="排序序号" prop="sort_order">
          <el-input-number
            v-model="siteForm.sort_order"
            :min="0"
            :max="9999"
            style="width: 100%"
          />
          <div class="form-tip">数值越小越靠前，0 表示不参与自定义排序（按默认规则排列）。</div>
        </el-form-item>
        <el-form-item label="Cookie" prop="cookie">
          <el-input
            v-model="siteForm.cookie"
            type="textarea"
            :rows="3"
            :placeholder="siteForm.site === 'rousi' ? '无需设置' : '从浏览器获取的Cookie字符串'"
            :disabled="siteForm.site === 'rousi'"
          ></el-input>
        </el-form-item>
        <el-form-item label="Passkey" prop="passkey">
          <el-input
            v-model="siteForm.passkey"
            :placeholder="
              siteForm.site === 'm-team' ? '控制台 → 实验室 → 存取令牌（36 位 UUID）' : '站点的Passkey'
            "
          ></el-input>
          <div
            v-if="siteForm.site === 'hddolby' || siteForm.site === 'pthome'"
            class="form-tip"
            style="color: #409eff; font-weight: bold"
          >
            杜比/铂金家的passkey为种子详情页复制种子链接时downhash=后的部分
          </div>
          <div
            v-else-if="siteForm.site === 'm-team'"
            class="form-tip"
            style="color: #409eff; font-weight: bold"
          >
            馒头这里要填「存取令牌」而不是站点 Passkey：控制台 → 实验室 → 存取令牌，生成结果是 36 位 UUID（形如 57b1fa6c-4444-3333-2222-1b1111111111）。填成站点 Passkey（32 位）发种会报 code=1「key無效」。
          </div>
          <div
            v-else-if="siteForm.site === 'rousi'"
            class="form-tip"
            style="color: #409eff; font-weight: bold"
          >
            获取肉丝passkey-
            <el-button
              type="primary"
              size="small"
              tag="a"
              href="https://rousi.pro/account?tab=passkey"
              target="_blank"
              rel="noopener noreferrer"
            >
              跳转
            </el-button>
          </div>
        </el-form-item>
        <el-form-item label="上传限速 (MB/s)" prop="speed_limit">
          <el-input-number
            v-model="siteForm.speed_limit"
            :min="0"
            :max="1000"
            style="width: 100%"
          />
          <div class="form-tip" style="color: red; font-size: 12px">
            填写 0 重启恢复默认；超过 999 显示不限速
          </div>
        </el-form-item>
        <el-form-item label="分享率阈值" prop="ratio_threshold">
          <el-input-number
            v-model="siteForm.ratio_threshold"
            :min="1.2"
            :max="100"
            :step="0.1"
            :precision="1"
            style="width: 100%"
          />
          <div class="form-tip" style="font-size: 12px">默认 3.0，达到后触发出种限速</div>
        </el-form-item>
        <el-form-item label="出种限速 (MB/s)" prop="seed_speed_limit">
          <el-input-number
            v-model="siteForm.seed_speed_limit"
            :min="0"
            :max="1000"
            style="width: 100%"
          />
          <div class="form-tip" style="color: #409eff; font-size: 12px">默认 5 MB/s</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="dialogVisible = false">取消</el-button>
          <el-button type="primary" @click="handleSave" :loading="isSaving"> 保存 </el-button>
        </span>
      </template>
    </el-dialog>

    <!-- 批量打标签对话框：作用于列表页勾选的站点 -->
    <el-dialog
      v-model="batchTagDialogVisible"
      title="批量打标签"
      width="520px"
      :close-on-click-modal="false"
      class="site-batch-tag-dialog"
    >
      <el-form label-width="72px" label-position="left">
        <el-form-item label="操作">
          <el-radio-group v-model="batchTagMode">
            <el-radio-button label="add">添加</el-radio-button>
            <el-radio-button label="remove">移除</el-radio-button>
            <el-radio-button label="replace">覆盖</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="标签">
          <el-select
            v-model="batchTagSelection"
            multiple
            filterable
            clearable
            allow-create
            default-first-option
            collapse-tags
            collapse-tags-tooltip
            placeholder="输入后回车即可新增标签，如：电影、通用"
            style="width: 100%"
          >
            <template #tag="{ data, deleteTag }">
              <span
                v-for="item in data"
                :key="String(item.value)"
                class="site-tag-chip site-tag-chip--closable"
                :style="siteTagStyle(String(item.value))"
              >
                {{ item.currentLabel ?? item.value }}
                <el-icon class="site-tag-chip-close" @click.stop="deleteTag($event, item)">
                  <Close />
                </el-icon>
              </span>
            </template>
            <el-option v-for="tag in allTagOptions" :key="tag" :label="tag" :value="tag">
              <span class="site-tag-chip" :style="siteTagStyle(tag)">{{ tag }}</span>
            </el-option>
          </el-select>
        </el-form-item>
      </el-form>
      <div class="form-tip batch-tag-hint">{{ batchTagHint }}</div>
      <div class="form-tip batch-tag-targets">作用于：{{ batchTagTargetText }}</div>
      <template #footer>
        <span class="dialog-footer">
          <el-button @click="batchTagDialogVisible = false">取消</el-button>
          <el-button type="primary" :loading="isBatchTagSaving" @click="handleBatchTagSubmit">
            确定
          </el-button>
        </span>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, computed, watch } from 'vue'
import axios from 'axios'
import { ElMessageBox } from 'element-plus'
import {
  Close,
  Delete,
  Download,
  Edit,
  PriceTag,
  Rank,
  Refresh,
  Search,
  Upload,
} from '@element-plus/icons-vue'
import { ElMessage } from '@/utils/uiNotify'
import { siteTagKey, siteTagKeys } from '@/utils/siteTag'
import { siteTagStyle } from '@/utils/siteTagColor'

type SiteConfig = {
  id: number | string | null
  site: string
  nickname: string
  base_url?: string
  special_tracker_domain?: string
  group?: string
  tags?: string[]
  forbidden_transfer_sites?: string[]
  cookie?: string
  passkey?: string
  speed_limit?: number | null
  ratio_threshold?: number | null
  seed_speed_limit?: number | null
  has_cookie?: boolean
  has_passkey?: boolean
  can_publish?: boolean | number
  dupe_check_enabled?: boolean | number
  dupe_size_tolerance_bytes?: number | null
  dupe_rules?: Record<string, string[]> | null
  [key: string]: unknown
}

type SiteStatus = {
  site: string
  is_source?: boolean
  is_target?: boolean
  [key: string]: unknown
}

type CookieCloudForm = {
  url: string
  key: string
  e2e_password: string
}

type SortOrder = 'ascending' | 'descending' | null

type SortState = {
  prop: string
  order: SortOrder
}

type PaginationState = {
  currentPage: number
  pageSize: number
  total: number
}

type SiteForm = {
  id: number | string | null
  site: string
  nickname: string
  base_url: string
  special_tracker_domain: string
  group: string
  /** 站点自定义标签（发布时可按标签一键勾选站点） */
  tags: string[]
  forbidden_transfer_sites: string[]
  cookie: string
  passkey: string
  speed_limit: number
  ratio_threshold: number
  seed_speed_limit: number
  can_publish: boolean
  sort_order: number
  dupe_check_enabled: boolean
  /** 体积容差（字节），后端存储口径 */
  dupe_size_tolerance_bytes: number
  /** 体积容差（MB），仅用于输入框展示与双向换算 */
  dupe_size_tolerance_mb: number
  /** 按媒介的查重规则：标准媒介键 → 判定维度集合（未列出的媒介不执行 dupe 校验） */
  dupe_rules: Record<string, string[]>
  /** 多音轨选择策略：0=未设置（取第一条）/ 1=第一条音轨 / 2=码率最高 / 3=规格最高 */
  audio_track_policy: number
}

/** dupe 判定维度选项（由 /api/sites/dupe_options 返回） */
type DupeDimensionOption = {
  value: string
  label: string
  /** 该维度依赖站点检索筛选；站点未声明时 supported 为 false，界面需置灰 */
  needs_site_filter: boolean
  supported: boolean
}

/** dupe 规则可选的媒介（由 /api/sites/dupe_options 返回） */
type DupeMediumOption = {
  medium: string
  label: string
  /** 站点上传表单里的取值，用于把同义媒介分组展示 */
  site_value: string
}

type DupeOptions = {
  enabled: boolean
  mediums: DupeMediumOption[]
  dimensions: DupeDimensionOption[]
  /** 兜底规则在规则表里的保留键（由后端下发，前端不硬编码） */
  fallback_medium: string
}

/** 规则编辑器的行结构（提交前转换成 dupe_rules 对象） */
type DupeRuleRow = {
  medium: string
  dimensions: string[]
}

// 已实现 dupe 检索能力的站点（configs/<site>.yaml 里有 dupe_check.enabled），只有这些站点显示开关。
// 判定能力以站点 YAML 为准；这里只负责入口显隐，后端在站点未声明能力时会跳过校验并写日志说明。
const DUPE_CHECK_SUPPORTED_SITES = [
  'audiences',
  'luckpt',
  'hdhome',
  'pterclub',
  'ourbits',
  'chdbits',
]

// 检索所用的外部 ID 描述：各站 search_area 支持的范围不同（有的站没有豆瓣/IMDb 范围），故按站点区分文案。
// 值与该站 configs/<site>.yaml 的 dupe_check.search_areas 保持一致，避免提示与实际行为不符。
const DUPE_SEARCH_ID_LABELS: Record<string, string> = {
  luckpt: 'IMDb ID（缺失时按标题检索）',
  hdhome: 'IMDb ID（缺失时按标题检索）',
  chdbits: 'IMDb ID（缺失时按标题检索）',
  pterclub: '豆瓣 / IMDb ID（缺失时按标题检索）',
  ourbits: '豆瓣 / IMDb ID（缺失时按标题检索）',
  audiences: '豆瓣 / IMDb ID',
}

// 体积容差换算：站点管理页以 MB 为单位展示与录入，底层仍按字节存储
// （与后端 DefaultDupeSizeToleranceBytes 一致：默认 1024 MB = 1 GiB = 1073741824 字节）。
const BYTES_PER_MB = 1024 * 1024
const DEFAULT_DUPE_TOLERANCE_MB = 1024
const DEFAULT_DUPE_TOLERANCE_BYTES = DEFAULT_DUPE_TOLERANCE_MB * BYTES_PER_MB

const mbToBytes = (mb: number): number =>
  Math.max(0, Math.round((Number.isFinite(mb) ? mb : 0) * BYTES_PER_MB))

const bytesToMb = (bytes: number): number => {
  const value = Number(bytes)
  if (!Number.isFinite(value) || value < 0) return DEFAULT_DUPE_TOLERANCE_MB
  if (value === 0) return 0
  // 非 0 的值至少回显 1 MB，避免手工写入的极小容差被四舍五入成 0（0 的语义是「体积必须完全一致」）。
  return Math.max(1, Math.round(value / BYTES_PER_MB))
}

// normalizeDupeToleranceBytes 归一化后端返回的容差字节数。
// 参数/返回：value 为后端字段值；返回可直接用于表单的字节数（> 0），非法或缺省时回退默认值。
// 说明：0 是合法取值（表示要求体积完全一致），因此不能像其它数值字段那样把 0 当缺省处理；
// 只有 null / undefined / 非数字 / 负数才回退默认值。
// 副作用：无。
const normalizeDupeToleranceBytes = (value: unknown): number => {
  if (value === null || value === undefined || value === '') return DEFAULT_DUPE_TOLERANCE_BYTES
  const parsed = Number(value)
  if (!Number.isFinite(parsed) || parsed < 0) return DEFAULT_DUPE_TOLERANCE_BYTES
  return parsed
}

// --- dupe 规则（按媒介配置判定维度）---

// 兜底规则在规则表里的保留键（与后端 dupe.DupeFallbackMedium 一致）。
// 读取已保存的规则时用它作键（此时选项接口可能还没返回），保存时优先用后端下发的键。
const DUPE_FALLBACK_MEDIUM = '*'
// 兜底规则默认勾选的维度，与单条规则的默认值一致（等于旧版的全局判定口径）。
const DUPE_DEFAULT_DIMENSIONS = ['size', 'team']
// 规则本身已经按媒介区分，因此「媒介」在该规则里恒为判定维度：界面锁为「已勾选且不可取消」。
// （后端 MatchRule 也会统一补上，两边都做是为了让界面显示与真实生效口径一致。）
const DUPE_LOCKED_DIMENSION = 'medium'

// withLockedDimension 保证维度集合包含被锁定的「媒介」。
const withLockedDimension = (dimensions: string[]): string[] => {
  const set = new Set(dimensions.map((item) => String(item)).filter(Boolean))
  set.add(DUPE_LOCKED_DIMENSION)
  return Array.from(set)
}

// 规则编辑器以「行」为编辑单位，提交时转成 dupe_rules 对象（媒介 → 维度集合）。
const dupeRuleRows = ref<DupeRuleRow[]>([])
// 兜底规则：开启后，未单独配置的媒介都走它。
const dupeFallbackEnabled = ref(false)
const dupeFallbackDimensions = ref<string[]>([...DUPE_DEFAULT_DIMENSIONS])
// 站点可选的媒介与维度，来自后端 /api/sites/dupe_options。
const dupeOptions = ref<DupeOptions | null>(null)
const isDupeOptionsLoading = ref(false)
const pendingDupeMedium = ref('')

// dupeFallbackKey 保存兜底规则时用的键：以后端下发为准，拿不到时用本地常量。
const dupeFallbackKey = computed(() => String(dupeOptions.value?.fallback_medium || DUPE_FALLBACK_MEDIUM))

// rowsFromRules 把规则表转成编辑器行；兜底规则单独渲染，这里必须排除掉。
const rowsFromRules = (rules: Record<string, string[]> | null | undefined): DupeRuleRow[] => {
  if (!rules || typeof rules !== 'object') return []
  return Object.entries(rules)
    .filter(([medium]) => medium !== DUPE_FALLBACK_MEDIUM)
    .map(([medium, dimensions]) => ({
      medium: String(medium),
      dimensions: withLockedDimension(Array.isArray(dimensions) ? dimensions.map((item) => String(item)) : []),
    }))
    .filter((row) => row.medium)
}

// fallbackFromRules 读取规则表里的兜底规则（未开启时返回空数组）。
const fallbackFromRules = (rules: Record<string, string[]> | null | undefined): string[] => {
  if (!rules || typeof rules !== 'object') return []
  const dims = rules[DUPE_FALLBACK_MEDIUM]
  return Array.isArray(dims) ? dims.map((item) => String(item)).filter(Boolean) : []
}

const rulesFromRows = (rows: DupeRuleRow[]): Record<string, string[]> => {
  const rules: Record<string, string[]> = {}
  rows.forEach((row) => {
    const medium = String(row.medium || '').trim()
    if (!medium) return
    // 「媒介」是锁定维度，始终参与；其余维度按勾选情况。
    const dimensions = withLockedDimension(
      (row.dimensions || []).map((item) => String(item).trim()).filter(Boolean),
    )
    rules[medium] = dimensions
  })
  return rules
}

const dupeMediumLabel = (medium: string): string => {
  const found = dupeOptions.value?.mediums.find((item) => item.medium === medium)
  return found?.label || medium
}

// 按站点取值分组：同义媒介（如 UHD Blu-ray 与 UHD DIY 共用同一上传取值）相邻展示，便于批量添加。
const dupeMediumGroups = computed(() => {
  const groups = new Map<string, { value: string; label: string; items: DupeMediumOption[] }>()
  for (const item of dupeOptions.value?.mediums || []) {
    const key = item.site_value || item.medium
    const existing = groups.get(key)
    if (existing) {
      existing.items.push(item)
      continue
    }
    groups.set(key, { value: key, label: item.label, items: [item] })
  }
  return Array.from(groups.values())
})

// 站点不支持（界面上置灰）的筛选维度中文名，用于在规则区给出显式提示。
const unsupportedRuleDimensions = computed(() =>
  (dupeOptions.value?.dimensions || [])
    .filter((item) => item.needs_site_filter && !item.supported)
    .map((item) => item.label),
)

const addDupeRule = () => {
  const medium = String(pendingDupeMedium.value || '').trim()
  if (!medium) return
  if (dupeRuleRows.value.some((row) => row.medium === medium)) {
    ElMessage.info('该媒介已在规则列表中。')
    return
  }
  // 新规则默认勾选「文件大小 + 制作组」（媒介为锁定项，恒定参与）——即旧版的全局判定口径。
  dupeRuleRows.value.push({ medium, dimensions: withLockedDimension([...DUPE_DEFAULT_DIMENSIONS]) })
  pendingDupeMedium.value = ''
}

const removeDupeRule = (row: DupeRuleRow) => {
  dupeRuleRows.value = dupeRuleRows.value.filter((item) => item.medium !== row.medium)
}

// 一键把站点全部媒介按「文件大小 + 制作组」加入规则列表（等价于旧版对所有媒介统一查重）。
const addAllDupeMediums = () => {
  for (const item of dupeOptions.value?.mediums || []) {
    if (dupeRuleRows.value.some((row) => row.medium === item.medium)) continue
    dupeRuleRows.value.push({ medium: item.medium, dimensions: withLockedDimension([...DUPE_DEFAULT_DIMENSIONS]) })
  }
}

const loadDupeOptions = async (siteCode: string) => {
  dupeOptions.value = null
  pendingDupeMedium.value = ''
  const trimmed = String(siteCode || '').trim()
  if (!trimmed) return
  isDupeOptionsLoading.value = true
  try {
    const response = await axios.get(`${API_BASE_URL}/sites/dupe_options`, { params: { site: trimmed } })
    dupeOptions.value = (response.data?.options as DupeOptions) || null
  } catch {
    dupeOptions.value = null
    ElMessage.error('获取 dupe 可选项失败，媒介与维度提示暂不可用。')
  } finally {
    isDupeOptionsLoading.value = false
  }
}

// buildDupeRules 把编辑器状态组装成提交用的规则表（含兜底规则）。
const buildDupeRules = (): Record<string, string[]> => {
  const rules = rulesFromRows(dupeRuleRows.value)
  if (dupeFallbackEnabled.value && dupeFallbackDimensions.value.length > 0) {
    rules[dupeFallbackKey.value] = [...dupeFallbackDimensions.value]
  }
  return rules
}

// --- 状态管理 ---
const isSaving = ref(false) // 用于站点编辑对话框的保存按钮

// --- 站点管理状态 ---
const sitesList = ref<SiteConfig[]>([]) // 存储从后端获取的原始列表
const existingSitesSet = ref<Set<string>>(new Set()) // 存储“有此站点”的标识集合（复用后端 active 规则）
const sitesStatusList = ref<SiteStatus[]>([]) // 存储源/目标站点状态信息
const isSitesLoading = ref(false)
const isCookieActionLoading = ref(false) // [新增] 用于新的"同步Cookie"按钮的加载状态
const cookieCloudForm = ref<CookieCloudForm>({ url: '', key: '', e2e_password: '' })
const searchQuery = ref('')
const siteFilter = ref('existing_supported')

// --- 排序状态 ---
const sortState = ref<SortState>({
  prop: 'sort_order',
  order: 'ascending',
})

const defaultSort = computed(() => ({
  prop: sortState.value.prop,
  order: sortState.value.order,
}))

// --- 分页状态 ---
const pagination = ref<PaginationState>({
  currentPage: 1,
  pageSize: 30,
  total: 0,
})

// --- 对话框状态 ---
const dialogVisible = ref(false)
const siteFormRef = ref<unknown>(null)
const siteForm = ref<SiteForm>({
  id: null,
  site: '',
  nickname: '',
  base_url: '',
  special_tracker_domain: '',
  group: '',
  tags: [],
  forbidden_transfer_sites: [],
  cookie: '',
  passkey: '',
  speed_limit: 0, // 前端显示和输入使用 MB/s 单位
  ratio_threshold: 3.0,
  seed_speed_limit: 5,
  can_publish: true,
  sort_order: 0,
  dupe_check_enabled: false,
  dupe_size_tolerance_bytes: DEFAULT_DUPE_TOLERANCE_BYTES,
  dupe_size_tolerance_mb: DEFAULT_DUPE_TOLERANCE_MB,
  dupe_rules: {},
  audio_track_policy: 0,
})

const API_BASE_URL = '/api'

// --- 列表勾选与批量打标签状态 ---
// 勾选范围只算「当前页」：表格未开 reserve-selection，翻页/改筛选后组件自身会清空选择。
const sitesTableRef = ref<unknown>(null)
const selectedSites = ref<SiteConfig[]>([])
const batchTagDialogVisible = ref(false)
const batchTagMode = ref<'add' | 'remove' | 'replace'>('add')
const batchTagSelection = ref<string[]>([])
const isBatchTagSaving = ref(false)

// --- 计算属性 ---

const sitesStatusMap = computed(() => {
  const map = new Map<string, SiteStatus>()
  for (const status of sitesStatusList.value || []) {
    if (status && status.site) {
      map.set(String(status.site), status)
    }
  }
  return map
})

const getSiteStatus = (site: SiteConfig) => {
  if (!site?.site) return null
  return sitesStatusMap.value.get(String(site.site)) || null
}

const transferSiteOptions = computed(() =>
  (sitesList.value || []).filter(
    (site) => String(site.site || '') !== String(siteForm.value.site || ''),
  ),
)

// 已存在的全部站点标签（去重、按中文排序）：编辑弹窗里供选择，也允许直接新建。
const allTagOptions = computed(() => {
  const set = new Set<string>()
  for (const site of sitesList.value || []) {
    for (const tag of site.tags || []) {
      const trimmed = String(tag || '').trim()
      if (trimmed) set.add(trimmed)
    }
  }
  return Array.from(set).sort((a, b) => a.localeCompare(b, 'zh-CN'))
})

// --- 标签筛选：表头上方下拉框多选标签，命中任一标签（或未设置标签）的站点即显示 ---
const activeTagFilters = ref<string[]>([])

// 「未设置标签」是一个特殊筛选项而不是真实标签：用不可能与真实标签撞车的字面量做值，
// 避免用户真的建了同名标签时产生歧义。
const TAG_FILTER_NO_TAG = '__ptn_no_tag__'
const TAG_FILTER_NO_TAG_LABEL = '未设置标签'

const noTagFilterActive = computed(() =>
  activeTagFilters.value.some((tag) => siteTagKey(tag) === TAG_FILTER_NO_TAG),
)

// 参与「任一命中」比对的真实标签 key 集合（哨兵值不参与）。
const activeTagFilterKeys = computed(
  () =>
    new Set(
      activeTagFilters.value
        .filter((tag) => siteTagKey(tag) !== TAG_FILTER_NO_TAG)
        .map((tag) => siteTagKey(tag)),
    ),
)

// 筛选结果条文案：跟随「是否勾了未设置标签」变化。
const tagFilterHint = computed(() => {
  const total = sortedSites.value.length
  if (noTagFilterActive.value && !activeTagFilterKeys.value.size) {
    return `一个标签都没设置的站点，共 ${total} 个`
  }
  if (noTagFilterActive.value) {
    return `命中任一标签、或未设置标签的站点，共 ${total} 个`
  }
  return `命中任一标签的站点，共 ${total} 个`
})

// 站点标签入库前统一清洗：去掉空串与重复项（忽略大小写）。
const normalizeTags = (tags: unknown): string[] => {
  if (!Array.isArray(tags)) return []
  const seen = new Set<string>()
  const result: string[] = []
  for (const raw of tags) {
    const trimmed = String(raw ?? '').trim()
    if (!trimmed) continue
    const key = trimmed.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    result.push(trimmed)
  }
  return result
}

// 批量打标签作用的目标站点文案：列前几个昵称，其余用数量收口。
const batchTagTargetText = computed(() => {
  const list = selectedSites.value
  if (!list.length) return '未勾选站点'
  const names = list.slice(0, 5).map((site) => String(site.nickname || site.site || ''))
  const suffix = list.length > names.length ? ` 等 ${list.length} 个站点` : ''
  return `${names.join('、')}${suffix}`
})

// 批量打标签的操作说明：跟随模式变化，明确「追加 / 移除 / 覆盖」的后果。
const batchTagHint = computed(() => {
  switch (batchTagMode.value) {
    case 'remove':
      return '从勾选站点上移除所选标签，其余标签保留。'
    case 'replace':
      return '用所选标签替换勾选站点的全部原有标签；不选任何标签即清空这些站点的标签。'
    default:
      return '把所选标签追加到勾选站点的原有标签上，原有标签保留。'
  }
})

// 仅对已实现 dupe 能力的站点显示开关（人人、幸运）。
const isDupeCheckVisible = computed(() =>
  DUPE_CHECK_SUPPORTED_SITES.includes(String(siteForm.value.site || '').toLowerCase()),
)

// 检索所用的外部 ID 描述：按站点区分，未登记时用通用文案。
const dupeSearchIdLabel = computed(
  () =>
    DUPE_SEARCH_ID_LABELS[String(siteForm.value.site || '').toLowerCase()] ?? '豆瓣 / IMDb ID',
)

// 供表单提示文案使用的容差展示。
const formatDupeTolerance = (bytes: number | null | undefined): string => {
  const value = Number(bytes)
  if (!Number.isFinite(value) || value < 0) return `${DEFAULT_DUPE_TOLERANCE_MB} MB`
  // 0 表示要求体积完全一致，直接显示 0 MB。
  return `${bytesToMb(value)} MB`
}

// dupeRuleCount 返回站点单独配置的媒介规则条数（不含兜底规则）。
const dupeRuleCount = (site: SiteConfig): number => {
  const rules = site?.dupe_rules
  if (!rules || typeof rules !== 'object') return 0
  return Object.keys(rules).filter((key) => key !== DUPE_FALLBACK_MEDIUM).length
}

// siteHasDupeFallback 判断站点是否开启了兜底规则。
const siteHasDupeFallback = (site: SiteConfig): boolean => fallbackFromRules(site.dupe_rules).length > 0

// dupeTagText 生成列表页 Dupe 列的标签文案。
const dupeTagText = (site: SiteConfig): string => {
  const count = dupeRuleCount(site)
  const fallback = siteHasDupeFallback(site)
  if (count === 0 && !fallback) return '已开（未配置规则）'
  const parts: string[] = []
  if (count > 0) parts.push(`${count} 条规则`)
  if (fallback) parts.push('兜底')
  return `已开 ${parts.join(' + ')}`
}

// dupeTagType 决定标签样式：没配规则的「已开」要用醒目的红色，避免误以为已生效。
const dupeTagType = (site: SiteConfig): 'warning' | 'danger' =>
  dupeRuleCount(site) === 0 && !siteHasDupeFallback(site) ? 'danger' : 'warning'

// dupeRuleSummary 生成列表页 Dupe 列的悬浮说明。
// 强调「开了开关但没配规则 = 不会查重」，避免把开关误当成已生效。
const dupeRuleSummary = (site: SiteConfig): string => {
  const tolerance = `体积容差 ${formatDupeTolerance(site.dupe_size_tolerance_bytes)}`
  const count = dupeRuleCount(site)
  const fallback = siteHasDupeFallback(site)
  if (count === 0 && !fallback) {
    return `${tolerance}；尚未配置任何媒介规则，该站点当前不会执行查重`
  }
  const scope = count > 0 ? `已配置 ${count} 条媒介规则` : '未单独配置媒介规则'
  return `${tolerance}；${scope}${fallback ? '，并已开启兜底规则（未单独配置的媒介按兜底执行）' : ''}`
}

const getSiteRole = (site: SiteConfig): 'none' | 'both' | 'source' | 'target' => {
  const status = getSiteStatus(site)
  if (!status) return 'none'
  if (status.is_source && status.is_target) return 'both'
  if (status.is_source) return 'source'
  if (status.is_target) return 'target'
  return 'none'
}

const isSiteConfigComplete = (site: SiteConfig) => {
  const hasCookie = Boolean(site?.has_cookie)
  const hasPasskey = Boolean(site?.has_passkey)
  // 与 Passkey 列展示规则保持一致：大多数站点自动获取，仅少数站点需要手动配置
  const needsPasskey = ['hddolby', 'm-team', 'hdtime', 'rousi'].includes(site?.site)
  // 肉丝站点不需要cookie，其他站点需要cookie
  const needsCookie = site?.site !== 'rousi'
  return (!needsCookie || hasCookie) && (!needsPasskey || hasPasskey)
}

const shouldHighlightIncompleteConfig = computed(() =>
  ['existing_supported', 'supported'].includes(siteFilter.value),
)

const normalizeString = (value: unknown) =>
  String(value ?? '')
    .trim()
    .toLowerCase()

const compareStrings = (a: unknown, b: unknown) => {
  const aText = normalizeString(a)
  const bText = normalizeString(b)
  if (!aText && !bText) return 0
  if (!aText) return 1
  if (!bText) return -1
  return aText.localeCompare(bText, 'zh-CN', { numeric: true, sensitivity: 'base' })
}

const compareNullableNumbers = (a: unknown, b: unknown) => {
  const aNull = a === null || a === undefined
  const bNull = b === null || b === undefined
  const aNumber = aNull ? NaN : Number(a)
  const bNumber = bNull ? NaN : Number(b)
  const aInvalid = aNull || Number.isNaN(aNumber)
  const bInvalid = bNull || Number.isNaN(bNumber)
  if (aInvalid && bInvalid) return 0
  if (aInvalid) return 1
  if (bInvalid) return -1
  return aNumber - bNumber
}

const getSupportRank = (site: SiteConfig) => {
  const role = getSiteRole(site)
  if (role === 'both') return 0
  if (role === 'source') return 1
  if (role === 'target') return 2
  return 3
}

const getCookieSortKey = (site: SiteConfig) => {
  if (site?.site === 'rousi') return 2 // 无需配置，放在已配置之后
  return site?.has_cookie ? 1 : 0
}

const getPasskeySortKey = (site: SiteConfig) => {
  const needsPasskey = ['hddolby', 'm-team', 'hdtime', 'rousi'].includes(site?.site)
  if (!needsPasskey) return 2 // 自动获取，放在已配置之后
  return site?.has_passkey ? 1 : 0
}

const compareByProp = (a: SiteConfig, b: SiteConfig, prop: string) => {
  switch (prop) {
    case 'nickname':
      return compareStrings(a?.nickname, b?.nickname)
    case 'support_role':
      return getSupportRank(a) - getSupportRank(b)
    case 'sort_order': {
      const oa = Number(a?.sort_order ?? 0)
      const ob = Number(b?.sort_order ?? 0)
      if (oa === 0 && ob === 0) return 0
      if (oa === 0) return 1
      if (ob === 0) return -1
      return oa - ob
    }
    case 'site':
      return compareStrings(a?.site, b?.site)
    case 'base_url':
      return compareStrings(a?.base_url, b?.base_url)
    case 'group':
      return compareStrings(a?.group, b?.group)
    case 'speed_limit':
      return compareNullableNumbers(a?.speed_limit, b?.speed_limit)
    case 'ratio_threshold':
      return compareNullableNumbers(a?.ratio_threshold, b?.ratio_threshold)
    case 'seed_speed_limit':
      return compareNullableNumbers(a?.seed_speed_limit, b?.seed_speed_limit)
    case 'has_cookie':
      return getCookieSortKey(a) - getCookieSortKey(b)
    case 'has_passkey':
      return getPasskeySortKey(a) - getPasskeySortKey(b)
    case 'can_publish':
      return (a?.can_publish ? 1 : 0) - (b?.can_publish ? 1 : 0)
    default:
      return 0
  }
}

const applySortOrder = (compareResult: number, order: SortOrder) => {
  if (!order) return 0
  return order === 'descending' ? -compareResult : compareResult
}

// 1. 先根据前端搜索框进行过滤
const filteredSites = computed(() => {
  let sites = sitesList.value || []

  if (siteFilter.value === 'existing_supported') {
    sites = sites.filter((site) => {
      const status = getSiteStatus(site)
      const isSupported = Boolean(status?.is_source || status?.is_target)
      return isSupported && existingSitesSet.value.has(site.site)
    })
  } else if (siteFilter.value === 'supported') {
    sites = sites.filter((site) => {
      const status = getSiteStatus(site)
      return Boolean(status?.is_source || status?.is_target)
    })
  }

  // 标签筛选与搜索、站点范围筛选是「同时成立」的关系（AND），
  // 而多个筛选项之间是「任一命中」（OR）：「未设置标签」与普通标签并列参与 OR。
  if (activeTagFilters.value.length) {
    const wantedKeys = activeTagFilterKeys.value
    const includeNoTag = noTagFilterActive.value
    sites = sites.filter((site) => {
      const keys = siteTagKeys(site.tags)
      // 「未设置标签」：一个有效标签都没有的站点（空数组 / 缺字段 / 只有空白项都算）
      if (includeNoTag && keys.length === 0) return true
      return keys.some((key) => wantedKeys.has(key))
    })
  }

  const term = searchQuery.value.trim().toLowerCase()
  if (term) {
    sites = sites.filter((site) => {
      const nickname = (site.nickname || '').toLowerCase()
      const siteIdentifier = (site.site || '').toLowerCase()
      const group = (site.group || '').toLowerCase()
      const tags = (site.tags || []).join(' ').toLowerCase()
      const forbidden = (site.forbidden_transfer_sites || []).join(' ').toLowerCase()
      return (
        nickname.includes(term) ||
        siteIdentifier.includes(term) ||
        group.includes(term) ||
        tags.includes(term) ||
        forbidden.includes(term)
      )
    })
  }

  return sites
})

// 2. 再根据排序规则对过滤后的结果进行排序
const sortedSites = computed(() => {
  const sites = (filteredSites.value || []).slice()

  return sites.sort((a, b) => {
    if (shouldHighlightIncompleteConfig.value) {
      const aIncomplete = isSiteConfigComplete(a) ? 0 : 1
      const bIncomplete = isSiteConfigComplete(b) ? 0 : 1
      if (aIncomplete !== bIncomplete) return bIncomplete - aIncomplete
    }

    const { prop, order } = sortState.value || {}
    const hasUserSort = Boolean(prop && order)

    if (hasUserSort) {
      const result = compareByProp(a, b, prop)
      const ordered = applySortOrder(result, order)
      if (ordered !== 0) return ordered
    } else {
      const result = compareByProp(a, b, 'sort_order')
      if (result !== 0) return result
    }

    const nicknameTie = compareByProp(a, b, 'nickname')
    if (nicknameTie !== 0) return nicknameTie

    return compareByProp(a, b, 'site')
  })
})

// 3. 再根据分页信息对排序后的结果进行切片
const paginatedSites = computed(() => {
  const start = (pagination.value.currentPage - 1) * pagination.value.pageSize
  const end = start + pagination.value.pageSize
  return sortedSites.value.slice(start, end)
})

watch(
  () => sortedSites.value.length,
  (total) => {
    pagination.value.total = total
  },
  { immediate: true },
)

// 监听搜索词变化，如果变化则返回第一页
watch(searchQuery, () => {
  pagination.value.currentPage = 1
})

// 标签筛选变化（表头上方的标签下拉框）时同样回到第一页
watch(activeTagFilters, () => {
  pagination.value.currentPage = 1
})

const handleSortChange = ({ prop, order }: { prop?: string; order?: SortOrder }) => {
  sortState.value = {
    prop: prop || 'sort_order',
    order: order || 'ascending',
  }
  pagination.value.currentPage = 1
}

// --- 拖拽排序状态 ---
const isSortMode = ref(false)
const isSortSaving = ref(false)
const dragSortedList = ref<SiteConfig[]>([])
const dragIndex = ref(-1)
const dragOverIndex = ref(-1)

const dragSortedSites = computed(() => dragSortedList.value)

const enterSortMode = () => {
  // 从 sitesList 构建排序列表，按 sort_order 排序（0 排末尾），tiebreaker 为 nickname
  const all = (sitesList.value || []).slice()
  all.sort((a, b) => {
    const oa = Number(a.sort_order ?? 0)
    const ob = Number(b.sort_order ?? 0)
    if (oa === 0 && ob === 0) return compareStrings(a.nickname, b.nickname)
    if (oa === 0) return 1
    if (ob === 0) return -1
    if (oa !== ob) return oa - ob
    return compareStrings(a.nickname, b.nickname)
  })
  dragSortedList.value = all
  isSortMode.value = true
}

const cancelSortMode = () => {
  isSortMode.value = false
  dragSortedList.value = []
  dragIndex.value = -1
  dragOverIndex.value = -1
}

const onDragStart = (event: DragEvent, index: number) => {
  dragIndex.value = index
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', String(index))
  }
}

const onDragOver = (_event: DragEvent, index: number) => {
  dragOverIndex.value = index
}

const onDrop = (_event: DragEvent, toIndex: number) => {
  const fromIndex = dragIndex.value
  if (fromIndex < 0 || fromIndex === toIndex) return
  const list = dragSortedList.value.slice()
  const [item] = list.splice(fromIndex, 1)
  list.splice(toIndex, 0, item)
  dragSortedList.value = list
  dragIndex.value = -1
  dragOverIndex.value = -1
}

const onDragEnd = () => {
  dragIndex.value = -1
  dragOverIndex.value = -1
}

const handleSaveSortOrder = async () => {
  if (dragSortedList.value.length === 0) return
  isSortSaving.value = true
  try {
    const ids = dragSortedList.value.map((s) => Number(s.id)).filter((id) => id > 0)
    await axios.post(`${API_BASE_URL}/sites/update_order`, { ids })
    ElMessage.success('站点排序已保存')
    isSortMode.value = false
    dragSortedList.value = []
    await fetchSites()
  } catch {
    ElMessage.error('保存排序失败')
  } finally {
    isSortSaving.value = false
  }
}

onMounted(() => {
  fetchCookieCloudSettings()
  fetchSites()
})

// --- 方法 ---

const fetchCookieCloudSettings = async () => {
  try {
    const response = await axios.get(`${API_BASE_URL}/settings`)
    if (response.data && response.data.cookiecloud) {
      cookieCloudForm.value.url = response.data.cookiecloud.url || ''
      cookieCloudForm.value.key = response.data.cookiecloud.key || ''
      cookieCloudForm.value.e2e_password = ''
    }
  } catch {
    ElMessage.error('加载CookieCloud配置失败！')
  }
}

// fetchSites 方法以接受筛选参数
const fetchSites = async () => {
  isSitesLoading.value = true
  try {
    const [allSitesResponse, existingSitesResponse, sitesStatusResponse] = await Promise.all([
      axios.get(`${API_BASE_URL}/sites`, { params: { filter_by_torrents: 'all' } }),
      axios.get(`${API_BASE_URL}/sites`, { params: { filter_by_torrents: 'active' } }),
      axios.get(`${API_BASE_URL}/sites/status`),
    ])

    sitesList.value = Array.isArray(allSitesResponse.data) ? (allSitesResponse.data as SiteConfig[]) : []
    existingSitesSet.value = new Set(
      (Array.isArray(existingSitesResponse.data) ? existingSitesResponse.data : [])
        .map((site) => String((site as Record<string, unknown>)?.site || '').trim())
        .filter(Boolean),
    )
    sitesStatusList.value = Array.isArray(sitesStatusResponse.data)
      ? (sitesStatusResponse.data as SiteStatus[])
      : []
  } catch {
    ElMessage.error('获取站点列表失败！')
  } finally {
    isSitesLoading.value = false
  }
}

// --- 站点配置导入 / 导出 ---
const isExporting = ref(false)
const isImporting = ref(false)
const importFileInput = ref<HTMLInputElement | null>(null)

/** 站点导入接口返回的统计结果 */
type SiteImportResult = {
  success?: boolean
  message?: string
  updated?: number
  unchanged?: number
  missing?: string[]
  invalid?: string[]
  failed?: { site?: string; error?: string }[]
}

const padNumber = (value: number) => String(value).padStart(2, '0')

const formatTimestamp = (date: Date) =>
  `${date.getFullYear()}${padNumber(date.getMonth() + 1)}${padNumber(date.getDate())}` +
  `-${padNumber(date.getHours())}${padNumber(date.getMinutes())}${padNumber(date.getSeconds())}`

const downloadJsonFile = (fileName: string, data: unknown) => {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' })
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = fileName
  document.body.appendChild(link)
  link.click()
  document.body.removeChild(link)
  window.URL.revokeObjectURL(url)
}

const handleExport = () => {
  ElMessageBox.confirm(
    '导出文件包含各站点的 Cookie / Passkey 等凭据，请妥善保管、不要随意外发。',
    '导出站点配置',
    { confirmButtonText: '确认导出', cancelButtonText: '取消', type: 'warning' },
  )
    .then(async () => {
      isExporting.value = true
      try {
        const response = await axios.get(`${API_BASE_URL}/sites/export`)
        const payload = (response.data || {}) as { sites?: unknown[] }
        const count = Array.isArray(payload.sites) ? payload.sites.length : 0
        downloadJsonFile(
          `ptnexus-sites-${formatTimestamp(new Date())}.json`,
          response.data || { sites: [] },
        )
        ElMessage.success(`已导出 ${count} 个站点配置`)
      } catch {
        ElMessage.error('导出站点配置失败')
      } finally {
        isExporting.value = false
      }
    })
    .catch(() => {})
}

const triggerImport = () => {
  const input = importFileInput.value
  if (!input) return
  // 先清空 value，保证连续导入同一个文件也能触发 change
  input.value = ''
  input.click()
}

const handleImportFileChange = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files && input.files[0]
  if (!file) return

  let items: Record<string, unknown>[] = []
  try {
    const text = await file.text()
    const parsed = JSON.parse(text) as unknown
    // 既接受完整导出结构 { sites: [...] }，也接受裸数组
    const rawSites = Array.isArray(parsed)
      ? parsed
      : parsed && typeof parsed === 'object' && Array.isArray((parsed as { sites?: unknown }).sites)
        ? (parsed as { sites: unknown[] }).sites
        : []
    items = rawSites.filter(
      (item): item is Record<string, unknown> => !!item && typeof item === 'object',
    )
  } catch {
    ElMessage.error('文件解析失败，请确认是合法的 JSON 文件')
    return
  }
  if (items.length === 0) {
    ElMessage.error('文件里没有站点数据，请选择由「导出」生成的 JSON 文件')
    return
  }

  try {
    await ElMessageBox.confirm(
      `文件里共 ${items.length} 个站点。导入只填充当前为空或仍是默认值的字段，已手动配置过的值不会被覆盖，也不会新增站点；Cookie / Passkey 只要库里有一个有值，就整组都不动。`,
      '导入站点配置',
      { confirmButtonText: '开始导入', cancelButtonText: '取消', type: 'info' },
    )
  } catch {
    return
  }

  isImporting.value = true
  try {
    const response = await axios.post(`${API_BASE_URL}/sites/import`, { sites: items })
    const result = (response.data || {}) as SiteImportResult
    if (result.success === false) {
      ElMessage.error(String(result.message || '导入失败'))
      return
    }
    const missing = Array.isArray(result.missing) ? result.missing : []
    const failed = Array.isArray(result.failed) ? result.failed : []
    const lines = [
      `更新 ${Number(result.updated) || 0} 个站点，${Number(result.unchanged) || 0} 个无需变更。`,
    ]
    if (missing.length > 0) {
      lines.push(
        `跳过 ${missing.length} 个库中不存在的站点（导入不会新增站点）：` +
          `${missing.slice(0, 5).join('、')}${missing.length > 5 ? ' 等' : ''}`,
      )
    }
    if (failed.length > 0) {
      const failedNames = failed
        .slice(0, 3)
        .map((item) => String(item?.site || ''))
        .filter(Boolean)
        .join('、')
      lines.push(`${failed.length} 个站点写入失败：${failedNames}`)
    }
    ElMessageBox.alert(lines.join('<br/>'), failed.length > 0 ? '导入完成（有失败项）' : '导入完成', {
      confirmButtonText: '知道了',
      type: failed.length > 0 ? 'warning' : 'success',
      dangerouslyUseHTMLString: true,
    }).catch(() => {})
    await fetchSites()
  } catch {
    ElMessage.error('导入站点配置失败')
  } finally {
    isImporting.value = false
  }
}

// 当后端筛选器改变时，重置分页并重新获取数据
const handleFilterChange = () => {
  pagination.value.currentPage = 1
}

const getRowClassName = ({ row }: { row: SiteConfig }) => {
  if (!shouldHighlightIncompleteConfig.value) return ''
  return isSiteConfigComplete(row) ? '' : 'row-config-incomplete'
}

const normalizeSiteForm = (site: SiteConfig): SiteForm => ({
  id: site.id ?? null,
  site: String(site.site || ''),
  nickname: String(site.nickname || ''),
  base_url: String(site.base_url || ''),
  special_tracker_domain: String(site.special_tracker_domain || ''),
  group: String(site.group || ''),
  tags: Array.isArray(site.tags)
    ? site.tags.map((item) => String(item).trim()).filter(Boolean)
    : [],
  forbidden_transfer_sites: Array.isArray(site.forbidden_transfer_sites)
    ? site.forbidden_transfer_sites.map((item) => String(item).trim()).filter(Boolean)
    : [],
  cookie: String(site.cookie || ''),
  passkey: String(site.passkey || ''),
  speed_limit: typeof site.speed_limit === 'number' ? site.speed_limit : Number(site.speed_limit) || 0,
  ratio_threshold:
    typeof site.ratio_threshold === 'number' ? site.ratio_threshold : Number(site.ratio_threshold) || 3.0,
  seed_speed_limit:
    typeof site.seed_speed_limit === 'number' ? site.seed_speed_limit : Number(site.seed_speed_limit) || 5,
  can_publish: site.can_publish == null ? true : Boolean(site.can_publish),
  sort_order: typeof site.sort_order === 'number' ? site.sort_order : Number(site.sort_order) || 0,
  dupe_check_enabled: Boolean(Number(site.dupe_check_enabled) || 0),
  dupe_size_tolerance_bytes: normalizeDupeToleranceBytes(site.dupe_size_tolerance_bytes),
  dupe_size_tolerance_mb: bytesToMb(normalizeDupeToleranceBytes(site.dupe_size_tolerance_bytes)),
  // 统一清洗一遍：去掉空维度与空条目，避免脏数据带进表单。
  dupe_rules: rulesFromRows(rowsFromRules(site.dupe_rules)),
  audio_track_policy: [0, 1, 2, 3].includes(Number(site.audio_track_policy)) ? Number(site.audio_track_policy) : 0,
})

// [新增] 合并后的保存与同步功能
const handleSaveAndSync = async () => {
  const rawURL = String(cookieCloudForm.value.url || '').trim()
  const normalizedURL = (() => {
    if (!rawURL) return ''
    const withScheme = /^https?:\/\//i.test(rawURL) ? rawURL : `http://${rawURL}`
    try {
      const parsed = new URL(withScheme)
      parsed.hash = ''
      return parsed.toString().replace(/\/$/, '')
    } catch {
      return ''
    }
  })()

  // 1. 前端校验
  if (!normalizedURL) {
    ElMessage.warning('CookieCloud URL 格式不正确，请填写 http(s)://host[:port]。')
    return
  }
  if (!cookieCloudForm.value.key) {
    ElMessage.warning('CookieCloud KEY 不能为空！')
    return
  }
  cookieCloudForm.value.url = normalizedURL

  isCookieActionLoading.value = true
  try {
    // 2. 第一步：先保存配置
    await axios.post(`${API_BASE_URL}/settings`, {
      cookiecloud: cookieCloudForm.value,
    })

    // 3. 第二步：配置保存成功后，立即执行同步
    const syncResponse = await axios.post(`${API_BASE_URL}/cookiecloud/sync`, cookieCloudForm.value)

    // 4. 处理同步结果
    if (syncResponse.data.success) {
      // 移除消息中"在 CookieCloud 中另有 X 个未匹配的 Cookie。"部分
      let message = syncResponse.data.message
      if (message) {
        message = message.replace(/在 CookieCloud 中另有 \d+ 个未匹配的 Cookie。?/, '')
      }
      ElMessage.success(`配置已保存. ${message || '同步完成！'}`)
      await fetchSites() // 同步成功后刷新站点列表
    } else {
      ElMessage.error(syncResponse.data.message || '同步失败，但配置已保存。')
    }
  } catch (error: unknown) {
    const errorMessage = axios.isAxiosError(error)
      ? ((error.response?.data as { message?: string } | undefined)?.message || error.message)
      : error instanceof Error
        ? error.message
        : '操作失败，请检查网络或后端服务。'
    ElMessage.error(errorMessage)
  } finally {
    isCookieActionLoading.value = false
  }
}

const handleOpenDialog = (site: SiteConfig) => {
  siteForm.value = normalizeSiteForm(site)
  dupeRuleRows.value = rowsFromRules(site.dupe_rules)
  const fallbackDims = fallbackFromRules(site.dupe_rules)
  dupeFallbackEnabled.value = fallbackDims.length > 0
  dupeFallbackDimensions.value = fallbackDims.length > 0 ? fallbackDims : [...DUPE_DEFAULT_DIMENSIONS]
  // 打开弹窗时按站点拉一次可选项（媒介清单与各维度可用性都取决于站点 YAML）。
  void loadDupeOptions(String(site.site || ''))
  dialogVisible.value = true
}

// 表格行点击：打开编辑对话框。
// 说明：勾选框所在列（type=selection）不打开弹窗，否则每勾一个站点都会弹出编辑框。
const handleRowClick = (row: SiteConfig, column?: { type?: string }) => {
  if (isSortMode.value) return
  if (column && column.type === 'selection') return
  handleOpenDialog(row)
}

// 勾选变化：只维护当前勾选的站点，实际作用范围由后端按 id 处理。
const handleSelectionChange = (rows: SiteConfig[]) => {
  selectedSites.value = Array.isArray(rows) ? rows : []
}

const openBatchTagDialog = () => {
  if (!selectedSites.value.length) {
    ElMessage.warning('请先勾选要操作的站点。')
    return
  }
  batchTagMode.value = 'add'
  batchTagSelection.value = []
  batchTagDialogVisible.value = true
}

const resetSiteSelection = () => {
  selectedSites.value = []
  // 表格实例类型由 Element Plus 提供，这里只用到 clearSelection，用窄化断言避免引入 any。
  const table = sitesTableRef.value as { clearSelection?: () => void } | null
  table?.clearSelection?.()
}

const handleBatchTagSubmit = async () => {
  const ids = selectedSites.value
    .map((site) => Number(site.id))
    .filter((id) => Number.isFinite(id) && id > 0)
  if (!ids.length) {
    ElMessage.warning('请先勾选要操作的站点。')
    return
  }
  const tags = normalizeTags(batchTagSelection.value)
  if (batchTagMode.value !== 'replace' && !tags.length) {
    ElMessage.warning('请选择要操作的标签。')
    return
  }
  // 「覆盖 + 不选标签」等于清空这批站点的标签，属于破坏性操作，先单独确认一次。
  if (batchTagMode.value === 'replace' && !tags.length) {
    try {
      await ElMessageBox.confirm(
        `将清空这 ${ids.length} 个站点的全部标签，是否继续？`,
        '确认清空标签',
        { confirmButtonText: '确定清空', cancelButtonText: '取消', type: 'warning' },
      )
    } catch {
      ElMessage.info('操作已取消。')
      return
    }
  }

  isBatchTagSaving.value = true
  try {
    const response = await axios.post(`${API_BASE_URL}/sites/batch_tags`, {
      ids,
      tags,
      mode: batchTagMode.value,
    })
    if (response.data?.success) {
      ElMessage.success(response.data.message || '站点标签已更新。')
      batchTagDialogVisible.value = false
      resetSiteSelection()
      await fetchSites()
    } else {
      ElMessage.error(response.data?.message || '操作失败！')
    }
  } catch (error: unknown) {
    const msg = axios.isAxiosError(error)
      ? ((error.response?.data as { message?: string } | undefined)?.message || error.message)
      : error instanceof Error
        ? error.message
        : '请求失败，请检查网络或后端服务。'
    ElMessage.error(msg)
  } finally {
    isBatchTagSaving.value = false
  }
}

const handleSave = async () => {
  // 兜底规则开着却一个维度都不勾 → 配置没有可执行语义，先拦住并说清原因。
  if (dupeFallbackEnabled.value && dupeFallbackDimensions.value.length === 0) {
    ElMessage.error('兜底规则至少需要勾选一个判定维度，或关闭兜底规则。')
    return
  }
  isSaving.value = true
  try {
    const siteData: SiteForm = {
      ...siteForm.value,
      cookie: siteForm.value.cookie ? siteForm.value.cookie.trim() : '',
      ratio_threshold: siteForm.value.ratio_threshold || 3.0,
      seed_speed_limit: siteForm.value.seed_speed_limit || 5,
      // 输入框以 MB 展示，提交前换算回后端存储口径（字节）。
      dupe_size_tolerance_bytes: mbToBytes(siteForm.value.dupe_size_tolerance_mb),
      // 编辑器按行维护规则，提交前转成「媒介 → 维度集合」对象（含兜底规则）。
      dupe_rules: buildDupeRules(),
      // 标签统一清洗后再提交（去空、去重）。
      tags: normalizeTags(siteForm.value.tags),
    }

    const response = await axios.post(`${API_BASE_URL}/sites/update`, siteData)

    if (response.data.success) {
      ElMessage.success(response.data.message)
      dialogVisible.value = false
      await fetchSites()
    } else {
      ElMessage.error(response.data.message || '操作失败！')
    }
  } catch (error: unknown) {
    const msg = axios.isAxiosError(error)
      ? ((error.response?.data as { message?: string } | undefined)?.message || error.message)
      : error instanceof Error
        ? error.message
        : '请求失败，请检查网络或后端服务。'
    ElMessage.error(msg)
  } finally {
    isSaving.value = false
  }
}

const handleDelete = (site: SiteConfig) => {
  ElMessageBox.confirm('', '警告', {
    confirmButtonText: '确定删除',
    cancelButtonText: '取消',
    type: 'warning',
    dangerouslyUseHTMLString: true,
    message: `您确定要删除站点【${site.nickname}】吗？<br/>可以通过修改 config.json 然后重启恢复。`,
  })
    .then(async () => {
      try {
        const response = await axios.post(`${API_BASE_URL}/sites/delete`, { id: site.id })
        if (response.data.success) {
          ElMessage.success('站点已删除。')
          await fetchSites()
        } else {
          ElMessage.error(response.data.message || '删除失败！')
        }
      } catch (error: unknown) {
        const msg = axios.isAxiosError(error)
          ? ((error.response?.data as { message?: string } | undefined)?.message || error.message)
          : error instanceof Error
            ? error.message
            : '删除请求失败。'
        ElMessage.error(msg)
      }
    })
    .catch(() => {
      ElMessage.info('操作已取消。')
    })
}

// [移除] 不再需要独立的 saveCookieCloudSettings 和 syncFromCookieCloud 方法
</script>

<style scoped>
/* 样式部分保持不变 */
.cookie-view-container {
  display: flex;
  flex-direction: column;
  height: calc(100vh - 40px);
  overflow: hidden;
}

.top-actions,
.tag-filter-row,
.settings-footer {
  flex-shrink: 0;
}

.top-actions {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: nowrap;
  gap: 16px;
}

.settings-view {
  flex-grow: 1;
  min-height: 0;
  overflow: hidden;
  background-color: transparent;
}

.settings-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 16px 24px;
}

.cookie-cloud-form {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
  flex: 1;
}

.cookie-cloud-form .el-form-item {
  margin-bottom: 0;
}

.browser-extension-link {
  margin-left: 8px;
  white-space: nowrap;
}

.right-action-group {
  display: flex;
  align-items: center;
  flex: 0 0 auto;
  margin-left: auto;
}

.search-input {
  width: 280px;
}

.settings-table {
  width: 100%;
}

.pagination-container {
  display: flex;
  align-items: center;
  gap: 10px;
}

.page-size-text {
  font-size: 14px;
  color: var(--el-text-color-regular);
}

.form-tip {
  color: #909399;
  font-size: 12px;
  line-height: 1.5;
  margin-top: 4px;
}

/* 标签列：多个标签之间留出间距（仅展示，不参与交互） */
.site-tag-list {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}

/* 表头上方的标签筛选条件行 */
.tag-filter-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-radius: 10px;
  font-size: 12px;
}

.tag-filter-row-label {
  color: var(--el-text-color-secondary);
}

.tag-filter-select {
  width: 320px;
}

.tag-filter-row-hint {
  color: var(--el-text-color-secondary);
}

/* 「未设置标签」是筛选项而非真实标签：灰色虚线胶囊，与按标签名推导的彩色胶囊区分开 */
.site-tag-chip--unset {
  color: var(--el-text-color-secondary);
  background-color: var(--el-fill-color-light);
  border-color: var(--el-border-color);
  border-style: dashed;
}

/* dupe 规则编辑器：按媒介配置判定维度 */
.dupe-rules {
  width: 100%;
}

.dupe-rules-table {
  margin-top: 6px;
  border: 1px solid #ebeef5;
  border-radius: 4px;
}

.dupe-rule-medium {
  font-size: 13px;
  color: #303133;
  line-height: 1.4;
}

.dupe-rule-key {
  font-family: monospace;
  font-size: 11px;
  color: #a8abb2;
  line-height: 1.4;
  word-break: break-all;
}

.dupe-rule-actions {
  display: flex;
  gap: 8px;
  margin-top: 8px;
}

.dupe-medium-select {
  flex: 1;
}

.dupe-rule-warning {
  color: #e6a23c;
}

/* 兜底规则：未单独配置的媒介统一走它 */
.dupe-fallback {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed #ebeef5;
}

.dupe-fallback-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.dupe-fallback-title {
  font-size: 13px;
  color: #303133;
}

.dupe-fallback-desc {
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}

.dupe-fallback-dims {
  margin-top: 6px;
}

.settings-table :deep(tr.row-config-incomplete > td.el-table__cell) {
  background-color: #ffecec;
}

.settings-table :deep(tr.row-config-incomplete:hover > td.el-table__cell) {
  background-color: #ffd6d6;
}

.role-tag {
  display: inline-block;
  padding: 0px 3px;
  border-radius: 2px;
  font-size: 11px;
  font-weight: 500;
}

.role-source {
  background-color: #ecf5ff;
  color: #409eff;
  border: 1px solid #b3d8ff;
}

.role-target {
  background-color: #f0f9ff;
  color: #67c23a;
  border: 1px solid #b3e0ff;
}

.role-both {
  background-color: #fdf6ec;
  color: #e6a23c;
  border: 1px solid #f5dab1;
}

.site-edit-dialog :deep(.el-overlay-dialog) {
  display: flex;
  justify-content: center;
  align-items: center;
  overflow: hidden;
}

.site-edit-dialog :deep(.el-dialog) {
  margin: 0;
}

/* 拖拽排序样式 */
.drag-handle {
  cursor: grab;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: 4px;
  border-radius: 4px;
  color: var(--el-text-color-secondary);
  transition: all 0.2s;
}

.drag-handle:hover {
  color: var(--el-color-primary);
  background-color: var(--el-color-primary-light-9);
}

.drag-handle:active {
  cursor: grabbing;
}

.sort-mode-table :deep(.el-table__body tr) {
  transition: background-color 0.15s;
}

.sort-mode-table :deep(.el-table__body tr:hover) {
  background-color: var(--el-color-primary-light-9) !important;
}

.sort-mode-table :deep(.drag-handle-col) {
  cursor: grab;
}

.right-action-group {
  display: flex;
  align-items: center;
  gap: 8px;
}

/* 站点配置导入用的隐藏文件选择框（由「导入」按钮触发 click） */
.hidden-file-input {
  display: none;
}

/* 批量打标签弹窗：说明与作用范围对齐表单 label 宽度（72px） */
.batch-tag-hint {
  margin-left: 72px;
  margin-top: -6px;
  margin-bottom: 6px;
}

.batch-tag-targets {
  margin-left: 72px;
  color: var(--el-text-color-secondary);
  word-break: break-all;
}

@media (max-width: 768px) {
  .cookie-view-container {
    height: 100%;
    min-height: 0;
  }

  .top-actions {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .cookie-cloud-form {
    width: 100%;
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .cookie-cloud-form .el-form-item {
    width: 100%;
    margin-right: 0;
  }

  .cookie-cloud-form :deep(.el-input),
  .cookie-cloud-form :deep(.el-input__wrapper) {
    width: 100% !important;
  }

  .right-action-group {
    width: 100%;
  }

  .search-input {
    width: 100%;
  }

  .tag-filter-row {
    align-items: stretch;
  }

  .tag-filter-select {
    width: 100%;
  }

  .settings-view {
    overflow: auto;
  }

  .settings-table {
    min-width: 1080px;
  }

  .settings-footer {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
    padding: 10px 12px;
  }

  .pagination-container {
    overflow-x: auto;
    justify-content: space-between;
  }

  .site-edit-dialog :deep(.el-dialog) {
    width: calc(100vw - 16px) !important;
    max-width: calc(100vw - 16px) !important;
  }
}
</style>
