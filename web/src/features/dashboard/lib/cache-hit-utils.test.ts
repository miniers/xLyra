import { describe, expect, it } from 'vitest'
import { buildCacheHitTrend, formatCacheHitDate, naturalCacheHitDateRange, sortCacheHitBreakdown } from './cache-hit-utils'

describe('cache hit utilities', () => {
  it('formats local date parameters', () => {
    expect(formatCacheHitDate(new Date(2026, 4, 9, 12, 0, 0))).toBe('2026-05-09')
  })

  it('builds a weighted trend for each selected dimension', () => {
    const points = [
      {
        date: '2026-05-01', site_id: 'site-a', site_name: 'Alpha', site_slug: 'alpha', site_type: 'openai', site_model_id: 'model-a', model_key: 'gpt-5', upstream_model_name: 'gpt-5', request_count: 1, prompt_tokens: 100, cached_tokens: 20, hit_rate: 0.2,
      },
      {
        date: '2026-05-01', site_id: 'site-a', site_name: 'Alpha', site_slug: 'alpha', site_type: 'openai', site_model_id: 'model-a', model_key: 'gpt-5', upstream_model_name: 'gpt-5', request_count: 1, prompt_tokens: 50, cached_tokens: 30, hit_rate: 0.6,
      },
      {
        date: '2026-05-01', site_id: 'site-b', site_name: 'Beta', site_slug: 'beta', site_type: 'anthropic', site_model_id: 'model-b', model_key: 'claude', upstream_model_name: 'claude', request_count: 1, prompt_tokens: 20, cached_tokens: 10, hit_rate: 0.5,
      },
    ]

    const trend = buildCacheHitTrend(points, 'site')

    expect(trend.series.map((item) => item.name)).toEqual(['Alpha', 'Beta'])
    expect(trend.data).toHaveLength(1)
    expect(trend.data[0]?.series_0).toBeCloseTo(1 / 3)
    expect(trend.data[0]?.series_1).toBe(0.5)
  })

  it('aggregates model trends across channels and keeps sparse dates', () => {
    const points = [
      {
        date: '2026-05-01', site_id: 'site-a', site_name: 'Alpha', site_slug: 'alpha', site_type: 'openai', site_model_id: 'model-a', model_key: 'gpt-5', upstream_model_name: 'gpt-5-a', request_count: 1, prompt_tokens: 100, cached_tokens: 20, hit_rate: 0.2,
      },
      {
        date: '2026-05-02', site_id: 'site-b', site_name: 'Beta', site_slug: 'beta', site_type: 'openai', site_model_id: 'model-b', model_key: 'claude', upstream_model_name: 'claude', request_count: 1, prompt_tokens: 50, cached_tokens: 25, hit_rate: 0.5,
      },
      {
        date: '2026-05-03', site_id: 'site-b', site_name: 'Beta', site_slug: 'beta', site_type: 'openai', site_model_id: 'model-b', model_key: 'gpt-5', upstream_model_name: 'gpt-5-b', request_count: 1, prompt_tokens: 100, cached_tokens: 80, hit_rate: 0.8,
      },
    ]

    const trend = buildCacheHitTrend(points, 'model')

    expect(trend.series.map((item) => item.name)).toEqual(['gpt-5', 'claude'])
    expect(trend.data.map((item) => item.date)).toEqual(['2026-05-01', '2026-05-02', '2026-05-03'])
    expect(trend.data[0]?.series_0).toBe(0.2)
    expect(trend.data[1]?.series_0).toBeUndefined()
    expect(trend.data[2]?.series_0).toBe(0.8)
  })

  it('uses an inclusive natural-day range', () => {
    const range = naturalCacheHitDateRange(1)
    expect(formatCacheHitDate(range.from)).toBe(formatCacheHitDate(range.to))
  })

  it('sorts breakdown rows by text and numeric dimensions', () => {
    const items = [
      {
        site_id: 'site-a', site_name: 'Alpha', site_slug: 'alpha', site_type: 'openai', site_model_id: 'model-a', model_key: 'gpt-5', upstream_model_name: 'gpt-5', request_count: 2, prompt_tokens: 100, cached_tokens: 20, hit_rate: 0.2,
      },
      {
        site_id: 'site-b', site_name: 'Beta', site_slug: 'beta', site_type: 'openai', site_model_id: 'model-b', model_key: 'claude', upstream_model_name: 'claude', request_count: 5, prompt_tokens: 50, cached_tokens: 40, hit_rate: 0.8,
      },
    ]

    expect(sortCacheHitBreakdown(items, 'site', 'asc').map((item) => item.site_name)).toEqual(['Alpha', 'Beta'])
    expect(sortCacheHitBreakdown(items, 'hitRate', 'desc').map((item) => item.hit_rate)).toEqual([0.8, 0.2])
    expect(sortCacheHitBreakdown(items, 'requests', 'asc').map((item) => item.request_count)).toEqual([2, 5])
  })
})
