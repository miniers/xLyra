import { afterEach, describe, expect, it, vi } from 'vitest'
import { temporaryRouteLockExpiresAt, updateModelRoutingLock } from './routes'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('temporary route lock', () => {
  it('uses a ten-minute default expiry', () => {
    const now = new Date('2026-08-31T00:00:00.000Z')

    expect(temporaryRouteLockExpiresAt(now)).toBe('2026-08-31T00:10:00.000Z')
  })

  it('sends the selected fallback policy and expiry to the routing-lock API', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({ routing_lock: {} }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }))
    vi.stubGlobal('fetch', fetchMock)

    const expiresAt = temporaryRouteLockExpiresAt(new Date('2026-08-31T00:00:00.000Z'))
    await updateModelRoutingLock({
      modelId: 'model-1',
      siteModelId: 'site-model-1',
      expiresAt,
      policy: 'fallback',
    })

    const [url, request] = fetchMock.mock.calls[0] ?? []
    expect(url).toBe('/api/v1/models/model-1/routing-lock')
    expect(request?.method).toBe('PUT')
    expect(JSON.parse(request?.body)).toMatchObject({
      site_model_id: 'site-model-1',
      expires_at: expiresAt,
      policy: 'fallback',
    })
  })
})
