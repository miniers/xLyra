import { useState, type ReactNode } from 'react'
import { useQuery, type UseQueryResult } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { LoaderCircle, LockKeyhole, Minus, Plus, RotateCcw, Save, Timer, Unlock, XCircle } from 'lucide-react'
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
  getModelRoutingLock,
  routeQueryKeys,
  selectRoute,
  temporaryRouteLockExpiresAt,
  type RouteCandidateItem,
  type RouteCooldown,
  type RouteLock,
  type RouteLockPolicy,
  type RouteOverviewItem,
  type RouteScoreAdjustmentDirection,
  type RouteScoreProfile,
  type RouteTrace,
} from '@/features/routes/api/routes'
import { getCanonicalModelMatrix, sitesQueryKeys, type CanonicalModelItem, type RoutingExplorationConfig, type RoutingPreference, type Site, type SiteAPIKey, type SiteModel } from '@/features/sites/api/sites'
import { findCurrentRouteChannel, routeChannelRowsFromMatrix, routeChannelStatus, routeChannelSwitchDisabledReason, routeModelCooldown } from '@/features/routes/lib/route-channels'
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
  anomalyClearingSiteModelId,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  pendingChannelState,
  onToggleChannel,
  onClearCooldown,
  onClearAutomaticAnomaly,
  onAdjustScore,
  onRoutingPreferenceChange,
  routingPreferencePending = false,
  onRoutingLockChange,
  onRoutingLockChannel,
  onRoutingLockDelete,
  routingLockPending = false,
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
  anomalyClearingSiteModelId?: string
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  onClearCooldown: (item: RouteCooldown) => void
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  onRoutingPreferenceChange?: (preference: RoutingPreference) => void
  routingPreferencePending?: boolean
  onRoutingLockChange?: (input: { siteModelId: string; expiresAt?: string | null; policy?: RouteLockPolicy; reason?: string }) => void
  onRoutingLockChannel?: (siteModelId: string) => void
  onRoutingLockDelete?: () => void
  routingLockPending?: boolean
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
  const routingLockQuery = useQuery({
    queryKey: routeQueryKeys.routingLock(modelId),
    queryFn: () => getModelRoutingLock(modelId),
    enabled: Boolean(modelId),
    refetchInterval: 30_000,
  })
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
  const routingLock = routingLockQuery.data
    ? routingLockQuery.data.routing_lock
    : selectionQuery.data?.canonical_model.routing_lock
      ?? candidatesQuery.data?.canonical_model.routing_lock
      ?? null

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
            <RoutingLockSection
              key={`${modelId}:${routingLock?.id ?? 'none'}:${routingLock?.site_model_id ?? ''}:${routingLock?.expires_at ?? ''}:${routingLock?.policy ?? ''}`}
              lock={routingLock}
              rows={channelRows}
              disabled={!onRoutingLockChange || routingLockPending}
              onChange={onRoutingLockChange}
              onDelete={onRoutingLockDelete}
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
            <CandidateScoresSection
              key={currentPreference}
              query={candidatesQuery}
              currentPreference={currentPreference}
              cooldowns={cooldowns}
              anomalyClearingSiteModelId={anomalyClearingSiteModelId}
              scoreAdjustingSiteModelId={scoreAdjustingSiteModelId}
              scoreAdjustingDirection={scoreAdjustingDirection}
              onClearAutomaticAnomaly={onClearAutomaticAnomaly}
              onAdjustScore={onAdjustScore}
              t={t}
            />
            <UpstreamCoverageSection
              rows={channelRows}
              loading={matrixQuery.isLoading || candidatesQuery.isLoading}
              error={matrixQuery.isError ? matrixQuery.error.message : undefined}
              pendingChannelState={pendingChannelState}
              onToggleChannel={onToggleChannel}
              lockedSiteModelId={routingLock?.site_model_id}
              onLockChannel={onRoutingLockChannel}
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

const SCORE_TABLE_COLUMN_WIDTHS = {
  rank: '3.5rem',
  site: '11rem',
  total: '5rem',
  siteScore: '6rem',
  modelScore: '6rem',
  actions: '11rem',
} as const

const SCORE_BREAKDOWN_COLUMN_WIDTHS: Record<string, string> = {
  site_health: '6rem',
  site_success_rate: '7rem',
  site_latency: '6rem',
  model_success_rate: '7rem',
  model_latency: '6rem',
  model_first_byte_latency: '8rem',
  api_key_capacity: '5rem',
  actual_price: '7rem',
  api_key_subscription_expiry_urgency: '9rem',
  api_key_subscription_expiry_rescue_bonus: '9rem',
  automatic_anomaly_penalty: '8rem',
  manual_score_adjustment: '8rem',
}

function scoreBreakdownColumnWidth(key: string) {
  return SCORE_BREAKDOWN_COLUMN_WIDTHS[key] ?? '8rem'
}

function CandidateScoresSection({
  query,
  currentPreference,
  cooldowns,
  anomalyClearingSiteModelId,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  onClearAutomaticAnomaly,
  onAdjustScore,
  t,
}: {
  query: CandidatesQuery
  currentPreference: RoutingPreference
  cooldowns: RouteCooldown[]
  anomalyClearingSiteModelId?: string
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
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
              <CandidateScoreTable
                items={sortCandidatesForPreference(items, preference)}
                preference={preference}
                cooldowns={cooldowns}
                anomalyClearingSiteModelId={anomalyClearingSiteModelId}
                scoreAdjustingSiteModelId={scoreAdjustingSiteModelId}
                scoreAdjustingDirection={scoreAdjustingDirection}
                onClearAutomaticAnomaly={onClearAutomaticAnomaly}
                onAdjustScore={onAdjustScore}
                t={t}
              />
            </TabsContent>
          ))}
        </Tabs>
      ) : (
        <SoftEmpty text={t('details.scores.noCandidates')} />
      )}
    </div>
  )
}

type ScoreBreakdownItem = {
  key: string
  label: string
  value?: number
  raw?: string
  visible?: boolean
}

type CandidateScoreRow = {
  candidate: RouteCandidateItem
  profile?: RouteScoreProfile
  breakdown: ScoreBreakdownItem[]
}

function CandidateScoreTable({
  items,
  preference,
  cooldowns,
  anomalyClearingSiteModelId,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  onClearAutomaticAnomaly,
  onAdjustScore,
  t,
}: {
  items: RouteCandidateItem[]
  preference: RoutingPreference
  cooldowns: RouteCooldown[]
  anomalyClearingSiteModelId?: string
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  t: TFunction
}) {
  const rows = items.map((candidate): CandidateScoreRow => {
    const profile = candidateScoreProfile(candidate, preference)
    return {
      candidate,
      profile,
      breakdown: buildScoreBreakdownItems(candidate, profile, t),
    }
  })
  const columns = scoreBreakdownColumns(rows)

  return (
    <div className="overflow-x-auto rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]">
      <table className="w-max min-w-full table-fixed border-collapse text-xs">
        <colgroup>
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.rank }} />
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.site }} />
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.total }} />
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.siteScore }} />
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.modelScore }} />
          {columns.map((column) => (
            <col key={column.key} style={{ width: scoreBreakdownColumnWidth(column.key) }} />
          ))}
          <col style={{ width: SCORE_TABLE_COLUMN_WIDTHS.actions }} />
        </colgroup>
        <thead>
          <tr className="border-b border-[hsl(var(--glass-divider))] text-left text-muted-soft">
            <th className="sticky left-0 z-20 whitespace-nowrap bg-[hsl(var(--surface-panel))] px-3 py-2 font-medium">
              {t('details.scores.table.rank')}
            </th>
            <th className="sticky left-14 z-20 whitespace-nowrap bg-[hsl(var(--surface-panel))] px-3 py-2 font-medium">
              {t('details.scores.table.target')}
            </th>
            <th className="sticky left-[14.5rem] z-20 whitespace-nowrap bg-[hsl(var(--surface-panel))] px-3 py-2 font-medium shadow-[4px_0_8px_-8px_rgba(0,0,0,0.45)]">
              {t('details.scores.table.total')}
            </th>
            <th className="whitespace-nowrap px-3 py-2 font-medium">{t('details.scores.table.site')}</th>
            <th className="whitespace-nowrap px-3 py-2 font-medium">{t('details.scores.table.model')}</th>
            {columns.map((column) => (
              <th key={column.key} className="px-3 py-2 font-medium leading-tight">
                {column.label}
              </th>
            ))}
            <th className="whitespace-nowrap px-3 py-2 font-medium">{t('details.scores.table.actions')}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map(({ candidate, profile, breakdown }) => {
            const scoreAdjusting = scoreAdjustingSiteModelId === candidate.model.site_model_id
            const anomalyClearing = anomalyClearingSiteModelId === candidate.model.site_model_id
            const cooldown = routeModelCooldown(cooldowns, candidate.site.id, candidate.model.site_model_id)
            const cooling = Boolean(cooldown)
            const stickyBackground = cooling
              ? 'bg-[hsl(var(--warning)/0.08)]'
              : 'bg-[hsl(var(--surface-panel))]'
            const cooldownHint = cooldown
              ? t('details.scores.cooldownHint', {
                duration: formatDuration(cooldown.remaining_seconds),
                reason: routeCooldownReasonLabel(cooldown, t),
              })
              : undefined
            return (
            <tr
              key={`${preference}-${candidate.model.site_model_id}`}
              className={cn(
                'border-b border-[hsl(var(--glass-divider))] last:border-b-0',
                cooling && 'bg-[hsl(var(--warning)/0.08)]',
              )}
            >
              <td className={cn('sticky left-0 z-[1] whitespace-nowrap px-3 py-2 align-top', stickyBackground)}>
                <Badge variant="neutral">#{profile?.rank ?? candidate.rank}</Badge>
              </td>
              <td className={cn('sticky left-14 z-[1] px-3 py-2 align-top', stickyBackground)}>
                <div className="flex min-w-0 flex-wrap items-center gap-1.5">
                  <Badge variant="neutral" className="px-2 py-0.5 text-[11px]">P{candidate.site.routing_priority}</Badge>
                  <span className="min-w-0 truncate font-medium text-foreground" title={candidate.site.name}>
                    {candidate.site.name}
                  </span>
                  {candidate.locked ? (
                    <Badge variant="accent" className="gap-1 px-2 py-0.5 text-[11px]">
                      <LockKeyhole className="h-3 w-3" aria-hidden="true" />
                      {t('details.lock.pinned')}
                    </Badge>
                  ) : null}
                  {cooldown ? (
                    <Badge variant="warning" className="gap-1 px-2 py-0.5 text-[11px]" title={cooldownHint}>
                      <Timer className="h-3 w-3" aria-hidden="true" />
                      {t('details.scores.cooldown')}
                    </Badge>
                  ) : null}
                </div>
              </td>
              <td className={cn('sticky left-[14.5rem] z-[1] whitespace-nowrap px-3 py-2 align-top font-semibold tabular-nums text-foreground shadow-[4px_0_8px_-8px_rgba(0,0,0,0.45)]', stickyBackground)}>
                {formatRouteScore(profile?.score)}
              </td>
              <td className="whitespace-nowrap px-3 py-2 align-top tabular-nums text-muted-soft">
                {formatRouteScore(routeSiteScore({ score_breakdown: profile?.breakdown }))}
              </td>
              <td className="whitespace-nowrap px-3 py-2 align-top tabular-nums text-muted-soft">
                {formatRouteScore(routeModelScore({ score_breakdown: profile?.breakdown }))}
              </td>
              {columns.map((column) => {
                const item = breakdown.find((entry) => entry.key === column.key)
                return (
                  <td key={column.key} className="px-3 py-2 align-top tabular-nums text-muted-soft">
                    {item ? (
                      <div
                        className="max-w-full overflow-hidden text-ellipsis whitespace-nowrap"
                        title={item.raw ? `${item.raw}: ${formatRouteScore(item.value)}` : formatRouteScore(item.value)}
                      >
                        {item.raw ? `(${item.raw})` : ''}: {formatRouteScore(item.value)}
                      </div>
                    ) : (
                      <span>—</span>
                    )}
                  </td>
                )
              })}
              <td className="whitespace-nowrap px-3 py-2 align-top">
                <div className="flex items-center gap-1">
                  <Button
                    size="icon"
                    variant="outline"
                    className="h-8 w-8"
                    onClick={() => onAdjustScore(candidate, 'increase')}
                    disabled={scoreAdjusting}
                    aria-label={t('details.scores.increaseScore')}
                    title={t('details.scores.increaseScore')}
                  >
                    {scoreAdjusting && scoreAdjustingDirection === 'increase' ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Plus className="h-4 w-4" />}
                  </Button>
                  <Button
                    size="icon"
                    variant="outline"
                    className="h-8 w-8"
                    onClick={() => onAdjustScore(candidate, 'decrease')}
                    disabled={scoreAdjusting}
                    aria-label={t('details.scores.decreaseScore')}
                    title={t('details.scores.decreaseScore')}
                  >
                    {scoreAdjusting && scoreAdjustingDirection === 'decrease' ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Minus className="h-4 w-4" />}
                  </Button>
                  {candidate.automatic_anomaly?.active ? (
                    <Button size="sm" variant="outline" onClick={() => onClearAutomaticAnomaly(candidate)} disabled={anomalyClearing}>
                      {anomalyClearing ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <XCircle className="h-4 w-4" />}
                      {t('details.scores.clearAutomaticAnomaly')}
                    </Button>
                  ) : null}
                </div>
              </td>
            </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function buildScoreBreakdownItems(candidate: RouteCandidateItem, profile: RouteScoreProfile | undefined, t: TFunction): ScoreBreakdownItem[] {
  const scoreBreakdown = profile?.breakdown
  const subscriptionExpiryRaw = formatRemainingDuration(candidate.availability.subscription_remaining_seconds, t)
  return ([
    { key: 'site_health', label: t('details.scores.components.siteHealth'), value: scoreBreakdown?.site_health, raw: candidate.health.status },
    { key: 'site_success_rate', label: t('details.scores.components.siteSuccessRate'), value: scoreBreakdown?.site_success_rate, raw: formatScoreRate(candidate.health.recent_success_rate) },
    { key: 'site_latency', label: t('details.scores.components.siteLatency'), value: scoreBreakdown?.site_latency, raw: formatScoreLatency(candidate.health.recent_avg_latency_ms) },
    { key: 'model_success_rate', label: t('details.scores.components.modelSuccessRate'), value: scoreBreakdown?.model_success_rate, raw: formatScoreRate(candidate.health.model_success_rate) },
    { key: 'model_latency', label: t('details.scores.components.modelLatency'), value: scoreBreakdown?.model_latency, raw: formatScoreLatency(candidate.health.model_avg_latency_ms) },
    { key: 'model_first_byte_latency', label: t('details.scores.components.modelFirstByteLatency'), value: scoreBreakdown?.model_first_byte_latency, raw: formatScoreLatency(modelFirstByteLatencyInput(candidate)) },
    { key: 'api_key_capacity', label: t('details.scores.components.apiKeyCapacity'), value: scoreBreakdown?.api_key_capacity, raw: `${candidate.availability.available_api_key_count}/${candidate.availability.total_api_key_count}` },
    { key: 'actual_price', label: t('details.scores.components.actualPrice'), value: scoreBreakdown?.actual_price, raw: formatActualPriceRaw(candidate, t) },
    { key: 'api_key_subscription_expiry_urgency', label: t('details.scores.components.subscriptionExpiryUrgency'), value: scoreBreakdown?.api_key_subscription_expiry_urgency, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
    { key: 'api_key_subscription_expiry_rescue_bonus', label: t('details.scores.components.subscriptionExpiryRescueBonus'), value: scoreBreakdown?.api_key_subscription_expiry_rescue_bonus, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
    { key: 'automatic_anomaly_penalty', label: t('details.scores.components.automaticAnomalyPenalty'), value: scoreBreakdown?.automatic_anomaly_penalty, raw: formatAutomaticAnomalyRaw(candidate, t), visible: candidate.automatic_anomaly?.active === true },
    { key: 'manual_score_adjustment', label: t('details.scores.components.manualScoreAdjustment'), value: scoreBreakdown?.manual_score_adjustment },
  ] as ScoreBreakdownItem[]).filter(({ value, visible = true }) => typeof value === 'number' && visible)
}

function scoreBreakdownColumns(rows: CandidateScoreRow[]) {
  const columns: ScoreBreakdownItem[] = []
  const seen = new Set<string>()
  for (const row of rows) {
    for (const item of row.breakdown) {
      if (seen.has(item.key)) continue
      seen.add(item.key)
      columns.push(item)
    }
  }
  return columns
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

function formatAutomaticAnomalyRaw(candidate: RouteCandidateItem, t: TFunction) {
  const metrics = candidate.automatic_anomaly?.metrics ?? []
  return metrics.map((metric) => {
    if (metric.metric === 'first_byte_latency') {
      return t('details.scores.raw.automaticAnomalyFirstByte', {
        recent: formatScoreLatency(metric.recent_value),
        baseline: formatScoreLatency(metric.baseline_value),
      })
    }
    if (metric.metric === 'cache_hit_rate') {
      return t('details.scores.raw.automaticAnomalyCache', {
        recent: formatScoreRate(metric.recent_value),
        baseline: formatScoreRate(metric.baseline_value),
      })
    }
    return metric.metric
  }).join(',')
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

function RoutingLockSection({
  lock,
  rows,
  disabled,
  onChange,
  onDelete,
  t,
}: {
  lock: RouteLock | null
  rows: RouteChannelRow[]
  disabled: boolean
  onChange?: (input: { siteModelId: string; expiresAt?: string | null; policy?: RouteLockPolicy; reason?: string }) => void
  onDelete?: () => void
  t: TFunction
}) {
  const targetRows = rows.filter((row, index) => rows.findIndex((item) => item.siteModelId === row.siteModelId) === index)
  const [siteModelId, setSiteModelId] = useState(lock?.site_model_id ?? '')
  const [expiresAt, setExpiresAt] = useState(
    lock ? toDateTimeLocal(lock.expires_at) : toDateTimeLocal(temporaryRouteLockExpiresAt()),
  )
  const [policy, setPolicy] = useState<RouteLockPolicy>(lock?.policy ?? 'fallback')
  const [reason, setReason] = useState(lock?.reason ?? '')

  function submit() {
    if (!siteModelId || !onChange) return
    onChange({
      siteModelId,
      expiresAt: dateTimeLocalToISO(expiresAt),
      policy,
      reason: reason.trim() || undefined,
    })
  }

  const target = targetRows.find((row) => row.siteModelId === siteModelId)
  const targetRoutable = rows.some((row) => row.siteModelId === siteModelId && row.routable)
  const targetLabel = target
    ? `${target.siteName} → ${target.upstreamName}`
    : lock?.target
      ? `${lock.target.site_name} → ${lock.target.display_name || lock.target.upstream_model_name}`
      : lock?.site_model_id
  const targetUnavailable = Boolean(lock && (!target || !targetRoutable))
  const remaining = lock?.expires_at ? formatRemainingDuration(lock.remaining_seconds, t) : undefined

  return (
    <div className="space-y-3 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-2.5">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-foreground">
            <LockKeyhole className="h-4 w-4 text-[hsl(var(--accent))]" />
            {t('details.lock.title')}
            {lock ? <Badge variant="accent">{t(`details.lock.policy.${lock.policy}`)}</Badge> : null}
          </div>
          <div className="mt-1 text-xs text-muted-soft">{t('details.lock.description')}</div>
        </div>
        {lock ? (
          <div className="flex items-center gap-2 text-xs text-muted-soft">
            <span>{targetLabel}</span>
            {targetUnavailable ? <Badge variant="warning">{t('details.lock.targetUnavailable')}</Badge> : null}
            <span>{remaining ? t('details.lock.remaining', { duration: remaining }) : t('details.lock.unlimited')}</span>
          </div>
        ) : null}
      </div>
      <div className="grid gap-2 sm:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)_minmax(0,1fr)]">
        <Select value={siteModelId} onValueChange={setSiteModelId} disabled={disabled || (!lock && targetRows.length === 0)}>
          <SelectTrigger aria-label={t('details.lock.target')}>
            <SelectValue placeholder={t('details.lock.target')} />
          </SelectTrigger>
          <SelectContent searchable={false} widthMode="content">
            {lock && !target ? (
              <SelectItem value={lock.site_model_id}>
                {targetLabel}
              </SelectItem>
            ) : null}
            {targetRows.map((row) => (
              <SelectItem key={row.siteModelId} value={row.siteModelId}>
                {row.siteName} → {row.upstreamName}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select value={policy} onValueChange={(value) => setPolicy(value as RouteLockPolicy)} disabled={disabled}>
          <SelectTrigger aria-label={t('details.lock.policy.title')}>
            <SelectValue />
          </SelectTrigger>
          <SelectContent searchable={false} widthMode="content">
            <SelectItem value="strict">{t('details.lock.policy.strict')}</SelectItem>
            <SelectItem value="fallback">{t('details.lock.policy.fallback')}</SelectItem>
          </SelectContent>
        </Select>
        <Input
          type="datetime-local"
          value={expiresAt}
          onChange={(event) => setExpiresAt(event.target.value)}
          disabled={disabled}
          aria-label={t('details.lock.expiresAt')}
        />
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={reason}
          onChange={(event) => setReason(event.target.value)}
          placeholder={t('details.lock.reason')}
          disabled={disabled}
          className="min-w-48 flex-1"
          maxLength={500}
        />
        <Button type="button" size="sm" variant="outline" onClick={submit} disabled={disabled || !siteModelId || !onChange}>
          {disabled ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
          {t('details.lock.save')}
        </Button>
        {lock ? (
          <Button type="button" size="sm" variant="ghost" onClick={onDelete} disabled={disabled || !onDelete}>
            <Unlock className="h-4 w-4" />
            {t('details.lock.remove')}
          </Button>
        ) : null}
      </div>
    </div>
  )
}

function toDateTimeLocal(value?: string | null) {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  const pad = (item: number) => String(item).padStart(2, '0')
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`
}

function dateTimeLocalToISO(value: string) {
  if (!value) return null
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? null : date.toISOString()
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
  lockedSiteModelId,
  onLockChannel,
  t,
}: {
  rows: RouteChannelRow[]
  loading: boolean
  error?: string
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  lockedSiteModelId?: string
  onLockChannel?: (siteModelId: string) => void
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
                locked={row.siteModelId === lockedSiteModelId || row.locked}
                onLock={onLockChannel && row.siteModelId !== lockedSiteModelId ? () => onLockChannel(row.siteModelId) : undefined}
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
  locked = false,
  onLock,
  onToggle,
  t,
}: {
  row: RouteChannelRow
  pending?: PendingChannelState
  current?: boolean
  locked?: boolean
  onLock?: () => void
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
          {locked ? (
            <Badge variant="accent">
              <LockKeyhole className="mr-1 h-3 w-3" />
              {t ? t('details.lock.pinned') : '已置顶'}
            </Badge>
          ) : null}
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
        onToggle || onLock ? (
          <div className="flex items-center gap-2">
            {onLock ? (
              <Button
                size="icon"
                variant="ghost"
                className="h-8 w-8"
                onClick={onLock}
                aria-label={t ? t('details.lock.pinChannel') : `临时置顶 ${row.siteName} ${row.upstreamName}`}
                title={t ? t('details.lock.pinChannel') : '临时置顶渠道'}
              >
                <LockKeyhole className="h-4 w-4" />
              </Button>
            ) : null}
            {onToggle ? (
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
            ) : null}
          </div>
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
