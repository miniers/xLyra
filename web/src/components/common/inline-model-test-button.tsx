import { useTranslation } from 'react-i18next'
import { CheckCircle2, FlaskConical, LoaderCircle, RotateCw, XCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

export type InlineModelTestResult = {
  ok: boolean
  latencyMs?: number | null
  error?: string | null
}

export type InlineModelTestButtonProps = {
  isTesting: boolean
  result?: InlineModelTestResult | null
  onTest: () => void
  disabled?: boolean
  disabledTooltip?: string
  className?: string
}

export function InlineModelTestButton({
  isTesting,
  result,
  onTest,
  disabled = false,
  disabledTooltip,
  className,
}: InlineModelTestButtonProps) {
  const { t } = useTranslation('components')

  if (isTesting) {
    return (
      <div
        className={cn('inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs text-muted-soft bg-[hsl(var(--surface-subtle))]', className)}
        title={t('modelsDraw.testing')}
      >
        <LoaderCircle className="h-3.5 w-3.5 animate-spin text-[hsl(var(--primary))]" />
        <span className="hidden sm:inline text-[11px]">{t('modelsDraw.testing')}</span>
      </div>
    )
  }

  if (result) {
    if (result.ok) {
      return (
        <div className={cn('inline-flex items-center gap-1', className)}>
          <span
            className="inline-flex items-center gap-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2 py-1 text-xs font-medium text-emerald-600 dark:text-emerald-400"
            title={result.latencyMs != null ? `${t('modelsDraw.testSuccess')} (${result.latencyMs}ms)` : t('modelsDraw.testSuccess')}
          >
            <CheckCircle2 className="h-3 w-3 shrink-0" />
            <span className="tabular-nums text-[11px]">{result.latencyMs != null ? `${result.latencyMs}ms` : t('modelsDraw.testSuccess')}</span>
          </span>
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="h-6 w-6 text-muted-soft hover:text-foreground"
            title={t('modelsDraw.testModel')}
            onClick={(e) => {
              e.stopPropagation()
              onTest()
            }}
          >
            <RotateCw className="h-3 w-3" />
          </Button>
        </div>
      )
    }

    return (
      <div className={cn('inline-flex items-center gap-1', className)}>
        <span
          className="inline-flex max-w-[140px] items-center gap-1 truncate rounded-md border border-red-500/30 bg-red-500/10 px-2 py-1 text-xs font-medium text-red-600 dark:text-red-400"
          title={result.error || t('modelsDraw.testFailed')}
        >
          <XCircle className="h-3 w-3 shrink-0" />
          <span className="truncate text-[11px]">{result.error || t('modelsDraw.testFailed')}</span>
        </span>
        <Button
          type="button"
          size="icon"
          variant="ghost"
          className="h-6 w-6 text-muted-soft hover:text-foreground"
          title={t('modelsDraw.testModel')}
          onClick={(e) => {
            e.stopPropagation()
            onTest()
          }}
        >
          <RotateCw className="h-3 w-3" />
        </Button>
      </div>
    )
  }

  const tooltip = disabled
    ? disabledTooltip || t('modelsDraw.noSiteAvailable')
    : t('modelsDraw.testModel')

  return (
    <Button
      type="button"
      size="icon"
      variant="ghost"
      disabled={disabled}
      className={cn(
        'h-7 w-7 text-muted-soft transition-colors hover:bg-[hsl(var(--surface-subtle))] hover:text-foreground disabled:opacity-40',
        className,
      )}
      title={tooltip}
      aria-label={tooltip}
      onClick={(e) => {
        e.stopPropagation()
        onTest()
      }}
    >
      <FlaskConical className="h-3.5 w-3.5" />
    </Button>
  )
}
