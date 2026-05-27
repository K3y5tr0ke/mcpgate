# mcpgate — Comprehensive Development Plan

**Version:** 1.0  
**Date:** May 22, 2026  
**Companion to:** MCP Proxy Gateway PRD v1.0

---

## 1. Development Philosophy

This plan follows four governing principles. First, **vertical slices over horizontal layers** — each sprint delivers a working capability end-to-end (handler → middleware → storage → test) rather than building all handlers first and all storage second. Second, **test-driven boundaries** — every module boundary is defined by an interface, and tests against that interface are written before or alongside the implementation. Third, **continuous integration from day one** — the CI pipeline runs on the first commit and every merge thereafter, so broken builds are never more than one commit away from diagnosis. Fourth, **ship the MVP, then enhance** — Phases 0 through 5 produce a fully functional proxy with auth, RBAC, logging, and metrics that can be operated via API and CLI. The admin console (Phases 6–7) and advanced features (Phases 8–9) layer on top without modifying the core.

---

## 2. Team Structure and Roles

### Recommended Team Composition

| Role | Count | Responsibilities |
|------|-------|-----------------|
| Tech Lead / Architect | 1 | Architecture decisions, code review, cross-cutting concerns (config, middleware chain, error handling). Writes core proxy engine. |
| Backend Engineer (Auth) | 1 | SAML SP, Auth0 IdP integration, JWT middleware, session management. Security-focused. |
| Backend Engineer (Proxy + Infra) | 1 | Transport adapters, connection pooling, circuit breaker, health checks, Docker, CI/CD, database migrations. |
| Backend Engineer (Observability) | 1 | Logging pipeline, metrics collector, OpenTelemetry integration, audit log integrity. |
| Frontend Engineer | 1 | React admin console, all pages, API client layer, E2E tests. Joins at Phase 6. |
| QA / Security Engineer | 0.5 | Security test suite, penetration testing, OWASP checklist, performance testing with k6. Can be a part-time or shared role. |

For a smaller team (2–3 engineers), the Tech Lead absorbs the Proxy + Infra role, one backend engineer covers Auth + Observability, and the frontend engineer joins part-time during Phases 6–7.

### Working Agreements

- **Branching model:** Trunk-based development with short-lived feature branches (< 3 days). All merges require passing CI and one approval.
- **Code review:** Every PR reviewed by at least one person other than the author. Security-sensitive PRs (auth, RBAC, crypto) require Tech Lead review.
- **Definition of done:** Feature works end-to-end, unit tests pass, integration test exists for the happy path and at least one error path, documentation stub exists, no linting warnings.
- **Sprint cadence:** 2-week sprints aligned to the phase structure. Each phase spans 1–2 sprints. Sprint demo to stakeholders at the end of each sprint.

---

## 3. Development Environment Setup

### Prerequisites

| Tool | Version | Purpose |
|------|---------|---------|
| Go | 1.23+ | Backend development |
| Node.js | 22 LTS | Frontend development and tooling |
| Docker | 24+ | Container builds and local services |
| docker-compose | 2.20+ | Local development stack |
| PostgreSQL | 16 | Primary datastore (via Docker) |
| Redis | 7 | Cache and rate limiting (via Docker) |
| golangci-lint | latest | Go linting |
| Make | any | Task runner |

### First-Day Setup Script

```bash
#!/bin/bash
# scripts/setup.sh — Run once after cloning

set -euo pipefail

echo "=== mcpgate Development Setup ==="

# 1. Verify prerequisites
command -v go >/dev/null || { echo "Go 1.23+ required"; exit 1; }
command -v node >/dev/null || { echo "Node.js 22+ required"; exit 1; }
command -v docker >/dev/null || { echo "Docker 24+ required"; exit 1; }

# 2. Install Go tools
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install github.com/golang-migrate/migrate/v4/cmd/migrate@latest

# 3. Start infrastructure
docker-compose -f deployments/docker/docker-compose.dev.yml up -d postgres redis

# 4. Wait for healthy services
echo "Waiting for PostgreSQL..."
until docker-compose -f deployments/docker/docker-compose.dev.yml exec -T postgres pg_isready -U mcpgate; do sleep 1; done

# 5. Run database migrations
migrate -path internal/store/postgres/migrations -database "postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable" up

# 6. Generate development TLS and SAML certificates
bash scripts/generate-certs.sh

# 7. Seed sample data (admin user, sample server, default roles)
go run ./cmd/mcpgate seed --config configs/mcpgate.dev.yaml

# 8. Install frontend dependencies
cd web && npm ci && cd ..

# 9. Verify build
make build
make test-unit

echo "=== Setup complete. Run 'make dev' to start. ==="
```

### Makefile Targets

```makefile
.PHONY: build test dev lint

# Build
build:
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mcpgate ./cmd/mcpgate

build-docker:
	docker build -t mcpgate:dev -f deployments/docker/Dockerfile .

# Development
dev:
	go run ./cmd/mcpgate --config configs/mcpgate.dev.yaml

dev-frontend:
	cd web && npm run dev

dev-all:
	docker-compose -f deployments/docker/docker-compose.dev.yml up -d postgres redis
	$(MAKE) dev &
	$(MAKE) dev-frontend

# Testing
test-unit:
	go test -race -count=1 -short ./internal/...

test-integration:
	go test -race -count=1 -tags=integration ./internal/...

test-frontend:
	cd web && npm run test

test-e2e:
	cd web && npx playwright test

test-all: test-unit test-integration test-frontend test-e2e

test-coverage:
	go test -race -coverprofile=coverage.out -covermode=atomic ./internal/...
	go tool cover -html=coverage.out -o coverage.html

# Quality
lint:
	golangci-lint run ./...
	cd web && npm run lint

lint-fix:
	golangci-lint run --fix ./...
	cd web && npm run lint:fix

security-scan:
	govulncheck ./...
	cd web && npm audit --audit-level=high

# Database
migrate-up:
	migrate -path internal/store/postgres/migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path internal/store/postgres/migrations -database "$(DATABASE_URL)" down 1

migrate-create:
	migrate create -ext sql -dir internal/store/postgres/migrations -seq $(name)

# Docker
docker-up:
	docker-compose -f deployments/docker/docker-compose.yml up --build

docker-down:
	docker-compose -f deployments/docker/docker-compose.yml down -v

# Seed
seed:
	go run ./cmd/mcpgate seed --config configs/mcpgate.dev.yaml
```

---

## 4. Sprint-by-Sprint Development Plan

### Sprint 0: Project Bootstrap (Week 1)

**Goal:** Repository structure, CI pipeline, and development environment are fully operational. Every subsequent sprint starts from a green build.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 0.1 | Initialize Go module, directory structure per PRD Section 11 | Tech Lead | 4h | `go build ./...` succeeds. All packages compile. |
| 0.2 | Makefile with build, test, lint, dev targets | Tech Lead | 2h | `make build`, `make test-unit`, `make lint` all succeed. |
| 0.3 | golangci-lint configuration (.golangci.yml) | Tech Lead | 1h | Linter runs with strict settings: govet, errcheck, staticcheck, gosec, revive, gocritic, exhaustive. |
| 0.4 | CI pipeline: lint → test-unit → build | Proxy Eng | 4h | Pipeline runs on every push and PR. Badge in README. |
| 0.5 | docker-compose.dev.yml with PostgreSQL 16 + Redis 7 | Proxy Eng | 2h | `docker-compose up` starts both services. Health checks pass. |
| 0.6 | Configuration system (internal/config) | Tech Lead | 4h | Loads from YAML file, env vars (MCPGATE_ prefix), and CLI flags. Validates required fields. Unit tests for merge order and validation. |
| 0.7 | Database connection pool (internal/store/postgres) | Proxy Eng | 3h | pgx pool connects to PostgreSQL. Connection test in CI (testcontainers). |
| 0.8 | Redis connection (internal/store/redis) | Proxy Eng | 2h | go-redis client connects. Ping test in CI (testcontainers). |
| 0.9 | Initial database migration (001_initial.up.sql) | Proxy Eng | 4h | All tables from PRD Section 9 created. Migration runs idempotently. Down migration drops all tables. |
| 0.10 | HTTP server skeleton with chi router (internal/server) | Tech Lead | 3h | Starts on configured port. Responds to GET /health with 200. Graceful shutdown on SIGTERM/SIGINT. |
| 0.11 | Structured logging setup (log/slog with JSON handler) | Observability Eng | 2h | All log output is structured JSON. Log level configurable. Request ID in context. |
| 0.12 | Setup script (scripts/setup.sh) | Proxy Eng | 2h | New developer can clone and run setup.sh → make dev within 10 minutes. |
| 0.13 | README with setup instructions, architecture overview, contributing guide | Tech Lead | 2h | README covers: what mcpgate is, how to set up dev, how to run tests, how to contribute. |

**Sprint 0 deliverable:** A green CI build with an empty but runnable server, database connectivity, and a one-command dev setup.

---

### Sprint 1: Core Proxy Engine (Weeks 2–3)

**Goal:** MCP messages are proxied end-to-end over Streamable HTTP. No auth, no RBAC, no logging — just transparent passthrough. This validates the fundamental proxy architecture.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 1.1 | MCP server registry (internal/store/postgres/servers.go) | Proxy Eng | 4h | CRUD operations for mcp_servers table. Repository interface with PostgreSQL implementation. Unit tests with testcontainers. |
| 1.2 | Server registry API endpoints (GET/POST/PUT/DELETE /api/v1/admin/servers) | Tech Lead | 4h | RESTful handlers for server CRUD. Input validation. Error responses. Integration test. |
| 1.3 | Streamable HTTP transport adapter (internal/proxy/transport/streamable.go) | Tech Lead | 8h | Proxies HTTP POST request body to upstream server URL. Returns upstream response body. Handles bidirectional SSE upgrade for streaming. Timeout handling. |
| 1.4 | Core proxy handler (internal/proxy/handler.go) | Tech Lead | 6h | Routes /mcp/{server_name}/message to the correct upstream server via transport adapter. Extracts JSON-RPC method and id from request body. Returns 404 for unknown servers, 502 for upstream failures. |
| 1.5 | JSON-RPC message parser (internal/proxy/jsonrpc.go) | Tech Lead | 3h | Parses JSON-RPC 2.0 messages to extract method, id, and params.name (for tools/call). Does not validate full MCP schema. Unit tests for all MCP method types. |
| 1.6 | Request/response correlation | Tech Lead | 2h | Match response to request via JSON-RPC id. Compute latency. Track in-flight requests. |
| 1.7 | Mock MCP server for testing (internal/testutil/mockserver.go) | Proxy Eng | 4h | HTTP server that implements tools/list and tools/call with configurable responses, latency, and error rates. Used in all integration tests. |
| 1.8 | SSE transport adapter (internal/proxy/transport/sse.go) | Proxy Eng | 6h | Relay SSE events (GET endpoint) from upstream. Proxy client messages (POST endpoint) to upstream. Handle connection lifecycle. |
| 1.9 | stdio bridge (internal/proxy/transport/stdio.go) | Proxy Eng | 8h | Spawn upstream MCP server as child process. Bridge stdin/stdout to HTTP request/response. Process lifecycle management (start, health, restart, kill). Configurable concurrency (process pool). |
| 1.10 | Connection pooling (internal/proxy/transport/pool.go) | Proxy Eng | 4h | HTTP connection pool per upstream server. Configurable max connections and idle timeout. Pool metrics (active, idle, waiting). |
| 1.11 | Integration test: full proxy round-trip for each transport | Both | 4h | Test sends tools/list and tools/call through the proxy to the mock server. Verifies correct response for Streamable HTTP, SSE, and stdio. |

**Sprint 1 deliverable:** `curl -X POST localhost:8080/mcp/test-server/message -d '{"jsonrpc":"2.0","method":"tools/list","id":1}'` returns the upstream server's tool list.

---

### Sprint 2: Authentication — SAML (Week 4–5)

**Goal:** Users authenticate via SAML 2.0. Unauthenticated proxy requests are rejected.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 2.1 | SAML Service Provider setup (internal/auth/saml/sp.go) | Auth Eng | 6h | Initialize crewjam/saml SP with configurable entity ID, ACS URL, certificate, and private key. SP metadata XML generation. |
| 2.2 | SAML HTTP handlers (internal/auth/saml/handlers.go) | Auth Eng | 6h | GET /auth/saml/login (SP-initiated SSO redirect). POST /auth/saml/acs (assertion consumption). GET /auth/saml/metadata (SP metadata). GET /auth/saml/logout (SLO initiation). |
| 2.3 | SAML assertion parsing and validation | Auth Eng | 4h | Validate assertion signature, audience, time conditions. Extract NameID and configured attributes. Reject replayed assertion IDs (store in Redis with TTL). |
| 2.4 | User provisioning (JIT) | Auth Eng | 3h | On successful SAML auth, create user record if not exists (by email or NameID). Update display_name and metadata from assertion attributes. |
| 2.5 | Attribute mapping engine (internal/auth/saml/attribute_map.go) | Auth Eng | 3h | Configurable mapping from SAML attribute URNs to internal fields. Support for multi-valued attributes (groups). Unit tests with sample assertions. |
| 2.6 | JWT session tokens (internal/auth/oauth/jwt.go) | Auth Eng | 4h | Issue RS256-signed JWTs after SAML auth. Claims: sub (user ID), email, roles, session_id, exp, iat, iss. Validation: signature, expiry, issuer. Unit tests for creation, validation, and rejection of tampered/expired tokens. |
| 2.7 | Session management (internal/proxy/session.go) | Auth Eng | 4h | Create session in PostgreSQL + cache in Redis after SAML auth. Session ID is UUID v7. Configurable TTL with sliding expiry. Session bound to client IP (configurable strictness). Invalidation on logout. |
| 2.8 | Auth middleware (internal/auth/middleware.go) | Auth Eng | 3h | Middleware extracts JWT from Authorization header (Bearer) or session cookie. Validates JWT. Injects user identity into request context. Returns 401 for missing/invalid/expired tokens. |
| 2.9 | Apply auth middleware to all proxy routes | Tech Lead | 1h | All /mcp/* routes require valid JWT. /health, /ready, /auth/* are exempt. |
| 2.10 | SAML IdP configuration storage (internal/store/postgres/saml.go) | Auth Eng | 3h | CRUD for saml_idp_configs table. Support multiple IdP configurations. |
| 2.11 | SAML test harness | Auth Eng | 4h | Local test IdP using samltest.id or a containerized SimpleSAMLphp. Automated test: initiate login → IdP authenticates → ACS receives assertion → JWT issued → proxy request succeeds. |
| 2.12 | Integration test: unauthenticated request rejected | Auth Eng | 2h | Request to /mcp/*/message without JWT returns 401. Request with valid JWT returns proxied response. Request with expired JWT returns 401. |

**Sprint 2 deliverable:** A user visits /auth/saml/login, authenticates with the test IdP, receives a JWT, and uses it to make authenticated MCP proxy requests.

---

### Sprint 3: Authentication — M2M JWKS Validation + Session Polish (Week 5–6)

**Goal:** Programmatic clients authenticate via externally-issued M2M JWTs. The provider is configurable at runtime — no code changes to add a new issuer. Session management is hardened.

mcpgate's role is to validate M2M JWTs via a database-driven JWKS config store (parallel to `saml_idp_configs`). Auth0 is the first configured provider; Okta, Azure AD, or an internal Vault PKI can be added via the admin API.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 3.1 | `jwks_configs` store (internal/store/postgres/jwks.go) | Auth Eng | 3h | `JWKSConfigStore` postgres implementation. CRUD + `GetByIssuer` + `ListEnabled`. `issuer` column has unique constraint. |
| 3.2 | JWKS client (internal/auth/jwks/client.go) | Auth Eng | 3h | Per-config key fetching with TTL cache (1h). Automatic refresh on `kid` miss. Thread-safe. |
| 3.3 | M2M token validator (internal/auth/jwks/validator.go) | Auth Eng | 3h | On each request: read `iss` claim → `GetByIssuer` → fetch/cache JWKS → verify RS256 signature, `aud`, `exp`. Return `UserIdentity` with roles from `permissions` claim. |
| 3.4 | Unified auth middleware (update internal/auth/middleware.go) | Auth Eng | 2h | Detects mcpgate-issued JWT (by `iss` config value) vs external M2M JWT and routes to the correct validator. Both paths produce a `UserIdentity` in request context. |
| 3.5 | JWKS config admin API (internal/admin/api/jwks.go) | Auth Eng | 3h | GET/POST/PUT/DELETE `/api/v1/admin/jwks/configs`. Seed Auth0 config on first install via `make seed-jwks`. |
| 3.6 | Brute-force detection on auth endpoints | Auth Eng | 3h | Rate limit failed auth attempts per IP (Redis counter). Lock out after N failures for M minutes. Log security event. |
| 3.7 | Session fixation prevention | Auth Eng | 1h | Generate new session ID after SAML authentication completes. Old session (if any) invalidated. |
| 3.8 | Integration test: M2M token validation | Auth Eng | 3h | Seed a JWKS config row with a test RSA key. Sign a JWT offline. Call protected route → 200. Tampered JWT → 401. Expired → 401. Unknown issuer → 401. |

**Sprint 3 deliverable:** Both human users (via SAML) and service accounts (via any configured JWKS issuer) can authenticate. Adding a new M2M provider requires only an admin API call.

---

### Sprint 4: RBAC (Weeks 7–8)

**Goal:** Fine-grained access control enforced on every proxy request.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 4.1 | Casbin model and enforcer setup (internal/rbac/enforcer.go) | Tech Lead | 4h | Casbin enforcer initialized with model from PRD Section 6.3.2. PostgreSQL adapter for policy storage. Enforcer reload on policy change. |
| 4.2 | Default roles and policies (seed data) | Tech Lead | 2h | Five default roles (super_admin, admin, operator, developer, viewer) with appropriate policies seeded on first startup. |
| 4.3 | Role management storage (internal/store/postgres/roles.go) | Auth Eng | 3h | CRUD for roles and user_roles tables. Role assignment/revocation. Prevent deletion of system roles. Prevent deletion of roles with assigned users. |
| 4.4 | Role management admin API | Auth Eng | 3h | GET/POST/PUT/DELETE /api/v1/admin/roles. PUT /api/v1/admin/users/{id}/roles for assignment. |
| 4.5 | Policy management admin API | Tech Lead | 3h | GET/POST/PUT/DELETE /api/v1/admin/policies. POST /api/v1/admin/policies/evaluate for dry-run testing. |
| 4.6 | RBAC middleware (internal/rbac/middleware.go) | Tech Lead | 6h | Extract user roles from JWT context. Extract target server and tool from MCP message (method + params.name). Call Casbin enforcer. Allow → continue. Deny → return MCP JSON-RPC error (-32600). Log decision. |
| 4.7 | Redis policy cache (internal/store/redis/policy_cache.go) | Proxy Eng | 3h | Cache Casbin decisions (key: user+server+tool+action, value: allow/deny, TTL: 30s). Invalidate on policy change (publish/subscribe). |
| 4.8 | SAML group-to-role mapping | Auth Eng | 3h | On SAML login, map IdP group attributes to mcpgate roles using configurable mapping (saml_idp_configs.group_role_map). Auto-assign roles. |
| 4.9 | Unit tests: RBAC policy scenarios | Tech Lead | 4h | Test matrix: admin can access all tools, developer can access assigned servers only, viewer can list but not call, explicit deny overrides allow, role inheritance works, wildcard server/tool matching. Minimum 20 test cases. |
| 4.10 | Integration test: RBAC enforcement end-to-end | Both | 3h | Authenticated user with "developer" role calls allowed tool → success. Same user calls denied tool → MCP error -32600. Policy change takes effect within cache TTL. |

**Sprint 4 deliverable:** `developer` role users can invoke tools on their assigned servers but are blocked from others. `viewer` role users can call `tools/list` but not `tools/call`.

---

### Sprint 5: Logging and Audit Trail (Weeks 9–10)

**Goal:** Every proxied message is logged with full metadata and tamper-evident hash chain.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 5.1 | Log entry builder (internal/logging/entry.go) | Observability Eng | 4h | Construct AuditLogEntry struct from proxy context: timestamp, trace_id, session_id, user, server, method, tool, direction, status, latency, request/response sizes, RBAC decision, metadata. Unit tests for each MCP method type. |
| 5.2 | Async log pipeline (internal/logging/pipeline.go) | Observability Eng | 6h | Buffered channel (configurable size, default 10,000). Writer goroutine pool (configurable count, default 4). Entries are dequeued in batches and written to the sink. Pipeline start/stop/drain lifecycle. |
| 5.3 | PostgreSQL log sink (internal/logging/sinks/postgres.go) | Observability Eng | 4h | Batch insert using pgx COPY protocol. Configurable flush interval (default 100ms) and batch size (default 500). Error handling: retry once, then fall through to WAL. |
| 5.4 | WAL fallback sink (internal/logging/sinks/file.go) | Observability Eng | 3h | When PostgreSQL sink fails, write entries as newline-delimited JSON to a local WAL file. Background goroutine replays WAL entries to PostgreSQL when available. WAL rotation by size. |
| 5.5 | Hash chain (internal/logging/hashchain.go) | Observability Eng | 3h | Each log entry's hash_chain field = SHA-256(previous_hash + entry_content_hash). Chain verification function that detects gaps or modifications. First entry in chain uses a configurable seed. Unit tests for chain integrity and tamper detection. |
| 5.6 | Log pipeline middleware (instrument proxy handler) | Observability Eng | 2h | After proxy handler completes, build log entry and push to pipeline channel. Non-blocking: if channel is full, increment a "dropped" counter (should never happen with WAL fallback). |
| 5.7 | Log query API (GET /api/v1/admin/logs) | Observability Eng | 4h | Paginated query with filters: time range, user, server, tool, method, status. Full-text search on error_message. Sort by timestamp (desc default). Response includes total count for pagination. |
| 5.8 | Log export API (GET /api/v1/admin/logs/export) | Observability Eng | 2h | Export filtered logs as CSV or JSON (format query param). Streaming response for large exports. |
| 5.9 | WebSocket log stream (WS /api/v1/admin/logs/stream) | Observability Eng | 3h | Real-time log tail via WebSocket. Client can send filter messages to narrow the stream. Backpressure: drop oldest if client is slow. |
| 5.10 | Log retention background job | Observability Eng | 3h | Cron-style goroutine that runs daily. Deletes logs older than hot_days from PostgreSQL. Compresses and archives logs between hot_days and warm_days. |
| 5.11 | Integration test: log completeness | Observability Eng | 3h | Proxy 1,000 requests through mcpgate. Verify exactly 1,000 log entries exist in PostgreSQL with correct metadata. Verify hash chain is intact. |
| 5.12 | Integration test: pipeline backpressure | Observability Eng | 2h | Artificially slow the PostgreSQL sink. Send burst of requests. Verify WAL file is created and replayed after PostgreSQL recovers. Zero entries lost. |

**Sprint 5 deliverable:** Every proxy request produces a tamper-evident audit log entry. Logs are queryable, exportable, and streamable in real-time.

---

### Sprint 6: Metrics and Observability (Weeks 11–12)

**Goal:** Prometheus metrics, distributed tracing, circuit breakers, and rate limiting.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 6.1 | Prometheus metrics registration (internal/metrics/collector.go) | Observability Eng | 4h | Register all metrics from PRD Section 6.5.1. Counter, Histogram, and Gauge types. Labeled by server, method, tool, user, status. |
| 6.2 | Instrument proxy handler with metrics | Observability Eng | 3h | Increment request counter, observe latency histogram, update active connections gauge — all in the proxy middleware chain. |
| 6.3 | Instrument auth and RBAC middleware | Observability Eng | 2h | auth_attempts_total, rbac_denials_total counters. Labeled by method and status. |
| 6.4 | Metrics HTTP endpoint (GET /metrics on metrics_port) | Observability Eng | 1h | Prometheus exposition format. Separate port (default 9090) from main API. Optionally IP-restricted. |
| 6.5 | OpenTelemetry tracing setup | Observability Eng | 4h | OTLP exporter configuration. Trace context propagation through proxy middleware chain. Spans: proxy.auth, proxy.rbac, proxy.upstream, proxy.log. |
| 6.6 | Metrics query API (GET /api/v1/admin/metrics/*) | Observability Eng | 4h | Summary endpoint: current RPM, error rate, p95 latency, active sessions. Timeseries endpoint: bucketed data for chart rendering. Per-user and per-server breakdowns. |
| 6.7 | Circuit breaker (internal/proxy/circuit/breaker.go) | Proxy Eng | 4h | Per-upstream-server circuit breaker. States: closed (normal) → open (failing, fast-reject) → half-open (probing). Configurable failure_threshold, recovery_timeout, half_open_max_requests. Unit tests for all state transitions. |
| 6.8 | Integrate circuit breaker with proxy handler | Proxy Eng | 2h | Before forwarding to upstream, check circuit state. If open, return MCP error immediately. Record success/failure from upstream response. |
| 6.9 | Upstream health checker (internal/proxy/health/checker.go) | Proxy Eng | 3h | Background goroutine pings upstream servers at configurable intervals. Updates mcpgate_upstream_health gauge. Marks server as unhealthy after N consecutive failures. |
| 6.10 | Rate limiter (internal/store/redis/rate_limiter.go) | Proxy Eng | 4h | Redis-based sliding window rate limiter. Configurable limits per user and per role (RPM, RPD, concurrent connections). Returns remaining quota and Retry-After in response. |
| 6.11 | Rate limit middleware | Proxy Eng | 2h | Check rate limits before proxy handler. If exceeded, return MCP error -32000 with Retry-After. Log rate limit event. |
| 6.12 | Integration test: circuit breaker behavior | Proxy Eng | 3h | Mock server returns errors. Verify circuit opens after threshold. Requests get fast-reject. After recovery_timeout, half-open allows probe. Successful probe closes circuit. |
| 6.13 | Integration test: rate limiting | Proxy Eng | 2h | Configure RPM=10 for test user. Send 15 requests in 1 minute. Verify first 10 succeed and last 5 return -32000 with Retry-After. |
| 6.14 | Prometheus + Grafana in docker-compose | Proxy Eng | 2h | Add Prometheus and Grafana to docker-compose.yml. Preconfigured Prometheus scrape config for mcpgate. Sample Grafana dashboard JSON. |

**Sprint 6 deliverable:** `/metrics` serves Prometheus data. Circuit breakers protect against upstream failures. Rate limiting prevents abuse. This completes the backend MVP.

---

### Sprint 7–8: Admin Console — Core Pages (Weeks 13–16)

**Goal:** React admin console with dashboard, servers, users, roles, and logs pages.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 7.1 | React project scaffolding | Frontend Eng | 4h | Vite + React 18 + TypeScript 5 + Tailwind CSS + TanStack Router. ESLint + Prettier configured. Build produces static assets. |
| 7.2 | Design system: tokens, colors, typography, layout | Frontend Eng | 4h | Tailwind config with custom colors (brand, semantic status colors), spacing scale, font stack. Dark/light mode CSS variables. Shared component library foundation: Button, Input, Select, Card, Badge, Modal, Toast. |
| 7.3 | API client layer (web/src/api/) | Frontend Eng | 6h | Typed fetch wrapper with JWT auth header injection. TanStack Query hooks for every admin API endpoint. Error handling with typed error responses. Automatic token refresh. |
| 7.4 | Authentication flow | Frontend Eng | 4h | Login page redirects to /auth/saml/login. Callback handler stores JWT. Protected route wrapper redirects to login if no valid JWT. Logout clears session. |
| 7.5 | App shell: sidebar navigation, header, layout | Frontend Eng | 4h | Responsive sidebar with navigation links (Dashboard, Servers, Users, Roles, Logs, Metrics, SAML, OAuth, Settings). User avatar + logout in header. Content area with breadcrumbs. |
| 7.6 | Dashboard page | Frontend Eng | 6h | Real-time metrics: request volume (RPM sparkline), error rate, p95 latency, active users count. Server health status cards (green/red). Recent activity feed (last 20 log entries). Auto-refresh every 10 seconds via TanStack Query refetchInterval. |
| 7.7 | Servers page — list and detail | Frontend Eng | 6h | Server list table with columns: name, transport, status (health), tool count, last seen. Detail view: server config, list of discovered tools with descriptions and schemas. Test connectivity button (calls POST /api/v1/admin/servers/{id}/test). |
| 7.8 | Servers page — create/edit forms | Frontend Eng | 4h | React Hook Form + Zod validation. Transport-specific form fields (URL for HTTP, command+args for stdio). Create and edit use the same form component. |
| 7.9 | Users page | Frontend Eng | 4h | User list table with search/filter by email, role, status. Columns: email, display_name, roles (as badges), status, last active. Detail drawer: user info, role assignment multi-select, recent activity. |
| 7.10 | Roles & Policies page — role management | Frontend Eng | 4h | Role list with create/edit/delete. Role form: name, description. User count per role. System roles are read-only. |
| 7.11 | Roles & Policies page — policy editor | Frontend Eng | 8h | Visual policy editor: matrix of servers (rows) × tools (columns) with allow/deny/inherit toggles for each action type (list, call, read, write). Policy diff preview before saving (shows added/removed rules). This is the most complex UI component. |
| 7.12 | Logs page — table and filters | Frontend Eng | 6h | TanStack Table with columns: timestamp, user, server, tool, method, status, latency. Filter bar: date range picker, user dropdown, server dropdown, status radio, full-text search input. Pagination controls. Click row → detail drawer with full log entry JSON. |
| 7.13 | Logs page — real-time tail | Frontend Eng | 4h | WebSocket connection to /api/v1/admin/logs/stream. New entries appear at top of table. Auto-scroll with pause/resume button. Filter controls apply to stream. Connection status indicator. |
| 7.14 | Logs page — export | Frontend Eng | 2h | "Export" button downloads filtered logs as CSV or JSON. Uses the streaming export API for large result sets. |
| 7.15 | Vitest unit tests for all components | Frontend Eng | 6h | Every component has at least one render test. Form components test validation. API hooks test loading/error/success states with MSW. Target: 70% line coverage. |
| 7.16 | Playwright E2E: core user journeys | Frontend Eng | 4h | Test 1: Login → view dashboard → verify metrics display. Test 2: Add server → test connectivity → view tools. Test 3: Create role → define policy → assign to user. Test 4: Search logs → filter by date → export CSV. |

**Sprint 7–8 deliverable:** Administrators can manage the full mcpgate lifecycle through a polished web console.

---

### Sprint 9: Admin Console — Advanced Pages (Weeks 17–18)

**Goal:** Metrics dashboards, SAML/OAuth configuration, and system settings.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 9.1 | Metrics page — overview charts | Frontend Eng | 4h | Recharts line/bar charts: request volume over time, error rate over time, latency percentiles over time. Date range picker (1h, 6h, 24h, 7d, 30d, custom). |
| 9.2 | Metrics page — per-server drilldown | Frontend Eng | 3h | Click server → see server-specific charts with tool-level breakdown. Top tools by usage table. |
| 9.3 | Metrics page — per-user drilldown | Frontend Eng | 3h | Click user → see user-specific usage patterns. Most-used tools, error rates, request volume. |
| 9.4 | SAML config page — IdP setup wizard | Frontend Eng | 6h | Step 1: Upload IdP metadata XML or enter SSO URL + certificate manually. Step 2: Preview parsed IdP info (entity ID, SSO URL, cert validity). Step 3: Configure attribute mappings with dropdown selectors. Step 4: Configure group → role mapping. Step 5: Download SP metadata for import into IdP. "Test Login" button that opens SAML flow in popup and displays parsed assertion result. |
| 9.5 | OAuth config page | Frontend Eng | 3h | Client list table (client_id, name, grant_types, created_at). Create client form (name, grant types, redirect URIs, scopes). Client secret shown once on creation. Revoke button with confirmation. |
| 9.6 | Settings page | Frontend Eng | 3h | System settings form: session TTL, rate limit defaults, log retention days, proxy timeouts. Changes saved via PUT /api/v1/admin/settings. Visual indicator for settings that require restart. |
| 9.7 | Dark/light mode toggle | Frontend Eng | 2h | Toggle in header. Persists preference in localStorage. Tailwind dark: variant. All components and charts adapt. |
| 9.8 | Accessibility audit and fixes | Frontend Eng | 4h | axe-core automated scan on all pages. Keyboard navigation for all interactive elements. ARIA labels on icons and status indicators. Focus management in modals. Color contrast ratio ≥ 4.5:1. Screen reader testing with VoiceOver. |
| 9.9 | Playwright E2E: advanced pages | Frontend Eng | 3h | Test 1: SAML config wizard → upload metadata → test login. Test 2: Create OAuth client → copy client_id. Test 3: Metrics page → change date range → verify chart updates. |

**Sprint 9 deliverable:** Complete admin console with all pages functional, accessible, and dark-mode ready.

---

### Sprint 10: Hardening and Security (Weeks 19–20)

**Goal:** Production security hardening, performance validation, and chaos testing.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 10.1 | OWASP Top 10 checklist walkthrough | QA/Security | 4h | Document each OWASP category with specific mcpgate mitigation evidence. All items pass. |
| 10.2 | Security header configuration | Proxy Eng | 2h | Admin console responses include: Content-Security-Policy, X-Frame-Options: DENY, X-Content-Type-Options: nosniff, Strict-Transport-Security, Referrer-Policy. |
| 10.3 | Input sanitization audit | QA/Security | 4h | Review all API endpoints for injection vectors. Test with OWASP ZAP automated scanner. Fix any findings. |
| 10.4 | SQL injection test suite | QA/Security | 3h | Parameterized query verification for all database operations. Automated tests with injection payloads on all text input fields. |
| 10.5 | XSS prevention test suite | QA/Security | 2h | CSP blocks inline scripts. Test with XSS payloads in all admin console input fields. Verify no reflected or stored XSS. |
| 10.6 | JWT security tests | QA/Security | 3h | Test: tampered token rejected, expired token rejected, token with wrong issuer rejected, token with modified roles rejected, none algorithm rejected. |
| 10.7 | SAML security tests | QA/Security | 3h | Test: replayed assertion rejected, assertion with wrong audience rejected, expired assertion rejected, assertion with invalid signature rejected. |
| 10.8 | Privilege escalation tests | QA/Security | 3h | Test: viewer role cannot access admin API, developer role cannot modify policies, suspended user cannot authenticate, revoked OAuth client cannot get tokens. |
| 10.9 | k6 load test: proxy throughput | Proxy Eng | 4h | Ramp from 100 to 1000 RPS over 10 minutes. Verify p95 < 50ms, p99 < 100ms, error rate < 1%. Profile with pprof. Fix any bottlenecks. |
| 10.10 | k6 load test: log pipeline under stress | Observability Eng | 3h | 1000 RPS sustained for 10 minutes = 600,000 log entries. Verify all entries present in database. Verify hash chain intact. Measure pipeline lag (time from request to log entry commit). |
| 10.11 | Chaos test: PostgreSQL failure | Proxy Eng | 2h | Kill PostgreSQL during proxy operation. Verify: proxy continues serving (sessions from Redis), logs write to WAL, WAL replays after PostgreSQL recovers. |
| 10.12 | Chaos test: Redis failure | Proxy Eng | 2h | Kill Redis during proxy operation. Verify: auth falls back to PostgreSQL session lookup, RBAC falls back to uncached Casbin evaluation, rate limiting becomes permissive (fail-open). Proxy does not crash. |
| 10.13 | Chaos test: upstream server failure | Proxy Eng | 2h | Kill upstream MCP server. Verify: circuit breaker opens, subsequent requests get fast MCP error, health checker detects failure, admin console shows server as unhealthy, circuit closes after server recovers. |
| 10.14 | Dependency vulnerability scan | QA/Security | 2h | Run govulncheck and npm audit. Trivy scan on Docker image. Fix or document all critical/high findings. |
| 10.15 | Docker image hardening | Proxy Eng | 2h | Distroless base image. Non-root user (UID 65534). Read-only filesystem (except /tmp and WAL directory). No shell. Image size < 50MB. |

**Sprint 10 deliverable:** All security tests pass. Performance targets met. Graceful degradation verified under failure conditions.

---

### Sprint 11: Production Packaging and Documentation (Weeks 21–22)

**Goal:** Production-ready Docker image, Kubernetes manifests, Helm chart, and comprehensive documentation.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 11.1 | Multi-stage Dockerfile (production) | Proxy Eng | 3h | Go binary built with CGO_ENABLED=0. React assets built and embedded. Distroless final image. Signed with Cosign. SBOM generated with Syft. |
| 11.2 | docker-compose.yml (production) | Proxy Eng | 2h | All services with resource limits, restart policies, health checks, proper networking, volume mounts. Environment variable documentation. |
| 11.3 | Kubernetes base manifests (Kustomize) | Proxy Eng | 4h | Deployment, Service, ConfigMap, Secret, PersistentVolumeClaim. Resource requests/limits. Readiness/liveness probes. SecurityContext (non-root, read-only root filesystem, no privilege escalation). |
| 11.4 | Kubernetes production overlay | Proxy Eng | 3h | HPA (scale on CPU and custom metrics). PodDisruptionBudget. NetworkPolicy (restrict ingress/egress). Ingress with TLS. Separate namespace. |
| 11.5 | Helm chart | Proxy Eng | 6h | Parameterized chart with values.yaml covering all configuration options. Subchart dependencies for PostgreSQL and Redis (optional, for bundled deployment). Comprehensive NOTES.txt with post-install instructions. |
| 11.6 | Architecture documentation (docs/architecture.md) | Tech Lead | 4h | Component diagram, data flow diagrams (proxy path, auth flow, logging pipeline), technology choices with rationale, module dependency graph. |
| 11.7 | Deployment guide (docs/deployment.md) | Proxy Eng | 4h | Step-by-step for Docker, docker-compose, and Kubernetes. Environment variable reference. TLS setup. Database migration instructions. Backup and restore procedures. |
| 11.8 | Configuration reference (docs/configuration.md) | Tech Lead | 3h | Every configuration option with type, default, description, and example. Organized by section (server, database, redis, auth, saml, proxy, rbac, logging, metrics, admin). |
| 11.9 | SAML setup guide (docs/saml-setup.md) | Auth Eng | 3h | Step-by-step SAML integration for Okta, Azure AD, and Google Workspace. Screenshots of IdP configuration. Attribute mapping examples. Troubleshooting section. |
| 11.10 | API reference (docs/api-reference.md) | Tech Lead | 4h | Every endpoint documented: method, path, auth requirement, request body schema, response schema, error codes, example curl commands. Generated from OpenAPI spec if time permits. |
| 11.11 | Runbook (docs/runbook.md) | Proxy Eng | 3h | Operational runbook: common issues and resolution (SAML assertion errors, upstream connection failures, high latency diagnosis, log pipeline backup, certificate rotation). Alert response procedures. |
| 11.12 | CI pipeline: release workflow | Proxy Eng | 3h | Workflow triggered on version tag. Builds Docker image, runs full test suite, signs image, generates SBOM, creates release with changelog, pushes to container registry. |

**Sprint 11 deliverable:** A tagged v1.0 release with signed Docker image, Helm chart, and comprehensive documentation.

---

### Sprint 12–13: Advanced Features (Weeks 23–26)

**Goal:** Multi-tenancy, webhooks, quota management, and operational polish.

| # | Task | Owner | Estimate | Acceptance Criteria |
|---|------|-------|----------|-------------------|
| 12.1 | Multi-tenancy: namespace model | Tech Lead | 6h | Tenant table. All entities (servers, policies, logs, users) scoped by tenant_id. Middleware injects tenant context from JWT claim. Cross-tenant queries impossible. |
| 12.2 | Multi-tenancy: admin console tenant switching | Frontend Eng | 4h | Tenant selector in header for super_admin. All pages filter by selected tenant. Tenant-specific settings. |
| 12.3 | Webhook notification system (internal/webhook/) | Observability Eng | 6h | Configurable webhooks with event types, URL, secret (HMAC signature), retry policy. Events: server_health_changed, rbac_denial_spike, rate_limit_breach, auth_failure_spike, quota_threshold. Async delivery via goroutine pool. |
| 12.4 | Webhook admin UI | Frontend Eng | 3h | Webhook list, create form (URL, events, secret), test delivery button, delivery log. |
| 12.5 | Quota management (internal/quota/) | Observability Eng | 4h | Daily and monthly request quotas per user and per team. Quota counters in Redis. Configurable warning thresholds. Webhook notification on threshold crossing. |
| 12.6 | Quota admin UI | Frontend Eng | 3h | Quota configuration per user/role. Usage bar charts showing current consumption vs limit. |
| 12.7 | Config hot-reload | Tech Lead | 4h | Watch config file for changes. Reload RBAC policies, rate limits, and server registrations without restart. SAML and TLS changes require graceful restart (documented). |
| 12.8 | JSON-RPC request validation | Tech Lead | 3h | Optional middleware that validates incoming messages against JSON-RPC 2.0 spec. Configurable strictness (off, warn, reject). |
| 12.9 | Server tool auto-discovery | Proxy Eng | 3h | On server registration, call initialize and tools/list. Cache discovered tools in server_tools table. Periodic refresh (configurable interval). Admin console shows last discovery time. |
| 12.10 | Final integration test sweep | All | 4h | Full regression run. Fix any broken tests. Verify all features work together. |
| 12.11 | v1.0 release preparation | Tech Lead | 2h | Changelog, release notes, version bump, final CI run. |

**Sprint 12–13 deliverable:** mcpgate v1.0 — feature-complete, tested, documented, and released.

---

## 5. Dependency Management

### Go Dependencies

| Dependency | Purpose | License | Update Policy |
|-----------|---------|---------|--------------|
| chi | HTTP router | MIT | Dependabot, minor/patch auto-merge |
| crewjam/saml | SAML SP | Apache 2.0 | Manual review (security-critical) |
| golang-jwt/jwt | JWT signing + validation (RS256) | MIT | Dependabot, minor/patch auto-merge |
| casbin/casbin | RBAC engine | Apache 2.0 | Dependabot, minor/patch auto-merge |
| jackc/pgx | PostgreSQL driver | MIT | Dependabot, minor/patch auto-merge |
| redis/go-redis | Redis client | BSD-2 | Dependabot, minor/patch auto-merge |
| prometheus/client_golang | Metrics | Apache 2.0 | Dependabot, minor/patch auto-merge |
| go.opentelemetry.io/otel | Tracing | Apache 2.0 | Dependabot, minor/patch auto-merge |
| koanf | Configuration | MIT | Dependabot, minor/patch auto-merge |
| testify | Test assertions | MIT | Dependabot, auto-merge |
| testcontainers-go | Integration tests | MIT | Dependabot, auto-merge |
| golang-migrate | DB migrations | MIT | Dependabot, minor/patch auto-merge |

### Frontend Dependencies

All frontend dependencies follow the same pattern: Dependabot PRs, auto-merge for patch versions, manual review for minor/major.

### Vulnerability Response SLA

| Severity | Response Time | Resolution Time |
|----------|-------------|-----------------|
| Critical (CVSS ≥ 9.0) | 4 hours | 24 hours |
| High (CVSS 7.0–8.9) | 24 hours | 72 hours |
| Medium (CVSS 4.0–6.9) | 1 week | 2 weeks |
| Low (CVSS < 4.0) | Next sprint | Next release |

---

## 6. Quality Gates

Every sprint must pass these gates before the sprint is considered complete:

| Gate | Criteria |
|------|---------|
| Build | CI pipeline green (lint + unit tests + build + security scan) |
| Coverage | Go: ≥ 80% line coverage on new code. React: ≥ 70%. |
| Integration | All integration tests pass against real PostgreSQL + Redis (testcontainers) |
| Security | No new critical/high vulnerabilities in govulncheck or npm audit |
| Performance | No regression in proxy latency benchmarks (compared to previous sprint) |
| Documentation | New features have at least a README or doc stub |
| Review | All PRs merged with at least one approval |

Phase gates (at the end of each phase, spanning 1–3 sprints) add:

| Gate | Criteria |
|------|---------|
| E2E | Playwright tests pass for all completed user journeys |
| Demo | Sprint demo delivered to stakeholders with no critical bugs observed |
| Threat model | Security review of new attack surface introduced in the phase |

---

## 7. Monitoring the Plan Itself

### Velocity Tracking

Track story points (or task count) completed per sprint. After Sprint 2, use the rolling average to recalibrate future sprint scopes. If velocity is lower than planned, cut scope from Phases 8–9 (advanced features), never from Phases 0–6 (core).

### Risk Review

At every sprint retrospective, review the risk register from PRD Section 19. Update likelihood/impact ratings based on new information. Add new risks discovered during implementation.

### Scope Change Process

Any new feature request during development is evaluated against three criteria: does it block the current phase, can it be deferred to a later phase, and does it change the architecture. If it changes the architecture, it requires a design review before acceptance. All scope changes are logged in a decision register.

---

*This development plan is a companion to the MCP Proxy Gateway PRD v1.0. It should be updated at each sprint retrospective.*
