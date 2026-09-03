import { afterEach, describe, expect, it, vi } from 'vitest'
import { getChannelSplit } from './channel-split'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('channel split api', () => {
  it('serializes a bounded site range', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await getChannelSplit({ siteIds: ['site-1'], dateFrom: '2026-09-01', dateTo: '2026-09-03' })

    expect(fetchMock).toHaveBeenCalledOnce()
    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/requests/channel-split?site_id=site-1&date_from=2026-09-01&date_to=2026-09-03')
  })

  it('serializes all-time and OAuth targets', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }))
    vi.stubGlobal('fetch', fetchMock)

    await getChannelSplit({ siteIds: ['site-1'], oauthConnectionId: 'oauth-1', range: 'all' })

    expect(fetchMock.mock.calls[0]?.[0]).toBe('/api/v1/requests/channel-split?site_id=site-1&oauth_connection_id=oauth-1&range=all')
  })
})
