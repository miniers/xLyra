import { useState } from 'react'
import { NavLink, useLocation } from 'react-router-dom'
import * as PopoverPrimitive from '@radix-ui/react-popover'
import { PanelLeftOpen } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { cn } from '@/lib/utils'
import type { AppNavItem, AppNavSection } from '@/lib/navigation'
import { AppLogo } from '@/components/common/app-logo'
import { UserAccountMenu } from '@/components/layout/user-account-menu'
import { Button } from '@/components/ui/button'

type CollapsedSidebarProps = {
  className?: string
  navSections: AppNavSection[]
  onNavigate?: () => void
  onExpand: () => void
}

export function CollapsedSidebar({
  className,
  navSections,
  onNavigate,
  onExpand,
}: CollapsedSidebarProps) {
  const { t } = useTranslation('components')

  return (
    <div
      className={cn(
        'glass-panel-strong flex h-full min-h-0 flex-col overflow-hidden rounded-[18px]',
        className,
      )}
    >
      <div className="flex flex-col items-center gap-2.5 border-b border-[hsl(var(--divider))] py-4 px-2">
        <AppLogo className="size-9 shrink-0 rounded-xl shadow-[0_14px_32px_rgba(0,0,0,0.12)]" decorative />
        <Button
          variant="ghost"
          size="icon"
          className="size-8 shrink-0 text-muted-soft hover:text-foreground hover:bg-[hsl(var(--surface-subtle))]"
          onClick={onExpand}
          title={t('navigation.expandSidebar')}
          aria-label={t('navigation.expandSidebar')}
        >
          <PanelLeftOpen className="size-4" />
        </Button>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-4">
        <div className="space-y-4">
          {navSections.map((section, idx) => (
            <div key={section.key} className="space-y-1">
              {idx > 0 && <div className="mx-auto my-2 h-px w-6 bg-[hsl(var(--divider))]" />}
              <nav className="flex flex-col items-center gap-1">
                {section.items.map((item) => {
                  const hasChildren = item.children && item.children.length > 0
                  if (hasChildren) {
                    return (
                      <CollapsedNavItemWithChildren
                        key={item.to}
                        item={item}
                        onNavigate={onNavigate}
                      />
                    )
                  }

                  const Icon = item.icon
                  return (
                    <NavLink
                      key={item.to}
                      to={item.to}
                      onClick={onNavigate}
                      title={item.label}
                      aria-label={item.label}
                      className={({ isActive }) =>
                        cn(
                          'relative flex size-10 items-center justify-center rounded-xl transition-all duration-150 group',
                          isActive
                            ? 'bg-[hsl(var(--surface-selected))] text-foreground shadow-[0_2px_8px_rgba(0,0,0,0.12)]'
                            : 'text-muted-soft hover:bg-[hsl(var(--surface-subtle))] hover:text-foreground',
                        )
                      }
                    >
                      {({ isActive }) => (
                        <>
                          {isActive && (
                            <div className="absolute left-0 top-1/2 -translate-y-1/2 w-1 h-5 rounded-r-md bg-[hsl(var(--primary))] shadow-[0_0_12px_hsl(var(--primary)/0.6)]" />
                          )}
                          <Icon className="h-5 w-5 shrink-0 transition-transform duration-200 group-hover:scale-110" />
                        </>
                      )}
                    </NavLink>
                  )
                })}
              </nav>
            </div>
          ))}
        </div>
      </div>

      <div className="px-2 py-4 flex justify-center">
        <UserAccountMenu
          mode="avatar"
          onNavigate={onNavigate}
          side="right"
        />
      </div>
    </div>
  )
}

function CollapsedNavItemWithChildren({
  item,
  onNavigate,
}: {
  item: AppNavItem
  onNavigate?: () => void
}) {
  const location = useLocation()
  const [open, setOpen] = useState(false)
  const Icon = item.icon
  const isChildActive = item.children?.some(
    (child) => location.pathname === child.to || location.pathname.startsWith(`${child.to}/`),
  )

  return (
    <PopoverPrimitive.Root open={open} onOpenChange={setOpen}>
      <PopoverPrimitive.Trigger asChild>
        <button
          type="button"
          title={item.label}
          aria-label={item.label}
          className={cn(
            'relative flex size-10 items-center justify-center rounded-xl transition-all duration-150 group',
            isChildActive || open
              ? 'bg-[hsl(var(--surface-selected))] text-foreground shadow-[0_2px_8px_rgba(0,0,0,0.12)]'
              : 'text-muted-soft hover:bg-[hsl(var(--surface-subtle))] hover:text-foreground',
          )}
        >
          {isChildActive && (
            <div className="absolute left-0 top-1/2 -translate-y-1/2 w-1 h-5 rounded-r-md bg-[hsl(var(--primary))] shadow-[0_0_12px_hsl(var(--primary)/0.6)]" />
          )}
          <Icon className="h-5 w-5 shrink-0 transition-transform duration-200 group-hover:scale-110" />
        </button>
      </PopoverPrimitive.Trigger>
      <PopoverPrimitive.Portal>
        <PopoverPrimitive.Content
          side="right"
          align="start"
          sideOffset={14}
          className="z-[120] min-w-[200px] overflow-hidden rounded-xl border border-[hsl(var(--glass-border))] bg-[hsl(var(--dialog-surface))] p-1 shadow-[var(--shadow-dialog)] backdrop-blur-xl animate-in fade-in-0 zoom-in-95 duration-150"
        >
          <div className="px-3 py-2 border-b border-[hsl(var(--divider))] text-xs font-semibold text-foreground">
            {item.label}
          </div>
          <div className="py-1">
            {item.children?.map((child) => {
              const ChildIcon = child.icon
              return (
                <NavLink
                  key={child.to}
                  to={child.to}
                  onClick={() => {
                    setOpen(false)
                    onNavigate?.()
                  }}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-2.5 rounded-lg px-3 py-2 text-xs font-medium transition-all duration-150 relative group',
                      isActive
                        ? 'bg-[hsl(var(--surface-selected))] text-foreground'
                        : 'text-muted-soft hover:bg-[hsl(var(--surface-subtle))] hover:text-foreground',
                    )
                  }
                >
                  {({ isActive }) => (
                    <>
                      {isActive && (
                        <div className="absolute left-0 top-1/2 -translate-y-1/2 w-1 h-3.5 rounded-r-md bg-[hsl(var(--primary))] shadow-[0_0_12px_hsl(var(--primary)/0.6)]" />
                      )}
                      <ChildIcon className="h-3.5 w-3.5 shrink-0 transition-transform duration-200 group-hover:scale-110" />
                      <span className="truncate">{child.label}</span>
                    </>
                  )}
                </NavLink>
              )
            })}
          </div>
        </PopoverPrimitive.Content>
      </PopoverPrimitive.Portal>
    </PopoverPrimitive.Root>
  )
}
