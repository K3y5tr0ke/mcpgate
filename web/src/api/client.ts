const BASE = '/api/v1/admin'

export class ApiError extends Error {
  readonly status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = 'ApiError'
  }
}

export async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
  const token = localStorage.getItem('mcpgate_token')
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init?.headers,
    },
  })

  if (res.status === 401) {
    localStorage.removeItem('mcpgate_token')
    window.location.href = '/auth/saml/login'
    throw new ApiError(401, 'Unauthorized')
  }

  if (!res.ok) {
    let message = res.statusText
    try {
      const err = await res.json()
      message = err.error ?? err.message ?? message
    } catch {
      // ignore parse error; use statusText
    }
    throw new ApiError(res.status, message)
  }

  return res.json() as Promise<T>
}

function toQS(params: Record<string, string | number | boolean | undefined>): string {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') {
      p.set(k, String(v))
    }
  }
  return p.toString()
}

export { toQS }
