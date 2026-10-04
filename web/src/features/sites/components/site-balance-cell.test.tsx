import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, describe, expect, it } from 'vitest'
import type { Site, SiteAPIKey } from '@/features/sites/api/sites'
import translations from '@/locales/zh/sites.json'
import { MobileSitesList } from './mobile-sites-list'
import { SiteBalanceCell, SiteBalanceDetailsContent } from './site-balance-cell'

const i18n = createInstance()

beforeAll(async () => {
  await i18n.init({ lng: 'zh', resources: { zh: { sites: translations } }, interpolation: { escapeValue: false } })
})

const commandCodeSite: Site = {
  id: 'cc',
  name: 'Command Code',
  slug: 'cc',
  site_type: 'command_code',
  base_url: 'https://example.invalid',
  status: 'active',
  enabled: true,
  routing_priority: 0,
  meta: {},
  created_at: '',
  updated_at: '',
  quota_probe: {
    probe_type: 'command_code',
    status: 'ok',
    plan: 'GOAT',
    expires_at: '2026-10-19T10:00:00Z',
    entries: [
      { label: 'balance', remaining: 25.5, unit: 'credits' },
      { label: 'five_hour', remaining: 3.77, limit: 5, unit: 'credits', reset_at: '2026-09-19T10:00:00Z' },
      { label: 'weekly', remaining: 27.5, limit: 40, unit: 'credits' },
    ],
  },
}

describe('Command Code balance cell', () => {
  it('shows credits and both remaining window percentages in the list', () => {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={commandCodeSite} />
      </I18nextProvider>,
    )
    expect(html).toContain('25.50 credits')
    expect(html).toContain('75.4%')
    expect(html).toContain('68.8%')
    expect(html).toContain('tabindex="0"')
  })

  it('shows plan, expiry, reset time, and full credit amounts in the details', () => {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={commandCodeSite} />
      </I18nextProvider>,
    )
    expect(html).toContain('GOAT')
    expect(html).toContain('3.77 credits / 5.00 credits')
    expect(html).toContain('到期时间')
    expect(html).toContain('2026')
  })

  it('makes a failed probe visible while retaining previously fetched quota', () => {
    const failed = { ...commandCodeSite, quota_probe: { ...commandCodeSite.quota_probe, status: 'error' } }
    expect(renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={failed} />
      </I18nextProvider>,
    )).toContain('额度探测失败')
    expect(renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={failed} />
      </I18nextProvider>,
    )).toContain('25.50 credits')
  })

  it('does not invent percentages when the window limit is unknown', () => {
    const missing = { ...commandCodeSite, quota_probe: { probe_type: 'command_code', entries: [{ label: 'weekly', remaining: 3, unit: 'credits' }] } }
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={missing} />
      </I18nextProvider>,
    )
    expect(html).not.toContain('%')
  })
})

const glmSite: Site = {
  id: 'glm-site',
  name: 'GLM',
  slug: 'glm',
  site_type: 'glm_code',
  base_url: 'https://open.bigmodel.cn/api/coding/paas/v4',
  status: 'active',
  enabled: true,
  routing_priority: 0,
  meta: {},
  created_at: '2026-09-19T00:00:00Z',
  updated_at: '2026-09-19T00:00:00Z',
  quota_probe: {
    probe_type: 'glm',
    unit: 'percent',
    entries: [
      { label: 'monthly', unit: 'percent', remaining: 0, reset_at: '2026-10-19T00:00:00Z' },
      { label: 'weekly', unit: 'percent', remaining: 73.5 },
      { label: 'five_hour', unit: 'percent', remaining: 85.7 },
    ],
  },
}

describe('GLM quota details', () => {
  it('renders separate quota rows in the shared tooltip and drawer content', () => {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={glmSite} />
      </I18nextProvider>,
    )
    const rows = [...markup.matchAll(/<div[^>]*><span[^>]*>([^<]+)<\/span><span[^>]*>([^<]+)<\/span><\/div>/g)]
      .map((match) => [match[1], match[2]])
    expect(rows).toEqual([
      ['5 小时额度', '剩余 85.7%'],
      ['周额度', '剩余 73.5%'],
      ['MCP 月度额度', expect.stringContaining('剩余 0% · ')],
    ])
  })

  it('includes all quota rows in the desktop tooltip description', () => {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={glmSite} />
      </I18nextProvider>,
    )
    expect(markup).toContain('role="tooltip"')
    expect(markup).toContain('5 小时额度 剩余 85.7%, 周额度 剩余 73.5%, MCP 月度额度 剩余 0%')
  })

  it('makes the mobile GLM balance a drawer trigger without requiring loaded API keys', () => {
    const noop = () => {}
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <MobileSitesList
          items={[glmSite]}
          modelsMap={{}}
          apiKeysMap={{}}
          validationSnapshots={{}}
          refreshingSiteIds={[]}
          togglingSiteId={null}
          deletingSiteId={null}
          updatedAtMode="absolute"
          now={Date.parse(glmSite.updated_at)}
          onUpdatedAtModeChange={noop}
          onRefresh={noop}
          onToggleEnabled={noop}
          onEdit={noop}
          onDelete={noop}
          onOpenModels={noop}
          onOpenAPIKeys={noop}
          onOpenGrokAccounts={noop}
          onOpenTest={noop}
          onOpenUsageSplit={noop}
          resolvedMode="light"
          siteTypes={[]}
        />
      </I18nextProvider>,
    )
    const buttons = markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? []
    const balance = buttons.find((button) => button.includes('85.7% / 73.5%'))
    expect(balance).toContain('aria-haspopup="dialog"')
  })
})

describe('DeepSeek balance details', () => {
  const site: Site = {
    id: 'deepseek-site',
    name: 'DeepSeek',
    slug: 'deepseek',
    site_type: 'deepseek',
    base_url: 'https://api.deepseek.com',
    status: 'active',
    enabled: true,
    routing_priority: 0,
    meta: {},
    created_at: '2026-09-19T00:00:00Z',
    updated_at: '2026-09-19T00:00:00Z',
    quota_probe: {
      probe_type: 'deepseek',
      unit: 'cny',
      entries: [
        { label: 'balance', unit: 'cny', remaining: -0.5, granted_balance: 0, topped_up_balance: -0.5 },
        { label: 'balance', unit: 'usd', remaining: 2.5, granted_balance: 1, topped_up_balance: 1.5 },
      ],
    },
  }

  it.each([false, true])('renders separate quota rows with API keys loaded: %s', (keysLoaded) => {
    const apiKeys: SiteAPIKey[] = keysLoaded ? [{
      id: 'deepseek-key',
      name: 'DeepSeek key',
      key: 'sk-***',
      routing_priority: 0,
      upstream_cost_multiplier: 1,
      status: 'active',
      enabled: true,
      models: [],
      quota_probe: { status: 'ok', kind: 'balance', entries: site.quota_probe?.entries },
    }] : []
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={site} apiKeys={apiKeys} />
      </I18nextProvider>,
    )
    const rows = [...markup.matchAll(/<div[^>]*><span[^>]*>([^<]+)<\/span><span[^>]*>([^<]+)<\/span><\/div>/g)]
      .map((match) => [match[1], match[2]])
    expect(rows).toEqual([
      ['账户余额', '-¥0.50'],
      ['赠送余额', '¥0.00'],
      ['充值余额', '-¥0.50'],
      ['账户余额', '$2.50'],
      ['赠送余额', '$1.00'],
      ['充值余额', '$1.50'],
    ])
  })

  it('includes all quota rows in the desktop tooltip description', () => {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={site} />
      </I18nextProvider>,
    )
    expect(markup).toContain('role="tooltip"')
    expect(markup).toContain('账户余额 -¥0.50, 赠送余额 ¥0.00, 充值余额 -¥0.50, 账户余额 $2.50')
  })

  it('makes the mobile DeepSeek balance a drawer trigger without requiring loaded API keys', () => {
    const noop = () => {}
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <MobileSitesList
          items={[site]}
          modelsMap={{}}
          apiKeysMap={{}}
          validationSnapshots={{}}
          refreshingSiteIds={[]}
          togglingSiteId={null}
          deletingSiteId={null}
          updatedAtMode="absolute"
          now={Date.parse(site.updated_at)}
          onUpdatedAtModeChange={noop}
          onRefresh={noop}
          onToggleEnabled={noop}
          onEdit={noop}
          onDelete={noop}
          onOpenModels={noop}
          onOpenAPIKeys={noop}
          onOpenGrokAccounts={noop}
          onOpenTest={noop}
          onOpenUsageSplit={noop}
          resolvedMode="light"
          siteTypes={[]}
        />
      </I18nextProvider>,
    )
    const buttons = markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? []
    const balance = buttons.find((button) => button.includes('-¥0.50 / $2.50'))
    expect(balance).toContain('aria-haspopup="dialog"')
  })
})

describe('Moonshot balance details', () => {
  const site: Site = {
    id: 'moonshot-site',
    name: 'Moonshot',
    slug: 'moonshot',
    site_type: 'moonshot',
    base_url: 'https://api.moonshot.cn',
    status: 'active',
    enabled: true,
    routing_priority: 0,
    meta: {},
    created_at: '2026-09-19T00:00:00Z',
    updated_at: '2026-09-19T00:00:00Z',
    quota_probe: {
      probe_type: 'moonshot',
      unit: 'cny',
      entries: [
        { label: 'balance', unit: 'cny', remaining: 0, cash_balance: -31.14918, voucher_balance: 0 },
      ],
    },
  }

  it.each([false, true])('renders separate quota rows with API keys loaded: %s', (keysLoaded) => {
    const apiKeys: SiteAPIKey[] = keysLoaded ? [{
      id: 'moonshot-key',
      name: 'Moonshot key',
      key: 'sk-***',
      routing_priority: 0,
      upstream_cost_multiplier: 1,
      status: 'active',
      enabled: true,
      models: [],
      quota_probe: { status: 'ok', kind: 'balance', entries: site.quota_probe?.entries },
    }] : []
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={site} apiKeys={apiKeys} />
      </I18nextProvider>,
    )
    const rows = [...markup.matchAll(/<div[^>]*><span[^>]*>([^<]+)<\/span><span[^>]*>([^<]+)<\/span><\/div>/g)]
      .map((match) => [match[1], match[2]])
    expect(rows).toEqual([
      ['账户余额', '¥0.00'],
      ['现金余额', '-¥31.15'],
      ['代金券余额', '¥0.00'],
    ])
  })

  it('includes all quota rows in the desktop tooltip description', () => {
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={site} />
      </I18nextProvider>,
    )
    expect(markup).toContain('role="tooltip"')
    expect(markup).toContain('账户余额 ¥0.00, 现金余额 -¥31.15, 代金券余额 ¥0.00')
  })

  it('makes the mobile Moonshot balance a drawer trigger without requiring loaded API keys', () => {
    const noop = () => {}
    const markup = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <MobileSitesList
          items={[site]}
          modelsMap={{}}
          apiKeysMap={{}}
          validationSnapshots={{}}
          refreshingSiteIds={[]}
          togglingSiteId={null}
          deletingSiteId={null}
          updatedAtMode="absolute"
          now={Date.parse(site.updated_at)}
          onUpdatedAtModeChange={noop}
          onRefresh={noop}
          onToggleEnabled={noop}
          onEdit={noop}
          onDelete={noop}
          onOpenModels={noop}
          onOpenAPIKeys={noop}
          onOpenGrokAccounts={noop}
          onOpenTest={noop}
          onOpenUsageSplit={noop}
          resolvedMode="light"
          siteTypes={[]}
        />
      </I18nextProvider>,
    )
    const buttons = markup.match(/<button\b[^>]*>[\s\S]*?<\/button>/g) ?? []
    const balance = buttons.find((button) => button.includes('¥0.00'))
    expect(balance).toContain('aria-haspopup="dialog"')
  })
})

const factorySite: Site = {
  id: 'factory-site',
  name: 'Factory Droid',
  slug: 'factory',
  site_type: 'factory',
  base_url: 'https://api.factory.ai',
  status: 'active',
  enabled: true,
  routing_priority: 0,
  meta: {},
  created_at: '',
  updated_at: '',
  quota_probe: {
    probe_type: 'factory',
    status: 'ok',
    plan: 'Pro',
    expires_at: '2026-11-01T00:00:00Z',
    limits: {
      standard: {
        rolling_5_hour: { percent_used: 12.5, reset_at: '2026-10-19T10:00:00Z' },
        weekly: { percent_used: 45 },
        monthly: { percent_used: 70 },
      },
      core: {
        available: true,
      },
    },
  },
}

describe('Factory balance cell', () => {
  it('shows plan and standard usage window percentages in the list', () => {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceCell site={factorySite} />
      </I18nextProvider>,
    )
    expect(html).toContain('Pro')
    expect(html).toContain('12.5%')
    expect(html).toContain('45%')
    expect(html).toContain('70%')
  })

  it('shows plan, expiry, windows, and core status in the details', () => {
    const html = renderToStaticMarkup(
      <I18nextProvider i18n={i18n}>
        <SiteBalanceDetailsContent site={factorySite} />
      </I18nextProvider>,
    )
    expect(html).toContain('Pro')
    expect(html).toContain('5 小时窗口')
    expect(html).toContain('12.5%')
    expect(html).toContain('Core 模型额度')
    expect(html).toContain('可用')
    expect(html).toContain('到期时间')
  })
})
