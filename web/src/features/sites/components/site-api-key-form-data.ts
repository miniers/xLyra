export const DEFAULT_API_KEY_FORM_DRAFT = {
  name: '',
  apiKey: '',
  routingPriority: '1.0',
  upstreamCostMultiplier: '1.0',
  upstreamCostMultiplierSource: 'manual' as const,
  rpmLimit: '',
  rpdLimit: '',
  resetTimezone: '',
}

export type APIKeyFormDraft = {
  name: string
  apiKey: string
  routingPriority: string
  upstreamCostMultiplier: string
  upstreamCostMultiplierSource: 'manual' | 'auto'
  rpmLimit: string
  rpdLimit: string
  resetTimezone: string
}

export type ParsedAPIKeyForm = {
  name?: string
  apiKey?: string
  routingPriority?: number
  upstreamCostMultiplier?: number
  upstreamCostMultiplierSource?: 'auto'
  rpmLimit?: number
  rpdLimit?: number
  resetTimezone?: string
}

export function parseSiteAPIKeyForm(
  draft: APIKeyFormDraft,
  options: {
    includeCostMultiplier: boolean
    requireAPIKey: boolean
    includeRoutingConfig?: boolean
  },
): ParsedAPIKeyForm | null {
  const apiKey = draft.apiKey.trim()
  if (options.requireAPIKey && !apiKey) return null

  const result: ParsedAPIKeyForm = {}
  if (options.includeRoutingConfig !== false) {
    const routingPriority = Number(draft.routingPriority)
    if (
      !Number.isFinite(routingPriority) ||
      routingPriority < 1 ||
      routingPriority > 5
    )
      return null
    if (Math.abs(routingPriority * 10 - Math.round(routingPriority * 10)) > 1e-9)
      return null
    result.name = draft.name.trim()
    result.routingPriority = routingPriority
  }
  if (apiKey) result.apiKey = apiKey

  const rpmStr = (draft.rpmLimit ?? '').trim()
  if (rpmStr !== '') {
    const rpm = Number(rpmStr)
    if (!Number.isFinite(rpm) || !Number.isInteger(rpm) || rpm < 0) return null
    result.rpmLimit = rpm
  }

  const rpdStr = (draft.rpdLimit ?? '').trim()
  if (rpdStr !== '') {
    const rpd = Number(rpdStr)
    if (!Number.isFinite(rpd) || !Number.isInteger(rpd) || rpd < 0) return null
    result.rpdLimit = rpd
  }

  const resetTimezone = (draft.resetTimezone ?? '').trim()
  if (resetTimezone) {
    result.resetTimezone = resetTimezone
  }

  if (!options.includeCostMultiplier) return result

  if (draft.upstreamCostMultiplierSource === 'auto') {
    result.upstreamCostMultiplierSource = 'auto'
    return result
  }

  const upstreamCostMultiplier = Number(draft.upstreamCostMultiplier)
  if (
    !Number.isFinite(upstreamCostMultiplier) ||
    upstreamCostMultiplier < 0.01 ||
    upstreamCostMultiplier > 100
  )
    return null
  if (
    Math.abs(
      upstreamCostMultiplier * 10000 -
        Math.round(upstreamCostMultiplier * 10000),
    ) > 1e-9
  )
    return null
  result.upstreamCostMultiplier = upstreamCostMultiplier
  return result
}
