import { Link, useRouterState } from '@tanstack/react-router'
import { clsx } from 'clsx'
import { useAuth } from '../../hooks/useAuth'
import { useDarkMode } from '../../hooks/useDarkMode'

const NAV = [
  { to: '/console/dashboard', label: 'Dashboard', icon: '⬛' },
  { to: '/console/servers', label: 'Servers', icon: '🖥' },
  { to: '/console/users', label: 'Users', icon: '👤' },
  { to: '/console/roles', label: 'Roles', icon: '🔑' },
  { to: '/console/logs', label: 'Logs', icon: '📋' },
  { to: '/console/metrics', label: 'Metrics', icon: '📊' },
  { to: '/console/idp', label: 'SAML IdPs', icon: '🔐' },
  { to: '/console/jwks', label: 'JWKS', icon: '🗝' },
  { to: '/console/settings', label: 'Settings', icon: '⚙' },
] as const

export function AppShell({ children }: { children: React.ReactNode }) {
  const { payload, logout } = useAuth()
  const routerState = useRouterState()
  const pathname = routerState.location.pathname
  const { dark, toggle } = useDarkMode()

  return (
    <div className="flex h-screen overflow-hidden bg-gray-50 dark:bg-gray-950">
      {/* Sidebar */}
      <nav className="flex w-56 flex-shrink-0 flex-col border-r border-gray-200 bg-white dark:border-gray-700 dark:bg-gray-900">
        <div className="flex h-14 items-center border-b border-gray-100 px-4 dark:border-gray-700">
          <span className="text-base font-bold text-indigo-600">mcpgate</span>
        </div>
        <ul className="flex-1 overflow-y-auto py-3">
          {NAV.map((item) => (
            <li key={item.to}>
              <Link
                to={item.to}
                className={clsx(
                  'flex items-center gap-3 px-4 py-2 text-sm transition-colors',
                  pathname.startsWith(item.to)
                    ? 'bg-indigo-50 text-indigo-700 font-medium dark:bg-indigo-950 dark:text-indigo-300'
                    : 'text-gray-600 hover:bg-gray-50 dark:text-gray-400 dark:hover:bg-gray-800',
                )}
              >
                <span>{item.icon}</span>
                {item.label}
              </Link>
            </li>
          ))}
        </ul>
      </nav>

      {/* Main area */}
      <div className="flex flex-1 flex-col overflow-hidden">
        {/* Header */}
        <header className="flex h-14 flex-shrink-0 items-center justify-between border-b border-gray-200 bg-white px-6 dark:border-gray-700 dark:bg-gray-900">
          <span className="text-sm text-gray-400">Admin Console</span>
          <div className="flex items-center gap-3">
            <span className="text-sm text-gray-600 dark:text-gray-400">{payload?.email}</span>
            <button
              onClick={toggle}
              title={dark ? 'Switch to light mode' : 'Switch to dark mode'}
              className="rounded px-2 py-1 text-xs text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
            >
              {dark ? '☀' : '🌙'}
            </button>
            <button
              onClick={logout}
              className="rounded px-2 py-1 text-xs text-gray-500 hover:bg-gray-100 dark:text-gray-400 dark:hover:bg-gray-800"
            >
              Sign out
            </button>
          </div>
        </header>

        {/* Content */}
        <main className="flex-1 overflow-y-auto p-6 dark:text-gray-100">{children}</main>
      </div>
    </div>
  )
}
