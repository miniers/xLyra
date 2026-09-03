import { describe, expect, it } from 'vitest'
import type { CLIProxyAPIAccount, CLIProxyAPIQuotaObservation } from '@/features/sites/api/sites'
import {
  buildClaudeQuotaWindows,
  buildCodexQuotaWindows,
  buildRecentRequestSeries,
  formatQuotaSignal,
  maskCLIProxyAPIEmail,
} from '@/features/sites/lib/cliproxyapi-utils'

const quota = (signals: Record<string, string>): CLIProxyAPIQuotaObservation => ({
  observed_at: '2026-09-03T03:00:00Z',
  signals,
})

describe('CLIProxyAPI quota projections', () => {
  it('groups Codex primary and secondary windows and derives remaining percent', () => {
    const windows = buildCodexQuotaWindows(quota({
      'X-Codex-Primary-Used-Percent': '23',
      'X-Codex-Primary-Window-Minutes': '300',
      'X-Codex-Primary-Reset-After-Seconds': '120',
      'X-Codex-Secondary-Used-Percent': '41',
      'X-Codex-Secondary-Window-Minutes': '10080',
      'X-Codex-Secondary-Allowed': 'true',
    }))

    expect(windows.map((window) => ({
      kind: window.kind,
      used: window.usedPercent,
      remaining: window.remainingPercent,
      minutes: window.windowMinutes,
    }))).toEqual([
      { kind: 'primary', used: 23, remaining: 77, minutes: 300 },
      { kind: 'secondary', used: 41, remaining: 59, minutes: 10080 },
    ])
    expect(windows[1]?.status).toBe('allowed')
  })

  it('keeps dynamically named Codex windows instead of dropping them', () => {
    const windows = buildCodexQuotaWindows(quota({
      'X-Codex-Code-Review-Primary-Used-Percent': '5',
      'X-Codex-Code-Review-Primary-Window-Minutes': '60',
      'X-Codex-Additional-Images-Primary-Used-Percent': '12',
      'X-Codex-Additional-Images-Primary-Limit-Name': 'Image generation',
    }))

    expect(windows.map((window) => window.kind)).toEqual(['code_review', 'additional'])
    expect(windows[1]?.namespace).toBe('additional-images')
    expect(windows[1]?.label).toBe('Image generation')
  })

  it('projects account-level Codex status even without a window signal', () => {
    const windows = buildCodexQuotaWindows(quota({
      'X-Codex-Allowed': 'false',
      'X-Codex-Limit-Reached': 'true',
    }))

    expect(windows).toHaveLength(1)
    expect(windows[0]).toMatchObject({ kind: 'primary', status: 'limited', limitReached: true })
  })

  it('normalizes Claude utilization and reset signals for 5h and 7d windows', () => {
    const windows = buildClaudeQuotaWindows(quota({
      'Anthropic-Ratelimit-Unified-5h-Utilization': '0.42',
      'Anthropic-Ratelimit-Unified-5h-Status': 'allowed',
      'Anthropic-Ratelimit-Unified-5h-Reset': '2026-09-03T05:00:00Z',
      'Anthropic-Ratelimit-Unified-7d-Utilization': '61',
      'Anthropic-Ratelimit-Unified-7d-Status': 'limited',
    }))

    expect(windows.map((window) => ({
      namespace: window.namespace,
      used: window.usedPercent,
      minutes: window.windowMinutes,
      status: window.status,
    }))).toEqual([
      { namespace: '5h', used: 42, minutes: 300, status: 'allowed' },
      { namespace: '7d', used: 61, minutes: 10080, status: 'limited' },
    ])
  })
})

describe('CLIProxyAPI recent request projections', () => {
  it('keeps the newest 20 buckets in upstream order', () => {
    const account: CLIProxyAPIAccount = {
      auth_index: 'account-1',
      provider: 'codex',
      disabled: false,
      unavailable: false,
      success: 0,
      failed: 0,
      recent_requests: Array.from({ length: 21 }, (_, index) => ({
        time: String(index),
        success: index,
        failed: index % 2,
      })),
    }

    const points = buildRecentRequestSeries(account)
    expect(points).toHaveLength(20)
    expect(points[0]).toMatchObject({ time: '1', success: 1, failed: 1, total: 2 })
    expect(points[19]).toMatchObject({ time: '20', success: 20, failed: 0, total: 20 })
  })
})

describe('CLIProxyAPI email masking', () => {
  it('keeps the local-part edges and full domain', () => {
    expect(maskCLIProxyAPIEmail('visual_direct7u@icloud.com')).toBe('v***u@icloud.com')
  })

  it('keeps short local parts identifiable without exposing them fully', () => {
    expect(maskCLIProxyAPIEmail('ab@example.com')).toBe('a***b@example.com')
    expect(maskCLIProxyAPIEmail('a@example.com')).toBe('a***@example.com')
  })

  it('returns a placeholder for missing or malformed email values', () => {
    expect(maskCLIProxyAPIEmail(undefined)).toBe('-')
    expect(maskCLIProxyAPIEmail('not-an-email')).toBe('-')
    expect(maskCLIProxyAPIEmail('user@')).toBe('-')
  })
})

describe('formatQuotaSignal', () => {
  it('formats percentages and retry durations without inventing quota values', () => {
    const t = (key: string, options?: Record<string, unknown>) => `${key}:${String(options?.count ?? '')}`
    expect(formatQuotaSignal('X-Codex-Primary-Used-Percent', '23', 'en', t)).toBe('23%')
    expect(formatQuotaSignal('Retry-After', '120', undefined, t)).toBe('cliproxyapi.duration.minutes:2')
    expect(formatQuotaSignal('X-Codex-Primary-Used-Percent', undefined, 'en', t)).toBe('-')
  })
})
