import { useMemo, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { ArrowDownUp, ChevronDown, ChevronUp, LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/common/error-state'
import { Button } from '@/components/ui/button'
import { DatePicker } from '@/components/ui/date-picker'
import { MultiSelect, type MultiSelectOption } from '@/components/ui/multi-select'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import {
  dashboardQueryKeys,
  getDashboardCacheHit,
  type CacheHitInput,
  type DashboardCacheHit,
} from '@/features/dashboard/api/dashboard'
import {
  buildCacheHitTrend,
  formatCacheHitDate,
  naturalCacheHitDateRange,
  sortCacheHitBreakdown,
  startCacheHitDay,
  type CacheHitDateRange,
  type CacheHitBreakdownSortDirection,
  type CacheHitBreakdownSortKey,
  type CacheHitGroupBy,
} from '@/features/dashboard/lib/cache-hit-utils'
import { formatCompactNumber, formatPercent } from '@/features/dashboard/lib/dashboard-utils'
import { DashboardChartPanel } from './dashboard-chart-panel'
import { CacheHitTrendChart } from './cache-hit-trend-chart'

const CACHE_HIT_RANGE_OPTIONS = [7, 30, 90] as const

type CacheHitAnalyticsPanelProps = {
  compact?: boolean
}

export function CacheHitAnalyticsPanel({ compact = false }: CacheHitAnalyticsPanelProps) {
  const { t } = useTranslation('dashboard')
  const [dateRange, setDateRange] = useState<CacheHitDateRange>(() => naturalCacheHitDateRange(30))
  const [siteIds, setSiteIds] = useState<string[]>([])
  const [selectedModelKeys, setSelectedModelKeys] = useState<string[]>([])
  const [groupBy, setGroupBy] = useState<CacheHitGroupBy>('site')
  const filterOptionsInput = useMemo<CacheHitInput>(() => ({
    dateFrom: formatCacheHitDate(dateRange.from),
    dateTo: formatCacheHitDate(dateRange.to),
  }), [dateRange])
  const filterOptionsQuery = useQuery({
    queryKey: dashboardQueryKeys.cacheHit(filterOptionsInput),
    queryFn: () => getDashboardCacheHit(filterOptionsInput),
    placeholderData: keepPreviousData,
  })
  const siteOptions = useMemo(() => cacheHitSiteOptions(filterOptionsQuery.data), [filterOptionsQuery.data])
  const modelOptions = useMemo(() => cacheHitModelOptions(filterOptionsQuery.data, siteIds), [filterOptionsQuery.data, siteIds])
  const activeModelKeys = useMemo(
    () => selectedModelKeys.filter((modelKey) => modelOptions.siteModelIDsByValue.has(modelKey)),
    [modelOptions.siteModelIDsByValue, selectedModelKeys],
  )
  const input = useMemo<CacheHitInput>(() => ({
    ...filterOptionsInput,
    siteIds,
    siteModelIds: activeModelKeys.flatMap((modelKey) => modelOptions.siteModelIDsByValue.get(modelKey) ?? []),
  }), [activeModelKeys, filterOptionsInput, modelOptions.siteModelIDsByValue, siteIds])
  const query = useQuery({
    queryKey: dashboardQueryKeys.cacheHit(input),
    queryFn: () => getDashboardCacheHit(input),
    placeholderData: keepPreviousData,
  })
  const overview = query.data
  const trend = useMemo(
    () => buildCacheHitTrend(overview?.daily ?? [], groupBy),
    [groupBy, overview?.daily],
  )
  const activeRange = CACHE_HIT_RANGE_OPTIONS.find((days) => sameCacheHitRange(dateRange, naturalCacheHitDateRange(days)))

  function setFromDate(value: Date | undefined) {
    if (!value) return
    const from = startCacheHitDay(value)
    setDateRange((current) => ({ from, to: current.to < from ? from : current.to }))
  }

  function setToDate(value: Date | undefined) {
    if (!value) return
    const to = startCacheHitDay(value)
    setDateRange((current) => ({ from: current.from > to ? to : current.from, to }))
  }

  function setQuickRange(days: number) {
    setDateRange(naturalCacheHitDateRange(days))
  }

  function handleSiteChange(nextSiteIDs: string[]) {
    setSiteIds(nextSiteIDs)
    setSelectedModelKeys([])
  }

  return (
    <DashboardChartPanel
      title={t('cacheHit.title')}
      description={t('cacheHit.description')}
      className={compact ? 'rounded-xl p-4' : undefined}
    >
      <div className="space-y-5">
        <div className="grid gap-3 lg:grid-cols-[minmax(0,1fr)_minmax(0,1fr)_minmax(0,1.25fr)_auto]">
          <MultiSelect
            value={siteIds}
            options={siteOptions}
            placeholder={t('cacheHit.filters.site')}
            searchPlaceholder={t('cacheHit.filters.searchSite')}
            emptyText={t('cacheHit.filters.emptySite')}
            selectedText={t('cacheHit.filters.selected')}
            clearText={t('cacheHit.filters.clear')}
            maxVisibleTags={1}
            onChange={handleSiteChange}
          />
          <MultiSelect
            value={activeModelKeys}
            options={modelOptions.options}
            placeholder={t('cacheHit.filters.model')}
            searchPlaceholder={t('cacheHit.filters.searchModel')}
            emptyText={t('cacheHit.filters.emptyModel')}
            selectedText={t('cacheHit.filters.selected')}
            clearText={t('cacheHit.filters.clear')}
            maxVisibleTags={1}
            onChange={setSelectedModelKeys}
          />
          <div className="grid grid-cols-2 gap-3">
            <DatePicker
              value={dateRange.from}
              onValueChange={setFromDate}
              placeholder={t('cacheHit.filters.dateFrom')}
              disableFutureDates
              clearable={false}
              triggerClassName="h-11"
            />
            <DatePicker
              value={dateRange.to}
              onValueChange={setToDate}
              placeholder={t('cacheHit.filters.dateTo')}
              disableFutureDates
              clearable={false}
              triggerClassName="h-11"
            />
          </div>
          <Tabs value={activeRange ? `${activeRange}d` : ''} onValueChange={(value) => setQuickRange(Number(value.replace('d', '')))}>
            <TabsList className="h-11 w-full lg:w-[174px]">
              {CACHE_HIT_RANGE_OPTIONS.map((days) => (
                <TabsTrigger key={days} value={`${days}d`} className="text-xs">
                  {t('cacheHit.ranges.days', { count: days })}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
        </div>

        {query.isError ? (
          <ErrorState title={t('cacheHit.loadFailed')} description={query.error.message} action={<Button variant="outline" onClick={() => query.refetch()}>{t('page.retry')}</Button>} />
        ) : query.isLoading && !overview ? (
          <div className="flex h-64 items-center justify-center text-sm text-muted-soft">
            <LoaderCircle className="mr-2 h-4 w-4 animate-spin" />
            {t('cacheHit.loading')}
          </div>
        ) : overview ? (
          <CacheHitAnalysis overview={overview} trend={trend} groupBy={groupBy} compact={compact} onGroupByChange={setGroupBy} />
        ) : null}
      </div>
    </DashboardChartPanel>
  )
}

function CacheHitAnalysis({
  overview,
  trend,
  groupBy,
  compact,
  onGroupByChange,
}: {
  overview: DashboardCacheHit
  trend: ReturnType<typeof buildCacheHitTrend>
  groupBy: CacheHitGroupBy
  compact: boolean
  onGroupByChange: (value: CacheHitGroupBy) => void
}) {
  const { t } = useTranslation('dashboard')
  const hasData = overview.breakdown.length > 0

  return (
    <>
      <div className="grid grid-cols-3 border-y border-[hsl(var(--glass-divider))]">
        <CacheHitMetric label={t('cacheHit.metrics.hitRate')} value={formatPercent(overview.summary.hit_rate)} />
        <CacheHitMetric label={t('cacheHit.metrics.cachedTokens')} value={formatCompactNumber(overview.summary.cached_tokens)} bordered />
        <CacheHitMetric label={t('cacheHit.metrics.inputTokens')} value={formatCompactNumber(overview.summary.prompt_tokens)} bordered />
      </div>

      {hasData ? (
        <div className={compact ? 'space-y-5' : 'grid gap-6 xl:grid-cols-[minmax(0,1.3fr)_minmax(360px,0.9fr)]'}>
          <div className="min-w-0">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-3">
              <h4 className="text-sm font-semibold text-foreground">{t('cacheHit.trend.title')}</h4>
              <Tabs value={groupBy} onValueChange={(value) => onGroupByChange(value as CacheHitGroupBy)}>
                <TabsList className="h-8 w-[142px]">
                  <TabsTrigger value="site" className="text-xs">{t('cacheHit.groupBy.site')}</TabsTrigger>
                  <TabsTrigger value="model" className="text-xs">{t('cacheHit.groupBy.model')}</TabsTrigger>
                </TabsList>
              </Tabs>
            </div>
            <CacheHitTrendChart data={trend.data} series={trend.series} height={compact ? 220 : 260} />
          </div>
          <CacheHitBreakdownTable items={overview.breakdown} compact={compact} />
        </div>
      ) : (
        <div className="py-12 text-center text-sm text-muted-soft">{t('cacheHit.empty')}</div>
      )}
    </>
  )
}

function CacheHitMetric({ label, value, bordered = false }: { label: string; value: string; bordered?: boolean }) {
  return (
    <div className={`min-w-0 px-2 py-3 sm:px-4 first:pl-0 sm:first:pl-4 ${bordered ? 'border-l border-[hsl(var(--glass-divider))]' : ''}`}>
      <p className="truncate text-xs text-muted-soft">{label}</p>
      <p className="mt-1 truncate text-lg font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  )
}

function CacheHitBreakdownTable({ items, compact }: { items: DashboardCacheHit['breakdown']; compact: boolean }) {
  const { t } = useTranslation('dashboard')
  const [sortKey, setSortKey] = useState<CacheHitBreakdownSortKey>('cached')
  const [sortDirection, setSortDirection] = useState<CacheHitBreakdownSortDirection>('desc')
  const sortedItems = useMemo(
    () => sortCacheHitBreakdown(items, sortKey, sortDirection),
    [items, sortDirection, sortKey],
  )

  function handleSort(nextSortKey: CacheHitBreakdownSortKey) {
    if (nextSortKey === sortKey) {
      setSortDirection((current) => current === 'asc' ? 'desc' : 'asc')
      return
    }
    setSortKey(nextSortKey)
    setSortDirection(nextSortKey === 'site' || nextSortKey === 'model' ? 'asc' : 'desc')
  }

  const sortLabels: Record<CacheHitBreakdownSortKey, string> = {
    site: t('cacheHit.breakdown.site'),
    model: t('cacheHit.breakdown.model'),
    hitRate: t('cacheHit.breakdown.hitRate'),
    requests: t('cacheHit.breakdown.requests'),
    input: t('cacheHit.breakdown.input'),
    cached: t('cacheHit.breakdown.cached'),
  }

  return (
    <div className="min-w-0 overflow-hidden rounded-lg border border-[hsl(var(--glass-border))]">
      <div className="border-b border-[hsl(var(--glass-divider))] px-4 py-3">
        <h4 className="text-sm font-semibold text-foreground">{t('cacheHit.breakdown.title')}</h4>
      </div>
      <div className={compact ? 'max-h-72 overflow-auto' : 'max-h-[260px] overflow-auto'}>
        <table className="w-full min-w-[600px] table-fixed text-left text-sm">
          <thead className="sticky top-0 bg-[hsl(var(--surface-subtle))] text-[11px] uppercase tracking-[0.12em] text-faint">
            <tr>
              <CacheHitSortHeader sortKey="site" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.site} onSort={handleSort} className="w-[24%] text-left" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.site })} />
              <CacheHitSortHeader sortKey="model" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.model} onSort={handleSort} className="w-[27%] text-left" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.model })} />
              <CacheHitSortHeader sortKey="hitRate" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.hitRate} onSort={handleSort} className="w-[14%] text-right" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.hitRate })} />
              <CacheHitSortHeader sortKey="requests" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.requests} onSort={handleSort} className="w-[12%] text-right" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.requests })} />
              <CacheHitSortHeader sortKey="input" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.input} onSort={handleSort} className="w-[13%] text-right" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.input })} />
              <CacheHitSortHeader sortKey="cached" sortKeyValue={sortKey} sortDirection={sortDirection} label={sortLabels.cached} onSort={handleSort} className="w-[10%] text-right" sortLabel={t('cacheHit.breakdown.sortBy', { field: sortLabels.cached })} />
            </tr>
          </thead>
          <tbody>
            {sortedItems.map((item) => (
              <tr key={`${item.site_id ?? item.site_name}-${item.site_model_id ?? item.model_key}`} className="border-t border-[hsl(var(--glass-divider))]">
                <td className="truncate px-4 py-2.5 font-medium text-foreground" title={item.site_name}>{item.site_name || item.site_slug || '-'}</td>
                <td className="px-4 py-2.5">
                  <p className="truncate text-foreground" title={item.model_key}>{item.model_key || item.upstream_model_name || '-'}</p>
                  {item.upstream_model_name && item.upstream_model_name !== item.model_key ? <p className="truncate text-xs text-muted-soft" title={item.upstream_model_name}>{item.upstream_model_name}</p> : null}
                </td>
                <td className="px-4 py-2.5 text-right font-semibold tabular-nums text-foreground">{formatPercent(item.hit_rate)}</td>
                <td className="px-4 py-2.5 text-right tabular-nums text-muted-soft">{formatCompactNumber(item.request_count)}</td>
                <td className="px-4 py-2.5 text-right tabular-nums text-muted-soft">{formatCompactNumber(item.prompt_tokens)}</td>
                <td className="px-4 py-2.5 text-right tabular-nums text-muted-soft">{formatCompactNumber(item.cached_tokens)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function CacheHitSortHeader({
  sortKey,
  sortKeyValue,
  sortDirection,
  label,
  sortLabel,
  className,
  onSort,
}: {
  sortKey: CacheHitBreakdownSortKey
  sortKeyValue: CacheHitBreakdownSortKey
  sortDirection: CacheHitBreakdownSortDirection
  label: string
  sortLabel: string
  className: string
  onSort: (sortKey: CacheHitBreakdownSortKey) => void
}) {
  const active = sortKey === sortKeyValue
  const SortIcon = active ? (sortDirection === 'asc' ? ChevronUp : ChevronDown) : ArrowDownUp
  return (
    <th aria-sort={active ? sortDirection === 'asc' ? 'ascending' : 'descending' : 'none'} className={`${className} px-4 py-2.5 font-medium`}>
      <button
        type="button"
        className="inline-flex w-full items-center gap-1.5 text-inherit hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[hsl(var(--ring-soft))]"
        onClick={() => onSort(sortKey)}
        aria-label={sortLabel}
      >
        <span className="truncate">{label}</span>
        <SortIcon className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
      </button>
    </th>
  )
}

function cacheHitSiteOptions(overview?: DashboardCacheHit): MultiSelectOption[] {
  const options = new Map<string, MultiSelectOption>()
  for (const item of overview?.breakdown ?? []) {
    if (!item.site_id || options.has(item.site_id)) continue
    options.set(item.site_id, {
      value: item.site_id,
      label: item.site_name || item.site_slug || item.site_id,
      description: item.site_type || undefined,
    })
  }
  return [...options.values()].sort((left, right) => left.label.localeCompare(right.label))
}

function cacheHitModelOptions(overview: DashboardCacheHit | undefined, selectedSiteIDs: string[]) {
  const selectedSites = new Set(selectedSiteIDs)
  const options = new Map<string, { option: MultiSelectOption; siteModelIDs: string[] }>()
  for (const item of overview?.breakdown ?? []) {
    const modelKey = item.model_key || item.upstream_model_name || 'unknown'
    if (!item.site_model_id) continue
    if (selectedSites.size > 0 && (!item.site_id || !selectedSites.has(item.site_id))) continue
    const option = options.get(modelKey)
    if (option) {
      if (!option.siteModelIDs.includes(item.site_model_id)) option.siteModelIDs.push(item.site_model_id)
      continue
    }
    options.set(modelKey, {
      option: { value: modelKey, label: modelKey },
      siteModelIDs: [item.site_model_id],
    })
  }
  const sortedOptions = [...options.values()]
    .sort((left, right) => left.option.label.localeCompare(right.option.label))
    .map(({ option }) => option)
  return {
    options: sortedOptions,
    siteModelIDsByValue: new Map([...options].map(([modelKey, option]) => [modelKey, option.siteModelIDs])),
  }
}

function sameCacheHitRange(left: CacheHitDateRange, right: CacheHitDateRange) {
  return formatCacheHitDate(left.from) === formatCacheHitDate(right.from) && formatCacheHitDate(left.to) === formatCacheHitDate(right.to)
}
