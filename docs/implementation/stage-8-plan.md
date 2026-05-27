# Sprint 8 Implementation Plan — Admin Console Completion + Backend Hardening

**Date:** 2026-05-26
**Status:** Pre-implementation
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/mcpgate-architecture-blueprint.md` · `docs/implementation/stage-7-26-05-2026.md`
**Predecessor:** `docs/implementation/stage-7-26-05-2026.md`

---

## 1. Context

Sprint 7 delivered a working React admin console covering six core pages (Dashboard, Servers, Users, Roles, Logs, Settings) and completed all six Sprint 6 backend carry-overs. The system is now operable via the web UI for day-to-day tasks.

Sprint 8 closes the remaining gaps across five dimensions:

1. **Backend carry-overs** — four items from stage-7 §14 that require focused, testable changes: the metrics timeseries full implementation, concurrent-connection enforcement, per-role rate limit lookups, and the corresponding admin endpoint.
2. **Frontend carry-overs** — four Sprint 7-8 items left incomplete: real-time log tail, log export, transport-aware server form, and the visual policy matrix editor.
3. **New admin pages** — three pages from the dev plan's Sprint 9 scope whose backend APIs already exist: a full Metrics page, a SAML IdP config page, and a JWKS config page. Dark mode is added as a fourth item.
4. **Testing foundation** — Vitest smoke tests and four Playwright E2E journeys. The `vitest` and `playwright` packages are already installed; only test files and config are missing.
5. **Local admin account** — a break-glass bootstrap account independent of SAML, allowing operators with no programmatic experience to set up mcpgate from scratch through the browser without requiring direct database access.

**End state:** a fresh deployment can be bootstrapped entirely through the browser: an operator uses the seeded local admin account to log in, configure the SAML IdP, and assign roles to the first SAML user — then disables the local account. Existing authenticated admins can manage IdP and JWKS configs from the UI, view time-series metrics charts with a date-range picker, see a live log tail, download log exports, and set per-role rate limits — all with a dark mode toggle. The CI pipeline gains a smoke-test pass gate.

---

## 2. What Already Exists

### Backend (complete, no changes needed)

| Endpoint | State |
|----------|-------|
| `GET/POST/PUT/DELETE /api/v1/admin/idps` | Complete |
| `GET/POST/PUT/DELETE /api/v1/admin/jwks/configs` | Complete |
| `GET /api/v1/admin/metrics/summary` | Complete |
| `GET /api/v1/admin/metrics/timeseries` | Complete — single-point placeholder |
| `GET /api/v1/admin/logs/stream` (WebSocket) | Complete |
| `GET /api/v1/admin/logs/export` | Complete |
| `GET/PUT /api/v1/admin/settings` | Complete |
| `GET/POST/PUT/DELETE /api/v1/admin/roles` | Complete |
| `GET/POST/DELETE /api/v1/admin/policies` | Complete (Casbin CRUD) |
| `POST /api/v1/admin/servers/{id}/test` | Complete |

### Frontend (complete, no changes needed)

- All UI primitives: `Button`, `Input`, `Select`, `Card`, `Badge`, `Modal`, `Toast`, `Table`, `DataDrawer`
- `AppShell`, `SparklineChart` (Recharts `AreaChart` wrapper — Recharts v3.8.1 already installed)
- `useWebSocket`, `usePagination`, `useAuth` hooks
- All existing queries in `queries.ts` and types in `types.ts`
- TanStack Router route tree in `router.tsx`

### Missing (needed by Track E)

| Item | State |
|------|-------|
| `password_hash TEXT` column on `users` table | Missing — requires new migration |
| `POST /auth/local/login` endpoint | Missing — new auth handler |
| `make seed` local admin creation | Missing — extend existing seed command |

### Installed but unused

- `vitest` v4, `@playwright/test` v1.60, `msw` v2, `@testing-library/react` v16 — all in `package.json`, no config or test files exist yet

---

## 3. Ordered Task List

Tasks are ordered by dependency. Backend carry-overs land first (Track A) so no frontend work is blocked. Frontend carry-overs and new pages run in parallel (Tracks B + C). Track E (local admin account) is self-contained and can run in parallel with Tracks B/C — its backend task (E1) must land before its frontend task (E2). Testing is last (Track D) but `vitest.config.ts` setup should happen at the start of Track B so tests can be written alongside pages.

### Track A — Backend carry-overs (4 tasks)

#### A1 — Metrics timeseries: Redis sorted-set implementation

**File:** `internal/metrics/collector.go`, `internal/server/server.go`

**Problem:** `GET /api/v1/admin/metrics/timeseries` returns a single-point placeholder. The frontend Metrics page needs real bucketed data.

**Design:**
- Add an optional `*redis.Client` field to `Collector`. When nil, the timeseries endpoint returns the placeholder (preserves existing behaviour when Redis is unavailable).
- In the Prometheus chi middleware (`Collector.Middleware`), after incrementing the request counter, also write:
  ```go
  minuteBucket := time.Now().Unix() / 60
  key := fmt.Sprintf("metrics:timeseries:%d", minuteBucket)
  redisClient.Incr(ctx, key)                     // atomic increment
  redisClient.Expire(ctx, key, 7*24*time.Hour)   // 7-day rolling window
  ```
  `INCR` + `EXPIRE` is safe to call without a transaction because `EXPIRE` only refreshes TTL.
- The `timeseries` handler reads keys for the requested window:
  ```go
  // window defaults to "1h", accepts "6h", "24h", "7d"
  now := time.Now().Unix() / 60
  for i := start; i <= now; i++ {
      val, _ := redisClient.Get(ctx, fmt.Sprintf("metrics:timeseries:%d", i)).Int64()
      buckets = append(buckets, Bucket{Timestamp: i * 60, Requests: val})
  }
  ```
- Response shape (unchanged — matches what the frontend will query):
  ```json
  {
    "window": "1h",
    "buckets": [
      { "timestamp": 1748910000, "requests": 42 },
      ...
    ]
  }
  ```
- Wire `redisClient` into `Collector` in `server.New()` after the Redis client is already initialised.

**Acceptance:** `GET /api/v1/admin/metrics/timeseries?window=1h` returns ≥ 2 distinct non-zero buckets after sending a handful of proxy requests. Unit test: mock Redis, verify INCR is called on each middleware pass and keys match the expected minute bucket.

---

#### A2 — CF-S6-2: StartConcurrent wiring

**Files:** `internal/ratelimit/middleware.go`, `internal/server/server.go`

**Problem:** `RateLimiter.StartConcurrent` is implemented and tested but never called. Long-lived SSE connections and the WebSocket log stream consume connection slots indefinitely with no per-user concurrent limit enforced.

**Design:**
- The rate limit middleware already wraps every request. The middleware is a standard `func(http.Handler) http.Handler`, so it sees every request including SSE and WebSocket upgrade requests.
- For SSE and WS, the connection is held open for the duration of the stream. Add a concurrent connection check at the middleware boundary:
  ```go
  cleanup, err := limiter.StartConcurrent(r.Context(), identity.UserID.String())
  if err != nil {  // ErrLimitExceeded
      writeJSONRPCError(w, -32000, "too many concurrent connections")
      return
  }
  defer cleanup()
  next.ServeHTTP(w, r)
  ```
- This is correct for both short-lived POST requests (cleanup called immediately after handler returns) and long-lived SSE/WS connections (cleanup deferred until the connection drops).
- No changes to the SSE or WS handlers themselves.

**Acceptance:** unit test: configure `ConcurrentConnections: 1`, open two concurrent long-lived connections from the same user, verify the second returns `-32000`. Existing rate limiter unit tests continue to pass.

---

#### A3 — CF-S6-6: Per-role rate limit override lookup

**Files:** `internal/ratelimit/middleware.go`, `internal/admin/api/router.go`, `internal/server/server.go`

**Problem:** `RateLimitOverrideStore` is wired into `server.New()` and `NewRouter` but the rate limit middleware ignores it. All users are subject to the single default limit from config.

**Design:**
- Add an optional `store.RateLimitOverrideStore` parameter to the `Middleware` constructor (nil = use defaults only, preserves backward compatibility):
  ```go
  func Middleware(
      limiter store.RateLimiter,
      cfg config.RateLimitConfig,
      overrides store.RateLimitOverrideStore, // nil-safe
      col *metrics.Collector,
      logger *slog.Logger,
  ) func(http.Handler) http.Handler
  ```
- On each request, after extracting `identity`, iterate `identity.Roles` and find the highest (most permissive) override:
  ```go
  rpm, rpd := cfg.Default.RPM, cfg.Default.RPD
  if overrides != nil {
      for _, role := range identity.Roles {
          if o, err := overrides.GetByRole(ctx, role); err == nil {
              if o.RPM > rpm { rpm = o.RPM }
              if o.RPD > rpd { rpd = o.RPD }
          }
      }
  }
  ```
- Pass the resolved `rpm`/`rpd` to `limiter.Allow(ctx, userID, rpm, rpd)`.
- Wire the `RateLimitOverrideStore` when constructing the middleware in `server.buildRouter()`.

**Acceptance:** E2E test: seed a `rate_limit_overrides` row for role `admin` with `rpm=1000`; seed a default user (role `developer`) with default `rpm=60`. Send 70 requests as the developer → get 429 after 60. Send 70 requests as admin → all succeed. Cache invalidation: update the override row via `PUT /api/v1/admin/roles/{role}/rate-limits`, verify the new limit takes effect within the next request.

---

#### A4 — Per-role rate limit admin endpoint

**Files:** `internal/admin/api/roles.go`, `internal/admin/api/router.go`

**Problem:** `RateLimitOverrideStore` has no admin API surface. Operators cannot set per-role limits without direct DB access.

**Design:**
- Add two handlers to `roleHandler` (it already holds `store.RoleStore`; add `overrides store.RateLimitOverrideStore`):
  ```
  GET  /api/v1/admin/roles/{role}/rate-limits   → GetByRole (404 if no override)
  PUT  /api/v1/admin/roles/{role}/rate-limits   → Upsert
  ```
  Request body for PUT:
  ```json
  { "rpm": 1000, "rpd": 50000 }
  ```
  Response (GET and PUT):
  ```json
  { "role_name": "admin", "rpm": 1000, "rpd": 50000, "updated_at": "..." }
  ```
- Wire in `router.go` under the existing `/roles/{id}` group:
  ```go
  r.Get("/rate-limits", rh.getRateLimits)
  r.Put("/rate-limits", rh.putRateLimits)
  ```
- `roleHandler` receives `overrides store.RateLimitOverrideStore` as a new constructor parameter.
- Add corresponding `useRoleLimits` query and `usePutRoleLimits` mutation to `web/src/api/queries.ts` for use by the Roles page.

**Acceptance:** integration test: `PUT /api/v1/admin/roles/developer/rate-limits` → 200 with correct body. `GET /api/v1/admin/roles/developer/rate-limits` → 200. `GET /api/v1/admin/roles/nonexistent/rate-limits` → 404.

---

### Track B — Frontend carry-overs (4 tasks)

#### B1 — Log stream panel

**File:** `web/src/pages/logs/LogsListPage.tsx`

**Design:** Add a collapsible `LogStreamPanel` section below the existing pagination table. The panel is a distinct visual block, not a replacement for the table.

**UI structure:**
```
[ Filters bar ]
[ Paginated table  ]        ← existing
─────────────────────────
▼ Live stream  ● Connected  [ Pause ]    ← new collapsible header
[ scrollable log feed, newest at top, max 100 entries ]
```

**Implementation:**
- `useWebSocket` hook is already production-ready. Use it:
  ```tsx
  const { connected, send } = useWebSocket({
    url: `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/v1/admin/logs/stream`,
    enabled: streamOpen,
    onMessage: (data) => setStreamEntries(prev => [data as AuditLogEntry, ...prev].slice(0, 100)),
  })
  ```
- On each filter change, send the current filter to the server:
  ```tsx
  useEffect(() => {
    if (connected && streamOpen) send({ search, status: statusFilter, mcp_method: methodFilter })
  }, [connected, streamOpen, search, statusFilter, methodFilter, send])
  ```
- `streamOpen` state (boolean) drives the collapsible panel. Persisted to `sessionStorage` so the panel state survives page navigation within the same tab.
- Pause/Resume: when paused, `onMessage` is a no-op (new entries are silently dropped). The WebSocket connection stays open.
- Connection indicator: green dot when `connected`, amber when reconnecting (`!connected && streamOpen`).
- Helper text below the toggle: _"The live stream shows entries as they arrive. Filters applied above are sent to the server."_

---

#### B2 — Log export button

**File:** `web/src/pages/logs/LogsListPage.tsx`

**Design:** Two buttons in the page header: `Export CSV` and `Export JSON`. Clicking either triggers a browser file download without a round-trip through React state.

**Implementation:**
```tsx
function buildExportURL(format: 'csv' | 'json') {
  const params = new URLSearchParams()
  params.set('format', format)
  if (search)       params.set('search', search)
  if (statusFilter) params.set('status', statusFilter)
  if (methodFilter) params.set('mcp_method', methodFilter)
  return `/api/v1/admin/logs/export?${params}`
}

// In JSX header:
<a href={buildExportURL('csv')} download>
  <Button variant="secondary" size="sm">Export CSV</Button>
</a>
<a href={buildExportURL('json')} download>
  <Button variant="secondary" size="sm">Export JSON</Button>
</a>
```

The `<a download>` pattern lets the browser handle the download without any JavaScript fetch machinery. The backend already streams the response with `Content-Disposition: attachment`.

Note: export respects the current filter state but ignores pagination — the backend returns all matching entries.

---

#### B3 — Transport-aware server form

**File:** `web/src/pages/servers/ServersListPage.tsx`

**Problem:** The current form shows a raw JSON textarea for `config`. Users must know the exact key names per transport. This is fragile.

**Design:** Replace the textarea with dynamic, transport-specific form fields. The serialised JSON is constructed before submission, not edited directly.

| Transport | Fields |
|-----------|--------|
| `streamable_http` | **Endpoint URL** (text input, `placeholder="https://mcp.example.com/mcp"`) |
| `sse` | **SSE Endpoint URL** + **Message Endpoint URL** (two text inputs) |
| `stdio` | **Command** (text input, `placeholder="npx"`) + **Arguments** (comma-separated text input, hint shown, split on submit) |

**Implementation:**
```tsx
// Replace the single `config: string` field with typed fields:
interface ServerFormState {
  name: string
  description: string
  transport: MCPServer['transport']
  is_enabled: boolean
  // transport-specific
  endpoint: string          // streamable_http
  sse_endpoint: string      // sse
  message_endpoint: string  // sse
  command: string           // stdio
  args_raw: string          // stdio — comma-separated, split on submit
}

function formToConfig(form: ServerFormState): Record<string, unknown> {
  switch (form.transport) {
    case 'streamable_http':
      return { endpoint: form.endpoint }
    case 'sse':
      return { sse_endpoint: form.sse_endpoint, message_endpoint: form.message_endpoint }
    case 'stdio':
      return {
        command: form.command,
        args: form.args_raw.split(',').map(s => s.trim()).filter(Boolean),
      }
  }
}

function configToForm(s: MCPServer): Partial<ServerFormState> {
  const c = s.config
  return {
    endpoint:         (c.endpoint ?? c.url ?? '') as string,
    sse_endpoint:     (c.sse_endpoint ?? '') as string,
    message_endpoint: (c.message_endpoint ?? '') as string,
    command:          (c.command ?? '') as string,
    args_raw:         Array.isArray(c.args) ? (c.args as string[]).join(', ') : '',
  }
}
```

Show only the fields relevant to the currently selected transport (conditional rendering on `form.transport`). Add `placeholder` and `helperText` to each field explaining the expected value.

**Backward compatibility:** `configToForm` reads both `"endpoint"` and `"url"` (the server-side alias) so existing `streamable_http` servers with `url` in their config open correctly in the edit modal.

---

#### B4 — Visual policy matrix editor

**File:** `web/src/pages/roles/RolesListPage.tsx` (extract `PolicyMatrixEditor` component)

**Design:** Replace the three-dropdown add-policy form with a 2D grid. The matrix is **role-scoped**: a role selector dropdown at the top sets the context; the grid below shows all known servers as rows and all actions as columns.

**Grid structure:**
```
Role: [ developer ▾ ]

                     tools/list   tools/call   resources/read   prompts/get   * (all)
  github              ☑            ☑            ☐                ☐            ☐
  filesystem          ☑            ☐            ☑                ☐            ☐
  * (all servers)     ☐            ☐            ☐                ☐            ☐

  ℹ Unchecked cells are implicitly denied. Only allow rules are stored.
    Granting * (all) overrides all per-action settings for that row.
```

**Data sources:**
- Rows: servers from `useServers()` + one fixed `*` wildcard row
- Columns: fixed set `['tools/list', 'tools/call', 'resources/read', 'prompts/get', '*']`
- Cell state: derived from `usePolicies()` filtered to `selected role` — a cell is checked if a policy `(role, server, action)` exists

**Interaction:**
- Checking a cell → `POST /api/v1/admin/policies` with `{ role, resource: serverName, action }`
- Unchecking a cell → `DELETE /api/v1/admin/policies` with the same body
- Each toggle is an immediate optimistic update; on error the cell reverts
- Helper text below grid (always visible): _"Unchecked cells are implicitly denied — mcpgate stores only allow rules. Granting `*` for a server allows all actions for that role on that server."_

**Component structure:**
```tsx
// Extract to web/src/components/policy/PolicyMatrixEditor.tsx
export function PolicyMatrixEditor({ roles }: { roles: Role[] })
```

This is extracted from `RolesListPage` to keep the page file manageable. The role selector lives inside the component (local state).

**Conflict handling:** if both `(role, server, *)` and `(role, server, tools/call)` exist, the `*` row shows as checked and individual action cells are shown as checked but visually dimmed with a tooltip _"Covered by the * (all) policy"_.

---

### Track C — New pages (4 tasks)

#### C1 — Metrics page

**New file:** `web/src/pages/metrics/MetricsPage.tsx`
**New route:** `/console/metrics` in `router.tsx`
**New sidebar link:** "Metrics" in `AppShell.tsx`

**Design:** A dedicated page for time-series operational data, separate from the Dashboard (which shows real-time point-in-time snapshots).

**Layout:**
```
[ 1h | 6h | 24h | 7d ]   ← date-range tabs (local state, no URL sync)

Request Volume          Error Rate           p95 Latency
[ Line chart ]          [ Line chart ]       [ Line chart ]

Per-server breakdown
Server: [ github ▾ ]
[ Volume line chart for selected server ]
```

**Implementation:**
- Date-range tabs drive a `window` state (`'1h' | '6h' | '24h' | '7d'`). On change, refetch timeseries with the new window param.
- `useMetricsSummary` (existing hook) powers the three summary stat cards at the top.
- `useMetricsTimeseries(window)` (new hook) powers the line charts:
  ```ts
  export const useMetricsTimeseries = (window: string) =>
    useQuery({
      queryKey: ['metrics', 'timeseries', window],
      queryFn: () => apiFetch<{ window: string; buckets: { timestamp: number; requests: number }[] }>(
        `/metrics/timeseries?window=${window}`
      ),
      refetchInterval: 30_000,
    })
  ```
- Charts use Recharts `LineChart` + `ResponsiveContainer` (same library as `SparklineChart`, just a richer component). No new dependencies.
- Per-server breakdown: a server dropdown (populated from `useServers()`) filters the chart data client-side — the timeseries endpoint returns aggregate counts, so client-side filtering by server name is not possible from the current API. The server breakdown chart is a placeholder that displays the aggregate data with a note _"Per-server breakdown requires the timeseries backend to emit server-labelled buckets (Sprint 9 backend item)"_ until the backend is extended.
- `refetchInterval: 30_000` keeps the page live without a manual refresh.

**New type:**
```ts
// web/src/api/types.ts addition
export interface TimeseriesBucket { timestamp: number; requests: number }
export interface MetricsTimeseries { window: string; buckets: TimeseriesBucket[] }
```

---

#### C2 — SAML IdP config page

**New file:** `web/src/pages/idp/IdpListPage.tsx`
**New route:** `/console/idp`
**New sidebar link:** "IdP Config" in `AppShell.tsx`

**Design:** A CRUD table for SAML IdP configurations, styled identically to `ServersListPage`. This is the primary way operators will manage IdP configs without direct DB access.

**Table columns:** Name · Entity ID · SSO URL (truncated) · Enabled badge · Actions (Edit / Delete)

**Create/Edit modal fields:**

| Field | Input type | Notes |
|-------|-----------|-------|
| Name | text | unique identifier, e.g. `auth0-dev` |
| Entity ID | text | IdP's EntityID from SAML metadata |
| SSO URL | text | IdP's SSO endpoint |
| Certificate (PEM) | `<textarea>` | Paste PEM block; `placeholder="-----BEGIN CERTIFICATE-----"` |
| Attribute map | key-value pair editor | Two columns: IdP claim URI → internal field name. Start with two rows (email, display_name) |
| Enabled | checkbox | |

**Attribute map editor:** a small sub-component — a list of `[key input] → [value input]` pairs with `+ Add mapping` and `× Remove` buttons. Serialised to `Record<string, string>` on submit.

**SP metadata download link:** a static `<a href="/auth/saml/metadata" target="_blank">Download SP Metadata XML</a>` link in the page header. This lets operators copy the SP metadata to the IdP without leaving the console.

**New query hooks:**
```ts
export const useIdps = () => useQuery({ queryKey: ['idps'], queryFn: () => apiFetch<{ idps: SAMLIdpConfig[] }>('/idps') })
export const useCreateIdp = () => { ... }   // POST /idps → invalidate ['idps']
export const useUpdateIdp = () => { ... }   // PUT /idps/{id}
export const useDeleteIdp = () => { ... }   // DELETE /idps/{id}
```

**New type:**
```ts
export interface SAMLIdpConfig {
  id: string
  name: string
  entity_id: string
  sso_url: string
  certificate: string
  attribute_map: Record<string, string>
  is_enabled: boolean
  created_at: string
  updated_at: string
}
```

---

#### C3 — JWKS config page

**New file:** `web/src/pages/jwks/JwksListPage.tsx`
**New route:** `/console/jwks`
**New sidebar link:** "M2M / JWKS" in `AppShell.tsx`

**Design:** Same CRUD pattern as `IdpListPage`. JWKS configs represent M2M issuers (Auth0, Okta, Vault).

**Table columns:** Name · Issuer · JWKS URL (truncated) · Audience · Enabled badge · Actions

**Create/Edit modal fields:**

| Field | Input type | Notes |
|-------|-----------|-------|
| Name | text | e.g. `auth0-m2m` |
| Issuer | text | `https://dev-xxx.us.auth0.com/` |
| JWKS URL | text | `https://dev-xxx.us.auth0.com/.well-known/jwks.json` |
| Audience | text | `https://api.mcpgate.internal` |
| Enabled | checkbox | |

**New query hooks:**
```ts
export const useJwksConfigs = () => useQuery({ queryKey: ['jwks'], queryFn: () => apiFetch<{ configs: JWKSConfig[] }>('/jwks/configs') })
export const useCreateJwksConfig = () => { ... }
export const useUpdateJwksConfig = () => { ... }
export const useDeleteJwksConfig = () => { ... }
```

**New type:**
```ts
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
```

---

#### C4 — Dark mode toggle

**Files:** `web/src/index.css`, `web/src/components/layout/AppShell.tsx`, all UI primitives in `web/src/components/ui/`

**Design:** CSS-variable-based theming. Tailwind's `dark:` variant is already enabled in Tailwind v4. No new dependencies.

**Implementation:**
1. In `index.css`, define CSS custom properties for semantic colour tokens:
   ```css
   :root {
     --bg-page: #f9fafb;
     --bg-card: #ffffff;
     --text-primary: #111827;
     --text-secondary: #6b7280;
     --border: #e5e7eb;
   }
   .dark {
     --bg-page: #0f172a;
     --bg-card: #1e293b;
     --text-primary: #f1f5f9;
     --text-secondary: #94a3b8;
     --border: #334155;
   }
   ```
2. `AppShell` header: add a sun/moon icon button that toggles `document.documentElement.classList.toggle('dark')` and persists the preference to `localStorage`:
   ```tsx
   function useDarkMode() {
     const [dark, setDark] = useState(() => localStorage.getItem('theme') === 'dark')
     const toggle = () => {
       const next = !dark
       setDark(next)
       localStorage.setItem('theme', next ? 'dark' : 'light')
       document.documentElement.classList.toggle('dark', next)
     }
     useEffect(() => { document.documentElement.classList.toggle('dark', dark) }, [])
     return { dark, toggle }
   }
   ```
3. Audit all 9 UI components and 8 pages: replace hardcoded `text-gray-900`, `bg-white`, `border-gray-200` etc. with the `dark:` variants (`dark:text-slate-100`, `dark:bg-slate-800`, etc.) or reference the CSS custom properties where Tailwind classes don't suffice.
4. Charts (`SparklineChart`, new Recharts `LineChart`): set `stroke` and `fill` colours from CSS variables via `getComputedStyle` so they adapt to dark mode.

---

### Track D — Testing (3 tasks)

#### D1 — Vitest setup + smoke tests

**New files:** `web/vitest.config.ts`, `web/src/test/setup.ts`, `web/src/test/mocks/handlers.ts`, one `*.test.tsx` per page and per UI component.

**Config:**
```ts
// web/vitest.config.ts
import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

export default defineConfig({
  plugins: [react()],
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    css: false,
  },
})
```

```ts
// web/src/test/setup.ts
import '@testing-library/jest-dom'
import { server } from './mocks/server'
beforeAll(() => server.listen())
afterEach(() => server.resetHandlers())
afterAll(() => server.close())
```

**MSW mock handlers** (`web/src/test/mocks/handlers.ts`): one `http.get` handler per query endpoint returning minimal fixture data. Shared across all tests.

**Smoke test scope (one test file per target):**

| File | Tests |
|------|-------|
| `DashboardPage.test.tsx` | Renders without crash. Shows "Loading" then renders metric cards. |
| `ServersListPage.test.tsx` | Renders table. "Add server" opens modal. Transport change shows correct fields. |
| `UsersListPage.test.tsx` | Renders table. Click user opens drawer. |
| `RolesListPage.test.tsx` | Renders roles. Policy matrix renders with correct columns. |
| `LogsListPage.test.tsx` | Renders table. Export links have correct `href`. Live stream toggle shows/hides panel. |
| `MetricsPage.test.tsx` | Renders without crash. Date-range tabs change `window` param. |
| `IdpListPage.test.tsx` | Renders table. "Add IdP" opens modal. |
| `JwksListPage.test.tsx` | Renders table. "Add config" opens modal. |
| `SettingsPage.test.tsx` | Renders form fields. |
| `Button.test.tsx` | Renders children. `loading` prop shows spinner. `disabled` prevents click. |
| `Modal.test.tsx` | Hidden when `open=false`. Renders children when `open=true`. |
| `Table.test.tsx` | Renders correct number of rows. |
| `Badge.test.tsx` | Renders with correct colour class. |

**Makefile addition:**
```makefile
test-frontend:
    cd web && npx vitest run
```

**`ci-fast` update:** add `test-frontend` to the `ci-fast` target so smoke tests run on every push alongside lint + unit + build.

---

#### D2 — Playwright E2E (4 core journeys)

**New files:** `web/playwright.config.ts`, `web/e2e/dashboard.spec.ts`, `web/e2e/servers.spec.ts`, `web/e2e/roles.spec.ts`, `web/e2e/logs.spec.ts`

**Config:**
```ts
// web/playwright.config.ts
import { defineConfig } from '@playwright/test'
export default defineConfig({
  testDir: './e2e',
  use: {
    baseURL: 'http://localhost:8080',
    // Auth handled via cookie set in beforeAll — see below
  },
  webServer: {
    command: 'make dev',
    url: 'http://localhost:8080/health',
    reuseExistingServer: true,
  },
})
```

**Auth strategy:** Playwright tests run against a live dev stack. A `beforeAll` fixture calls a test-only endpoint (`GET /api/v1/dev/test-token` — added only when `MCPGATE_DEV_MODE=true`) that returns a pre-seeded admin JWT; the fixture sets it as the `mcpgate_session` cookie. This keeps tests fast and avoids a real SAML round-trip.

**The four journeys:**

| File | Journey | Steps |
|------|---------|-------|
| `dashboard.spec.ts` | Dashboard loads live data | Navigate to `/console/dashboard` → verify metric cards visible → verify sparkline chart rendered |
| `servers.spec.ts` | Server CRUD | Create streamable_http server → verify in table → edit endpoint URL → delete → verify gone |
| `roles.spec.ts` | Policy matrix | Select role → check a cell → verify policy added via API → uncheck → verify removed |
| `logs.spec.ts` | Log filter + export | Filter by status → verify table updates → click Export CSV → verify download triggered |

**Makefile addition:**
```makefile
test-e2e:
    cd web && npx playwright test
```

---

#### D3 — WebSocket auth integration test (backend)

**File:** `tests/e2e/logging_test.go` (add a new test function)

**Test:** `TestE2E_WebSocket_SessionExpiry_ClosesConnection`

**Steps:**
1. Spin up test server with `SessionTTL: 2 * time.Second` (short TTL for test speed).
2. Authenticate via test SAML harness → get session cookie.
3. Open WebSocket connection to `/api/v1/admin/logs/stream` with the cookie → verify connection is established (receive initial open frame or send filter message and get echo).
4. `time.Sleep(3 * time.Second)` — session TTL elapses; Redis session key expires.
5. Verify the server closes the WebSocket connection (the server-side `stream` handler should re-check session validity on each receive, or use a ticker to periodically re-validate).

**Backend note:** the WS `stream` handler currently validates the session cookie only at upgrade time. If it does not periodically re-validate, this test will surface the gap. Implementing a 60-second session re-validation ticker in the `stream` handler is part of this task (the check calls `sessionCache.Get(ctx, sessionID)` and closes the WS with `CloseGoingAway` on miss).

### Track E — Local admin account (2 tasks)

#### E1 — Backend: migration, auth endpoint, seed command

**New files:** `internal/store/postgres/migrations/006_local_auth.up.sql`, `006_local_auth.down.sql`, `internal/auth/local/handler.go`
**Modified files:** `cmd/mcpgate/seed.go` (or equivalent seed entrypoint), `internal/server/server.go`

**Migration 006 — `password_hash` column:**
```sql
-- 006_local_auth.up.sql
ALTER TABLE users ADD COLUMN password_hash TEXT;
-- Nullable: SAML-provisioned users always have NULL here.
-- Local users have a bcrypt hash. The column presence does not
-- change any existing queries — NULL is safe in all SELECT paths.
```
```sql
-- 006_local_auth.down.sql
ALTER TABLE users DROP COLUMN IF EXISTS password_hash;
```

**`UserStore` addition** — one new method on the existing interface:
```go
// internal/store/interfaces.go
SetPasswordHash(ctx context.Context, userID uuid.UUID, hash string) error
GetByEmailWithHash(ctx context.Context, email string) (*User, string, error)
// Returns user + raw hash together in one query to avoid a second round-trip.
```

**`POST /auth/local/login` handler** (`internal/auth/local/handler.go`):
- Route: `POST /auth/local/login` — mounted in `server.buildRouter()` alongside the SAML routes, **not** behind auth middleware.
- Request body: `{"email": "admin@local", "password": "..."}`.
- Logic:
  1. `GetByEmailWithHash(ctx, email)` — if `ErrNotFound` or `password_hash` is empty: return `401 "invalid credentials"` (same error for both — no user enumeration).
  2. Verify `idp_name == "local"` — refuse login for SAML-provisioned users even if they somehow have a hash set.
  3. `bcrypt.CompareHashAndPassword(hash, []byte(password))` — on mismatch: `401 "invalid credentials"`.
  4. Check `user.Status == "active"` — `403 "account suspended"` if not.
  5. Fetch roles via `roleStore.GetUserRoles`, create session (Postgres + Redis), issue JWT, set `mcpgate_session` cookie — identical to the SAML ACS handler post-assertion path.
  6. Return `302` redirect to `/console/dashboard`.
- No brute-force guard in Sprint 8 (deferred — see §7). Rate limiting by the global rate limiter applies at the IP level.

**`make seed` extension** — when seeding, check whether a local admin user already exists. If not, create one:
```go
// Pseudocode in seed command
password := generateSecurePassword()   // crypto/rand, 16 chars, mixed case + digits + symbols
hash, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
user := &store.User{
    ID:          uuid.New(),
    Email:       "admin@local",
    DisplayName: "Local Admin",
    IDPName:     "local",
    IDPSubject:  "local-admin",
    Status:      "active",
    Metadata:    map[string]any{},
}
userStore.Create(ctx, user)
userStore.SetPasswordHash(ctx, user.ID, string(hash))
roleStore.AssignToUser(ctx, user.ID, adminRoleID)
```
Print credentials to stdout once, clearly:
```
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
  Local admin account created
  Email:    admin@local
  Password: xK9#mP2$vL7!rQ4n

  Log in at: http://localhost:8080/auth/local/login

  ⚠  Store this password securely. It will not be shown again.
     Disable this account once your SAML admin is set up.
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
```
If the local admin user already exists (idempotent re-run), skip creation silently — do not regenerate or print the password.

**`make setup-dev` update** — `setup-dev` already calls `seed`; no additional wiring needed. The local admin credentials appear automatically on first `make setup-dev`.

**Acceptance:** `make seed` on a clean DB prints credentials. `POST /auth/local/login` with correct credentials → `mcpgate_session` cookie set → redirect to `/console/dashboard`. Wrong password → 401. Suspended user → 403. SAML user with matching email → 401 (idp_name guard). Second `make seed` → no duplicate user created.

---

#### E2 — Frontend: local login page

**New file:** `web/src/pages/auth/LocalLoginPage.tsx`
**New route:** `/auth/local/login` in `router.tsx`
**Modified:** `web/src/api/client.ts` — update the 401 redirect to offer both auth paths

**Login page design:**
```
┌──────────────────────────────────┐
│         mcpgate                  │
│                                  │
│  Sign in                         │
│                                  │
│  Email ________________________  │
│  Password ____________________   │
│                                  │
│  [ Sign in ]                     │
│                                  │
│  ── or ──                        │
│                                  │
│  [ Sign in with SSO →  ]         │
│                                  │
│  ℹ Use your local admin account  │
│    or your organisation's        │
│    identity provider.            │
└──────────────────────────────────┘
```

**Implementation:**
```tsx
// web/src/pages/auth/LocalLoginPage.tsx
export function LocalLoginPage() {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    setLoading(true)
    setError(null)
    try {
      // POST to /auth/local/login — not /api/v1/admin, so apiFetch BASE doesn't apply
      const res = await fetch('/auth/local/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ email, password }),
        redirect: 'follow',
      })
      if (res.ok || res.redirected) {
        window.location.href = '/console/dashboard'
      } else {
        const body = await res.json().catch(() => ({}))
        setError(body.error ?? 'Invalid email or password')
      }
    } catch {
      setError('Could not reach the server')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center bg-gray-50 dark:bg-slate-900">
      <div className="w-full max-w-sm space-y-6 rounded-xl bg-white dark:bg-slate-800 p-8 shadow">
        <h1 className="text-xl font-semibold text-gray-900 dark:text-slate-100">Sign in</h1>
        <form onSubmit={handleSubmit} className="space-y-4">
          <Input label="Email" type="email" value={email} onChange={e => setEmail(e.target.value)} required />
          <Input label="Password" type="password" value={password} onChange={e => setPassword(e.target.value)} required />
          {error && <p className="text-xs text-red-500">{error}</p>}
          <Button type="submit" loading={loading} className="w-full">Sign in</Button>
        </form>
        <div className="relative">
          <div className="absolute inset-0 flex items-center"><div className="w-full border-t border-gray-200 dark:border-slate-700" /></div>
          <div className="relative flex justify-center text-xs text-gray-400">or</div>
        </div>
        <a href="/auth/saml/login?redirect_to=/console/dashboard">
          <Button variant="secondary" className="w-full">Sign in with SSO</Button>
        </a>
        <p className="text-center text-xs text-gray-400">
          Use your local admin account or your organisation's identity provider.
        </p>
      </div>
    </div>
  )
}
```

**401 redirect update** in `web/src/api/client.ts`: instead of redirecting directly to `/auth/saml/login`, redirect to the local login page which offers both options:
```ts
// before
window.location.href = '/auth/saml/login?redirect_to=/console/dashboard'
// after
window.location.href = '/auth/local/login'
```
The login page's "Sign in with SSO" button then sends the user to SAML. This gives operators a choice rather than forcing SAML for all 401 redirects.

**Route:** the `/auth/local/login` route lives outside the `/console` mount, consistent with `/auth/saml/login`. It is a public route — no auth middleware.

**Acceptance:** navigating to `http://localhost:8080/auth/local/login` shows the login form without requiring authentication. Submitting correct credentials → session cookie set → redirect to `/console/dashboard`. Submitting wrong credentials → inline error message, form stays open. "Sign in with SSO" link initiates SAML flow. The page renders correctly in both light and dark mode.

---

## 4. New Package / File Structure

```
web/src/
├── pages/
│   ├── auth/
│   │   └── LocalLoginPage.tsx        new — E2
│   ├── metrics/
│   │   └── MetricsPage.tsx           new — C1
│   ├── idp/
│   │   └── IdpListPage.tsx           new — C2
│   └── jwks/
│       └── JwksListPage.tsx          new — C3
├── components/
│   └── policy/
│       └── PolicyMatrixEditor.tsx    new — B4 (extracted from RolesListPage)
├── test/
│   ├── setup.ts                      new — D1
│   └── mocks/
│       ├── handlers.ts               new — D1
│       └── server.ts                 new — D1
web/
├── vitest.config.ts                  new — D1
├── e2e/
│   ├── dashboard.spec.ts             new — D2
│   ├── servers.spec.ts               new — D2
│   ├── roles.spec.ts                 new — D2
│   └── logs.spec.ts                  new — D2
└── playwright.config.ts              new — D2

internal/
├── auth/
│   └── local/
│       └── handler.go                new — E1 (local login handler)
├── metrics/
│   └── collector.go                  modified — A1 (Redis timeseries)
├── ratelimit/
│   └── middleware.go                 modified — A2, A3
└── admin/api/
    └── roles.go                      modified — A4 (rate-limits endpoints)

internal/store/
├── interfaces.go                     modified — E1 (SetPasswordHash, GetByEmailWithHash)
└── postgres/
    ├── users.go                      modified — E1 (implement new UserStore methods)
    └── migrations/
        ├── 006_local_auth.up.sql     new — E1
        └── 006_local_auth.down.sql   new — E1
```

Files modified (frontend):
- `web/src/api/client.ts` — E2: 401 redirect changed from `/auth/saml/login` to `/auth/local/login`
- `web/src/api/queries.ts` — new hooks: `useMetricsTimeseries`, `useIdps`, `useCreateIdp`, `useUpdateIdp`, `useDeleteIdp`, `useJwksConfigs`, `useCreateJwksConfig`, `useUpdateJwksConfig`, `useDeleteJwksConfig`, `useRoleLimits`, `usePutRoleLimits`
- `web/src/api/types.ts` — new types: `MetricsTimeseries`, `TimeseriesBucket`, `SAMLIdpConfig`, `JWKSConfig`
- `web/src/router.tsx` — four new routes: `/auth/local/login`, `/console/metrics`, `/console/idp`, `/console/jwks`
- `web/src/components/layout/AppShell.tsx` — three new sidebar links + dark mode toggle
- `web/src/pages/logs/LogsListPage.tsx` — B1 (stream panel), B2 (export buttons)
- `web/src/pages/servers/ServersListPage.tsx` — B3 (transport-aware form)
- `web/src/pages/roles/RolesListPage.tsx` — B4 (replace dropdown form with matrix import)
- `web/src/index.css` — dark mode CSS variables
- All `web/src/components/ui/*.tsx` — `dark:` variants

---

## 5. Implementation Guidelines

### Backend

- **Redis key hygiene:** metrics timeseries keys follow the pattern `metrics:timeseries:{unix_minute}`. Use `Expire` (not `PExpire`) with a 7-day TTL on every write. Keys expire automatically; no background cleanup goroutine is needed.
- **Nil-safe stores:** the `overrides` parameter to `ratelimit.Middleware` must be nil-safe throughout. No panics if the store is not wired.
- **Rate limit resolution:** "most permissive wins" — always pick the highest RPM and RPD across all role overrides. Never let an override lower a user's limit (that requires an explicit deny rule at the Casbin level, not the rate limiter).
- **WS session re-validation:** add a `time.NewTicker(60 * time.Second)` inside the `stream` handler's `select` loop. On each tick, call `sessionCache.Get`; on miss, write a WebSocket `CloseGoingAway` frame and return.
- **Local auth — bcrypt cost:** use `bcrypt.DefaultCost` (12) for password hashing in both seed and the login handler. Do not use a lower cost even in dev — the seed command runs once, not in a hot path.
- **Local auth — no user enumeration:** the login handler must return the same `401 "invalid credentials"` response whether the email does not exist, the user has no password hash, or the password is wrong. Never reveal which condition failed.
- **Local auth — idp_name guard:** `POST /auth/local/login` must check `user.IDPName == "local"` before attempting password verification. SAML-provisioned users (`idp_name = "auth0-dev"` etc.) must be rejected even if they somehow have a `password_hash` value set.
- **Local auth — seed idempotency:** the seed command checks for an existing user with `email = "admin@local"` before creating. If found, it logs `"local admin already exists, skipping"` and exits cleanly without touching the existing password hash.

### Frontend

- **No new dependencies.** Recharts, React Hook Form, Zod, MSW, and Tailwind are all already installed. Do not add packages without discussion.
- **Component extraction threshold:** any component over ~120 lines or containing its own data-fetching logic should be extracted to its own file under `web/src/components/` or a sub-folder within `pages/`.
- **Query key conventions:** follow the existing pattern — `['servers']`, `['servers', id]`, `['idps']`, `['jwks']`, `['metrics', 'summary']`, `['metrics', 'timeseries', window]`, `['policies']`.
- **Dark mode:** use `dark:` Tailwind variants, not inline styles. CSS custom properties are used only for values Tailwind cannot express (e.g. chart stroke colours via `getComputedStyle`).
- **Error handling:** every mutation that can fail shows an inline error message (the `formError` + `<p className="text-xs text-red-500">` pattern already used in `ServersListPage`). No silent failures.
- **Optimistic updates:** the policy matrix uses optimistic updates (toggle cell immediately, revert on error) to feel responsive. Other mutations (create/edit/delete) do not use optimistic updates — they wait for the server response and then `invalidateQueries`.

### Testing

- **MSW handlers** return the minimal shape needed by each component. No deep fixture nesting.
- **Smoke tests** use `render(<Component />)` wrapped in the necessary providers (`QueryClientProvider`, `RouterProvider`). A `TestWrapper` component should be defined once in `setup.ts` and reused.
- **Playwright** tests must be deterministic. Any test that depends on existing data (e.g. a server named "test") should create that data in `beforeAll` and delete it in `afterAll`.
- **Dev-mode test token endpoint:** the `GET /api/v1/dev/test-token` endpoint must be guarded by a build tag or an `MCPGATE_DEV_MODE` env var check. It must never be compiled into a production binary.

---

## 6. Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Unchecked policy matrix cell = implicit deny (no explicit deny rule stored) | Casbin stores only allow rules. `DefaultAction: deny` already denies anything without an allow rule. An explicit deny toggle would require a model change. Agreed with user. |
| D2 | Per-role rate limit override: most-permissive-wins resolution | Users with multiple roles should not be penalised to the most-restrictive role's limit. Operators who want stricter limits should not grant the user a more permissive role. |
| D3 | Metrics timeseries: Redis per-minute INCR counters (not Prometheus remote-write) | Avoids adding a Prometheus remote storage dependency. The Redis approach is consistent with the existing Redis infrastructure and supports the 7-day window needed by the UI. |
| D4 | Per-server metrics chart is client-side placeholder | The timeseries endpoint emits aggregate totals, not per-server buckets. Extending the backend to label by server name is deferred (Sprint 9 backend item). The UI shows a placeholder rather than silently showing wrong data. |
| D5 | Playwright auth via dev-mode test-token endpoint | A real SAML round-trip requires a live Auth0 connection, which is unavailable in CI. A `MCPGATE_DEV_MODE` guard keeps the endpoint out of production binaries. |
| D6 | Dark mode uses `dark:` Tailwind variants + CSS custom properties | Tailwind v4 has first-class `dark:` support. Custom properties handle the cases (chart colours) that Tailwind cannot reach. No theming library needed. |
| D7 | `vitest` added to `ci-fast` | Smoke tests are fast (<5s) and catch regressions without Docker. Full E2E (Playwright) requires a live stack and stays in `make test-e2e`. |
| D8 | WS session re-validation on 60s ticker | The upgrade-time-only check allows sessions that expire mid-stream to remain connected indefinitely. A 60s ticker bounds the exposure window without hammering Redis on every received message. |
| D9 | Local admin account over `MCPGATE_BOOTSTRAP_ADMIN_EMAIL` env var | The env var approach still requires SAML to be working before the first admin can log in. The local account is SAML-independent and accessible to operators with no programmatic experience. Agreed with user. |
| D10 | 401 redirect goes to `/auth/local/login` (not directly to SAML) | A unified login page offering both local and SSO options is more transparent to users. Operators who only have the local admin account would otherwise be confused by a redirect to an SSO provider. |
| D11 | Local account identified by `idp_name = "local"` (not a separate table) | Reuses the existing `users` table and all downstream auth infrastructure (sessions, JWT, roles). A separate table would duplicate schema and complicate JIT provisioning logic. The `idp_name` discriminator is already used to distinguish SAML providers. |
| D12 | Password generated by `make seed`, not user-chosen | User-chosen passwords at seed time require a CLI prompt, complicating scripted environments. A strong random password printed once to stdout is consistent with the pattern used by tools like Grafana and Gitea on first run. |

---

## 7. Deferred Items

The following items are explicitly out of scope for Sprint 8. They are captured here and will be promoted to Sprint 9.

| Label | Item | Reason |
|-------|------|--------|
| S8-D1 | Per-server timeseries metrics in the backend | Requires adding a `server_name` label to the Redis counter key and updating the timeseries handler to return per-server buckets. Deferred until the Metrics page UX is validated first. |
| S8-D2 | Playwright CI integration | The dev-mode test-token endpoint and a live-stack CI job need infrastructure work (GitHub Actions service containers for Postgres + Redis, Auth0 credentials in CI secrets). Deferred to a dedicated CI sprint. |
| S8-D3 | Deeper Vitest coverage (form validation, hook error states, API hook retry behaviour) | Smoke tests catch regressions without setup overhead. Deeper tests are valuable but require MSW handler complexity. Captured as a TODO in stage-8 notes. |
| S8-D4 | SAML IdP setup wizard (5-step with "Test Login" popup) | The full wizard from the dev plan is significantly more complex than a CRUD form. Sprint 8 delivers the CRUD form; the wizard is Sprint 9. |
| S8-D5 | OAuth/JWKS client secrets shown once on creation | The current `JWKSConfig` model has no client secret. If Auth0 M2M client secret rotation is needed, it requires a new secret-management endpoint. Deferred. |
| S8-D6 | OpenAPI spec generation | `types.ts` is maintained by hand. Auto-generating from Go handler annotations (e.g. via `swaggo/swag`) is a Sprint 9 quality item. |
| S8-D7 | Accessibility audit (axe-core, keyboard nav, ARIA, contrast) | Sprint 9 per the dev plan. |
| S8-D8 | `//go:generate` directives for web build | `make build-web` already covers this workflow; `go generate` adds no CI value right now. |
| S8-D9 | Brute-force guard on `POST /auth/local/login` | The global rate limiter applies per-IP, which provides baseline protection. A dedicated per-email lockout counter (Redis INCR, same pattern as SAML brute-force guard) is the correct long-term solution but is a focused Sprint 9 hardening item. |
| S8-D10 | Password reset / change for local admin account | Out of scope for the bootstrap use case. If needed, the password can be reset via `make seed --reset-admin` (to be designed). Sprint 9 item. |
| S8-D11 | `MCPGATE_BOOTSTRAP_ADMIN_EMAIL` env var approach | Superseded by the local admin account (Track E). No longer needed. |

---

## 8. Verification and Acceptance Criteria

### Backend

```bash
# A1 — timeseries real data
for i in $(seq 1 10); do
  curl -s http://localhost:8080/mcp/test/message \
    -H "Cookie: mcpgate_session=<token>" -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' > /dev/null
done
curl http://localhost:8080/api/v1/admin/metrics/timeseries?window=1h \
  -H "Cookie: mcpgate_session=<token>"
# → buckets array with at least one non-zero entry

# A2 — concurrent connection limit
# Configure ConcurrentConnections: 1, open two SSE connections as same user
# → second connection returns JSON-RPC -32000

# A3 — per-role rate limit override takes effect
curl -X PUT http://localhost:8080/api/v1/admin/roles/developer/rate-limits \
  -H "Cookie: mcpgate_session=<admin_token>" \
  -d '{"rpm": 5, "rpd": 100}'
# Send 6 requests as developer → 6th returns 429

# A4 — rate limit admin endpoint
curl http://localhost:8080/api/v1/admin/roles/developer/rate-limits \
  -H "Cookie: mcpgate_session=<admin_token>"
# → {"role_name":"developer","rpm":5,"rpd":100,"updated_at":"..."}
```

### Frontend

```bash
# Build passes
make build

# Smoke tests pass
make test-frontend   # all vitest smoke tests green

# Linting passes
cd web && npm run lint
```

### Track E — Local admin account

```bash
# E1 — migration applies cleanly
make migrate-up
# → no error; users table has password_hash column

# E1 — seed creates local admin (first run)
make seed
# → prints credentials block with email, password, and login URL
# → second run: "local admin already exists, skipping"

# E1 — local login API
curl -c /tmp/cookie.txt -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@local","password":"<printed password>"}'
# → 302 redirect to /console/dashboard; mcpgate_session cookie set

# E1 — wrong password
curl -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@local","password":"wrong"}'
# → 401 {"error":"invalid credentials"}

# E1 — SAML user blocked from local login
curl -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"prathiksaisb.2595@gmail.com","password":"anything"}'
# → 401 {"error":"invalid credentials"}

# E1 — suspended user blocked
# (suspend via PATCH /api/v1/admin/users/{id} first)
curl -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@local","password":"<printed password>"}'
# → 403 {"error":"account suspended"}
```

### Manual UI verification

- [ ] Navigating to `http://localhost:8080/auth/local/login` shows the login form (no auth required)
- [ ] Submitting correct local admin credentials → session cookie set → redirect to `/console/dashboard`
- [ ] Submitting wrong credentials → inline error "Invalid email or password", form stays open
- [ ] "Sign in with SSO" button redirects to Auth0 SAML flow
- [ ] Any 401 API response (e.g. expired session) redirects to `/auth/local/login` not directly to SAML
- [ ] Login page renders correctly in dark mode
- [ ] Metrics page loads, date-range tabs refetch data, line charts render
- [ ] SAML IdP list page shows the seeded `auth0-dev` config; edit modal opens prepopulated
- [ ] JWKS list page; create a new config → appears in table → delete it → gone
- [ ] Servers form: selecting `streamable_http` shows Endpoint field; switching to `stdio` shows Command + Arguments; switching to `sse` shows two URL fields
- [ ] Policy matrix: select `developer` role, check `github / tools/call` → policy appears in `GET /api/v1/admin/policies`; uncheck → policy removed
- [ ] Logs page: toggle live stream → log entries appear as requests arrive; Pause stops new entries; Export CSV triggers download
- [ ] Dark mode toggle: persists across page reload; all pages and charts adapt
- [ ] WS auth test: session expires mid-stream → server closes the WebSocket connection

### Full CI suite

```bash
make ci   # lint → test-unit → ci-services → migrate-up → test-integration → build → security-scan
make test-frontend   # new: vitest smoke tests
```
