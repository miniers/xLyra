import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AnalyticsSeriesPoint } from '@/features/analytics/api/analytics'
import {
  analyticsCacheHitRate,
  formatTrendMetricValue,
  pointMetricValue,
  presetRange,
} from './analytics-utils'

afterEach(() => {
  vi.useRealTimers()
})

describe('analytics date presets', () => {
  it('returns yesterday as a single-day range', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date(2026, 7, 18, 12, 0, 0))

    expect(presetRange('yesterday')).toEqual({
      from: '2026-08-17',
      to: '2026-08-17',
    })
  })
})

const point: AnalyticsSeriesPoint = {
  date: '2026-08-17',
  requests: 1,
  success_count: 1,
  failure_count: 0,
  prompt_tokens: 100,
  completion_tokens: 10,
  cached_tokens: 25,
  total_tokens: 110,
  cost: 0,
  avg_latency_ms: 20,
  max_latency_ms: 20,
}

describe('analytics cache hit rate helpers', () => {
  it('calculates a ratio from aggregated input and cached tokens', () => {
    expect(analyticsCacheHitRate(100, 25)).toBe(0.25)
    expect(analyticsCacheHitRate(0, 0)).toBeNull()
    expect(analyticsCacheHitRate(-1, 1)).toBeNull()
    expect(analyticsCacheHitRate(100, Number.NaN)).toBeNull()
  })

  it('uses the cache hit rate for series points and formats it as a percentage', () => {
    expect(pointMetricValue(point, 'cache-hit-rate')).toBe(0.25)
    expect(formatTrendMetricValue(0.256, 'cache-hit-rate', 'USD')).toBe('25.6%')
  })
})
