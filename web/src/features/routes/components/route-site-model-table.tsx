import { Fragment, useMemo, useState, type ComponentType, type ReactNode } from 'react'
import * as PopoverPrimitive from '@radix-ui/react-popover'
import { useTranslation } from 'react-i18next'
import {
  ChevronDown,
  ChevronRight,
  Ellipsis,
  FlaskConical,
  LoaderCircle,
  LockKeyhole,
  Minus,
  Plus,
  RotateCcw,
  Save,
  Unlock,
  XCircle,
} from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import {
  temporaryRouteLockExpiresAt,
  type RouteCandidateItem,
  type RouteLock,
  type RouteLockPolicy,
  type RouteScoreAdjustmentDirection,
  type RouteScoreProfile,
} from '@/features/routes/api/routes'
import {
  formatModelHealth,
  formatRemainingDuration,
  formatRoutePricing,
} from '@/features/routes/lib/route-format'
import { formatRouteScore, routeModelScore, routeSiteScore } from '@/features/routes/lib/route-score'
import {
  routeChannelStatus,
  routeChannelSwitchDisabledReason,
  routeSiteModelStatus,
} from '@/features/routes/lib/route-channels'
import type { PendingChannelState, RouteChannelRow, RouteSiteModelGroup } from '@/features/routes/lib/types'
import type { RoutingExplorationConfig, RoutingPreference } from '@/features/sites/api/sites'
import { cn } from '@/lib/utils'

const ROUTING_PREFERENCES: RoutingPreference[] = ['default', 'value', 'speed']

const DEFAULT_EXPLORATION_CONFIG: Omit<RoutingExplorationConfig, 'reset_at'> = {
  enabled: false,
  new_trials_per_site: 5,
  idle_after_hours: 24,
  idle_trials_per_site: 2,
}

type RouteSiteModelTableProps = {
  groups: RouteSiteModelGroup[]
  loading: boolean
  error?: string
  preference: RoutingPreference
  routingPreferencePending: boolean
  onRoutingPreferenceChange?: (preference: RoutingPreference) => void
  rescueEnabled: boolean
  rescuePending: boolean
  onRescueChange?: (enabled: boolean) => void
  exploration?: RoutingExplorationConfig
  explorationPending: boolean
  onExplorationChange?: (config: Omit<RoutingExplorationConfig, 'reset_at'>) => void
  onExplorationReset?: () => void
  onExplorationResetSite?: (siteModelId: string) => void
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  anomalyClearingSiteModelId?: string
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  routingLock: RouteLock | null
  routingLockPending: boolean
  onRoutingLockChange?: (input: { siteModelId: string; expiresAt?: string | null; policy?: RouteLockPolicy; reason?: string }) => void
  onRoutingLockChannel?: (siteModelId: string) => void
  onRoutingLockDelete?: () => void
  onTestSiteModel?: (group: RouteSiteModelGroup) => void
  compact?: boolean
}

export function RouteSiteModelTable({
  groups,
  loading,
  error,
  preference,
  routingPreferencePending,
  onRoutingPreferenceChange,
  rescueEnabled,
  rescuePending,
  onRescueChange,
  exploration,
  explorationPending,
  onExplorationChange,
  onExplorationReset,
  onExplorationResetSite,
  pendingChannelState,
  onToggleChannel,
  anomalyClearingSiteModelId,
  onClearAutomaticAnomaly,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  onAdjustScore,
  routingLock,
  routingLockPending,
  onRoutingLockChange,
  onRoutingLockChannel,
  onRoutingLockDelete,
  onTestSiteModel,
  compact = false,
}: RouteSiteModelTableProps) {
  const { t } = useTranslation('routes')
  const [expandedSiteModelId, setExpandedSiteModelId] = useState<string | null>(null)
  const [lockDialogGroup, setLockDialogGroup] = useState<RouteSiteModelGroup | null>(null)
  const sortedGroups = useMemo(
    () => [...groups].sort((left, right) => compareGroups(left, right, preference)),
    [groups, preference],
  )

  function toggleExpanded(siteModelId: string) {
    setExpandedSiteModelId((current) => current === siteModelId ? null : siteModelId)
  }

  function renderExpanded(group: RouteSiteModelGroup) {
    const profile = candidateScoreProfile(group.candidate, preference)
    const locked = group.locked || routingLock?.site_model_id === group.siteModelId

    return (
      <ExpandedSiteModelDetails
        group={group}
        profile={profile}
        locked={locked}
        pendingChannelState={pendingChannelState}
        onToggleChannel={onToggleChannel}
        t={t}
      />
    )
  }

  return (
    <div className="space-y-3">
      <RoutingControlBar
        preference={preference}
        routingPreferencePending={routingPreferencePending}
        onRoutingPreferenceChange={onRoutingPreferenceChange}
        rescueEnabled={rescueEnabled}
        rescuePending={rescuePending}
        onRescueChange={onRescueChange}
        exploration={exploration}
        explorationPending={explorationPending}
        onExplorationChange={onExplorationChange}
        onExplorationReset={onExplorationReset}
        t={t}
      />

      {loading ? (
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      ) : error ? (
        <TableEmpty text={t('details.table.loadFailed')} />
      ) : sortedGroups.length === 0 ? (
        <TableEmpty text={t('details.table.noModels')} />
      ) : compact ? (
        <div className="space-y-2">
          {sortedGroups.map((group) => {
            const expanded = expandedSiteModelId === group.siteModelId
            return (
              <MobileSiteModelRow
                key={group.siteModelId}
                group={group}
                preference={preference}
                expanded={expanded}
                locked={group.locked || routingLock?.site_model_id === group.siteModelId}
                lockPending={routingLockPending}
                onToggleExpanded={() => toggleExpanded(group.siteModelId)}
                onOpenLock={() => setLockDialogGroup(group)}
                onQuickLock={onRoutingLockChannel ? () => onRoutingLockChannel(group.siteModelId) : undefined}
                onDeleteLock={onRoutingLockDelete}
                onTest={onTestSiteModel}
                onAdjustScore={onAdjustScore}
                scoreAdjustingSiteModelId={scoreAdjustingSiteModelId}
                scoreAdjustingDirection={scoreAdjustingDirection}
                anomalyClearingSiteModelId={anomalyClearingSiteModelId}
                onClearAutomaticAnomaly={onClearAutomaticAnomaly}
                onResetSite={onExplorationResetSite}
                explorationPending={explorationPending}
                t={t}
              >
                {renderExpanded(group)}
              </MobileSiteModelRow>
            )
          })}
        </div>
      ) : (
        <div className="overflow-x-auto rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]">
          <table className="min-w-[920px] w-full table-fixed border-collapse text-xs">
            <colgroup>
              <col style={{ width: '9%' }} />
              <col style={{ width: '21%' }} />
              <col style={{ width: '15%' }} />
              <col style={{ width: '22%' }} />
              <col style={{ width: '12%' }} />
              <col style={{ width: '21%' }} />
            </colgroup>
            <thead>
              <tr className="border-b border-[hsl(var(--glass-divider))] text-left text-muted-soft">
                <th className="px-3 py-2 font-medium">{t('details.table.headers.rank')}</th>
                <th className="px-3 py-2 font-medium">{t('details.table.headers.siteModel')}</th>
                <th className="px-3 py-2 font-medium">{t('details.table.headers.score')}</th>
                <th className="px-3 py-2 font-medium">{t('details.table.headers.healthPricing')}</th>
                <th className="px-3 py-2 font-medium">{t('details.table.headers.exploration')}</th>
                <th className="px-3 py-2 font-medium">{t('details.table.headers.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {sortedGroups.map((group) => {
                const expanded = expandedSiteModelId === group.siteModelId
                const profile = candidateScoreProfile(group.candidate, preference)
                const status = routeSiteModelStatus(group, t)
                const locked = group.locked || routingLock?.site_model_id === group.siteModelId

                return (
                  <Fragment key={group.siteModelId}>
                    <tr className={cn(
                      'cursor-pointer border-b border-[hsl(var(--glass-divider))] align-top transition-colors hover:bg-[hsl(var(--surface-subtle)/0.45)]',
                      expanded && 'bg-[hsl(var(--surface-subtle)/0.55)]',
                    )}
                    onClick={() => toggleExpanded(group.siteModelId)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter' || event.key === ' ') {
                        event.preventDefault()
                        toggleExpanded(group.siteModelId)
                      }
                    }}
                    tabIndex={0}
                    aria-expanded={expanded}
                    >
                      <td className="px-3 py-3">
                        <div className="flex items-center gap-2">
                          <Button
                            size="icon"
                            variant="ghost"
                            className="h-7 w-7 shrink-0"
                            onClick={(event) => {
                              event.stopPropagation()
                              toggleExpanded(group.siteModelId)
                            }}
                            onKeyDown={(event) => event.stopPropagation()}
                            aria-label={t(expanded ? 'details.table.collapse' : 'details.table.expand')}
                            title={t(expanded ? 'details.table.collapse' : 'details.table.expand')}
                          >
                            {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
                          </Button>
                          {profile ? (
                            <span className="tabular-nums font-semibold text-foreground">#{profile.rank}</span>
                          ) : (
                            <span className="text-muted-soft">-</span>
                          )}
                        </div>
                      </td>
                      <td className="px-3 py-3">
                        <div className="min-w-0">
                          <div className="truncate font-medium text-foreground" title={group.siteName}>
                            {group.siteName}
                          </div>
                          <div className="mt-1 truncate text-muted-soft" title={group.upstreamName}>
                            {group.upstreamName}
                          </div>
                          <div className="mt-2 flex flex-wrap items-center gap-1.5">
                            <Badge variant={group.current ? 'accent' : status.badgeVariant}>
                              {group.current ? t('details.currentRoute.current') : status.label}
                            </Badge>
                            {locked ? (
                              <Badge variant="accent" className="gap-1">
                                <LockKeyhole className="h-3 w-3" aria-hidden="true" />
                                {t('details.lock.pinned')}
                              </Badge>
                            ) : null}
                          </div>
                        </div>
                      </td>
                      <td className="px-3 py-3 tabular-nums">
                        {profile ? (
                          <div className="space-y-1">
                            <div className="font-semibold text-foreground">{formatRouteScore(profile.score)}</div>
                            <div className="text-muted-soft">
                              {t('details.scores.site', { score: formatRouteScore(routeSiteScore({ score_breakdown: profile.breakdown })) })}
                            </div>
                            <div className="text-muted-soft">
                              {t('details.scores.model', { score: formatRouteScore(routeModelScore({ score_breakdown: profile.breakdown })) })}
                            </div>
                          </div>
                        ) : (
                          <span className="text-muted-soft">-</span>
                        )}
                      </td>
                      <td className="px-3 py-3 text-muted-soft">
                        <div className="space-y-1">
                          <div title={group.candidate ? formatModelHealth(group.candidate, t) : undefined}>
                            {group.candidate ? formatModelHealth(group.candidate, t) : t('details.table.noHealth')}
                          </div>
                          <div title={formatRoutePricing(group.candidate?.pricing ?? group.channels[0]?.pricing, t)}>
                            {formatRoutePricing(group.candidate?.pricing ?? group.channels[0]?.pricing, t)}
                          </div>
                        </div>
                      </td>
                      <td className="px-3 py-3 text-muted-soft">
                        {group.candidate?.exploration ? (
                          <div className="space-y-1">
                            <div>{formatExplorationStatus(group.candidate.exploration.status, t)}</div>
                            <div className="tabular-nums">{group.candidate.exploration.attempts}/{group.candidate.exploration.target || 0}</div>
                          </div>
                        ) : (
                          <span>-</span>
                        )}
                      </td>
                      <td className="px-3 py-3" onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
                        <SiteModelActions
                          group={group}
                          locked={locked}
                          lockPending={routingLockPending}
                          onOpenLock={() => setLockDialogGroup(group)}
                          onQuickLock={onRoutingLockChannel ? () => onRoutingLockChannel(group.siteModelId) : undefined}
                          onDeleteLock={onRoutingLockDelete}
                          onTest={onTestSiteModel}
                          onAdjustScore={onAdjustScore}
                          scoreAdjustingSiteModelId={scoreAdjustingSiteModelId}
                          scoreAdjustingDirection={scoreAdjustingDirection}
                          anomalyClearingSiteModelId={anomalyClearingSiteModelId}
                          onClearAutomaticAnomaly={onClearAutomaticAnomaly}
                          onResetSite={onExplorationResetSite}
                          explorationPending={explorationPending}
                          t={t}
                        />
                      </td>
                    </tr>
                    {expanded ? (
                      <tr className="border-b border-[hsl(var(--glass-divider))]">
                        <td colSpan={6} className="bg-[hsl(var(--surface-subtle)/0.35)] px-4 py-4">
                          {renderExpanded(group)}
                        </td>
                      </tr>
                    ) : null}
                  </Fragment>
                )
              })}
            </tbody>
          </table>
        </div>
      )}

      {lockDialogGroup ? (
        <RoutingLockDialog
          key={`${lockDialogGroup.siteModelId}:${routingLock?.site_model_id === lockDialogGroup.siteModelId ? routingLock.id : 'new'}`}
          group={lockDialogGroup}
          lock={routingLock?.site_model_id === lockDialogGroup.siteModelId ? routingLock : null}
          disabled={routingLockPending}
          onOpenChange={(open) => {
            if (!open) setLockDialogGroup(null)
          }}
          onChange={(input) => {
            onRoutingLockChange?.(input)
            setLockDialogGroup(null)
          }}
          onDelete={() => {
            onRoutingLockDelete?.()
            setLockDialogGroup(null)
          }}
          t={t}
        />
      ) : null}
    </div>
  )
}

function RoutingControlBar({
  preference,
  routingPreferencePending,
  onRoutingPreferenceChange,
  rescueEnabled,
  rescuePending,
  onRescueChange,
  exploration,
  explorationPending,
  onExplorationChange,
  onExplorationReset,
  t,
}: {
  preference: RoutingPreference
  routingPreferencePending: boolean
  onRoutingPreferenceChange?: (preference: RoutingPreference) => void
  rescueEnabled: boolean
  rescuePending: boolean
  onRescueChange?: (enabled: boolean) => void
  exploration?: RoutingExplorationConfig
  explorationPending: boolean
  onExplorationChange?: (config: Omit<RoutingExplorationConfig, 'reset_at'>) => void
  onExplorationReset?: () => void
  t: TFunction
}) {
  const initial = exploration ? { ...DEFAULT_EXPLORATION_CONFIG, ...exploration } : DEFAULT_EXPLORATION_CONFIG
  const [enabled, setEnabled] = useState(initial.enabled)
  const [newTrials, setNewTrials] = useState(String(initial.new_trials_per_site))
  const [idleAfterHours, setIdleAfterHours] = useState(String(initial.idle_after_hours))
  const [idleTrials, setIdleTrials] = useState(String(initial.idle_trials_per_site))
  const [error, setError] = useState('')

  function submitExploration() {
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
    onExplorationChange?.(values)
  }

  return (
    <div className="space-y-3 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))] px-3 py-3">
      <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
        <div className="flex min-w-52 flex-1 items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="text-sm font-medium text-foreground">{t('details.preference.title')}</div>
            <div className="mt-1 text-xs text-muted-soft">{t('details.preference.description')}</div>
          </div>
          <Select
            value={preference}
            disabled={routingPreferencePending || !onRoutingPreferenceChange}
            onValueChange={(value) => onRoutingPreferenceChange?.(value as RoutingPreference)}
          >
            <SelectTrigger className="w-32 shrink-0">
              <SelectValue />
            </SelectTrigger>
            <SelectContent searchable={false} widthMode="content">
              {ROUTING_PREFERENCES.map((value) => (
                <SelectItem key={value} value={value}>{t(`details.preference.${value}`)}</SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <div className="flex min-w-52 flex-1 items-center justify-between gap-3 border-l border-[hsl(var(--glass-divider))] pl-6">
          <div className="min-w-0">
            <div className="text-sm font-medium text-foreground">{t('details.preference.expiryRescue')}</div>
            <div className="mt-1 text-xs text-muted-soft">{t('details.preference.expiryRescueDescription')}</div>
          </div>
          <Switch
            checked={rescueEnabled}
            disabled={rescuePending || !onRescueChange}
            onCheckedChange={(checked) => onRescueChange?.(checked)}
            aria-label={t('details.preference.expiryRescue')}
          />
        </div>
      </div>
      <div className="border-t border-[hsl(var(--glass-divider))] pt-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="text-sm font-medium text-foreground">{t('details.exploration.title')}</div>
            <div className="mt-1 text-xs text-muted-soft">{t('details.exploration.description')}</div>
          </div>
          <Switch
            checked={enabled}
            disabled={explorationPending || !onExplorationChange}
            onCheckedChange={setEnabled}
            aria-label={t('details.exploration.enabled')}
          />
        </div>
        <div className="mt-3 grid gap-2 sm:grid-cols-3">
          <ExplorationNumberField label={t('details.exploration.newTrials')} value={newTrials} disabled={explorationPending} onChange={setNewTrials} />
          <ExplorationNumberField label={t('details.exploration.idleAfterHours')} value={idleAfterHours} disabled={explorationPending} onChange={setIdleAfterHours} />
          <ExplorationNumberField label={t('details.exploration.idleTrials')} value={idleTrials} disabled={explorationPending} onChange={setIdleTrials} />
        </div>
        {error ? <div className="mt-2 text-xs text-[hsl(var(--danger))]">{error}</div> : null}
        <div className="mt-3 flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="outline" onClick={submitExploration} disabled={explorationPending || !onExplorationChange}>
            {explorationPending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            {t('details.exploration.save')}
          </Button>
          <Button type="button" size="sm" variant="ghost" onClick={onExplorationReset} disabled={explorationPending || !onExplorationReset}>
            <RotateCcw className="h-4 w-4" />
            {t('details.exploration.reset')}
          </Button>
        </div>
      </div>
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

function SiteModelActions({
  group,
  locked,
  lockPending,
  onOpenLock,
  onQuickLock,
  onDeleteLock,
  onTest,
  onAdjustScore,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  anomalyClearingSiteModelId,
  onClearAutomaticAnomaly,
  onResetSite,
  explorationPending,
  t,
}: {
  group: RouteSiteModelGroup
  locked: boolean
  lockPending: boolean
  onOpenLock: () => void
  onQuickLock?: () => void
  onDeleteLock?: () => void
  onTest?: (group: RouteSiteModelGroup) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  anomalyClearingSiteModelId?: string
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onResetSite?: (siteModelId: string) => void
  explorationPending: boolean
  t: TFunction
}) {
  const scoreAdjusting = scoreAdjustingSiteModelId === group.siteModelId
  const anomalyClearing = anomalyClearingSiteModelId === group.siteModelId
  const lockLabel = locked ? t('details.table.editLock') : t('details.table.lock')

  return (
    <div className="flex shrink-0 flex-col items-end gap-1.5">
      {!locked && onQuickLock ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-8 gap-1.5 px-2.5 text-xs"
          onClick={onQuickLock}
          disabled={lockPending}
          aria-label={t('details.table.pinLock')}
          title={t('details.table.pinLock')}
        >
          {lockPending ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <LockKeyhole className="h-4 w-4" />}
          {t('details.table.pinLock')}
        </Button>
      ) : null}
      <SiteModelActionsMenu
        group={group}
        locked={locked}
        lockPending={lockPending}
        lockLabel={lockLabel}
        onOpenLock={onOpenLock}
        onDeleteLock={onDeleteLock}
        onTest={onTest}
        onAdjustScore={onAdjustScore}
        scoreAdjusting={scoreAdjusting}
        scoreAdjustingDirection={scoreAdjustingDirection}
        anomalyClearing={anomalyClearing}
        onClearAutomaticAnomaly={onClearAutomaticAnomaly}
        onResetSite={onResetSite}
        explorationPending={explorationPending}
        t={t}
      />
    </div>
  )
}

function SiteModelActionsMenu({
  group,
  locked,
  lockPending,
  lockLabel,
  onOpenLock,
  onDeleteLock,
  onTest,
  onAdjustScore,
  scoreAdjusting,
  scoreAdjustingDirection,
  anomalyClearing,
  onClearAutomaticAnomaly,
  onResetSite,
  explorationPending,
  t,
}: {
  group: RouteSiteModelGroup
  locked: boolean
  lockPending: boolean
  lockLabel: string
  onOpenLock: () => void
  onDeleteLock?: () => void
  onTest?: (group: RouteSiteModelGroup) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  scoreAdjusting: boolean
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  anomalyClearing: boolean
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onResetSite?: (siteModelId: string) => void
  explorationPending: boolean
  t: TFunction
}) {
  const [open, setOpen] = useState(false)
  const candidate = group.candidate

  function run(action: () => void) {
    setOpen(false)
    action()
  }

  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger asChild>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-8 w-8 text-foreground/70 hover:text-foreground"
          aria-label={t('details.table.more')}
          title={t('details.table.more')}
        >
          <Ellipsis className="h-4 w-4" />
        </Button>
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          align="end"
          sideOffset={6}
          className="z-[120] w-52 overflow-hidden rounded-lg border border-[hsl(var(--glass-border))] bg-[hsl(var(--dialog-surface))] py-1 shadow-[var(--shadow-dialog)] backdrop-blur-xl"
        >
          <ActionItem
            icon={FlaskConical}
            label={t('details.table.test')}
            disabled={!onTest}
            onClick={() => run(() => onTest?.(group))}
          />
          {candidate ? (
            <>
              <ActionItem
                icon={scoreAdjusting && scoreAdjustingDirection === 'increase' ? LoaderCircle : Plus}
                label={t('details.scores.increaseScore')}
                disabled={scoreAdjusting}
                busy={scoreAdjusting && scoreAdjustingDirection === 'increase'}
                onClick={() => run(() => onAdjustScore(candidate, 'increase'))}
              />
              <ActionItem
                icon={scoreAdjusting && scoreAdjustingDirection === 'decrease' ? LoaderCircle : Minus}
                label={t('details.scores.decreaseScore')}
                disabled={scoreAdjusting}
                busy={scoreAdjusting && scoreAdjustingDirection === 'decrease'}
                onClick={() => run(() => onAdjustScore(candidate, 'decrease'))}
              />
              {candidate.automatic_anomaly?.active ? (
                <ActionItem
                  icon={anomalyClearing ? LoaderCircle : XCircle}
                  label={t('details.scores.clearAutomaticAnomaly')}
                  disabled={anomalyClearing}
                  busy={anomalyClearing}
                  onClick={() => run(() => onClearAutomaticAnomaly(candidate))}
                />
              ) : null}
            </>
          ) : null}
          {candidate?.exploration?.enabled && onResetSite ? (
            <ActionItem
              icon={explorationPending ? LoaderCircle : RotateCcw}
              label={t('details.exploration.resetSite')}
              disabled={explorationPending}
              busy={explorationPending}
              onClick={() => run(() => onResetSite(group.siteModelId))}
            />
          ) : null}
          <ActionItem
            icon={LockKeyhole}
            label={lockLabel}
            disabled={lockPending}
            onClick={() => run(onOpenLock)}
          />
          {locked ? (
            <ActionItem
              icon={Unlock}
              label={t('details.table.removeLock')}
              disabled={lockPending || !onDeleteLock}
              onClick={() => run(() => onDeleteLock?.())}
            />
          ) : null}
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}

function ActionItem({
  icon: Icon,
  label,
  disabled,
  busy,
  onClick,
}: {
  icon: ComponentType<{ className?: string }>
  label: string
  disabled?: boolean
  busy?: boolean
  onClick: () => void
}) {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClick}
      className="flex w-full items-center gap-3 px-3 py-2 text-left text-sm font-medium text-foreground transition-colors hover:bg-[hsl(var(--surface-subtle))] disabled:cursor-not-allowed disabled:opacity-40"
    >
      <Icon className={cn('h-4 w-4 text-current', busy && 'animate-spin')} />
      {label}
    </button>
  )
}

function MobileSiteModelRow({
  group,
  preference,
  expanded,
  locked,
  lockPending,
  onToggleExpanded,
  onOpenLock,
  onQuickLock,
  onDeleteLock,
  onTest,
  onAdjustScore,
  scoreAdjustingSiteModelId,
  scoreAdjustingDirection,
  anomalyClearingSiteModelId,
  onClearAutomaticAnomaly,
  onResetSite,
  explorationPending,
  t,
  children,
}: {
  group: RouteSiteModelGroup
  preference: RoutingPreference
  expanded: boolean
  locked: boolean
  lockPending: boolean
  onToggleExpanded: () => void
  onOpenLock: () => void
  onQuickLock?: () => void
  onDeleteLock?: () => void
  onTest?: (group: RouteSiteModelGroup) => void
  onAdjustScore: (candidate: RouteCandidateItem, direction: RouteScoreAdjustmentDirection) => void
  scoreAdjustingSiteModelId?: string
  scoreAdjustingDirection?: RouteScoreAdjustmentDirection
  anomalyClearingSiteModelId?: string
  onClearAutomaticAnomaly: (candidate: RouteCandidateItem) => void
  onResetSite?: (siteModelId: string) => void
  explorationPending: boolean
  t: TFunction
  children: ReactNode
}) {
  const profile = candidateScoreProfile(group.candidate, preference)
  const status = routeSiteModelStatus(group, t)

  return (
    <div className={cn(
      'overflow-hidden rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]',
      expanded && 'border-[hsl(var(--primary)/0.45)]',
    )}>
      <div
        className="flex cursor-pointer items-start gap-2 p-3 transition-colors hover:bg-[hsl(var(--surface-subtle)/0.45)]"
        onClick={onToggleExpanded}
        onKeyDown={(event) => {
          if (event.key === 'Enter' || event.key === ' ') {
            event.preventDefault()
            onToggleExpanded()
          }
        }}
        tabIndex={0}
        aria-expanded={expanded}
      >
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="mt-0.5 h-7 w-7 shrink-0"
          onClick={(event) => {
            event.stopPropagation()
            onToggleExpanded()
          }}
          onKeyDown={(event) => event.stopPropagation()}
          aria-label={t(expanded ? 'details.table.collapse' : 'details.table.expand')}
          title={t(expanded ? 'details.table.collapse' : 'details.table.expand')}
        >
          {expanded ? <ChevronDown className="h-4 w-4" /> : <ChevronRight className="h-4 w-4" />}
        </Button>
        <div className="min-w-0 flex-1 space-y-2">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-foreground">{group.siteName}</div>
            <div className="truncate text-xs text-muted-soft">{group.upstreamName}</div>
          </div>
          <div className="flex flex-wrap items-center gap-1.5">
            {profile ? <Badge variant="neutral">#{profile.rank} · {formatRouteScore(profile.score)}</Badge> : null}
            <Badge variant={group.current ? 'accent' : status.badgeVariant}>
              {group.current ? t('details.currentRoute.current') : status.label}
            </Badge>
            {locked ? (
              <Badge variant="accent" className="gap-1">
                <LockKeyhole className="h-3 w-3" aria-hidden="true" />
                {t('details.lock.pinned')}
              </Badge>
            ) : null}
          </div>
          <div className="flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-soft">
            {group.candidate?.exploration ? (
              <span>{formatExplorationStatus(group.candidate.exploration.status, t)} · {group.candidate.exploration.attempts}/{group.candidate.exploration.target || 0}</span>
            ) : null}
          </div>
        </div>
        <div onClick={(event) => event.stopPropagation()} onKeyDown={(event) => event.stopPropagation()}>
          <SiteModelActions
            group={group}
            locked={locked}
            lockPending={lockPending}
            onOpenLock={onOpenLock}
            onQuickLock={onQuickLock}
            onDeleteLock={onDeleteLock}
            onTest={onTest}
            onAdjustScore={onAdjustScore}
            scoreAdjustingSiteModelId={scoreAdjustingSiteModelId}
            scoreAdjustingDirection={scoreAdjustingDirection}
            anomalyClearingSiteModelId={anomalyClearingSiteModelId}
            onClearAutomaticAnomaly={onClearAutomaticAnomaly}
            onResetSite={onResetSite}
            explorationPending={explorationPending}
            t={t}
          />
        </div>
      </div>
      {expanded ? <div className="border-t border-[hsl(var(--glass-divider))] px-3 py-3">{children}</div> : null}
    </div>
  )
}

function ExpandedSiteModelDetails({
  group,
  profile,
  locked,
  pendingChannelState,
  onToggleChannel,
  t,
}: {
  group: RouteSiteModelGroup
  profile?: RouteScoreProfile
  locked: boolean
  pendingChannelState?: PendingChannelState
  onToggleChannel: (row: RouteChannelRow, enabled: boolean) => void
  t: TFunction
}) {
  const breakdown = profile && group.candidate
    ? buildScoreBreakdownItems(group.candidate, profile, t)
    : []

  return (
    <div className="space-y-4">
      <div>
        <div className="mb-2 text-xs font-medium text-muted-soft">{t('details.table.scoreDetails')}</div>
        {breakdown.length ? (
          <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
            {breakdown.map((item) => (
              <div key={item.key} className="min-w-0 rounded-md bg-[hsl(var(--surface-panel))] px-2.5 py-2">
                <div className="truncate text-[11px] text-muted-soft" title={item.label}>{item.label}</div>
                <div className="mt-1 truncate tabular-nums text-xs text-foreground" title={item.raw}>
                  {item.raw ?? '-'} <span className="text-muted-soft">({formatRouteScore(item.value)})</span>
                </div>
              </div>
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-soft">{t('details.scores.unavailable')}</div>
        )}
      </div>
      <div>
        <div className="mb-2 flex flex-wrap items-center gap-2 text-xs font-medium text-muted-soft">
          <span>{t('details.table.channelDetails')}</span>
          {locked ? <Badge variant="accent">{t('details.lock.pinned')}</Badge> : null}
        </div>
        {group.channels.length ? (
          <div className="divide-y divide-[hsl(var(--glass-divider))] rounded-md border border-[hsl(var(--glass-border))]">
            {group.channels.map((row) => (
              <RouteSiteModelChannelRow
                key={row.id}
                row={row}
                pending={pendingChannelState?.id === row.id ? pendingChannelState : undefined}
                onToggle={onToggleChannel}
                t={t}
              />
            ))}
          </div>
        ) : (
          <div className="text-xs text-muted-soft">{t('details.table.noChannels')}</div>
        )}
      </div>
    </div>
  )
}

function RouteSiteModelChannelRow({
  row,
  pending,
  onToggle,
  t,
}: {
  row: RouteChannelRow
  pending?: PendingChannelState
  onToggle: (row: RouteChannelRow, enabled: boolean) => void
  t: TFunction
}) {
  const status = routeChannelStatus(row, t)
  const checked = pending ? pending.enabled : row.enabled
  const disabledReason = routeChannelSwitchDisabledReason(row, pending, t)
  const metadata = [
    row.apiKeyName,
    row.groupName ? t('details.coverage.group', { name: row.groupName }) : undefined,
    row.apiKeyRoutingPriority != null ? `P${row.apiKeyRoutingPriority}` : undefined,
    row.apiKeyUpstreamCostMultiplier != null ? `${row.apiKeyUpstreamCostMultiplier}x` : undefined,
    formatModelHealth(row.candidate, t),
    formatRoutePricing(row.pricing, t),
  ].filter(Boolean)

  return (
    <div className="flex items-start justify-between gap-3 px-2.5 py-2.5">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant={row.current ? 'accent' : status.badgeVariant}>
            {row.current ? t('details.currentRoute.current') : status.label}
          </Badge>
          {row.apiKeyName ? <Badge variant="neutral">{row.apiKeyName}</Badge> : null}
          {row.locked ? (
            <Badge variant="accent" className="gap-1">
              <LockKeyhole className="h-3 w-3" aria-hidden="true" />
              {t('details.lock.pinned')}
            </Badge>
          ) : null}
        </div>
        <div className="mt-1 truncate text-xs font-medium text-foreground" title={row.upstreamName}>
          {row.upstreamName}
        </div>
        {metadata.length ? (
          <div className="mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px] text-muted-soft">
            {metadata.map((value) => <span key={value}>{value}</span>)}
          </div>
        ) : null}
      </div>
      <span className={cn('inline-flex shrink-0', disabledReason && 'cursor-not-allowed')} title={disabledReason}>
        <Switch
          checked={checked}
          disabled={Boolean(disabledReason)}
          className={cn(disabledReason && 'cursor-not-allowed opacity-50')}
          aria-label={t('details.coverage.switchLabel', {
            site: row.siteName,
            apiKey: row.apiKeyName ?? '',
            model: row.upstreamName,
          })}
          onCheckedChange={(enabled) => onToggle(row, enabled)}
        />
      </span>
    </div>
  )
}

function RoutingLockDialog({
  group,
  lock,
  disabled,
  onOpenChange,
  onChange,
  onDelete,
  t,
}: {
  group: RouteSiteModelGroup
  lock: RouteLock | null
  disabled: boolean
  onOpenChange: (open: boolean) => void
  onChange?: (input: { siteModelId: string; expiresAt?: string | null; policy?: RouteLockPolicy; reason?: string }) => void
  onDelete?: () => void
  t: TFunction
}) {
  const [expiresAt, setExpiresAt] = useState(
    lock ? toDateTimeLocal(lock.expires_at) : toDateTimeLocal(temporaryRouteLockExpiresAt()),
  )
  const [policy, setPolicy] = useState<RouteLockPolicy>(lock?.policy ?? 'fallback')
  const [reason, setReason] = useState(lock?.reason ?? '')
  const remaining = lock?.expires_at ? formatRemainingDuration(lock.remaining_seconds, t) : undefined
  const targetUnavailable = Boolean(lock && (!group.routable || group.siteModelId !== lock.site_model_id))

  function submit() {
    if (!onChange) return
    onChange({
      siteModelId: group.siteModelId,
      expiresAt: dateTimeLocalToISO(expiresAt),
      policy,
      reason: reason.trim() || undefined,
    })
  }

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent className="w-[min(92vw,640px)]">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <LockKeyhole className="h-4 w-4 text-[hsl(var(--accent))]" />
            {lock ? t('details.table.editLock') : t('details.table.lock')}
          </DialogTitle>
          <DialogDescription>{t('details.lock.description')}</DialogDescription>
        </DialogHeader>
        <DialogBody className="space-y-4">
          <div className="rounded-md bg-[hsl(var(--surface-subtle))] px-3 py-2.5">
            <div className="text-xs text-muted-soft">{t('details.lock.target')}</div>
            <div className="mt-1 text-sm font-medium text-foreground">{group.siteName} / {group.upstreamName}</div>
            {lock ? (
              <div className="mt-1 flex flex-wrap gap-2 text-xs text-muted-soft">
                {targetUnavailable ? <Badge variant="warning">{t('details.lock.targetUnavailable')}</Badge> : null}
                <span>{remaining ? t('details.lock.remaining', { duration: remaining }) : t('details.lock.unlimited')}</span>
              </div>
            ) : null}
          </div>
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="space-y-1 text-xs text-muted-soft">
              <span>{t('details.lock.policy.title')}</span>
              <Select value={policy} onValueChange={(value) => setPolicy(value as RouteLockPolicy)} disabled={disabled}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent searchable={false} widthMode="content">
                  <SelectItem value="strict">{t('details.lock.policy.strict')}</SelectItem>
                  <SelectItem value="fallback">{t('details.lock.policy.fallback')}</SelectItem>
                </SelectContent>
              </Select>
            </label>
            <label className="space-y-1 text-xs text-muted-soft">
              <span>{t('details.lock.expiresAt')}</span>
              <Input type="datetime-local" value={expiresAt} disabled={disabled} onChange={(event) => setExpiresAt(event.target.value)} />
            </label>
          </div>
          <label className="space-y-1 text-xs text-muted-soft">
            <span>{t('details.lock.reason')}</span>
            <Input value={reason} maxLength={500} disabled={disabled} onChange={(event) => setReason(event.target.value)} />
          </label>
        </DialogBody>
        <DialogFooter>
          {lock ? (
            <Button type="button" variant="ghost" onClick={onDelete} disabled={disabled || !onDelete}>
              <Unlock className="h-4 w-4" />
              {t('details.table.removeLock')}
            </Button>
          ) : null}
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={disabled}>
            {t('details.table.cancel')}
          </Button>
          <Button type="button" onClick={submit} disabled={disabled || !onChange}>
            {disabled ? <LoaderCircle className="h-4 w-4 animate-spin" /> : <Save className="h-4 w-4" />}
            {t('details.lock.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

type ScoreBreakdownItem = {
  key: string
  label: string
  value?: number
  raw?: string
  visible?: boolean
}

function candidateScoreProfile(candidate: RouteCandidateItem | undefined, preference: RoutingPreference): RouteScoreProfile | undefined {
  if (!candidate) return undefined
  const profile = candidate.score_profiles?.[preference]
  if (profile) return profile
  if (preference !== 'default') return undefined
  return {
    rank: candidate.rank,
    score: candidate.score,
    breakdown: candidate.score_breakdown ?? {},
  }
}

function compareGroups(left: RouteSiteModelGroup, right: RouteSiteModelGroup, preference: RoutingPreference) {
  const leftProfile = candidateScoreProfile(left.candidate, preference)
  const rightProfile = candidateScoreProfile(right.candidate, preference)
  if (leftProfile && rightProfile && leftProfile.rank !== rightProfile.rank) return leftProfile.rank - rightProfile.rank
  if (leftProfile && !rightProfile) return -1
  if (!leftProfile && rightProfile) return 1
  const siteDiff = left.siteName.localeCompare(right.siteName, 'zh-CN', { numeric: true, sensitivity: 'base' })
  if (siteDiff !== 0) return siteDiff
  return left.upstreamName.localeCompare(right.upstreamName, 'zh-CN', { numeric: true, sensitivity: 'base' })
}

function buildScoreBreakdownItems(candidate: RouteCandidateItem, profile: RouteScoreProfile, t: TFunction): ScoreBreakdownItem[] {
  const scoreBreakdown = profile.breakdown
  const subscriptionExpiryRaw = formatRemainingDuration(candidate.availability.subscription_remaining_seconds, t)
  return [
    { key: 'site_health', label: t('details.scores.components.siteHealth'), value: scoreBreakdown.site_health, raw: candidate.health.status },
    { key: 'site_success_rate', label: t('details.scores.components.siteSuccessRate'), value: scoreBreakdown.site_success_rate, raw: formatScoreRate(candidate.health.recent_success_rate) },
    { key: 'site_latency', label: t('details.scores.components.siteLatency'), value: scoreBreakdown.site_latency, raw: formatScoreLatency(candidate.health.recent_avg_latency_ms) },
    { key: 'model_success_rate', label: t('details.scores.components.modelSuccessRate'), value: scoreBreakdown.model_success_rate, raw: formatScoreRate(candidate.health.model_success_rate) },
    { key: 'model_latency', label: t('details.scores.components.modelLatency'), value: scoreBreakdown.model_latency, raw: formatScoreLatency(candidate.health.model_avg_latency_ms) },
    { key: 'model_first_byte_latency', label: t('details.scores.components.modelFirstByteLatency'), value: scoreBreakdown.model_first_byte_latency, raw: formatScoreLatency(modelFirstByteLatencyInput(candidate)) },
    { key: 'api_key_capacity', label: t('details.scores.components.apiKeyCapacity'), value: scoreBreakdown.api_key_capacity, raw: `${candidate.availability.available_api_key_count}/${candidate.availability.total_api_key_count}` },
    { key: 'actual_price', label: t('details.scores.components.actualPrice'), value: scoreBreakdown.actual_price, raw: formatActualPriceRaw(candidate, t) },
    { key: 'api_key_subscription_expiry_urgency', label: t('details.scores.components.subscriptionExpiryUrgency'), value: scoreBreakdown.api_key_subscription_expiry_urgency, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
    { key: 'api_key_subscription_expiry_rescue_bonus', label: t('details.scores.components.subscriptionExpiryRescueBonus'), value: scoreBreakdown.api_key_subscription_expiry_rescue_bonus, raw: subscriptionExpiryRaw, visible: subscriptionExpiryRaw !== undefined },
    { key: 'automatic_anomaly_penalty', label: t('details.scores.components.automaticAnomalyPenalty'), value: scoreBreakdown.automatic_anomaly_penalty, raw: formatAutomaticAnomalyRaw(candidate, t), visible: candidate.automatic_anomaly?.active === true },
    { key: 'manual_score_adjustment', label: t('details.scores.components.manualScoreAdjustment'), value: scoreBreakdown.manual_score_adjustment },
  ].filter(({ value, visible = true }) => typeof value === 'number' && visible)
}

function formatScoreRate(value?: number | null) {
  return typeof value === 'number' ? `${Math.round(value * 100)}%` : undefined
}

function formatScoreLatency(value?: number | null) {
  return typeof value === 'number' ? `${Math.round(value)} ms` : undefined
}

function formatActualPriceRaw(candidate: RouteCandidateItem, t: TFunction) {
  const parts = [formatScoreMultiplier(candidate.pricing.upstream_cost_multiplier)]
  if (typeof candidate.pricing.per_request_value === 'number') return parts.join(',')

  const cacheHitRate = formatScoreRate(candidate.health.model_cache_hit_rate)
  if (cacheHitRate !== undefined) parts.push(t('details.scores.raw.actualPriceCacheHitRate', { value: cacheHitRate }))
  if (typeof candidate.pricing.cache_read_ratio === 'number') parts.push(t('details.scores.raw.actualPriceCacheReadRatio', { value: `${candidate.pricing.cache_read_ratio}x` }))
  return parts.join(',')
}

function formatAutomaticAnomalyRaw(candidate: RouteCandidateItem, t: TFunction) {
  return (candidate.automatic_anomaly?.metrics ?? []).map((metric) => {
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
  if (requestCount > 0 && requestCount < 3) return candidate.health.model_avg_latency_ms
  return candidate.health.model_avg_first_byte_latency_ms
}

function formatScoreMultiplier(value?: number | null) {
  const multiplier = typeof value === 'number' && value > 0 ? value : 1
  return `${multiplier}x`
}

function formatExplorationStatus(status: string, t: TFunction) {
  return t(`details.exploration.status.${status}`, { defaultValue: status })
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

function TableEmpty({ text }: { text: string }) {
  return <div className="rounded-md border border-[hsl(var(--glass-border))] px-4 py-8 text-center text-sm text-muted-soft">{text}</div>
}

type TFunction = (key: string, options?: Record<string, unknown>) => string
