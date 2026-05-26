import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch, toQS } from './client'
import type {
  AuditLogEntry,
  MCPServer,
  MetricsSummary,
  Policy,
  Role,
  ServerTestResult,
  Settings,
  User,
} from './types'

// ---- Servers ----------------------------------------------------------------

export const useServers = () =>
  useQuery({
    queryKey: ['servers'],
    queryFn: () => apiFetch<{ servers: MCPServer[]; total: number }>('/servers'),
  })

export const useServer = (id: string) =>
  useQuery({
    queryKey: ['servers', id],
    queryFn: () => apiFetch<MCPServer>(`/servers/${id}`),
    enabled: !!id,
  })

export const useCreateServer = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<MCPServer>) =>
      apiFetch<MCPServer>('/servers', { method: 'POST', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['servers'] }),
  })
}

export const useUpdateServer = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...data }: Partial<MCPServer> & { id: string }) =>
      apiFetch<MCPServer>(`/servers/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['servers'] }),
  })
}

export const useDeleteServer = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/servers/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['servers'] }),
  })
}

export const useTestServer = () =>
  useMutation({
    mutationFn: (id: string) =>
      apiFetch<ServerTestResult>(`/servers/${id}/test`, { method: 'POST' }),
  })

// ---- Users ------------------------------------------------------------------

export const useUsers = (params: { email?: string; status?: string; page?: number }) =>
  useQuery({
    queryKey: ['users', params],
    queryFn: () =>
      apiFetch<{ users?: User[]; total?: number }>('/users?' + toQS(params)),
  })

export const useUser = (id: string) =>
  useQuery({
    queryKey: ['users', id],
    queryFn: () => apiFetch<User>(`/users/${id}`),
    enabled: !!id,
  })

export const usePatchUser = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string; status?: string; roles?: string[] }) =>
      apiFetch<User>(`/users/${id}`, { method: 'PATCH', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['users'] }),
  })
}

// ---- Roles ------------------------------------------------------------------

export const useRoles = () =>
  useQuery({
    queryKey: ['roles'],
    queryFn: () => apiFetch<Role[]>('/roles'),
  })

export const useCreateRole = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: { name: string; description?: string }) =>
      apiFetch<Role>('/roles', { method: 'POST', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['roles'] }),
  })
}

export const useUpdateRole = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...data }: { id: string; description?: string }) =>
      apiFetch<Role>(`/roles/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['roles'] }),
  })
}

export const useDeleteRole = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/roles/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['roles'] }),
  })
}

export const useAssignRole = () =>
  useMutation({
    mutationFn: ({ roleId, userId }: { roleId: string; userId: string }) =>
      apiFetch<void>(`/roles/${roleId}/users`, {
        method: 'POST',
        body: JSON.stringify({ user_id: userId }),
      }),
  })

// ---- Policies ---------------------------------------------------------------

export const usePolicies = () =>
  useQuery({
    queryKey: ['policies'],
    queryFn: () => apiFetch<Policy[]>('/policies'),
  })

export const useAddPolicy = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: Policy) =>
      apiFetch<void>('/policies', { method: 'POST', body: JSON.stringify(p) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}

export const useRemovePolicy = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: Policy) =>
      apiFetch<void>('/policies', { method: 'DELETE', body: JSON.stringify(p) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['policies'] }),
  })
}

// ---- Audit Logs -------------------------------------------------------------

export const useLogs = (params: {
  from?: string
  to?: string
  user_id?: string
  server_id?: string
  status?: string
  mcp_method?: string
  search?: string
  page?: number
  page_size?: number
}) =>
  useQuery({
    queryKey: ['logs', params],
    queryFn: () =>
      apiFetch<{ entries: AuditLogEntry[]; total: number }>('/logs?' + toQS(params)),
  })

// ---- Metrics ----------------------------------------------------------------

export const useMetricsSummary = () =>
  useQuery({
    queryKey: ['metrics', 'summary'],
    queryFn: () => apiFetch<MetricsSummary>('/metrics/summary'),
    refetchInterval: 10_000,
  })

export const useMetricsTimeseries = (window: string = '1h', bucket: string = '5m') =>
  useQuery({
    queryKey: ['metrics', 'timeseries', window, bucket],
    queryFn: () =>
      apiFetch<{ buckets: { requests: number }[] }>(
        `/metrics/timeseries?window=${window}&bucket=${bucket}`,
      ),
    refetchInterval: 30_000,
  })

// ---- Settings ---------------------------------------------------------------

export const useSettings = () =>
  useQuery({
    queryKey: ['settings'],
    queryFn: () => apiFetch<Settings>('/settings'),
  })

export const useUpdateSettings = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<Settings>) =>
      apiFetch<Settings>('/settings', { method: 'PUT', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }),
  })
}
