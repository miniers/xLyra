import { describe, expect, it } from 'vitest'

import type { Site, SiteQuotaProbeEntry } from '@/features/sites/api/sites'
import { commandCodeQuotaWindows, formatCompactTokens, formatDateTime, formatSiteBalance, isSiteAbnormal, siteBalanceDetails, sortSitesForDisplay, sub2APIKeyQuotaDetails } from '@/features/sites/lib/site-utils'

function siteWithSyncState(failureClass: 'unknown' | 'limited' | 'transient' | 'credential_invalid'): Site {
  return {
    id: 'site-1',
    name: 'Upstream',
    slug: 'upstream',
    site_type: 'openai',
    base_url: 'https://example.invalid',
    status: 'active',
    enabled: true,
    routing_priority: 0,
    meta: {},
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
    sync_state: {
      status: 'partial',
      validation_ok: true,
      failure_class: failureClass,
    },
  }
}

describe('isSiteAbnormal', () => {
  it.each(['unknown', 'limited', 'transient'] as const)('keeps %s failures available', (failureClass) => {
    expect(isSiteAbnormal(siteWithSyncState(failureClass))).toBe(false)
  })

  it('marks a confirmed invalid API key as abnormal', () => {
    expect(isSiteAbnormal(siteWithSyncState('credential_invalid'))).toBe(true)
  })
})

describe('sortSitesForDisplay', () => {
  it('sorts enabled sites by routing priority and keeps disabled sites last', () => {
    const sites = [
      { ...siteWithSyncState('unknown'), id: 'low', name: 'Low', slug: 'low', routing_priority: 1 },
      { ...siteWithSyncState('unknown'), id: 'disabled', name: 'Disabled', slug: 'disabled', routing_priority: 100, enabled: false },
      { ...siteWithSyncState('unknown'), id: 'high', name: 'High', slug: 'high', routing_priority: 10 },
    ]

    expect(sortSitesForDisplay(sites).map((site) => site.id)).toEqual(['high', 'low', 'disabled'])
  })
})

describe('formatCompactTokens', () => {
  it('can omit the unit for compact mobile metrics', () => {
    expect(formatCompactTokens(3_200_000)).toBe('3.2M tokens')
    expect(formatCompactTokens(3_200_000, false)).toBe('3.2M')
  })
})

describe('Command Code quota formatting', () => {
  const site: Site = {
    ...siteWithSyncState('unknown'), site_type: 'command_code',
    quota_probe: {
      probe_type: 'command_code', remaining_min: 0, unit: 'credits',
      entries: [
        { label: 'five_hour', unit: 'credits', remaining: 3.77, limit: 5, reset_at: '2026-09-19T10:00:00Z' },
        { label: 'weekly', unit: 'credits', remaining: 27.5, limit: 40 },
        { label: 'balance', unit: 'credits', remaining: 25.5 },
      ],
    },
  }
  it('uses the balance entry rather than a limiting window or another credential', () => {
    expect(formatSiteBalance(site)).toBe('25.50 credits')
    expect(commandCodeQuotaWindows(site.quota_probe).map((row) => row.value)).toEqual(['75.4%', '68.8%'])
    const details = siteBalanceDetails(site, 'en')
    expect(details[0].value).toBe('25.50 credits')
    expect(details[1].value).toBe('3.77 credits / 5.00 credits')
    expect(details[1].extra).toBe(formatDateTime('2026-09-19T10:00:00Z', 'en', 'h23'))
  })
  it('keeps stale values on error without inventing missing data', () => {
    const probe = { ...site.quota_probe, status: 'error', entries: [{ label: 'weekly', remaining: 0, limit: 40 }] }
    expect(formatSiteBalance({ ...site, quota_probe: probe })).toBe('-')
    expect(commandCodeQuotaWindows(probe).map((row) => row.value)).toEqual(['0%'])
    expect(commandCodeQuotaWindows({ ...probe, entries: [{ label: 'weekly', remaining: 0, limit: 0 }] })).toEqual([])
  })
})

describe('Kimi quota formatting', () => {
  const site: Site = {
    ...siteWithSyncState('unknown'),
    site_type: 'kimi_code',
    quota_probe: {
      probe_type: 'kimi',
      remaining_min: 0,
      unit: 'percent',
      entries: [
        { label: 'five_hour', unit: 'percent', remaining: 0, limit: 100, used: 100 },
        { label: 'weekly', unit: 'percent', remaining: 80, limit: 100, used: 20 },
      ],
    },
  }

  it('shows remaining quota for both windows in the table', () => {
    expect(formatSiteBalance(site)).toBe('0% / 80%')
  })

  it('marks the remaining quota values for details', () => {
    expect(siteBalanceDetails(site).map((detail) => ({ value: detail.value, valuePrefix: detail.valuePrefix }))).toEqual([
      { value: '0%', valuePrefix: 'remaining' },
      { value: '80%', valuePrefix: 'remaining' },
    ])
  })

  it('formats reset times without a 12-hour suffix', () => {
    const value = formatDateTime('2026-07-30T01:12:00Z', 'en', 'h23')
    expect(value).toMatch(/\b\d{2}:\d{2}\b/)
    expect(value).not.toMatch(/\b(?:AM|PM)\b/i)
  })
})

describe('GLM quota formatting', () => {
  const glmSite = (entries: SiteQuotaProbeEntry[]): Site => ({
    ...siteWithSyncState('unknown'),
    site_type: 'glm_code',
    quota_probe: {
      probe_type: 'glm',
      remaining_min: 0,
      unit: 'percent',
      plan: 'Pro',
      entries,
    },
  })

  it('shows remaining quota for both windows in the table', () => {
    const site = glmSite([
      { label: 'five_hour', unit: 'percent', remaining: 100, limit: 100, used: 0 },
      { label: 'weekly', unit: 'percent', remaining: 0, limit: 100, used: 100, reset_at: '2026-09-10T01:59:59Z' },
    ])
    expect(formatSiteBalance(site)).toBe('100% / 0%')
  })

  it('shows a dash for a missing window', () => {
    const site = glmSite([
      { label: 'five_hour', unit: 'percent', remaining: 97, limit: 100, used: 3 },
    ])
    expect(formatSiteBalance(site)).toBe('97% / -')
  })

  it('marks the remaining quota values for details', () => {
    const site = glmSite([
      { label: 'five_hour', unit: 'percent', remaining: 100, limit: 100, used: 0 },
      { label: 'weekly', unit: 'percent', remaining: 0, limit: 100, used: 100 },
    ])
    expect(siteBalanceDetails(site).map((detail) => ({ label: detail.label, value: detail.value, valuePrefix: detail.valuePrefix }))).toEqual([
      { label: 'fiveHourQuota', value: '100%', valuePrefix: 'remaining' },
      { label: 'weeklyQuota', value: '0%', valuePrefix: 'remaining' },
    ])
  })
})

describe('sub2api key quota formatting', () => {
  it('formats Plan windows as remaining percentages in display order', () => {
    const details = sub2APIKeyQuotaDetails({
      status: 'ok',
      kind: 'subscription_plan',
      plan: '待宵计划',
      entries: [
        { label: 'monthly', unit: 'percent', remaining: 0, limit: 100, used: 100 },
        { label: 'daily', unit: 'percent', remaining: 100, limit: 100, used: 0, reset_at: '2026-08-19T00:00:00+08:00' },
        { label: 'weekly', unit: 'percent', remaining: 74.5, limit: 100, used: 25.5 },
      ],
    }, 'zh')

    expect(details.map((detail) => ({ label: detail.label, value: detail.value, valuePrefix: detail.valuePrefix }))).toEqual([
      { label: 'dailyQuota', value: '100%', valuePrefix: 'remaining' },
      { label: 'weeklyQuota', value: '74.5%', valuePrefix: 'remaining' },
      { label: 'monthlyQuota', value: '0%', valuePrefix: 'remaining' },
    ])
    expect(details[0]?.extra).toBeTruthy()
  })

  it('keeps the direct value on the account balance fallback', () => {
    const site: Site = {
      ...siteWithSyncState('unknown'),
      gateway_config: { quota_probe: 'sub2api' },
      quota_probe: {
        probe_type: 'sub2api',
        remaining_min: 15,
        unit: 'percent',
        plan: '待宵计划',
        entries: [
          { label: 'daily', unit: 'percent', remaining: 100, limit: 100, used: 0 },
        ],
      },
      sync_state: {
        status: 'synced',
        user_summary: { data: { quota: 21_000_000, currency: 'USD' } },
      },
    }

    expect(formatSiteBalance(site)).toBe('$42.00')
  })
})
