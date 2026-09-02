import { Input } from '@/components/ui/input'
import type { APIKeyFormDraft } from '@/features/sites/components/site-api-key-form-data'

export function SiteAPIKeyFormFields({
  draft,
  onChange,
  showAPIKey,
  showCostMultiplier,
  showRoutingConfig = true,
  t,
}: {
  draft: APIKeyFormDraft
  onChange: (value: APIKeyFormDraft) => void
  showAPIKey: boolean
  showCostMultiplier: boolean
  showRoutingConfig?: boolean
  t: (key: string) => string
}) {
  return (
    <div className="space-y-4">
      {showRoutingConfig ? (
        <>
          <label className="block space-y-2">
            <span className="block text-sm font-medium">{t('apiKeys.name')}</span>
            <Input
              value={draft.name}
              maxLength={100}
              onChange={(event) => onChange({ ...draft, name: event.target.value })}
            />
          </label>
          <label className="block space-y-2">
            <span className="block text-sm font-medium">
              {t('apiKeys.routingPriority')}
            </span>
            <Input
              type="number"
              min="1"
              max="5"
              step="0.1"
              value={draft.routingPriority}
              onChange={(event) =>
                onChange({ ...draft, routingPriority: event.target.value })
              }
            />
          </label>
        </>
      ) : null}
      {showCostMultiplier ? (
        <>
          <label className="block space-y-2">
            <span className="block text-sm font-medium">
              {t('apiKeys.costMultiplier')}
            </span>
            <Input
              type="number"
              min="0.01"
              max="100"
              step="0.0001"
              value={draft.upstreamCostMultiplier}
              disabled={draft.upstreamCostMultiplierSource === 'auto'}
              onChange={(event) =>
                onChange({
                  ...draft,
                  upstreamCostMultiplier: event.target.value,
                })
              }
            />
          </label>
          <label className="block space-y-2">
            <span className="block text-sm font-medium">
              {t('apiKeys.multiplierSource')}
            </span>
            <select
              className="flex h-9 w-full rounded-md border border-input bg-background px-3 py-1 text-sm text-foreground shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
              value={draft.upstreamCostMultiplierSource}
              onChange={(event) =>
                onChange({
                  ...draft,
                  upstreamCostMultiplierSource: event.target.value as 'manual' | 'auto',
                })
              }
            >
              <option value="manual">{t('apiKeys.multiplierManual')}</option>
              <option value="auto">{t('apiKeys.multiplierAuto')}</option>
            </select>
          </label>
        </>
      ) : null}
      {showAPIKey ? (
        <label className="block space-y-2">
          <span className="block text-sm font-medium">ApiKey</span>
          <Input
            value={draft.apiKey}
            autoComplete="off"
            onChange={(event) =>
              onChange({ ...draft, apiKey: event.target.value })
            }
          />
        </label>
      ) : null}
    </div>
  )
}
