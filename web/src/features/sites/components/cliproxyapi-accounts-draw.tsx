import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { ChevronDown, LoaderCircle, RefreshCw } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import {
  Draw,
  DrawBody,
  DrawContent,
  DrawHeader,
  DrawTitle,
} from '@/components/ui/draw'
import { toast } from '@/lib/toast'
import {
  syncCLIProxyAPISite,
  sitesQueryKeys,
  type CLIProxyAPIAccount,
  type CLIProxyAPIQuotaObservation,
  type CLIProxyAPISyncSnapshot,
  type Site,
  type SiteCLIProxyAPIConfig,
} from '@/features/sites/api/sites'
import {
  buildQuotaWindows,
  buildRecentRequestSeries,
  formatQuotaSignal,
  maskCLIProxyAPIEmail,
  type CLIProxyAPIQuotaTranslator,
  type CLIProxyAPIQuotaWindow,
  type CLIProxyAPIRecentRequestPoint,
} from '@/features/sites/lib/cliproxyapi-utils'
import { formatDateTime } from '@/features/sites/lib/site-utils'

const STALE_AFTER_MS = 15 * 60 * 1000

export function CLIProxyAPIAccountsDraw({
  site,
  onOpenChange,
}: {
  site: Site | null
  onOpenChange: (open: boolean) => void
}) {
  const { t, i18n } = useTranslation('sites')
  const queryClient = useQueryClient()
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const [now, setNow] = useState(() => Date.now())
  const open = Boolean(site)

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 60_000)
    return () => window.clearInterval(timer)
  }, [])

  const syncMutation = useMutation({
    mutationFn: () => {
      if (!site) throw new Error('site required')
      return syncCLIProxyAPISite(site.id)
    },
    onSuccess: async (result) => {
      await queryClient.invalidateQueries({ queryKey: sitesQueryKeys.list() })
      await queryClient.invalidateQueries({ queryKey: sitesQueryKeys.detail(result.site.id) })
      if (result.ok) {
        toast.success(t('cliproxyapi.toast.synced'))
      } else {
        toast.warning(t('cliproxyapi.toast.syncFailed'), {
          description: result.message,
        })
      }
    },
    onError: (error) =>
      toast.error(t('cliproxyapi.toast.syncFailed'), {
        description: error.message,
      }),
  })

  const syncedSite = syncMutation.data?.site
  const currentSite = syncedSite?.id === site?.id ? syncedSite : site
  const config = currentSite?.cliproxyapi
  const sync = config?.sync
  const accounts = sync?.accounts ?? []
  const lastSuccess = sync?.last_success_at
    ? new Date(sync.last_success_at).getTime()
    : Number.NaN
  const stale = Number.isFinite(lastSuccess) && now - lastSuccess > STALE_AFTER_MS
  const available = accounts.filter((account) => !account.disabled && !account.unavailable).length
  const disabled = accounts.filter((account) => account.disabled).length
  const unavailable = accounts.filter((account) => account.unavailable).length

  const emptyMessage = useMemo(() => {
    if (!config?.management_key_configured) return t('cliproxyapi.unconfigured')
    if (sync?.status === 'error') return t('cliproxyapi.noSnapshot')
    return t('cliproxyapi.noAccounts')
  }, [config?.management_key_configured, sync?.status, t])

  return (
    <Draw
      open={open}
      onOpenChange={onOpenChange}
    >
      <DrawContent side="right" size="wide">
        <DrawHeader className="flex flex-col items-stretch gap-3 sm:flex-row sm:items-center sm:justify-between">
          <DrawTitle>{t('cliproxyapi.title')}</DrawTitle>
          <Button
            size="sm"
            variant="outline"
            className="min-h-11 w-full sm:w-auto"
            disabled={syncMutation.isPending || !config?.management_key_configured}
            onClick={() => syncMutation.mutate()}
          >
            {syncMutation.isPending ? (
              <LoaderCircle className="h-4 w-4 animate-spin motion-reduce:animate-none" />
            ) : (
              <RefreshCw className="h-4 w-4" />
            )}
            {t('cliproxyapi.refresh')}
          </Button>
        </DrawHeader>
        <DrawBody>
          <div className="space-y-4">
            <SyncSummary
              config={config}
              sync={sync}
              stale={stale}
              available={available}
              disabled={disabled}
              unavailable={unavailable}
              language={i18n.language}
              t={t}
            />

            {accounts.length === 0 ? (
              <p className="py-10 text-center text-sm text-muted-soft">{emptyMessage}</p>
            ) : (
              <div className="space-y-3">
                {accounts.map((account) => {
                  const key = `${currentSite?.id}:${account.auth_index}`
                  return (
                    <AccountCard
                      key={account.auth_index}
                      account={account}
                      expanded={expandedKey === key}
                      onToggle={() => setExpandedKey((current) => current === key ? null : key)}
                      language={i18n.language}
                      t={t}
                    />
                  )
                })}
              </div>
            )}
          </div>
        </DrawBody>
      </DrawContent>
    </Draw>
  )
}

function SyncSummary({
  config,
  sync,
  stale,
  available,
  disabled,
  unavailable,
  language,
  t,
}: {
  config?: SiteCLIProxyAPIConfig
  sync?: CLIProxyAPISyncSnapshot
  stale: boolean
  available: number
  disabled: number
  unavailable: number
  language: string
  t: CLIProxyAPIQuotaTranslator
}) {
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-soft">{t('cliproxyapi.observationHint')}</p>
      <div className="section-soft grid grid-cols-2 gap-3 rounded-lg p-4 text-sm sm:grid-cols-4">
        <SummaryMetric label={t('cliproxyapi.summary.total')} value={String(sync?.account_count ?? 0)} />
        <SummaryMetric label={t('cliproxyapi.summary.available')} value={String(available)} />
        <SummaryMetric label={t('cliproxyapi.summary.disabled')} value={String(disabled)} />
        <SummaryMetric label={t('cliproxyapi.summary.unavailable')} value={String(unavailable)} />
      </div>
      <div className="flex flex-wrap items-center gap-2 text-xs text-muted-soft">
        <StatusBadge status={sync?.status} t={t} />
        {sync?.last_success_at ? (
          <span>{t('cliproxyapi.lastSuccess', { time: formatDateTime(sync.last_success_at, language) })}</span>
        ) : null}
        {sync?.fetched_at && sync.fetched_at !== sync.last_success_at ? (
          <span>{t('cliproxyapi.lastFetched', { time: formatDateTime(sync.fetched_at, language) })}</span>
        ) : null}
        {stale ? <Badge variant="outline" className="text-amber-400">{t('cliproxyapi.stale')}</Badge> : null}
      </div>
      {config?.management_url ? (
        <p className="truncate text-xs text-muted-soft" title={config.management_url}>
          {t('cliproxyapi.managementUrl')}: {config.management_url}
        </p>
      ) : null}
      {sync?.last_error ? <p className="text-xs text-red-400">{sync.last_error}</p> : null}
    </div>
  )
}

function SummaryMetric({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <span className="block text-xs text-muted-soft">{label}</span>
      <span className="mt-1 block text-lg font-semibold tabular-nums text-foreground">{value}</span>
    </div>
  )
}

function StatusBadge({
  status,
  t,
}: {
  status?: string
  t: CLIProxyAPIQuotaTranslator
}) {
  const key = status === 'ok' ? 'ok' : status === 'error' ? 'error' : status === 'unconfigured' ? 'unconfigured' : 'never'
  return <Badge variant="outline">{t(`cliproxyapi.status.${key}`)}</Badge>
}

function AccountCard({
  account,
  expanded,
  onToggle,
  language,
  t,
}: {
  account: CLIProxyAPIAccount
  expanded: boolean
  onToggle: () => void
  language: string
  t: CLIProxyAPIQuotaTranslator
}) {
  const [emailRevealed, setEmailRevealed] = useState(false)
  const email = account.email?.trim() ?? ''
  const maskedEmail = maskCLIProxyAPIEmail(email)
  const status = account.disabled
    ? t('cliproxyapi.account.disabled')
    : account.unavailable
      ? t('cliproxyapi.account.unavailable')
      : account.status || t('cliproxyapi.account.available')
  const modelQuotas = Object.entries(account.model_quotas ?? {})
    .filter(([, quota]) => buildQuotaWindows(account.provider, quota).length > 0)
  const hasDetails = modelQuotas.length > 0
  const detailsId = `cliproxyapi-details-${account.auth_index.replace(/[^a-zA-Z0-9_-]/g, '-')}`

  return (
    <article className="section-soft space-y-3 rounded-lg p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 space-y-1.5">
          {email && maskedEmail !== '-' ? (
            <button
              type="button"
              className="flex min-h-11 max-w-full items-center truncate rounded-sm text-left text-sm font-medium text-foreground outline-none focus-visible:ring-2 focus-visible:ring-primary"
              title={emailRevealed ? email : undefined}
              aria-label={t(emailRevealed ? 'cliproxyapi.account.hideEmail' : 'cliproxyapi.account.showEmail')}
              aria-pressed={emailRevealed}
              onClick={() => setEmailRevealed((revealed) => !revealed)}
            >
              <span className="truncate">{emailRevealed ? email : maskedEmail}</span>
            </button>
          ) : (
            <span className="text-sm text-muted-soft">-</span>
          )}
          <div className="flex flex-wrap items-center gap-1.5 text-xs text-muted-soft">
            {account.provider ? <Badge variant="outline" className="px-1.5 py-0 text-[10px]">{account.provider}</Badge> : null}
            {account.plan_type ? <Badge variant="outline" className="px-1.5 py-0 text-[10px]">{account.plan_type}</Badge> : null}
            <Badge variant="outline" className="px-1.5 py-0 text-[10px]">{status}</Badge>
          </div>
        </div>
        {hasDetails ? (
          <Button
            size="icon"
            variant="ghost"
            className="h-11 w-11 shrink-0"
            onClick={onToggle}
            aria-expanded={expanded}
            aria-controls={detailsId}
            aria-label={t(expanded ? 'cliproxyapi.account.hideDetails' : 'cliproxyapi.account.showDetails')}
          >
            <ChevronDown className={`h-4 w-4 transition-transform motion-reduce:transition-none ${expanded ? 'rotate-180' : ''}`} />
          </Button>
        ) : null}
      </div>

      {account.status_message ? <p className="text-xs text-muted-soft">{account.status_message}</p> : null}
      {!isStandardQuotaProvider(account.provider) ? <p className="text-xs text-muted-soft">{t('cliproxyapi.account.noStandardQuota')}</p> : null}
      <QuotaOverview provider={account.provider} quota={account.quota} language={language} t={t} />
      <AccountStats account={account} language={language} t={t} />
      <RequestHistory account={account} t={t} />

      {expanded ? (
        <div id={detailsId} className="space-y-4 border-t border-[hsl(var(--glass-divider))] pt-3">
          <ModelQuotaDisclosure
            detailsId={detailsId}
            provider={account.provider}
            modelQuotas={modelQuotas}
            language={language}
            t={t}
          />
        </div>
      ) : null}
    </article>
  )
}

function ModelQuotaDisclosure({
  detailsId,
  provider,
  modelQuotas,
  language,
  t,
}: {
  detailsId: string
  provider: string
  modelQuotas: [string, CLIProxyAPIQuotaObservation][]
  language: string
  t: CLIProxyAPIQuotaTranslator
}) {
  if (modelQuotas.length === 0) return null
  return (
    <section className="space-y-3" aria-labelledby={`${detailsId}-models`}>
      <h4 id={`${detailsId}-models`} className="text-xs font-semibold text-foreground">{t('cliproxyapi.account.modelQuota')}</h4>
      {modelQuotas.map(([model, quota]) => (
        <div key={model} className="rounded-md border border-[hsl(var(--glass-border))] p-3">
          <div className="mb-2 text-xs font-medium text-foreground">{model}</div>
          <QuotaOverview provider={provider} quota={quota} language={language} t={t} showTitle={false} />
        </div>
      ))}
    </section>
  )
}

function AccountStats({
  account,
  language,
  t,
}: {
  account: CLIProxyAPIAccount
  language: string
  t: CLIProxyAPIQuotaTranslator
}) {
  return (
    <div className="grid grid-cols-2 gap-2 text-xs text-muted-soft sm:grid-cols-4">
      <SummaryMetric label={t('cliproxyapi.account.success')} value={String(account.success)} />
      <SummaryMetric label={t('cliproxyapi.account.failed')} value={String(account.failed)} />
      <SummaryMetric label={t('cliproxyapi.account.lastRefresh')} value={formatOptionalDate(account.last_refresh, language)} />
      <SummaryMetric label={t('cliproxyapi.account.retry')} value={formatQuotaSignal('Retry-After', account.next_retry_after, language, t)} />
    </div>
  )
}

function QuotaOverview({
  provider,
  quota,
  language,
  t,
  showTitle = true,
}: {
  provider: string
  quota?: CLIProxyAPIQuotaObservation
  language: string
  t: CLIProxyAPIQuotaTranslator
  showTitle?: boolean
}) {
  if (!isStandardQuotaProvider(provider)) return null
  const windows = buildQuotaWindows(provider, quota)
  return (
    <section className="space-y-2" aria-label={showTitle ? t('cliproxyapi.account.quotaWindows') : undefined}>
      {showTitle ? <h4 className="text-xs font-semibold text-foreground">{t('cliproxyapi.account.quotaWindows')}</h4> : null}
      {windows.length > 0 ? (
        <div className="grid gap-2 sm:grid-cols-2">
          {windows.map((window) => <QuotaWindow key={window.key} window={window} language={language} t={t} />)}
        </div>
      ) : (
        <p className="text-xs text-muted-soft">{t('cliproxyapi.account.quotaNotObserved')}</p>
      )}
    </section>
  )
}

function QuotaWindow({
  window,
  language,
  t,
}: {
  window: CLIProxyAPIQuotaWindow
  language: string
  t: CLIProxyAPIQuotaTranslator
}) {
  const hasPercent = window.usedPercent != null && window.remainingPercent != null
  const remaining = window.remainingPercent
  const progressVariant = remaining != null && remaining <= 10
    ? 'danger'
    : remaining != null && remaining <= 30
      ? 'warning'
      : 'success'
  const status = quotaWindowStatus(window, t)
  const reset = window.resetAt
    ? formatQuotaSignal('Reset-At', window.resetAt, language, t)
    : window.resetAfterSeconds != null
      ? formatQuotaSignal('Retry-After', String(window.resetAfterSeconds), language, t)
      : '-'

  return (
    <div className="rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-subtle)/0.35)] p-3">
      <div className="flex items-start justify-between gap-2">
        <span className="text-xs font-medium text-foreground">{quotaWindowLabel(window, t)}</span>
        <span className="text-right text-[11px] tabular-nums text-muted-soft">
          {window.usedPercent != null ? t('cliproxyapi.account.usedPercent', { value: formatPercent(window.usedPercent) }) : t('cliproxyapi.account.notObserved')}
        </span>
      </div>
      {hasPercent && remaining != null ? (
        <Progress
          value={remaining}
          max={100}
          variant={progressVariant}
          className="mt-2"
          aria-label={t('cliproxyapi.account.remainingPercent', { value: formatPercent(remaining) })}
        />
      ) : null}
      <div className="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1 text-[11px] text-muted-soft">
        {window.remainingPercent != null ? <span>{t('cliproxyapi.account.remainingPercent', { value: formatPercent(window.remainingPercent) })}</span> : null}
        {window.windowMinutes != null ? <span>{t('cliproxyapi.account.windowDuration', { value: formatWindowDuration(window.windowMinutes, t) })}</span> : null}
        <span>{t('cliproxyapi.account.reset', { value: reset })}</span>
        {status ? <Badge variant="outline" className="px-1.5 py-0 text-[10px]">{status}</Badge> : null}
      </div>
      {window.observedAt ? <div className="mt-1 text-[10px] text-muted-soft">{t('cliproxyapi.account.observedAt', { time: formatDateTime(window.observedAt, language) })}</div> : null}
    </div>
  )
}

function RequestHistory({
  account,
  t,
}: {
  account: CLIProxyAPIAccount
  t: CLIProxyAPIQuotaTranslator
}) {
  const points = buildRecentRequestSeries(account)
  const legacyTotal = account.recent_requests_total
  if (points.length === 0 && legacyTotal == null) return null

  const recentSuccess = points.reduce((sum, point) => sum + point.success, 0)
  const recentFailed = points.reduce((sum, point) => sum + point.failed, 0)
  const recentTotal = points.length > 0 ? recentSuccess + recentFailed : legacyTotal ?? 0
  const hasRequests = points.some((point) => point.total > 0)
  const maxTotal = Math.max(1, ...points.map((point) => point.total))

  return (
    <section className="space-y-2" aria-labelledby={`cliproxyapi-history-${account.auth_index}`}>
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 id={`cliproxyapi-history-${account.auth_index}`} className="text-xs font-semibold text-foreground">{t('cliproxyapi.account.requestHistory')}</h4>
        <span className="text-[11px] text-muted-soft">{t('cliproxyapi.account.totalRequests', { value: recentTotal })}</span>
      </div>
      {points.length > 0 && hasRequests ? (
        <>
          <p className="sr-only">{t('cliproxyapi.account.historySummary', { count: points.length, success: recentSuccess, failed: recentFailed })}</p>
          <div className="flex h-28 items-end gap-1 rounded-md border border-[hsl(var(--glass-border))] bg-[hsl(var(--surface-subtle)/0.35)] px-2 py-2" role="group" aria-label={t('cliproxyapi.account.requestHistory')}>
            {points.map((point, index) => (
              <button
                key={`${point.time}:${index}`}
                type="button"
                className="group relative flex h-full min-w-0 flex-1 items-end justify-center rounded-sm px-0.5 outline-none focus-visible:ring-2 focus-visible:ring-primary"
                aria-label={historyPointLabel(point, t)}
                title={historyPointLabel(point, t)}
              >
                <span className="pointer-events-none absolute bottom-full left-1/2 z-10 mb-1 hidden -translate-x-1/2 whitespace-nowrap rounded bg-[hsl(var(--surface-panel))] px-2 py-1 text-[10px] text-foreground shadow-lg group-hover:block group-focus-visible:block">
                  {historyPointLabel(point, t)}
                </span>
                <span className="flex h-full w-full max-w-5 flex-col justify-end gap-px" aria-hidden="true">
                  <span className="w-full rounded-t-sm bg-red-400/80" style={{ height: `${(point.failed / maxTotal) * 100}%` }} />
                  <span className="w-full rounded-b-sm bg-emerald-400/80" style={{ height: `${(point.success / maxTotal) * 100}%` }} />
                </span>
              </button>
            ))}
          </div>
          <div className="flex items-center justify-between gap-3 text-[10px] text-muted-soft">
            <span>{points[0]?.time ?? '-'}</span>
            <span>{points[Math.floor(points.length / 2)]?.time ?? '-'}</span>
            <span>{points[points.length - 1]?.time ?? '-'}</span>
          </div>
          <div className="flex flex-wrap items-center gap-3 text-[10px] text-muted-soft" aria-hidden="true">
            <span className="inline-flex items-center gap-1"><i className="h-2 w-2 rounded-sm bg-emerald-400/80" />{t('cliproxyapi.account.success')}</span>
            <span className="inline-flex items-center gap-1"><i className="h-2 w-2 rounded-sm bg-red-400/80" />{t('cliproxyapi.account.failed')}</span>
          </div>
          <table className="sr-only">
            <caption>{t('cliproxyapi.account.requestHistory')}</caption>
            <thead><tr><th>{t('cliproxyapi.account.time')}</th><th>{t('cliproxyapi.account.success')}</th><th>{t('cliproxyapi.account.failed')}</th></tr></thead>
            <tbody>{points.map((point, index) => <tr key={`${point.time}-row-${index}`}><td>{point.time}</td><td>{point.success}</td><td>{point.failed}</td></tr>)}</tbody>
          </table>
        </>
      ) : points.length > 0 || legacyTotal === 0 ? (
        <p className="text-xs text-muted-soft">{t('cliproxyapi.account.noRequestHistory')}</p>
      ) : (
        <p className="text-xs text-muted-soft">{t('cliproxyapi.account.legacyRequestCount', { value: legacyTotal })}</p>
      )}
    </section>
  )
}

function quotaWindowLabel(window: CLIProxyAPIQuotaWindow, t: CLIProxyAPIQuotaTranslator) {
  if (window.kind === 'primary' && window.windowMinutes === 300) return t('cliproxyapi.account.fiveHour')
  if (window.kind === 'secondary' && window.windowMinutes === 10080) return t('cliproxyapi.account.sevenDay')
  if (window.kind === 'primary') return t('cliproxyapi.account.primaryWindow')
  if (window.kind === 'secondary') return t('cliproxyapi.account.secondaryWindow')
  if (window.kind === 'additional') return `${t('cliproxyapi.account.additionalWindow')} · ${window.namespace?.replace(/^additional-/, '').replace(/-/g, ' ') ?? ''}`
  if (window.kind === 'code_review') return t('cliproxyapi.account.codeReviewWindow')
  return window.label
}

function quotaWindowStatus(window: CLIProxyAPIQuotaWindow, t: CLIProxyAPIQuotaTranslator) {
  if (window.limitReached === true || window.status === 'limited') return t('cliproxyapi.account.limitReached')
  if (window.status === 'not_allowed') return t('cliproxyapi.account.notAllowed')
  if (window.status === 'allowed') return t('cliproxyapi.account.allowed')
  if (window.status) return window.status
  return undefined
}

function formatPercent(value: number) {
  return `${Number.isInteger(value) ? value : value.toFixed(1).replace(/\.0$/, '')}%`
}

function formatWindowDuration(minutes: number, t: CLIProxyAPIQuotaTranslator) {
  if (minutes % 10080 === 0) return t('cliproxyapi.duration.weeks', { count: minutes / 10080 })
  if (minutes % 1440 === 0) return t('cliproxyapi.duration.days', { count: minutes / 1440 })
  if (minutes % 60 === 0) return t('cliproxyapi.duration.hours', { count: minutes / 60 })
  return t('cliproxyapi.duration.minutes', { count: minutes })
}

function historyPointLabel(point: CLIProxyAPIRecentRequestPoint, t: CLIProxyAPIQuotaTranslator) {
  return t('cliproxyapi.account.historyPoint', {
    time: point.time,
    success: point.success,
    failed: point.failed,
  })
}

function formatOptionalDate(value: string | undefined, language: string) {
  return value ? formatDateTime(value, language) : '-'
}

function isStandardQuotaProvider(provider: string) {
  const normalized = provider.trim().toLowerCase()
  return normalized === 'codex' || normalized === 'claude' || normalized === 'claude_code'
}
