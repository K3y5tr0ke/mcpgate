import React, { useState } from 'react'
import { Button } from '../../components/ui/Button'

export function LocalLoginPage() {
  const [email, setEmail] = useState('admin@localhost')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  const raw = new URLSearchParams(window.location.search).get('redirect_to') ?? ''
  // Reject absolute URLs and protocol-relative paths (//evil.com) to prevent open redirect.
  const redirectTo = raw.startsWith('/') && !raw.startsWith('//') ? raw : '/console/dashboard'

  async function handleSubmit(e: React.SyntheticEvent) {
    e.preventDefault()
    setError('')
    setLoading(true)

    try {
      const res = await fetch('/auth/local/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
      })

      if (res.status === 429) {
        setError('Too many attempts. Please wait before trying again.')
        return
      }

      if (res.status === 403) {
        setError('Account suspended. Contact an administrator.')
        return
      }

      if (!res.ok) {
        setError('Invalid credentials.')
        return
      }

      const { token } = await res.json()
      localStorage.setItem('mcpgate_token', token)
      window.location.href = redirectTo
    } catch {
      setError('Network error. Is the server running?')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 px-4">
      <div className="w-full max-w-sm space-y-8">
        <div className="text-center">
          <h1 className="text-2xl font-bold tracking-tight text-gray-900">MCPGate</h1>
          <p className="mt-1 text-sm text-gray-500">Sign in with local admin account</p>
        </div>

        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label htmlFor="email" className="block text-sm font-medium text-gray-700">
              Email
            </label>
            <input
              id="email"
              type="email"
              required
              autoComplete="username"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm placeholder-gray-400 shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            />
          </div>

          <div>
            <label htmlFor="password" className="block text-sm font-medium text-gray-700">
              Password
            </label>
            <input
              id="password"
              type="password"
              required
              autoComplete="current-password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              className="mt-1 block w-full rounded-md border border-gray-300 px-3 py-2 text-sm placeholder-gray-400 shadow-sm focus:border-indigo-500 focus:outline-none focus:ring-1 focus:ring-indigo-500"
            />
          </div>

          {error && (
            <p className="rounded-md bg-red-50 px-3 py-2 text-sm text-red-700">{error}</p>
          )}

          <Button type="submit" className="w-full" loading={loading}>
            Sign in
          </Button>
        </form>

        <div className="space-y-3">
          <div className="relative">
            <div className="absolute inset-0 flex items-center">
              <div className="w-full border-t border-gray-200" />
            </div>
            <div className="relative flex justify-center text-xs">
              <span className="bg-gray-50 px-2 text-gray-400">or</span>
            </div>
          </div>

          <a
            href={`/auth/saml/login?redirect_to=${encodeURIComponent(redirectTo)}`}
            className="flex w-full items-center justify-center rounded-md border border-gray-300 bg-white px-4 py-2 text-sm font-medium text-gray-700 shadow-sm hover:bg-gray-50"
          >
            Sign in with SSO
          </a>
        </div>

        <p className="text-center text-xs text-gray-400">
          Run <code className="font-mono">make seed</code> to create or reset the local admin
          password.
        </p>
      </div>
    </div>
  )
}
