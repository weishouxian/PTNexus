/**
 * 等级表裁剪：把「贵宾 / 退休 / 工作组 / 管理组」这类**不能靠做种量升上去**的特殊用户组
 * 从升级阶梯里去掉，只保留普通会员等级链。
 *
 * 站点 FAQ 的等级表通常把两类等级混在一列（人人 24 行、馒头 20+ 行），
 * 直接全量展示会让阶梯看起来永远差七八级，进度条也失去意义。
 */

/** 等级行的最小结构（只需要名字，方便直接喂站点资料里的等级数组） */
export type LevelRowLike = { name: string }

/** 归一化等级名，用于「当前等级」与站点等级表对齐（容忍大小写/空格/括号差异） */
export const normalizeLevel = (value: string): string =>
  (value || '').toLowerCase().replace(/[\s_\-()[\]（）【】]/g, '')

/**
 * 特殊用户组别名表（只收敛用户明确要求隐藏的特殊组，其余等级一律照原样显示）。
 * 第一个元素是英文键（去空格小写后全等比较），其余是中文别名（包含匹配，
 * 因为站点常写成「金玉满堂(VIP)」「养老族(Retiree)」这类「中文(English)」形式）。
 */
const SPECIAL_LEVEL_ALIASES: string[][] = [
  ['vip', '贵宾', '金玉满堂'],
  ['retiree', '养老族', '退休组'],
  ['helper', '得力助手'],
  ['seeder', '保种员'],
  ['transferrer', '转载员'],
  ['uploader', '发布员'],
  ['torrentmanager', '种子管理员'],
  ['forummoderator', '论坛版主'],
  ['coder', '辅助开发'],
  ['moderator', '总版主'],
  ['administrator', '管理员'],
  ['sysop', '维护开发员'],
  ['staffleader', '主管'],
  ['rainbow', '彩虹照耀', '彩虹'],
  ['other', '其他', '其它'],
  ['encoder', '编码员', '压制员', '压片员'],
  ['recorder', '录制员', '录播员'],
  ['honorary', 'honor', '荣誉会员'],
  // TTG 的「资深组」是元老组（免考核、长期不活动也保留账号），同样升不上去。
  // 只匹配「资深组」全词：ptcafe 的普通等级「Crazy User 资深玩家」不能被误伤。
  ['seniorgroup', '资深组'],
]

/** 判断某个等级名是否属于特殊用户组（`种子管理员(Torrent Manager)`、`贵宾(VIP)` 都能命中）。 */
export const isSpecialUserGroup = (name: string): boolean => {
  const latin = ((name || '').toLowerCase().match(/[a-z][a-z\s]*/g) || [])
    .join(' ')
    .replace(/\s+/g, '')
    .trim()
  const normalized = normalizeLevel(name)
  return SPECIAL_LEVEL_ALIASES.some(
    ([english, ...aliases]) =>
      (latin && latin === english) ||
      aliases.some((alias) => normalized.includes(normalizeLevel(alias))),
  )
}

/**
 * 计算「等级表某一行的名称」与「资料页当前等级」的匹配分，0 表示不匹配。
 * 为什么不能简单做双向 includes：站点等级名互为子串（`User` 是 `Power User` /
 * `Elite User` / `Crazy User` … 的子串），双向包含会把一整串等级同时标成「当前」。
 * 因此这里按「越贴近越可信」排序：全等 > 前后缀相等 > 任意包含，同档内长度差越小分越高。
 */
export const levelMatchScore = (name: string, current: string): number => {
  if (!name || !current) return 0
  if (name === current) return 1000
  // 长度差越大越可能是「短名误配长名」，把它折算成分数差上限 400，避免减成负数
  const penalty = Math.min(Math.abs(name.length - current.length), 400)
  // 站点 FAQ 的等级名常带中文别名（「明星(Veteran User)」），资料页只给
  // 「Veteran User」甚至「Veteran」，所以允许包含匹配，只是分数低一档。
  if (name.startsWith(current) || name.endsWith(current)) return 600 - penalty
  if (current.startsWith(name) || current.endsWith(name)) return 500 - penalty
  if (name.includes(current)) return 400 - penalty
  if (current.includes(name)) return 300 - penalty
  return 0
}

/** 在等级表里找「当前等级」所在行；返回 -1 表示没匹配上 */
export const findCurrentLevelIndex = (levels: LevelRowLike[], userClass: string): number => {
  const current = normalizeLevel(userClass)
  if (!current) return -1
  let bestIndex = -1
  let bestScore = 0
  levels.forEach((level, index) => {
    const score = levelMatchScore(normalizeLevel(level.name), current)
    if (score > bestScore) {
      bestScore = score
      bestIndex = index
    }
  })
  return bestIndex
}

/**
 * 按「是否特殊用户组」裁剪等级表。
 *
 * - 当前等级是普通等级：剔掉特殊组，只留可升级的等级链；
 * - 当前等级本身就在特殊组（发布员 / 管理员 / Rainbow…）：**它的那一行要留下**
 *   （否则看不到自己），其余普通等级照常全部显示，其他特殊组仍然隐藏。
 *
 * `hidden` 是隐藏掉的行数，用于在完整表上方提示「已隐藏 N 个特殊组」，
 * 避免用户误以为数据没抓到。
 */
export const splitLevelsForUser = <T extends LevelRowLike>(
  levels: T[],
  userClass: string,
): { levels: T[]; hidden: number } => {
  if (!levels.length) return { levels, hidden: 0 }
  const currentIndex = findCurrentLevelIndex(levels, userClass)
  const currentLevel = currentIndex >= 0 ? levels[currentIndex] : null
  const kept = levels.filter(
    (level) => level === currentLevel || !isSpecialUserGroup(level.name),
  )
  // 整张表都是特殊组时不做裁剪，否则弹窗会空掉
  if (!kept.length) return { levels, hidden: 0 }
  return { levels: kept, hidden: levels.length - kept.length }
}
