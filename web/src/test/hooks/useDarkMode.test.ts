import { describe, it, expect, beforeEach, vi } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useDarkMode } from '../../hooks/useDarkMode'

const KEY = 'mcpgate_dark_mode'

function stubMatchMedia(matches: boolean) {
  vi.stubGlobal(
    'matchMedia',
    vi.fn().mockReturnValue({
      matches,
      media: '(prefers-color-scheme: dark)',
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    }),
  )
}

describe('useDarkMode', () => {
  beforeEach(() => {
    localStorage.clear()
    document.documentElement.classList.remove('dark')
  })

  it('defaults to false when no storage and OS is light', () => {
    stubMatchMedia(false)
    const { result } = renderHook(() => useDarkMode())
    expect(result.current.dark).toBe(false)
  })

  it('reads true from localStorage', () => {
    stubMatchMedia(false)
    localStorage.setItem(KEY, 'true')
    const { result } = renderHook(() => useDarkMode())
    expect(result.current.dark).toBe(true)
  })

  it('toggle flips the value and persists to localStorage', () => {
    stubMatchMedia(false)
    const { result } = renderHook(() => useDarkMode())
    act(() => result.current.toggle())
    expect(result.current.dark).toBe(true)
    expect(localStorage.getItem(KEY)).toBe('true')
  })

  it('toggle adds/removes the dark class on the document element', () => {
    stubMatchMedia(false)
    const { result } = renderHook(() => useDarkMode())
    act(() => result.current.toggle())
    expect(document.documentElement.classList.contains('dark')).toBe(true)
    act(() => result.current.toggle())
    expect(document.documentElement.classList.contains('dark')).toBe(false)
  })
})
