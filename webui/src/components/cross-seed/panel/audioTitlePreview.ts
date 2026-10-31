import type { TitleComponent } from '../crossSeedPanelContext'

// 与后端 processing/title/preview.go 的 defaultTitleComponentsOrder 对齐的组件展示顺序。
// 顺序来自 server/configs/global_mappings.yaml 的 default_title_components.source_key。
// 制作组不在此列（末尾用 - 拼接），「无法识别」不参与标题拼接。
const TITLE_COMPONENT_ORDER = [
  '主标题',
  '季集',
  '年份',
  '剧集状态',
  '发布版本',
  '分辨率',
  '帧率',
  '片源平台',
  '媒介',
  'HDR格式',
  '视频编码',
  '视频格式',
  '色深',
  '音频编码',
]

// 空格分隔的制作组（与后端 isSpaceSeparatedReleaseGroup 对齐）。
const SPACE_SEPARATED_RELEASE_GROUPS = new Set(['MNHD-FRDS', 'mUHD-FRDS', '￡cXcY-FRDS'])

// normalizeTitleForDisplay 对齐后端 normalizeTitleForDisplayLocal：
// 把标题里的点号归一为空格，但保留视频编码的 H.264/H.265/H.266 小数点。
function normalizeTitleForDisplay(title: string): string {
  const runes = Array.from(title.trim())
  if (runes.length === 0) return ''
  const isDigit = (ch: string) => ch >= '0' && ch <= '9'
  const result: string[] = []
  for (let i = 0; i < runes.length; i++) {
    const ch = runes[i]
    if (ch !== '.') {
      result.push(ch)
      continue
    }
    const prevDigit = i > 0 && isDigit(runes[i - 1])
    const nextDigit = i + 1 < runes.length && isDigit(runes[i + 1])
    // 视频编码小数点：H.264 / H.265 / H.266（H 后跟三位数字）。
    const isCodecDot =
      i > 0 &&
      runes[i - 1].toUpperCase() === 'H' &&
      i + 3 < runes.length &&
      isDigit(runes[i + 1]) &&
      isDigit(runes[i + 2]) &&
      isDigit(runes[i + 3]) &&
      (runes.slice(i + 1, i + 4).join('') === '264' ||
        runes.slice(i + 1, i + 4).join('') === '265' ||
        runes.slice(i + 1, i + 4).join('') === '266')
    if (!(prevDigit && nextDigit) && !isCodecDot) {
      result.push(' ')
    } else {
      result.push('.')
    }
  }
  return result.join('').split(/\s+/).filter(Boolean).join(' ')
}

/**
 * buildPreviewTitleWithAudio 根据标题组件重拼预览标题，并把「音频编码」替换为指定音频 token。
 * 与后端 processing/title/preview.go:BuildPreviewTitleFromTitleComponents + 音频替换口径保持一致。
 * 参数：components 为当前编辑中的标题组件；audioDisplay 为目标音轨的展示写法（如 TrueHD / DTS:X）。
 * 返回：重拼后的完整标题。
 */
export function buildPreviewTitleWithAudio(
  components: TitleComponent[],
  audioDisplay: string,
): string {
  const valueByKey = new Map<string, string>()
  for (const component of components) {
    const key = (component.key || '').trim()
    if (!key) continue
    const raw = (component.value || '').trim()
    if (raw === '') continue
    valueByKey.set(key, raw)
  }

  // 用目标音轨的展示 token 覆盖「音频编码」。
  if (audioDisplay) {
    valueByKey.set('音频编码', audioDisplay)
  }

  const titleParts: string[] = []
  for (const key of TITLE_COMPONENT_ORDER) {
    const value = (valueByKey.get(key) || '').trim()
    if (value === '') continue
    titleParts.push(value)
  }

  let mainPart = normalizeTitleForDisplay(titleParts.join(' '))
  if (!mainPart.trim()) {
    mainPart = normalizeTitleForDisplay(valueByKey.get('主标题') || '')
  }

  let releaseGroup = (valueByKey.get('制作组') || '').trim()
  if (!releaseGroup) releaseGroup = 'NOGROUP'
  if (releaseGroup.includes('N/A')) releaseGroup = 'NOGROUP'

  if (SPACE_SEPARATED_RELEASE_GROUPS.has(releaseGroup)) {
    return `${mainPart} ${releaseGroup}`.trim()
  }
  return `${mainPart}-${releaseGroup}`.trim()
}
