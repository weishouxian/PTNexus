<template>
  <el-popover placement="bottom-end" :width="260" trigger="click">
    <template #reference>
      <el-button type="info" plain size="small">
        <el-icon><Setting /></el-icon>
        列
      </el-button>
    </template>
    <div class="column-toggle-content">
      <div class="column-toggle-actions">
        <el-button link type="primary" size="small" @click="selectAll">全选</el-button>
        <el-button link type="primary" size="small" @click="resetDefault">重置</el-button>
      </div>
      <div class="column-toggle-tip">勾选控制显示，↑↓ 调整顺序</div>
      <div class="column-toggle-list">
        <div
          v-for="(col, index) in orderedColumns"
          :key="col.prop"
          class="column-toggle-item"
          :class="{ 'is-hidden': !isVisible(col.prop) }"
        >
          <el-checkbox
            :model-value="isVisible(col.prop)"
            @change="(checked: boolean) => toggleColumn(col.prop, checked)"
            >{{ col.label }}</el-checkbox
          >
          <span class="column-toggle-arrows" @click.stop>
            <el-button
              link
              size="small"
              :disabled="!isVisible(col.prop) || visibleIndex(col.prop) === 0"
              @click="moveColumn(col.prop, -1)"
            >
              <el-icon><Top /></el-icon>
            </el-button>
            <el-button
              link
              size="small"
              :disabled="!isVisible(col.prop) || visibleIndex(col.prop) === localVisible.length - 1"
              @click="moveColumn(col.prop, 1)"
            >
              <el-icon><Bottom /></el-icon>
            </el-button>
          </span>
        </div>
      </div>
    </div>
  </el-popover>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { Setting, Top, Bottom } from '@element-plus/icons-vue'

export interface ColumnDef {
  prop: string
  label: string
}

const props = defineProps<{
  columns: ColumnDef[]
  modelValue: string[]
  defaults?: string[]
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string[]): void
}>()

const localVisible = ref<string[]>([...props.modelValue])

watch(
  () => props.modelValue,
  (val) => {
    localVisible.value = [...val]
  },
)

// 展示顺序 = 可见列按 localVisible 顺序排列，隐藏列插回其在 props.columns 中的原始锚点
// （紧邻的前一个可见列之后）。这样：隐藏列不会被挤到末尾，上移/下移只影响可见列，
// 隐藏列始终贴在它原本相邻的可见列附近。
const orderedColumns = computed<ColumnDef[]>(() => {
  const visibleSet = new Set(localVisible.value)
  const columns = props.columns
  // 原始顺序中，每个隐藏列前面最近的可见列（没有则记 null → 放到最前）
  const anchorOf = (hiddenProp: string): string | null => {
    let anchor: string | null = null
    for (const c of columns) {
      if (c.prop === hiddenProp) break
      if (visibleSet.has(c.prop)) anchor = c.prop
    }
    return anchor
  }

  const hidden = columns.filter((c) => !visibleSet.has(c.prop))
  const hiddenByAnchor = new Map<string | null, ColumnDef[]>()
  for (const h of hidden) {
    const key = anchorOf(h.prop)
    const list = hiddenByAnchor.get(key) ?? []
    list.push(h)
    hiddenByAnchor.set(key, list)
  }

  const result: ColumnDef[] = []
  // 插到最前面的隐藏列
  const leading = hiddenByAnchor.get(null) ?? []
  result.push(...leading)

  for (const prop of localVisible.value) {
    const col = columns.find((c) => c.prop === prop)
    if (!col) continue
    result.push(col)
    // 紧跟在该可见列之后的隐藏列
    const trailing = hiddenByAnchor.get(prop) ?? []
    result.push(...trailing)
  }
  return result
})

const isVisible = (prop: string) => localVisible.value.includes(prop)
// 列在可见序列中的下标（隐藏列返回 -1）
const visibleIndex = (prop: string) => localVisible.value.indexOf(prop)

const toggleColumn = (prop: string, checked: boolean) => {
  const next = [...localVisible.value]
  if (checked) {
    if (!next.includes(prop)) next.push(prop)
  } else {
    const idx = next.indexOf(prop)
    if (idx >= 0) next.splice(idx, 1)
  }
  localVisible.value = next
  emit('update:modelValue', [...next])
}

// 上下移动列在可见序列中的位置（隐藏列不参与排序，仅调整可见列之间的先后）
const moveColumn = (prop: string, delta: number) => {
  const visible = [...localVisible.value]
  const idx = visible.indexOf(prop)
  if (idx < 0) return
  const target = idx + delta
  if (target < 0 || target >= visible.length) return
  const next = [...visible]
  const [moved] = next.splice(idx, 1)
  next.splice(target, 0, moved)
  localVisible.value = next
  emit('update:modelValue', [...next])
}

const selectAll = () => {
  // 全选保持当前顺序，仅补齐缺失列
  const next = [...localVisible.value]
  for (const col of props.columns) {
    if (!next.includes(col.prop)) next.push(col.prop)
  }
  localVisible.value = next
  emit('update:modelValue', [...next])
}

const resetDefault = () => {
  const def = props.defaults || props.columns.map((c) => c.prop)
  localVisible.value = [...def]
  emit('update:modelValue', [...def])
}
</script>

<style scoped>
.column-toggle-content {
  padding: 4px 0;
}

.column-toggle-actions {
  display: flex;
  gap: 12px;
  margin-bottom: 6px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--el-border-color-lighter);
}

.column-toggle-tip {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  margin: 6px 0;
}

.column-toggle-list {
  max-height: 360px;
  overflow-y: auto;
}

.column-toggle-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 4px;
  padding: 3px 0;
}

.column-toggle-item.is-hidden {
  opacity: 0.5;
}

.column-toggle-item :deep(.el-checkbox) {
  flex: 1;
  margin-right: 0;
}

.column-toggle-arrows {
  display: inline-flex;
  gap: 0;
  flex-shrink: 0;
}

.column-toggle-arrows .el-button {
  padding: 2px;
}
</style>
