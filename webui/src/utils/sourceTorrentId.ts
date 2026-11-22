export interface SourceTorrentSiteDetails {
  comment?: string | null
  torrentId?: string | null
}

const DIGITS_RE = /^\d+$/
const UUID_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i
const INFOHASH_RE = /^[0-9a-f]{40}$/i
const RELATIVE_URL_BASE = 'https://pt-nexus.local'

const normalizeText = (value: string | null | undefined): string => {
  return typeof value === 'string' ? value.trim() : ''
}

const extractNumericQueryParam = (value: string, key: 'id' | 'torrent_id'): string | null => {
  const trimmed = normalizeText(value)
  if (!trimmed) return null

  try {
    const parsed = new URL(trimmed, RELATIVE_URL_BASE)
    const queryValue = normalizeText(parsed.searchParams.get(key))
    if (DIGITS_RE.test(queryValue)) {
      return queryValue
    }
  } catch {
    // Ignore URL parse errors and fall back to regex-based extraction.
  }

  const pattern = new RegExp(`${key}=([0-9]+)`, 'i')
  const matched = trimmed.match(pattern)
  return matched?.[1] || null
}

// 数字型详情页路径：NexusPHP 是 details.php?id=（走 query 参数），
// 自研/SPA 站点把 ID 放在路径里 —— 好学 hxpt 是单数 /torrent/{id}，
// 肉丝 PeerGo 与可乐 colapt 是复数 /torrents/{id}（可乐的 .torrent comment 就是
// https://www.colapt.com/torrents/28914），TTG 是 /t/{id}/，四种都要认。
const NUMERIC_DETAIL_PATH_RE = /\/(?:detail|details|torrent|torrents|t)\/(\d+)(?:\/|$)/i
// UUID 型详情页路径：肉丝老平台 /torrent/{uuid}、PeerGo 下也有 /torrents/{uuid} 形态。
const UUID_DETAIL_PATH_RE =
  /\/torrents?\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})(?:\/|$)/i

const extractNumericFromDetailPath = (value: string): string | null => {
  const trimmed = normalizeText(value)
  if (!trimmed) return null

  try {
    const parsed = new URL(trimmed, RELATIVE_URL_BASE)
    const matchedPath = parsed.pathname.match(NUMERIC_DETAIL_PATH_RE)
    if (matchedPath?.[1]) {
      return matchedPath[1]
    }
  } catch {
    // Ignore URL parse errors and fall back to regex-based extraction.
  }

  const matched = trimmed.match(NUMERIC_DETAIL_PATH_RE)
  return matched?.[1] || null
}

const extractUuidFromPath = (value: string): string | null => {
  const trimmed = normalizeText(value)
  if (!trimmed) return null

  if (UUID_RE.test(trimmed)) {
    return trimmed
  }

  try {
    const parsed = new URL(trimmed, RELATIVE_URL_BASE)
    const matchedPath = parsed.pathname.match(UUID_DETAIL_PATH_RE)
    if (matchedPath?.[1]) {
      return matchedPath[1]
    }
  } catch {
    // Ignore URL parse errors and fall back to regex-based extraction.
  }

  const matched = trimmed.match(UUID_DETAIL_PATH_RE)
  return matched?.[1] || null
}

const isUsableStoredSourceTorrentId = (value: string): boolean => {
  return DIGITS_RE.test(value) || UUID_RE.test(value)
}

export const extractSourceTorrentIdFromComment = (
  comment: string | null | undefined,
): string | null => {
  const trimmed = normalizeText(comment)
  if (!trimmed) {
    return null
  }

  if (DIGITS_RE.test(trimmed)) {
    return trimmed
  }

  const id = extractNumericQueryParam(trimmed, 'id')
  if (id) {
    return id
  }

  const torrentId = extractNumericQueryParam(trimmed, 'torrent_id')
  if (torrentId) {
    return torrentId
  }

  const detailId = extractNumericFromDetailPath(trimmed)
  if (detailId) {
    return detailId
  }

  const uuid = extractUuidFromPath(trimmed)
  if (uuid) {
    return uuid
  }

  return null
}

export const resolveSourceTorrentId = ({
  sourceInfoTorrentId,
  siteDetails,
}: {
  sourceInfoTorrentId?: string | null
  siteDetails?: SourceTorrentSiteDetails | null
}): string | null => {
  const explicitTorrentId = normalizeText(sourceInfoTorrentId)
  if (explicitTorrentId) {
    return explicitTorrentId
  }

  const storedTorrentId = normalizeText(siteDetails?.torrentId)
  if (storedTorrentId && !INFOHASH_RE.test(storedTorrentId) && isUsableStoredSourceTorrentId(storedTorrentId)) {
    return storedTorrentId
  }

  return extractSourceTorrentIdFromComment(siteDetails?.comment)
}
