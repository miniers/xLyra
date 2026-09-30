import type { RouteCandidateItem } from '@/features/routes/api/routes'
import type { CanonicalModelMatrixRow } from '@/features/sites/api/sites'

export type SortMode = 'default' | 'success_asc' | 'success_desc' | 'candidates_asc' | 'candidates_desc'

export type FilterItem = {
  key: string
  label: string
  count: number
  iconPath?: string
  fallbackText?: string
}

export type RouteChannelRow = {
  id: string
  kind: 'api_key' | 'site_model'
  siteId: string
  siteName: string
  siteType: string
  siteEnabled: boolean
  siteModelId: string
  siteModelEnabled: boolean
  upstreamName: string
  apiKeyId?: string
  apiKeyName?: string
  apiKeyEnabled?: boolean
  apiKeyRoutingPriority?: number
  siteRoutingPriority?: number
  apiKeyUpstreamCostMultiplier?: number
  apiKeySecretMissing?: boolean
  apiKeyModelName?: string
  apiKeyModelEnabled?: boolean
  groupName?: string
  current?: boolean
  cooling?: boolean
  candidate?: RouteCandidateItem
  locked?: boolean
  pricing?: CanonicalModelMatrixRow['pricing'][number] | RouteCandidateItem['pricing']
  enabled: boolean
  routable: boolean
  toggleDisabled?: boolean
}

export type RouteSiteModelGroup = {
  siteModelId: string
  siteId: string
  siteName: string
  siteType: string
  upstreamName: string
  siteEnabled: boolean
  siteModelEnabled: boolean
  current: boolean
  locked: boolean
  cooling: boolean
  routable: boolean
  enabledChannelCount: number
  routableChannelCount: number
  channels: RouteChannelRow[]
  candidate?: RouteCandidateItem
}

export type PendingChannelState = {
  id: string
  enabled: boolean
}
