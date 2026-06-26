import { describe, it, expect } from 'vitest'
import { safeFormat } from '../../lib/datetime'

describe('safeFormat', () => {
  // Use local-time (no trailing Z) values so the assertion is timezone-independent:
  // a UTC-midnight instant renders as the previous day west of UTC.
  it('formats a valid date-time string', () => {
    expect(safeFormat('2026-06-26T13:45:30', 'yyyy-MM-dd')).toBe('2026-06-26')
  })

  it('formats a Date instance', () => {
    expect(safeFormat(new Date(2026, 5, 26, 13, 45, 30), 'yyyy-MM-dd')).toBe('2026-06-26')
  })

  it('returns the fallback for null / undefined / empty', () => {
    expect(safeFormat(null, 'HH:mm:ss')).toBe('—')
    expect(safeFormat(undefined, 'HH:mm:ss')).toBe('—')
    expect(safeFormat('', 'HH:mm:ss')).toBe('—')
  })

  it('returns the fallback for an unparseable value instead of throwing', () => {
    expect(() => safeFormat('not-a-date', 'HH:mm:ss')).not.toThrow()
    expect(safeFormat('not-a-date', 'HH:mm:ss')).toBe('—')
  })

  it('honours a custom fallback', () => {
    expect(safeFormat(undefined, 'HH:mm:ss', 'n/a')).toBe('n/a')
  })
})
