import { describe, expect, it } from 'vitest'
import { compareRouteOverviewItems } from '@/features/routes/lib/route-filters'
import type { RouteOverviewItem } from '@/features/routes/api/routes'

function item(id: string, requestCount: number, successRate: number | null, modelKey = id): RouteOverviewItem {
  return {
    canonical_model: {
      id,
      model_key: modelKey,
      display_name: modelKey,
      provider: 'unknown',
      category: 'chat',
      status: 'active',
    },
    candidate_summary: {
      site_model_count: 1,
      site_count: 1,
      eligible_count: 1,
      cooldown_count: 0,
    },
    traffic_24h: {
      request_count: requestCount,
      success_count: successRate === null ? 0 : Math.round(requestCount * successRate),
      success_rate: successRate,
    },
    last_route: {},
  }
}

describe('compareRouteOverviewItems', () => {
  it('puts models with data first and sorts them by success rate descending by default', () => {
    const noData = item('no-data', 0, null)
    const low = item('low', 10, 0.6)
    const high = item('high', 10, 0.95)
    const map = new Map()

    expect([
      noData,
      low,
      high,
    ].toSorted((a, b) => compareRouteOverviewItems(a, b, 'default', map)).map((entry) => entry.canonical_model.id)).toEqual([
      'high',
      'low',
      'no-data',
    ])
  })

  it('uses request count as a stable tie-breaker for equal success rates', () => {
    const fewer = item('fewer', 2, 0.8)
    const more = item('more', 20, 0.8)
    const map = new Map()

    expect(compareRouteOverviewItems(more, fewer, 'default', map)).toBeLessThan(0)
  })
})
