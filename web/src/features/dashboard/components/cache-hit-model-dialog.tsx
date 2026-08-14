import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ErrorState } from '@/components/common/error-state'
import { DatePicker } from '@/components/ui/date-picker'
import { Dialog, DialogBody, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import {
  dashboardQueryKeys,
  getDashboardCacheHit,
  type CacheHitInput,
} from '@/features/dashboard/api/dashboard'
import { CacheHitTrendChart } from '@/features/dashboard/components/cache-hit-trend-chart'
import {
  buildCacheHitTrend,
  formatCacheHitDate,
  naturalCacheHitDateRange,
  startCacheHitDay,
  type CacheHitDateRange,
} from '@/features/dashboard/lib/cache-hit-utils'
import { formatCompactNumber, formatPercent } from '@/features/dashboard/lib/dashboard-utils'
import type { Site, SiteModel } from '@/features/sites/api/sites'

type CacheHitModelDialogProps = {
  site: Site | null
  model: SiteModel | null
  onOpenChange: (open: boolean) => void
}

export function CacheHitModelDialog({ site, model, onOpenChange }: CacheHitModelDialogProps) {
  const open = Boolean(site && model)
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {site && model ? <CacheHitModelDialogBody key={`${site.id}-${model.id}`} site={site} model={model} /> : null}
    </Dialog>
  )
}

function CacheHitModelDialogBody({ site, model }: { site: Site; model: SiteModel }) {
  const { t } = useTranslation('dashboard')
  const [dateRange, setDateRange] = useState<CacheHitDateRange>(() => naturalCacheHitDateRange(30))
  const input: CacheHitInput = {
    dateFrom: formatCacheHitDate(dateRange.from),
    dateTo: formatCacheHitDate(dateRange.to),
    siteIds: [site.id],
    siteModelIds: [model.id],
  }
  const query = useQuery({
    queryKey: dashboardQueryKeys.cacheHit(input),
    queryFn: () => getDashboardCacheHit(input),
    placeholderData: keepPreviousData,
  })
  const trend = buildCacheHitTrend(query.data?.daily ?? [], 'model')

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

  return (
    <DialogContent className="grid max-h-[86dvh] w-[min(94vw,900px)] grid-rows-[auto_minmax(0,1fr)] overflow-hidden rounded-xl">
      <DialogHeader className="px-5 py-4">
        <DialogTitle>{t('cacheHit.modelDialog.title')}</DialogTitle>
        <DialogDescription>{t('cacheHit.modelDialog.description', { site: site.name, model: model.display_name || model.upstream_model_name })}</DialogDescription>
      </DialogHeader>
      <DialogBody className="min-h-0 space-y-5 overflow-y-auto px-5 py-5">
        <div className="grid gap-3 sm:grid-cols-2">
          <DatePicker value={dateRange.from} onValueChange={setFromDate} placeholder={t('cacheHit.filters.dateFrom')} disableFutureDates clearable={false} />
          <DatePicker value={dateRange.to} onValueChange={setToDate} placeholder={t('cacheHit.filters.dateTo')} disableFutureDates clearable={false} />
        </div>
        {query.isError ? (
          <ErrorState title={t('cacheHit.loadFailed')} description={query.error.message} />
        ) : query.isLoading && !query.data ? (
          <div className="flex h-64 items-center justify-center text-sm text-muted-soft">
            <LoaderCircle className="mr-2 h-4 w-4 animate-spin" />
            {t('cacheHit.loading')}
          </div>
        ) : query.data ? (
          <>
            <div className="grid border-y border-[hsl(var(--glass-divider))] sm:grid-cols-3">
              <DialogMetric label={t('cacheHit.metrics.hitRate')} value={formatPercent(query.data.summary.hit_rate)} />
              <DialogMetric label={t('cacheHit.metrics.cachedTokens')} value={formatCompactNumber(query.data.summary.cached_tokens)} bordered />
              <DialogMetric label={t('cacheHit.metrics.inputTokens')} value={formatCompactNumber(query.data.summary.prompt_tokens)} bordered />
            </div>
            {query.data.daily.length ? <CacheHitTrendChart data={trend.data} series={trend.series} /> : <div className="py-12 text-center text-sm text-muted-soft">{t('cacheHit.empty')}</div>}
          </>
        ) : null}
      </DialogBody>
    </DialogContent>
  )
}

function DialogMetric({ label, value, bordered = false }: { label: string; value: string; bordered?: boolean }) {
  return (
    <div className={`min-w-0 px-4 py-3 first:pl-0 sm:first:pl-4 ${bordered ? 'sm:border-l sm:border-[hsl(var(--glass-divider))]' : ''}`}>
      <p className="truncate text-xs text-muted-soft">{label}</p>
      <p className="mt-1 truncate text-lg font-semibold tabular-nums text-foreground">{value}</p>
    </div>
  )
}
