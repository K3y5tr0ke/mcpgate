import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { apiFetch, toQS } from './client'
import type {
  AuditLogEntry,
  JWKSConfig,
  JWKSRotateResult,
  MCPServer,
  MetricsSummary,
  MetricsTimeseries,
  Policy,
  Role,
  SAMLIdP,
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

// useMetricsTimeseries fetches request-volume buckets for a window. When `server`
// is set, the backend returns the per-server series. Bucket size / resolution is
// server-derived and reported back in the response (window, bucket, resolution_secs).
export const useMetricsTimeseries = (window: string = '1h', server?: string) =>
  useQuery({
    queryKey: ['metrics', 'timeseries', window, server ?? ''],
    queryFn: () =>
      apiFetch<MetricsTimeseries>(
        `/metrics/timeseries?window=${window}${server ? `&server=${encodeURIComponent(server)}` : ''}`,
      ),
    refetchInterval: 30_000,
  })

// ---- SAML IdP configs -------------------------------------------------------

export const useIdPs = () =>
  useQuery({ queryKey: ['idps'], queryFn: () => apiFetch<SAMLIdP[]>('/idps') })

export const useCreateIdP = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<SAMLIdP>) =>
      apiFetch<SAMLIdP>('/idps', { method: 'POST', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['idps'] }),
  })
}

export const useUpdateIdP = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...data }: Partial<SAMLIdP> & { id: string }) =>
      apiFetch<SAMLIdP>(`/idps/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['idps'] }),
  })
}

export const useDeleteIdP = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/idps/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['idps'] }),
  })
}

// ---- JWKS configs -----------------------------------------------------------

export const useJWKSConfigs = () =>
  useQuery({ queryKey: ['jwks'], queryFn: () => apiFetch<JWKSConfig[]>('/jwks/configs') })

export const useCreateJWKSConfig = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (data: Partial<JWKSConfig>) =>
      apiFetch<JWKSConfig>('/jwks/configs', { method: 'POST', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jwks'] }),
  })
}

export const useUpdateJWKSConfig = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, ...data }: Partial<JWKSConfig> & { id: string }) =>
      apiFetch<JWKSConfig>(`/jwks/configs/${id}`, { method: 'PUT', body: JSON.stringify(data) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jwks'] }),
  })
}

export const useDeleteJWKSConfig = () => {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => apiFetch<void>(`/jwks/configs/${id}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['jwks'] }),
  })
}

// useRotateJWKS forces the backend to evict its cached key set for an issuer,
// re-fetching on the next token validation. Returns the advertised key count.
export const useRotateJWKS = () =>
  useMutation({
    mutationFn: (id: string) =>
      apiFetch<JWKSRotateResult>(`/jwks/configs/${id}/rotate`, { method: 'POST' }),
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
