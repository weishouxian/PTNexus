export type MediaInfoSummaryField = {
  label: string
  value: string
}

export type MediaInfoSummary = {
  fileName: string
  general: MediaInfoSummaryField[]
  video: MediaInfoSummaryField[]
  audio: string[]
  subtitles: string[]
  hasSummary: boolean
}

type ParsedMediaInfoSection = {
  kind: string
  title: string
  fields: MediaInfoSummaryField[]
}

const sectionHeaderPattern = /^[A-Za-z][A-Za-z0-9 ()/#&.+-]*$/

const normalizeLabel = (label: string): string => label.replace(/\s+/g, ' ').trim()

const normalizeValue = (value: string): string => value.replace(/\s+/g, ' ').trim()

const normalizeMediaInfoText = (text: string): string =>
  (text || '').replace(/\r\n?/g, '\n').replace(/\u00a0/g, ' ')

const isSectionHeader = (line: string): boolean => {
  if (!line || line.includes(':')) return false
  if (!sectionHeaderPattern.test(line)) return false

  const kind = line.match(/^[A-Za-z]+/)?.[0].toLowerCase()
  return Boolean(
    kind && ['general', 'video', 'audio', 'text', 'subtitle', 'subtitles'].includes(kind),
  )
}

const getSectionKind = (title: string): string => title.match(/^[A-Za-z]+/)?.[0].toLowerCase() || ''

const parseSections = (text: string): ParsedMediaInfoSection[] => {
  const sections: ParsedMediaInfoSection[] = []
  let currentSection: ParsedMediaInfoSection | null = null

  for (const rawLine of normalizeMediaInfoText(text).split('\n')) {
    const line = rawLine.trim()
    if (!line) continue

    if (isSectionHeader(line)) {
      currentSection = {
        kind: getSectionKind(line),
        title: line,
        fields: [],
      }
      sections.push(currentSection)
      continue
    }

    const match = line.match(/^(.+?)\s*:\s*(.*)$/)
    if (!match || !currentSection) continue

    currentSection.fields.push({
      label: normalizeLabel(match[1]),
      value: normalizeValue(match[2]),
    })
  }

  return sections
}

const findFieldValue = (section: ParsedMediaInfoSection | undefined, labels: string[]): string => {
  if (!section) return ''
  const wanted = labels.map((label) => label.toLowerCase())
  return section.fields.find((field) => wanted.includes(field.label.toLowerCase()))?.value || ''
}

const buildField = (
  section: ParsedMediaInfoSection | undefined,
  label: string,
  labels: string[],
): MediaInfoSummaryField | null => {
  const value = findFieldValue(section, labels)
  return value ? { label, value } : null
}

const compactFields = (fields: Array<MediaInfoSummaryField | null>): MediaInfoSummaryField[] =>
  fields.filter((field): field is MediaInfoSummaryField => Boolean(field))

const getFileName = (value: string): string => {
  const trimmed = value.trim()
  if (!trimmed) return 'MediaInfo'
  return trimmed.split(/[\\/]/).pop() || trimmed
}

const normalizeBitrate = (value: string): string =>
  value
    .replace(/(\d)\s+(?=\d)/g, '$1')
    .replace(/\s+(?=[kKmMgG]b\/s)/g, '')
    .replace(/([kKmMgG])b\/s/g, (_, unit: string) => {
      return `${unit.toLowerCase()}b/s`
    })

const normalizePixels = (value: string): string =>
  value
    .replace(/\s+/g, '')
    .replace(/pixels?/gi, '')
    .trim()

const normalizeAudioCodec = (format: string, commercialName: string): string => {
  const source = commercialName || format
  if (/truehd/i.test(source) && /atmos/i.test(source)) return 'TrueHD Atmos'
  if (/truehd/i.test(source)) return 'TrueHD'
  if (/dts-hd\s*master\s*audio/i.test(source) || /dts-hd\s*ma/i.test(source)) return 'DTS-HD MA'
  if (/dts\s*xll/i.test(format)) return 'DTS-HD MA'
  if (/dts:x/i.test(source)) return 'DTS:X'
  if (/dolby\s*digital\s*plus/i.test(source)) return 'DD+'
  if (/dolby\s*digital/i.test(source)) return format || 'AC-3'
  return format || source
}

const normalizeAudioChannels = (value: string): string => {
  const match = value.match(/(\d+(?:\.\d+)?)/)
  if (!match) return value

  const channels = Number(match[1])
  if (!Number.isFinite(channels)) return value
  if (channels === 1) return '1.0ch'
  if (channels === 2) return '2.0ch'
  if (channels === 6) return '5.1ch'
  if (channels === 8) return '7.1ch'
  return `${channels}ch`
}

const buildVideoFields = (section: ParsedMediaInfoSection | undefined): MediaInfoSummaryField[] => {
  const format = findFieldValue(section, ['Format'])
  const bitDepth = findFieldValue(section, ['Bit depth'])
  const width = findFieldValue(section, ['Width'])
  const height = findFieldValue(section, ['Height'])
  const ratio = findFieldValue(section, ['Display aspect ratio'])
  const videoFormat = format ? `${format}${bitDepth ? ` (${bitDepth})` : ''}` : ''
  const bitrate = findFieldValue(section, ['Bit rate', 'Nominal bit rate'])
  const frameRate = findFieldValue(section, ['Frame rate'])
  const hdrFormat = findFieldValue(section, ['HDR format'])
  const resolution =
    width && height
      ? `${normalizePixels(width)}*${normalizePixels(height)}${ratio ? ` (${ratio})` : ''}`
      : ''
  const hdrFields = hdrFormat
    .split(/\s+\/\s+/)
    .map((value) => value.trim())
    .filter(Boolean)
    .map((value) => ({ label: 'HDR', value }))

  return [
    videoFormat ? { label: 'Format', value: videoFormat } : null,
    bitrate ? { label: 'Bit Rate', value: normalizeBitrate(bitrate) } : null,
    resolution ? { label: 'Resolution', value: resolution } : null,
    frameRate ? { label: 'Frame Rate', value: frameRate } : null,
  ]
    .concat(hdrFields)
    .filter((field): field is MediaInfoSummaryField => Boolean(field))
}

// 音频行的中文语种前缀映射（仅前端展示，不改 MediaInfo 原文）。
// 顺序即优先级：粤语/台配必须排在国语（chinese/mandarin）之前，否则
// `Chinese (Cantonese)` 会被 chinese 子串先命中成国语（对齐后端 tagging 口径）。
const audioLanguageRules: Array<{ pattern: RegExp; label: string }> = [
  { pattern: /cantonese|粤语|广东话|香港/i, label: '粤语' },
  { pattern: /taiwan|台配|台语|闽南语/i, label: '台配' },
  { pattern: /mandarin|普通话|华语|mainland|cmn|mandrin|国语|chinese|中文/i, label: '国语' },
  { pattern: /english|英语/i, label: '英语' },
  { pattern: /japanese|日本語|日语/i, label: '日语' },
  { pattern: /korean|韩语|한국어/i, label: '韩语' },
  { pattern: /french|français|法语/i, label: '法语' },
  { pattern: /german|deutsch|德语/i, label: '德语' },
  { pattern: /russian|русский|俄语/i, label: '俄语' },
  { pattern: /spanish|español|castellano|西班牙语/i, label: '西班牙语' },
  { pattern: /portuguese|português|葡萄牙语/i, label: '葡萄牙语' },
  { pattern: /italian|italiano|意大利语/i, label: '意大利语' },
  { pattern: /hindi|हिन्दी|印地语/i, label: '印地语' },
  { pattern: /thai|ไทย|泰语/i, label: '泰语' },
  { pattern: /arabic|العربية|阿拉伯语/i, label: '阿拉伯语' },
  { pattern: /ukrainian|українська|乌克兰语/i, label: '乌克兰语' },
  { pattern: /polish|polski|波兰语/i, label: '波兰语' },
  { pattern: /dutch|nederlands|荷兰语/i, label: '荷兰语' },
  { pattern: /swedish|svenska|瑞典语/i, label: '瑞典语' },
  { pattern: /danish|dansk|丹麦语/i, label: '丹麦语' },
  { pattern: /norwegian|norsk|挪威语/i, label: '挪威语' },
  { pattern: /finnish|suomi|芬兰语/i, label: '芬兰语' },
  { pattern: /vietnamese|tiếng việt|越南语/i, label: '越南语' },
  { pattern: /indonesian|bahasa indonesia|印尼语/i, label: '印尼语' },
  { pattern: /turkish|türkçe|土耳其语/i, label: '土耳其语' },
]

// matchAudioLanguageLabel 从音频行的 Language/Title 值推断中文语种名。
// 参数/返回：value 为 Language（缺省时 Title）字段值；返回中文语种名，无法识别返回空串。
// 失败场景：空值或无命中时返回空串，展示层保持原样（不加前缀）。
// 副作用：无。
const matchAudioLanguageLabel = (value: string): string => {
  const text = (value || '').trim()
  if (!text) return ''
  for (const rule of audioLanguageRules) {
    if (rule.pattern.test(text)) return rule.label
  }
  return ''
}

const buildAudioLine = (section: ParsedMediaInfoSection): string => {
  const title = findFieldValue(section, ['Title'])
  const language = findFieldValue(section, ['Language'])
  const format = findFieldValue(section, ['Format'])
  const commercialName = findFieldValue(section, ['Commercial name'])
  const channels = findFieldValue(section, ['Channel(s)'])
  const bitrate = findFieldValue(section, ['Bit rate'])
  const prefix = language || title
  const langLabel = matchAudioLanguageLabel(prefix)
  const codec = normalizeAudioCodec(format, commercialName)
  const suffix = title ? ` (${title})` : ''
  const core = [
    langLabel,
    prefix,
    codec,
    channels ? normalizeAudioChannels(channels) : '',
  ]
    .filter(Boolean)
    .join(' ')
  return (
    `${core}${bitrate ? ` @ ${normalizeBitrate(bitrate)}` : ''}${suffix}`.trim() || section.title
  )
}

const buildSubtitleLine = (section: ParsedMediaInfoSection): string => {
  const title = findFieldValue(section, ['Title'])
  const language = findFieldValue(section, ['Language'])
  const format = findFieldValue(section, ['Format', 'Codec ID'])
  const suffix = title && title !== language ? ` (${title})` : ''
  const base = [language, format].filter(Boolean).join(' ')
  return `${base}${suffix}`.trim() || title || section.title
}

export const buildMediaInfoSummary = (text: string): MediaInfoSummary => {
  const sections = parseSections(text)
  const generalSection = sections.find((section) => section.kind === 'general')
  const videoSection = sections.find((section) => section.kind === 'video')
  const audioSections = sections.filter((section) => section.kind === 'audio')
  const subtitleSections = sections.filter((section) =>
    ['text', 'subtitle', 'subtitles'].includes(section.kind),
  )

  const completeName = findFieldValue(generalSection, ['Complete name'])
  const generalBitrate = findFieldValue(generalSection, ['Overall bit rate', 'Bit rate'])
  const general = compactFields([
    buildField(generalSection, 'Format', ['Format']),
    buildField(generalSection, 'Duration', ['Duration']),
    buildField(generalSection, 'File Size', ['File size']),
    generalBitrate ? { label: 'Bit Rate', value: normalizeBitrate(generalBitrate) } : null,
  ])
  const video = buildVideoFields(videoSection)
  const audio = audioSections.map(buildAudioLine).filter(Boolean)
  const subtitles = subtitleSections.map(buildSubtitleLine).filter(Boolean)
  const hasSummary =
    general.length > 0 || video.length > 0 || audio.length > 0 || subtitles.length > 0

  return {
    fileName: getFileName(completeName),
    general,
    video,
    audio,
    subtitles,
    hasSummary,
  }
}
