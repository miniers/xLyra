import { describe, expect, it } from 'vitest'
import { formatRouteScore, routeModelScore, routeSiteScore } from '@/features/routes/lib/route-score'

describe('route score helpers', () => {
  it('splits the candidate score into site and model portions', () => {
    const candidate = {
      score_breakdown: {
        site_health: 40,
        site_success_rate: 9,
        site_latency: 5,
        model_success_rate: 27,
        model_latency: 12,
        model_cache_hit_rate: 4,
        api_key_capacity: 6,
        price: 8,
      },
    }

    expect(routeSiteScore(candidate)).toBe(54)
    expect(routeModelScore(candidate)).toBe(57)
  })

  it('returns a placeholder when the breakdown is not available', () => {
    expect(routeSiteScore()).toBeUndefined()
    expect(routeModelScore({})).toBeUndefined()
    expect(formatRouteScore(undefined)).toBe('-')
    expect(formatRouteScore(103.5)).toBe('103.5')
  })
})
