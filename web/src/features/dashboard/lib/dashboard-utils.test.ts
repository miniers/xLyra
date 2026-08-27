import i18next from 'i18next'
import { describe, expect, it } from 'vitest'
import type { DashboardOverview } from '@/features/dashboard/api/dashboard'
import { buildAttentionRiskItems } from './dashboard-utils'

function overviewWithAutomaticAnomaly(): DashboardOverview {
  return {
    meta: {
      days: 7,
      timezone: 'UTC',
      generated_at: '2026-08-25T00:00:00Z',
      today_start: '2026-08-25T00:00:00Z',
      range_start: '2026-08-19T00:00:00Z',
      range_end: '2026-08-25T00:00:00Z',
    },
    kpis: {
      cost: { today: 0, yesterday: 0, total: 0, currency: 'USD' },
      requests: {
        today: 0,
        yesterday: 0,
        total: 0,
        today_tokens: 0,
        yesterday_tokens: 0,
        total_tokens: 0,
        today_prompt_tokens: 0,
        total_prompt_tokens: 0,
        today_completion_tokens: 0,
        total_completion_tokens: 0,
        today_cached_tokens: 0,
        total_cached_tokens: 0,
      },
      rate_limit: {
        rpm: { used: 0 },
        tpm: { used: 0, actual: 0, reserved: 0 },
      },
    },
    health: { uptime_rows: [] },
    cooldowns: { items: [] },
    attention: {
      items: [{
        id: 'anomaly:canonical:site-model',
        severity: 'critical',
        type: 'automatic_anomaly',
        subject: {
          model_key: 'gpt-test',
          site_name: 'Primary',
        },
        metrics: {
          penalty: -20,
          remaining_seconds: 3720,
          metrics: [
            { metric: 'first_byte_latency', recent_value: 2200, baseline_value: 900 },
            { metric: 'cache_hit_rate', recent_value: 0.3, baseline_value: 0.75 },
          ],
        },
        action: { type: 'open_routes' },
      }],
    },
    insights: {
      failure_reasons: [],
      insufficient_candidates: [],
      high_latency: [],
    },
  }
}

describe('buildAttentionRiskItems', () => {
  it('renders automatic anomaly values and raw metrics instead of unresolved templates', async () => {
    const instance = i18next.createInstance()
    await instance.init({
      lng: 'zh',
      resources: {
        zh: {
          dashboard: {
            attention: {
              title: {
                automatic_anomaly: '{{site}} / {{model}} 正在自动异常降权，扣 {{penalty}} 分，预计持续 {{remaining}}',
              },
              detail: {
                automatic_anomaly: '正在自动异常降权，扣 {{penalty}} 分，预计持续 {{remaining}}。',
                automatic_anomaly_first_byte: '首字延迟近期 {{recent}} / 基线 {{baseline}}',
                automatic_anomaly_cache: '缓存命中率近期 {{recent}} / 基线 {{baseline}}',
              },
              actions: { automatic_anomaly: '查看路由' },
            },
            severityLabels: { critical: '严重' },
            value: { automatic_anomaly: '-{{penalty}} 分' },
          },
        },
      },
      interpolation: { escapeValue: false },
    })

    const [item] = buildAttentionRiskItems(overviewWithAutomaticAnomaly(), instance.getFixedT('zh', 'dashboard'))

    expect(item.title).toBe('Primary / gpt-test 正在自动异常降权，扣 20 分，预计持续 1h 2m')
    expect(item.value).toBe('-20 分')
    expect(item.detail).toContain('正在自动异常降权，扣 20 分，预计持续 1h 2m。')
    expect(item.detail).toContain('首字延迟近期 2.2s / 基线 900ms')
    expect(item.detail).toContain('缓存命中率近期 30.0% / 基线 75.0%')
    expect(`${item.title} ${item.value} ${item.detail}`).not.toMatch(/\{\{|attention\./)
  })
})
