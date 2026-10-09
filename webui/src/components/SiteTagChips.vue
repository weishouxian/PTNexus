<template>
  <div v-if="groups.length" class="site-tag-chips">
    <span class="site-tag-chips-title">按标签选择</span>
    <el-tooltip
      v-for="group in groups"
      :key="group.tag"
      :content="group.tooltip"
      placement="top"
      :show-after="150"
    >
      <span
        class="site-tag-chip"
        :class="{
          'site-tag-chip--active': group.allSelected,
          'site-tag-chip--empty': group.selectableCount === 0,
        }"
        :style="chipStyle(group)"
        @click="handleClick(group)"
      >
        {{ group.tag }}
        <span class="site-tag-chip-count">
          {{ group.selectedCount }}/{{ group.selectableCount }}
        </span>
      </span>
    </el-tooltip>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { siteTagStyle } from '@/utils/siteTagColor'

/** 参与标签聚合的站点条目：disabled 的站点不会被标签勾选带入。 */
export type SiteTagItem = {
  name: string
  tags?: string[] | null
  disabled?: boolean
}

type SiteTagGroup = {
  tag: string
  /** 该标签下可被选中的站点名（未勾选/可选） */
  selectable: string[]
  selectableCount: number
  selectedCount: number
  allSelected: boolean
  tooltip: string
}

const props = defineProps<{
  /** 站点列表（含各自标签与是否可选） */
  sites: SiteTagItem[]
  /** 当前已选中的站点名，用于展示 x/y 进度 */
  selected?: string[]
}>()

const emit = defineEmits<{
  /** 点击标签且该标签下站点未全选：把可选中站点并入已选集合（父组件负责去重） */
  apply: [names: string[]]
  /** 再次点击已全选的标签：把这批站点从已选集合中移除 */
  remove: [names: string[]]
}>()

const groups = computed<SiteTagGroup[]>(() => {
  const selectedSet = new Set(props.selected || [])
  const bucket = new Map<string, SiteTagItem[]>()

  for (const site of props.sites || []) {
    const name = String(site?.name || '').trim()
    if (!name) continue
    for (const raw of site.tags || []) {
      const tag = String(raw || '').trim()
      if (!tag) continue
      const list = bucket.get(tag)
      if (list) {
        list.push(site)
      } else {
        bucket.set(tag, [site])
      }
    }
  }

  return Array.from(bucket.entries())
    .map(([tag, items]) => {
      const selectable = items
        .filter((site) => !site.disabled)
        .map((site) => String(site.name).trim())
      const selectedCount = selectable.filter((name) => selectedSet.has(name)).length
      const allNames = items.map((site) => String(site.name).trim())
      const allSelected = selectable.length > 0 && selectedCount === selectable.length
      let tooltip = `「${tag}」下暂无可选站点：${allNames.join('、')}`
      if (selectable.length > 0) {
        tooltip = allSelected
          ? `再次点击取消「${tag}」下全部站点：${allNames.join('、')}`
          : `点击选中「${tag}」下全部站点：${allNames.join('、')}`
      }
      return {
        tag,
        selectable,
        selectableCount: selectable.length,
        selectedCount,
        allSelected,
        tooltip,
      }
    })
    .sort((a, b) => a.tag.localeCompare(b.tag, 'zh-CN'))
})

// 点击即切换：该标签下站点未全选时补选，已全选时整组取消。
const handleClick = (group: SiteTagGroup) => {
  if (group.selectableCount === 0) return
  if (group.allSelected) {
    emit('remove', [...group.selectable])
    return
  }
  emit('apply', [...group.selectable])
}

// 标签用「标签本色」：未全选用浅底本色，已全选切同色系实底以区分状态。
const chipStyle = (group: SiteTagGroup) =>
  siteTagStyle(group.tag, group.allSelected ? 'solid' : 'plain')
</script>

<style scoped>
.site-tag-chips {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}

.site-tag-chips-title {
  font-size: 13px;
  color: var(--el-text-color-secondary);
}

.site-tag-chip {
  cursor: pointer;
}

.site-tag-chip--empty {
  cursor: not-allowed;
  opacity: 0.6;
}

.site-tag-chip-count {
  margin-left: 4px;
  font-size: 11px;
  opacity: 0.75;
}
</style>
