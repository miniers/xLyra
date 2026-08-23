import { describe, expect, it } from 'vitest'
import { getInitialRequestFilters, requestFiltersFromSearchParams } from '@/features/requests/lib/types'

describe('request list default filters', () => {
  it('leaves the end date empty on first entry', () => {
    const initial = getInitialRequestFilters(new Date('2026-08-22T10:30:00.000Z'))

    expect(initial.createdFromDate).toBeInstanceOf(Date)
    expect(initial.createdFromTime).toBeDefined()
    expect(initial.createdToDate).toBeUndefined()
    expect(initial.createdToTime).toBeUndefined()
  })

  it('preserves an explicitly supplied end date', () => {
    const filters = requestFiltersFromSearchParams(new URLSearchParams({
      created_to: '2026-08-22T12:15:00.000Z',
    }))

    const expected = new Date('2026-08-22T12:15:00.000Z')
    expect(filters.createdToDate).toBeInstanceOf(Date)
    expect(filters.createdToTime).toBe(`${String(expected.getHours()).padStart(2, '0')}:${String(expected.getMinutes()).padStart(2, '0')}`)
  })
})
