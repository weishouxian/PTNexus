/**
 * 从发种日志（publish_logs.logs）里提取「可以直接展示的失败原因」。
 *
 * 后端 `torrentdata/publish_status.go:extractPublishReason` 用的是同一套优先级，
 * 这里复刻一份供发布记录类界面使用，保证「列表里看到的原因」与「日志里的事实」一致：
 *   ① 最后一条「发布结果：…」——成功/失败流程都会写，信息最聚焦；
 *   ② 最后一条「站点错误提示：…」——站点直接拒绝时才有；
 *   ③ 最后一个非空且非 `---` 分隔的行——兜底（如「未到可发种时间」这类单行日志）。
 */

const PUBLISH_RESULT_LINE = /发布结果[：:]\s*(.+)/
const SITE_ERROR_LINE = /站点错误提示[：:]\s*(.+)/

/** 按字符（非字节）截断，避免截断中文产生乱码。 */
const truncate = (value: string, limit: number): string => {
  if (limit <= 0) return value
  const chars = Array.from(value)
  if (chars.length <= limit) return value
  return `${chars.slice(0, limit).join('')}…`
}

/** 取最后一条命中正则的捕获组内容（没有命中返回空串）。 */
const lastMatch = (lines: string[], pattern: RegExp): string => {
  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const matched = lines[index]?.match(pattern)
    const value = matched?.[1]?.trim()
    if (value) return value
  }
  return ''
}

/**
 * 提取失败原因。
 * @param logs publish_logs.logs 原文
 * @param limit 最大字符数，默认 120
 */
export const extractPublishFailureReason = (logs?: string | null, limit = 120): string => {
  const text = String(logs ?? '').trim()
  if (!text) return ''

  const lines = text.split('\n').map((line) => line.trim())
  const resultLine = lastMatch(lines, PUBLISH_RESULT_LINE)
  if (resultLine) return truncate(resultLine, limit)

  const siteErrorLine = lastMatch(lines, SITE_ERROR_LINE)
  if (siteErrorLine) return truncate(siteErrorLine, limit)

  for (let index = lines.length - 1; index >= 0; index -= 1) {
    const line = lines[index] ?? ''
    if (!line || line.startsWith('---')) continue
    return truncate(line, limit)
  }
  return ''
}

/**
 * 摘要有失败语义时返回原因，否则返回空串。
 * 供「失败状态才展示原因」的列表使用，避免成功记录也占一列文字。
 */
export const resolvePublishFailureReason = (
  status: string | undefined | null,
  logs?: string | null,
  limit = 120,
): string => {
  const normalized = String(status ?? '').toLowerCase()
  if (normalized !== 'failed' && normalized !== 'pre_check_limit') return ''
  return extractPublishFailureReason(logs, limit)
}
