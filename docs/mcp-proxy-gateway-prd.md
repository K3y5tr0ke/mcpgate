# MCP Proxy Gateway — Product Requirements Document

**Version:** 1.0  
**Date:** May 22, 2026  
**Status:** Draft  
**Codename:** `mcpgate`

---

## 1. Executive Summary

The MCP Proxy Gateway ("mcpgate") is a production-grade reverse proxy that sits between MCP clients (such as Claude Desktop, IDE extensions, or custom agents) and upstream MCP servers. It provides enterprise-grade authentication, authorization, observability, and administration — capabilities that the MCP specification intentionally leaves out of scope, but that every production deployment needs.

The proxy is transparent to the MCP protocol: clients and servers operate as if directly connected, while mcpgate intercepts the transport layer (stdio-over-HTTP, SSE, and the newer Streamable HTTP) to enforce policy and capture telemetry.

**Core value proposition:** Organizations can adopt MCP tooling without sacrificing the identity, access-control, audit, and compliance requirements they already enforce for every other integration.

---

## 2. Problem Statement

The Model Context Protocol is rapidly becoming the standard for connecting LLM agents to tools and data sources. However, MCP servers today are typically deployed with no authentication, no authorization, no audit trail, and no centralized management. This creates several urgent problems:

**Security gap.** Any client that can reach an MCP server can invoke any tool it exposes. There is no concept of user identity, roles, or scoped permissions at the protocol level.

**Compliance blind spot.** Regulated industries (finance, healthcare, government) require auditable records of every action an AI agent takes on behalf of a human. MCP provides no logging specification.

**Operational opacity.** Teams deploying multiple MCP servers have no centralized view of latency, error rates, or usage patterns. Debugging a slow or broken tool call requires digging through disparate server logs.

**Identity fragmentation.** Enterprises already have identity providers (Okta, Azure AD, Google Workspace). MCP offers no standard way to integrate with SAML or OIDC, forcing ad-hoc solutions per server.

mcpgate solves all four problems with a single deployment that wraps any number of upstream MCP servers.

---

## 3. Goals and Non-Goals

### Goals

- Provide a protocol-transparent proxy for MCP servers supporting stdio, SSE, and Streamable HTTP transports.
- Authenticate users via SAML 2.0 with support for multiple IdPs.
- Authorize tool invocations via OAuth 2.0-backed RBAC, with per-tool and per-server permission granularity.
- Log every request, response, and error with structured metadata (user, session, tool, server, latency, payload sizes, timestamps).
- Expose real-time and historical metrics via a Prometheus-compatible endpoint and a built-in admin dashboard.
- Provide a React-based admin console for configuration, user/role management, log exploration, and system health monitoring.
- Ship as a single Docker image (or docker-compose stack) with sane defaults and zero mandatory external dependencies beyond a database.
- Maintain sub-5ms p99 proxy overhead for passthrough requests.

### Non-Goals

- mcpgate does not implement MCP server logic. It proxies to upstream servers.
- mcpgate does not modify MCP message payloads (no content filtering, redaction, or transformation in v1).
- mcpgate does not provide its own LLM or agent runtime.
- mcpgate does not replace an organization's IdP. It integrates with existing ones.
- mcpgate is not a service mesh or general-purpose API gateway. It is purpose-built for MCP.

---

## 4. Target Users and Personas

**Platform Engineer (primary).** Deploys mcpgate, configures upstream servers, sets up SAML, and defines RBAC policies. Needs Terraform/Helm-friendly config, clear logs, and alerting hooks.

**Security/Compliance Officer.** Reviews audit logs, verifies that access policies are enforced, and exports reports. Needs immutable, searchable logs and exportable metrics.

**AI/ML Engineer.** Develops agents that consume MCP tools. Needs the proxy to be invisible during development but wants per-tool latency data to optimize agent behavior.

**Team Lead/Admin.** Manages which team members can access which tools. Uses the admin console to assign roles without writing YAML.

---

## 5. Architecture Overview

```
┌──────────────────────────────────────────────────────────────────┐
│                        MCP Clients                               │
│   (Claude Desktop, VS Code, Custom Agents, Claude Code)          │
└──────────────┬───────────────────────────────────────────────────┘
               │  HTTPS (Streamable HTTP / SSE)
               ▼
┌──────────────────────────────────────────────────────────────────┐
│                      mcpgate Proxy                               │
│                                                                  │
│  ┌─────────┐  ┌──────────┐  ┌──────────┐  ┌─────────────────┐  │
│  │  Auth    │  │  RBAC    │  │  Logging │  │  Metrics        │  │
│  │  Layer   │  │  Engine  │  │  Pipeline│  │  Collector      │  │
│  │(SAML/   │  │(OAuth/   │  │(Struct.  │  │(Prometheus/     │  │
│  │ OAuth)  │  │ Casbin)  │  │ JSON)    │  │ OpenTelemetry)  │  │
│  └────┬────┘  └────┬─────┘  └────┬─────┘  └────────┬────────┘  │
│       │            │             │                   │           │
│  ┌────▼────────────▼─────────────▼───────────────────▼────────┐ │
│  │              Core Proxy Engine (Transport Adapters)         │ │
│  │     ┌──────────┐  ┌──────────┐  ┌────────────────┐        │ │
│  │     │  stdio   │  │   SSE    │  │ Streamable HTTP │        │ │
│  │     │  bridge  │  │  relay   │  │    passthrough  │        │ │
│  │     └──────────┘  └──────────┘  └────────────────┘        │ │
│  └────────────────────────────────────────────────────────────┘ │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │         Admin Console API (REST + WebSocket)               │  │
│  └────────────────────────────────────────────────────────────┘  │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐  │
│  │         Data Layer (PostgreSQL + Redis)                     │  │
│  └────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
               │
               │  stdio / SSE / Streamable HTTP
               ▼
┌──────────────────────────────────────────────────────────────────┐
│                    Upstream MCP Servers                           │
│   (Filesystem, GitHub, Slack, Database, Custom Tools, etc.)      │
└──────────────────────────────────────────────────────────────────┘
```

### Component Responsibilities

**Auth Layer.** Handles SAML 2.0 SP initiation and assertion consumption for user authentication. Issues short-lived JWTs for session management. Handles OAuth 2.0 token issuance for programmatic/API access with scope-based permissions.

**RBAC Engine.** Evaluates authorization policies using Casbin with a PostgreSQL policy adapter. Policies map roles to permissions on specific MCP servers, tools, and resource patterns. Supports deny-by-default with explicit allow rules.

**Logging Pipeline.** Captures structured JSON logs for every proxied message. Logs are written asynchronously via a buffered channel to avoid impacting proxy latency. Supports pluggable sinks (stdout, file, PostgreSQL, and external systems via OTLP).

**Metrics Collector.** Tracks request count, latency histograms (p50/p95/p99), error rates, and active sessions per user, per server, and per tool. Exposes a `/metrics` endpoint in Prometheus exposition format. Integrates with OpenTelemetry for distributed tracing.

**Core Proxy Engine.** The central message router. Accepts connections from clients, matches them to upstream server configurations, applies auth and RBAC checks, proxies the MCP messages, and feeds the logging and metrics pipelines. Transport adapters handle protocol-specific concerns.

**Admin Console API.** A RESTful API (with WebSocket support for real-time log streaming) consumed by the React admin UI. Provides CRUD for server configs, role definitions, user mappings, SAML IdP settings, and log/metrics queries.

**Data Layer.** PostgreSQL for durable storage (configs, policies, audit logs, user records). Redis for session cache, rate-limit counters, and real-time metrics aggregation.

---

## 6. Detailed Feature Specifications

### 6.1 MCP Protocol Proxy

**6.1.1 Transport Support**

| Transport | Client-Facing | Upstream | Notes |
|-----------|--------------|----------|-------|
| Streamable HTTP | ✅ | ✅ | Primary transport. Bidirectional over HTTP POST + optional SSE. |
| SSE | ✅ | ✅ | Legacy transport. Client connects via GET for server events, POST for client messages. |
| stdio | ❌ (converted) | ✅ | mcpgate can spawn and manage stdio-based MCP servers as child processes, bridging them to HTTP for clients. |

**6.1.2 Message Handling**

The proxy intercepts MCP JSON-RPC messages at the transport layer. It does not parse or validate the MCP schema beyond extracting the `method` field (for RBAC routing) and the `id` field (for request/response correlation). This keeps the proxy forward-compatible with MCP specification changes.

Extracted fields per message:

- `jsonrpc` — validated as "2.0"
- `method` — e.g., `tools/call`, `tools/list`, `resources/read`, `prompts/get`
- `id` — for request/response correlation and latency measurement
- `params.name` — for tool-call-level RBAC (extracted from `tools/call` messages)
- Response `result` or `error` — for success/failure metrics

**6.1.3 Session Management**

Each authenticated client connection is assigned a session ID (UUID v7 for time-ordering). The session binds a user identity to a set of upstream server connections. Session metadata is cached in Redis with a configurable TTL (default: 1 hour, sliding).

**6.1.4 Connection Pooling**

For HTTP-based upstream transports, mcpgate maintains a connection pool per upstream server with configurable max connections, idle timeout, and health-check intervals. For stdio-based upstreams, mcpgate manages a process pool with configurable concurrency limits.

**6.1.5 Health Checks**

mcpgate periodically sends `ping` messages to upstream servers (where supported) and tracks their availability. Unhealthy servers are removed from routing until recovery. The admin console displays real-time server health status.

---

### 6.2 Authentication (SAML 2.0)

**6.2.1 SAML Service Provider**

mcpgate acts as a SAML 2.0 Service Provider. It supports:

- SP-initiated SSO (redirect binding for AuthnRequest, POST binding for Response)
- IdP-initiated SSO
- Single Logout (SLO) via both redirect and POST bindings
- Multiple IdP configurations (for organizations with multiple identity providers or multi-tenant deployments)
- Signed AuthnRequests (RSA-SHA256)
- Encrypted assertions (AES-256-CBC with RSA-OAEP key transport)
- NameID formats: emailAddress, persistent, transient

**6.2.2 Attribute Mapping**

Configurable mapping from SAML assertion attributes to internal user properties:

```yaml
saml:
  attribute_mapping:
    email: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
    display_name: "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/name"
    groups: "http://schemas.xmlsoap.org/claims/Group"
    department: "urn:oid:2.16.840.1.113730.3.1.241"
```

Group attributes can be mapped to mcpgate roles automatically (e.g., IdP group "engineering" → mcpgate role "developer").

**6.2.3 SAML Configuration via Admin Console**

The admin console provides a guided SAML setup flow:

1. Upload or paste IdP metadata XML (or enter IdP SSO URL + certificate manually).
2. Download mcpgate SP metadata XML for import into the IdP.
3. Configure attribute mappings with a visual mapper.
4. Test the SAML flow with a "Test Login" button that performs a real SP-initiated SSO and displays the parsed assertion.

---

### 6.3 Authorization (OAuth 2.0 + RBAC)

**6.3.1 OAuth 2.0 Authorization Server**

mcpgate includes a lightweight OAuth 2.0 authorization server for programmatic access (agent-to-proxy, CI/CD pipelines, service accounts). Supported grants:

- Authorization Code with PKCE (for interactive clients)
- Client Credentials (for service-to-service)
- Token Refresh

Tokens are JWTs signed with RS256, containing the user's roles and scoped permissions as claims. Token lifetime is configurable (default: access token 15 min, refresh token 7 days).

**6.3.2 RBAC Model**

The authorization model uses Casbin with a custom model definition:

```
[request_definition]
r = sub, obj_server, obj_tool, act

[policy_definition]
p = sub, obj_server, obj_tool, act, eft

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

[matchers]
m = g(r.sub, p.sub) && (p.obj_server == "*" || r.obj_server == p.obj_server) && (p.obj_tool == "*" || r.obj_tool == p.obj_tool) && (p.act == "*" || r.act == p.act)
```

This supports:

- **Role hierarchy.** `admin` inherits from `developer`, which inherits from `viewer`.
- **Server-level permissions.** Allow/deny access to entire MCP servers.
- **Tool-level permissions.** Allow/deny specific tools within a server (e.g., allow `read_file` but deny `write_file` on the filesystem server).
- **Action-level permissions.** Distinguish between `tools/list` (discovery) and `tools/call` (execution), or between `resources/read` and `resources/write`.
- **Explicit deny.** Deny rules override allow rules for safety.

**6.3.3 Default Roles**

| Role | Permissions |
|------|------------|
| `super_admin` | Full access to admin console, all servers, all tools. Can manage SAML, roles, and users. |
| `admin` | Admin console access. Can manage roles and view logs. Cannot modify SAML or system config. |
| `operator` | Can view logs and metrics. Cannot modify configurations. |
| `developer` | Can invoke tools on assigned servers. No admin console access. |
| `viewer` | Can list tools and read resources. Cannot invoke tools. |

Administrators can create custom roles with arbitrary permission sets.

**6.3.4 Policy Evaluation Flow**

```
1. Extract user identity from JWT (set by SAML login or OAuth token)
2. Extract target server and tool from the MCP message
3. Query Casbin enforcer with (user_role, server_id, tool_name, action)
4. If ALLOW → proxy the message
5. If DENY → return MCP error response (-32600 "Unauthorized") and log the denial
```

Policy decisions are cached in Redis (TTL: 30s) to minimize database queries.

---

### 6.4 Logging and Audit Trail

**6.4.1 Log Entry Schema**

Every proxied MCP message generates a structured log entry:

```json
{
  "timestamp": "2026-05-22T14:30:00.000Z",
  "level": "INFO",
  "trace_id": "abc123def456",
  "session_id": "01902e8a-...",
  "user_id": "jane.doe@company.com",
  "user_roles": ["developer"],
  "client_ip": "10.0.1.42",
  "upstream_server": "github-mcp",
  "mcp_method": "tools/call",
  "tool_name": "create_issue",
  "request_id": "req-789",
  "direction": "request",
  "latency_ms": 142,
  "status": "success",
  "error_code": null,
  "error_message": null,
  "request_size_bytes": 1024,
  "response_size_bytes": 2048,
  "rbac_decision": "allow",
  "metadata": {
    "user_agent": "claude-desktop/1.2.3",
    "mcp_protocol_version": "2025-06-18"
  }
}
```

**6.4.2 Log Storage and Retention**

- **Hot storage (PostgreSQL):** Last 30 days of logs, fully indexed for search and filtering.
- **Warm storage (compressed JSON files):** 30–90 days, queryable via the admin API with slower response times.
- **Cold export:** Configurable export to S3-compatible object storage, OTLP collectors, or syslog endpoints for long-term retention.
- **Retention policies:** Configurable per log level and per server. Automatic purge of expired logs via a background cron job.

**6.4.3 Audit Immutability**

Audit log entries are append-only. The admin console does not expose a delete API for audit logs. Each log entry includes a SHA-256 hash of its content chained to the previous entry's hash (lightweight tamper-evidence, not a blockchain). Tampering is detectable by verifying the hash chain.

**6.4.4 Log Pipeline Architecture**

```
MCP Message → Proxy Handler → Log Entry Builder → Buffered Channel (size: 10,000)
    → Log Writer Goroutine Pool (N=4) → PostgreSQL COPY batch insert (every 100ms or 500 entries)
    → Async export to external sinks (OTLP, S3, syslog)
```

The buffered channel decouples logging from the request hot path. If the channel is full (backpressure), log entries are written to a local WAL file and replayed when capacity is available. No log entry is silently dropped.

---

### 6.5 Metrics and Observability

**6.5.1 Proxy Metrics (Prometheus)**

| Metric | Type | Labels |
|--------|------|--------|
| `mcpgate_requests_total` | Counter | `server`, `method`, `tool`, `status`, `user` |
| `mcpgate_request_duration_seconds` | Histogram | `server`, `method`, `tool` |
| `mcpgate_active_sessions` | Gauge | `server` |
| `mcpgate_active_connections` | Gauge | `server`, `transport` |
| `mcpgate_upstream_health` | Gauge | `server` (0=down, 1=up) |
| `mcpgate_rbac_denials_total` | Counter | `server`, `tool`, `user`, `role` |
| `mcpgate_auth_attempts_total` | Counter | `method` (saml/oauth), `status` |
| `mcpgate_log_pipeline_backlog` | Gauge | — |
| `mcpgate_upstream_latency_seconds` | Histogram | `server` |

**6.5.2 OpenTelemetry Integration**

mcpgate produces OTLP traces for every proxied request, enabling distributed tracing across the client → proxy → upstream server chain. Spans include:

- `proxy.auth` — authentication check
- `proxy.rbac` — authorization evaluation
- `proxy.upstream` — time spent waiting for the upstream server
- `proxy.log` — time spent constructing the log entry (should be <1ms)

**6.5.3 Built-in Dashboard Metrics**

The admin console displays pre-built dashboards:

- **Overview:** Request volume (RPM), error rate, p95 latency, active users, server health.
- **Per-Server:** Drill-down into individual MCP server metrics with tool-level breakdown.
- **Per-User:** Usage patterns, most-used tools, error rates, and request volume over time.
- **RBAC:** Denied request heatmap (which users are hitting which policy boundaries).
- **System:** Go runtime metrics (goroutines, heap, GC pause), connection pool utilization, log pipeline health.

---

### 6.6 Admin Console (React)

**6.6.1 Pages and Features**

| Page | Description |
|------|------------|
| **Dashboard** | Real-time overview with key metrics, server health indicators, and recent activity feed. |
| **Servers** | CRUD for upstream MCP server configurations. Test connectivity. View per-server metrics. |
| **Users** | List authenticated users (synced from SAML). Assign/revoke roles. View per-user activity. |
| **Roles & Policies** | Create/edit roles. Define permission policies with a visual editor (server → tool → action matrix). |
| **Logs** | Full-text search and filter across audit logs. Export as CSV/JSON. Real-time log tail via WebSocket. |
| **Metrics** | Interactive charts for all Prometheus metrics. Date range selection. Comparison views. |
| **SAML Config** | Guided IdP setup. Metadata upload/download. Attribute mapping. Test flow. Certificate management. |
| **OAuth Config** | Manage OAuth clients (create/revoke). View active tokens. Configure token lifetimes. |
| **Settings** | System-level settings: log retention, session TTL, rate limits, proxy timeouts, TLS configuration. |

**6.6.2 UI/UX Principles**

- Accessible (WCAG 2.1 AA).
- Responsive (usable on tablet; not optimized for phone).
- Dark/light mode support.
- All destructive actions require confirmation.
- Changes to RBAC policies show a diff preview before applying.
- Real-time updates via WebSocket (no polling).

**6.6.3 Technology Stack**

- React 18+ with TypeScript
- TanStack Router for routing
- TanStack Query for server state management
- Zustand for client state
- Tailwind CSS for styling
- Recharts for metrics visualization
- React Table for log and user tables with sorting/filtering/pagination
- Vitest + React Testing Library for unit/integration tests
- Playwright for E2E tests

---

### 6.7 Rate Limiting and Quotas

**6.7.1 Rate Limiting**

Configurable per user, per role, and per server:

- Requests per minute (RPM)
- Requests per day (RPD)
- Concurrent connections per user

Rate limit state is stored in Redis using a sliding window algorithm. When a limit is exceeded, mcpgate returns an MCP error response with code `-32000` and a `Retry-After` hint.

**6.7.2 Quota Management**

Administrators can set daily and monthly request quotas per user or per team. The admin console displays quota utilization with warning thresholds (80%, 90%, 100%). Notifications are sent via configurable webhooks when thresholds are crossed.

---

### 6.8 Additional Features

**6.8.1 Server Registry and Discovery**

mcpgate maintains a registry of upstream MCP servers. Each entry includes:

- Server name and description
- Transport type and connection details (URL, command for stdio)
- Expected capabilities (tools, resources, prompts)
- Health check configuration
- RBAC policy bindings

Servers can be registered via the admin console, config file, or API. mcpgate periodically calls `initialize` and `tools/list` on registered servers to keep the capability cache fresh.

**6.8.2 Request/Response Validation**

Optional JSON-RPC 2.0 schema validation on proxied messages. When enabled, malformed messages are rejected with a clear error before reaching the upstream server. This protects upstream servers from malformed input and provides better error messages to clients.

**6.8.3 Circuit Breaker**

Per-upstream-server circuit breaker (states: closed, open, half-open). Configurable failure threshold, recovery timeout, and half-open probe count. When a circuit is open, mcpgate returns a fast error to clients rather than queuing requests to a failing server.

**6.8.4 Webhook Notifications**

Configurable webhooks for events:

- Server health changes (up/down)
- RBAC denial spikes (>N denials in M minutes)
- Rate limit breaches
- Authentication failures (>N in M minutes, potential brute-force)
- Quota threshold crossings

**6.8.5 Multi-Tenancy**

Support for logical tenant isolation via namespaces. Each tenant gets its own set of server registrations, RBAC policies, and log partitions. Tenants share the same mcpgate deployment but cannot see each other's data. Useful for SaaS platforms hosting MCP servers for multiple customers.

**6.8.6 Configuration Hot-Reload**

RBAC policies, server registrations, rate limits, and most settings can be changed via the admin console or API without restarting the proxy. Changes take effect within the Redis cache TTL (default: 30 seconds). SAML and TLS certificate changes require a graceful restart.

---

## 7. Security Design (OWASP Alignment)

### 7.1 OWASP Top 10 Mitigations

| OWASP Category | Mitigation |
|----------------|-----------|
| A01: Broken Access Control | Deny-by-default RBAC. Every request checked. No direct object references to upstream servers. |
| A02: Cryptographic Failures | TLS 1.3 enforced for all external connections. Secrets stored encrypted at rest (AES-256-GCM). JWT signed with RS256. SAML assertions encrypted. |
| A03: Injection | JSON-RPC input validated. SQL queries parameterized (no string concatenation). Admin API inputs sanitized. |
| A04: Insecure Design | Threat model documented. Security review checkpoints in every milestone. Principle of least privilege for all defaults. |
| A05: Security Misconfiguration | Secure defaults. No debug endpoints in production builds. Docker image runs as non-root. Hardened Go HTTP server settings. |
| A06: Vulnerable Components | Automated dependency scanning (govulncheck, npm audit) in CI. Dependabot/Renovate enabled. |
| A07: Auth Failures | SAML replay protection (assertion ID tracking). OAuth PKCE enforced. Brute-force detection on login endpoints. JWT with short TTL + rotation. |
| A08: Data Integrity Failures | Audit log hash chain. Signed Docker images. SBOM generation. CI pipeline integrity (signed commits). |
| A09: Logging Failures | Comprehensive structured logging with tamper evidence. Log pipeline backpressure handling (no silent drops). |
| A10: SSRF | Upstream server URLs restricted to allowlist. No user-controlled URL parameters in proxy routing. |

### 7.2 Additional Security Measures

- **Secret management:** Support for external secret stores (HashiCorp Vault, AWS Secrets Manager) via environment variable or sidecar patterns. Local secrets encrypted with a master key derived from a passphrase via Argon2id.
- **TLS termination:** mcpgate terminates TLS. Upstream connections can be TLS or plaintext (configurable per server). mTLS supported for upstream servers that require it.
- **CSP headers:** Admin console served with strict Content-Security-Policy, X-Frame-Options, and other security headers.
- **Session fixation:** New session ID generated after SAML authentication. Session bound to client IP and user-agent (configurable strictness).
- **CORS:** Admin API CORS restricted to the admin console origin. Proxy endpoints do not serve CORS headers (not browser-accessible by design).

---

## 8. Technology Stack

### Backend (Go)

| Component | Technology | Rationale |
|-----------|-----------|-----------|
| HTTP framework | `net/http` + `chi` router | Stdlib-aligned, minimal overhead, middleware-friendly |
| SAML | `crewjam/saml` | Mature Go SAML SP library |
| OAuth | `ory/fosite` | Comprehensive OAuth 2.0 server library |
| RBAC | `casbin/casbin` | Flexible policy engine with PostgreSQL adapter |
| Database | `jackc/pgx` v5 | High-performance PostgreSQL driver |
| Migrations | `golang-migrate/migrate` | Version-controlled schema migrations |
| Redis | `redis/go-redis` v9 | Session cache, rate limiting, metrics aggregation |
| Logging | `log/slog` (stdlib) | Structured logging with JSON handler |
| Metrics | `prometheus/client_golang` | Prometheus exposition |
| Tracing | `go.opentelemetry.io/otel` | Distributed tracing |
| Config | `koanf` | Multi-source config (file, env, flags) with hot-reload |
| Testing | `testing` + `testify` + `testcontainers-go` | Stdlib + assertions + real DB/Redis in tests |
| Linting | `golangci-lint` | Comprehensive static analysis |

### Frontend (React)

| Component | Technology |
|-----------|-----------|
| Framework | React 18 + TypeScript 5 |
| Routing | TanStack Router |
| Server state | TanStack Query |
| Client state | Zustand |
| Styling | Tailwind CSS |
| Charts | Recharts |
| Tables | TanStack Table |
| Forms | React Hook Form + Zod |
| Testing | Vitest + React Testing Library |
| E2E | Playwright |
| Build | Vite |

### Infrastructure

| Component | Technology |
|-----------|-----------|
| Containerization | Docker (multi-stage builds) |
| Orchestration | docker-compose (dev/small), Kubernetes (production) |
| Database | PostgreSQL 16 |
| Cache | Redis 7 |
| CI/CD | GitHub Actions |
| Image registry | GitHub Container Registry |

---

## 9. Data Model

### Core Tables

```sql
-- Users (synced from SAML, managed via admin console)
CREATE TABLE users (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email       TEXT UNIQUE NOT NULL,
    display_name TEXT,
    idp_subject TEXT,           -- SAML NameID
    idp_name    TEXT,           -- Which IdP authenticated this user
    status      TEXT NOT NULL DEFAULT 'active', -- active, suspended, deactivated
    metadata    JSONB DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Roles
CREATE TABLE roles (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT UNIQUE NOT NULL,
    description TEXT,
    is_system   BOOLEAN DEFAULT false, -- true for default roles
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- User-Role assignments
CREATE TABLE user_roles (
    user_id  UUID REFERENCES users(id) ON DELETE CASCADE,
    role_id  UUID REFERENCES roles(id) ON DELETE CASCADE,
    assigned_by UUID REFERENCES users(id),
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);

-- RBAC policies (Casbin-compatible)
CREATE TABLE casbin_rule (
    id    SERIAL PRIMARY KEY,
    ptype TEXT NOT NULL,  -- 'p' for policy, 'g' for role grouping
    v0    TEXT DEFAULT '',
    v1    TEXT DEFAULT '',
    v2    TEXT DEFAULT '',
    v3    TEXT DEFAULT '',
    v4    TEXT DEFAULT '',
    v5    TEXT DEFAULT ''
);

-- Upstream MCP server configurations
CREATE TABLE mcp_servers (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT UNIQUE NOT NULL,
    description TEXT,
    transport   TEXT NOT NULL,  -- 'streamable_http', 'sse', 'stdio'
    config      JSONB NOT NULL, -- Transport-specific config (url, command, args, env)
    health_check JSONB DEFAULT '{}',
    is_enabled  BOOLEAN DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Tool capability cache
CREATE TABLE server_tools (
    server_id   UUID REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name   TEXT NOT NULL,
    description TEXT,
    input_schema JSONB,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, tool_name)
);

-- Audit log
CREATE TABLE audit_logs (
    id              BIGSERIAL PRIMARY KEY,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT now(),
    trace_id        TEXT,
    session_id      UUID,
    user_id         UUID REFERENCES users(id),
    user_email      TEXT,
    client_ip       INET,
    server_id       UUID REFERENCES mcp_servers(id),
    server_name     TEXT,
    mcp_method      TEXT,
    tool_name       TEXT,
    direction       TEXT,  -- 'request', 'response', 'notification'
    status          TEXT,  -- 'success', 'error', 'denied'
    error_code      INTEGER,
    error_message   TEXT,
    latency_ms      INTEGER,
    request_size    INTEGER,
    response_size   INTEGER,
    rbac_decision   TEXT,
    metadata        JSONB DEFAULT '{}',
    hash_chain      TEXT  -- SHA-256 hash for tamper evidence
);

-- Partitioned by month for efficient retention management
-- CREATE TABLE audit_logs (...) PARTITION BY RANGE (timestamp);

-- SAML IdP configurations
CREATE TABLE saml_idp_configs (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name            TEXT UNIQUE NOT NULL,
    entity_id       TEXT NOT NULL,
    sso_url         TEXT NOT NULL,
    slo_url         TEXT,
    certificate     TEXT NOT NULL,  -- PEM-encoded X.509
    attribute_map   JSONB DEFAULT '{}',
    group_role_map  JSONB DEFAULT '{}',
    is_enabled      BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth clients
CREATE TABLE oauth_clients (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id       TEXT UNIQUE NOT NULL,
    client_secret   TEXT NOT NULL,  -- bcrypt hashed
    name            TEXT NOT NULL,
    grant_types     TEXT[] NOT NULL,
    redirect_uris   TEXT[],
    scopes          TEXT[],
    owner_id        UUID REFERENCES users(id),
    is_active       BOOLEAN DEFAULT true,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Sessions
CREATE TABLE sessions (
    id          UUID PRIMARY KEY,
    user_id     UUID REFERENCES users(id) ON DELETE CASCADE,
    client_ip   INET,
    user_agent  TEXT,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Indexes
CREATE INDEX idx_audit_logs_timestamp ON audit_logs (timestamp DESC);
CREATE INDEX idx_audit_logs_user_id ON audit_logs (user_id);
CREATE INDEX idx_audit_logs_server_name ON audit_logs (server_name);
CREATE INDEX idx_audit_logs_tool_name ON audit_logs (tool_name);
CREATE INDEX idx_audit_logs_status ON audit_logs (status);
CREATE INDEX idx_audit_logs_session ON audit_logs (session_id);
```

---

## 10. API Design

### 10.1 Proxy Endpoints

```
POST   /mcp/{server_name}/message       — Streamable HTTP message endpoint
GET    /mcp/{server_name}/sse            — SSE event stream
POST   /mcp/{server_name}/sse            — SSE client-to-server messages
```

All proxy endpoints require a valid session (JWT in Authorization header or session cookie).

### 10.2 Auth Endpoints

```
GET    /auth/saml/login                  — Initiate SAML SP login
POST   /auth/saml/acs                    — SAML Assertion Consumer Service
GET    /auth/saml/logout                 — Initiate SAML SLO
POST   /auth/saml/slo                    — SAML SLO response
GET    /auth/saml/metadata               — SP metadata XML

POST   /auth/oauth/token                 — OAuth token endpoint
POST   /auth/oauth/authorize             — OAuth authorization endpoint
POST   /auth/oauth/revoke                — Revoke token
GET    /auth/oauth/jwks                  — JWKS endpoint
```

### 10.3 Admin API Endpoints

All prefixed with `/api/v1/admin/`. Require admin role.

```
# Servers
GET    /servers                          — List registered MCP servers
POST   /servers                          — Register a new server
GET    /servers/{id}                     — Get server details + cached tools
PUT    /servers/{id}                     — Update server config
DELETE /servers/{id}                     — Remove server
POST   /servers/{id}/test                — Test connectivity
POST   /servers/{id}/refresh-tools       — Re-discover tools

# Users
GET    /users                            — List users (paginated, filterable)
GET    /users/{id}                       — Get user details + roles + activity
PUT    /users/{id}/roles                 — Assign roles
PUT    /users/{id}/status                — Activate/suspend user

# Roles & Policies
GET    /roles                            — List roles
POST   /roles                            — Create role
PUT    /roles/{id}                       — Update role
DELETE /roles/{id}                       — Delete role (fails if users assigned)
GET    /policies                         — List RBAC policies
POST   /policies                         — Add policy
PUT    /policies/{id}                    — Update policy
DELETE /policies/{id}                    — Remove policy
POST   /policies/evaluate                — Test a policy evaluation (dry run)

# Logs
GET    /logs                             — Query audit logs (paginated, filterable)
GET    /logs/export                      — Export logs as CSV/JSON
WS     /logs/stream                      — Real-time log tail (WebSocket)

# Metrics
GET    /metrics/summary                  — Dashboard summary metrics
GET    /metrics/timeseries               — Time-series data for charts
GET    /metrics/per-user                 — Per-user usage breakdown
GET    /metrics/per-server               — Per-server breakdown

# SAML
GET    /saml/configs                     — List IdP configs
POST   /saml/configs                     — Add IdP config
PUT    /saml/configs/{id}                — Update IdP config
DELETE /saml/configs/{id}                — Remove IdP config
POST   /saml/configs/{id}/test           — Test SAML flow

# OAuth
GET    /oauth/clients                    — List OAuth clients
POST   /oauth/clients                    — Create client
DELETE /oauth/clients/{id}               — Revoke client

# Settings
GET    /settings                         — Get system settings
PUT    /settings                         — Update settings

# System
GET    /health                           — Health check (no auth required)
GET    /ready                            — Readiness check (DB + Redis)
GET    /metrics                          — Prometheus metrics (no auth required, IP-restricted)
```

---

## 11. Project Structure

```
mcpgate/
├── cmd/
│   └── mcpgate/
│       └── main.go                      # Entry point
├── internal/
│   ├── config/
│   │   ├── config.go                    # Configuration struct + loader
│   │   └── config_test.go
│   ├── proxy/
│   │   ├── handler.go                   # Core proxy HTTP handler
│   │   ├── session.go                   # Session management
│   │   ├── transport/
│   │   │   ├── streamable.go            # Streamable HTTP adapter
│   │   │   ├── sse.go                   # SSE adapter
│   │   │   ├── stdio.go                 # stdio bridge (process manager)
│   │   │   └── pool.go                  # Connection pooling
│   │   ├── circuit/
│   │   │   └── breaker.go               # Circuit breaker
│   │   ├── health/
│   │   │   └── checker.go               # Upstream health checks
│   │   └── handler_test.go
│   ├── auth/
│   │   ├── saml/
│   │   │   ├── sp.go                    # SAML Service Provider
│   │   │   ├── handlers.go              # SAML HTTP handlers
│   │   │   ├── attribute_map.go         # Attribute mapping
│   │   │   └── sp_test.go
│   │   ├── oauth/
│   │   │   ├── server.go                # OAuth 2.0 authorization server
│   │   │   ├── handlers.go              # OAuth HTTP handlers
│   │   │   ├── jwt.go                   # JWT creation/validation
│   │   │   └── server_test.go
│   │   └── middleware.go                # Auth middleware (JWT validation)
│   ├── rbac/
│   │   ├── enforcer.go                  # Casbin enforcer wrapper
│   │   ├── middleware.go                # RBAC middleware
│   │   ├── policies.go                  # Policy CRUD
│   │   └── enforcer_test.go
│   ├── logging/
│   │   ├── pipeline.go                  # Async log pipeline
│   │   ├── entry.go                     # Log entry builder
│   │   ├── hashchain.go                 # Hash chain for tamper evidence
│   │   ├── sinks/
│   │   │   ├── postgres.go              # PostgreSQL sink
│   │   │   ├── otlp.go                  # OTLP export sink
│   │   │   └── file.go                  # File sink (WAL fallback)
│   │   └── pipeline_test.go
│   ├── metrics/
│   │   ├── collector.go                 # Prometheus metrics registration
│   │   └── collector_test.go
│   ├── admin/
│   │   ├── api/
│   │   │   ├── router.go               # Admin API router
│   │   │   ├── servers.go              # Server management handlers
│   │   │   ├── users.go                # User management handlers
│   │   │   ├── roles.go                # Role management handlers
│   │   │   ├── logs.go                 # Log query handlers
│   │   │   ├── metrics.go             # Metrics query handlers
│   │   │   ├── saml.go                # SAML config handlers
│   │   │   ├── oauth.go              # OAuth client handlers
│   │   │   ├── settings.go           # Settings handlers
│   │   │   └── websocket.go          # WebSocket log streaming
│   │   └── api_test.go
│   ├── store/
│   │   ├── postgres/
│   │   │   ├── users.go                # User repository
│   │   │   ├── servers.go              # Server repository
│   │   │   ├── logs.go                 # Audit log repository
│   │   │   ├── sessions.go            # Session repository
│   │   │   └── migrations/            # SQL migration files
│   │   │       ├── 001_initial.up.sql
│   │   │       ├── 001_initial.down.sql
│   │   │       └── ...
│   │   └── redis/
│   │       ├── session_cache.go        # Session cache
│   │       ├── rate_limiter.go         # Rate limiting
│   │       └── policy_cache.go         # RBAC policy cache
│   └── server/
│       └── server.go                    # HTTP server setup + graceful shutdown
├── web/                                 # React admin console
│   ├── src/
│   │   ├── api/                        # API client + TanStack Query hooks
│   │   ├── components/                 # Shared components
│   │   ├── pages/                      # Page components
│   │   │   ├── Dashboard/
│   │   │   ├── Servers/
│   │   │   ├── Users/
│   │   │   ├── Roles/
│   │   │   ├── Logs/
│   │   │   ├── Metrics/
│   │   │   ├── SamlConfig/
│   │   │   ├── OAuthConfig/
│   │   │   └── Settings/
│   │   ├── hooks/                      # Custom React hooks
│   │   ├── store/                      # Zustand stores
│   │   ├── types/                      # TypeScript type definitions
│   │   └── utils/                      # Utility functions
│   ├── tests/
│   │   ├── unit/                       # Vitest unit tests
│   │   ├── integration/                # Component integration tests
│   │   └── e2e/                        # Playwright E2E tests
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   └── tailwind.config.ts
├── deployments/
│   ├── docker/
│   │   ├── Dockerfile                  # Multi-stage build
│   │   ├── Dockerfile.dev              # Development image
│   │   └── docker-compose.yml          # Full stack (proxy + DB + Redis + console)
│   ├── kubernetes/
│   │   ├── base/                       # Kustomize base
│   │   └── overlays/
│   │       ├── dev/
│   │       └── prod/
│   └── terraform/                      # Optional cloud provisioning
├── scripts/
│   ├── setup.sh                        # Local dev setup
│   ├── seed.sh                         # Seed database with sample data
│   └── generate-certs.sh               # Generate dev TLS/SAML certs
├── configs/
│   ├── mcpgate.yaml                    # Default configuration
│   └── mcpgate.example.yaml            # Example with all options documented
├── docs/
│   ├── architecture.md
│   ├── deployment.md
│   ├── configuration.md
│   ├── saml-setup.md
│   └── api-reference.md
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## 12. Implementation Roadmap

### Phase 0: Foundation (Weeks 1–2)

**Objective:** Skeleton project with CI, basic proxy passthrough, and database schema.

| Task | Description | Estimate |
|------|------------|----------|
| Project scaffolding | Go module, directory structure, Makefile, linting, CI pipeline | 2d |
| Docker setup | Multi-stage Dockerfile, docker-compose with PostgreSQL + Redis | 1d |
| Configuration system | koanf-based config loader with YAML + env vars + defaults | 1d |
| Database schema | Initial migration with all core tables. pgx connection pool setup | 2d |
| Basic HTTP server | chi router, health/ready endpoints, graceful shutdown | 1d |
| Basic proxy passthrough | Streamable HTTP proxy handler (no auth, no logging) — proof of concept | 3d |

**Milestone:** A request to `POST /mcp/{server}/message` is proxied to an upstream MCP server and the response is returned to the client. docker-compose brings up the full stack.

**Tests:** Unit tests for config loading, database connection. Integration test: proxy passthrough with a mock MCP server.

---

### Phase 1: Transport Adapters + Session Management (Weeks 3–4)

**Objective:** Full transport support and session infrastructure.

| Task | Description | Estimate |
|------|------------|----------|
| SSE transport adapter | Relay SSE events from upstream, handle client POST messages | 3d |
| stdio bridge | Process manager for stdio-based MCP servers. Spawn, manage, and bridge to HTTP | 3d |
| Connection pooling | HTTP connection pool with configurable limits and health checks | 2d |
| Session management | UUID v7 session IDs, Redis-backed session cache, session lifecycle | 2d |

**Milestone:** All three transports work. Sessions are created and tracked in Redis.

**Tests:** Integration tests for each transport adapter with mock servers. Session creation/expiry tests.

---

### Phase 2: Authentication (Weeks 5–7)

**Objective:** SAML 2.0 authentication with IdP integration and OAuth token issuance.

| Task | Description | Estimate |
|------|------------|----------|
| SAML SP implementation | SP-initiated SSO, ACS handler, SLO, metadata generation | 4d |
| SAML attribute mapping | Configurable attribute extraction and user provisioning (JIT) | 2d |
| JWT session tokens | Issue JWTs after SAML auth. Middleware for JWT validation on proxy routes | 2d |
| OAuth authorization server | Authorization Code + PKCE, Client Credentials grants. Token endpoint | 3d |
| OAuth client management | CRUD for OAuth clients. Client secret hashing. Scope validation | 2d |
| Auth middleware | Unified middleware: check JWT (from SAML session or OAuth token) on all proxy requests | 1d |
| SAML test harness | Local IdP simulator (samltest.id integration or local SimpleSAMLphp) for dev/test | 1d |

**Milestone:** Users authenticate via SAML. Agents authenticate via OAuth. Unauthenticated requests are rejected.

**Tests:** Unit tests for JWT creation/validation, SAML assertion parsing. Integration tests with a mock IdP. E2E test: full SAML login flow.

---

### Phase 3: Authorization / RBAC (Weeks 8–9)

**Objective:** Fine-grained access control for MCP servers and tools.

| Task | Description | Estimate |
|------|------------|----------|
| Casbin setup | Model definition, PostgreSQL adapter, enforcer initialization | 2d |
| RBAC middleware | Intercept `tools/call` and `resources/read` messages. Extract target server + tool. Evaluate policy. Block or allow | 3d |
| Default roles and policies | Seed the five default roles with appropriate policies | 1d |
| Policy CRUD API | Admin API endpoints for managing policies. Dry-run evaluation endpoint | 2d |
| Redis policy cache | Cache policy decisions with TTL. Invalidation on policy change | 1d |
| SAML group → role mapping | Auto-assign roles based on IdP group attributes | 1d |

**Milestone:** Requests are authorized per RBAC policy. Unauthorized tool calls return MCP error. Policies are manageable via API.

**Tests:** Unit tests for Casbin model (allow/deny scenarios, role inheritance). Integration tests: policy enforcement end-to-end.

---

### Phase 4: Logging and Audit (Weeks 10–11)

**Objective:** Comprehensive audit trail with async pipeline and tamper evidence.

| Task | Description | Estimate |
|------|------------|----------|
| Log entry builder | Construct structured log entries from proxied messages. Extract method, tool, latency, sizes | 2d |
| Async log pipeline | Buffered channel, writer goroutine pool, batch PostgreSQL inserts | 2d |
| Hash chain | SHA-256 hash chain for audit log tamper evidence | 1d |
| WAL fallback | Write-ahead log for entries that can't be written to PostgreSQL (backpressure) | 1d |
| Log query API | Paginated, filterable log queries. Full-text search. CSV/JSON export | 2d |
| Log retention | Background job for partition management and purge of expired logs | 1d |

**Milestone:** Every proxied message is logged with full metadata. Logs are queryable via the admin API. Hash chain is verifiable.

**Tests:** Unit tests for log entry builder and hash chain. Integration tests: log pipeline under load. Backpressure test: verify no entries are dropped when PostgreSQL is slow.

---

### Phase 5: Metrics and Observability (Weeks 12–13)

**Objective:** Prometheus metrics, OpenTelemetry tracing, and metrics query API.

| Task | Description | Estimate |
|------|------------|----------|
| Prometheus metrics | Register all metrics. Instrument proxy handler, auth middleware, RBAC middleware | 2d |
| OpenTelemetry tracing | Trace spans for auth, RBAC, upstream, logging. OTLP exporter configuration | 2d |
| Metrics query API | Time-series aggregation queries for the admin dashboard (per-user, per-server, per-tool) | 2d |
| Circuit breaker | Per-server circuit breaker with configurable thresholds | 1d |
| Upstream health checks | Periodic ping to upstream servers. Health status tracking and reporting | 1d |
| Rate limiting | Redis-based sliding window rate limiter. Per-user and per-role limits | 2d |

**Milestone:** `/metrics` endpoint serves Prometheus data. Traces are exported to an OTLP collector. Circuit breaker and rate limiting are functional.

**Tests:** Unit tests for circuit breaker state machine, rate limiter. Integration tests: verify Prometheus metrics under load. Rate limit enforcement test.

---

### Phase 6: Admin Console — Core (Weeks 14–17)

**Objective:** React admin console with core pages.

| Task | Description | Estimate |
|------|------------|----------|
| Project setup | Vite + React + TypeScript + Tailwind + TanStack Router/Query. Design system tokens | 2d |
| API client layer | Typed API client with TanStack Query hooks for all admin endpoints | 2d |
| Authentication flow | Admin login via SAML redirect. JWT storage. Protected routes | 2d |
| Dashboard page | Real-time metrics overview, server health, recent activity | 3d |
| Servers page | CRUD for MCP servers. Test connectivity button. Tool list display | 3d |
| Users page | User list with search/filter. Role assignment. Activity summary | 2d |
| Roles & Policies page | Role CRUD. Visual policy editor (server × tool × action matrix). Policy diff preview | 4d |
| Logs page | Searchable, filterable log table with pagination. Detail drawer. CSV export | 3d |
| Real-time log tail | WebSocket connection for live log streaming. Auto-scroll with pause | 2d |

**Milestone:** Administrators can manage servers, users, roles, and view logs through the web console.

**Tests:** Vitest unit tests for all components. Integration tests for API client hooks. Playwright E2E tests for critical flows (login → add server → assign role → view logs).

---

### Phase 7: Admin Console — Advanced (Weeks 18–19)

**Objective:** Metrics dashboards, SAML configuration, and settings pages.

| Task | Description | Estimate |
|------|------------|----------|
| Metrics page | Interactive time-series charts (Recharts). Date range picker. Per-user and per-server drilldown | 3d |
| SAML config page | Guided IdP setup wizard. Metadata upload. Attribute mapper. Test login button | 3d |
| OAuth config page | Client management (create/revoke). Active token list | 1d |
| Settings page | System settings editor. Log retention, rate limits, session TTL | 1d |
| Dark mode | Tailwind dark mode support. Theme toggle | 1d |
| Accessibility audit | WCAG 2.1 AA compliance pass. Screen reader testing | 1d |

**Milestone:** Full admin console feature set complete. All pages functional and accessible.

**Tests:** E2E tests for SAML config flow, metrics page interactions. Accessibility tests.

---

### Phase 8: Hardening and Production Readiness (Weeks 20–22)

**Objective:** Security hardening, performance testing, documentation, and production packaging.

| Task | Description | Estimate |
|------|------------|----------|
| Security audit | OWASP checklist walkthrough. Dependency scan. Secret handling review | 2d |
| Penetration testing | Automated security scanning (OWASP ZAP). Manual review of auth flows | 2d |
| Performance testing | Load testing with k6: proxy throughput, latency under load, log pipeline capacity | 2d |
| Chaos testing | Simulate upstream server failures, database outages, Redis unavailability. Verify graceful degradation | 2d |
| Documentation | Architecture doc, deployment guide, configuration reference, SAML setup guide, API reference | 3d |
| Docker image optimization | Minimize image size (distroless base). Non-root user. Security scan (Trivy) | 1d |
| Kubernetes manifests | Kustomize base + overlays. Resource limits, HPA, PDB, network policies | 2d |
| SBOM and signing | Generate SBOM (Syft). Sign Docker images (Cosign). Provenance attestation | 1d |

**Milestone:** Production-ready release candidate. All security checks pass. Documentation complete.

---

### Phase 9: Multi-Tenancy and Advanced Features (Weeks 23–26)

**Objective:** Multi-tenancy, webhooks, and operational polish.

| Task | Description | Estimate |
|------|------------|----------|
| Multi-tenancy | Namespace isolation for servers, policies, and logs. Tenant-scoped admin console | 4d |
| Webhook notifications | Configurable webhooks for health, security, and quota events | 2d |
| Quota management | Request quota enforcement per user/team. Admin console quota dashboard | 2d |
| Config hot-reload | Watch for config changes, reload RBAC policies and rate limits without restart | 2d |
| Request/response validation | Optional JSON-RPC 2.0 validation with configurable strictness | 2d |
| Server discovery | Auto-discover tools on server registration. Periodic refresh | 1d |
| Helm chart | Packaged Helm chart for Kubernetes deployment | 3d |

**Milestone:** v1.0 release. All features complete and tested.

---

## 13. Testing Strategy

### 13.1 Test Pyramid

```
          ┌──────────┐
          │   E2E    │  ~20 tests (Playwright)
          │  Tests   │  Full user journeys through admin console
         ┌┴──────────┴┐
         │ Integration │  ~80 tests (testcontainers-go)
         │   Tests     │  Real DB + Redis + mock MCP servers
        ┌┴────────────┴┐
        │    Unit       │  ~300+ tests (Go testing + Vitest)
        │    Tests      │  Pure logic, no I/O
        └──────────────┘
```

### 13.2 Unit Tests (Go)

Every package has a `_test.go` file. Key areas:

- **Config parsing:** Valid/invalid YAML, env var overrides, defaults.
- **JWT creation and validation:** Token generation, expiry, claim extraction, invalid signatures.
- **RBAC policy evaluation:** Casbin model tests covering allow, deny, role inheritance, wildcard matching, tool-level granularity.
- **Log entry builder:** Correct field extraction from various MCP message types.
- **Hash chain:** Chain integrity verification, tamper detection.
- **Circuit breaker:** State transitions (closed → open → half-open → closed), threshold enforcement.
- **Rate limiter:** Window sliding, limit enforcement, reset behavior.
- **Session management:** Creation, retrieval, expiry, invalidation.

### 13.3 Integration Tests (Go + testcontainers)

Use testcontainers-go to spin up real PostgreSQL and Redis instances per test suite.

- **Proxy passthrough:** Mock MCP server (net/http/httptest) responds to `tools/list` and `tools/call`. Verify proxy forwards correctly for each transport.
- **Auth flow:** Mock SAML IdP issues assertions. Verify user is created, session is established, JWT is valid.
- **RBAC enforcement:** Seed policies in PostgreSQL. Verify allowed requests pass and denied requests return MCP error.
- **Log pipeline:** Proxy 1000 requests. Verify all 1000 appear in the audit log with correct metadata and hash chain.
- **Metrics:** Proxy requests with known latency (mock server delays). Verify Prometheus metrics report correct values.
- **Rate limiting:** Exceed configured rate limit. Verify rejection and Retry-After header.
- **Circuit breaker:** Mock server returns errors. Verify circuit opens after threshold. Verify recovery.
- **Database migrations:** Run all up migrations, verify schema. Run all down migrations, verify clean state.

### 13.4 Frontend Tests (React)

**Vitest + React Testing Library:**

- Component rendering with mock data.
- Form validation (role creation, server config, SAML setup).
- API client hooks with MSW (Mock Service Worker) for API mocking.
- State management (Zustand store tests).

**Playwright E2E:**

- Full login flow (SAML mock → dashboard).
- Add MCP server → test connectivity → view tools.
- Create role → define policy → assign to user.
- Search logs → filter by user → export CSV.
- SAML IdP configuration wizard.
- Metrics page date range selection and drill-down.

### 13.5 Security Tests

- SQL injection attempts on all API endpoints.
- XSS attempts on admin console (CSP enforcement verification).
- CSRF token validation.
- JWT tampering (modified claims, expired tokens, invalid signatures).
- SAML assertion replay detection.
- Rate limit bypass attempts.
- Privilege escalation (viewer role attempting admin API calls).

### 13.6 Performance Tests (k6)

```javascript
// k6 script outline
export const options = {
  scenarios: {
    proxy_throughput: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      stages: [
        { duration: '2m', target: 500 },
        { duration: '5m', target: 500 },
        { duration: '2m', target: 1000 },
        { duration: '5m', target: 1000 },
      ],
    },
  },
  thresholds: {
    'http_req_duration{type:proxy}': ['p95<50', 'p99<100'],
    'http_req_duration{type:admin}': ['p95<200'],
    'http_req_failed': ['rate<0.01'],
  },
};
```

Targets: 1000 RPS proxy throughput with <50ms p95 latency on a 4-core/8GB instance.

### 13.7 CI Pipeline

```yaml
# .github/workflows/ci.yml (outline)
jobs:
  lint:
    - golangci-lint run
    - cd web && npm run lint

  test-unit:
    - go test -race -count=1 ./internal/...
    - cd web && npm run test:unit

  test-integration:
    services: [postgres, redis]
    - go test -race -tags=integration ./internal/...

  test-e2e:
    - docker-compose up -d
    - cd web && npx playwright test

  security:
    - govulncheck ./...
    - cd web && npm audit --audit-level=high
    - trivy image mcpgate:latest

  build:
    - docker build -t mcpgate:$SHA .
    - cosign sign mcpgate:$SHA
```

---

## 14. Docker Packaging

### 14.1 Dockerfile (Multi-Stage)

```dockerfile
# Stage 1: Build Go binary
FROM golang:1.23-alpine AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o mcpgate ./cmd/mcpgate

# Stage 2: Build React frontend
FROM node:22-alpine AS web-builder
WORKDIR /build
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# Stage 3: Production image
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=go-builder /build/mcpgate /mcpgate
COPY --from=web-builder /build/dist /web
COPY configs/mcpgate.yaml /etc/mcpgate/mcpgate.yaml
EXPOSE 8080 9090
ENTRYPOINT ["/mcpgate"]
CMD ["--config", "/etc/mcpgate/mcpgate.yaml"]
```

### 14.2 docker-compose.yml

```yaml
version: "3.9"
services:
  mcpgate:
    build: .
    ports:
      - "8080:8080"   # Proxy + Admin API
      - "9090:9090"   # Metrics
    environment:
      - MCPGATE_DB_HOST=postgres
      - MCPGATE_REDIS_HOST=redis
      - MCPGATE_LOG_LEVEL=info
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
    volumes:
      - ./configs:/etc/mcpgate

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: mcpgate
      POSTGRES_USER: mcpgate
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-changeme}
    ports:
      - "5432:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U mcpgate"]
      interval: 5s
      timeout: 5s
      retries: 5

  redis:
    image: redis:7-alpine
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 5s
      retries: 5

  prometheus:
    image: prom/prometheus:latest
    ports:
      - "9091:9090"
    volumes:
      - ./deployments/prometheus.yml:/etc/prometheus/prometheus.yml

  grafana:
    image: grafana/grafana:latest
    ports:
      - "3000:3000"
    environment:
      - GF_SECURITY_ADMIN_PASSWORD=${GRAFANA_PASSWORD:-admin}

volumes:
  pgdata:
```

---

## 15. Configuration Reference

```yaml
# mcpgate.yaml — annotated reference configuration

server:
  host: "0.0.0.0"
  port: 8080
  metrics_port: 9090
  tls:
    enabled: false
    cert_file: ""
    key_file: ""
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  max_header_bytes: 1048576  # 1MB

database:
  host: "localhost"
  port: 5432
  name: "mcpgate"
  user: "mcpgate"
  password: ""               # Use MCPGATE_DB_PASSWORD env var
  ssl_mode: "prefer"
  max_connections: 25
  max_idle_connections: 5
  connection_max_lifetime: 1h

redis:
  host: "localhost"
  port: 6379
  password: ""               # Use MCPGATE_REDIS_PASSWORD env var
  db: 0
  pool_size: 20

auth:
  session_ttl: 1h
  session_sliding: true
  jwt:
    issuer: "mcpgate"
    access_token_ttl: 15m
    refresh_token_ttl: 168h  # 7 days
    signing_key_file: ""     # RS256 private key PEM

saml:
  entity_id: "https://mcpgate.example.com/saml"
  acs_url: "https://mcpgate.example.com/auth/saml/acs"
  metadata_url: "https://mcpgate.example.com/auth/saml/metadata"
  certificate_file: ""
  private_key_file: ""
  sign_requests: true
  require_encrypted_assertions: false
  attribute_mapping:
    email: "email"
    display_name: "displayName"
    groups: "groups"

proxy:
  default_timeout: 30s
  max_request_size: 10485760  # 10MB
  connection_pool:
    max_per_server: 50
    idle_timeout: 90s
  circuit_breaker:
    failure_threshold: 5
    recovery_timeout: 30s
    half_open_max_requests: 3
  health_check:
    interval: 30s
    timeout: 5s

rbac:
  cache_ttl: 30s
  default_action: "deny"

logging:
  level: "info"
  format: "json"
  pipeline:
    buffer_size: 10000
    flush_interval: 100ms
    batch_size: 500
    writer_count: 4
  retention:
    hot_days: 30
    warm_days: 90

rate_limiting:
  enabled: true
  default:
    rpm: 600
    rpd: 50000
    concurrent_connections: 10

metrics:
  enabled: true
  otlp:
    enabled: false
    endpoint: "localhost:4317"
    insecure: true

admin:
  web_root: "/web"
  cors_origin: "https://mcpgate.example.com"

# Upstream MCP servers (can also be managed via admin console)
servers:
  - name: "filesystem"
    transport: "stdio"
    config:
      command: "npx"
      args: ["-y", "@modelcontextprotocol/server-filesystem", "/data"]
    enabled: true

  - name: "github"
    transport: "streamable_http"
    config:
      url: "https://github-mcp.internal.company.com/mcp"
    enabled: true
```

---

## 16. Error Handling Strategy

### 16.1 Error Categories

| Category | HTTP Status | MCP Error Code | Handling |
|----------|------------|----------------|----------|
| Authentication failure | 401 | — | Return HTTP 401. Redirect to SAML login for browser clients. Return JSON error for API clients. |
| Authorization failure | 403 / MCP error | -32600 | Return MCP JSON-RPC error response with "Unauthorized" message. Log the denial with full context. |
| Upstream server unavailable | 502 | -32603 | Return MCP error "Internal error: upstream server unavailable". Trigger circuit breaker. |
| Upstream server timeout | 504 | -32603 | Return MCP error "Internal error: upstream server timeout". Record latency as timeout. |
| Rate limit exceeded | 429 | -32000 | Return MCP error with Retry-After hint. Include quota info in response. |
| Invalid MCP message | 400 | -32600 | Return MCP error "Invalid Request". Log the malformed input. |
| Internal proxy error | 500 | -32603 | Return MCP error "Internal error". Log full stack trace. Alert via webhook if configured. |

### 16.2 Error Propagation Principles

- **Never leak internal details.** Error messages to clients describe what happened, not why (no stack traces, no database errors, no internal IPs).
- **Always log the full context.** Internal logs include the complete error chain, request/response bodies (configurable), and system state.
- **Upstream errors pass through.** If the upstream MCP server returns an error, proxy it as-is to the client (it's the server's responsibility to format its errors). Log the error with proxy metadata attached.
- **Graceful degradation.** If Redis is down, session validation falls back to PostgreSQL (slower but functional). If PostgreSQL is down for logging, the WAL fallback captures entries. If metrics collection fails, proxy operation continues.

### 16.3 Panic Recovery

All HTTP handlers are wrapped in a recovery middleware that catches panics, logs the stack trace, and returns a 500 response. Panics in background goroutines (log pipeline, health checker) are also recovered with logging and automatic restart of the goroutine.

---

## 17. Open Questions for Clarification

1. **Deployment target.** Will the primary deployment be on-premises, a specific cloud provider, or cloud-agnostic? This affects the secret management integration (Vault vs. AWS Secrets Manager vs. GCP Secret Manager) and the Kubernetes manifest structure.

2. **Scale expectations.** What is the expected number of concurrent users, MCP servers, and requests per second? This informs connection pool sizing, database partitioning strategy, and whether horizontal proxy scaling (multiple mcpgate instances behind a load balancer) is needed in v1.

3. **MCP protocol version pinning.** Should mcpgate support multiple MCP protocol versions simultaneously (version negotiation), or target a single version? The spec is evolving quickly.

4. **Content inspection.** Is there a future need for content filtering, PII redaction, or DLP (Data Loss Prevention) on MCP messages? If so, the message handling pipeline should be designed with an extensible middleware chain from the start.

5. **Existing IdP.** Which SAML IdP(s) will be used (Okta, Azure AD, Google Workspace, Ping Identity)? Each has IdP-specific quirks in SAML assertion format that benefit from targeted testing.

6. **Audit compliance framework.** Are there specific compliance requirements (SOC 2, HIPAA, FedRAMP, GDPR) that drive log retention, encryption, or access-control decisions?

7. **Admin console SSO.** Should admin console authentication use the same SAML flow as MCP client authentication, or should it support a separate auth method (e.g., local username/password for bootstrap)?

8. **High availability.** Is active-active HA required for v1, or is a single-instance deployment with automated restart (Kubernetes) sufficient? Active-active would require leader election for background jobs (log retention, health checks).

9. **Licensing and distribution.** Will mcpgate be open-source, source-available, or proprietary? This affects dependency selection (some Go libraries have copyleft licenses).

---

## 18. Success Criteria

| Criterion | Target |
|-----------|--------|
| Proxy overhead (p99 latency added) | < 5ms for passthrough, < 15ms with auth + RBAC + logging |
| Proxy throughput (single instance, 4 cores) | > 1,000 RPS sustained |
| Log completeness | 100% of proxied messages logged (zero silent drops) |
| Auth flow completion time | < 3s for SAML SSO round-trip |
| RBAC evaluation time | < 1ms cached, < 10ms uncached |
| Admin console page load | < 2s initial load, < 500ms subsequent navigation |
| Unit test coverage | > 80% line coverage for Go, > 70% for React |
| Zero critical/high CVEs | At release and within 30 days of disclosure |
| Documentation completeness | All features documented with examples |

---

## 19. Risk Register

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|-----------|
| MCP spec changes break proxy compatibility | High | High | Minimal message parsing (only extract method/id/params.name). Version-aware transport adapters. Rapid release cycle for spec updates. |
| SAML integration complexity with diverse IdPs | Medium | Medium | Test against 3+ IdPs (Okta, Azure AD, Google). SAML test harness in CI. Fallback to manual metadata entry. |
| Log pipeline becomes bottleneck under load | Medium | High | Async pipeline with backpressure. WAL fallback. Configurable log levels to reduce volume. Batch inserts. |
| Scope creep delays v1 | Medium | High | Strict phase gates. Phases 0–5 are the MVP. Phases 6–9 are enhancements. Ship after Phase 5 with CLI-only admin if needed. |
| Security vulnerability in auth layer | Low | Critical | Third-party library for SAML/OAuth (crewjam/saml, ory/fosite). Security-focused code review. Automated scanning. |

---

*This document is a living artifact. It should be reviewed and updated at each phase gate.*
