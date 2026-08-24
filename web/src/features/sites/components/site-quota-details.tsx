import { useEffect, useId, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import {
  formatDateTime,
  formatDisplayQuota,
  sub2APIKeyQuotaDetails,
} from '@/features/sites/lib/site-utils'
import type { SiteAPIKey, SiteAPIKeyUsageData } from '@/features/sites/api/sites'
import type { SiteBalanceDetail } from '@/features/sites/lib/site-utils'

type PointerPosition = {
  x: number
  y: number
}

type SiteQuotaDetailsPopoverProps = {
  children: ReactNode
  content: ReactNode
  description?: string
  enabled: boolean
  className?: string
}

export function SiteQuotaDetailsPopover({
  children,
  content,
  description,
  enabled,
  className,
}: SiteQuotaDetailsPopoverProps) {
  const tooltipId = useId()
  const closeTimer = useRef<number | null>(null)
  const [open, setOpen] = useState(false)
  const [position, setPosition] = useState<PointerPosition | null>(null)

  useEffect(() => () => {
    if (closeTimer.current !== null) window.clearTimeout(closeTimer.current)
  }, [])

  const cancelClose = () => {
    if (closeTimer.current === null) return
    window.clearTimeout(closeTimer.current)
    closeTimer.current = null
  }

  const scheduleClose = () => {
    cancelClose()
    closeTimer.current = window.setTimeout(() => {
      setOpen(false)
      closeTimer.current = null
    }, 150)
  }

  const tooltip = enabled && open && position && typeof document !== 'undefined'
    ? createPortal(
        <div
          id={tooltipId}
          role="tooltip"
          className="glass-panel-strong fixed z-[160] max-h-[min(70vh,520px)] w-[min(440px,calc(100vw-32px))] overflow-y-auto overscroll-contain rounded-lg px-3 py-2 text-xs leading-5 shadow-lg"
          style={{
            left: Math.min(position.x + 12, Math.max(12, window.innerWidth - 456)),
            top: position.y <= window.innerHeight / 2 ? position.y + 14 : undefined,
            bottom: position.y > window.innerHeight / 2 ? window.innerHeight - position.y + 14 : undefined,
          }}
          onMouseEnter={cancelClose}
          onMouseLeave={scheduleClose}
        >
          {content}
        </div>,
        document.body,
      )
    : null

  return (
    <>
      <span
        className={cn(className)}
        aria-describedby={enabled ? tooltipId : undefined}
        tabIndex={enabled ? 0 : undefined}
        onMouseEnter={(event) => {
          if (!enabled) return
          cancelClose()
          setOpen(true)
          setPosition({ x: event.clientX, y: event.clientY })
        }}
        onMouseMove={(event) => {
          if (!enabled) return
          setPosition({ x: event.clientX, y: event.clientY })
        }}
        onMouseLeave={scheduleClose}
        onFocus={(event) => {
          if (!enabled) return
          cancelClose()
          const rect = event.currentTarget.getBoundingClientRect()
          setOpen(true)
          setPosition({ x: rect.left + rect.width / 2, y: rect.bottom })
        }}
        onBlur={() => {
          cancelClose()
          setOpen(false)
        }}
      >
        {children}
      </span>
      {!open && enabled && description ? (
        <span id={tooltipId} role="tooltip" className="sr-only">{description}</span>
      ) : null}
      {tooltip}
    </>
  )
}

function quotaDetailValue(
  detail: SiteBalanceDetail,
  translate: (key: string) => string,
) {
  const quota = detail.valuePrefix
    ? `${translate(`table.quotaDetails.${detail.valuePrefix}`)} ${detail.value}`
    : detail.value
  return detail.extra ? `${quota} · ${detail.extra}` : quota
}

function formatAPIKeyExpiry(
  value: string | number | null | undefined,
  language: string,
) {
  if (value === null || value === undefined) return ''
  if (typeof value === 'number' && Number.isFinite(value)) {
    if (value <= 0) return ''
    const milliseconds = Math.abs(value) < 1_000_000_000_000 ? value * 1000 : value
    return formatDateTime(new Date(milliseconds).toISOString(), language, 'h23')
  }
  if (typeof value !== 'string') return ''
  const normalized = value.trim()
  if (!normalized) return ''
  if (/^[+-]?\d+(?:\.\d+)?$/.test(normalized)) {
    const numeric = Number(normalized)
    if (!Number.isFinite(numeric) || numeric <= 0) return ''
    const milliseconds = Math.abs(numeric) < 1_000_000_000_000 ? numeric * 1000 : numeric
    return formatDateTime(new Date(milliseconds).toISOString(), language, 'h23')
  }
  return formatDateTime(normalized, language, 'h23')
}

function apiKeyUsageDetails(data: SiteAPIKeyUsageData | undefined): SiteBalanceDetail[] {
  if (!data) return []
  const amount = (value: number) => `$${formatDisplayQuota(Math.max(value, 0))}`
  if (data.unlimited_quota === true) {
    const rows: SiteBalanceDetail[] = []
    if (typeof data.total_used === 'number') {
      rows.push({ label: 'tokenUsed', value: amount(data.total_used) })
    }
    rows.push({ label: 'unlimited', value: '∞' })
    return rows
  }

  const rows: SiteBalanceDetail[] = []
  if (typeof data.total_available === 'number') {
    rows.push({ label: 'tokenBalance', value: amount(data.total_available) })
  }
  if (typeof data.total_granted === 'number') {
    rows.push({ label: 'totalLimit', value: amount(data.total_granted) })
  }
  if (typeof data.total_used === 'number') {
    rows.push({ label: 'tokenUsed', value: amount(data.total_used) })
  }
  return rows
}

export function SiteAPIKeyQuotaDetailsContent({
  apiKey,
  showIdentity = true,
}: {
  apiKey: SiteAPIKey
  showIdentity?: boolean
}) {
  const { t, i18n } = useTranslation('sites')
  const probe = apiKey.quota_probe
  const probeDetails = sub2APIKeyQuotaDetails(probe, i18n.language)
  const details = probeDetails.length > 0
    ? probeDetails
    : apiKeyUsageDetails(apiKey.usage?.data)
  const failure = probe?.status === 'error'
    ? probe.fetched_at
      ? t('table.quotaDetails.probeFailedAt', {
          time: formatDateTime(probe.fetched_at, i18n.language, 'h23'),
        })
      : t('table.quotaDetails.probeFailed')
    : ''
  const plan = details.some((detail) => detail.label === 'accountBalance') ? undefined : probe?.plan
  const expiresAt = formatAPIKeyExpiry(
    probe?.expires_at ?? apiKey.usage?.data?.expires_at,
    i18n.language,
  )
  const keyName = apiKey.name && apiKey.name !== apiKey.key ? apiKey.name : ''

  return (
    <div className={showIdentity ? 'py-2 first:pt-0 last:pb-0' : 'text-left'}>
      {showIdentity ? (
        <>
          <div className="flex min-w-0 items-center justify-between gap-3">
            <span className="truncate text-foreground">{keyName || apiKey.key}</span>
            {apiKey.group ? <span className="shrink-0 text-muted-foreground">{apiKey.group}</span> : null}
          </div>
          {keyName ? <div className="truncate font-mono text-muted-foreground">{apiKey.key}</div> : null}
        </>
      ) : null}
      {failure ? <div className="mt-1 text-amber-400">{failure}</div> : null}
      {plan || expiresAt || details.length > 0 ? (
        <div className="mt-1 grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-0.5">
          {plan ? (
            <>
              <span className="text-muted-foreground">{t('table.quotaDetails.plan')}</span>
              <span className="text-foreground">{plan}</span>
            </>
          ) : null}
          {details.map((detail, index) => (
            <div key={`${detail.label}-${index}`} className="contents">
              <span className="text-muted-foreground">{detail.labelText ?? t(`table.quotaDetails.${detail.label}`)}</span>
              <span className="text-foreground tabular-nums">{quotaDetailValue(detail, (key) => t(key))}</span>
            </div>
          ))}
          {expiresAt ? (
            <>
              <span className="text-muted-foreground">{t('table.quotaDetails.expiresAt')}</span>
              <span className="text-foreground tabular-nums">{expiresAt}</span>
            </>
          ) : null}
        </div>
      ) : !failure ? <div className="mt-1 text-muted-foreground">{t('table.quotaDetails.noQuota')}</div> : null}
    </div>
  )
}
