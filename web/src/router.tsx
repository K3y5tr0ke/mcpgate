import {
  createRootRoute,
  createRoute,
  createRouter,
  Navigate,
  Outlet,
} from '@tanstack/react-router'
import { DashboardPage } from './pages/dashboard/DashboardPage'
import { ServersListPage } from './pages/servers/ServersListPage'
import { UsersListPage } from './pages/users/UsersListPage'
import { RolesListPage } from './pages/roles/RolesListPage'
import { LogsListPage } from './pages/logs/LogsListPage'
import { SettingsPage } from './pages/settings/SettingsPage'

const rootRoute = createRootRoute({ component: Outlet })

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => <Navigate to="/console/dashboard" />,
})

const dashboardRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/dashboard',
  component: DashboardPage,
})

const serversRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/servers',
  component: ServersListPage,
})

const usersRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/users',
  component: UsersListPage,
})

const rolesRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/roles',
  component: RolesListPage,
})

const logsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/logs',
  component: LogsListPage,
})

const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/console/settings',
  component: SettingsPage,
})

const routeTree = rootRoute.addChildren([
  indexRoute,
  dashboardRoute,
  serversRoute,
  usersRoute,
  rolesRoute,
  logsRoute,
  settingsRoute,
])

export const router = createRouter({ routeTree })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}
