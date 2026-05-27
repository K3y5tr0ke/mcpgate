# Understanding mcpgate

This document explains how mcpgate works in production in enough detail to reason
about configuration, access-control decisions, and failure modes. It assumes you
have read the README overview and want to understand the mechanics beneath it.

---

## The mental model: a front door

mcpgate is a **reverse proxy with a front door**. Users and AI agents talk only to
mcpgate on a single address. mcpgate verifies identity, checks permissions, records
the request, and then forwards it to the correct upstream MCP server. Upstream
servers are never exposed directly.

```
                          ┌──────────── mcpgate ─────────────────────────────┐
                          │                                                   │
  User / AI agent         │  1. Validate JWT / JWKS                          │
  ─────────────────────►  │  2. Pre-parse JSON-RPC body                      │
  POST /mcp/              │  3. Increment metrics counters                    │
  github-tools/message    │  4. Rate limit check                              │
                          │  5. RBAC: (role, server, action) → Casbin        │
  ◄─────────────────────  │  6. Forward raw bytes to upstream                │
  JSON-RPC response       │  7. Return upstream response verbatim            │
                          │  8. Submit audit log entry (async)               │
                          └───────────────────────────────────────────────────┘
                                              │
                                   ┌──────────▼──────────┐
                                   │   github-tools MCP  │
                                   │   POST /mcp         │
                                   │   (unchanged body)  │
                                   └─────────────────────┘
```

---

## Registering an upstream MCP server

An admin registers a server via the admin console or the REST API:

```http
POST /api/v1/admin/servers
{
  "name":       "github-tools",
  "transport":  "streamable_http",
  "config":     { "endpoint": "http://mcp-github:8080/mcp" },
  "is_enabled": true
}
```

This writes one row to the `mcp_servers` Postgres table. That is the entire
registration step.

**What mcpgate does NOT do at registration time:**
- It does not connect to the upstream server.
- It does not send `initialize`.
- It does not call `tools/list` or cache the server's capabilities.
- It does not create any RBAC policy for the new server.

The first real client request to `/mcp/github-tools/message` triggers the lazy
transport initialisation: the connection pool (or child process for stdio) is
created and cached in memory for subsequent requests.

Server config is re-read from Postgres at most once every 30 seconds per server
name (in-memory cache). Config changes propagate within 30 seconds without a
restart.

### Tool discovery

mcpgate is a **transparent proxy** for tool discovery. When a client sends
`tools/list` to `/mcp/github-tools/message`, mcpgate forwards that request to
the upstream server live and returns the response verbatim. The tool list is not
stored anywhere in mcpgate.

The `server_tools` table exists in the schema as pre-built infrastructure for a
future sprint (per-tool RBAC, admin console display without hitting the upstream).
It is not read or written by the current proxy.

The admin console has a **Test** button (`POST /api/v1/admin/servers/{id}/test`)
that fires a single `tools/list` probe and returns the result to the UI. The
result is not persisted.

---

## The request journey

The following describes every step a request takes from the client to the upstream
and back, in the order the middleware chain executes.

### URL structure

The server name is embedded in the URL and is the **only routing key**:

```
POST /mcp/{serverName}/message   — Streamable HTTP (single request/response)
POST /mcp/{serverName}/sse       — SSE message endpoint
GET  /mcp/{serverName}/sse       — SSE stream endpoint (persistent connection)
```

### Step 1 — Authentication (`internal/auth/middleware.go`)

The middleware reads the bearer token from:
1. `Authorization: Bearer <token>` header
2. `mcpgate_session` cookie (fallback)

It peeks at the JWT's `iss` (issuer) claim without verifying the signature, then
routes to one of two validation paths:

**Session JWT path** (token was issued by mcpgate after SAML login):
- Verifies the RS256 signature against mcpgate's own signing key.
- Parses the `session_id` claim and checks that the session is still alive in Redis.
  A revoked session fails here even if the JWT has not expired.
- Injects `UserIdentity{UserID, Email, Roles, SessionID}` into the request context.

**M2M path** (token was issued by an external JWKS provider, e.g. Auth0):
- Resolves the issuer to a `JWKSConfig` row in Postgres.
- Downloads and caches the provider's public keys (TTL configurable, default 1 hour).
- Verifies the RS256/ES256 signature and validates `aud`, `exp`, `nbf`.
- Injects a `UserIdentity` with roles sourced from the JWT claims.

Failure at either path → `401 {"error":"..."}`. The upstream is never contacted.

### Step 2 — Logging middleware (`internal/logging/middleware.go`)

Reads and buffers the full request body, then parses it as JSON-RPC to extract
`method` and the tool name from `params.name` (for `tools/call` requests). Both
are stored in the request context so downstream middleware can read them without
re-parsing.

The body is restored on the request so the proxy handler can read it again.

An `AuditLogEntry` is started at this point; the status code and latency are
filled in after the response returns and the entry is submitted to the async
pipeline.

### Step 3 — Metrics middleware (`internal/metrics/collector.go`)

Increments Redis counters:
- `metrics:ts:{unix_minute}` — global request count, used for the dashboard
  timeseries chart.
- `metrics:ts:srv:{serverName}:{unix_minute}` — per-server request count (Sprint 9),
  used for the per-server drilldown chart.

Both keys have a 7-day TTL.

### Step 4 — Rate limit middleware (`internal/ratelimit/middleware.go`)

Checks three limits against Redis sliding window counters keyed by `user_id`:
- **RPM** — requests per minute
- **RPD** — requests per day
- **Concurrent connections** — WebSocket / SSE streams open simultaneously

Limits are per-role with global defaults; per-role overrides are stored in Postgres.
Failure → `429` with a `Retry-After` header. Fail-open on Redis errors: if Redis
is unreachable, the request is allowed through.

### Step 5 — RBAC middleware (`internal/auth/rbac/middleware.go`)

This step answers: **is this user allowed to perform this action on this server?**

The JSON-RPC `method` (pre-parsed in Step 2) is mapped to an action category:

| Action category | MCP methods |
|-----------------|-------------|
| `list`          | `tools/list`, `resources/list`, `prompts/list` |
| `call`          | `tools/call` |
| `read`          | `resources/read`, `resources/subscribe`, `prompts/get` |
| `write`         | `resources/write` |
| `connect`       | `initialize`, `ping`, GET SSE stream open |
| `admin`         | `logging/setLevel` |
| `unknown`       | anything not in the above list |

The action category, server name (from the URL), and roles (from the JWT/identity)
are passed to the Casbin enforcer:

```go
enforcer.Enforce(ctx, identity.Roles, serverName, actionCategory)
```

Casbin checks the policy rules stored in Postgres against the model:

```
[matchers]
m = r.sub == p.sub && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
```

Where `sub` = role, `obj` = server name, `act` = action category. Decisions are
cached in Redis for 30 seconds. Failure → `403`. The upstream is never contacted.

### Step 6 — Proxy handler (`internal/proxy/handler.go`)

The proxy has now passed all gatekeeping checks and can contact the upstream.

1. Fetches server config from the in-memory cache (or Postgres on cache miss).
   Checks `is_enabled` — disabled servers return a JSON-RPC internal error.
2. Looks up the cached transport object for this server name in a `sync.Map`. If
   none exists, builds one based on the `transport` field:
   - `streamable_http` — an `http.Client` connection pool targeting `config.endpoint`
   - `sse` — SSE transport with `config.sse_endpoint` and `config.message_endpoint`
   - `stdio` — spawns `config.command` as a child process; stdin/stdout are the wire
3. Calls `tr.Send(ctx, rawBody)` wrapped in a per-server circuit breaker. If the
   breaker is open (recent consecutive failures exceeded the threshold), returns a
   JSON-RPC error with a `Retry-After` header immediately.
4. Returns the raw response bytes from the upstream to the client with
   `Content-Type: application/json`. The payload is never parsed, modified, or
   inspected beyond what was already done in Step 2.

### Steps 7–8 — Response + audit

The response bytes from Step 6 are written to the client. The logging middleware
then finalises the `AuditLogEntry` with the HTTP status code, latency, and
response size, and submits it to the async audit log pipeline. The pipeline batches
entries and writes them to Postgres with a hash-chain tamper-evidence record.

---

## RBAC and Casbin in detail

### Policies are not created automatically

Registering a server in `mcp_servers` does **not** create any Casbin policy. The
two systems are independent:

| Question | Answered by |
|----------|-------------|
| Does this server exist and is it reachable? | `mcp_servers` Postgres table + transport layer |
| Is this user allowed to act on it? | Casbin policy table |

A newly registered server is unreachable by anyone except roles that have a
matching wildcard policy (e.g. `(admin, *, *)`).

### How wildcards work

Consider the policy `(developer, *, call)` stored in Postgres. When a request
arrives for server `github-tools`:

```
Casbin evaluates: (sub="developer", obj="github-tools", act="call")

Policy row: (developer, *, call)
  r.sub == p.sub  →  "developer" == "developer"  ✓
  p.obj == "*"    →  true (wildcard bypasses exact match)  ✓
  r.act == p.act  →  "call" == "call"  ✓

→ Match. Access allowed.
```

Casbin never needed to know that `github-tools` is a registered server. The
wildcard matches any string that appears in the URL. If you register a new server
tomorrow, existing `(developer, *, call)` policies cover it immediately.

### The two-layer rejection model

Casbin and the proxy are sequential, independent gatekeepers. This means there are
two distinct failure points:

**RBAC allows, proxy rejects** — the policy matches but the server doesn't exist
in Postgres or is disabled:

```
Request: developer calls tools/call on /mcp/ghost-server/message
  RBAC:  (developer, *, call) matches "ghost-server" → allowed (Casbin never
         checks whether ghost-server is registered)
  Proxy: GetByName("ghost-server") → ErrNotFound → JSON-RPC internal error
  Result: HTTP 200 with JSON-RPC error body (not a 403 or 404)
```

**RBAC rejects, proxy never runs** — the policy does not match:

```
Request: viewer calls tools/call on /mcp/github-tools/message
  RBAC:  (viewer, *, list) and (viewer, *, read) exist but not (viewer, *, call)
         → no match → forbidden
  Proxy: never reached
  Result: HTTP 403
```

### The tool name is not part of RBAC

The `tools/call` body contains `{"name": "search_repos", "arguments": {...}}`. The
RBAC check for this request is simply `(role, github-tools, call)`. The name
`search_repos` plays no part in the access-control decision. It is extracted by the
logging middleware (via `msg.ToolName()`) and stored in the `tool_name` column of
the audit log, but it is otherwise passed through to the upstream verbatim.

Per-tool policies — restricting a role to specific tools on a server — do not
exist today. The `server_tools` table is the foundation for this capability in a
future sprint.

---

## The action category system

The five meaningful categories and their current policy coverage in a default seed:

| Category  | Methods                                    | Seeded for `developer`? |
|-----------|--------------------------------------------|-------------------------|
| `list`    | `tools/list`, `resources/list`, `prompts/list` | Yes (`(developer, *, list)`) |
| `call`    | `tools/call`                               | Yes (`(developer, *, call)`) |
| `read`    | `resources/read`, `resources/subscribe`, `prompts/get` | Yes (`(developer, *, read)`) |
| `write`   | `resources/write`                          | No — deny by default    |
| `connect` | `initialize`, `ping`, SSE stream open      | Yes (covered by `(admin, *, *)` for admin; `connect` is not seeded for `developer` but required to reach the proxy at all — see note below) |
| `admin`   | `logging/setLevel`                         | No — deny by default    |

**Note on `connect`:** the proxy health check sends `ping` which maps to `connect`.
If a `developer` has no `connect` policy, health pings from the proxy itself would
be denied. In practice, `initialize` and `ping` are typically allowed implicitly
or covered by the wildcard `(admin, *, *)` for admin-initiated checks. For
end-user clients, the first method after opening a connection is usually
`tools/list` (action: `list`), not `initialize`.

The `unknown` category is returned for any method not in the mapping table. Since
no seed policy grants `unknown`, any non-standard method is effectively denied for
all roles other than `admin`.

---

## Transport types

Three transports are supported, each affecting how the proxy connects to the
upstream and what config fields are required.

### Streamable HTTP

The default MCP transport. The proxy makes one HTTP POST per JSON-RPC request to
a single configured endpoint and reads the response body.

Required config: `endpoint` (the full URL, e.g. `http://mcp-github:8080/mcp`).

### SSE (Server-Sent Events)

The legacy MCP SSE transport. The proxy has two endpoints:
- `POST message_endpoint` — sends a JSON-RPC request; the upstream responds with
  `Content-Type: text/event-stream` and writes the response as `data: {...}\n\n`.
- `GET sse_endpoint` — a persistent event stream opened for server-push
  notifications; the proxy holds this connection open for the life of the client
  connection.

Required config: `sse_endpoint` and `message_endpoint`.

### stdio

The proxy spawns the configured command as a child process and communicates via
stdin/stdout with newline-terminated JSON-RPC messages. The process is started
lazily on the first request and kept alive across requests. If the process exits
unexpectedly, the transport resets itself so the next request transparently
restarts the process.

Required config: `command` (absolute path to the binary) and optionally `args`
(string array). The binary must exist on the same filesystem as the mcpgate
process — it cannot run in a separate container.

---

## Summary of key design decisions

| Decision | What it means in practice |
|----------|--------------------------|
| Server name is the only routing key | Clients must know which server has which tools. mcpgate does not aggregate or re-route based on tool name. |
| No automatic tool discovery | Tool lists are fetched live from upstreams on every `tools/list` request. No stale cache. |
| RBAC is at (role, server, action-category) granularity | There are no per-tool policies today. Any `developer` with `(developer, *, call)` can call any tool on any server. |
| Casbin wildcards cover future servers automatically | Registering a new server does not require updating policies if a wildcard already covers the role + action combination. |
| Transparent payload forwarding | mcpgate cannot transform, filter, or inspect tool arguments or results beyond what is needed for logging. |
| Lazy transport init | No connections are made at startup or registration. The first request pays the init cost. |
