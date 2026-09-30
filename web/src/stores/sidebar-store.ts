import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

type SidebarStore = {
  isCollapsed: boolean
  toggleCollapsed: () => void
  setCollapsed: (collapsed: boolean) => void
}

const memoryStorage = {
  getItem: () => null,
  setItem: () => {},
  removeItem: () => {},
}

export const useSidebarStore = create<SidebarStore>()(
  persist(
    (set) => ({
      isCollapsed: false,
      toggleCollapsed: () => set((state) => ({ isCollapsed: !state.isCollapsed })),
      setCollapsed: (collapsed) => set({ isCollapsed: collapsed }),
    }),
    {
      name: 'xlyra:sidebar-collapsed',
      storage: createJSONStorage(() =>
        typeof window !== 'undefined' && window.localStorage ? window.localStorage : memoryStorage,
      ),
    },
  ),
)
