import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useSidebarStore } from './sidebar-store'

describe('useSidebarStore', () => {
  const store = new Map<string, string>()

  beforeEach(() => {
    store.clear()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => store.get(key) ?? null,
      setItem: (key: string, val: string) => store.set(key, val),
      removeItem: (key: string) => store.delete(key),
    })
    useSidebarStore.setState({ isCollapsed: false })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('initializes with isCollapsed false', () => {
    expect(useSidebarStore.getState().isCollapsed).toBe(false)
  })

  it('toggles collapsed state', () => {
    useSidebarStore.getState().toggleCollapsed()
    expect(useSidebarStore.getState().isCollapsed).toBe(true)

    useSidebarStore.getState().toggleCollapsed()
    expect(useSidebarStore.getState().isCollapsed).toBe(false)
  })

  it('sets collapsed state directly', () => {
    useSidebarStore.getState().setCollapsed(true)
    expect(useSidebarStore.getState().isCollapsed).toBe(true)

    useSidebarStore.getState().setCollapsed(false)
    expect(useSidebarStore.getState().isCollapsed).toBe(false)
  })
})
