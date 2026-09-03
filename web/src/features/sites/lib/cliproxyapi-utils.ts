import type {
  CLIProxyAPIAccount,
  CLIProxyAPIQuotaObservation,
  CLIProxyAPIRecentRequestBucket,
} from '@/features/sites/api/sites'

export type CLIProxyAPIQuotaWindow = {
  key: string
  label: string
  kind: 'primary' | 'secondary' | 'additional' | 'code_review' | 'other'
  namespace?: string
  usedPercent?: number
  remainingPercent?: number
  windowMinutes?: number
  resetAt?: string
  resetAfterSeconds?: number
  status?: string
  limitReached?: boolean
  observedAt?: string
  signals: Record<string, string>
}

export type CLIProxyAPIRecentRequestPoint = CLIProxyAPIRecentRequestBucket & {
  total: number
}

export type CLIProxyAPIQuotaTranslator = (key: string, options?: Record<string, unknown>) => string

const CODEX_PREFIX = 'x-codex-'
const CLAUDE_PREFIX = 'anthropic-ratelimit-unified-'
const RECENT_REQUEST_BUCKET_LIMIT = 20

export function buildQuotaWindows(provider: string, quota?: CLIProxyAPIQuotaObservation): CLIProxyAPIQuotaWindow[] {
  const normalized = provider.trim().toLowerCase()
  if (normalized === 'codex') return buildCodexQuotaWindows(quota)
  if (normalized === 'claude' || normalized === 'claude_code') return buildClaudeQuotaWindows(quota)
  return []
}

export function buildCodexQuotaWindows(quota?: CLIProxyAPIQuotaObservation): CLIProxyAPIQuotaWindow[] {
  if (!quota) return []

  const groups = new Map<string, {
    kind: 'primary' | 'secondary'
    namespace: string
    signals: Record<string, string>
  }>()

  for (const [key, value] of Object.entries(quota.signals ?? {})) {
    const parsed = splitCodexSignal(key)
    if (!parsed) continue
    const groupKey = `${parsed.namespace}:${parsed.kind}`
    const group = groups.get(groupKey) ?? {
      kind: parsed.kind,
      namespace: parsed.namespace,
      signals: {},
    }
    group.signals[key] = value
    groups.set(groupKey, group)
  }

  const accountStatusSignals = Object.fromEntries(
    Object.entries(quota.signals ?? {}).filter(([key]) => {
      const normalized = key.trim().toLowerCase()
      return normalized === 'x-codex-allowed' || normalized === 'x-codex-limit-reached'
    }),
  )
  if (Object.keys(accountStatusSignals).length > 0) {
    if (groups.size === 0) {
      groups.set('account:primary', { kind: 'primary', namespace: '', signals: accountStatusSignals })
    } else {
      for (const group of groups.values()) {
        for (const [key, value] of Object.entries(accountStatusSignals)) {
          if (!(key in group.signals)) group.signals[key] = value
        }
      }
    }
  }

  return Array.from(groups.entries()).map<CLIProxyAPIQuotaWindow>(([key, group]) => {
    const kind: CLIProxyAPIQuotaWindow['kind'] = group.namespace.startsWith('additional-')
      ? 'additional'
      : group.namespace.startsWith('code-review')
        ? 'code_review'
        : group.namespace
          ? 'other'
          : group.kind
    const windowMinutes = parsePositiveNumber(findSignal(group.signals, 'window-minutes'))
    const usedPercent = parsePercent(findSignal(group.signals, 'used-percent'))
    const limitReached = parseBoolean(findSignal(group.signals, 'limit-reached'))
    const allowed = parseBoolean(findSignal(group.signals, 'allowed'))
    return {
      key,
      label: codexWindowLabel(group.kind, group.namespace, findSignal(group.signals, 'limit-name')),
      kind,
      namespace: group.namespace || undefined,
      usedPercent,
      remainingPercent: usedPercent == null ? undefined : 100 - usedPercent,
      windowMinutes,
      resetAt: findSignal(group.signals, 'reset-at'),
      resetAfterSeconds: parseNonNegativeNumber(findSignal(group.signals, 'reset-after-seconds')),
      status: limitReached === true ? 'limited' : allowed === false ? 'not_allowed' : allowed === true ? 'allowed' : undefined,
      limitReached,
      observedAt: quota.observed_at,
      signals: group.signals,
    }
  }).sort(compareQuotaWindows)
}

export function buildClaudeQuotaWindows(quota?: CLIProxyAPIQuotaObservation): CLIProxyAPIQuotaWindow[] {
  if (!quota) return []

  const groups = new Map<string, Record<string, string>>()
  const accountSignals: Record<string, string> = {}
  for (const [key, value] of Object.entries(quota.signals ?? {})) {
    const normalized = key.trim().toLowerCase()
    if (normalized === 'retry-after') {
      accountSignals[key] = value
      continue
    }
    if (!normalized.startsWith(CLAUDE_PREFIX)) continue
    const rest = normalized.slice(CLAUDE_PREFIX.length)
    const separator = rest.indexOf('-')
    const window = separator < 0 ? 'unified' : rest.slice(0, separator)
    const group = groups.get(window) ?? {}
    group[key] = value
    groups.set(window, group)
  }
  if (Object.keys(accountSignals).length > 0) {
    if (groups.size === 0) {
      groups.set('unified', accountSignals)
    } else {
      for (const group of groups.values()) {
        for (const [key, value] of Object.entries(accountSignals)) {
          if (!(key in group)) group[key] = value
        }
      }
    }
  }

  return Array.from(groups.entries()).map<CLIProxyAPIQuotaWindow>(([window, signals]) => {
    const utilization = parseUtilization(findSignalBySuffix(signals, 'utilization'))
    const reset = findSignalBySuffix(signals, 'reset')
    const retryAfter = findSignal(signals, 'retry-after')
    const status = findSignalBySuffix(signals, 'status')
    const limitReached = status ? /(?:limit|exceed|block|over)/i.test(status) : undefined
    return {
      key: `claude:${window}`,
      label: window === 'unified' ? 'Unified' : window,
      kind: 'other',
      namespace: window,
      usedPercent: utilization,
      remainingPercent: utilization == null ? undefined : 100 - utilization,
      windowMinutes: parseWindowLabelMinutes(window),
      resetAt: reset,
      resetAfterSeconds: parseNonNegativeNumber(retryAfter),
      status,
      limitReached,
      observedAt: quota.observed_at,
      signals,
    }
  }).sort(compareQuotaWindows)
}

export function buildRecentRequestSeries(account: Pick<CLIProxyAPIAccount, 'recent_requests'>): CLIProxyAPIRecentRequestPoint[] {
  if (!Array.isArray(account.recent_requests)) return []
  return account.recent_requests.slice(-RECENT_REQUEST_BUCKET_LIMIT).map((bucket) => {
    const success = nonNegativeNumber(bucket.success)
    const failed = nonNegativeNumber(bucket.failed)
    return {
      time: bucket.time || '-',
      success,
      failed,
      total: success + failed,
    }
  })
}

export function maskCLIProxyAPIEmail(value: string | undefined) {
  const email = value?.trim() ?? ''
  const at = email.lastIndexOf('@')
  if (at <= 0 || at === email.length - 1) return '-'

  const local = email.slice(0, at)
  const domain = email.slice(at + 1)
  if (local.length === 1) return `${local}***@${domain}`
  return `${local[0]}***${local[local.length - 1]}@${domain}`
}

export function formatQuotaSignal(
  key: string,
  value: string | undefined,
  language: string | undefined,
  t: CLIProxyAPIQuotaTranslator,
) {
  if (!value) return '-'
  const normalized = key.toLowerCase()
  const number = Number(value)
  if (Number.isFinite(number) && normalized === 'retry-after') {
    if (number > 1e9 && language) {
      const date = new Date(number * 1000)
      if (Number.isFinite(date.getTime())) return formatDateTime(date.toISOString(), language)
    }
    return formatQuotaDuration(number, t)
  }
  if (Number.isFinite(number) && (normalized.includes('reset-at') || normalized.endsWith('-reset') || normalized === 'reset')) {
    const timestamp = number > 1e12 ? number : number * 1000
    const date = new Date(timestamp)
    if (Number.isFinite(date.getTime()) && language) return formatDateTime(date.toISOString(), language)
  }
  if (Number.isFinite(number) && (normalized.includes('utilization') || normalized.includes('percentage')) && number >= 0 && number <= 1 && !value.includes('%')) {
    return `${trimNumber(number * 100)}%`
  }
  if (Number.isFinite(number) && normalized.includes('percent') && !value.includes('%')) return `${value}%`
  if (Number.isFinite(number) && normalized.includes('seconds')) return formatQuotaDuration(number, t)
  if (Number.isFinite(number) && normalized.includes('minutes')) return t('cliproxyapi.duration.minutes', { count: number })
  if (language && (normalized.includes('reset') || normalized.includes('retry'))) {
    const date = new Date(value)
    if (Number.isFinite(date.getTime())) return formatDateTime(value, language)
  }
  return value
}

export function formatQuotaDuration(seconds: number, t: CLIProxyAPIQuotaTranslator) {
  if (seconds < 60) return t('cliproxyapi.duration.seconds', { count: Math.round(seconds) })
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return t('cliproxyapi.duration.minutes', { count: minutes })
  return t('cliproxyapi.duration.hours', { count: Math.floor(minutes / 60) })
}

function splitCodexSignal(key: string): { kind: 'primary' | 'secondary'; namespace: string; field: string } | null {
  const normalized = key.trim().toLowerCase()
  if (!normalized.startsWith(CODEX_PREFIX)) return null
  const rest = normalized.slice(CODEX_PREFIX.length)
  for (const kind of ['primary', 'secondary'] as const) {
    if (rest.startsWith(`${kind}-`)) return { kind, namespace: '', field: rest.slice(kind.length + 1) }
    const marker = `-${kind}-`
    const index = rest.indexOf(marker)
    if (index >= 0) return { kind, namespace: rest.slice(0, index), field: rest.slice(index + marker.length) }
  }
  return null
}

function codexWindowLabel(kind: 'primary' | 'secondary', namespace: string, limitName?: string) {
  if (namespace && limitName) return limitName
  if (!namespace) return kind === 'primary' ? 'Primary' : 'Secondary'
  return `${namespace.replace(/-/g, ' ')} · ${kind}`
}

function findSignal(signals: Record<string, string>, suffix: string) {
  const normalizedSuffix = suffix.toLowerCase()
  for (const [key, value] of Object.entries(signals)) {
    const normalized = key.toLowerCase()
    if (normalized === normalizedSuffix || normalized.endsWith(`-${normalizedSuffix}`)) return value
  }
  return undefined
}

function findSignalBySuffix(signals: Record<string, string>, suffix: string) {
  const normalizedSuffix = `-${suffix.toLowerCase()}`
  for (const [key, value] of Object.entries(signals)) {
    if (key.toLowerCase().endsWith(normalizedSuffix)) return value
  }
  return undefined
}

function parsePercent(value: string | undefined) {
  if (!value) return undefined
  const parsed = Number(value.replace(/%$/, '').trim())
  return Number.isFinite(parsed) && parsed >= 0 && parsed <= 100 ? parsed : undefined
}

function parseUtilization(value: string | undefined) {
  if (!value) return undefined
  const parsed = Number(value.replace(/%$/, '').trim())
  if (!Number.isFinite(parsed) || parsed < 0) return undefined
  const percent = parsed <= 1 && !value.includes('%') ? parsed * 100 : parsed
  return percent <= 100 ? percent : undefined
}

function parseBoolean(value: string | undefined) {
  if (!value) return undefined
  const normalized = value.trim().toLowerCase()
  if (normalized === 'true' || normalized === 'yes' || normalized === '1') return true
  if (normalized === 'false' || normalized === 'no' || normalized === '0') return false
  return undefined
}

function parsePositiveNumber(value: string | undefined) {
  const parsed = parseFiniteNumber(value)
  return parsed != null && parsed > 0 ? parsed : undefined
}

function parseNonNegativeNumber(value: string | undefined) {
  const parsed = parseFiniteNumber(value)
  return parsed != null && parsed >= 0 ? parsed : undefined
}

function parseFiniteNumber(value: string | undefined) {
  if (!value) return undefined
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : undefined
}

function parseWindowLabelMinutes(value: string) {
  const match = /^(\d+)([mhdw])$/i.exec(value)
  if (!match) return undefined
  const amount = Number(match[1])
  const multiplier = match[2].toLowerCase() === 'w' ? 10080 : match[2].toLowerCase() === 'd' ? 1440 : match[2].toLowerCase() === 'h' ? 60 : 1
  return amount * multiplier
}

function compareQuotaWindows(left: CLIProxyAPIQuotaWindow, right: CLIProxyAPIQuotaWindow) {
  const leftMinutes = left.windowMinutes ?? Number.MAX_SAFE_INTEGER
  const rightMinutes = right.windowMinutes ?? Number.MAX_SAFE_INTEGER
  if (leftMinutes !== rightMinutes) return leftMinutes - rightMinutes
  return left.label.localeCompare(right.label)
}

function nonNegativeNumber(value: number) {
  return Number.isFinite(value) && value >= 0 ? value : 0
}

function trimNumber(value: number) {
  return Number.isInteger(value) ? String(value) : value.toFixed(1).replace(/\.0$/, '')
}

function formatDateTime(value: string, language: string) {
  try {
    return new Intl.DateTimeFormat(language, {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      hourCycle: 'h23',
    }).format(new Date(value))
  } catch {
    return value
  }
}
