# Sprint 7 Implementation Plan — Admin Console + Backend Polish

**Date:** 2026-05-26  
**Status:** Pre-implementation  
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/mcpgate-architecture-blueprint.md` · `docs/implementation/stage-6-26-05-2026.md`  
**Predecessor:** `docs/implementation/stage-6-26-05-2026.md`

---

## 1. Context

Sprints 0–6 delivered a fully operational, observability-instrumented backend: authenticated and authorised MCP proxy, tamper-evident audit log pipeline, Prometheus metrics, OpenTelemetry tracing, per-user rate limiting, and per-server circuit breakers. The system is API-complete but has no operator-facing UI. Every management action requires raw HTTP calls against the admin REST API.

Sprint 7 closes this gap. The primary deliverable is a React admin console served at `/console` that gives operators a polished web interface for every day-to-day task: monitoring the dashboard, managing MCP servers, administering users and roles, inspecting audit logs, and editing system settings.

The sprint is split into two tracks that run in parallel after the first few days:

**Track A — Backend prerequisites:** new API endpoints that the frontend depends on, plus all carry-forward items from Sprint 6 that are small enough to complete before the UI work is blocked.

**Track B — Frontend:** the React application from scaffolding through all five core pages.

**End state:** an authenticated admin navigates to `http://localhost:8080/console`, sees a live dashboard with request volume and server health, can register a new MCP server and test its connectivity, manage users and roles, and inspect or export the audit log — all without touching the CLI.

---

## 2. What Already Exists

### Backend APIs (available today)

| Endpoint | Handler | State |
|----------|---------|-------|
| `GET/POST/PUT/DELETE /api/v1/admin/servers` | `serverHandler` | Complete |
| `GET/POST/PUT/DELETE /api/v1/admin/idps` | `idpHandler` | Complete |
| `GET/POST/PUT/DELETE /api/v1/admin/jwks/configs` | `jwksHandler` | Complete |
| `GET /api/v1/admin/logs` | `logHandler.list` | Complete |
| `GET /api/v1/admin/logs/export` | `logHandler.export` | Complete |
| `WS GET /api/v1/admin/logs/stream` | `logHandler.stream` | Complete |
| `GET /api/v1/admin/logs/verify` | `logHandler.verify` | Complete |
| `GET /api/v1/admin/users` | `userHandler.list` | Stub — empty response |
| `GET /api/v1/admin/roles` | `roleHandler.list` | Stub — returns static list |
| `GET/POST /api/v1/admin/policies` | `policyHandler` | Complete (Casbin CRUD) |

### Missing backend APIs (needed by Sprint 7 frontend)

| Endpoint | Purpose |
|----------|---------|
| `GET /api/v1/admin/metrics/summary` | Dashboard real-time stats |
| `GET /api/v1/admin/metrics/timeseries` | Dashboard sparkline charts |
| `POST /api/v1/admin/servers/{id}/test` | Server connectivity probe |
| `PATCH /api/v1/admin/users/{id}` | User status and role update |
| `POST /api/v1/admin/roles` | Create role |
| `PUT /api/v1/admin/roles/{id}` | Update role |
| `DELETE /api/v1/admin/roles/{id}` | Delete role |
| `PUT /api/v1/admin/users/{id}/roles` | Assign roles to user |
| `GET /api/v1/admin/settings` | Read current config values |
| `PUT /api/v1/admin/settings` | Update mutable settings |

### Frontend (nothing exists)

No `web/` directory exists. The architecture blueprint specifies `/console` as the admin console root.

### Carry-forward from Sprint 6 (all backend)

From `stage-6-26-05-2026.md §15`:

| Label | Item |
|-------|------|
| CF-S6-1 | WAL replay true idempotency — `INSERT ... ON CONFLICT (hash_chain) DO NOTHING` in `BatchInsert` |
| CF-S6-2 | `ConcurrentConnections` enforcement — Redis INCR/DECR per-user counter |
| CF-S6-3 | Prometheus alerting rules — `deployments/prometheus/mcpgate-alerts.yaml` |
| CF-S6-4 | OTLP sampling strategy — configurable `ParentBased(TraceIDRatioBased)` sampler |
| CF-S6-5 | Upstream circuit state in `/ready` — expose `breakers` map via `proxy.Handler` accessor |
| CF-S6-6 | Per-role rate limit overrides — `rate_limit_overrides` table + middleware lookup |

---

## 3. New Package / Directory Structure

```
web/                                 React SPA root (new)
├── src/
│   ├── api/
│   │   ├── client.ts               Typed fetch wrapper (auth header injection, error handling)
│   │   ├── queries.ts              TanStack Query hooks for every admin API endpoint
│   │   └── types.ts                TypeScript types mirroring Go store structs
│   ├── components/
│   │   ├── ui/                     Design-system primitives (Button, Input, Badge, Card, Modal, Toast)
│   │   ├── layout/                 AppShell, Sidebar, Header, Breadcrumbs
│   │   └── charts/                 Recharts wrappers (SparklineChart, BarChart, LineChart)
│   ├── pages/
│   │   ├── dashboard/              DashboardPage, MetricsCards, ServerHealthGrid, ActivityFeed
│   │   ├── servers/                ServersListPage, ServerDetailPage, ServerForm
│   │   ├── users/                  UsersListPage, UserDetailDrawer, RoleAssignForm
│   │   ├── roles/                  RolesListPage, RoleForm, PolicyMatrixEditor
│   │   └── logs/                   LogsListPage, LogDetailDrawer, LogStreamPanel
│   ├── hooks/
│   │   ├── useAuth.ts              JWT storage, expiry check, logout
│   │   ├── useWebSocket.ts         Managed WS lifecycle with reconnect
│   │   └── usePagination.ts        Page/pageSize state + URL sync
│   ├── router.tsx                  TanStack Router — route tree definition
│   ├── main.tsx                    React 18 createRoot entry point
│   └── index.css                   Tailwind directives + CSS custom properties
├── public/
│   └── favicon.svg
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts
└── vitest.config.ts

internal/
├── admin/api/
│   ├── metrics.go                  metricsHandler: summary, timeseries endpoints (new)
│   ├── users.go                    Completed: list, get, patch status, assign roles
│   ├── roles.go                    Completed: full CRUD + user assignment
│   ├── settings.go                 settingsHandler: get/put mutable config values (new)
│   └── router.go                   Wire new handlers, serve SPA static assets
│
└── proxy/
    └── handler.go                  + ServerCircuitStates() map[string]State accessor (CF-S6-5)

deployments/
└── prometheus/
    └── mcpgate-alerts.yaml         Alerting rules for circuit breaker, rate limits, pipeline (CF-S6-3)
```

Files modified:

| File | Change |
|------|--------|
| `internal/store/postgres/audit_logs.go` | `BatchInsert` gains `ON CONFLICT (hash_chain) DO NOTHING` (CF-S6-1) |
| `internal/store/redis/rate_limiter.go` | Add `checkConcurrency(ctx, key, limit)` for concurrent-connection enforcement (CF-S6-2) |
| `internal/ratelimit/middleware.go` | Call `checkConcurrency` on SSE/WS routes; INCR on connect, DECR on disconnect |
| `internal/tracing/provider.go` | Add `SampleRate float64` to `OTLPConfig`; switch to `ParentBased(TraceIDRatioBased)` when set (CF-S6-4) |
| `internal/server/server.go` | Wire metrics handler, settings handler, SPA static serving; `handleReady` gets circuit states (CF-S6-5) |
| `internal/config/config.go` | Add `OTLPConfig.SampleRate float64` (CF-S6-4); add `RateLimitOverride` struct (CF-S6-6) |
| `internal/store/interfaces.go` | Add `RateLimitOverrideStore` interface (CF-S6-6) |
| `internal/store/postgres/migrations/005_rate_limit_overrides.up.sql` | `rate_limit_overrides` table (CF-S6-6) |
| `go.mod` | embed (stdlib); no new Go deps expected |
| `cmd/mcpgate/main.go` | Add `//go:generate` directive for frontend build |
| `Makefile` | `build-web`, `dev-all` targets |

---

## 4. New Dependencies

### Go
No new Go dependencies. The `embed` package is part of the standard library (Go 1.16+).

### Frontend (npm)

```bash
# Core framework
npm install react@18 react-dom@18
npm install -D typescript@5 vite@6 @vitejs/plugin-react
npm install -D tailwindcss @tailwindcss/vite

# Routing + data fetching
npm install @tanstack/react-router @tanstack/router-vite-plugin
npm install @tanstack/react-query @tanstack/react-query-devtools

# Forms + validation
npm install react-hook-form zod @hookform/resolvers

# Charts
npm install recharts

# Utilities
npm install date-fns clsx tailwind-merge

# Testing
npm install -D vitest @vitest/ui @testing-library/react @testing-library/user-event jsdom
npm install -D @playwright/test msw
```

---

## 5. Implementation Details

### 5.1 Frontend Build Integration

The React app builds to `web/dist/`. The Go binary embeds these assets using `//go:embed`:

```go
// internal/admin/api/console.go (new)
import "embed"

//go:embed web/dist
var consoleDist embed.FS

// Handler returns an HTTP handler that serves the React SPA.
// All paths that don't match a static file fall back to index.html so that
// TanStack Router (client-side) can handle deep-link navigation.
func ConsoleSPAHandler() http.Handler {
    sub, _ := fs.Sub(consoleDist, "web/dist")
    fileServer := http.FileServer(http.FS(sub))
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        // Try to serve static asset; fall back to index.html for SPA routes
        _, err := fs.Stat(sub, strings.TrimPrefix(r.URL.Path, "/"))
        if err != nil {
            r = r.Clone(r.Context())
            r.URL.Path = "/"
        }
        fileServer.ServeHTTP(w, r)
    })
}
```

The console is mounted at `/console` in `buildRouter()`:

```go
r.Handle("/console", http.RedirectHandler("/console/", http.StatusMovedPermanently))
r.Handle("/console/*", http.StripPrefix("/console", api.ConsoleSPAHandler()))
```

In development, the Vite dev server runs on port 5173 and proxies API calls to `http://localhost:8080/api`. In production, both are served from port 8080 (static assets embedded in the binary).

The `//go:embed` directive requires the `web/dist` directory to exist at compile time. Add a `Makefile` rule:

```makefile
build-web:
    cd web && npm ci && npm run build

build: build-web
    CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mcpgate ./cmd/mcpgate

dev-all:
    docker-compose -f deployments/docker/docker-compose.dev.yml up -d postgres redis
    cd web && npm run dev &
    go run ./cmd/mcpgate --config configs/mcpgate.dev.yaml
```

A `//go:generate` directive in `cmd/mcpgate/main.go` ensures `go generate ./...` also builds the frontend:

```go
//go:generate npm --prefix ../../web install
//go:generate npm --prefix ../../web run build
```

### 5.2 Vite + TypeScript Configuration

**`vite.config.ts`:**
```typescript
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { TanStackRouterVite } from '@tanstack/router-vite-plugin'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), TanStackRouterVite(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/api': 'http://localhost:8080',
      '/auth': 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
})
```

The `TanStackRouterVite` plugin auto-generates the route tree from files in `src/pages/`, eliminating manual route registration.

### 5.3 API Client Layer (`web/src/api/`)

**`client.ts`** — typed fetch wrapper:
```typescript
const BASE = '/api/v1/admin'

async function apiFetch<T>(path: string, init?: RequestInit): Promise<T> {
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
    throw new Error('Unauthorized')
  }
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new ApiError(res.status, err.error ?? 'Unknown error')
  }
  return res.json()
}

export class ApiError extends Error {
  constructor(public readonly status: number, message: string) {
    super(message)
  }
}
```

**`queries.ts`** — TanStack Query hooks, one file per domain:
```typescript
// Servers
export const useServers = () =>
  useQuery({ queryKey: ['servers'], queryFn: () => apiFetch<ServerList>('/servers') })

export const useTestServer = () =>
  useMutation({ mutationFn: (id: string) => apiFetch<TestResult>(`/servers/${id}/test`, { method: 'POST' }) })

// Users
export const useUsers = (params: UserFilter) =>
  useQuery({ queryKey: ['users', params], queryFn: () => apiFetch<UserList>('/users?' + toQS(params)) })

// Metrics
export const useMetricsSummary = () =>
  useQuery({ queryKey: ['metrics', 'summary'], queryFn: () => apiFetch<MetricsSummary>('/metrics/summary'),
    refetchInterval: 10_000 })

export const useMetricsTimeseries = (window: string, bucket: string) =>
  useQuery({ queryKey: ['metrics', 'timeseries', window, bucket],
    queryFn: () => apiFetch<Timeseries>(`/metrics/timeseries?window=${window}&bucket=${bucket`),
    refetchInterval: 30_000 })
```

**TypeScript types (`types.ts`)** mirror Go store structs exactly so that API responses are fully typed without manual casting.

### 5.4 Design System (`web/src/components/ui/`)

A small set of composable primitives built on top of Tailwind CSS. No external component library (avoids large bundle sizes and opinionated styling):

| Component | Props | Notes |
|-----------|-------|-------|
| `Button` | `variant: primary\|secondary\|danger`, `size`, `loading`, `disabled` | Renders `<button>` with consistent hover/focus styles |
| `Badge` | `color: green\|red\|yellow\|gray\|blue` | Status pill for circuit breaker state, user status, etc. |
| `Card` | `title`, `footer` | White rounded container with shadow |
| `Input` | standard HTML input + `error` string | Accessible label + error message |
| `Select` | standard HTML select + typed options | Consistent dropdown styling |
| `Modal` | `open`, `onClose`, `title` | Focus-trapped overlay via `<dialog>` |
| `Toast` | global context with `success(msg)`, `error(msg)` | Fixed bottom-right toast stack |
| `Table` | `columns`, `data`, `onSort`, pagination props | Sortable, paginated data table |
| `DataDrawer` | `open`, `onClose`, `title`, children | Side panel for detail views |
| `SparklineChart` | `data: number[]`, `color` | Recharts `AreaChart` wrapper for dashboard metrics |

All components are dark-mode ready via Tailwind's `dark:` variant. A CSS custom property (`--color-brand`) enables one-file brand colour changes.

### 5.5 Authentication Flow

The admin console re-uses the existing SAML and JWT infrastructure:

1. **On load:** `useAuth` hook checks `localStorage.getItem('mcpgate_token')`. If the JWT is expired (decoded without signature verification), the user is redirected to `/auth/saml/login`.
2. **SAML callback:** The ACS handler (`POST /auth/saml/acs`) already issues a JWT and sets `Set-Cookie: mcpgate_session`. The React ACS callback page (`/console/auth/callback`) reads the `token` from the JSON response body (the ACS handler also includes it in the JSON response), stores it in `localStorage`, and redirects to `/console`.
3. **Logout:** Clears `localStorage`, calls `GET /auth/saml/logout`, redirects to `/console`.

No new backend auth changes are required — the SAML flow is already complete from Sprint 2/3.

### 5.6 App Shell (`web/src/components/layout/`)

The `AppShell` component wraps all authenticated pages:

```
┌─────────────────────────────────────────────────────────┐
│  Header: [mcpgate logo]              [user email] [logout] │
├──────────┬──────────────────────────────────────────────┤
│          │                                              │
│ Sidebar  │  Content area                                │
│          │  ┌── Breadcrumbs ──────────────────────────┐│
│ Dashboard│  │  Page title                             ││
│ Servers  │  │                                         ││
│ Users    │  │  <route component>                      ││
│ Roles    │  │                                         ││
│ Logs     │  └─────────────────────────────────────────┘│
│ Settings │                                              │
│          │                                              │
└──────────┴──────────────────────────────────────────────┘
```

The sidebar collapses to an icon-only rail on narrow viewports (≤768px). The active route is highlighted. Route links are generated from the TanStack Router type-safe `Link` component.

### 5.7 Dashboard Page (`web/src/pages/dashboard/`)

The dashboard auto-refreshes every 10 seconds via TanStack Query's `refetchInterval`. It has three sections:

**Real-time metrics strip** — four `Card` components in a responsive grid:
- Requests/min (current RPM from `summary.requests_per_minute`)
- Error rate % (`summary.error_rate_percent`) with `Badge` colour (green/yellow/red by threshold)
- p95 latency ms (`summary.p95_latency_ms`)
- Active sessions (`summary.active_sessions`)

**Server health grid** — one card per registered MCP server showing:
- Server name + transport badge
- Circuit breaker state badge (green=closed, red=open, yellow=half-open)
- Last request timestamp

**Activity feed** — last 20 audit log entries (queried from `GET /api/v1/admin/logs?page_size=20`), showing user email, server name, MCP method, and status. Clicking a row opens the `DataDrawer` with the full entry JSON.

Optional sparklines (RPM over last hour) are populated by `GET /api/v1/admin/metrics/timeseries?window=1h&bucket=5m`.

### 5.8 Servers Page (`web/src/pages/servers/`)

**List view (`ServersListPage`):**
- `Table` component with columns: name, transport (badge), enabled (toggle), circuit state, last updated
- `+ Add Server` button → opens `ServerForm` in a `Modal`
- Row click → `ServerDetailPage`

**Detail view (`ServerDetailPage`):**
- Server config fields (read-only)
- `Test Connectivity` button → calls `POST /api/v1/admin/servers/{id}/test`
  - Success: shows discovered tools in an expandable list with descriptions
  - Failure: shows the error message from the upstream
- `Edit` button → opens `ServerForm` with pre-filled values
- `Delete` button with confirmation `Modal`

**`ServerForm`:**
- Name (text)
- Transport (`Select`: `streamable_http`, `sse`, `stdio`)
- Config fields — rendered dynamically based on transport:
  - `streamable_http` / `sse`: endpoint URL input(s)
  - `stdio`: command + args (dynamic array input)
- Is enabled toggle
- React Hook Form + Zod validation; errors shown inline

### 5.9 Users Page (`web/src/pages/users/`)

- `Table` with search input (live `useQuery` refetch on input change, 300ms debounce)
- Columns: email, display name, roles (as `Badge` list), status (active/suspended), last active
- Row click → `UserDetailDrawer` (side panel):
  - User info fields
  - `RoleAssignForm`: multi-select of all available roles (current roles pre-checked)
  - `Suspend / Activate` toggle button → `PATCH /api/v1/admin/users/{id}`
- `onClose` triggers `queryClient.invalidateQueries(['users'])` to refresh the table

### 5.10 Roles & Policies Page (`web/src/pages/roles/`)

**Role list sub-page:**
- Table: name, description, user count, system (read-only badge)
- `+ Add Role` button → `RoleForm` modal (name, description)
- Edit / Delete buttons; system roles disable Delete
- Delete guard: API returns 409 if role has assigned users — surface as `Toast` error

**Policy matrix sub-page:**
Accessible via tab on the Roles page:
- Rows = MCP servers (from `GET /api/v1/admin/servers`)
- Columns = roles (from `GET /api/v1/admin/roles`)
- Each cell = `Select` with values `allow` / `deny` / `inherit` for the `call` action
- A separate matrix (or expandable rows) for `list` and `read` actions
- `Save Changes` button → diffs current state vs loaded state → posts only changed Casbin rules via `POST /api/v1/admin/policies` and `DELETE /api/v1/admin/policies`
- `Diff preview` panel shows added (green) and removed (red) policy lines before saving

This is the most complex frontend component. The diff is computed client-side: load existing policies on mount → user edits toggles → on save, compute symmetric difference between old and new → batch API calls.

### 5.11 Logs Page (`web/src/pages/logs/`)

**Filter bar:** date range picker (via native `<input type="date">`), user dropdown, server dropdown, status radio, free-text search. All filter state is synced to URL query params so links are shareable.

**Table view:** `Table` with columns: timestamp, user email, server, MCP method, tool name, status badge (success/denied/error), latency. Pagination via `usePagination`. Row click → `LogDetailDrawer` with full entry JSON rendered in a syntax-highlighted `<pre>`.

**Real-time tail panel:** collapsible panel below the table, connected to `WS /api/v1/admin/logs/stream` via `useWebSocket`. New entries prepended with a subtle animation. `Pause / Resume` button. Filter state from the filter bar is sent to the WS as a JSON message on change.

**Export button:** triggers `GET /api/v1/admin/logs/export?format=csv` (or `json`) with the current filters as query params. The response is streamed directly to a `<a>` download link.

### 5.12 Settings Page (`web/src/pages/settings/`)

A single-page form (React Hook Form + Zod) for mutable configuration values:

| Setting | Type | Source |
|---------|------|--------|
| Session TTL | Duration string | `cfg.Auth.SessionTTL` |
| Rate limit RPM (default) | number | `cfg.RateLimiting.Default.RPM` |
| Rate limit RPD (default) | number | `cfg.RateLimiting.Default.RPD` |
| Log retention (hot days) | number | `cfg.Logging.Retention.HotDays` |
| Proxy default timeout | Duration string | `cfg.Proxy.DefaultTimeout` |
| Circuit breaker failure threshold | number | `cfg.Proxy.CircuitBreaker.FailureThreshold` |
| Circuit breaker recovery timeout | Duration string | `cfg.Proxy.CircuitBreaker.RecoveryTimeout` |

Changes that require a server restart are marked with a `⚠ Requires restart` badge. The backend stores mutable settings in the database (`settings` table — see §6.1) so they survive restarts without file edits.

---

## 6. Backend API Additions

### 6.1 Settings Storage (Migration 005, partial)

A simple `settings` table persists the mutable subset of config values:

```sql
-- internal/store/postgres/migrations/005_sprint7.up.sql
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value JSONB    NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rate_limit_overrides (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    role_name  TEXT NOT NULL REFERENCES roles(name) ON DELETE CASCADE,
    rpm        INT  NOT NULL DEFAULT 0,  -- 0 = use global default
    rpd        INT  NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (role_name)
);
```

`settings` is a key-value store for mutable config. `rate_limit_overrides` supports CF-S6-6.

### 6.2 Metrics Query API (`internal/admin/api/metrics.go`)

Two new endpoints under the admin router:

#### `GET /api/v1/admin/metrics/summary`

Reads current values from the in-process Prometheus registry plus Redis:

```go
type MetricsSummary struct {
    RequestsPerMinute float64        `json:"requests_per_minute"`
    ErrorRatePercent  float64        `json:"error_rate_percent"`
    P95LatencyMS      float64        `json:"p95_latency_ms"`
    ActiveRequests    int64          `json:"active_requests"`
    ActiveSessions    int64          `json:"active_sessions"`
    CircuitBreakers   []CBState      `json:"circuit_breakers"`
    DroppedAuditLogs  int64          `json:"dropped_audit_logs"`
}
type CBState struct {
    Server string `json:"server"`
    State  string `json:"state"` // "closed", "open", "half-open"
}
```

`requests_per_minute` is approximated using a Redis rolling 60-second sorted-set counter (same Lua-based sliding window, but read-only for the summary endpoint). `p95_latency_ms` uses histogram buckets from the Prometheus registry (computed via `registry.Gather()` → parse `MetricFamily` of type HISTOGRAM → estimate from bucket counts using linear interpolation within the highest-populated bucket).

`ActiveSessions` queries `SELECT COUNT(*) FROM sessions WHERE expires_at > now()` — simple and accurate.

`CircuitBreakers` uses the new `proxy.Handler.ServerCircuitStates() map[string]string` accessor (CF-S6-5).

#### `GET /api/v1/admin/metrics/timeseries`

Query params: `window` (`1h`, `6h`, `24h`), `bucket` (`1m`, `5m`, `15m`, `1h`).

The backend maintains per-minute request counts in Redis using a sorted set keyed `metrics:rpm:<minute_unix_ts>`. The metrics middleware increments this on each request. The timeseries endpoint reads the relevant keys and returns bucketed data:

```json
{
  "buckets": [
    {"timestamp": "2026-05-26T10:00:00Z", "requests": 42, "errors": 1},
    {"timestamp": "2026-05-26T10:05:00Z", "requests": 38, "errors": 0},
    ...
  ]
}
```

The sorted set approach keeps each minute's count in a separate key with TTL = window size + 1h. This is O(window/bucket) reads — at most 24×60=1440 keys for the maximum window.

**Implementation note:** a small addition to `metrics.Collector.Middleware()` writes to Redis. The `Collector` must accept an optional `*redis.Client` for this purpose — `nil` skips the write (for unit tests and configurations without Redis).

### 6.3 Server Connectivity Test (`POST /api/v1/admin/servers/{id}/test`)

```go
func (h *serverHandler) test(w http.ResponseWriter, r *http.Request) {
    id := chi.URLParam(r, "id")
    srv, err := h.store.GetByID(r.Context(), uuid.MustParse(id))
    // build transport for srv
    // call tools/list with a 10s timeout
    // return {"ok": true, "tools": [...]} or {"ok": false, "error": "..."}
}
```

The handler creates a short-lived transport (does not cache it), sends a `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` message, and returns the response. Uses the same `buildTransport` logic from `proxy.Handler` (extracted to a package-level function for reuse).

### 6.4 Complete User and Role Management

**Users** (`PATCH /api/v1/admin/users/{id}`)
```json
// Request
{"status": "suspended"}
// or
{"roles": ["admin", "developer"]}
```
Validating `status` against `{"active", "suspended"}`. Role assignment calls `RoleStore.SetUserRoles(ctx, userID, roles)` which diffs and issues the minimal set of INSERT/DELETE operations.

**Roles** (`POST/PUT/DELETE /api/v1/admin/roles`)
- `POST`: insert into `roles` table; `is_system = false`
- `PUT`: update `description`; `name` is immutable (changing a role name would break Casbin policies)
- `DELETE`: check `user_roles` for active assignments; return 409 if any exist; else delete

### 6.5 CF-S6-5: Upstream Circuit State in `/ready`

Add an accessor to `proxy.Handler`:

```go
// ServerCircuitStates returns the current state of every circuit breaker that
// has been lazily created. Returns an empty map if no requests have been made.
func (h *Handler) ServerCircuitStates() map[string]string {
    states := make(map[string]string)
    h.breakers.Range(func(k, v any) bool {
        states[k.(string)] = v.(*circuitbreaker.CircuitBreaker).State().String()
        return true
    })
    return states
}
```

`handleReady` in `server.go` calls this and adds an `"upstreams"` key to the components map:

```json
{
  "status": "degraded",
  "components": {
    "database": {"status": "ok", "latency_ms": 2},
    "redis":    {"status": "ok", "latency_ms": 1},
    "pipeline": {"status": "ok", "detail": "dropped_total=0"},
    "upstreams": {
      "github-mcp": {"status": "ok",       "circuit_state": "closed"},
      "slack-mcp":  {"status": "degraded", "circuit_state": "open"}
    }
  }
}
```

An open or half-open circuit sets `"status": "degraded"` for the upstream; if any upstream is `"error"` (circuit open) the overall `"status"` becomes `"degraded"` (not `"error"` — upstream health is non-critical for the proxy itself).

---

## 7. Carry-Forward Items (Backend)

### CF-S6-1 — WAL Replay True Idempotency

Migration 004 added `UNIQUE INDEX idx_audit_logs_hash_chain ON audit_logs (hash_chain)`. Now that this index is deployed, `BatchInsert` in `internal/store/postgres/audit_logs.go` can use conflict-safe inserts:

```go
// Change the INSERT in BatchInsert from:
INSERT INTO audit_logs (...) VALUES ...
// to:
INSERT INTO audit_logs (...) VALUES ...
ON CONFLICT (hash_chain) DO NOTHING
```

This makes WAL replay idempotent: replaying entries that were already committed produces zero rows inserted (not duplicates). The `tag.RowsAffected()` value will reflect actual inserts only.

**Prerequisite:** migration 004 must be applied before deploying this code change. Given migration 004 was part of Sprint 6, this is safe to implement now.

### CF-S6-2 — ConcurrentConnections Enforcement

Add a `checkConcurrency` method to `RateLimiter`:

```go
// checkConcurrency increments a per-user connection counter in Redis.
// Returns (over_limit bool, cleanup func(), error).
// cleanup() must be deferred by the caller to decrement the counter on disconnect.
func (r *RateLimiter) StartConcurrent(ctx context.Context, key string, max int) (bool, func(), error) {
    redisKey := "ratelimit:conc:" + key
    n, err := r.client.Incr(ctx, redisKey).Result()
    if err != nil {
        return false, func() {}, err // fail-open
    }
    r.client.Expire(ctx, redisKey, 5*time.Minute) // TTL guards against leaked connections
    if int(n) > max {
        r.client.Decr(ctx, redisKey)
        return true, func() {}, nil // over limit
    }
    cleanup := func() { r.client.Decr(context.Background(), redisKey) }
    return false, cleanup, nil
}
```

The rate limit middleware calls `StartConcurrent` for SSE and WebSocket upgrade routes only (not `POST /message` which is short-lived). The `cleanup` function is deferred so the counter decrements when the connection closes.

`ConcurrentConnections: 0` (default) skips the check entirely.

### CF-S6-3 — Prometheus Alerting Rules

New file `deployments/prometheus/mcpgate-alerts.yaml`:

```yaml
groups:
  - name: mcpgate
    rules:
      - alert: MCPGateHighErrorRate
        expr: rate(mcpgate_upstream_errors_total[5m]) > 0.1
        for: 2m
        labels:
          severity: warning
        annotations:
          summary: "High upstream error rate on {{ $labels.server }}"
          description: "Error rate {{ $value | humanizePercentage }} on server {{ $labels.server }}"

      - alert: MCPGateCircuitBreakerOpen
        expr: mcpgate_circuit_breaker_state == 1
        for: 1m
        labels:
          severity: critical
        annotations:
          summary: "Circuit breaker open for {{ $labels.server }}"
          description: "Server {{ $labels.server }} circuit breaker has been open for > 1 minute"

      - alert: MCPGateRateLimitStorm
        expr: rate(mcpgate_rate_limit_hits_total[1m]) > 50
        for: 0m
        labels:
          severity: warning
        annotations:
          summary: "Rate limit storm detected"
          description: "{{ $value }} rate-limited requests/min on window={{ $labels.window }}"

      - alert: MCPGateAuditPipelineDropping
        expr: mcpgate_audit_pipeline_dropped_total > 0
        for: 5m
        labels:
          severity: critical
        annotations:
          summary: "Audit log pipeline is dropping entries"
          description: "{{ $value }} entries dropped. Check Postgres and WAL sink."

      - alert: MCPGateHighLatency
        expr: histogram_quantile(0.95, rate(mcpgate_request_duration_seconds_bucket[5m])) > 1.0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "p95 latency above 1s"
          description: "p95 latency is {{ $value }}s"
```

No Go code changes required. Add a Prometheus scrape target and alerting rules file reference to `deployments/prometheus/prometheus.yml`.

### CF-S6-4 — OTLP Sampling Strategy

Add `SampleRate float64` to `OTLPConfig`:

```go
type OTLPConfig struct {
    Enabled   bool    `koanf:"enabled"`
    Endpoint  string  `koanf:"endpoint"`
    Insecure  bool    `koanf:"insecure"`
    SampleRate float64 `koanf:"sample_rate"` // 0 = AlwaysSample, 0.1 = 10%, 1.0 = AlwaysSample
}
```

In `tracing.New`:
```go
sampler := sdktrace.AlwaysSample()
if cfg.SampleRate > 0 && cfg.SampleRate < 1.0 {
    sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SampleRate))
}
tp := sdktrace.NewTracerProvider(
    ...,
    sdktrace.WithSampler(sampler),
)
```

`ParentBased` respects the incoming trace context: if the upstream set `sampled=1` in the W3C `traceparent` header, the span is always recorded regardless of `SampleRate`. If there is no incoming context, the ratio-based sampler decides. Default `SampleRate = 0` preserves current `AlwaysSample()` behaviour.

### CF-S6-6 — Per-Role Rate Limit Overrides

The `rate_limit_overrides` table (migration 005) stores per-role RPM/RPD values. The rate limit middleware is updated to look up role overrides before applying the global default:

```go
// In ratelimit.Middleware, after extracting identity:
limit := store.RateLimit{RPM: cfg.Default.RPM, RPD: cfg.Default.RPD}

// Check for per-role override (highest limits among the user's roles win)
for _, role := range identity.Roles {
    if override, err := overrideStore.GetByRole(ctx, role); err == nil {
        if override.RPM > limit.RPM { limit.RPM = override.RPM }
        if override.RPD > limit.RPD { limit.RPD = override.RPD }
    }
}
// RPM == 0 means "no limit for this window"
```

This requires `Middleware` to accept an optional `store.RateLimitOverrideStore` parameter (nil when feature is not configured).

Admin API endpoint: `GET/PUT /api/v1/admin/roles/{role}/rate-limits` — reads/writes a single `rate_limit_overrides` row for the named role.

---

## 8. Migration 005

**`005_sprint7.up.sql`:**

```sql
-- Settings table for mutable config values (read at startup, overriding file config).
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Per-role rate limit overrides (CF-S6-6).
CREATE TABLE IF NOT EXISTS rate_limit_overrides (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_name  TEXT        NOT NULL REFERENCES roles(name) ON DELETE CASCADE,
    rpm        INT         NOT NULL DEFAULT 0,
    rpd        INT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (role_name)
);

-- Seed default settings from dev defaults.
INSERT INTO settings (key, value) VALUES
    ('session_ttl_seconds',             '3600'::jsonb),
    ('rate_limit_default_rpm',          '600'::jsonb),
    ('rate_limit_default_rpd',          '50000'::jsonb),
    ('log_retention_hot_days',          '30'::jsonb),
    ('proxy_default_timeout_seconds',   '10'::jsonb),
    ('circuit_breaker_failure_threshold', '5'::jsonb),
    ('circuit_breaker_recovery_seconds', '30'::jsonb)
ON CONFLICT (key) DO NOTHING;
```

**`005_sprint7.down.sql`:**

```sql
DROP TABLE IF EXISTS rate_limit_overrides;
DELETE FROM settings WHERE key IN (
    'session_ttl_seconds', 'rate_limit_default_rpm', 'rate_limit_default_rpd',
    'log_retention_hot_days', 'proxy_default_timeout_seconds',
    'circuit_breaker_failure_threshold', 'circuit_breaker_recovery_seconds'
);
DROP TABLE IF EXISTS settings;
```

---

## 9. Test Plan

### Unit tests (no services, `-short` compatible)

| File | Tests |
|------|-------|
| `web/src/components/ui/*.test.tsx` | Every primitive renders without error; Button loading state disables click; Badge renders correct colour class |
| `web/src/api/client.test.ts` | 401 clears token + redirects; API error maps to `ApiError` with status; headers set correctly |
| `web/src/pages/servers/ServerForm.test.tsx` | Required field validation; transport-specific fields appear/disappear on transport change |
| `web/src/pages/roles/PolicyMatrixEditor.test.tsx` | Diff produces correct added/removed sets; Save button disabled when no changes |
| `web/src/hooks/useAuth.test.ts` | Expired JWT detected; logout clears storage |
| `internal/admin/api/metrics_test.go` | Summary endpoint returns valid JSON; timeseries returns correct bucket count for 1h/5m window |
| `internal/admin/api/settings_test.go` | GET returns all keys; PUT validates unknown keys; PUT updates value and updated_at |

### Integration tests (`//go:build integration`)

| File | Tests |
|------|-------|
| `internal/admin/api/users_test.go` | List returns all users; PATCH suspends user; PATCH with invalid status returns 400 |
| `internal/admin/api/roles_test.go` | CRUD lifecycle; delete with users returns 409; system role delete returns 403 |
| `internal/store/postgres/rate_limit_overrides_test.go` | CRUD; GetByRole returns ErrNotFound for unmapped roles |
| `internal/store/postgres/audit_logs_test.go` | `BatchInsert` with duplicate `hash_chain` is idempotent (no error, rows=0 on second call) |

### E2E tests (`//go:build integration`, `tests/e2e/`)

| File | Tests |
|------|-------|
| `tests/e2e/console_test.go` | `TestE2E_Console_ServesSPA` — GET `/console/` returns 200, body contains `<div id="root">`; GET `/console/nonexistent-route` returns 200 (SPA fallback) |
| `tests/e2e/metrics_api_test.go` | `TestE2E_MetricsAPI_Summary` — send requests, call summary API, assert `requests_per_minute > 0` and `circuit_breakers` is an array |
| `tests/e2e/settings_test.go` | `TestE2E_Settings_GetAndPut` — GET settings returns all keys; PUT updates one key; GET confirms updated value |
| `tests/e2e/server_test_connectivity.go` | `TestE2E_ServerTest_LiveServer` — register mock, POST `/test`, assert `ok: true` and `tools` non-empty |

### Playwright browser E2E (Sprint 7 — core journeys)

Run in CI against the full stack (real Postgres + Redis + built frontend):

| Test | Journey |
|------|---------|
| `login.spec.ts` | Navigate to `/console` → redirected to SAML login → (uses test token injection for CI) → dashboard renders |
| `servers.spec.ts` | Click Servers → Add Server → fill form → submit → server appears in list → click → Test Connectivity → tools shown |
| `users.spec.ts` | Click Users → search by email → open user → assign role → confirm badge appears |
| `logs.spec.ts` | Click Logs → filter by date range → table shows entries → export CSV → verify download triggered |

Note: Playwright tests require a full dev stack. They run in CI with `docker-compose up` and use the admin token from a seeded test user.

---

## 10. Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Embed frontend assets in Go binary via `embed.FS` | Single deployable binary. No separate static asset server or CDN required for self-hosted deployments. `embed.FS` supports HTTP range requests natively. |
| D2 | Vite + React 18 + TanStack Router (not Next.js) | mcpgate is a single-origin SPA, not a server-rendered app. Next.js adds SSR complexity with no benefit. Vite produces smaller bundles and has faster HMR. TanStack Router provides full type safety for route params and search params. |
| D3 | TanStack Query for all server state (no Redux/Zustand) | All shared UI state in this app is really server state. TanStack Query handles loading, error, stale, and refetch automatically. Introducing a separate state management library would add complexity with no benefit. |
| D4 | No external component library (build own design system) | External libraries (MUI, Shadcn) have heavy dependencies and opinionated themes. A 12-component internal system built on Tailwind is smaller, faster to load, and exactly matches mcpgate's design needs. |
| D5 | Dark/light mode via Tailwind `dark:` + CSS custom properties | Tailwind's built-in dark mode requires only a `dark` class on `<html>`. All components use `dark:` variants. No runtime colour-switching library needed. |
| D6 | Metrics timeseries via Redis sorted sets (not Prometheus remote read) | Prometheus remote read adds an external dependency (Thanos/VictoriaMetrics). Redis sorted sets with 1-minute buckets are already available, fast to write (same path as rate limiting), and sufficient for dashboard sparklines. |
| D7 | Settings stored in DB, loaded at startup (override file config) | Allows runtime setting changes without file edits or restarts for non-critical settings. File config remains the canonical source for secrets and infrastructure config. `settings` table overrides only business-logic defaults. |
| D8 | SPA fallback: serve `index.html` for unknown `/console/*` paths | TanStack Router handles client-side navigation. Direct deep-links to any `/console/...` URL must return the SPA entry point. File-not-found falls through to `index.html`; actual 404s for unknown API paths are returned as JSON (different path prefix). |
| D9 | Role names are immutable after creation | Casbin policy rules reference role names as strings. Renaming a role would require updating all policy rows atomically. Making names immutable eliminates this complexity. The UI's edit form disables the name field for existing roles. |
| D10 | `settings` table as flat key-value (JSONB values) | Avoids schema migrations when adding new settings. New settings can be added by inserting a row with a default value. The Go settings handler validates keys against a known allowlist to prevent arbitrary injection. |
| D11 | `INSERT ... ON CONFLICT (hash_chain) DO NOTHING` for WAL idempotency | Now that migration 004's unique index is deployed, this is the cleanest approach. The alternative (tracking replay state with a separate table) is more complex. `DO NOTHING` is safe because duplicate entries are semantically identical. |
| D12 | Per-role rate limit overrides keyed by role name (not role ID) | Casbin policies use role names as strings. Using role names in `rate_limit_overrides` avoids a JOIN when the rate limit middleware looks up overrides by role. Role names are immutable (D9), so the FK reference is stable. |

---

## 11. Ordered File List (Implementation Order)

Dependencies flow downward. Steps 1–8 unblock frontend work by establishing APIs and build infrastructure. Steps 9 onwards are the frontend and can be developed in parallel once step 9 is complete.

```
Backend prerequisites:
 1. internal/store/postgres/migrations/005_sprint7.up.sql
    internal/store/postgres/migrations/005_sprint7.down.sql

 2. internal/proxy/handler.go
    → add ServerCircuitStates() accessor (CF-S6-5)

 3. internal/store/postgres/audit_logs.go
    → BatchInsert: add ON CONFLICT (hash_chain) DO NOTHING (CF-S6-1)

 4. internal/config/config.go
    → OTLPConfig.SampleRate float64 (CF-S6-4)
    → RateLimitOverride struct (CF-S6-6)

 5. internal/tracing/provider.go
    → ParentBased(TraceIDRatioBased) sampler when SampleRate > 0 (CF-S6-4)

 6. internal/store/interfaces.go
    → RateLimitOverrideStore interface (CF-S6-6)

 7. internal/store/postgres/rate_limit_overrides.go   (new file — CF-S6-6)
    internal/store/postgres/settings.go               (new file — settings CRUD)

 8. internal/store/redis/rate_limiter.go
    → StartConcurrent method (CF-S6-2)

 9. internal/ratelimit/middleware.go
    → RateLimitOverrideStore param + concurrent connection check (CF-S6-2, CF-S6-6)

10. internal/admin/api/metrics.go                      (new file — summary + timeseries)
11. internal/admin/api/settings.go                     (new file — get/put settings)
12. internal/admin/api/users.go                        (complete list, get, patch)
13. internal/admin/api/roles.go                        (complete full CRUD)
14. internal/admin/api/servers.go                      (add test handler)
15. internal/admin/api/router.go                       (wire all new handlers)

16. internal/server/server.go
    → handleReady gains circuit states from ServerCircuitStates() (CF-S6-5)
    → wire settings store, rate limit override store, SPA handler

17. deployments/prometheus/mcpgate-alerts.yaml         (new file — CF-S6-3)

18. go mod tidy + go build ./... → verify clean build

Frontend:
19. web/package.json + web/tsconfig.json + web/vite.config.ts + web/index.html
    (npm init, install all deps)

20. web/src/index.css + tailwind base setup
21. web/src/main.tsx + web/src/router.tsx  (app entry + TanStack Router tree)
22. web/src/api/types.ts                   (TypeScript mirrors of Go structs)
23. web/src/api/client.ts                  (fetch wrapper + ApiError)
24. web/src/api/queries.ts                 (all TanStack Query hooks)
25. web/src/hooks/useAuth.ts + useWebSocket.ts + usePagination.ts
26. web/src/components/ui/  (Button, Badge, Card, Input, Select, Modal, Toast, Table, DataDrawer, SparklineChart)
27. web/src/components/layout/ (AppShell, Sidebar, Header, Breadcrumbs)
28. web/src/pages/dashboard/   (DashboardPage, MetricsCards, ServerHealthGrid, ActivityFeed)
29. web/src/pages/servers/     (ServersListPage, ServerDetailPage, ServerForm)
30. web/src/pages/users/       (UsersListPage, UserDetailDrawer, RoleAssignForm)
31. web/src/pages/roles/       (RolesListPage, RoleForm, PolicyMatrixEditor)
32. web/src/pages/logs/        (LogsListPage, LogDetailDrawer, LogStreamPanel)
33. web/src/pages/settings/    (SettingsPage, SettingsForm)

34. web/src/components/**/*.test.tsx + web/src/api/*.test.ts   (unit tests)
35. tests/e2e/console_test.go + tests/e2e/metrics_api_test.go + tests/e2e/settings_test.go
36. web/e2e/*.spec.ts           (Playwright tests — login, servers, users, logs)

37. Makefile: add build-web, dev-all, test-frontend, test-playwright targets
38. cmd/mcpgate/main.go: //go:generate directives
39. internal/admin/api/console.go: embed.FS + SPA handler (added last — needs web/dist to exist)
```

---

## 12. Verification

```bash
# Unit tests (no services)
go test -race -count=1 -short ./...
cd web && npm run test

# Integration tests (requires Docker)
make docker-up && make migrate-up
go test -race -count=1 -tags=integration ./internal/...

# E2E tests (Go)
go test -race -count=1 -tags=integration -timeout=120s ./tests/e2e/...

# Build frontend
cd web && npm ci && npm run build
go build ./...  # verifies embed.FS compiles cleanly

# Manual: admin console
make dev-all
open http://localhost:5173/console   # dev server (Vite proxy to :8080 API)
# OR
make build && ./bin/mcpgate          # embedded assets at :8080/console

# Manual: metrics API
curl -s http://localhost:8080/api/v1/admin/metrics/summary \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
# → {"requests_per_minute": ..., "circuit_breakers": [...], ...}

# Manual: server connectivity test
curl -s -X POST http://localhost:8080/api/v1/admin/servers/$SERVER_ID/test \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq .
# → {"ok": true, "tools": [...]}

# Manual: rate limit override
curl -s -X PUT http://localhost:8080/api/v1/admin/roles/admin/rate-limits \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"rpm": 0, "rpd": 0}' | jq .
# → {"role": "admin", "rpm": 0, "rpd": 0}

# Manual: readiness with circuit states
curl -s http://localhost:8080/ready | jq .
# → {"status":"ok","components":{"upstreams":{"my-server":{"status":"ok","circuit_state":"closed"}},...}}

# Playwright (requires full stack)
make docker-up && make migrate-up && make build && ./bin/mcpgate &
cd web && npx playwright test
```

---

## 13. TODOs and Carry-Forward to Sprint 8

### Completed in Sprint 7 (target)

- [ ] React admin console — scaffold, design system, auth flow, app shell
- [ ] Dashboard page with real-time metrics and server health
- [ ] Servers page with connectivity test and CRUD
- [ ] Users page with role assignment
- [ ] Roles & policies page with visual policy matrix editor
- [ ] Logs page with real-time tail and export
- [ ] Settings page
- [ ] Metrics summary + timeseries backend API
- [ ] `embed.FS` static serving at `/console`
- [ ] Complete user and role management APIs
- [ ] CF-S6-1: WAL idempotency (`ON CONFLICT DO NOTHING`)
- [ ] CF-S6-2: ConcurrentConnections enforcement
- [ ] CF-S6-3: Prometheus alerting rules YAML
- [ ] CF-S6-4: OTLP sampling strategy
- [ ] CF-S6-5: Upstream circuit state in `/ready`
- [ ] CF-S6-6: Per-role rate limit overrides
- [ ] Migration 005 (settings + rate_limit_overrides)

### Deferred to Sprint 8

- [ ] **Advanced pages** — Metrics charts page with per-server drilldown; SAML/JWKS config pages (currently managed via raw API only); dark/light mode toggle; accessibility audit and fixes.
- [ ] **Playwright E2E expansion** — roles/policy matrix test; settings test; SAML config wizard.
- [ ] **Per-user rate limit overrides** — Sprint 6 defined per-role overrides; per-user overrides require a `user_rate_limit_overrides` table and UI treatment.
- [ ] **Server tool auto-discovery** — background goroutine that calls `tools/list` on all enabled servers periodically, caches results in a `server_tools` table (new migration). The Servers page detail view shows cached tools without a live test.
- [ ] **Config hot-reload** — watch config file + `settings` table for changes; reload rate limits and CB thresholds without restart (see dev plan sprint 12).
- [ ] **WarmDays archive** — `RetentionConfig.WarmDays` processing: either pg_partman partition detach, or `INSERT INTO audit_logs_warm SELECT ... WHERE timestamp < now() - warm_days`. Requires the warm storage strategy to be defined first.
- [ ] **WebSocket auth hardening** — the WS log stream uses the `mcpgate_session` cookie. Add an explicit integration test verifying that a WS connection with an expired cookie is rejected after the session TTL, not just at upgrade time.
- [ ] **OpenAPI spec generation** — generate `openapi.yaml` from the Go admin API handlers (using `swaggo/swag` or hand-written spec). Feed into the TypeScript client generator to replace the manually maintained `types.ts`.
