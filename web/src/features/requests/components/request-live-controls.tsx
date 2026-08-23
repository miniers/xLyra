import { Ban, LoaderCircle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import type { RequestLogItem } from '@/features/requests/api/requests'

type RequestLiveControlsProps = {
  item: Pick<RequestLogItem, 'request_id' | 'is_live' | 'can_cancel'>
  pending?: boolean
  onCancel: (requestID: string) => void
  compact?: boolean
}

export function RequestLiveControls({ item, pending = false, onCancel, compact = false }: RequestLiveControlsProps) {
  const { t } = useTranslation('requests')
  const canCancel = item.is_live === true && item.can_cancel === true
  if (!canCancel) return null

  const cancelLabel = t('actions.cancel')

  return (
    <div className="flex items-center justify-end gap-1.5">
      <Button
        type="button"
        variant="destructive"
        size={compact ? 'icon' : 'sm'}
        className={compact ? 'size-8' : undefined}
        aria-label={cancelLabel}
        title={cancelLabel}
        disabled={pending}
        onClick={(event) => {
          event.stopPropagation()
          if (!pending) onCancel(item.request_id)
        }}
      >
        {pending ? <LoaderCircle className="size-3.5 animate-spin" /> : <Ban className="size-3.5" />}
        {!compact ? cancelLabel : null}
      </Button>
    </div>
  )
}
