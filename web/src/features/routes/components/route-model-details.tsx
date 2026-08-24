import { useState, type ReactNode } from 'react'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { LoaderCircle, RotateCcw, Save, XCircle } from 'lucide-react'
import { StatusBadge } from '@/components/common/status-badge'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { cn } from '@/lib/utils'
import {
  listRouteCandidates,
  listRouteTraces,
  selectRoute,
  type RouteCandidateItem,
  type RouteCooldown,
  type RouteOverviewItem,
  type RouteScoreProfile,
  type RouteTrace,
} from '@/features/routes/api/routes'
import { getCanonicalModelMatrix, sitesQueryKeys, type CanonicalModelItem, type RoutingExplorationConfig, type RoutingPreference, type Site, type SiteAPIKey, type SiteModel } from '@/features/sites/api/sites'
import { findCurrentRouteChannel, routeChannelRowsFromMatrix, routeChannelStatus, routeChannelSwitchDisabledReason } from '@/features/routes/lib/route-channels'
import {
  formatCooldownScope,
  formatDateTime,
  formatDuration,
  formatLatency,
  formatModelHealth,
  formatRemainingDuration,
  formatRoutePricing,
  routeCooldownReasonLabel,
  routeCooldownSiteName,
  routeCooldownSourceLabel,
  routeCooldownTargetLabel,
} from '@/features/routes/lib/route-format'
import { formatRouteScore, routeModelScore, routeSiteScore } from '@/features/routes/lib/route-score'
import type { PendingChannelState, RouteChannelRow } from '@/features/routes/lib/types'

const EMPTY_TRACES: RouteTrace[] = []

type SelectionQuery = UseQueryResult<Awaited<ReturnType<typeof selectRoute>>>
type CandidatesQuery = UseQueryResult<Awaited<ReturnType<typeof listRouteCandidates>>>
type TracesQuery = UseQueryResult<Awaited<ReturnType<typeof listRouteTraces>>>

export function RouteModelDetails({
  item,
  canonical,
  selectionQuery,
  candidatesQuery,
  tracesQuery,
  cooldowns,
  sites,
  modelsMap,
  apiKeysMap,
  cooldownsLoading,
  clearingId,
  pendingChannelState,
  onToggleChannel,
  onClearCooldown,
  onRoutingPreferenceChange,
  routingPreferencePending = false,
  onRoutingExpiryRescueChange,
  routingExpiryRescuePending = false,
  onRoutingExplorationChange,
  onRoutingExplorationReset,
  onRoutingExplorationResetSite,
  routingExplorationPending = false,
  compact = false,
}: {
  item?: RouteOverviewItem
  canonical?: CanonicalModelItem
  selectionQuery: SelectionQuery
  candidatesQuery: CandidatesQuery
  tracesQuery: TracesQuery
  cooldowns: RouteCooldown[]
  sites: Site[]
  modelsMap: Record<string, SiteModel[]>
  apiKeysMap: Record<string, SiteAPIKey[]>
  cooldownsLoading: boolean
  clearingId?: string
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  onClearCooldown: (item: RouteCooldown) => void
  onRoutingPreferenceChange?: (preference: RoutingPreference) => void
  routingPreferencePending?: boolean
  onRoutingExpiryRescueChange?: (enabled: boolean) => void
  routingExpiryRescuePending?: boolean
  onRoutingExplorationChange?: (config: Omit<RoutingExplorationConfig, 'reset_at'>) => void
  onRoutingExplorationReset?: () => void
  onRoutingExplorationResetSite?: (siteModelId: string) => void
  routingExplorationPending?: boolean
  compact?: boolean
}) {
  const { t } = useTranslation('routes')
  const modelId = item?.canonical_model.id || selectionQuery.data?.canonical_model.id || ''
  const currentPreference = canonical?.routing_preference ?? selectionQuery.data?.canonical_model.routing_preference ?? 'default'
  const currentExpiryRescueEnabled = canonical?.routing_expiry_rescue_enabled
    ?? selectionQuery.data?.canonical_model.routing_expiry_rescue_enabled
    ?? false
  const matrixQuery = useQuery({
    queryKey: [...sitesQueryKeys.all, 'routes-matrix', modelId],
    queryFn: () => getCanonicalModelMatrix(modelId),
    enabled: Boolean(modelId),
  })
  const channelRows = routeChannelRowsFromMatrix(
    matrixQuery.data?.items ?? [],
    sites,
    apiKeysMap,
    selectionQuery.data?.selected,
    candidatesQuery.data?.items ?? [],
    cooldowns,
  )
  const currentChannel = findCurrentRouteChannel(channelRows, selectionQuery.data?.selected)

  return (
    <div className={cn('border-t border-[hsl(var(--glass-divider))] pb-4 pt-3', compact ? 'px-3' : 'px-4')}>
      <Tabs defaultValue="routing" variant="segmented">
        <TabsList className={compact ? 'h-10' : undefined}>
          <TabsTrigger value="routing" className={compact ? 'px-2 text-xs' : undefined}>
            {t('details.tabs.routing')}
          </TabsTrigger>
          <TabsTrigger value="traces" className={compact ? 'px-2 text-xs' : undefined}>
            {t('details.tabs.traces')}
          </TabsTrigger>
          <TabsTrigger value="cooldowns" className={cn('gap-1.5', compact && 'px-2 text-xs')}>
            {t('details.tabs.cooldowns')}
            {cooldowns.length > 0 ? (
              <span className="inline-flex min-w-5 items-center justify-center rounded-full bg-[hsl(var(--warning)/0.15)] px-1.5 py-0.5 text-[11px] font-medium text-[hsl(var(--warning))]">
                {cooldowns.length}
              </span>
            ) : null}
          </TabsTrigger>
        </TabsList>
        <TabsContent value="routing">
          <div className="space-y-5">
            <CurrentRouteSection query={selectionQuery} currentChannel={currentChannel} t={t} />
            <RoutingPreferenceSection
              value={currentPreference}
              disabled={!onRoutingPreferenceChange || routingPreferencePending}
              onChange={onRoutingPreferenceChange}
              rescueEnabled={currentExpiryRescueEnabled}
              rescueDisabled={!onRoutingExpiryRescueChange || routingExpiryRescuePending}
              onRescueChange={onRoutingExpiryRescueChange}
              t={t}
            />
            <RoutingExplorationSection
              key={`${modelId}:${canonical?.routing_exploration?.reset_at ?? ''}:${canonical?.routing_exploration?.enabled ?? ''}`}
              config={canonical?.routing_exploration ?? selectionQuery.data?.canonical_model.routing_exploration}
              candidates={candidatesQuery.data?.items ?? []}
              disabled={routingExplorationPending}
              onChange={onRoutingExplorationChange}
              onReset={onRoutingExplorationReset}
              onResetSite={onRoutingExplorationResetSite}
              t={t}
            />
            <CandidateScoresSection key={currentPreference} query={candidatesQuery} currentPreference={currentPreference} t={t} />
            <UpstreamCoverageSection
              rows={channelRows}
              loading={matrixQuery.isLoading || candidatesQuery.isLoading}
              error={matrixQuery.isError ? matrixQuery.error.message : undefined}
              pendingChannelState={pendingChannelState}
              onToggleChannel={onToggleChannel}
              t={t}
            />
          </div>
        </TabsContent>
        <TabsContent value="traces">
          <RouteTracesSection query={tracesQuery} t={t} />
        </TabsContent>
        <TabsContent value="cooldowns">
          <CooldownsSection
            items={cooldowns}
            sites={sites}
            modelsMap={modelsMap}
            loading={cooldownsLoading}
            clearingId={clearingId}
            onClear={onClearCooldown}
            t={t}
          />
        </TabsContent>
      </Tabs>
    </div>
  )
}

const ROUTING_PREFERENCES: RoutingPreference[] = ['default', 'value', 'speed']

function CandidateScoresSection({
  query,
  currentPreference,
  t,
}: {
  query: CandidatesQuery
  currentPreference: RoutingPreference
  t: TFunction
}) {
  const [selectedPreference, setSelectedPreference] = useState<RoutingPreference>(currentPreference)
  const items = query.data?.items ?? []

  const preferenceItems = ROUTING_PREFERENCES.map((preference) => ({
    preference,
    label: t(`details.preference.${preference}`),
    total: preferenceTotal(items, preference),
  }))

  return (
    <div className="space-y-3">
      <SectionTitle>{t('details.scores.title')}</SectionTitle>
      {query.isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : query.isError ? (
        <SoftEmpty text={t('details.scores.loadFailed')} />
      ) : items.length ? (
        <Tabs
          value={selectedPreference}
          onValueChange={(value) => setSelectedPreference(value as RoutingPreference)}
        >
          <TabsList className="h-auto w-full flex-wrap justify-start gap-1">
            {preferenceItems.map(({ preference, label, total }) => (
              <TabsTrigger key={preference} value={preference} className="gap-1.5 px-3 py-2 text-xs">
                <span>{label}</span>
                <span className="tabular-nums text-muted-soft">{formatRouteScore(total)}</span>
              </TabsTrigger>
            ))}
          </TabsList>
          {ROUTING_PREFERENCES.map((preference) => (
            <TabsContent key={preference} value={preference}>
              <div className="space-y-2">
                {sortCandidatesForPreference(items, preference).map((candidate) => (
                  <CandidateScoreLine
                    key={`${preference}-${candidate.model.site_model_id}`}
                    candidate={candidate}
                    profile={candidateScoreProfile(candidate, preference)}
                    t={t}
                  />
                ))}
              </div>
            </TabsContent>
          ))}
        </Tabs>
      ) : (
        <SoftEmpty text={t('details.scores.noCandidates')} />
      )}
    </div>
  )
}

function CandidateScoreLine({
  candidate,
  profile,
  t,
}: {
  candidate: RouteCandidateItem
  profile?: RouteScoreProfile
  t: TFunction
}) {
  const scoreBreakdown = profile?.breakdown
  const subscriptionExpiryRaw = formatRemainingDuration(candidate.availability.subscription_remaining_seconds, t)
  const breakdownItems = ([
    { label: t('details.scores.components.siteHealth'), value: scoreBreakdown?.site_health, raw: candidate.health.status },
    { label: t('details.scores.components.siteSuccessRate'), value: scoreBreakdown?.site_success_rate, raw: formatScoreRate(candidate.health.recent_success_rate) },
    { label: t('details.scores.components.siteLatency'), value: scoreBreakdown?.site_latency, raw: formatScoreLatency(candidate.health.recent_avg_latency_ms) },
    { label: t('details.scores.components.modelSuccessRate'), value: scoreBreakdown?.model_success_rate, raw: formatScoreRate(candidate.health.model_success_rate) },
    { label: t('details.scores.components.modelLatency'), value: scoreBreakdown?.model_latency, raw: formatScoreLatency(candidate.health.model_avg_latency_ms) },
    { label: t('details.scores.components.modelFirstByteLatency'), value: scoreBreakdown?.model_first_byte_latency, raw: formatScoreLatency(modelFirstByteLatencyInput(candidate)) },
    { label: t('details.scores.components.apiKeyCapacity'), value: scoreBreakdown?.api_key_capacity, raw: `${candidate.availability.available_api_key_count}/${candidate.availability.total_api_key_count}` },
    { label: t('details.scores.components.actualPrice'), value: scoreBreakdown?.actual_price, raw: formatActualPriceRaw(candidate, t) },
    { label: t('details.scores.components.subscriptionExpiryUrgency'), value: scoreBreakdown?.api_key_subscription_expiry_urgency, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
    { label: t('details.scores.components.subscriptionExpiryRescueBonus'), value: scoreBreakdown?.api_key_subscription_expiry_rescue_bonus, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
  ] as Array<{ label: string; value: number | undefined; raw?: string; visible?: boolean }>).filter(({ value, visible = true }) => typeof value === 'number' && visible)

  return (
    <div className="rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-2.5">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            <Badge variant="neutral">{t('details.scores.rank', { rank: profile?.rank ?? candidate.rank })}</Badge>
            <Badge variant="accent">{t('details.scores.total', { score: formatRouteScore(profile?.score) })}</Badge>
            <Badge variant="neutral">P{candidate.site.routing_priority}</Badge>
            <span className="min-w-0 break-all text-sm font-medium text-foreground">
              {candidate.site.name} → {candidate.model.upstream_model_name}
            </span>
          </div>
          <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-soft">
            <span>{t('details.scores.site', { score: formatRouteScore(routeSiteScore({ score_breakdown: scoreBreakdown })) })}</span>
            <span>{t('details.scores.model', { score: formatRouteScore(routeModelScore({ score_breakdown: scoreBreakdown })) })}</span>
          </div>
        </div>
      </div>
      {breakdownItems.length ? (
        <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-soft">
          {breakdownItems.map(({ label, value, raw }) => (
            <span key={label}>{label}{raw ? `(${raw})` : ''}: {formatRouteScore(value)}</span>
          ))}
        </div>
      ) : (
        <div className="mt-2 text-xs text-muted-soft">{t('details.scores.unavailable')}</div>
      )}
    </div>
  )
}

function formatScoreRate(value?: number | null) {
  return typeof value === 'number' ? `${Math.round(value * 100)}%` : undefined
}

function formatScoreLatency(value?: number | null) {
  return typeof value === 'number' ? `${Math.round(value)} ms` : undefined
}

function formatScoreMultiplier(value?: number | null) {
  const multiplier = typeof value === 'number' && value > 0 ? value : 1
  return `${multiplier}x`
}

function formatActualPriceRaw(candidate: RouteCandidateItem, t: TFunction) {
  const parts = [formatScoreMultiplier(candidate.pricing.upstream_cost_multiplier)]
  if (typeof candidate.pricing.per_request_value === 'number') return parts.join(',')

  const cacheHitRate = formatScoreRate(candidate.health.model_cache_hit_rate)
  if (cacheHitRate !== undefined) {
    parts.push(t('details.scores.raw.actualPriceCacheHitRate', { value: cacheHitRate }))
  }
  if (typeof candidate.pricing.cache_read_ratio === 'number') {
    parts.push(t('details.scores.raw.actualPriceCacheReadRatio', { value: `${candidate.pricing.cache_read_ratio}x` }))
  }
  return parts.join(',')
}

function modelFirstByteLatencyInput(candidate: RouteCandidateItem) {
  const requestCount = candidate.health.model_first_byte_request_count ?? 0
  if (requestCount > 0 && requestCount < 3) {
    return candidate.health.model_avg_latency_ms
  }
  return candidate.health.model_avg_first_byte_latency_ms
}

function candidateScoreProfile(candidate: RouteCandidateItem, preference: RoutingPreference) {
  const profile = candidate.score_profiles?.[preference]
  if (profile) return profile
  if (preference !== 'default') return undefined
  return {
    rank: candidate.rank,
    score: candidate.score,
    breakdown: candidate.score_breakdown ?? {},
  }
}

function preferenceTotal(items: RouteCandidateItem[], preference: RoutingPreference) {
  const ranked = items
    .map((candidate) => candidateScoreProfile(candidate, preference))
    .filter((profile): profile is RouteScoreProfile => Boolean(profile))
    .sort((a, b) => a.rank - b.rank)
  return ranked[0]?.score
}

function sortCandidatesForPreference(items: RouteCandidateItem[], preference: RoutingPreference) {
  return items.toSorted((a, b) => {
    const aProfile = candidateScoreProfile(a, preference)
    const bProfile = candidateScoreProfile(b, preference)
    if (aProfile && bProfile && aProfile.rank !== bProfile.rank) return aProfile.rank - bProfile.rank
    if (aProfile && bProfile && aProfile.score !== bProfile.score) return bProfile.score - aProfile.score
    return a.rank - b.rank
  })
}

function RoutingPreferenceSection({
  value,
  disabled,
  onChange,
  rescueEnabled,
  rescueDisabled,
  onRescueChange,
  t,
}: {
  value: RoutingPreference
  disabled: boolean
  onChange?: (preference: RoutingPreference) => void
  rescueEnabled: boolean
  rescueDisabled: boolean
  onRescueChange?: (enabled: boolean) => void
  t: TFunction
}) {
  return (
    <div className="space-y-3 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-2.5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-foreground">{t('details.preference.title')}</div>
          <div className="mt-1 text-xs text-muted-soft">{t('details.preference.description')}</div>
        </div>
        <Select
          value={value}
          disabled={disabled}
          onValueChange={(next) => onChange?.(next as RoutingPreference)}
        >
          <SelectTrigger className="w-36">
            <SelectValue />
          </SelectTrigger>
          <SelectContent searchable={false} widthMode="content">
            <SelectItem value="default">{t('details.preference.default')}</SelectItem>
            <SelectItem value="value">{t('details.preference.value')}</SelectItem>
            <SelectItem value="speed">{t('details.preference.speed')}</SelectItem>
          </SelectContent>
        </Select>
      </div>
      <div className="flex items-center justify-between gap-3 border-t border-[hsl(var(--glass-divider))] pt-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-foreground">{t('details.preference.expiryRescue')}</div>
          <div className="mt-1 text-xs text-muted-soft">{t('details.preference.expiryRescueDescription')}</div>
        </div>
        <Switch
          checked={rescueEnabled}
          disabled={rescueDisabled}
          onCheckedChange={(checked) => onRescueChange?.(checked)}
          aria-label={t('details.preference.expiryRescue')}
        />
      </div>
    </div>
  )
}

const DEFAULT_EXPLORATION_CONFIG: Omit<RoutingExplorationConfig, 'reset_at'> = {
  enabled: false,
  new_trials_per_site: 5,
  idle_after_hours: 24,
  idle_trials_per_site: 2,
}

function RoutingExplorationSection({
  config,
  candidates,
  disabled,
  onChange,
  onReset,
  onResetSite,
  t,
}: {
  config?: RoutingExplorationConfig
  candidates: RouteCandidateItem[]
  disabled: boolean
  onChange?: (config: Omit<RoutingExplorationConfig, 'reset_at'>) => void
  onReset?: () => void
  onResetSite?: (siteModelId: string) => void
  t: TFunction
}) {
  const initial = config ? { ...DEFAULT_EXPLORATION_CONFIG, ...config } : DEFAULT_EXPLORATION_CONFIG
  const [enabled, setEnabled] = useState(initial.enabled)
  const [newTrials, setNewTrials] = useState(String(initial.new_trials_per_site))
  const [idleAfterHours, setIdleAfterHours] = useState(String(initial.idle_after_hours))
  const [idleTrials, setIdleTrials] = useState(String(initial.idle_trials_per_site))
  const [error, setError] = useState('')

  function submit() {
    const values = {
      enabled,
      new_trials_per_site: Number(newTrials),
      idle_after_hours: Number(idleAfterHours),
      idle_trials_per_site: Number(idleTrials),
    }
    if (!Number.isInteger(values.new_trials_per_site) || values.new_trials_per_site < 1 || values.new_trials_per_site > 100) {
      setError(t('details.exploration.validationTrials'))
      return
    }
    if (!Number.isInteger(values.idle_after_hours) || values.idle_after_hours < 1 || values.idle_after_hours > 720) {
      setError(t('details.exploration.validationIdleHours'))
      return
    }
    if (!Number.isInteger(values.idle_trials_per_site) || values.idle_trials_per_site < 1 || values.idle_trials_per_site > 100) {
      setError(t('details.exploration.validationIdleTrials'))
      return
    }
    setError('')
    onChange?.(values)
  }

  const activeCandidates = candidates.filter((candidate) => candidate.exploration?.enabled)

  return (
    <div className="space-y-3 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-3">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="text-sm font-medium text-foreground">{t('details.exploration.title')}</div>
          <div className="mt-1 text-xs text-muted-soft">{t('details.exploration.description')}</div>
        </div>
        <Switch checked={enabled} disabled={disabled} onCheckedChange={setEnabled} aria-label={t('details.exploration.enabled')} />
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <ExplorationNumberField label={t('details.exploration.newTrials')} value={newTrials} disabled={disabled} onChange={setNewTrials} />
        <ExplorationNumberField label={t('details.exploration.idleAfterHours')} value={idleAfterHours} disabled={disabled} onChange={setIdleAfterHours} />
        <ExplorationNumberField label={t('details.exploration.idleTrials')} value={idleTrials} disabled={disabled} onChange={setIdleTrials} />
      </div>
      {error ? <div className="text-xs text-[hsl(var(--danger))]">{error}</div> : null}
      <div className="flex flex-wrap gap-2">
        <Button type="button" size="sm" variant="outline" onClick={submit} disabled={disabled || !onChange}>
          <Save className="h-4 w-4" />
          {t('details.exploration.save')}
        </Button>
        <Button type="button" size="sm" variant="ghost" onClick={onReset} disabled={disabled || !onReset}>
          <RotateCcw className="h-4 w-4" />
          {t('details.exploration.reset')}
        </Button>
      </div>
      {enabled ? (
        <div className="space-y-2 border-t border-[hsl(var(--glass-divider))] pt-3">
          <div className="text-xs font-medium text-muted-soft">{t('details.exploration.progress')}</div>
          {activeCandidates.length === 0 ? (
            <div className="text-xs text-muted-soft">{t('details.exploration.noCandidates')}</div>
          ) : (
            <div className="divide-y divide-[hsl(var(--glass-divider))] rounded border border-[hsl(var(--glass-border))]">
              {activeCandidates.map((candidate) => {
                const exploration = candidate.exploration
                if (!exploration) return null
                return (
                  <div key={candidate.model.site_model_id} className="flex min-h-9 items-center gap-2 px-2.5 py-1.5 text-xs">
                    <span className="min-w-0 truncate text-foreground">{candidate.site.name}</span>
                    <span className="ml-auto shrink-0 text-muted-soft">
                      {t(`details.exploration.status.${exploration.status}`, { defaultValue: exploration.status })} · {exploration.attempts}/{exploration.target || 0}
                    </span>
                    <Button
                      type="button"
                      size="sm"
                      variant="ghost"
                      className="h-7 px-2 text-xs"
                      onClick={() => onResetSite?.(candidate.model.site_model_id)}
                      disabled={disabled || !onResetSite}
                    >
                      <RotateCcw className="h-3.5 w-3.5" />
                      {t('details.exploration.resetSite')}
                    </Button>
                  </div>
                )
              })}
            </div>
          )}
        </div>
      ) : null}
    </div>
  )
}

function ExplorationNumberField({
  label,
  value,
  disabled,
  onChange,
}: {
  label: string
  value: string
  disabled: boolean
  onChange: (value: string) => void
}) {
  return (
    <label className="space-y-1 text-xs text-muted-soft">
      <span>{label}</span>
      <Input type="number" min={1} value={value} disabled={disabled} onChange={(event) => onChange(event.target.value)} />
    </label>
  )
}

type TFunction = (key: string, options?: Record<string, unknown>) => string

function CurrentRouteSection({ query, currentChannel, t }: { query: SelectionQuery; currentChannel?: RouteChannelRow; t: TFunction }) {
  if (query.isLoading) {
    return (
      <div className="space-y-3">
        <SectionTitle>{t('details.currentRoute.title')}</SectionTitle>
        <Skeleton className="h-16 w-full" />
      </div>
    )
  }

  if (query.isError) {
    return (
      <div className="space-y-3">
        <SectionTitle>{t('details.currentRoute.title')}</SectionTitle>
        <RouteInfoLine
          badges={<Badge variant="warning">{t('details.currentRoute.noAvailableRoute')}</Badge>}
          primary={t('details.currentRoute.fallbackHint')}
        />
      </div>
    )
  }

  if (!query.data) {
    return null
  }

  return (
    <div className="space-y-3">
      <SectionTitle>{t('details.currentRoute.title')}</SectionTitle>
      {currentChannel ? <RouteChannelLine row={currentChannel} current t={t} /> : <CandidateRouteLine candidate={query.data.selected} current t={t} />}
    </div>
  )
}

function UpstreamCoverageSection({
  rows,
  loading,
  error,
  pendingChannelState,
  onToggleChannel,
  t,
}: {
  rows: RouteChannelRow[]
  loading: boolean
  error?: string
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  t: TFunction
}) {
  return (
    <div className="space-y-3">
      <SectionTitle>{t('details.coverage.title')}</SectionTitle>
      {loading ? (
        <div className="space-y-3">
          <Skeleton className="h-14 w-full" />
          <Skeleton className="h-14 w-full" />
        </div>
      ) : error ? (
        <SoftEmpty text={t('details.coverage.loadFailed')} />
      ) : rows.length ? (
        <div className="divide-y divide-[hsl(var(--glass-divider))]">
          {rows.map((row) => {
            return (
              <RouteChannelLine
                key={row.id}
                row={row}
                pending={pendingChannelState?.id === row.id ? pendingChannelState : undefined}
                onToggle={(enabled) => onToggleChannel(row, enabled)}
                t={t}
              />
            )
          })}
        </div>
      ) : (
        <SoftEmpty text={t('details.coverage.noConfig')} />
      )}
    </div>
  )
}

function RouteChannelLine({
  row,
  pending,
  current,
  onToggle,
  t,
}: {
  row: RouteChannelRow
  pending?: PendingChannelState
  current?: boolean
  onToggle?: (enabled: boolean) => void
  t?: TFunction
}) {
  const status = routeChannelStatus(row, t)
  const checked = pending ? pending.enabled : row.enabled
  const disabledReason = routeChannelSwitchDisabledReason(row, pending, t)

  return (
    <RouteInfoLine
      badges={
        <>
          <Badge variant={current ? 'accent' : status.badgeVariant}>{current ? (t ? t('details.currentRoute.current') : '当前使用') : status.label}</Badge>
          {row.candidate ? <ScoreBadge candidate={row.candidate} t={t} /> : null}
          {row.apiKeyName ? (
            <Badge variant="neutral" title={row.groupName ? `${row.apiKeyName} · ${row.groupName}` : row.apiKeyName}>
              {row.apiKeyName}
            </Badge>
          ) : null}
          {row.apiKeyRoutingPriority != null ? <Badge variant="neutral">P{row.apiKeyRoutingPriority}</Badge> : null}
          {row.apiKeyUpstreamCostMultiplier != null ? <Badge variant="neutral">{row.apiKeyUpstreamCostMultiplier}x</Badge> : null}
        </>
      }
      primary={`${row.siteName} → ${row.upstreamName}`}
      meta={[
        row.groupName ? (t ? t('details.coverage.group', { name: row.groupName }) : `分组 ${row.groupName}`) : '',
        formatModelHealth(row.candidate, t),
        formatRoutePricing(row.pricing, t),
      ].filter(Boolean)}
      action={
        onToggle ? (
          <span className={cn('inline-flex', disabledReason && 'cursor-not-allowed')} title={disabledReason}>
            <Switch
              checked={checked}
              disabled={Boolean(disabledReason)}
              className={cn(disabledReason && 'cursor-not-allowed opacity-50')}
              aria-label={
                t
                  ? t('details.coverage.switchLabel', {
                      site: row.siteName,
                      apiKey: row.apiKeyName ?? '',
                      model: row.upstreamName,
                    })
                  : `切换 ${row.siteName} ${row.apiKeyName ?? ''} ${row.upstreamName}`
              }
              onCheckedChange={onToggle}
            />
          </span>
        ) : undefined
      }
    />
  )
}

function CandidateRouteLine({ candidate, current, t }: { candidate: RouteCandidateItem; current?: boolean; t?: TFunction }) {
  return (
    <RouteInfoLine
      badges={
        <>
          <Badge variant={current ? 'accent' : 'neutral'}>
            {current
              ? t
                ? t('details.currentRoute.current')
                : '当前使用'
              : t
                ? t('details.currentRoute.routable', { rank: candidate.rank })
              : `可路由 #${candidate.rank}`}
          </Badge>
          <ScoreBadge candidate={candidate} t={t} />
          {candidate.pricing.group_name ? <Badge variant="neutral">{candidate.pricing.group_name}</Badge> : null}
          {candidate.credential.name ? <Badge variant="neutral">{candidate.credential.name}</Badge> : null}
        </>
      }
      primary={`${candidate.site.name} → ${candidate.model.upstream_model_name}`}
      meta={[
        `Key ${candidate.availability.available_api_key_count}/${candidate.availability.total_api_key_count}`,
        candidate.site.supports_api_key_cost_multiplier
          ? `P${candidate.credential.routing_priority} · ${candidate.credential.upstream_cost_multiplier}x`
          : `P${candidate.credential.routing_priority}`,
        formatModelHealth(candidate, t),
        formatRoutePricing(candidate.pricing, t),
      ]}
    />
  )
}

function ScoreBadge({ candidate, t }: { candidate: RouteCandidateItem; t?: TFunction }) {
  const total = formatRouteScore(candidate.score)
  const title = t
    ? t('details.scores.tooltip', {
        total,
        site: formatRouteScore(routeSiteScore(candidate)),
        model: formatRouteScore(routeModelScore(candidate)),
      })
    : `总分 ${total} · 站点 ${formatRouteScore(routeSiteScore(candidate))} · 模型 ${formatRouteScore(routeModelScore(candidate))}`

  return (
    <Badge variant="neutral" title={title}>
      {t ? t('details.scores.badge', { score: total }) : `评分 ${total}`}
    </Badge>
  )
}

function RouteInfoLine({ badges, primary, meta, action }: { badges?: ReactNode; primary: string; meta?: string[]; action?: ReactNode }) {
  return (
    <div className="py-2.5">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-2">
            {badges}
            <span className="min-w-0 break-all text-sm font-medium text-foreground">{primary}</span>
          </div>
          {meta?.length ? (
            <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-sm text-muted-soft">
              {meta.map((value) => (
                <span key={value}>{value}</span>
              ))}
            </div>
          ) : null}
        </div>
        {action ? <div className="shrink-0 pt-0.5">{action}</div> : null}
      </div>
    </div>
  )
}

function RouteTracesSection({ query, t }: { query: TracesQuery; t: TFunction }) {
  const items = (query.data?.items ?? EMPTY_TRACES).slice(0, 5)

  return (
    <div className="space-y-3">
      <SectionTitle>{t('details.traces.title')}</SectionTitle>
      {query.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-12 w-full" />
          <Skeleton className="h-12 w-full" />
        </div>
      ) : query.isError ? (
        <SoftEmpty text={t('details.traces.loadFailed')} />
      ) : items.length ? (
        <div className="divide-y divide-[hsl(var(--glass-divider))]">
          {items.map((trace) => {
            const attempt = trace.attempts.find((item) => item.success) ?? trace.attempts.at(-1)
            return (
              <RouteInfoLine
                key={trace.parent_request_id}
                badges={
                  <StatusBadge status={trace.success ? 'healthy' : 'error'}>
                    {trace.success ? t('details.traces.success') : t('details.traces.failure')}
                  </StatusBadge>
                }
                primary={attempt?.site.name ?? '-'}
                meta={[
                  formatDateTime(trace.started_at),
                  formatLatency(trace.total_latency_ms),
                  attempt?.credential.name ? `${t('format.upstreamCredential')} ${attempt.credential.name}` : '',
                  trace.failover_count > 0
                    ? t('details.traces.failover', {
                        count: trace.failover_count,
                      })
                    : '',
                ].filter(Boolean)}
              />
            )
          })}
        </div>
      ) : (
        <SoftEmpty text={t('details.traces.noTraces')} />
      )}
    </div>
  )
}

function CooldownsSection({
  items,
  sites,
  modelsMap,
  loading,
  clearingId,
  onClear,
  t,
}: {
  items: RouteCooldown[]
  sites: Site[]
  modelsMap: Record<string, SiteModel[]>
  loading: boolean
  clearingId?: string
  onClear: (item: RouteCooldown) => void
  t: TFunction
}) {
  return (
    <div className="space-y-3">
      <SectionTitle>{t('details.cooldowns.title')}</SectionTitle>
      {loading ? (
        <Skeleton className="h-20 w-full" />
      ) : items.length ? (
        <div className="max-h-[300px] divide-y divide-[hsl(var(--glass-divider))] overflow-y-auto pr-1">
          {items.map((item) => {
            const clearing = clearingId === item.site_id
            const siteName = routeCooldownSiteName(item, sites, t)
            const target = routeCooldownTargetLabel(item, modelsMap, t)
            const source = routeCooldownSourceLabel(item, t)
            const reason = routeCooldownReasonLabel(item, t)

            return (
              <div key={item.id} className="flex items-start justify-between gap-3 py-2.5">
                <div className="min-w-0 flex-1">
                  <RouteInfoLine
                    badges={
                      <>
                        <Badge variant="warning">{formatCooldownScope(item.scope, t)}</Badge>
                        <Badge variant="neutral">{source}</Badge>
                      </>
                    }
                    primary={`${siteName} · ${target}`}
                    meta={[
                      reason,
                      t('details.cooldowns.remaining', {
                        duration: formatDuration(item.remaining_seconds),
                      }),
                    ].filter(Boolean)}
                  />
                </div>
                <Button size="sm" variant="outline" onClick={() => onClear(item)} disabled={clearing}>
                  {clearing ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <XCircle className="h-4 w-4" />}
                  {t('details.cooldowns.clear')}
                </Button>
              </div>
            )
          })}
        </div>
      ) : (
        <SoftEmpty text={t('details.cooldowns.noCooldowns')} />
      )}
    </div>
  )
}

function SectionTitle({ children }: { children: string }) {
  return <div className="text-xs font-medium tracking-[0.14em] text-[hsl(var(--text-muted-soft))]">{children}</div>
}

function SoftEmpty({ text }: { text: string }) {
  return <div className="text-muted-soft rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-3 text-sm">{text}</div>
}
