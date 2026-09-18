import { describe, expect, it } from 'vitest'

import type { Site, SiteModel } from '@/features/sites/api/sites'
import { buildDisplayModelItems, compareSiteModels, isNVIDIAFreeModel, siteModelDisplayName } from '@/features/sites/lib/site-model-items'

function nvidiaModel(name: string, free: boolean): SiteModel {
  return {
    id: name,
    site_id: 'site-1',
    upstream_model_name: name,
    display_name: free ? `${name} [free]` : name,
    capabilities: { source: 'nvidia_catalog', free_endpoint: free },
    status: 'active',
    created_at: '2026-01-01T00:00:00Z',
    updated_at: '2026-01-01T00:00:00Z',
  }
}

const nvidiaSite = { site_type: 'nvidia' } as Site

describe('NVIDIA site model presentation', () => {
  it('marks free catalog models and sorts them before paid models', () => {
    const free = nvidiaModel('vendor/free', true)
    const paid = nvidiaModel('vendor/paid', false)

    expect(isNVIDIAFreeModel(free)).toBe(true)
    expect(siteModelDisplayName(free)).toBe('vendor/free [free]')
    expect([paid, free].sort(compareSiteModels).map((model) => model.id)).toEqual(['vendor/free', 'vendor/paid'])
  })

  it('keeps the raw upstream id for copying while showing the free marker', () => {
    const items = buildDisplayModelItems(nvidiaSite, [nvidiaModel('vendor/paid', false), nvidiaModel('vendor/free', true)], [])

    expect(items.map((item) => item.displayName)).toEqual(['vendor/free [free]', 'vendor/paid'])
    expect(items[0]?.copyName).toBe('vendor/free')
  })

  it('handles models with undefined or empty capabilities safely', () => {
    const modelWithoutCaps = {
      id: 'custom-model',
      site_id: 'site-1',
      upstream_model_name: 'custom-model',
      display_name: 'Custom Model',
      status: 'active',
      created_at: '2026-01-01T00:00:00Z',
      updated_at: '2026-01-01T00:00:00Z',
    } as unknown as SiteModel

    expect(isNVIDIAFreeModel(undefined)).toBe(false)
    expect(isNVIDIAFreeModel(modelWithoutCaps)).toBe(false)
    expect(siteModelDisplayName(modelWithoutCaps)).toBe('custom-model')
    expect(compareSiteModels(modelWithoutCaps, nvidiaModel('vendor/free', true))).toBe(1)
  })
})
