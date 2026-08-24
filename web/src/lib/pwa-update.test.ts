import { afterEach, describe, expect, it, vi } from 'vitest'
import { updateServiceWorker } from './pwa-update'

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('updateServiceWorker', () => {
  it('updates the provided registration', async () => {
    const update = vi.fn().mockResolvedValue(undefined)

    await updateServiceWorker({ update } as unknown as ServiceWorkerRegistration)

    expect(update).toHaveBeenCalledOnce()
  })

  it('gets and updates the current registration when none is provided', async () => {
    const update = vi.fn().mockResolvedValue(undefined)
    const getRegistration = vi.fn().mockResolvedValue({ update })
    vi.stubGlobal('navigator', { serviceWorker: { getRegistration } })

    await updateServiceWorker()

    expect(getRegistration).toHaveBeenCalledOnce()
    expect(update).toHaveBeenCalledOnce()
  })

  it('reports when service workers are unavailable', async () => {
    vi.stubGlobal('navigator', {})

    await expect(updateServiceWorker()).rejects.toThrow('Service Worker is not supported')
  })
})
