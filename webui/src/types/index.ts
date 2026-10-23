export interface SiteData {
  uploaded: number
  comment: string
  migration: number
  state: string
  seeders: number
  torrentId?: string
  site?: string
  site_name?: string
}

/**
 * 发布状态分组，由后端 publish_logs.status 归一而来：
 * success=发布成功 / exists=站点已存在 / failed=发布失败 / pending=排队或执行中 / void=已作废 / other=未知
 */
export type PublishStatusGroup = 'success' | 'exists' | 'failed' | 'pending' | 'void' | 'other'

/** 某个目标站点的发布状态聚合（同一站点发过多次时取最新状态，count 为历史次数）。 */
export interface PublishSiteStatus {
  site: string
  /** 原始 publish_logs.status，如 success / edited / exists / failed / pre_check_limit / queued / invalidated */
  status: string
  group: PublishStatusGroup
  /** 该种子发往该站点的历史记录条数（含失败重试） */
  count: number
  /** 最新一条记录的完成时间 */
  last_at: string
  /** 失败原因摘要（后端已截断到 200 字） */
  reason: string
  result_url?: string
  cost_ms?: number
  log_id?: number
}

/** 发布状态分组计数，total 为有发布记录的站点数。 */
export type PublishSummary = Record<PublishStatusGroup | 'total', number>

export interface Torrent {
  unique_id: string
  hash?: string
  hashes?: string[]
  name: string
  save_path: string
  size: number
  size_formatted: string
  progress: number
  state: string
  sites: Record<string, SiteData>
  total_uploaded: number
  total_uploaded_formatted: string
  seeders: number
  downloaderId?: string
  downloaderIds?: string[]
  target_sites_count?: number
  publish_at?: string | null
  last_publish_at?: string | null
  source_data_status?: 'missing' | 'unreviewed' | 'reviewed'
  source_data_fetched?: boolean
  source_data_reviewed?: boolean
  /** 以下三项取自源站数据（seed_parameters），标准值如 category.movie / medium.remux / source.china */
  type?: string
  medium?: string
  source?: string
  /** 各目标站点的发布状态（按站点聚合，失败优先排序），用于展开行标签着色与内嵌明细表 */
  publish_sites?: PublishSiteStatus[]
  /** 发布状态分组计数，用于列表「发布」列 */
  publish_summary?: PublishSummary
}

export interface ISourceInfo {
  name: string
  site: string
  torrentId: string
}

export interface Downloader {
  id: string
  name: string
  enabled: boolean
  color?: string
}
