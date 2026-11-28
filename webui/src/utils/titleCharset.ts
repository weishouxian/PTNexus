/**
 * 发布标题字符集校验：转种/发布标题只接受英文（ASCII 及常规符号），
 * 不允许出现汉字与中文标点。
 *
 * 背景：站点侧对中文标题的接受度不一致，含中文的标题经常被站点拒绝、判为垃圾种，
 * 或者发出去之后还要人工改名。所以这里在「一种多站」核对页就前置拦截：
 * 命中中文字符时输入框标红警告，并且禁止继续发布（后端还有一道硬拦截兜底）。
 */

// 汉字（基本区 / 扩展 A / 兼容区）+ 中文标点（、。《》「」【】·，：；？！（）—…）
const CHINESE_CHAR_PATTERN =
  /[\u3400-\u4DBF\u4E00-\u9FFF\uF900-\uFAFF\u3001\u3002\u3008-\u3011\u3014\u3015\u301D-\u301F\u30FB\uFF01\uFF08\uFF09\uFF0C\uFF1A\uFF1B\uFF1F\u2014\u2026\u00B7]/u

/**
 * 找出文本中出现的中文字符。
 * 参数：text 为待检查的标题或标题组件值。
 * 返回：命中的字符数组（去重，按首次出现顺序）；没有命中返回空数组。
 */
export function findChineseChars(text: string): string[] {
  if (!text) return []
  const hits: string[] = []
  for (const ch of text.normalize('NFC')) {
    if (!CHINESE_CHAR_PATTERN.test(ch)) continue
    if (!hits.includes(ch)) hits.push(ch)
  }
  return hits
}

/**
 * 判断文本是否含中文字符（汉字或中文标点）。
 * 参数：text 为待检查文本。
 * 返回：含中文字符返回 true。
 */
export function containsChineseChars(text: string): boolean {
  return findChineseChars(text).length > 0
}
