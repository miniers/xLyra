import { describe, expect, it } from 'vitest'
import { routeChannelRowsFromMatrix, routeModelCooldown, routeSiteModelGroupsFromChannels, routeSiteModelStatus } from '@/features/routes/lib/route-channels'
import type { RouteCandidateItem, RouteCooldown } from '@/features/routes/api/routes'
import type { CanonicalModelMatrixRow, Site, SiteAPIKey } from '@/features/sites/api/sites'
import type { RouteChannelRow } from './types'

describe('routeChannelRowsFromMatrix', () => {
  it('applies the credential cost multiplier to token and per-request prices', () => {
    const site: Site = {
      id: 'site-1',
      name: 'Primary',
      slug: 'primary',
      site_type: 'openai',
      base_url: 'https://example.test',
      status: 'active',
      enabled: true,
      routing_priority: 1,
      supports_multiple_api_keys: true,
      supports_api_key_cost_multiplier: true,
      meta: {},
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }
    const apiKey: SiteAPIKey = {
      id: 'credential-1',
      name: 'Primary key',
      routing_priority: 5,
      upstream_cost_multiplier: 1.5,
      group: 'default',
      key: 'sk-***',
      status: 'active',
      enabled: true,
      models: ['gpt-test'],
    }
    const matrixRow: CanonicalModelMatrixRow = {
      site_id: site.id,
      site_name: site.name,
      site_slug: site.slug,
      site_type: site.site_type,
      site_enabled: true,
      site_model_id: 'site-model-1',
      upstream_model_name: 'gpt-test',
      display_name: 'GPT Test',
      model_status: 'active',
      canonical_match_source: 'manual',
      canonical_match_confidence: 100,
      api_key_count: 1,
      available_api_key_count: 1,
      pricing: [{
        group_name: 'default',
        currency: 'USD',
        input_value: 2,
        output_value: 4,
        per_request_value: 0.2,
        available: true,
      }],
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }

    const rows = routeChannelRowsFromMatrix([matrixRow], [site], { [site.id]: [apiKey] }, undefined, [], [])

    expect(rows).toHaveLength(1)
    expect(rows[0]?.pricing).toMatchObject({
      base_input_value: 2,
      base_output_value: 4,
      base_per_request_value: 0.2,
      input_value: 3,
      output_value: 6,
      upstream_cost_multiplier: 1.5,
    })
    expect(rows[0]?.pricing?.per_request_value).toBeCloseTo(0.3)

    site.supports_api_key_cost_multiplier = false
    const fixedMultiplierRows = routeChannelRowsFromMatrix(
      [matrixRow],
      [site],
      { [site.id]: [apiKey] },
      undefined,
      [],
      [],
    )
    expect(fixedMultiplierRows[0]?.apiKeyUpstreamCostMultiplier).toBeUndefined()
    expect(fixedMultiplierRows[0]?.pricing).toMatchObject({
      input_value: 2,
      output_value: 4,
      per_request_value: 0.2,
      upstream_cost_multiplier: 1,
    })
  })

  it('keeps a manually added compatible site model visible without a per-key snapshot', () => {
    const site: Site = {
      id: 'site-compat',
      name: 'Compatible relay',
      slug: 'compatible-relay',
      site_type: 'openai_compatible',
      base_url: 'https://example.test',
      status: 'active',
      enabled: true,
      routing_priority: 1,
      supports_multiple_api_keys: true,
      meta: {},
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }
    const matrixRow: CanonicalModelMatrixRow = {
      site_id: site.id,
      site_name: site.name,
      site_slug: site.slug,
      site_type: site.site_type,
      site_enabled: true,
      site_model_id: 'manual-site-model',
      upstream_model_name: 'custom-model',
      display_name: 'Custom Model',
      model_status: 'active',
      canonical_match_source: 'manual',
      canonical_match_confidence: 100,
      api_key_count: 1,
      available_api_key_count: 1,
      pricing: [],
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    }

    const rows = routeChannelRowsFromMatrix([matrixRow], [site], { [site.id]: [] }, undefined, [], [])

    expect(rows).toHaveLength(1)
    expect(rows[0]).toMatchObject({
      kind: 'site_model',
      siteId: site.id,
      siteModelId: matrixRow.site_model_id,
      upstreamName: matrixRow.upstream_model_name,
      routable: true,
    })
  })
})

describe('routeModelCooldown', () => {
  const cooldowns: RouteCooldown[] = [
    {
      id: 'site-model-cooldown',
      site_id: 'site-1',
      site_model_id: 'model-1',
      site_credential_id: null,
      scope: 'model',
      source: 'gateway',
      reason: 'upstream timeout',
      active_until: '2026-01-01T01:00:00Z',
      remaining_seconds: 1800,
      metadata: null,
      created_at: '2026-01-01T00:30:00Z',
      updated_at: '2026-01-01T00:30:00Z',
    },
    {
      id: 'credential-cooldown',
      site_id: 'site-1',
      site_model_id: null,
      site_credential_id: 'credential-1',
      scope: 'credential',
      source: 'gateway',
      reason: 'upstream 429',
      active_until: '2026-01-01T01:00:00Z',
      remaining_seconds: 1800,
      metadata: null,
      created_at: '2026-01-01T00:30:00Z',
      updated_at: '2026-01-01T00:30:00Z',
    },
  ]

  it('returns site and site-model cooldowns but ignores credential-only cooldowns', () => {
    expect(routeModelCooldown(cooldowns, 'site-1', 'model-1')?.id).toBe('site-model-cooldown')
    expect(routeModelCooldown(cooldowns, 'site-1', 'model-2')).toBeUndefined()
    expect(routeModelCooldown(cooldowns, 'site-2', 'model-1')?.id).toBe('site-model-cooldown')
  })
})

describe('routeSiteModelGroupsFromChannels', () => {
  function candidate(siteModelId = 'site-model-1'): RouteCandidateItem {
    return {
      rank: 1,
      score: 90,
      site: {
        id: 'site-1',
        name: 'Primary',
        slug: 'primary',
        site_type: 'openai',
        base_url: 'https://example.test',
        routing_priority: 1,
      },
      model: {
        site_model_id: siteModelId,
        upstream_model_name: 'gpt-test',
        display_name: 'GPT Test',
        canonical_match_source: 'manual',
        canonical_match_confidence: 100,
      },
      health: {
        status: 'healthy',
        consecutive_failures: 0,
      },
      availability: {
        available_api_key_count: 1,
        total_api_key_count: 1,
      },
      credential: {
        routing_priority: 1,
        upstream_cost_multiplier: 1,
      },
      pricing: {},
    }
  }

  function channel(overrides: Partial<RouteChannelRow> = {}): RouteChannelRow {
    return {
      id: 'api-key:site-1:site-model-1:key-1:model',
      kind: 'api_key',
      siteId: 'site-1',
      siteName: 'Primary',
      siteType: 'openai',
      siteEnabled: true,
      siteModelId: 'site-model-1',
      siteModelEnabled: true,
      upstreamName: 'gpt-test',
      apiKeyId: 'key-1',
      apiKeyName: 'Primary key',
      apiKeyEnabled: true,
      apiKeyModelEnabled: true,
      enabled: true,
      routable: true,
      ...overrides,
    }
  }

  it('deduplicates candidate and exploration data across multiple API keys', () => {
    const primary = candidate()
    const groups = routeSiteModelGroupsFromChannels([
      channel({ candidate: primary, current: true, locked: true }),
      channel({
        id: 'api-key:site-1:site-model-1:key-2:model',
        apiKeyId: 'key-2',
        apiKeyName: 'Backup key',
        apiKeyEnabled: false,
        enabled: false,
        routable: false,
        candidate: primary,
      }),
    ], [primary])

    expect(groups).toHaveLength(1)
    expect(groups[0]).toMatchObject({
      siteModelId: 'site-model-1',
      current: true,
      locked: true,
      enabledChannelCount: 1,
      routableChannelCount: 1,
      candidate: primary,
    })
    expect(groups[0]?.channels).toHaveLength(2)
  })

  it('keeps candidate-only site models available to the unified table', () => {
    const item = candidate('candidate-only')
    const groups = routeSiteModelGroupsFromChannels([], [item])

    expect(groups).toHaveLength(1)
    expect(groups[0]).toMatchObject({
      siteModelId: 'candidate-only',
      siteId: 'site-1',
      siteName: 'Primary',
      upstreamName: 'gpt-test',
      routable: true,
      locked: false,
      candidate: item,
      channels: [],
    })
  })

  it('aggregates current, cooling, disabled, and locked channel states', () => {
    const groups = routeSiteModelGroupsFromChannels([
      channel({ current: true, cooling: true, routable: false, enabled: false, locked: true }),
      channel({
        id: 'api-key:site-1:site-model-1:key-2:model',
        apiKeyId: 'key-2',
        siteEnabled: false,
        siteModelEnabled: false,
        routable: false,
        enabled: false,
      }),
    ])

    expect(groups[0]).toMatchObject({
      current: true,
      cooling: true,
      routable: false,
      locked: true,
    })
    expect(routeSiteModelStatus(groups[0]!)).toMatchObject({ label: '冷却中' })
  })

  it('reports disabled status when no channel is cooling', () => {
    const groups = routeSiteModelGroupsFromChannels([
      channel({ siteEnabled: false, siteModelEnabled: false, routable: false, enabled: false }),
    ])

    expect(routeSiteModelStatus(groups[0]!)).toMatchObject({ label: '站点停用' })
  })
})
