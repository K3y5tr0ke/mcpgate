# mcpgate

A production-grade reverse proxy for MCP (Model Context Protocol) servers.
mcpgate sits between MCP clients (Claude Desktop, IDE extensions, custom agents)
and upstream MCP servers, adding authentication (SAML 2.0), authorization (RBAC),
structured audit logging, Prometheus metrics, and a React admin console — without
modifying MCP message semantics.

```
MCP Clients  →  mcpgate (auth · RBAC · logging · metrics)  →  Upstream MCP Servers
```

---

## How it works

mcpgate is a **front door** between users (or AI agents) and all upstream MCP servers. Users speak only to mcpgate; mcpgate checks their identity and permissions, then forwards the request to the right server. Upstream servers are never exposed directly.

### Server registration

An admin registers an upstream MCP server via the admin console or REST API, supplying its name, transport type, and connection details. The server name becomes the routing key in every URL:

```
POST /mcp/github-tools/message   →  forwards to the "github-tools" upstream
POST /mcp/jira-tools/message     →  forwards to the "jira-tools" upstream
```

Registration is configuration only — mcpgate does not connect to the server, send `initialize`, or fetch its tool list at registration time. The first real request from a client triggers the connection.

### Request journey

Every request through the proxy passes through a fixed middleware chain before reaching the upstream:

```
Client request
  │
  ├─ 1. Auth        — validate JWT (SAML session or M2M JWKS); extract identity + roles
  ├─ 2. Logging     — pre-parse JSON-RPC body; start audit log entry
  ├─ 3. Metrics     — increment Redis request counters (global + per-server)
  ├─ 4. Rate limit  — check RPM / RPD / concurrent connection limits per user
  ├─ 5. RBAC        — map MCP method → action category; check Casbin policy
  └─ 6. Proxy       — forward raw bytes to upstream; return raw response verbatim
```

### Action categories and RBAC

mcpgate maps MCP JSON-RPC methods to action categories for access-control decisions:

| Category  | MCP methods |
|-----------|-------------|
| `list`    | `tools/list`, `resources/list`, `prompts/list` |
| `call`    | `tools/call` |
| `read`    | `resources/read`, `resources/subscribe`, `prompts/get` |
| `write`   | `resources/write` |
| `connect` | `initialize`, `ping`, SSE stream open |

Casbin policies are stored as `(role, server, action)` tuples. Wildcards are supported:

```
(developer, *,            call)  →  developer can call tools on any server
(developer, github-tools, list)  →  developer can list tools on github-tools only
(admin,     *,            *)     →  admin can do anything on any server
```

Registering a new server does **not** create any Casbin policy. Whether a role can reach a new server depends entirely on whether an existing wildcard rule covers it.

### Transparent forwarding

mcpgate never modifies MCP message payloads. The raw JSON-RPC bytes from the client are forwarded unchanged to the upstream; the raw bytes from the upstream are returned unchanged to the client. The specific tool name inside a `tools/call` body is not parsed for routing — it is passed through verbatim and captured in the audit log.

For a detailed walkthrough of each step, the Casbin policy model, and the action category mapping, see [`docs/understanding.md`](docs/understanding.md).

---

## Requirements

| Tool | Version | Purpose |
|------|---------|---------|
| [gvm](https://github.com/moovweb/gvm) | any | Go version manager |
| Go | 1.26.0 (via gvm) | Backend |
| [Docker](https://docs.docker.com/get-docker/) | 24+ | Postgres, Redis, dev services |
| [Node.js](https://nodejs.org/) | 22 LTS | Admin console frontend |
| [nvm](https://github.com/nvm-sh/nvm) | any | Node version manager (optional but recommended) |
| openssl | any | Dev certificate generation |
| psql | any | `make seed-idp` — upsert IdP config |
| [golang-migrate](https://github.com/golang-migrate/migrate) CLI | v4 | Database migrations |
| [golangci-lint](https://golangci-lint.run/welcome/install/) | v2.12+ | Linting |

### Installing requirements

**gvm + Go 1.26.0**
```bash
bash < <(curl -s -S -L https://raw.githubusercontent.com/moovweb/gvm/master/binscripts/gvm-installer)
source ~/.gvm/scripts/gvm
gvm install go1.26.0 -B
gvm use go1.26.0 --default
```

**Docker**
Follow the official guide for your OS: https://docs.docker.com/get-docker/

**Node.js 22 via nvm**
```bash
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.0/install.sh | bash
source ~/.nvm/nvm.sh
nvm install 22
nvm use 22
```

**golang-migrate CLI**
```bash
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest
```

**golangci-lint**
```bash
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

**psql** (macOS)
```bash
brew install libpq
brew link --force libpq
```

---

## Quick start

```bash
git clone https://github.com/K3y5tr0ke/mcpgate
cd mcpgate

# First-time tool installation (gvm, migrate, golangci-lint, certs, Docker services, migrations)
bash scripts/setup.sh

# Seed the DB and start everything (backend :8080 + frontend :5173)
make setup-dev
```

Check it's running:
```bash
curl http://localhost:8080/health   # {"status":"ok"}
curl http://localhost:8080/ready    # {"status":"ok"} (requires Postgres + Redis)
```

Open the admin console: http://localhost:8080/console

---

## Developer workflows

Step-by-step sequences for the most common tasks. Run these from the repo root.

### First-time setup (run once after cloning)

```bash
# 1. Install the pre-push git hook (runs make ci-fast before every push)
make install-hooks

# 2. Start Postgres + Redis, apply migrations, seed DB + Auth0 IdP, start everything
make setup-dev
# → backend on :8080, frontend dev server on :5173
```

### Daily development (subsequent runs)

```bash
# Start Docker services (Postgres + Redis) if not already running
make docker-up

# Start backend + frontend together
make dev-all

# Or start them separately:
make dev           # backend on :8080
make dev-frontend  # frontend on :5173 (separate terminal)
```

### Unit tests (no external services required)

```bash
make test-unit
# Runs in ~seconds. Safe to run any time, no Docker needed.
```

### Integration tests (requires Postgres + Redis)

```bash
# 1. Start services and wait until healthy
make ci-services

# 2. Apply migrations (idempotent — safe to re-run)
make migrate-up

# 3. Run integration tests
make test-integration
```

### Full local CI pipeline (mirrors CI)

```bash
make ci
# Runs: lint → unit tests → start services → migrate → integration tests → build → security scan
# Use this before opening a PR.
```

### Fast pre-push check (no Docker)

```bash
make ci-fast
# Runs: lint → unit tests → build (~30 s)
# The pre-push hook runs this automatically on every git push (after make install-hooks).
```

### Running the example MCP servers

The repo includes three example MCP servers (one per transport) for integration testing and local development.

```bash
# 1. Build the stdio binary (required for stdio registration)
make build-examples
# → bin/mcp-streamable-example, bin/mcp-sse-example, bin/mcp-stdio-example

# 2. Start the two containerised example servers (Streamable HTTP + SSE)
make docker-up-examples
# Adds mcp-streamable (:8081) and mcp-sse (:8082) to the running dev stack.

# 3. Register all three servers in mcpgate via the admin API
#    Requires a running mcpgate instance and a valid admin JWT.
TOKEN=$(curl -s -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@mcpgate.local","password":"<password>"}' | jq -r .token)

MCPGATE_TOKEN=$TOKEN make seed-examples
```

See [`examples/seed-example-servers.sh`](examples/seed-example-servers.sh) for `--upstream-host` options when mcpgate itself runs in a container.

---

## What's implemented

### Sprint 0 — Foundation
- Go module (`github.com/K3y5tr0ke/mcpgate`), pinned to Go 1.26.0 via gvm
- `koanf`-based config loader: YAML + `MCPGATE_` env var overrides
- Store interfaces (`UserStore`, `SessionStore`, `ServerStore`, `RoleStore`, etc.)
- Postgres (`pgx/v5`) and Redis (`go-redis/v9`) clients with connection pooling
- `chi` HTTP server with `/health` and `/ready` endpoints, graceful shutdown
- SQL migrations (`golang-migrate`) under `internal/store/postgres/migrations/`
- Docker Compose dev stack (`deployments/docker/docker-compose.dev.yml`)
- CI pipeline (lint → unit → integration → build)
- `scripts/setup.sh` — one-shot dev environment bootstrap

### Sprint 1 — Core Proxy Engine
- MCP proxy handler supporting all three transports:
  - **Streamable HTTP** (`POST /mcp/{serverName}/message`)
  - **SSE** (`GET /mcp/{serverName}/sse`, `POST /mcp/{serverName}/sse`)
  - **stdio** — subprocess-based upstream with JSON-RPC framing
- JSON-RPC 2.0 parser and message router
- Semaphore-bounded upstream HTTP connection pool
- 30-second in-memory config cache for server lookups
- `ServerStore` (`pgx` implementation) — full CRUD
- Admin REST API skeleton: `GET/POST /api/v1/admin/servers`, `GET/PUT/DELETE /api/v1/admin/servers/{id}`
- Prometheus metrics registration (`/metrics` on `:9090`)
- `testcontainers-go` integration test helper + `MockMCPServer`

### Sprint 2 — Authentication & Admin Console
- **SAML 2.0 SP-initiated login** via [`crewjam/saml`](https://github.com/crewjam/saml):
  - `GET  /auth/saml/login` — initiates HTTP-Redirect binding to IdP
  - `POST /auth/saml/acs` — assertion consumer: validates signature, replay-detects, JIT-provisions user, issues JWT
  - `GET  /auth/saml/metadata` — serves SP metadata XML
  - `GET  /auth/saml/logout` — invalidates session + clears cookie
- **RS256 JWT** session tokens (`golang-jwt/jwt/v5`); ephemeral key in dev, file-backed in prod
- **Session management**: durable sessions in Postgres + sliding-window cache in Redis
- **Auth middleware**: validates JWT → looks up live session → injects `UserIdentity` into context
- **Brute-force guard**: Redis-backed rate limiting on login and ACS endpoints
- **RBAC middleware**: role-check (`admin` required for all `/api/v1/admin/*` routes)
- **JIT user provisioning**: auto-creates or links users from SAML assertions; attribute mapping from IdP claim URIs
- **Replay detection**: Redis `SET NX` on assertion ID prevents replayed assertions
- **IDP config CRUD**: `GET/POST /api/v1/admin/idps`, `GET/PUT/DELETE /api/v1/admin/idps/{id}`
- **Auth0 integration**: pre-configured SAML IdP connection to Auth0 tenant via `make seed-idp`
- **React admin console** (Vite + TanStack Router + Tailwind CSS):
  - Dashboard with metrics summary + timeseries charts
  - MCP Servers list
  - Audit log viewer
  - Served at `/console` from embedded `web/dist/`

---

## Configuration

Copy `configs/mcpgate.example.yaml`, or override with `MCPGATE_` env vars:

```bash
MCPGATE_DATABASE.PASSWORD=secret \
MCPGATE_SERVER.PORT=9000 \
./bin/mcpgate serve --config configs/mcpgate.yaml
```

Full annotated reference: [`configs/mcpgate.example.yaml`](configs/mcpgate.example.yaml)

Sensitive values (JWT signing key path, DB password, Redis password) should be
supplied via env vars or a secrets manager — never committed to the repo.
The `.env` file is gitignored and holds local dev credentials only.

---

## Makefile targets

### Setup & development

| Target | Description |
|--------|-------------|
| `make setup-dev` | **Full first-time setup**: starts Docker services, applies migrations, seeds DB + Auth0 IdP config, then starts backend and frontend |
| `make dev` | Run the backend server with dev config (`:8080`) |
| `make dev-frontend` | Run the Vite dev server (`:5173`) with proxy to backend |
| `make dev-all` | Start Docker services + backend + frontend (skips migrate/seed) |

### Build

| Target | Description |
|--------|-------------|
| `make build` | Build web assets then compile binary to `bin/mcpgate` |
| `make build-web` | Build React admin console to `web/dist/` |
| `make build-docker` | Build production Docker image |
| `make build-examples` | Build example MCP server binaries to `bin/` (required for stdio) |
| `make clean` | Remove `bin/`, `web/dist/`, coverage artifacts |

### Database

| Target | Description |
|--------|-------------|
| `make migrate-up` | Apply all pending migrations |
| `make migrate-down` | Roll back the last migration |
| `make migrate-create name=<suffix>` | Create a new migration file pair |
| `make seed` | Seed default roles and admin user |
| `make seed-idp` | Upsert Auth0 dev SAML IdP config into Postgres (idempotent) |

### Testing

| Target | Description |
|--------|-------------|
| `make test-unit` | Unit tests — no external services required (`-short`) |
| `make test-integration` | Integration tests — requires Postgres + Redis (`-tags=integration`) |
| `make test-coverage` | Generate HTML coverage report at `coverage.html` |

### Quality

| Target | Description |
|--------|-------------|
| `make lint` | Run `golangci-lint` |
| `make lint-fix` | Run `golangci-lint` with auto-fix |
| `make security-scan` | Run `govulncheck` |

### CI

| Target | Description |
|--------|-------------|
| `make ci-fast` | Lint + unit tests + build — no Docker (~30 s); run by the pre-push git hook |
| `make ci-services` | Start Postgres + Redis and wait until healthy |
| `make ci` | Full local CI pipeline: lint → unit → services → migrate → integration → build → security scan |

### Hooks & utilities

| Target | Description |
|--------|-------------|
| `make install-hooks` | Install the pre-push git hook (`scripts/pre-push.hook → .git/hooks/pre-push`) — run once after cloning |
| `make docker-up` | Start all Docker services (Postgres, Redis, Prometheus, Grafana) |
| `make docker-down` | Stop and remove all Docker services (destroys volumes) |
| `make docker-up-examples` | Start dev stack + containerised example MCP servers (Streamable HTTP + SSE) |
| `make seed-examples` | Register example MCP servers via admin API (requires `MCPGATE_TOKEN`) |
| `make stop` | Kill the running mcpgate server (ports 8080 + 9090) |
| `make help` | Print all targets with descriptions |

---

## Architecture

```
cmd/mcpgate/              Entry point: serve / migrate / seed / version
internal/
  config/                 koanf config loader (YAML + MCPGATE_ env vars)
  server/                 chi HTTP server, SPA handler, graceful shutdown
  proxy/                  MCP proxy engine, transport adapters
  auth/
    jwt/                  RS256 JWT manager (Issue + Validate)
    saml/                 SAML 2.0 SP: handlers, SP init, attribute mapping
    middleware.go         TokenValidator interface + Authenticate() chi middleware
    context.go            UserIdentity type + context helpers
  rbac/                   RBAC middleware (role enforcement)
  admin/api/              Admin REST API handlers (servers, users, idps, logs, metrics)
  store/
    interfaces.go         Store interface definitions + shared types
    postgres/             pgx repositories (users, sessions, servers, roles, saml)
    redis/                go-redis session cache + rate limiter
  logging/                Async audit log pipeline, hash-chain tamper evidence
  metrics/                Prometheus metrics registration
web/                      React admin console (Vite + TanStack Router + Tailwind)
deployments/docker/       docker-compose.dev.yml, Dockerfile
scripts/                  setup.sh, generate-certs.sh, seed.sh, seed_auth0_idp.sql
configs/                  mcpgate.example.yaml, mcpgate.dev.yaml
certs/                    Dev TLS + SAML certificates (gitignored)
docs/                     Architecture deep-dives, implementation notes, PRD
```

---

## Go version

Pinned to `go1.26.0` via [gvm](https://github.com/moovweb/gvm). The Makefile
invokes `$HOME/.gvm/gos/go1.26.0/bin/go` directly — no PATH manipulation required.

---

## Contributing

- Branching: short-lived feature branches off `main` (< 3 days)
- Every PR requires passing `make ci` (lint → unit → integration → build → security scan)
- Security-sensitive PRs (auth, RBAC, crypto) require a second review
- Definition of done: feature works end-to-end, unit test covers the happy path and at least one error path

---

## License

TBD
