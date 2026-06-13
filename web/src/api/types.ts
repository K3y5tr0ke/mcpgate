// TypeScript types that mirror Go store structs.

export interface User {
  id: string
  email: string
  display_name: string
  idp_subject: string
  idp_name: string
  status: 'active' | 'suspended' | 'deactivated'
  metadata: Record<string, unknown>
  created_at: string
  updated_at: string
}

export interface Role {
  id: string
  name: string
  description: string
  is_system: boolean
  created_at: string
}

export interface MCPServer {
  id: string
  name: string
  description: string
  transport: 'streamable_http' | 'sse' | 'stdio'
  config: Record<string, unknown>
  health_check: Record<string, unknown>
  is_enabled: boolean
  created_at: string
  updated_at: string
}

export interface AuditLogEntry {
  id: number
  timestamp: string
  trace_id: string
  session_id: string
  user_id: string
  user_email: string
  client_ip: string
  server_id: string
  server_name: string
  mcp_method: string
  tool_name: string
  direction: string
  status: 'success' | 'error' | 'denied'
  error_code: number | null
  error_message: string | null
  latency_ms: number
  request_size: number
  response_size: number
  rbac_decision: string
  metadata: Record<string, unknown>
  hash_chain: string
}

export interface CBState {
  server: string
  state: 'closed' | 'open' | 'half-open'
}

export interface MetricsSummary {
  requests_per_minute: number
  error_rate_percent: number
  p95_latency_ms: number
  active_requests: number
  active_sessions: number
  circuit_breakers: CBState[]
  dropped_audit_logs: number
}

export interface MetricsBucket {
  minute: number // unix minute (epoch seconds / 60) of the bucket's start
  requests: number
}

export interface MetricsTimeseries {
  window: string // "1h" | "6h" | "24h" | "7d"
  bucket: string // e.g. "1m", "15m", "2h"
  resolution_secs: number
  buckets: MetricsBucket[]
}

export interface JWKSRotateResult {
  issuer: string
  key_count: number
  rotated_at: string
}

export interface ServerTestResult {
  ok: boolean
  tools?: unknown[]
  error?: string
}

export interface Policy {
  subject: string
  resource: string
  action: string
  effect: string
}

export interface SAMLIdP {
  id: string
  name: string
  entity_id: string
  sso_url: string
  slo_url: string | null
  certificate: string
  attribute_map: Record<string, string>
  group_role_map: Record<string, string>
  is_enabled: boolean
  created_at: string
  updated_at: string
}

export interface JWKSConfig {
  id: string
  name: string
  issuer: string
  jwks_url: string
  audience: string
  is_enabled: boolean
  created_at: string
  updated_at: string
}

export type Settings = Record<string, number | string | boolean>

export interface RateLimitOverride {
  role_name: string
  rpm: number
  rpd: number
}

export interface Pagination {
  page: number
  page_size: number
  total: number
}

export interface ListResponse<T> {
  entries?: T[]
  items?: T[]
  total?: number
  servers?: T[]
  users?: T[]
  roles?: T[]
}
