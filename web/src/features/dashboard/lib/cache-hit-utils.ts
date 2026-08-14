import type { DashboardCacheHitBreakdown, DashboardCacheHitDailyPoint } from '@/features/dashboard/api/dashboard'
import type { DashboardSeries, DashboardTrendDatum } from '@/features/dashboard/components'

export type CacheHitDateRange = {
  from: Date
  to: Date
}

export type CacheHitGroupBy = 'site' | 'model'

export type CacheHitTrend = {
  data: DashboardTrendDatum[]
  series: DashboardSeries[]
}

export type CacheHitBreakdownSortKey = 'site' | 'model' | 'hitRate' | 'requests' | 'input' | 'cached'
export type CacheHitBreakdownSortDirection = 'asc' | 'desc'

type CacheHitTrendAggregate = {
  key: string
  name: string
  promptTokens: number
  cachedTokens: number
}

export function naturalCacheHitDateRange(days = 30): CacheHitDateRange {
  const today = startLocalDay(new Date())
  const from = new Date(today)
  from.setDate(from.getDate() - Math.max(1, days) + 1)
  return { from, to: today }
}

export function formatCacheHitDate(value: Date) {
  const year = value.getFullYear()
  const month = String(value.getMonth() + 1).padStart(2, '0')
  const day = String(value.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

export function startCacheHitDay(value: Date) {
  return startLocalDay(value)
}

export function buildCacheHitTrend(points: DashboardCacheHitDailyPoint[], groupBy: CacheHitGroupBy): CacheHitTrend {
  const groups = new Map<string, { name: string; totalPromptTokens: number }>()
  const byDate = new Map<string, Map<string, CacheHitTrendAggregate>>()

  for (const point of points) {
    const group = cacheHitGroup(point, groupBy)
    const groupMeta = groups.get(group.key)
    if (groupMeta) {
      groupMeta.totalPromptTokens += point.prompt_tokens
    } else {
      groups.set(group.key, { name: group.name, totalPromptTokens: point.prompt_tokens })
    }

    const byGroup = byDate.get(point.date) ?? new Map<string, CacheHitTrendAggregate>()
    const aggregate = byGroup.get(group.key) ?? {
      key: group.key,
      name: group.name,
      promptTokens: 0,
      cachedTokens: 0,
    }
    aggregate.promptTokens += point.prompt_tokens
    aggregate.cachedTokens += point.cached_tokens
    byGroup.set(group.key, aggregate)
    byDate.set(point.date, byGroup)
  }

  const selectedGroups = [...groups.entries()]
    .sort(([, left], [, right]) => right.totalPromptTokens - left.totalPromptTokens || left.name.localeCompare(right.name))
    .map(([key, value], index) => ({ key, name: value.name, seriesKey: `series_${index}` }))
  const seriesByGroup = new Map(selectedGroups.map((item) => [item.key, item]))
  const data = [...byDate.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([date, byGroup]) => {
      const row: DashboardTrendDatum = { date }
      for (const [groupKey, aggregate] of byGroup) {
        const series = seriesByGroup.get(groupKey)
        if (!series) continue
        row[series.seriesKey] = aggregate.promptTokens > 0 ? aggregate.cachedTokens / aggregate.promptTokens : 0
      }
      return row
    })

  return {
    data,
    series: selectedGroups.map(({ seriesKey, name }) => ({ key: seriesKey, name })),
  }
}

export function sortCacheHitBreakdown(
  items: DashboardCacheHitBreakdown[],
  sortKey: CacheHitBreakdownSortKey,
  direction: CacheHitBreakdownSortDirection,
) {
  return [...items].sort((left, right) => {
    const leftValue = cacheHitBreakdownSortValue(left, sortKey)
    const rightValue = cacheHitBreakdownSortValue(right, sortKey)
    const leftMissing = leftValue == null
    const rightMissing = rightValue == null
    if (leftMissing || rightMissing) {
      if (leftMissing && rightMissing) return 0
      return leftMissing ? 1 : -1
    }

    const comparison = typeof leftValue === 'number' && typeof rightValue === 'number'
      ? leftValue - rightValue
      : String(leftValue).localeCompare(String(rightValue))
    return direction === 'asc' ? comparison : -comparison
  })
}

function cacheHitGroup(point: DashboardCacheHitDailyPoint, groupBy: CacheHitGroupBy) {
  if (groupBy === 'site') {
    const id = point.site_id || `${point.site_slug}\u0000${point.site_name}`
    return { key: `site:${id}`, name: point.site_name || point.site_slug || id }
  }
  const id = point.model_key || point.upstream_model_name || point.site_model_id || 'unknown'
  return { key: `model:${id}`, name: point.model_key || point.upstream_model_name || id }
}

function cacheHitBreakdownSortValue(item: DashboardCacheHitBreakdown, sortKey: CacheHitBreakdownSortKey) {
  switch (sortKey) {
    case 'site':
      return item.site_name || item.site_slug || ''
    case 'model':
      return item.model_key || item.upstream_model_name || ''
    case 'hitRate':
      return item.hit_rate
    case 'requests':
      return item.request_count
    case 'input':
      return item.prompt_tokens
    case 'cached':
      return item.cached_tokens
  }
}

function startLocalDay(value: Date) {
  const result = new Date(value)
  result.setHours(0, 0, 0, 0)
  return result
}
