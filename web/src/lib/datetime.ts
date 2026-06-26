import { format } from 'date-fns'

/**
 * Format a date value defensively. A single missing or malformed timestamp
 * (e.g. an API field that came back undefined or non-ISO) would otherwise make
 * `format(new Date(value), …)` throw "Invalid time value" and white-screen the
 * whole page. safeFormat catches that and renders a placeholder for the bad
 * row instead, so one bad record never takes down the view.
 *
 * @param value    a date string, number, or Date (anything `new Date()` accepts)
 * @param pattern  a date-fns format pattern
 * @param fallback what to render when the value is missing/invalid (default "—")
 */
export function safeFormat(
  value: string | number | Date | null | undefined,
  pattern: string,
  fallback = '—',
): string {
  if (value === null || value === undefined || value === '') return fallback
  const d = value instanceof Date ? value : new Date(value)
  if (Number.isNaN(d.getTime())) return fallback
  return format(d, pattern)
}
