export type RequirementMetric = {
  key: string
  label: string
  icon: string
  kind: 'bytes' | 'ratio' | 'weeks' | 'days' | 'number'
  targetText: string
  currentText: string
  met: boolean | null
  percent: number
  threshold: number | null
  remainingText: string
}

export type UpgradePath = {
  name: string
  items: RequirementMetric[]
  percent: number
  done: number
  total: number
  raw: string
}

export type RequirementProfile = {
  uploaded: number
  downloaded: number
  ratio: number
  bonus: number
  seed_bonus: number
  join_date: string
  seeding: number
  invites: number
}

type MetricDef = {
  key: string
  label: string
  icon: string
  kind: RequirementMetric['kind']
  pattern: RegExp
  /**
   * 反向指标：站点要求「某值以下」而不是「至少」。
   * 典型是青蛙的「分享率小于 4.0」（传火蛙路线要求分享率别太高），
   * 判定从 value >= threshold 变成 value <= threshold，进度也反过来算。
   */
  inverse?: boolean
}

const numberToken = String.raw`[\d.,]+(?:\s*(?:万|萬|[wW]))?(?:\s*(?:[kKmMgGtTpP]i?[bB]|[tTgGmMkKpP]|[bB]))?`

const metricDefs: MetricDef[] = [
  {
    key: 'uploaded',
    label: '上传量',
    icon: 'uploaded',
    kind: 'bytes',
    pattern: new RegExp(
      String.raw`(?:上[传傳](?:量|载|載)?)\s*(?:在|至少|不少于|大於|大于|超过|滿|满|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'monthly_downloaded',
    label: '月均下载量',
    icon: 'downloaded',
    kind: 'bytes',
    // 「月均下载量」里含「下载量」，本项必须排在 downloaded 前面，
    // 由 parseClause 匹配后从文本里剔掉，否则同一个数字会被算成两项。
    pattern: new RegExp(
      String.raw`(?:月均下[载載](?:量)?|平均月下[载載](?:量)?)\s*(?:在|至少|不少于|大於|大于|超过|滿|满|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'downloaded',
    label: '下载量',
    icon: 'downloaded',
    kind: 'bytes',
    // 「真实下载量」也算下载量，和「下载量大于 500 GB 或 真实下载量大于 2048 GB」合并成同一项。
    pattern: new RegExp(
      String.raw`(?:(?:真实|實際)?\s*下[载載](?:量)?)\s*(?:在|至少|不少于|大於|大于|超过|滿|满|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'ratio',
    label: '分享率',
    icon: 'ratio',
    kind: 'ratio',
    // 「分享率在 1.0 以上」中间夹着「在」，只认比较词会整条漏掉（青蛙全站都是这个写法）
    pattern: /(?:分享率|分享比率|ratio)\s*(?:在|至少|不少于|大於|大于|超过|≥|>=|>)?\s*([\d.]+)/gi,
  },
  {
    key: 'ratio_max',
    label: '分享率上限',
    icon: 'ratio',
    kind: 'ratio',
    inverse: true,
    // 青蛙的写法是「分享率小于（没错，就是小于）4.0」，比较词与数字之间允许夹一段括注
    pattern:
      /(?:分享率|分享比率|ratio)\s*(?:小于等于|小於等於|小于|小於|低于|低於|≤|<=|<)\s*(?:[（(][^）)]*[）)])?\s*([\d.]+)/gi,
  },
  {
    key: 'join_weeks',
    label: '注册时间',
    icon: 'join',
    kind: 'weeks',
    pattern: /(?:注[册冊](?:时间|時間)?|註冊)[^\d]{0,12}?([\d.]+)\s*(?:周|週)/gi,
  },
  {
    key: 'join_days',
    label: '注册时间',
    icon: 'join',
    kind: 'days',
    // 青蛙按天写：「必须注册已至少 35 天」
    pattern: /(?:注[册冊](?:已)?|註冊)[^\d]{0,12}?([\d.]+)\s*(?:天|日)/gi,
  },
  {
    key: 'seed_bonus',
    label: '做种积分',
    icon: 'seed',
    kind: 'number',
    // 「做」字可以缺：杜比 hddolby 的 FAQ 写的是「种积分大于48000积分」，
    // 只认「做种积分」会整项漏掉，弹窗里少了最关键的一项门槛。
    pattern: new RegExp(
      String.raw`(?:做?[种種](?:积分|積分|得分)|seed\s*bonus)\s*(?:至少|不少于|大於|大于|超过|达到|達到|达|達|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'bonus',
    label: '魔力',
    icon: 'bonus',
    kind: 'number',
    pattern: new RegExp(
      String.raw`(?:魔力(?:值)?|karma)\s*(?:至少|不少于|大於|大于|超过|达到|達到|达|達|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'published',
    label: '发布种子数',
    icon: 'published',
    kind: 'number',
    // 青蛙叫「发种」（发种大于 120 个），有的站叫「发布数 / 发布种子数」
    pattern: new RegExp(
      String.raw`(?:发布|發[布佈]|发[种種]|發[種種])(?:种子|種子)?(?:数|數)?\s*(?:至少|不少于|大於|大于|超过|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
  {
    key: 'seeding',
    label: '做种数',
    icon: 'seeding',
    kind: 'number',
    pattern: new RegExp(
      String.raw`(?:做[种種](?:数|數)?|seeding)\s*(?:至少|不少于|大於|大于|超过|≥|>=|>)?\s*(${numberToken})`,
      'gi',
    ),
  },
]

/** 把文本里的数量转成可比较的数值：支持 100W / 10 万 / 3.5TB / 1,500G。 */
export const parseRequirementNumber = (raw: string): number | null => {
  const text = raw.replace(/\s+/g, '').replace(/,/g, '')
  // 单位允许带 i（GiB / TiB / MiB / KiB / PiB）：站点常把「下载量大于 10 GiB」写成 i 后缀，
  // 之前不认 i 会让 10 GiB 只捕获到裸数字 10，被当成 10 字节。
  const match = /^([\d.]+)(万|萬|[wW]|[kKmMgGtTpP]i?[bB]|[tTgGmMkKpP]|[bB])?$/.exec(text)
  if (!match) return null
  const value = Number.parseFloat(match[1])
  if (!Number.isFinite(value)) return null
  const unit = (match[2] || '').toLowerCase()
  if (unit === '万' || unit === '萬' || unit === 'w') return value * 10000
  const bytesUnits: Record<string, number> = {
    b: 1,
    kb: 1024,
    kib: 1024,
    k: 1024,
    mb: 1024 ** 2,
    mib: 1024 ** 2,
    m: 1024 ** 2,
    gb: 1024 ** 3,
    gib: 1024 ** 3,
    g: 1024 ** 3,
    tb: 1024 ** 4,
    tib: 1024 ** 4,
    t: 1024 ** 4,
    pb: 1024 ** 5,
    pib: 1024 ** 5,
    p: 1024 ** 5,
  }
  if (!unit) return value
  return bytesUnits[unit] === undefined ? null : value * bytesUnits[unit]
}

const formatBytes = (size: number): string => {
  if (!Number.isFinite(size) || size < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let value = size
  let index = 0
  while (value >= 1024 && index < units.length - 1) {
    value /= 1024
    index += 1
  }
  // 去掉多余的尾零：「1.00 TB」显示成「1 TB」，和站点原文的写法更接近
  const shown = index === 0 ? Math.round(value) : Number(value.toFixed(2))
  return `${shown} ${units[index]}`
}

const formatNumber = (value: number): string =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 }).format(value)

/** 站点原文里的数字（`8585.97` / `46723.75`）原样保留两位小数，只加千分位。 */
const formatPairNumber = (value: number): string =>
  new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 2 }).format(value)

const joinWeeks = (joinDate: string): number | null => {
  const text = (joinDate || '').trim()
  // 接口站（朱雀）给的是 unix 时间戳，秒/毫秒都可能：先归一成毫秒，
  // 否则注册周数算不出来，等级进度里「注册时间」永远是「站点未提供」。
  if (/^\d{9,13}$/.test(text)) {
    const raw = Number(text)
    const ms = text.length >= 12 ? raw : raw * 1000
    const weeks = (Date.now() - ms) / (7 * 24 * 60 * 60 * 1000)
    return Number.isFinite(weeks) ? Math.max(0, weeks) : null
  }
  const match = /^(\d{4})[-/](\d{1,2})[-/](\d{1,2})/.exec(text)
  if (!match) return null
  const joined = new Date(Date.UTC(Number(match[1]), Number(match[2]) - 1, Number(match[3])))
  const weeks = (Date.now() - joined.getTime()) / (7 * 24 * 60 * 60 * 1000)
  return Number.isFinite(weeks) ? Math.max(0, weeks) : null
}

const joinDays = (joinDate: string): number | null => {
  const weeks = joinWeeks(joinDate)
  return weeks === null ? null : weeks * 7
}

const currentValue = (key: string, profile: RequirementProfile): number | null => {
  switch (key) {
    case 'uploaded':
      return profile.uploaded
    case 'downloaded':
      return profile.downloaded
    case 'ratio':
    case 'ratio_max':
      return profile.ratio
    case 'seed_bonus':
      return profile.seed_bonus
    case 'bonus':
      return profile.bonus
    case 'seeding':
      return profile.seeding
    case 'invites':
      return profile.invites
    case 'monthly_downloaded': {
      const days = joinDays(profile.join_date)
      if (days === null) return null
      const months = Math.max(days / 30, 1)
      return profile.downloaded / months
    }
    case 'join_weeks':
      return joinWeeks(profile.join_date)
    case 'join_days':
      return joinDays(profile.join_date)
    default:
      return null
  }
}

/** 去掉「注意…低于 X 自动降级」这类降级提示，以及不参与判定的括注，只保留升级要求。 */
export const cleanRequirementText = (text: string): string =>
  (text || '')
    .replace(/注意[，,][^。；;]*[。；;]?/g, '')
    .replace(/无论何时[^。；;]*[。；;]?/g, '')
    // 「（仅计算通过审核、做种数>=2、完成数>=2的种子）」这类口径说明里也带数字，
    // 不剔掉会被解析成「做种数 ≥ 2」这种假要求（青蛙的偷天蛙路线全带这段）。
    .replace(
      /[（(][^（()）]*(?:仅计算|僅計算|仅统计|僅統計|不含|不包括|不算|以.{0,6}为准)[^（()）]*[）)]/g,
      '',
    )
    .replace(
      /[，,；;。]?\s*(?:如果)?[^。；;]{0,10}?低于\s*[\d.]+[^。；;]{0,10}?(?:自动|自動)?降[级級][^。；;]*[。；;]?\s*$/g,
      '',
    )
    .trim()

/** 站点写的是「降级规则」而不是升级门槛，这类文本不做指标解析（否则会算出假进度）。 */
const isDemotionText = (text: string): boolean =>
  /(?:自动降至本级|自動降至本級|降至本级|降至本級|被自动降|被自動降)/.test(text)

const splitClauses = (text: string): string[] =>
  text
    .split(/[；;。]+/)
    .map((part) => part.trim().replace(/[。:：]+$/, ''))
    .filter(Boolean)

const targetText = (raw: string, kind: RequirementMetric['kind']): string => {
  const value = raw.trim().replace(/\s+/g, ' ')
  if (kind === 'weeks') return `≥ ${value.replace(/\s+/g, '')} 周`
  if (kind === 'days') return `≥ ${value.replace(/\s+/g, '')} 天`
  return `≥ ${value
    .replace(/([A-Za-z]+)$/, ' $1')
    .replace(/\s+/g, ' ')
    .trim()}`
}

type ParsedItem = {
  key: string
  label: string
  icon: string
  kind: RequirementMetric['kind']
  thresholds: number[]
  rawTargets: string[]
  inverse?: boolean
  /**
   * 站点直接给出的当前值（classes.php 的「注册周数：59/48」写法）。
   * 有值时不再从 profile 推算 —— 这类指标（完成种子数、累计做种时间、H&R 积分）
   * 本来就取不到资料字段，站点自己就是唯一可信来源。
   */
  currentValue?: number
  /** 站点给出的单位（GiB / 天 / 周），用于原样展示，不做二次换算。 */
  unit?: string
  /**
   * 该指标在资料页里本来就有实时字段（上传量 / 魔力 / 注册时间…）。
   * 这种就优先用资料页的实时值，站点等级页里的数字只是抓取那一刻的快照。
   */
  tracked?: boolean
}

const parseClause = (clause: string): ParsedItem[] => {
  const items: ParsedItem[] = []
  let remaining = clause
  for (const def of metricDefs) {
    def.pattern.lastIndex = 0
    let match: RegExpExecArray | null
    const thresholds: number[] = []
    const rawTargets: string[] = []
    while ((match = def.pattern.exec(remaining)) !== null) {
      const threshold = parseRequirementNumber(match[1])
      if (threshold === null) continue
      thresholds.push(threshold)
      rawTargets.push(match[1].trim())
    }
    if (!thresholds.length) continue
    items.push({
      key: def.key,
      label: def.label,
      icon: def.icon,
      kind: def.kind,
      thresholds,
      rawTargets,
      inverse: def.inverse,
    })
    // 「月均下载量超过 5 GB」里的「下载量」会被 downloaded 的正则再命中一次，
    // 变成「下载量 ≥ 5 GB」的假门槛：月均这项匹配完就从文本里删掉。
    if (def.key === 'monthly_downloaded') {
      remaining = remaining.replace(def.pattern, ' ')
      def.pattern.lastIndex = 0
    }
  }
  return items
}

const metricFromParsed = (item: ParsedItem, profile: RequirementProfile): RequirementMetric => {
  // 「当前值/目标值」写法（classes.php）自带当前值：
  //  - 资料页里有实时字段的（上传量 / 魔力 / 注册时间）用实时值，站点那个数字只是抓取时的快照；
  //  - 没有实时字段的（完成种子数 / 做种时间 / H&R 积分）只能用站点给的快照值。
  const live = currentValue(item.key, profile)
  const snapshot = item.currentValue
  let value: number | null = live
  let unit = (item.unit || '').trim()
  if (item.tracked) {
    if (live !== null && (live > 0 || snapshot === undefined)) {
      unit = '' // 实时值统一走标准格式化（字节 → TB、周 → 周）
    } else if (snapshot !== undefined) {
      value = snapshot
    }
  } else if (snapshot !== undefined) {
    value = snapshot
  }
  const thresholds = item.thresholds
  // 反向指标（分享率上限）取最严的门槛：值越小越难，所以用 min 比较 value <= threshold。
  const easiest = thresholds.length ? Math.min(...thresholds) : null
  // 分享率显示为 ∞ 时后端给的是 -1：它满足任何「分享率 ≥ X」，但不满足「分享率 ≤ X」
  const infiniteRatio = item.key === 'ratio' && value !== null && value < 0
  const met =
    value === null || easiest === null
      ? null
      : item.inverse
        ? value <= easiest
        : infiniteRatio
          ? true
          : value >= easiest
  let percent = 0
  if (infiniteRatio) {
    percent = item.inverse ? 0 : 100
  } else if (value !== null && easiest !== null && easiest > 0) {
    percent = item.inverse
      ? value <= easiest
        ? 100
        : Math.round((easiest / value) * 100)
      : Math.round((value / easiest) * 100)
  }
  percent = Math.max(0, Math.min(100, percent))
  let currentText = '站点未提供'
  if (value !== null) {
    if (unit) currentText = `${formatPairNumber(value)} ${unit}`
    else if (item.kind === 'bytes') currentText = formatBytes(value)
    else if (item.kind === 'ratio') currentText = value < 0 ? '∞' : value.toFixed(2)
    else if (item.kind === 'weeks') currentText = `${Math.floor(value)} 周`
    else if (item.kind === 'days') currentText = `${Math.floor(value)} 天`
    else currentText = formatNumber(value)
  }
  let remainingText = ''
  if (met === false && easiest !== null && value !== null && !infiniteRatio) {
    if (item.inverse) {
      // 上限没达标是「太高」，提示要降多少而不是差多少
      remainingText = `需降 ${(value - easiest).toFixed(2)}`
    } else {
      const remaining = easiest - value
      if (unit) remainingText = `${formatPairNumber(remaining)} ${unit}`
      else if (item.kind === 'weeks') remainingText = `${Math.max(1, Math.ceil(remaining))} 周`
      else if (item.kind === 'days') remainingText = `${Math.max(1, Math.ceil(remaining))} 天`
      else if (item.kind === 'bytes') remainingText = formatBytes(remaining)
      else if (item.kind === 'ratio') remainingText = remaining.toFixed(2)
      else remainingText = formatNumber(remaining)
    }
  }
  return {
    key: item.key,
    label: item.label,
    icon: item.icon,
    kind: item.kind,
    targetText: item.inverse
      ? item.rawTargets.map((raw) => `≤ ${raw}`).join(' 或 ')
      : unit
        ? item.thresholds.map((threshold) => `≥ ${formatPairNumber(threshold)} ${unit}`).join(' 或 ')
        : item.kind === 'number'
        ? item.thresholds.map((threshold) => `≥ ${formatNumber(threshold)}`).join(' 或 ')
        : item.kind === 'bytes'
          ? // 体积门槛统一按 1TB = 1024GB 换算成可读单位（「≥ 8192 G」→「≥ 8 TB」），
            // 否则目标写 G、当前写 TB，用户没法一眼判断达成没达成
            item.thresholds.map((threshold) => `≥ ${formatBytes(threshold)}`).join(' 或 ')
          : item.rawTargets.map((raw) => targetText(raw, item.kind)).join(' 或 '),
    currentText,
    met,
    percent,
    threshold: easiest,
    remainingText,
  }
}

const buildPath = (name: string, clauses: string[], profile: RequirementProfile, extra: ParsedItem[] = []): UpgradePath => {
  const merged = new Map<string, ParsedItem>()
  for (const item of extra) merged.set(item.key, item)
  for (const clause of clauses) {
    for (const item of parseClause(clause)) {
      const existing = merged.get(item.key)
      if (!existing) {
        merged.set(item.key, item)
        continue
      }
      existing.thresholds.push(...item.thresholds)
      existing.rawTargets.push(...item.rawTargets)
    }
  }
  const items = Array.from(merged.values()).map((item) => metricFromParsed(item, profile))
  // 「月均下载量」为了避开 downloaded 的正则被排在它前面，展示时挪回「下载量」之后，
  // 和站点原文的顺序（下载量 … 月均下载量）保持一致。
  const monthlyIndex = items.findIndex((item) => item.key === 'monthly_downloaded')
  const downloadedIndex = items.findIndex((item) => item.key === 'downloaded')
  if (monthlyIndex >= 0 && downloadedIndex >= 0 && monthlyIndex < downloadedIndex) {
    const [monthly] = items.splice(monthlyIndex, 1)
    items.splice(downloadedIndex, 0, monthly)
  }
  const known = items.filter((item) => item.met !== null)
  const done = known.filter((item) => item.met).length
  return {
    name,
    items,
    percent: known.length ? Math.round((done / known.length) * 100) : 0,
    done,
    total: known.length,
    raw: clauses.join('；'),
  }
}

/**
 * 显式路线标记：「条件A-偷天蛙：…」「条件B-传火蛙：…」（青蛙）。
 * 这种写法没有「满足任意一个即可」后面接编号列表，只能靠标记本身切分。
 */
const routeMarkerPattern =
  /条件\s*[A-Za-z0-9一二三四]\s*[-－—–_]?\s*([^：:。；;，,\s]{1,12})\s*[：:]/g

type RouteGroup = { common: string; routes: { name: string; body: string }[] }

const splitByRouteMarkers = (text: string): RouteGroup | null => {
  routeMarkerPattern.lastIndex = 0
  const marks: { index: number; end: number; name: string }[] = []
  let match: RegExpExecArray | null
  while ((match = routeMarkerPattern.exec(text)) !== null) {
    marks.push({
      index: match.index,
      end: match.index + match[0].length,
      name: (match[1] || '').trim(),
    })
  }
  if (!marks.length) return null
  return {
    common: text.slice(0, marks[0].index),
    routes: marks.map((mark, index) => ({
      name: mark.name || `方式 ${index + 1}`,
      body: text.slice(mark.end, index + 1 < marks.length ? marks[index + 1].index : text.length),
    })),
  }
}

/**
 * 「当前值/目标值」式要求行：`注册周数：59/48`、`上传量（GiB）：8585.97/50000`。
 * 这是较新版 NexusPHP 独立等级页（北洋园 tjupt 的 /classes.php）的写法，
 * 站点已经把每条门槛的当前进度算好，直接用，比从资料字段反推准得多
 * （完成种子数、累计做种时间、H&R 积分这些根本没有对应的资料字段）。
 */
const pairLinePattern = /^(.{1,26}?)\s*[：:]\s*([\d.,]+)\s*\/\s*([\d.,]+)$/

/** 括号里是纯单位（`GiB` / `天`）才当单位展示；「体积>200MiB」这类口径说明直接丢掉。 */
const unitTokenPattern =
  /^(?:[KMGTP]i?B|B|[KMGTP]|天|日|周|週|个|個|人|次|条|條)$/i

/** 站点把体积写成 GiB/TiB 时换成字节，好和资料页的实时值同单位比较。 */
const byteUnitScale: Record<string, number> = {
  b: 1,
  kb: 1024,
  kib: 1024,
  mb: 1024 ** 2,
  mib: 1024 ** 2,
  gb: 1024 ** 3,
  gib: 1024 ** 3,
  tb: 1024 ** 4,
  tib: 1024 ** 4,
  pb: 1024 ** 5,
  pib: 1024 ** 5,
}

type PairLabelMeta = {
  key: string
  label: string
  icon: string
  kind: RequirementMetric['kind']
  /** 资料页里有对应实时字段（上传量 / 魔力 / 注册时间…），不走站点快照值 */
  tracked?: boolean
}

const pairLabelMeta = (label: string): PairLabelMeta => {
  if (/上传/.test(label))
    return { key: 'uploaded', label: '上传量', icon: 'uploaded', kind: 'bytes', tracked: true }
  if (/下载/.test(label))
    return { key: 'downloaded', label: '下载量', icon: 'downloaded', kind: 'bytes', tracked: true }
  if (/分享率|比率/.test(label))
    return { key: 'ratio', label: '分享率', icon: 'ratio', kind: 'ratio', tracked: true }
  if (/注册/.test(label))
    return { key: 'join_weeks', label: '注册时间', icon: 'join', kind: 'weeks', tracked: true }
  if (/(?:做种|保种|發布|发布)时间/.test(label))
    return { key: 'seed_days', label: '做种时间', icon: 'seed', kind: 'days' }
  if (/完成种子|完成数/.test(label))
    return { key: 'completed', label: '完成种子数', icon: 'published', kind: 'number' }
  if (/发布|發佈|发种|發種/.test(label))
    return { key: 'published', label: '发布种子数', icon: 'published', kind: 'number' }
  if (/做种|保种/.test(label))
    return { key: 'seeding', label: '做种数', icon: 'seeding', kind: 'number', tracked: true }
  if (/魔力|魔法/.test(label))
    return { key: 'bonus', label: '魔力', icon: 'bonus', kind: 'number', tracked: true }
  if (/积分|積分/.test(label)) return { key: 'points', label, icon: 'seed', kind: 'number' }
  return { key: `pair:${label}`, label, icon: '', kind: 'number' }
}

/**
 * 抽出所有「当前值/目标值」要求行，并从原文里删掉这些片段，
 * 免得剩余的文本解析把「上传量（GiB）：8585.97/50000」再猜成「上传量 ≥ 8585.97」。
 */
const extractPairRequirements = (text: string): { rest: string; items: ParsedItem[] } => {
  const items: ParsedItem[] = []
  const rest: string[] = []
  for (const segment of (text || '').split(/[\n；;]+/)) {
    const trimmed = segment.trim()
    if (!trimmed) continue
    const match = pairLinePattern.exec(trimmed)
    const parsedCurrent = match ? parseRequirementNumber(match[2]) : null
    const parsedTarget = match ? parseRequirementNumber(match[3]) : null
    if (!match || parsedCurrent === null || parsedTarget === null) {
      rest.push(trimmed)
      continue
    }
    let current = parsedCurrent
    let target = parsedTarget
    const rawLabel = match[1].trim()
    const paren = /^(.+?)\s*[（(]([^（）()]{1,16})[）)]\s*$/.exec(rawLabel)
    const label = paren ? paren[1].trim() : rawLabel
    let unit = paren && unitTokenPattern.test(paren[2].trim()) ? paren[2].trim() : ''
    // 「上传量（GiB）：8585.97/50000」的数值单位是 GiB，先换成字节，
    // 才能和资料页的上传量（字节）比；展示时再统一格式化。
    const scale = byteUnitScale[unit.toLowerCase()] || 1
    if (scale > 1) {
      current *= scale
      target *= scale
      unit = ''
    }
    const meta = pairLabelMeta(label)
    // 「注册天数：400/300」按天算，别当成周
    if (meta.key === 'join_weeks' && /^(天|日)$/.test(unit)) {
      meta.key = 'join_days'
      meta.kind = 'days'
    }
    items.push({
      key: meta.key,
      label: meta.label,
      icon: meta.icon,
      kind: meta.kind,
      thresholds: [target],
      rawTargets: [match[3]],
      currentValue: current,
      unit,
      tracked: meta.tracked,
    })
  }
  return { rest: rest.join('；'), items }
}

/**
 * 把站点 FAQ 里的自由文本升级条件拆成可展示的指标行。
 * 支持四种写法：
 *  1. 「当前值/目标值」逐条列表（classes.php，站点直接给出进度）；
 *  2. 「条件A-偷天蛙：… 条件B-传火蛙：…」显式路线（青蛙）；
 *  3. 「以下条件 1、2 满足任意一个即可」+ 编号列表；
 *  4. 单一路线（普通 NexusPHP 站）。
 */
export const buildUpgradePaths = (text: string, profile: RequirementProfile): UpgradePath[] => {
  const cleaned = cleanRequirementText(text)
  if (!cleaned) return []
  // 降级规则（青蛙 Peasant）不是升级门槛：解析出来的「下载量 ≥ 10GB」会变成假进度，
  // 这里只回一条空路线，让弹窗退化成展示原文。
  if (isDemotionText(cleaned)) {
    return [{ name: '', items: [], percent: 0, done: 0, total: 0, raw: cleaned }]
  }

  // classes.php 这种「每条门槛 + 当前值」的页面本身就是最准的来源，优先按它出结果。
  const pairs = extractPairRequirements(cleaned)
  if (pairs.items.length) {
    const clauses = splitClauses(pairs.rest).filter((clause) => parseClause(clause).length)
    return [buildPath('', clauses, profile, pairs.items)]
  }

  const grouped = splitByRouteMarkers(cleaned)
  if (grouped) {
    const common = splitClauses(grouped.common)
    const routes = grouped.routes
      .map((route) => buildPath(route.name, [...common, ...splitClauses(route.body)], profile))
      .filter((path) => path.items.length)
    if (routes.length) return routes
  }

  const clauses = splitClauses(cleaned)
  const alternativePattern =
    /满足[^。；;]{0,8}任意|滿足[^。；;]{0,8}任意|任意一种|任意一種|任意一[条條项項]|任意\s*\d\s*个|任选|任選/
  const alternativeAt = clauses.findIndex((clause) => alternativePattern.test(clause))
  if (alternativeAt < 0) return [buildPath('', clauses, profile)]

  // 「以下条件 1、2 满足任意一个即可：做种积分…；做种积分…」里，
  // 第一条路线和提示语在同一个分句里（冒号后面），要先拆开，否则只会解析出第二条路线。
  const marker = clauses[alternativeAt]
  const colonAt = marker.search(/[：:]/)
  if (colonAt >= 0 && colonAt + 1 < marker.length) {
    const remainder = marker.slice(colonAt + 1).trim()
    if (remainder) {
      clauses.splice(alternativeAt, 1, marker.slice(0, colonAt + 1).trim(), remainder)
    }
  }

  const common = clauses.slice(0, alternativeAt)
  const alternatives = clauses
    .slice(alternativeAt + 1)
    .filter((clause) => parseClause(clause).length)
  if (!alternatives.length) return [buildPath('', clauses, profile)]
  return alternatives.map((clause, index) =>
    buildPath(`方式 ${index + 1}`, [...common, clause], profile),
  )
}
