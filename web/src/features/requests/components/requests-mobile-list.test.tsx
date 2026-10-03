import { createInstance } from 'i18next'
import { renderToStaticMarkup } from 'react-dom/server'
import { I18nextProvider } from 'react-i18next'
import { beforeAll, describe, expect, it } from 'vitest'
import type { RequestLogItem } from '@/features/requests/api/requests'
import enTranslations from '@/locales/en/requests.json'
import jpTranslations from '@/locales/jp/requests.json'
import zhTranslations from '@/locales/zh/requests.json'
import { RequestsMobileList } from './requests-mobile-list'

const locales = [
  { language: 'zh', translations: zhTranslations, tokenUnit: 'tokens', inputLabel: '输入', outputLabel: '输出', i18n: createInstance() },
  { language: 'en', translations: enTranslations, tokenUnit: 'tokens', inputLabel: 'In', outputLabel: 'Out', i18n: createInstance() },
  { language: 'jp', translations: jpTranslations, tokenUnit: 'トークン', inputLabel: '入力', outputLabel: '出力', i18n: createInstance() },
]

beforeAll(async () => {
  await Promise.all(locales.map(({ language, translations, i18n }) => i18n.init({
    lng: language,
    resources: { [language]: { requests: translations } },
    interpolation: { escapeValue: false },
  })))
})

function renderMobileList(i18n: ReturnType<typeof createInstance>, overrides: Partial<RequestLogItem> = {}) {
  const item: RequestLogItem = {
    id: 'log-1',
    request_id: 'req-1',
    success: true,
    state: 'completed',
    created_at: '2026-10-02T00:00:00Z',
    api_key: {},
    site: {},
    model: { canonical_model: 'requested-model', upstream_model: 'requested-model' },
    stream: true,
    latency_ms: 11_000,
    first_byte_latency_ms: 1_000,
    ...overrides,
    usage: {
      prompt_tokens: 1_234,
      completion_tokens: 217,
      total_tokens: 12_345,
      ...overrides.usage,
    },
  }
  return renderToStaticMarkup(
    <I18nextProvider i18n={i18n}>
      <RequestsMobileList items={[item]} expandedId={null} onExpandedIdChange={() => undefined} />
    </I18nextProvider>,
  )
}

const tpsCases: {
  name: string
  overrides: Partial<RequestLogItem>
  total: string
  tps: string
}[] = [
  { name: 'formats the reported total independently of input and output', overrides: {}, total: '12,345', tps: '21.7 t/s' },
  { name: 'preserves zero totals and zero TPS', overrides: { usage: { prompt_tokens: 0, completion_tokens: 0, total_tokens: 0 } }, total: '0', tps: '0.0 t/s' },
  { name: 'marks estimated totals', overrides: { tokens_estimated: true }, total: '~12,345', tps: '21.7 t/s' },
  { name: 'replaces missing totals with a dash', overrides: { usage: { total_tokens: null } }, total: '-', tps: '21.7 t/s' },
]

describe.each(locales)('RequestsMobileList token badge ($language)', ({ i18n, tokenUnit, inputLabel, outputLabel }) => {
  it.each(tpsCases)('$name', ({ overrides, total, tps }) => {
    const markup = renderMobileList(i18n, overrides)

    expect(markup).toContain(`>${total} ${tokenUnit} · ${tps}</div>`)
    expect(markup).not.toContain('{{total}}')
  })

  it.each([false, true])('keeps input and output when TPS is unavailable (estimated=%s)', (estimated) => {
    const markup = renderMobileList(i18n, { latency_ms: null, tokens_estimated: estimated })
    const marker = estimated ? '~' : ''

    expect(markup).toContain(`>${inputLabel} ${marker}1,234 / ${outputLabel} ${marker}217</div>`)
    expect(markup).not.toContain(' t/s')
  })
})
