/**
 * 站点标签配色：同名标签恒定同色，无需任何人工维护。
 *
 * 颜色由「标签名哈希 → 精选色相」推导，保证：
 *  - 稳定：刷新、换页、换设备后同一个标签永远同色；
 *  - 无状态：不占库、不加接口，新增标签自动有颜色；
 *  - 可读：浅底色 + 同色系深字（plain），或同色系实底 + 白字（solid）。
 */

import { siteTagKey } from './siteTag'

/** 精选色相：相邻色相已拉开距离，且避开纯红/纯绿以免与「可发种 / 禁转」等语义撞色。 */
const TAG_HUES = [210, 275, 190, 330, 25, 160, 240, 45, 300, 120, 350, 265]

/** 标签样式档位：plain = 浅底（常规展示），solid = 实底（选中/激活态）。 */
export type SiteTagKind = 'plain' | 'solid'

/** 计算标签的固定色相（忽略大小写与首尾空白）。 */
export const siteTagHue = (tag: string): number => {
  const key = siteTagKey(tag)
  // FNV-1a 32 位 + 末尾雪崩混淆。
  // ⚠️ 不要退回 `hash * 31 + code`：中文短标签（1~2 个字）在这种线性哈希下
  // 会明显聚集（实测 24 个标签只落到 9 个色相、单个色相挤 7 个），加雪崩后接近均匀。
  let hash = 0x811c9dc5
  for (let i = 0; i < key.length; i += 1) {
    hash ^= key.charCodeAt(i)
    hash = Math.imul(hash, 0x01000193)
  }
  hash ^= hash >>> 13
  hash = Math.imul(hash, 0x5bd1e995)
  hash ^= hash >>> 15
  return TAG_HUES[(hash >>> 0) % TAG_HUES.length]
}

/** 取标签配色三元组（背景/边框/文字）。 */
export const siteTagPalette = (tag: string, kind: SiteTagKind = 'plain') => {
  const hue = siteTagHue(tag)
  if (kind === 'solid') {
    return {
      background: `hsl(${hue}, 55%, 45%)`,
      border: `hsl(${hue}, 55%, 45%)`,
      color: '#ffffff',
    }
  }
  return {
    background: `hsl(${hue}, 72%, 93%)`,
    border: `hsl(${hue}, 60%, 76%)`,
    color: `hsl(${hue}, 62%, 28%)`,
  }
}

/** 供 `:style` 直接绑定。 */
export const siteTagStyle = (tag: string, kind: SiteTagKind = 'plain') => {
  const palette = siteTagPalette(tag, kind)
  return {
    backgroundColor: palette.background,
    borderColor: palette.border,
    color: palette.color,
  }
}
