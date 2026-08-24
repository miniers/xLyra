import type { RouteCandidateItem, RouteScoreBreakdown } from '@/features/routes/api/routes'

const SITE_SCORE_KEYS: ReadonlyArray<keyof RouteScoreBreakdown> = [
  'site_health',
  'site_success_rate',
  'site_latency',
]

const MODEL_SCORE_KEYS: ReadonlyArray<keyof RouteScoreBreakdown> = [
  'model_success_rate',
  'model_latency',
  'model_first_byte_latency',
  'api_key_capacity',
  'actual_price',
  'api_key_subscription_expiry_urgency',
  'api_key_subscription_expiry_rescue_bonus',
]

export function routeSiteScore(candidate?: Pick<RouteCandidateItem, 'score_breakdown'>) {
  return sumScoreComponents(candidate?.score_breakdown, SITE_SCORE_KEYS)
}

export function routeModelScore(candidate?: Pick<RouteCandidateItem, 'score_breakdown'>) {
  return sumScoreComponents(candidate?.score_breakdown, MODEL_SCORE_KEYS)
}

export function formatRouteScore(value?: number | null) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '-'
  return value.toFixed(2).replace(/\.00$/, '').replace(/(\.\d)0$/, '$1')
}

function sumScoreComponents(
  breakdown: RouteScoreBreakdown | undefined,
  keys: ReadonlyArray<keyof RouteScoreBreakdown>,
) {
  if (!breakdown) return undefined

  return keys.reduce((total, key) => {
    const value = breakdown[key]
    return total + (typeof value === 'number' && Number.isFinite(value) ? value : 0)
  }, 0)
}
