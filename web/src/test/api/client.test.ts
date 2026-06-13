import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { apiFetch, ApiError } from '../../api/client'

describe('apiFetch 401 handling', () => {
  const originalLocation = window.location

  beforeEach(() => {
    localStorage.setItem('mcpgate_token', 'stale-token')
    // jsdom's window.location is not writable by default; replace it with a stub.
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: { href: '' },
    })
  })

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: originalLocation,
    })
    vi.unstubAllGlobals()
    localStorage.clear()
  })

  it('clears the token, redirects to login, and throws ApiError(401)', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ status: 401, ok: false }))

    await expect(apiFetch('/servers')).rejects.toMatchObject({
      name: 'ApiError',
      status: 401,
    })

    expect(localStorage.getItem('mcpgate_token')).toBeNull()
    expect(window.location.href).toBe('/console/login?redirect_to=/console/dashboard')
  })

  it('throws ApiError with the server-provided message on non-401 errors', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue({
        status: 400,
        ok: false,
        statusText: 'Bad Request',
        json: () => Promise.resolve({ error: 'invalid window' }),
      }),
    )

    const err = (await apiFetch('/metrics/timeseries').catch((e) => e)) as ApiError
    expect(err).toBeInstanceOf(ApiError)
    expect(err.status).toBe(400)
    expect(err.message).toBe('invalid window')
  })
})
