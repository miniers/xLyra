import { renderToStaticMarkup } from 'react-dom/server'
import { describe, expect, it, vi } from 'vitest'
import { SiteBalanceCell, SiteBalanceDetailsContent } from './site-balance-cell'
import type { Site } from '../api/sites'

vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key, i18n: { language: 'en' } }),
}))

const site: Site = {
  id: 'cc', name: 'Command Code', slug: 'cc', site_type: 'command_code',
  base_url: 'https://example.invalid', status: 'active', enabled: true, routing_priority: 0,
  meta: {}, created_at: '', updated_at: '',
  quota_probe: {
    probe_type: 'command_code', status: 'ok', plan: 'GOAT', expires_at: '2026-10-19T10:00:00Z',
    entries: [
      { label: 'balance', remaining: 25.5, unit: 'credits' },
      { label: 'five_hour', remaining: 3.77, limit: 5, unit: 'credits', reset_at: '2026-09-19T10:00:00Z' },
      { label: 'weekly', remaining: 27.5, limit: 40, unit: 'credits' },
    ],
  },
}

describe('Command Code balance cell', () => {
  it('shows credits and both remaining window percentages in the list', () => {
    const html = renderToStaticMarkup(<SiteBalanceCell site={site} />)
    expect(html).toContain('25.50 credits')
    expect(html).toContain('75.4%')
    expect(html).toContain('68.8%')
    expect(html).toContain('tabindex="0"')
  })
  it('shows plan, expiry, reset time, and full credit amounts in the details', () => {
    const html = renderToStaticMarkup(<SiteBalanceDetailsContent site={site} />)
    expect(html).toContain('GOAT')
    expect(html).toContain('3.77 credits / 5.00 credits')
    expect(html).toContain('table.quotaDetails.expiresAt')
    expect(html).toContain('2026')
  })
  it('makes a failed probe visible while retaining previously fetched quota', () => {
    const failed = { ...site, quota_probe: { ...site.quota_probe, status: 'error' } }
    expect(renderToStaticMarkup(<SiteBalanceCell site={failed} />)).toContain('table.quotaDetails.probeFailed')
    expect(renderToStaticMarkup(<SiteBalanceDetailsContent site={failed} />)).toContain('25.50 credits')
  })
  it('does not invent percentages when the window limit is unknown', () => {
    const missing = { ...site, quota_probe: { probe_type: 'command_code', entries: [{ label: 'weekly', remaining: 3, unit: 'credits' }] } }
    const html = renderToStaticMarkup(<SiteBalanceCell site={missing} />)
    expect(html).not.toContain('%')
  })
})
