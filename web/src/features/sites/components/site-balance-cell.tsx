import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import { commandCodeQuotaWindows, formatDateTime, formatSiteBalance, isSub2APIQuotaSite, siteBalanceDetails, sub2APIKeyQuotaDetails } from '@/features/sites/lib/site-utils'
import type { Site, SiteAPIKey } from '@/features/sites/api/sites'
import { SiteAPIKeyQuotaDetailsContent, SiteQuotaDetailsPopover } from '@/features/sites/components/site-quota-details'

function getBalanceDetails(site: Site, apiKeys: SiteAPIKey[], language: string) {
  const details = siteBalanceDetails(site, language)
  const sub2APIKeys = isSub2APIQuotaSite(site) ? apiKeys : []
  const keyDetails = sub2APIKeys.map((apiKey) => ({
    apiKey,
    details: sub2APIKeyQuotaDetails(apiKey.quota_probe, language),
  }))

  return { details, keyDetails }
}

export function SiteBalanceDetailsContent({ site, apiKeys = [] }: { site: Site; apiKeys?: SiteAPIKey[] }) {
  const { t, i18n } = useTranslation('sites')
  const { details, keyDetails } = getBalanceDetails(site, apiKeys, i18n.language)
  const detailValue = (detail: (typeof details)[number]) => {
    const quota = detail.valuePrefix
      ? `${t(`table.quotaDetails.${detail.valuePrefix}`)} ${detail.value}`
      : detail.value
    return detail.extra ? `${quota} · ${detail.extra}` : quota
  }
  if (keyDetails.length === 0) {
    return (
      <div className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1 text-left">
        {site.quota_probe?.probe_type === 'command_code' && (
          <>
            {site.quota_probe.status === 'error' && <span className="col-span-2 text-destructive">{t('table.quotaDetails.probeFailed')}</span>}
            {site.quota_probe.plan && <>
              <span className="text-muted-foreground">{t('table.quotaDetails.plan')}</span>
              <span>{site.quota_probe.plan}</span>
            </>}
            {site.quota_probe.expires_at && <>
              <span className="text-muted-foreground">{t('table.quotaDetails.expiresAt')}</span>
              <span>{formatDateTime(site.quota_probe.expires_at, i18n.language, 'h23')}</span>
            </>}
          </>
        )}
        {details.map((detail, index) => (
          <div key={`${detail.label}-${index}`} className="contents">
            <span className="text-muted-foreground">{detail.labelText ?? t(`table.quotaDetails.${detail.label}`)}</span>
            <span className="text-foreground tabular-nums">{detailValue(detail)}</span>
          </div>
        ))}
      </div>
    )
  }

  return (
    <div className="divide-y divide-[hsl(var(--glass-divider))] text-left">
      {keyDetails.map(({ apiKey }) => <SiteAPIKeyQuotaDetailsContent key={apiKey.id} apiKey={apiKey} />)}
    </div>
  )
}

export function SiteBalanceCell({ site, apiKeys = [], className }: { site: Site; apiKeys?: SiteAPIKey[]; className?: string }) {
  const { t, i18n } = useTranslation('sites')
  const value = formatSiteBalance(site)
  const { details, keyDetails } = getBalanceDetails(site, apiKeys, i18n.language)
  const commandCode = site.quota_probe?.probe_type === 'command_code'
  const windows = commandCodeQuotaWindows(site.quota_probe)
  const failed = commandCode && site.quota_probe?.status === 'error'
  const showTooltip = keyDetails.length > 0 || details.length > 0 || commandCode
  const detailValue = (detail: (typeof details)[number]) => {
    const quota = detail.valuePrefix
      ? `${t(`table.quotaDetails.${detail.valuePrefix}`)} ${detail.value}`
      : detail.value
    return detail.extra ? `${quota} · ${detail.extra}` : quota
  }
  const probeFailureText = (probe: SiteAPIKey['quota_probe']) => {
    if (probe?.status !== 'error') return ''
    const fetchedAt = probe.fetched_at ? formatDateTime(probe.fetched_at, i18n.language, 'h23') : ''
    return fetchedAt
      ? t('table.quotaDetails.probeFailedAt', { time: fetchedAt })
      : t('table.quotaDetails.probeFailed')
  }
  const tooltipDescription = showTooltip && keyDetails.length === 0
    ? `${value}: ${details.map((detail) => `${detail.labelText ?? t(`table.quotaDetails.${detail.label}`)} ${detailValue(detail)}`).join(', ')}`
    : keyDetails.length > 0
      ? `${value}: ${keyDetails.map(({ apiKey, details: apiKeyDetails }) => {
          const probe = apiKey.quota_probe
          const expiresAt = probe?.expires_at ? formatDateTime(probe.expires_at, i18n.language, 'h23') : ''
          const values = [apiKey.name && apiKey.name !== apiKey.key ? `${apiKey.name}, ${apiKey.key}` : apiKey.key]
          const plan = apiKeyDetails.some((detail) => detail.label === 'accountBalance') ? undefined : probe?.plan
          if (apiKey.group) values.push(apiKey.group)
          const failure = probeFailureText(probe)
          if (failure) values.push(failure)
          if (plan) values.push(`${t('table.quotaDetails.plan')} ${plan}`)
          values.push(...apiKeyDetails.map((detail) => `${detail.labelText ?? t(`table.quotaDetails.${detail.label}`)} ${detailValue(detail)}`))
          if (expiresAt) values.push(`${t('table.quotaDetails.expiresAt')} ${expiresAt}`)
          if (!failure && !plan && !expiresAt && apiKeyDetails.length === 0) values.push(t('table.quotaDetails.noQuota'))
          return values.join(', ')
        }).join('; ')}`
      : undefined

  return (
    <SiteQuotaDetailsPopover
      enabled={showTooltip}
      className={cn('text-sm text-foreground tabular-nums', commandCode && 'inline-flex flex-col gap-0.5', className)}
      description={tooltipDescription}
      content={<SiteBalanceDetailsContent site={site} apiKeys={apiKeys} />}
    >
      <span>{value}</span>
      {windows.length > 0 && <span className="text-xs text-muted-foreground">
        {windows.map((window) => `${t(`table.quotaDetails.${window.label}`)} ${t('table.quotaDetails.remaining')} ${window.value}`).join(' · ')}
      </span>}
      {failed && <span className="text-xs text-destructive">{t('table.quotaDetails.probeFailed')}</span>}
    </SiteQuotaDetailsPopover>
  )
}
