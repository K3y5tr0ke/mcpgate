import { useCallback, useMemo } from 'react'

interface JWTPayload {
  sub: string
  email: string
  roles: string[]
  exp: number
  session_id: string
}

function decodeJWT(token: string): JWTPayload | null {
  try {
    const parts = token.split('.')
    if (parts.length !== 3) return null
    const payload = JSON.parse(atob(parts[1].replace(/-/g, '+').replace(/_/g, '/')))
    return payload as JWTPayload
  } catch {
    return null
  }
}

export function useAuth() {
  const token = localStorage.getItem('mcpgate_token')
  const payload = useMemo(() => (token ? decodeJWT(token) : null), [token])
  const isExpired = payload ? payload.exp * 1000 < Date.now() : true
  const isAuthenticated = !!token && !isExpired

  const logout = useCallback(() => {
    localStorage.removeItem('mcpgate_token')
    window.location.href = '/auth/saml/logout'
  }, [])

  return { isAuthenticated, payload, token, logout }
}
