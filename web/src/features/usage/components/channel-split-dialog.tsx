import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { EmptyState } from '@/components/common/empty-state'
import { ErrorState } from '@/components/common/error-state'
import { Button } from '@/components/ui/button'
import { DatePicker } from '@/components/ui/date-picker'
import { MultiSelect, type MultiSelectOption } from '@/components/ui/multi-select'
import {
  Dialog,
  DialogBody,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Progress } from '@/components/ui/progress'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  analyticsQueryKeys,
  getAnalyticsUsage,
  type AnalyticsSeriesPoint,
  type AnalyticsUsage,
  type AnalyticsUsageParams,
} from '@/features/analytics/api/analytics'
import type { OAuthConnectionListItem } from '@/features/oauth/api/oauth'
import type { Site } from '@/features/sites/api/sites'
import { isOAuthSite, siteTypeIcon } from '@/features/sites/lib/site-utils'
import {
  channelSplitQueryKeys,
  getChannelSplit,
  type ChannelSplitInput,
  type ChannelSplitItem,
  type ChannelSplitSummary,
  type ChannelSplitTarget,
} from '@/features/usage/api/channel-split'
import { formatCompactNumber, formatDashboardCurrency, formatPercent } from '@/features/dashboard/lib/dashboard-utils'
import { formatLatencyMs } from '@/features/analytics/lib/analytics-utils'
import { useMobileLayout } from '@/hooks/use-media-query'
import { cn } from '@/lib/utils'

type ChannelSplitMetric = 'cost' | 'tokens'
type ChannelSplitDimension = 'api_key' | 'model'
type ChannelSplitModelRow = {
  model_key: string
  model_name: string
  request_count: number
  success_count: number
  failure_count: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  cache_write_tokens: number
  cache_creation_input_tokens: number
  cache_write_cost: number
  total_tokens: number
  estimated_cost: number
  cost_by_currency: Record<string, number>
  currency: string
  token_share: number
  cost_share: number
}
type ChannelSplitRow = ChannelSplitItem | ChannelSplitModelRow

type HourlyUsagePoint = {
  hour: string
  request_count: number
  success_count: number
  failure_count: number
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  total_tokens: number
  estimated_cost: number
  currency: string
}

type ChannelSplitDialogProps = {
  open: boolean
  initialTarget?: ChannelSplitTarget | null
  sites: Site[]
  oauthConnections: OAuthConnectionListItem[]
  onOpenChange: (open: boolean) => void
}

const QUICK_RANGES = [1, 7, 30] as const

export function ChannelSplitDialog({
  open,
  initialTarget,
  sites,
  oauthConnections,
  onOpenChange,
}: ChannelSplitDialogProps) {
  const { t } = useTranslation('oauth')
  const isMobile = useMobileLayout()
  const [metric, setMetric] = useState<ChannelSplitMetric>('cost')
  const [dimension, setDimension] = useState<ChannelSplitDimension>('api_key')
  const initialSelection = useMemo(() => initialChannelSplitSelection(initialTarget, oauthConnections), [initialTarget, oauthConnections])
  const [selectedSiteIds, setSelectedSiteIds] = useState(() => initialSelection.siteIds)
  const [oauthConnectionId] = useState(() => initialSelection.oauthConnectionId)
  const [dateRange, setDateRange] = useState(() => naturalDayRange(30))
  const [allTime, setAllTime] = useState(false)
  const siteOptions = useMemo(() => buildSiteOptions(sites, {
    deleted: t('split.filters.deletedSite'),
    oauth: t('split.filters.oauthGroup'),
    sites: t('split.filters.sitesGroup'),
  }), [sites, t])
  const dateFrom = formatDateParam(dateRange.from)
  const dateTo = formatDateParam(dateRange.to)
  const isSingleDay = Boolean(dateFrom && dateTo && dateFrom === dateTo && !allTime)
  const queryInput = useMemo<ChannelSplitInput | null>(() => {
    if (!selectedSiteIds.length) return null
    if (allTime) {
      return { siteIds: selectedSiteIds, oauthConnectionId, range: 'all' }
    }
    if (!dateFrom || !dateTo) return null
    return { siteIds: selectedSiteIds, oauthConnectionId, dateFrom, dateTo }
  }, [allTime, dateFrom, dateTo, oauthConnectionId, selectedSiteIds])

  const splitQuery = useQuery({
    queryKey: queryInput ? channelSplitQueryKeys.detail(queryInput) : channelSplitQueryKeys.all,
    queryFn: () => {
      if (!queryInput) throw new Error(t('split.targetRequired'))
      return getChannelSplit(queryInput)
    },
    enabled: open && Boolean(queryInput),
    placeholderData: keepPreviousData,
  })

  const analyticsParams = useMemo<AnalyticsUsageParams | null>(() => {
    if (!queryInput) return null
    if (allTime) {
      return {
        from: '1970-01-01',
        site_ids: selectedSiteIds,
        group_by: 'model',
        include_contributions: false,
      }
    }
    if (!dateFrom || !dateTo) return null
    return {
      from: dateFrom,
      to: dateTo,
      site_ids: selectedSiteIds,
      group_by: 'model',
      include_contributions: false,
    }
  }, [allTime, dateFrom, dateTo, queryInput, selectedSiteIds])

  const analyticsQuery = useQuery({
    queryKey: analyticsParams ? analyticsQueryKeys.usage(analyticsParams) : analyticsQueryKeys.all,
    queryFn: () => {
      if (!analyticsParams) throw new Error(t('split.targetRequired'))
      return getAnalyticsUsage(analyticsParams)
    },
    enabled: open && Boolean(analyticsParams),
    placeholderData: keepPreviousData,
  })

  const visibleData = queryInput ? splitQuery.data : undefined
  const actualSummary = visibleData?.summary
  const emptySummary = useMemo(() => emptyChannelSplitSummary(allTime, dateFrom, dateTo), [allTime, dateFrom, dateTo])
  const summary = actualSummary ?? emptySummary
  const items = visibleData?.items ?? []
  const analyticsUsage = analyticsParams ? analyticsQuery.data : undefined
  const modelItems = useMemo(() => buildModelRows(analyticsUsage), [analyticsUsage])
  const trendPoints = useMemo(() => buildUsageTrendPoints(analyticsUsage), [analyticsUsage])
  const analyticsTotals = analyticsUsage?.totals
  const analyticsHasMultipleCurrencies = (analyticsUsage?.meta.available_currencies?.length ?? 0) > 1
  const useAnalyticsTotals = Boolean(analyticsTotals && !analyticsHasMultipleCurrencies && (analyticsTotals.requests > 0 || !hasChannelSplitUsage(summary)))
  const displaySummary = useAnalyticsTotals && analyticsTotals
    ? {
        ...summary,
        request_count: analyticsTotals.requests,
        success_count: analyticsTotals.success_count,
        failure_count: analyticsTotals.failure_count,
        prompt_tokens: analyticsTotals.prompt_tokens,
        completion_tokens: analyticsTotals.completion_tokens,
        cached_tokens: analyticsTotals.cached_tokens,
        total_tokens: analyticsTotals.total_tokens,
        estimated_cost: analyticsTotals.cost,
        cost_by_currency: analyticsTotals.cost_by_currency,
        currency: analyticsUsage?.meta.currency || summary.currency,
      }
      : summary
  const analyticsMetrics = useAnalyticsTotals ? analyticsTotals : undefined
  const rows: ChannelSplitRow[] = dimension === 'model' ? modelItems : items
  const currency = displaySummary.currency ?? 'USD'
  const hasMultipleCurrencies = Object.keys(displaySummary.cost_by_currency ?? {}).filter((code) => (displaySummary.cost_by_currency?.[code] ?? 0) > 0).length > 1
  const activeQuickRange = allTime ? null : QUICK_RANGES.find((days) => sameDayRange(dateRange, naturalDayRange(days)))
  const chartMetric = metric === 'cost' && !hasCostData(displaySummary) ? 'tokens' : metric
  const hasUsage = hasChannelSplitUsage(displaySummary)
  const hasTokenUsage = hasChannelSplitTokenUsage(displaySummary)

  function applyQuickRange(days: number) {
    setAllTime(false)
    setDateRange(naturalDayRange(days))
  }

  function applyAllTime() {
    setAllTime(true)
  }

  function setFromDate(date: Date | undefined) {
    if (!date) return
    setAllTime(false)
    const nextFrom = startLocalDay(date)
    setDateRange((current) => ({
      from: nextFrom,
      to: current.to < nextFrom ? nextFrom : current.to,
    }))
  }

  function setToDate(date: Date | undefined) {
    if (!date) return
    setAllTime(false)
    const nextTo = startLocalDay(date)
    setDateRange((current) => ({
      from: current.from > nextTo ? nextTo : current.from,
      to: nextTo,
    }))
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent
        className={cn(
          'max-w-none grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-2xl',
          isMobile
            ? 'h-[84dvh] w-[min(94vw,1180px)]'
            : 'aspect-[16/10] w-[min(96vw,140.8vh,1180px)]',
        )}
      >
        <DialogHeader className={isMobile ? 'px-4 py-4' : 'px-6 py-5'}>
          <DialogTitle>{t('split.title')}</DialogTitle>
          <DialogDescription>
            {actualSummary
              ? t('split.descriptionWithTarget', {
                  target: splitTargetTitle(summary),
                  range: summary.range_all ? t('split.ranges.all') : t('split.ranges.between', { from: summary.date_from, to: summary.date_to }),
                })
              : t('split.description')}
          </DialogDescription>
          <p className="text-xs text-muted-soft">{t('split.observationNotice')}</p>
        </DialogHeader>
        <DialogBody
          className={cn(
            'min-h-0',
            isMobile ? 'overflow-y-auto px-4 py-4' : 'overflow-hidden px-6 py-5',
          )}
        >
          <div className={cn('flex flex-col', isMobile ? 'min-h-full gap-3' : 'h-full min-h-0 gap-5')}>
            <div className={cn(
              'grid shrink-0',
              isMobile ? 'grid-cols-2 gap-2' : 'grid-cols-4 items-center gap-3',
            )}>
              <MultiSelect
                value={selectedSiteIds}
                options={siteOptions}
                placeholder={t('split.filters.target')}
                searchPlaceholder={t('split.filters.searchSites')}
                emptyText={t('split.filters.emptySites')}
                selectedText={t('split.filters.selectedSites')}
                maxVisibleTags={1}
                className={isMobile ? 'col-span-2' : undefined}
                onChange={setSelectedSiteIds}
              />

              <DatePicker
                value={dateRange.from}
                onValueChange={setFromDate}
                placeholder={t('split.filters.dateFrom')}
                disabledDates={{ after: startLocalDay(new Date()) }}
                disabled={allTime}
                clearable={false}
                triggerClassName={cn('h-10', isMobile && 'px-3')}
              />
              <DatePicker
                value={dateRange.to}
                onValueChange={setToDate}
                placeholder={t('split.filters.dateTo')}
                disabledDates={{ after: startLocalDay(new Date()) }}
                disabled={allTime}
                clearable={false}
                triggerClassName={cn('h-10', isMobile && 'px-3')}
              />

              <div className={cn('grid min-w-0 grid-cols-4 gap-2', isMobile && 'col-span-2')}>
                {QUICK_RANGES.map((days) => (
                  <Button
                    key={days}
                    type="button"
                    size="sm"
                    variant={activeQuickRange === days ? 'default' : 'outline'}
                    onClick={() => applyQuickRange(days)}
                    className="w-full min-w-0 px-2"
                  >
                    {t('split.ranges.days', { count: days })}
                  </Button>
                ))}
                <Button
                  type="button"
                  size="sm"
                  variant={allTime ? 'default' : 'outline'}
                  onClick={applyAllTime}
                  className="w-full min-w-0 px-2"
                >
                  {t('split.ranges.allShort')}
                </Button>
              </div>
            </div>

            {queryInput && splitQuery.isError ? (
              <ErrorState title={t('split.loadFailed')} description={splitQuery.error.message} />
            ) : queryInput && splitQuery.isLoading ? (
              <div className="flex min-h-0 flex-1 items-center justify-center text-sm text-muted-soft">
                <LoaderCircle className="mr-2 h-4 w-4 animate-spin" />
                {t('split.loading')}
              </div>
            ) : (
              <>
                <div className={cn('grid shrink-0', isMobile ? 'grid-cols-2 gap-2' : 'grid-cols-6 gap-3')}>
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.totalCost')} value={formatSplitCost(displaySummary, t('split.cost.unconfigured'))} />
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.totalTokens')} value={formatUsageNumber(displaySummary.total_tokens, hasTokenUsage)} />
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.inputTokens')} value={formatUsageNumber(displaySummary.prompt_tokens, hasTokenUsage)} />
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.outputTokens')} value={formatUsageNumber(displaySummary.completion_tokens, hasTokenUsage)} />
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.cachedTokens')} value={formatUsageNumber(displaySummary.cached_tokens, hasTokenUsage)} />
                  <SplitMetricCard compact={isMobile} label={t('split.metrics.requests')} value={formatUsageNumber(displaySummary.request_count, hasUsage)} />
                </div>

                <div className={cn('grid shrink-0 text-xs text-muted-soft', isMobile ? 'grid-cols-2 gap-2' : 'grid-cols-4 gap-3')}>
                  <SplitSecondaryMetric label={t('split.metrics.successRate')} value={formatSplitRate(analyticsMetrics?.success_rate ?? rate(displaySummary.success_count, displaySummary.request_count), hasUsage)} />
                  <SplitSecondaryMetric label={t('split.metrics.failureRate')} value={formatSplitRate(rate(displaySummary.failure_count, displaySummary.request_count), hasUsage)} />
                  <SplitSecondaryMetric label={t('split.metrics.cacheHitRate')} value={formatSplitRate(analyticsMetrics?.cache_hit_rate ?? rate(displaySummary.cached_tokens, displaySummary.prompt_tokens), hasUsage)} />
                  <SplitSecondaryMetric label={t('split.metrics.avgLatency')} value={hasUsage && typeof analyticsMetrics?.avg_latency_ms === 'number' ? formatLatencyMs(analyticsMetrics.avg_latency_ms) : '—'} />
                </div>

                {!allTime && hasUsage && trendPoints.length > 0 ? (
                  <UsageTrend
                    points={trendPoints}
                    emptyLabel={isSingleDay ? t('split.hourlyEmpty') : t('split.dailyEmpty')}
                    title={isSingleDay ? t('split.hourlyTitle') : t('split.dailyTitle')}
                  />
                ) : null}

                {!hasUsage ? (
                  <EmptyState
                    className="min-h-0 flex-1"
                    title={t('split.empty.title')}
                    description={t('split.empty.description')}
                  />
                ) : null}

                {hasUsage ? <div className={cn(
                  'grid',
                  isMobile ? 'gap-3' : 'min-h-0 flex-1 grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-5',
                )}>
                  <div className={cn(
                    'flex flex-col rounded-lg border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]',
                    isMobile ? 'p-3' : 'min-h-0 p-4',
                  )}>
                    <div className={cn('flex shrink-0 flex-wrap items-center justify-between gap-3', isMobile ? 'mb-3' : 'mb-4')}>
                      <div className="min-w-0">
                        <h3 className="text-sm font-semibold text-foreground">
                          {dimension === 'model' ? t('split.chart.modelTitle') : t('split.chart.title')}
                        </h3>
                      </div>
                      <div className="flex flex-wrap items-center gap-2">
                        <Tabs value={dimension} onValueChange={(value) => setDimension(value as ChannelSplitDimension)}>
                          <TabsList className="h-8">
                            <TabsTrigger value="api_key" className="text-xs">{t('split.dimensions.apiKey')}</TabsTrigger>
                            <TabsTrigger value="model" className="text-xs">{t('split.dimensions.model')}</TabsTrigger>
                          </TabsList>
                        </Tabs>
                        <Tabs value={metric} onValueChange={(value) => setMetric(value as ChannelSplitMetric)}>
                          <TabsList className={isMobile ? 'h-8 w-[148px]' : 'h-8 w-[164px]'}>
                            <TabsTrigger value="cost" className="text-xs">{t('split.metrics.cost')}</TabsTrigger>
                            <TabsTrigger value="tokens" className="text-xs">{t('split.metrics.tokens')}</TabsTrigger>
                          </TabsList>
                        </Tabs>
                      </div>
                    </div>
                    <div className={cn('pr-1', !isMobile && 'min-h-0 flex-1 overflow-y-auto')}>
                      {dimension === 'model' && analyticsQuery.isError ? <p className="mb-3 text-xs text-muted-soft">{t('split.analyticsUnavailable')}</p> : null}
                      <SplitContributionBars items={rows} metric={chartMetric} currency={currency} unconfiguredLabel={t('split.cost.unconfigured')} emptyLabel={t('split.empty.breakdown')} multiCurrency={hasMultipleCurrencies} />
                    </div>
                  </div>

                  <div className={cn(
                    'flex min-w-0 flex-col rounded-lg border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]',
                    isMobile ? 'p-3' : 'min-h-0 p-4',
                  )}>
                    <div className={cn('flex shrink-0 flex-wrap items-center justify-between gap-2', isMobile ? 'mb-3' : 'mb-4')}>
                      <div>
                        <h3 className="text-sm font-semibold text-foreground">{dimension === 'model' ? t('split.table.modelTitle') : t('split.table.title')}</h3>
                      </div>
                    </div>
                    <div className={!isMobile ? 'min-h-0 flex-1' : undefined}>
                      <SplitTable isMobile={isMobile} items={rows} currency={currency} unconfiguredLabel={t('split.cost.unconfigured')} emptyLabel={t('split.empty.breakdown')} dimension={dimension} multiCurrency={hasMultipleCurrencies} />
                    </div>
                  </div>
                </div> : null}
              </>
            )}
          </div>
        </DialogBody>
      </DialogContent>
    </Dialog>
  )
}

function SplitMetricCard({ compact, label, value }: { compact: boolean; label: string; value: string }) {
  return (
    <div className={cn(
      'rounded-lg border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-panel))]',
      compact ? 'px-3 py-2.5' : 'px-4 py-3',
    )}>
      <div className="text-xs text-muted-soft">{label}</div>
      <div className={cn('mt-1 truncate font-semibold tabular-nums text-foreground', compact ? 'text-lg' : 'text-xl')} title={value}>
        {value}
      </div>
    </div>
  )
}

function SplitSecondaryMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-2 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-subtle))] px-3 py-2">
      <span>{label}</span>
      <span className="font-medium tabular-nums text-foreground">{value}</span>
    </div>
  )
}

function UsageTrend({
  points,
  title,
  emptyLabel,
}: {
  points: HourlyUsagePoint[]
  title: string
  emptyLabel: string
}) {
  const hasTokenData = points.some((point) => hasChannelSplitTokenUsage(point))
  if (!points.length || !hasTokenData) {
    return <div className="rounded-md border border-[hsl(var(--glass-border))] px-3 py-2 text-xs text-muted-soft">{emptyLabel}</div>
  }
  const maxTokens = Math.max(1, ...points.map((point) => point.total_tokens))
  return (
    <section className="shrink-0 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-subtle))] px-3 py-2.5" aria-label={title}>
      <div className="mb-2 flex items-center justify-between gap-2 text-xs text-muted-soft">
        <span>{title}</span>
        <span>{formatUsageNumber(points.reduce((sum, point) => sum + point.total_tokens, 0), hasTokenData)} tokens</span>
      </div>
      <div className="flex h-16 items-end gap-1" role="img" aria-label={points.map((point) => `${point.hour}: ${point.total_tokens} tokens`).join(', ')}>
        {points.map((point) => (
          <div key={point.hour} className="h-full min-w-0 flex-1" title={`${point.hour}: ${point.total_tokens} tokens`}>
            <div className="mx-auto w-full max-w-3 rounded-t bg-primary/80" style={{ height: `${Math.max(4, (point.total_tokens / maxTokens) * 100)}%` }} />
          </div>
        ))}
      </div>
    </section>
  )
}

function buildModelRows(usage: AnalyticsUsage | undefined): ChannelSplitModelRow[] {
  if (!usage) return []
  const totalTokens = usage.totals.total_tokens
  const totalCost = usage.totals.cost
  const currency = usage.meta.currency || 'USD'
  return usage.breakdowns.model.map((item) => ({
    model_key: item.key,
    model_name: item.label,
    request_count: item.requests,
    success_count: item.success_count,
    failure_count: item.failure_count,
    prompt_tokens: item.prompt_tokens,
    completion_tokens: item.completion_tokens,
    cached_tokens: item.cached_tokens,
    cache_write_tokens: 0,
    cache_creation_input_tokens: 0,
    cache_write_cost: 0,
    total_tokens: item.total_tokens,
    estimated_cost: item.cost,
    cost_by_currency: item.cost > 0 ? { [currency]: item.cost } : {},
    currency,
    token_share: totalTokens > 0 ? item.total_tokens / totalTokens : 0,
    cost_share: totalCost > 0 ? item.cost / totalCost : 0,
  }))
}

function buildUsageTrendPoints(usage: AnalyticsUsage | undefined): HourlyUsagePoint[] {
  if (!usage) return []
  const points = new Map<string, HourlyUsagePoint>()
  for (const series of usage.series) {
    for (const point of series.points) {
      const current = points.get(point.date) ?? {
        hour: point.date,
        request_count: 0,
        success_count: 0,
        failure_count: 0,
        prompt_tokens: 0,
        completion_tokens: 0,
        cached_tokens: 0,
        total_tokens: 0,
        estimated_cost: 0,
        currency: usage.meta.currency || 'USD',
      }
      addHourlyPoint(current, point)
      points.set(point.date, current)
    }
  }
  return [...points.values()].sort((left, right) => left.hour.localeCompare(right.hour))
}

function addHourlyPoint(target: HourlyUsagePoint, point: AnalyticsSeriesPoint) {
  target.request_count += point.requests
  target.success_count += point.success_count
  target.failure_count += point.failure_count
  target.prompt_tokens += point.prompt_tokens
  target.completion_tokens += point.completion_tokens
  target.cached_tokens += point.cached_tokens
  target.total_tokens += point.total_tokens
  target.estimated_cost += point.cost
}

function SplitContributionBars({
  currency,
  items,
  metric,
  unconfiguredLabel,
  emptyLabel,
  multiCurrency,
}: {
  currency: string
  items: ChannelSplitRow[]
  metric: ChannelSplitMetric
  unconfiguredLabel: string
  emptyLabel: string
  multiCurrency: boolean
}) {
  if (!items.length) {
    return <div className="flex min-h-20 items-center justify-center text-sm text-muted-soft">{emptyLabel}</div>
  }

  return (
    <div className="space-y-3">
      {items.map((item, index) => {
        const share = metric === 'cost' ? item.cost_share : item.token_share
        const shareLabel = metric === 'cost' && multiCurrency ? '—' : formatPercent(share, 1)
        const value = metric === 'cost'
          ? splitRowCost(item, currency, unconfiguredLabel)
          : formatUsageNumber(item.total_tokens, hasChannelSplitTokenUsage(item))

        return (
          <div key={splitRowKey(item)} className="space-y-1.5">
            <div className="flex items-center justify-between gap-3 text-sm">
              <div className="flex min-w-0 items-center gap-2">
                <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-md bg-[hsl(var(--surface-subtle))] text-[11px] text-muted-soft">
                  {index + 1}
                </span>
                <span className="truncate font-medium text-foreground" title={splitRowLabel(item)}>
                  {splitRowLabel(item)}
                </span>
              </div>
              <div className="shrink-0 text-right tabular-nums">
                <span className="text-foreground">{value}</span>
                <span className="ml-2 text-muted-soft">{shareLabel}</span>
              </div>
            </div>
            <Progress value={share * 100} max={100} variant={index === 0 ? 'accent' : 'info'} className="h-2" />
          </div>
        )
      })}
    </div>
  )
}

function SplitTable({
  currency,
  isMobile,
  items,
  unconfiguredLabel,
  emptyLabel,
  dimension,
  multiCurrency,
}: {
  currency: string
  isMobile: boolean
  items: ChannelSplitRow[]
  unconfiguredLabel: string
  emptyLabel: string
  dimension: ChannelSplitDimension
  multiCurrency: boolean
}) {
  const { t } = useTranslation('oauth')

  if (!items.length) {
    return <div className="flex min-h-20 items-center justify-center text-sm text-muted-soft">{emptyLabel}</div>
  }

  if (isMobile) {
    return (
      <div className="divide-y divide-[hsl(var(--glass-divider))]">
        {items.map((item) => {
          const name = splitRowLabel(item)

          return (
            <div key={splitRowKey(item)} className="py-3 first:pt-0 last:pb-0">
              <div className="flex min-w-0 items-center justify-between gap-3">
                <div className="truncate text-sm font-semibold text-foreground" title={name}>
                  {name}
                </div>
              </div>
              <div className="mt-2 grid grid-cols-2 gap-x-4 gap-y-2">
                <SplitMobileValue label={t('split.table.cost')} value={splitRowCost(item, currency, unconfiguredLabel)} />
                <SplitMobileValue label={t('split.table.share')} value={multiCurrency ? '—' : formatPercent(item.cost_share, 1)} />
                <SplitMobileValue label={t('split.table.tokens')} value={formatUsageNumber(item.total_tokens, hasChannelSplitTokenUsage(item))} />
                <SplitMobileValue label={t('split.table.requests')} value={formatUsageNumber(item.request_count, hasChannelSplitUsage(item))} />
                <SplitMobileValue label={t('split.table.successRate')} value={formatSplitRate(rate(item.success_count, item.request_count), hasChannelSplitUsage(item))} />
              </div>
            </div>
          )
        })}
      </div>
    )
  }

  return (
    <div className="h-full min-h-0 overflow-y-auto overflow-x-hidden rounded-lg">
      <table className="w-full table-fixed border-collapse text-left">
        <thead className="sticky top-0 z-10 bg-[hsl(var(--surface-subtle))] shadow-[0_1px_0_hsl(var(--glass-divider))]">
          <tr className="text-faint text-xs uppercase tracking-[0.16em]">
            <th className="w-[36%] overflow-hidden text-ellipsis whitespace-nowrap px-3 py-3 font-medium">{dimension === 'model' ? t('split.table.model') : t('split.table.apiKey')}</th>
            <th className="w-[16%] overflow-hidden text-ellipsis whitespace-nowrap px-3 py-3 text-right font-medium">{t('split.table.cost')}</th>
            <th className="w-[12%] overflow-hidden text-ellipsis whitespace-nowrap px-3 py-3 text-right font-medium">{t('split.table.share')}</th>
            <th className="w-[18%] overflow-hidden text-ellipsis whitespace-nowrap px-3 py-3 text-right font-medium">{t('split.table.tokens')}</th>
            <th className="w-[18%] overflow-hidden text-ellipsis whitespace-nowrap px-3 py-3 text-right font-medium">{t('split.table.requests')}</th>
          </tr>
        </thead>
        <tbody>
          {items.map((item) => {
            const name = splitRowLabel(item)

            return (
              <tr
                key={splitRowKey(item)}
                className="border-t border-[hsl(var(--glass-divider))] [&>td]:transition-colors hover:[&>td]:bg-[hsl(var(--surface-subtle))] hover:[&>td:first-child]:rounded-l-md hover:[&>td:last-child]:rounded-r-md"
              >
                <td className="overflow-hidden px-3 py-2.5 align-middle">
                  <div className="truncate text-sm font-medium text-foreground" title={name}>
                    {name}
                  </div>
                </td>
                <td className="overflow-hidden text-ellipsis whitespace-nowrap px-3 py-2.5 text-right align-middle tabular-nums">
                  {splitRowCost(item, currency, unconfiguredLabel)}
                </td>
                <td className="overflow-hidden text-ellipsis whitespace-nowrap px-3 py-2.5 text-right align-middle tabular-nums">
                  {multiCurrency ? '—' : formatPercent(item.cost_share, 1)}
                </td>
                <td className="overflow-hidden text-ellipsis whitespace-nowrap px-3 py-2.5 text-right align-middle tabular-nums">
                  {formatUsageNumber(item.total_tokens, hasChannelSplitTokenUsage(item))}
                </td>
                <td className="overflow-hidden text-ellipsis whitespace-nowrap px-3 py-2.5 text-right align-middle tabular-nums">
                  {formatUsageNumber(item.request_count, hasChannelSplitUsage(item))}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

function SplitMobileValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <div className="text-[11px] text-muted-soft">{label}</div>
      <div className="mt-0.5 truncate text-sm font-medium tabular-nums text-foreground" title={value}>
        {value}
      </div>
    </div>
  )
}

function splitRowKey(item: ChannelSplitRow) {
  return 'model_key' in item ? `model:${item.model_key}` : `api-key:${item.api_key_id}`
}

function splitRowLabel(item: ChannelSplitRow) {
  if ('model_key' in item) return item.model_name || item.model_key || '-'
  return item.api_key_name || item.masked_key || item.api_key_id || '-'
}

function splitRowCost(item: ChannelSplitRow, currency: string, unconfiguredLabel: string) {
  return formatSplitCost(item, unconfiguredLabel, currency)
}

function hasChannelSplitUsage(item: { request_count: number; total_tokens: number; prompt_tokens?: number; completion_tokens?: number; cached_tokens?: number }) {
  return item.request_count > 0 || item.total_tokens > 0 || (item.prompt_tokens ?? 0) > 0 || (item.completion_tokens ?? 0) > 0 || (item.cached_tokens ?? 0) > 0
}

function hasChannelSplitTokenUsage(item: { total_tokens: number; prompt_tokens?: number; completion_tokens?: number; cached_tokens?: number }) {
  return item.total_tokens > 0 || (item.prompt_tokens ?? 0) > 0 || (item.completion_tokens ?? 0) > 0 || (item.cached_tokens ?? 0) > 0
}

function hasCostData(item: { estimated_cost: number; cost_by_currency?: Record<string, number>; request_count?: number; total_tokens?: number }) {
  const values = Object.values(item.cost_by_currency ?? {})
  return values.some((value) => Number.isFinite(value) && value > 0) || (values.length === 0 && Number.isFinite(item.estimated_cost) && item.estimated_cost > 0)
}

function formatSplitCost(item: { estimated_cost: number; currency: string; cost_by_currency?: Record<string, number>; request_count?: number; total_tokens?: number }, unconfiguredLabel: string, fallbackCurrency = 'USD') {
  if (!hasChannelSplitUsage({ request_count: item.request_count ?? 0, total_tokens: item.total_tokens ?? 0 })) return '—'
  const entries = Object.entries(item.cost_by_currency ?? {})
    .filter(([, value]) => Number.isFinite(value))
    .sort(([left], [right]) => left.localeCompare(right))
  if (!entries.length) {
    if (!Number.isFinite(item.estimated_cost) || item.estimated_cost <= 0) return unconfiguredLabel
    return formatDashboardCurrency(item.estimated_cost, item.currency || fallbackCurrency, 2)
  }
  if (entries.every(([, value]) => value <= 0)) return unconfiguredLabel
  return entries
    .filter(([, value]) => value > 0)
    .map(([code, value]) => formatDashboardCurrency(value, code, 2))
    .join(' · ')
}

function formatUsageNumber(value: number | undefined, available: boolean) {
  if (!available || typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return formatCompactNumber(value)
}

function formatSplitRate(value: number | null | undefined, available: boolean) {
  if (!available || typeof value !== 'number' || !Number.isFinite(value)) return '—'
  return formatPercent(value, 1)
}

function rate(value: number, total: number) {
  return total > 0 ? value / total : null
}

function emptyChannelSplitSummary(allTime: boolean, dateFrom: string, dateTo: string): ChannelSplitSummary {
  return {
    site_id: '',
    site_ids: [],
    site_name: '',
    site_names: [],
    site_slug: '',
    site_type: '',
    target_label: '',
    oauth_connection_id: null,
    oauth_provider: null,
    oauth_account: null,
    range_all: allTime,
    date_from: allTime ? '' : dateFrom,
    date_to: allTime ? '' : dateTo,
    range_start: '',
    range_end: '',
    timezone: '',
    generated_at: '',
    request_count: 0,
    success_count: 0,
    failure_count: 0,
    prompt_tokens: 0,
    completion_tokens: 0,
    cached_tokens: 0,
    cache_write_tokens: 0,
    cache_creation_input_tokens: 0,
    cache_write_cost: 0,
    total_tokens: 0,
    estimated_cost: 0,
    cost_by_currency: {},
    currency: 'USD',
    api_key_count: 0,
  }
}

function buildSiteOptions(sites: Site[], labels: { deleted: string; oauth: string; sites: string }): MultiSelectOption[] {
  const activeOAuth = sortChannelSplitSites(sites.filter((site) => site.status !== 'deleted' && isOAuthSite(site)))
  const activeSites = sortChannelSplitSites(sites.filter((site) => site.status !== 'deleted' && !isOAuthSite(site)))
  const deletedOAuth = sortChannelSplitSites(sites.filter((site) => site.status === 'deleted' && isOAuthSite(site)))
  const deletedSites = sortChannelSplitSites(sites.filter((site) => site.status === 'deleted' && !isOAuthSite(site)))

  return [
    ...activeOAuth.map((site) => channelSplitSiteOption(site, labels, labels.oauth)),
    ...activeSites.map((site) => channelSplitSiteOption(site, labels, labels.sites)),
    ...deletedOAuth.map((site) => channelSplitSiteOption(site, labels, labels.deleted)),
    ...deletedSites.map((site) => channelSplitSiteOption(site, labels, labels.deleted)),
  ]
}

function sortChannelSplitSites(sites: Site[]) {
  return [...sites].sort((left, right) => channelSplitSiteSortLabel(left).localeCompare(channelSplitSiteSortLabel(right), 'zh-CN', { numeric: true, sensitivity: 'base' }))
}

function channelSplitSiteOption(site: Site, labels: { deleted: string }, group?: string): MultiSelectOption {
  const deleted = site.status === 'deleted'
  const name = channelSplitSiteDisplayName(site)
  return {
    value: site.id,
    label: deleted ? `${name} (${labels.deleted})` : name,
    description: [site.site_type, channelSplitSiteAccount(site), site.slug].filter(Boolean).join(' · '),
    icon: channelSplitSiteIcon(site),
    group,
  }
}

function channelSplitSiteIcon(site: Site) {
  return siteTypeIcon(site.site_type) || site.icon_url
}

function channelSplitSiteSortLabel(site: Site) {
  return `${channelSplitSiteDisplayName(site)} ${site.slug}`.trim()
}

function channelSplitSiteDisplayName(site: Site) {
  return site.name || channelSplitSiteAccount(site) || site.slug || site.id
}

function channelSplitSiteAccount(site: Site) {
  return site.oauth_account?.email || site.oauth_account?.account_id || ''
}

function initialChannelSplitSelection(target: ChannelSplitTarget | null | undefined, oauthConnections: OAuthConnectionListItem[]) {
  if (!target) return { siteIds: [] as string[], oauthConnectionId: undefined as string | undefined }
  if (target.type === 'site') {
    return { siteIds: [target.id], oauthConnectionId: undefined }
  }
  const connection = oauthConnections.find((item) => item.id === target.id)
  const siteId = target.siteIds?.[0] ?? connection?.site?.id ?? connection?.site_id ?? ''
  return {
    siteIds: siteId ? [siteId] : [],
    oauthConnectionId: target.id,
  }
}

function splitTargetTitle(summary: ChannelSplitSummary) {
  const target = summary.target_label || summary.site_name || summary.site_names?.[0] || '-'
  if (summary.oauth_account) return `${target} · ${summary.oauth_account}`
  return target
}

function naturalDayRange(days: number) {
  const to = startLocalDay(new Date())
  const from = addLocalDays(to, -days + 1)
  return { from, to }
}

function startLocalDay(date: Date) {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate())
}

function addLocalDays(date: Date, days: number) {
  const next = new Date(date)
  next.setDate(next.getDate() + days)
  return startLocalDay(next)
}

function formatDateParam(date: Date | undefined) {
  if (!date) return ''
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

function sameDayRange(left: { from: Date; to: Date }, right: { from: Date; to: Date }) {
  return sameDay(left.from, right.from) && sameDay(left.to, right.to)
}

function sameDay(left: Date, right: Date) {
  return left.getFullYear() === right.getFullYear() &&
    left.getMonth() === right.getMonth() &&
    left.getDate() === right.getDate()
}
