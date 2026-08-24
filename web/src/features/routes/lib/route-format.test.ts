import { describe, expect, it } from 'vitest'
import { formatRemainingDuration } from '@/features/routes/lib/route-format'

describe('formatRemainingDuration', () => {
  const t = (key: string) => ({
    'format.durationDays': '天',
    'format.durationHours': '时',
    'format.durationMinutes': '分',
  }[key] ?? key)

  it('shows days, hours, and minutes for subscription expiry details', () => {
    expect(formatRemainingDuration(1 * 86400 + 2 * 3600 + 4 * 60, t)).toBe('1天2时4分')
  })

  it('keeps the useful lower-order units when higher units are zero', () => {
    expect(formatRemainingDuration(2 * 3600 + 4 * 60, t)).toBe('2时4分')
    expect(formatRemainingDuration(4 * 60, t)).toBe('4分')
  })

  it('rounds a partial minute up and hides missing or expired values', () => {
    expect(formatRemainingDuration(1, t)).toBe('1分')
    expect(formatRemainingDuration(0, t)).toBeUndefined()
    expect(formatRemainingDuration(null, t)).toBeUndefined()
  })
})
