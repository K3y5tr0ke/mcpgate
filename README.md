# mcpgate

A production-grade reverse proxy for MCP (Model Context Protocol) servers.
mcpgate sits between MCP clients (Claude Desktop, IDE extensions, custom agents)
and upstream MCP servers, adding authentication (SAML 2.0), authorization (RBAC),
structured audit logging, Prometheus metrics, and a React admin console — all
without modifying MCP message semantics.

```
MCP Clients  →  mcpgate (auth · RBAC · logging · metrics)  →  Upstream MCP Servers
```

## Quick start

**Prerequisites:** [gvm](https://github.com/moovweb/gvm) · Docker 24+ · Node.js 22+

```bash
git clone https://github.com/K3y5tr0ke/mcpgate
cd mcpgate
bash scripts/setup.sh   # installs tools, starts services, runs migrations
make dev                # starts the server on :8080
```

Check it's running:

```bash
curl http://localhost:8080/health  # {"status":"ok"}
curl http://localhost:8080/ready   # {"status":"ok"} (requires Postgres + Redis)
```

## Development

| Command | Description |
|---------|-------------|
| `make dev` | Run server with dev config |
| `make test-unit` | Run unit tests (no external services) |
| `make test-integration` | Run integration tests (requires Docker services) |
| `make test-coverage` | Generate HTML coverage report |
| `make lint` | Run golangci-lint |
| `make migrate-up` | Apply pending database migrations |
| `make migrate-down` | Roll back the last migration |
| `make docker-up` | Start Postgres + Redis in Docker |
| `make docker-down` | Stop Docker services |
| `make seed` | Seed default roles and admin user |
| `make build` | Compile binary to `bin/mcpgate` |
| `make help` | Show all targets |

## Architecture

```
cmd/mcpgate/          Entry point (serve / migrate / seed / version)
internal/
  config/             koanf-based config loader (YAML + env vars)
  server/             chi HTTP server, health/ready endpoints, graceful shutdown
  proxy/              MCP proxy engine, transport adapters (Streamable HTTP, SSE, stdio)
  auth/               SAML 2.0 SP, OAuth 2.0 server, JWT middleware
  rbac/               Casbin RBAC enforcer + middleware
  logging/            Async audit log pipeline, hash-chain tamper evidence
  metrics/            Prometheus metrics registration
  admin/api/          REST admin API (servers, users, roles, logs, metrics, settings)
  store/
    postgres/         pgx connection pool, repositories, SQL migrations
    redis/            go-redis client, session cache, rate limiter, policy cache
deployments/docker/   docker-compose.dev.yml, Dockerfile (Sprint 11)
scripts/              setup.sh, generate-certs.sh, seed.sh
configs/              mcpgate.example.yaml, mcpgate.dev.yaml
```

See [docs/](docs/) for architecture deep-dives, deployment guides, and API reference.

## Configuration

Copy `configs/mcpgate.example.yaml` and edit, or use `MCPGATE_` env vars:

```bash
MCPGATE_DATABASE.PASSWORD=secret \
MCPGATE_SERVER.PORT=9000 \
./bin/mcpgate serve --config configs/mcpgate.yaml
```

Full reference: `configs/mcpgate.example.yaml`.

## Go version

Pinned to `go1.26.0` via [gvm](https://github.com/moovweb/gvm). The Makefile
uses `$HOME/.gvm/gos/go1.26.0/bin/go` directly — no PATH manipulation needed.

## Contributing

- Branching: short-lived feature branches off `main` (< 3 days)
- Every PR requires passing CI (`lint → test-unit → test-integration → build`)
- Security-sensitive PRs (auth, RBAC, crypto) require a second review
- Definition of done: feature works end-to-end, unit test covers the happy path and at least one error path

## License

TBD
