# Sprint 9 Implementation Plan — Testing Foundation, Metrics Enrichment, and IdP Polish

**Date:** 2026-05-27
**Status:** Pre-implementation
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/mcpgate-architecture-blueprint.md` · `docs/implementation/stage-8-26-05-2026.md`
**Predecessor:** `docs/implementation/stage-8-26-05-2026.md`

---

## 1. Context

Sprint 8 completed the admin console's core pages, local admin bootstrap, backend hardening, dark mode, and the Vitest smoke test harness. A number of items were explicitly deferred — either because they depended on Sprint 8 foundations not yet in place, or because they were lower priority than the sprint's critical path.

Sprint 9 closes those deferred gaps across four dimensions:

1. **Metrics enrichment** — the MetricsPage currently shows static stat cards and a fixed 60-minute sparkline. Sprint 9 upgrades it to Recharts line charts with a date-range picker, configurable window sizes (1h / 6h / 24h / 7d), and per-server drilldown — matching the full spec from `mcpgate-development-plan.md §9.1–9.2`. The backend timeseries endpoint is extended with a `window` query parameter and per-server Redis key tracking.

2. **IdP configuration polish** — the SAML IdP page (Sprint 8 Track C2) is a flat CRUD modal. Sprint 9 replaces it with a 5-step setup wizard (Basic Info → SSO URLs → Certificate → Attribute Mapping → Test Login) and adds the `attribute_map`/`group_role_map` key-value editor that was explicitly deferred as S8-D10.

3. **Testing foundation** — the Playwright configuration file was never written. Sprint 9 delivers the Playwright E2E test suite (4 journeys), Vitest component-level coverage, the two Go E2E regression tests for the login routing fix, and the WebSocket session re-validation integration test. A CI job is added to run Playwright against the docker-compose dev stack.

4. **Backend polish** — JWKS key rotation endpoint, Settings page dark mode input fixes, `go generate` web build directives, and the OpenAPI-aligned `types.ts` review.

**What is explicitly out of scope for Sprint 9:**
- Sprint 10 security hardening (OWASP checklist, k6 load tests, chaos tests) — unchanged.
- Per-user metrics drilldown (`mcpgate-development-plan.md §9.3`) — deferred to Sprint 10; requires a new admin endpoint for per-user aggregation that does not yet exist.
- OAuth client management page (`mcpgate-development-plan.md §9.5`) — mcpgate validates external M2M JWTs via JWKS (Sprint 3); it is not an OAuth authorisation server and has no `clients` table. The JWKS config page already covers this use case. No action needed.

**End state:** a fresh deployment's operator can configure a SAML IdP through a guided wizard, including attribute and group-role mapping, and test the login flow in-browser. The Metrics page shows full time-series charts across multiple window sizes with per-server drilldown. The CI pipeline runs Playwright E2E tests on every push. The test suite has no known gaps in the login and session management paths.

---

## 2. What Already Exists

### Backend (complete, no changes needed)

| Endpoint | State |
|----------|-------|
| `GET /api/v1/admin/metrics/summary` | Complete |
| `GET /api/v1/admin/metrics/timeseries` | Complete — fixed 60-bucket (1h) window only; no `window` param; no per-server keys |
| `GET/POST/PUT/DELETE /api/v1/admin/idps` | Complete — flat CRUD; `attribute_map`/`group_role_map` accepted but no test-login path |
| `GET/POST/PUT/DELETE /api/v1/admin/jwks/configs` | Complete — missing rotation endpoint |
| `GET/PUT /api/v1/admin/settings` | Complete |
| `GET/POST/PUT/DELETE /api/v1/admin/roles` | Complete |
| `GET/POST/DELETE /api/v1/admin/policies` | Complete |
| `GET/PUT /api/v1/admin/roles/{id}/rate-limits` | Complete |

### Frontend (complete, no changes needed)

- All UI primitives including `Button`, `Input`, `Card`, `Modal`, `Table`, `Badge`
- `AppShell` with dark mode toggle, full nav
- `useWebSocket`, `usePagination`, `useAuth`, `useDarkMode` hooks
- Recharts v3.8.1 already installed in `package.json` — no install needed
- `@playwright/test` v1.60 already in `package.json` — only config + test files needed
- `msw` v2 and `@testing-library/react` v16 installed — available for component tests

### Already done from dev-plan Sprint 9 scope

| Dev-plan task | State |
|---|---|
| 9.6 Settings page | Complete — functional CRUD; missing dark mode input variants |
| 9.7 Dark/light mode toggle | Complete (Sprint 8 Track C4) |
| Basic metrics page (`StatCard`, `Sparkline` SVG) | Complete (Sprint 8 Track C1) — needs Recharts upgrade |
| Basic SAML IdP page | Complete (Sprint 8 Track C2) — needs wizard + map editor |
| JWKS config page | Complete (Sprint 8 Track C3) — needs rotation endpoint |

### Missing (Sprint 9 will deliver)

| Item | Track |
|------|-------|
| Metrics timeseries window parameter (`?window=1h/6h/24h/7d`) | A1 |
| Per-server Redis timeseries keys | A1 |
| `POST /api/v1/admin/jwks/{id}/rotate` | A2 |
| Recharts line charts + date-range picker on MetricsPage | B1 |
| Per-server metrics drilldown on MetricsPage | B2 |
| SAML IdP 5-step setup wizard | B3 |
| `KeyValueEditor` component + IdpListPage map editor | B4 |
| Settings page dark mode input/select classes | B5 |
| `playwright.config.ts` + 4 E2E test journeys | C1 |
| CI job: Playwright against docker-compose | C2 |
| Vitest component tests (`Button`, `Modal`, `Table`) | C3 |
| Vitest `client.ts` 401 redirect assertion | C4 |
| Go E2E: `GET /console/login` returns 200 HTML | C5 |
| WebSocket session re-validation integration test | C6 |
| `go generate` web build directive | D1 |

---

## 3. Ordered Task List

Tracks A and B5 land first (backend changes before frontend consumers). B1–B4 are independent of each other and can proceed in parallel once A1 is merged. Track C is independent throughout — tests do not block any other track. Track D is a single quick task.

---

### Track A — Backend enhancements (2 tasks)

#### A1 — Metrics timeseries: window parameter + per-server keys

**Files:** `internal/metrics/collector.go`, `internal/admin/api/metrics.go`

**Problem 1 — Fixed window:** `GET /api/v1/admin/metrics/timeseries` always returns 60 buckets (the last 60 minutes). The frontend MetricsPage needs to request 6h, 24h, and 7d windows for its date-range picker.

**Problem 2 — No per-server breakdown:** S8-D1 deferred per-server tracking. The MetricsPage per-server drilldown (Track B2) requires it.

**Design — window parameter:**

The `timeseries` handler currently computes `numBuckets = 60` and `bucketMin = now - int64(numBuckets-1-i)`. Extend to accept a `window` query parameter:

```
?window=1h   →  60 buckets × 1-minute resolution  (default, current behaviour)
?window=6h   → 360 buckets × 1-minute resolution
?window=24h  →  96 buckets × 15-minute resolution
?window=7d   →  84 buckets × 2-hour resolution
```

The bucket size changes at larger windows to keep the response under ~100 items. Resolution is controlled by `bucketSecs` (60 for 1h/6h, 900 for 24h, 7200 for 7d).

For multi-minute buckets, the pipeline must read all per-minute keys within the bucket span and sum them:
```go
// For each display bucket i spanning minutes [lo, hi):
for m := lo; m < hi; m++ {
    cmds = append(cmds, pipe.Get(ctx, fmt.Sprintf("metrics:ts:%d", now-int64(totalMins-1-m))))
}
```
Sum across the pipeline results for the bucket's display value.

Response shape gains a `resolution_secs` field (so the frontend can format X-axis labels correctly):
```json
{
  "window":           "6h",
  "resolution_secs":  60,
  "buckets": [
    { "minute": 1748910000, "requests": 42 },
    ...
  ]
}
```

**Design — per-server keys:**

In `collector.go`, the `Middleware()` already has access to the chi router's URL params via the request's `RouteContext`. After the existing `metrics:ts:{minute}` INCR, add a per-server key:

```go
if serverName := chi.URLParam(r, "serverName"); serverName != "" {
    srvKey := fmt.Sprintf("metrics:ts:srv:%s:%d", serverName, now.Unix()/60)
    c.redis.Incr(ctx, srvKey)
    c.redis.Expire(ctx, srvKey, 7*24*time.Hour)
}
```

New handler: `GET /api/v1/admin/metrics/timeseries/server/{serverName}` — same pipeline logic as the global endpoint, but reads `metrics:ts:srv:{name}:{minute}` keys. Alternatively, a query param `?server=` on the existing endpoint avoids a new route path.

**Decision:** use `?server=` on the existing endpoint. One endpoint, two modes: `?window=6h` for global, `?window=6h&server=github` for per-server.

**Acceptance:**
- `GET /api/v1/admin/metrics/timeseries?window=6h` returns 360 buckets at 60s resolution.
- `GET /api/v1/admin/metrics/timeseries?window=24h` returns 96 buckets at 900s resolution.
- `GET /api/v1/admin/metrics/timeseries?window=7d` returns 84 buckets at 7200s resolution.
- `GET /api/v1/admin/metrics/timeseries?window=1h&server=my-server` returns 60 per-server buckets.
- Backward compat: `GET /api/v1/admin/metrics/timeseries` (no params) returns 60 buckets at 60s resolution — existing frontend sparklines are unaffected.

---

#### A2 — JWKS key rotation endpoint

**File:** `internal/admin/api/jwks.go`, `internal/store/postgres/jwks.go`

**Problem:** When an external JWT issuer (Auth0, Okta, etc.) rotates its signing keys, the cached public key set in mcpgate becomes stale. Operators currently have no way to force a re-fetch short of restarting the server. S8-D5.

**Design:**

New handler: `POST /api/v1/admin/jwks/{id}/rotate`

```go
func (h *jwksHandler) rotate(w http.ResponseWriter, r *http.Request) {
    id, err := uuid.Parse(chi.URLParam(r, "id"))
    // ... parse + 400 if invalid

    cfg, err := h.store.GetByID(r.Context(), id)
    // ... 404 if not found

    // Re-fetch JWKS from cfg.JWKSURL
    resp, err := http.Get(cfg.JWKSURL)
    // ... 502 if unreachable

    // Validate response is valid JSON containing a "keys" array
    var jwks struct { Keys []json.RawMessage `json:"keys"` }
    json.NewDecoder(resp.Body).Decode(&jwks)
    if len(jwks.Keys) == 0 {
        writeJSONError(w, http.StatusBadGateway, "remote JWKS response contained no keys")
        return
    }

    // Invalidate the auth middleware's in-memory JWKS cache for this issuer.
    // The cache is a sync.Map keyed by issuer string — expose an Invalidate(issuer) method.
    h.jwksCache.Invalidate(cfg.Issuer)

    writeJSON(w, http.StatusOK, map[string]any{
        "issuer":    cfg.Issuer,
        "key_count": len(jwks.Keys),
        "rotated_at": time.Now().UTC(),
    })
}
```

The handler does not store the fetched keys — the validator fetches and caches them lazily on the next request. The rotation endpoint's sole purpose is to evict the stale cache entry.

**Wire in `router.go`:**
```go
r.Route("/jwks/configs", func(r chi.Router) {
    // ... existing routes
    r.Post("/{id}/rotate", jh.rotate)
})
```

**Acceptance:** integration test: seed a JWKS config with a mock `JWKSURL` pointing to a test server. Call `POST /.../rotate` → 200 with `key_count ≥ 1`. Call with unknown ID → 404. Call when upstream is unreachable → 502. Verify the auth middleware's cache entry is evicted (subsequent request triggers a fresh fetch).

---

### Track B — Frontend enhancements (5 tasks)

#### B1 — MetricsPage: Recharts line charts + date-range picker

**File:** `web/src/pages/metrics/MetricsPage.tsx`

**Problem:** The current page renders a custom SVG sparkline and static `StatCard` values. It lacks the interactive Recharts line charts and date-range selector specified in `mcpgate-development-plan.md §9.1`. Recharts v3.8.1 is already installed.

**Design:**

Replace the SVG `Sparkline` component with a `MetricsLineChart` wrapper around Recharts `ResponsiveContainer` + `AreaChart`:

```tsx
function MetricsLineChart({ data, label }: { data: { ts: number; value: number }[]; label: string }) {
  return (
    <ResponsiveContainer width="100%" height={140}>
      <AreaChart data={data} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
        <defs>
          <linearGradient id="fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="5%"  stopColor="#6366f1" stopOpacity={0.3} />
            <stop offset="95%" stopColor="#6366f1" stopOpacity={0}   />
          </linearGradient>
        </defs>
        <XAxis dataKey="ts" tickFormatter={formatTs} tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
        <YAxis width={28} tick={{ fontSize: 10 }} axisLine={false} tickLine={false} />
        <Tooltip formatter={(v) => [v, label]} labelFormatter={formatTs} />
        <Area type="monotone" dataKey="value" stroke="#6366f1" fill="url(#fill)" strokeWidth={1.5} dot={false} />
      </AreaChart>
    </ResponsiveContainer>
  )
}
```

`formatTs` converts the `minute` Unix timestamp to `HH:mm` for 1h/6h views and `MM/DD HH:mm` for 24h/7d.

**Date-range picker:**

Simple tab group (not a date picker component — tabs are sufficient given the fixed window options):

```tsx
const WINDOWS = ['1h', '6h', '24h', '7d'] as const
type Window = typeof WINDOWS[number]

const [window, setWindow] = useState<Window>('1h')
```

The `useMetricsTimeseries` hook is updated to accept `window` and pass it as a query param:
```ts
// queries.ts
export const useMetricsTimeseries = (window: string = '1h') =>
  useQuery({
    queryKey: ['metrics', 'timeseries', window],
    queryFn: () => apiFetch<MetricsTimeseries>(`/metrics/timeseries?window=${window}`),
    refetchInterval: 30_000,
  })
```

**Page layout (updated):**

```
[ 1h ][ 6h ][ 24h ][ 7d ]    ← window tabs, local state

Request Volume          Error Rate (%)        p95 Latency (ms)
[ MetricsLineChart ]    [ MetricsLineChart ]   [ MetricsLineChart ]

[ StatCard ×6 grid — existing, unchanged ]

[ Circuit breakers — existing, unchanged ]
```

The three charts share the same `buckets` array but map different fields. Since the timeseries endpoint only returns `requests`, error rate and latency charts are shown when the summary endpoint adds time-series data (deferred — for now only the request volume chart is populated from real data; error rate and latency charts display a "coming soon" placeholder).

**Acceptance:** selecting "6h" fetches `/metrics/timeseries?window=6h`, chart re-renders with up to 360 data points; X-axis labels update to show the wider time range. Selecting "1h" restores the previous view.

---

#### B2 — MetricsPage: per-server drilldown

**File:** `web/src/pages/metrics/MetricsPage.tsx`

**Problem:** The MetricsPage shows only global totals. Operators need to identify which server is generating load or errors. `mcpgate-development-plan.md §9.2`.

**Design:**

New section below the global charts:

```
Per-server breakdown
Server: [ github ▾ ]    ← select populated from useServers()

[ MetricsLineChart — request volume for selected server ]
```

Local state `selectedServer` (default: first server name, or empty if none). Query:
```ts
useMetricsTimeseries(window, selectedServer)
// internally: /metrics/timeseries?window=1h&server=github
```

`useMetricsTimeseries` updated to accept an optional `server` param:
```ts
export const useMetricsTimeseries = (window = '1h', server?: string) =>
  useQuery({
    queryKey: ['metrics', 'timeseries', window, server ?? ''],
    queryFn: () =>
      apiFetch<MetricsTimeseries>(
        `/metrics/timeseries?window=${window}${server ? `&server=${server}` : ''}`
      ),
    refetchInterval: 30_000,
    enabled: !server || !!server,  // always enabled
  })
```

The section is rendered only when `servers.length > 0`. If the query returns all-zero buckets (server has no traffic in the selected window), display a subtle "No traffic in this window" message below the chart.

The server `<select>` inherits the dark mode classes established in §9.8.2 (`bg-white text-gray-900 dark:bg-gray-700 dark:text-gray-100 dark:border-gray-600`).

---

#### B3 — SAML IdP setup wizard

**Files:** `web/src/pages/idp/IdpListPage.tsx`, `web/src/components/idp/IdpWizard.tsx` (new)

**Problem:** The current IdP create/edit modal is a flat form with 6 fields. Operators unfamiliar with SAML find it difficult to know what to fill in and in what order. S8-D4. `mcpgate-development-plan.md §9.4`.

**Design:**

Extract the create flow into a 5-step wizard component. Edit remains the flat modal (editing an existing IdP is a minor tweak; the wizard is for first-time setup).

**`web/src/components/idp/IdpWizard.tsx`** — new component, mounted in a full-screen `Modal`:

| Step | Title | Fields |
|------|-------|--------|
| 1 | Basic Info | Name (text), Enabled (toggle) |
| 2 | SSO Endpoints | SSO URL (text), SLO URL (optional text) |
| 3 | Identity Provider Certificate | Certificate PEM (`<textarea rows={8}>`), Entity ID (text) |
| 4 | Attribute & Role Mapping | `attribute_map` key-value editor, `group_role_map` key-value editor (uses `KeyValueEditor` from Track B4) |
| 5 | Review & Save | Read-only summary of all entered values, "Save & Activate" button |

**Step navigation:**
```tsx
const [step, setStep] = useState(1)
const TOTAL_STEPS = 5

// Progress indicator
<div className="flex gap-1 mb-6">
  {Array.from({ length: TOTAL_STEPS }, (_, i) => (
    <div key={i} className={`h-1 flex-1 rounded ${i + 1 <= step ? 'bg-indigo-600' : 'bg-gray-200 dark:bg-gray-700'}`} />
  ))}
</div>
```

Each step validates its own fields before allowing Next. The final step calls `createIdP.mutateAsync(form)` and closes the wizard on success.

**Download SP metadata button (Step 5):**
```tsx
<a href="/auth/saml/metadata" download="sp-metadata.xml" target="_blank">
  <Button variant="secondary">Download SP metadata XML</Button>
</a>
```

Operators can download the SP metadata and upload it to their IdP before clicking Save.

**Test Login button (Step 5 — stretch goal):**
Opens `window.open('/auth/saml/login?redirect_to=/console/idp', 'saml-test', 'width=600,height=700')`. After a successful login, the popup redirects back to `/console/idp` and closes. The parent page detects the focus event and shows a success banner. This requires the IdP to already be saved (partial save on Step 4 completion, then test on Step 5) — flag as stretch within the sprint.

**IdpListPage changes:**
- Replace "New IdP" → opens `IdpWizard` (not the flat modal).
- Edit button → opens the existing flat modal (unchanged).
- "New IdP" becomes "Setup IdP" to signal the wizard UX.

---

#### B4 — `KeyValueEditor` component + IdpListPage map editor

**Files:** `web/src/components/ui/KeyValueEditor.tsx` (new), `web/src/pages/idp/IdpListPage.tsx`

**Problem:** The `attribute_map` and `group_role_map` fields in the IdP config are `Record<string, string>` JSONB columns. The current flat modal and wizard (Track B3 Step 4) need a component that lets operators add, edit, and remove key-value pairs. S8-D10.

**`KeyValueEditor` component design:**

```tsx
interface Props {
  label: string
  value: Record<string, string>
  onChange: (next: Record<string, string>) => void
  keyPlaceholder?: string
  valuePlaceholder?: string
}

export function KeyValueEditor({ label, value, onChange, keyPlaceholder = 'key', valuePlaceholder = 'value' }: Props)
```

Renders a list of existing pairs as editable rows + a blank "add row" row at the bottom:

```
[ attribute_map ]
  urn:oid:0.9.2342.19200300.100.1.3   →  email        [ × ]
  urn:oid:2.5.4.42                     →  first_name   [ × ]
  ────────────────────────────────────────
  [ new key              ]  [ new value   ] [ Add ]
```

Each row: two `<input>` elements + a remove `<button>`. Editing either input updates the key-value in place. The `×` remove button deletes the pair immediately (calls `onChange` with the pair omitted). The "Add" button appends the new pair and clears the add row inputs.

**Internal state:** the component holds `newKey`/`newValue` for the add-row only. The `value` prop is the source of truth for existing pairs — the component is controlled.

**Usage in IdpWizard Step 4:**
```tsx
<KeyValueEditor
  label="Attribute mapping"
  value={form.attribute_map}
  onChange={(m) => setForm((f) => ({ ...f, attribute_map: m }))}
  keyPlaceholder="SAML attribute (e.g. urn:oid:0.9.2342...)"
  valuePlaceholder="mcpgate field (e.g. email)"
/>
<KeyValueEditor
  label="Group → role mapping"
  value={form.group_role_map}
  onChange={(m) => setForm((f) => ({ ...f, group_role_map: m }))}
  keyPlaceholder="IdP group name"
  valuePlaceholder="mcpgate role (e.g. admin)"
/>
```

---

#### B5 — Settings page dark mode input fixes

**File:** `web/src/pages/settings/SettingsPage.tsx`

**Problem:** The boolean `<select>` (Enabled/Disabled) and numeric `<input>` elements in the Settings page do not have dark mode classes. The `Input` component (fixed globally in §9.9) is not used here — these are raw HTML form elements. The `SettingRow` label also uses `text-gray-900` without a dark variant.

**Fix:**

Numeric input:
```tsx
// before
className="h-9 w-28 rounded-md border border-gray-300 px-3 text-sm ..."

// after
className="h-9 w-28 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900
           focus:outline-none focus:ring-2 focus:ring-indigo-500
           dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
```

Boolean select:
```tsx
// before
className="h-9 w-24 rounded-md border border-gray-300 px-3 text-sm ..."

// after
className="h-9 w-24 rounded-md border border-gray-300 bg-white px-3 text-sm text-gray-900
           focus:outline-none focus:ring-2 focus:ring-indigo-500
           dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100"
```

Other settings `<input>` (string type):
```tsx
// same dark classes as above
```

`SettingRow` label:
```tsx
// before
<p className="text-sm font-medium text-gray-900">{label}</p>

// after
<p className="text-sm font-medium text-gray-900 dark:text-white">{label}</p>
```

`SettingRow` description:
```tsx
// before
<p className="text-xs text-gray-400">{description}</p>

// after  (gray-400 is already OK in dark mode against gray-800; no change needed)
```

`Card` divider rows:
```tsx
// The divide-y divide-gray-50 on the wrapping div needs a dark variant
className="divide-y divide-gray-50 dark:divide-gray-700"
```

---

### Track C — Testing (6 tasks)

#### C1 — Playwright E2E: 4 journeys

**Files:**
- `web/playwright.config.ts` (new)
- `web/tests/e2e/dashboard.spec.ts` (new)
- `web/tests/e2e/servers.spec.ts` (new)
- `web/tests/e2e/policies.spec.ts` (new)
- `web/tests/e2e/logs.spec.ts` (new)

**`playwright.config.ts`:**

```ts
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,
  retries: process.env.CI ? 1 : 0,
  workers: 1,
  reporter: [['list'], ['html', { open: 'never' }]],
  use: {
    baseURL: process.env.PLAYWRIGHT_BASE_URL ?? 'http://localhost:8080',
    storageState: 'tests/e2e/.auth/admin.json',
  },
  projects: [
    {
      name: 'setup',
      testMatch: '**/auth.setup.ts',
    },
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
      dependencies: ['setup'],
    },
  ],
})
```

**`tests/e2e/auth.setup.ts`** — global setup that logs in as `admin@localhost` and saves the storage state (cookie + localStorage) to `tests/e2e/.auth/admin.json`. All other tests re-use this session rather than logging in individually.

```ts
import { test as setup } from '@playwright/test'

setup('authenticate', async ({ page }) => {
  await page.goto('/console/login')
  await page.fill('input[type="email"]', 'admin@localhost')
  await page.fill('input[type="password"]', process.env.ADMIN_PASSWORD ?? 'changeme')
  await page.click('button[type="submit"]')
  await page.waitForURL('/console/dashboard')
  await page.context().storageState({ path: 'tests/e2e/.auth/admin.json' })
})
```

**Journey 1 — Dashboard KPIs (`dashboard.spec.ts`):**
- Navigate to `/console/dashboard`.
- Assert `<h1>` contains "Dashboard".
- Assert at least one `StatCard` value is visible and non-empty (requests/min, active sessions, etc.).
- Assert the request volume sparkline SVG is rendered (or Recharts AreaChart after B1 lands).

**Journey 2 — Server CRUD via transport-aware form (`servers.spec.ts`):**
- Navigate to `/console/servers`.
- Click "Add server".
- Fill in Name, select transport `streamable_http`, fill in Endpoint URL.
- Click "Create" → assert row appears in the table.
- Click "Edit" on the new row → assert modal opens pre-populated.
- Click "Delete" → confirm → assert row is removed.

**Journey 3 — Policy matrix toggle (`policies.spec.ts`):**
- Navigate to `/console/roles`.
- Locate the policy matrix.
- Find the `admin` role row and the `* (all)` resource column.
- Assert the `* → all` cell is checked (green, from the seeded `SeedDefaultPolicies` policy).
- Click the cell to remove it → assert it turns grey.
- Click it again to re-add it → assert it turns green.
- (This is a round-trip toggle test — it leaves the policy in the original state.)

**Journey 4 — Log export (`logs.spec.ts`):**
- Navigate to `/console/logs`.
- Set up a download listener: `page.waitForEvent('download')`.
- Click "Export CSV".
- Assert the downloaded filename ends with `.csv`.
- Assert the downloaded content starts with a CSV header (first line contains comma-separated field names).

---

#### C2 — CI job: Playwright tests

**File:** CI pipeline configuration (tool-neutral — document the intended setup, not GitHub Actions syntax)

A CI job `playwright-e2e` that:
1. Starts the docker-compose dev stack (`make docker-up && make migrate-up`).
2. Runs `make seed` to create the local admin account (password injected via `ADMIN_PASSWORD` env var).
3. Builds and starts the server (`make build && ./mcpgate serve &`).
4. Waits for the health endpoint (`until curl -sf http://localhost:8080/health; do sleep 1; done`).
5. Runs `npx --prefix web playwright test --project=chromium`.
6. Uploads the Playwright HTML report as a CI artefact.

The job runs only on the main branch and pull requests targeting main.

**Makefile target** (so the job steps are version-controlled):
```makefile
test-e2e-playwright:
	$(NVM) cd web && npx playwright test --project=chromium
```

---

#### C3 — Vitest component coverage

**Files:**
- `web/src/test/components/Button.test.tsx` (new)
- `web/src/test/components/Modal.test.tsx` (new)
- `web/src/test/components/Table.test.tsx` (new)
- `web/src/test/hooks/useDarkMode.test.ts` (new)

**Button tests:**
- Renders label text.
- Shows spinner and disables when `loading={true}`.
- Calls `onClick` when clicked.
- Does not call `onClick` when `disabled={true}`.
- Applies correct variant class (`danger` adds a red background class).

**Modal tests:**
- Does not render when `open={false}`.
- Renders children and title when `open={true}`.
- Calls `onClose` when the close button is clicked.
- Calls `onClose` when Escape is pressed (keyboard accessibility).

**Table tests:**
- Renders header columns.
- Renders one row per data item.
- Renders `emptyText` when `data=[]`.
- Calls `onRowClick` when a row is clicked.
- Does not render a clickable cursor when `onRowClick` is undefined.

**`useDarkMode` hook tests:**
- Returns `dark: false` when `localStorage` has no entry and `prefers-color-scheme` does not match dark.
- Returns `dark: true` when `localStorage` has `"true"`.
- Calling `toggle()` flips the value and updates `localStorage`.
- Calling `toggle()` adds/removes the `dark` class on `document.documentElement`.

---

#### C4 — Vitest: `client.ts` 401 redirect assertion

**File:** `web/src/test/api/client.test.ts` (new)

**Goal:** Assert that `apiFetch` navigates to `/console/login?redirect_to=/console/dashboard` when the API returns 401. Catches future regressions to the redirect target (e.g. the §9.4 routing bug) without requiring a running server. S8-D3a.

**Setup:**

```ts
// Mock window.location.href (jsdom does not support assignment by default)
const locationSpy = vi.spyOn(window, 'location', 'get').mockReturnValue({
  ...window.location,
  href: '',
})

// Mock fetch to return 401
globalThis.fetch = vi.fn().mockResolvedValue({ status: 401, ok: false })
```

**Tests:**
1. Assert `localStorage.removeItem('mcpgate_token')` is called on 401.
2. Assert `window.location.href` is set to `'/console/login?redirect_to=/console/dashboard'`.
3. Assert the function throws an `ApiError` with `status: 401`.

---

#### C5 — Go E2E: `/console/login` returns 200 HTML

**File:** `tests/e2e/console_test.go` (modify existing)

**Goal:** Extend `TestE2E_Console_IndexHTMLServed` (or add a new test in the same file) to assert that `GET /console/login` returns HTTP 200 with `Content-Type: text/html`. This test would have caught the §9.4 routing bug before it reached a browser. S8-D3b.

```go
func TestE2E_Console_LoginPageServed(t *testing.T) {
    srv := startTestServer(t)
    resp, err := http.Get(srv.URL + "/console/login")
    if err != nil {
        t.Fatalf("GET /console/login: %v", err)
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        t.Errorf("status = %d, want 200", resp.StatusCode)
    }
    ct := resp.Header.Get("Content-Type")
    if !strings.HasPrefix(ct, "text/html") {
        t.Errorf("Content-Type = %q, want text/html", ct)
    }
}
```

The test lives in the `integration` build tag alongside the other E2E tests (`//go:build integration`).

---

#### C6 — WebSocket session re-validation integration test

**File:** `internal/auth/middleware_test.go` or `tests/e2e/websocket_test.go` (new, `//go:build integration`)

**Goal:** Verify that a WebSocket connection authenticated with a valid session token is rejected (not just at upgrade time) when the session is revoked in Redis mid-connection. S8-D3.

**Design:**

The test is an integration test that requires a running Redis instance (uses the docker-compose dev Redis at `localhost:6379`).

```go
func TestWebSocket_SessionRevoked_ConnectionClosed(t *testing.T) {
    // 1. Issue a real JWT + session via jwtManager.Issue + sessionCache.Set.
    // 2. Open a WebSocket connection to WS /api/v1/admin/logs/stream
    //    authenticated via Cookie: mcpgate_session=<token>.
    // 3. Assert the connection is OPEN (receive at least one ping/message).
    // 4. Delete the session from Redis: sessionCache.Delete(ctx, sessionID).
    // 5. Wait for the server's session validation interval (configurable, default 30s).
    // 6. Assert the connection is CLOSED (ws.ReadMessage returns an error).
}
```

**Constraint:** Step 5 requires the server to re-validate sessions on active connections. The current WebSocket handler validates the session only at upgrade time. If this validation does not yet happen mid-connection, the test documents the gap rather than passing. The test is written as a behaviour specification — it is expected to fail until the re-validation heartbeat is implemented (which may be Sprint 10 work). The test file includes a `t.Skip("re-validation heartbeat not yet implemented — Sprint 10")` that can be removed when the feature lands.

---

### Track D — Polish (1 task)

#### D1 — `go generate` web build directive

**File:** `cmd/mcpgate/main.go` (or `internal/server/server.go`)

**Goal:** Add a `//go:generate` comment so `go generate ./...` triggers the frontend build. S8-D8.

```go
//go:generate npm --prefix ../../web run build
```

Placed in `cmd/mcpgate/main.go` above the `package main` declaration. The path `../../web` is relative to the file's location (`cmd/mcpgate/`), which resolves to the project root's `web/` directory.

Update `Makefile` to note that `make build` calls `go generate` before compiling:
```makefile
build: build-web
	$(GOPATH_CMD) go build -o bin/mcpgate ./cmd/mcpgate
```

(This is the current behaviour via `build-web`; the `go generate` directive adds IDE/toolchain support for the same step without requiring `make`.)

---

## 4. File Inventory

### New files

| File | Track | Purpose |
|------|-------|---------|
| `web/playwright.config.ts` | C1 | Playwright configuration |
| `web/tests/e2e/auth.setup.ts` | C1 | Session bootstrap for all E2E tests |
| `web/tests/e2e/dashboard.spec.ts` | C1 | E2E: Dashboard KPIs |
| `web/tests/e2e/servers.spec.ts` | C1 | E2E: Server CRUD |
| `web/tests/e2e/policies.spec.ts` | C1 | E2E: Policy matrix toggle |
| `web/tests/e2e/logs.spec.ts` | C1 | E2E: Log export |
| `web/tests/e2e/.auth/.gitkeep` | C1 | Placeholder — `.auth/admin.json` is gitignored |
| `web/src/test/components/Button.test.tsx` | C3 | Button unit tests |
| `web/src/test/components/Modal.test.tsx` | C3 | Modal unit tests |
| `web/src/test/components/Table.test.tsx` | C3 | Table unit tests |
| `web/src/test/hooks/useDarkMode.test.ts` | C3 | useDarkMode hook tests |
| `web/src/test/api/client.test.ts` | C4 | client.ts 401 redirect test |
| `web/src/components/idp/IdpWizard.tsx` | B3 | 5-step SAML IdP setup wizard |
| `web/src/components/ui/KeyValueEditor.tsx` | B4 | Reusable key-value pair editor |
| `tests/e2e/websocket_test.go` | C6 | WebSocket session re-validation test |

### Modified files

| File | Track | Change |
|------|-------|--------|
| `internal/admin/api/metrics.go` | A1 | Add `window`/`server` query params; multi-resolution bucketing |
| `internal/metrics/collector.go` | A1 | INCR per-server key when `serverName` is available in route context |
| `internal/admin/api/jwks.go` | A2 | Add `rotate` handler |
| `internal/admin/api/router.go` | A2 | Wire `POST /jwks/configs/{id}/rotate` |
| `web/src/pages/metrics/MetricsPage.tsx` | B1, B2 | Recharts charts, window tabs, per-server drilldown |
| `web/src/api/queries.ts` | B1, B2 | Update `useMetricsTimeseries` to accept `window` + `server` params; add `useRotateJWKS` |
| `web/src/api/types.ts` | B1 | Add `resolution_secs` to `MetricsTimeseries` type |
| `web/src/pages/idp/IdpListPage.tsx` | B3 | Replace create modal with `IdpWizard`; keep edit modal |
| `web/src/pages/settings/SettingsPage.tsx` | B5 | Dark mode classes on inputs, select, and `SettingRow` label |
| `web/package.json` | C1 | Add `test:e2e` script: `playwright test --project=chromium` |
| `cmd/mcpgate/main.go` | D1 | `//go:generate` directive |
| `Makefile` | C2, D1 | `test-e2e-playwright` target |
| `tests/e2e/console_test.go` | C5 | Add `TestE2E_Console_LoginPageServed` |

---

## 5. Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Per-server timeseries uses `metrics:ts:srv:{name}:{minute}` keys, not a hash | Consistent with the global `metrics:ts:{minute}` key pattern; pipelines treat all keys uniformly |
| D2 | Multi-resolution bucketing in the timeseries handler (not a separate endpoint) | A single `?window=` parameter avoids URL proliferation; the resolution change is an implementation detail |
| D3 | JWKS rotation evicts the cache without storing fetched keys | The auth middleware fetches keys lazily on the next request; the rotation endpoint only triggers that re-fetch |
| D4 | Wizard for create, flat modal for edit | Wizards are most valuable for first-time setup where order matters; editing a known config does not benefit from a multi-step flow |
| D5 | `KeyValueEditor` is a controlled component (no internal key-value state) | Avoids sync bugs between internal state and the parent form; parent always has the canonical value |
| D6 | WebSocket re-validation test added with `t.Skip` if heartbeat not implemented | Tests should document intended behaviour regardless of implementation state; a skipped test is better than no test |
| D7 | Playwright `storageState` shared across all tests via auth setup | Avoids repeated login steps; the admin session is valid for the full test run |
| D8 | `test-e2e-playwright` Makefile target wraps the Playwright command | CI and developers use the same command; no CI-specific invocation paths |

---

## 6. Testing

### Test inventory

| Test file | Type | Gate |
|-----------|------|------|
| `web/tests/e2e/*.spec.ts` (×4) | Playwright E2E | CI `test-e2e-playwright` |
| `web/src/test/components/*.test.tsx` (×3) | Vitest component | `make test-vitest` |
| `web/src/test/hooks/useDarkMode.test.ts` | Vitest hook | `make test-vitest` |
| `web/src/test/api/client.test.ts` | Vitest unit | `make test-vitest` |
| `tests/e2e/console_test.go` (new test) | Go E2E integration | `make test-integration` |
| `tests/e2e/websocket_test.go` | Go E2E integration | `make test-integration` (skipped until heartbeat) |

### Existing test suite — no regressions expected

| Suite | Command |
|-------|---------|
| Go unit | `go test -short ./...` |
| Go integration | `go test -tags=integration ./internal/... ./tests/e2e/...` |
| Vitest smoke | `npm --prefix web run test` |

---

## 7. Deferred Items

Items carried forward to Sprint 10 or later:

| ID | Item | Reason |
|----|------|--------|
| S9-D1 | Per-user metrics drilldown (`mcpgate-development-plan.md §9.3`) | Requires a new backend endpoint for per-user aggregation (`GET /api/v1/admin/metrics/user/{id}/timeseries`). No Redis key structure exists yet. Deferred to Sprint 10. |
| S9-D2 | SAML wizard "Test Login" popup step | Requires the IdP to be partially saved before testing, which adds state-management complexity to the wizard. The download SP metadata button in Step 5 is included; the in-browser test flow is deferred. |
| S9-D3 | WebSocket re-validation heartbeat implementation | The C6 test documents the intended behaviour with `t.Skip`. The implementation (a periodic `sessionCache.Get` inside the WebSocket handler goroutine) is Sprint 10 work aligned with Sprint 10's hardening focus. |
| S9-D4 | OpenAPI spec auto-generation (S8-D6) | `swaggo` or `huma` integration is a standalone task that does not block any other Sprint 9 work. Deferred to Sprint 11 alongside the documentation sprint. |
| S9-D5 | Accessibility audit with axe-core (S8-D7 / dev-plan 9.8) | Blocked on the Playwright setup (C1) being stable. Scope: axe-core Playwright plugin runs on every E2E journey page, violations collected and reported. Deferred to Sprint 10 once Playwright is in CI. |
