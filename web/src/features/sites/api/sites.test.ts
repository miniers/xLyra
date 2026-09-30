import { afterEach, describe, expect, it, vi } from 'vitest'
import { createSite, updateSite } from './sites'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('updateSite', () => {
  it('sends an empty request_headers array when all custom headers are removed', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ site: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await updateSite('site-1', {
      name: 'Example',
      slug: 'example',
      siteType: 'openai',
      baseUrl: 'https://example.com',
      enabled: true,
      routingPriority: 1,
      requestHeaders: [],
    })

    const [, request] = fetchMock.mock.calls[0]
    expect(JSON.parse(request.body)).toMatchObject({ request_headers: [] })
  })

  it('sends the site concurrency limit and clear flag in the gateway payload', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ site: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await updateSite('site-1', {
      name: 'Example',
      slug: 'example',
      siteType: 'openai',
      baseUrl: 'https://example.com',
      enabled: true,
      routingPriority: 1,
      gateway: {
        maxConcurrency: 4,
        clearMaxConcurrency: false,
      },
    })

    const [, request] = fetchMock.mock.calls[0]
    expect(JSON.parse(request.body).gateway).toMatchObject({
      max_concurrency: 4,
      clear_max_concurrency: false,
    })
  })

  it('includes rpm_limit, rpd_limit, and reset_timezone when creating site with api_keys', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ site: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await createSite({
      name: 'Example',
      slug: 'example',
      siteType: 'openai',
      baseUrl: 'https://example.com',
      enabled: true,
      routingPriority: 1,
      apiKeys: [
        {
          name: 'Primary Key',
          apiKey: 'sk-test',
          routingPriority: 1,
          rpmLimit: 60,
          rpdLimit: 1500,
          resetTimezone: 'America/Los_Angeles',
        },
      ],
    })

    const [, request] = fetchMock.mock.calls[0]
    expect(JSON.parse(request.body).api_keys).toEqual([
      {
        api_key: 'sk-test',
        name: 'Primary Key',
        routing_priority: 1,
        rpm_limit: 60,
        rpd_limit: 1500,
        reset_timezone: 'America/Los_Angeles',
      },
    ])
  })
})

