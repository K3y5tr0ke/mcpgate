SHELL := /bin/bash

GO_VERSION := go1.26.4
GVM_GO     := $(HOME)/.gvm/gos/$(GO_VERSION)/bin/go
# Pin GOROOT *and* PATH to match GO_VERSION so the build is independent of the
# developer's current `gvm use` selection. Without GOROOT, an inherited value
# pointing at a different patch release makes the compiler reject the toolchain's
# own stdlib ("version goX does not match go tool version goY"); without the PATH
# prefix, tools that shell out to `go` (e.g. golangci-lint's typechecker) pick up
# a mismatched `go` and hit the same error in reverse.
export GOROOT := $(HOME)/.gvm/gos/$(GO_VERSION)
export PATH   := $(HOME)/.gvm/gos/$(GO_VERSION)/bin:$(PATH)
# Bootstrap nvm so npm/node are on PATH in non-interactive shells (Makefile, git hooks).
# Usage: $(NVM) npm ci   →  sources nvm then runs the rest of the line.
NVM        := export NVM_DIR="$${NVM_DIR:-$$HOME/.nvm}"; [ -s "$$NVM_DIR/nvm.sh" ] && . "$$NVM_DIR/nvm.sh";
DATABASE_URL ?= postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable
MIGRATIONS  := internal/store/postgres/migrations
BIN         := bin/mcpgate

.PHONY: build build-web build-docker build-examples dev dev-frontend \
        test-unit test-integration test-frontend test-e2e-playwright test-e2e test-coverage \
        lint lint-fix security-scan \
        migrate-up migrate-down migrate-create \
        docker-up docker-down docker-up-examples \
        ci ci-fast ci-services \
        seed seed-idp reset-audit-log seed-examples smoke-examples call-examples \
        load-test load-test-pipeline setup-dev stop clean install-hooks help

# ── Build ─────────────────────────────────────────────────────────────────────

build-web:
	$(NVM) cd web && npm ci && npm run build

build: build-web
	CGO_ENABLED=0 $(GVM_GO) build -ldflags="-s -w" -o $(BIN) ./cmd/mcpgate

build-docker:
	docker build -t mcpgate:dev -f deployments/docker/Dockerfile .

# Build the three example MCP server binaries into bin/.
# Required before running seed-examples for the stdio server.
build-examples:
	CGO_ENABLED=0 $(GVM_GO) build -o bin/mcp-streamable-example ./examples/mcp-servers/streamable-http
	CGO_ENABLED=0 $(GVM_GO) build -o bin/mcp-sse-example        ./examples/mcp-servers/sse
	CGO_ENABLED=0 $(GVM_GO) build -o bin/mcp-stdio-example      ./examples/mcp-servers/stdio

# ── Development ───────────────────────────────────────────────────────────────

dev:
	$(GVM_GO) run ./cmd/mcpgate serve --config configs/mcpgate.dev.yaml

dev-frontend:
	$(NVM) cd web && npm run dev

# Start Postgres + Redis in Docker, then run the backend and frontend
dev-all:
	docker compose -f deployments/docker/docker-compose.dev.yml up -d postgres redis
	$(MAKE) dev &
	$(MAKE) dev-frontend

# ── Testing ───────────────────────────────────────────────────────────────────

test-unit:
	$(GVM_GO) test -race -count=1 -short ./internal/...

test-integration:
	$(GVM_GO) test -race -count=1 -tags=integration ./internal/...

# Frontend lint (ESLint) + unit/component tests (Vitest, jsdom). Fast, no Docker —
# runs in ci-fast.
test-frontend:
	$(NVM) cd web && npm run lint && npm run test

# Playwright E2E against an ALREADY-RUNNING stack (server + Postgres + Redis).
# For the self-provisioning, CI-callable variant use `make test-e2e`.
test-e2e-playwright:
	$(NVM) cd web && npx playwright test --project=chromium

# Full self-provisioning Playwright E2E (S9-D7, "Playwright in CI"):
# services → migrate → build → seed (captures admin password) → serve →
# health-wait → playwright → teardown. This is what a CI job invokes.
test-e2e:
	bash scripts/run-playwright.sh

test-coverage:
	$(GVM_GO) test -race -coverprofile=coverage.out -covermode=atomic ./internal/...
	$(GVM_GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ── Linting and security ──────────────────────────────────────────────────────

# .golangci.yml is v2-format. A v1 binary fails with a cryptic
# "configuration file ... for v2 ... with v1" message, so guard the version up
# front with an actionable error. Install v2 with:
#   go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
define REQUIRE_GOLANGCI_V2
	@command -v golangci-lint >/dev/null 2>&1 || { \
	  echo "golangci-lint not found. Install v2:"; \
	  echo "  go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
	  exit 1; }
	@golangci-lint version 2>&1 | grep -qE 'version v?2\.' || { \
	  echo "golangci-lint v2 required (.golangci.yml is v2-format), found:"; \
	  golangci-lint version 2>&1 | head -1; \
	  echo "Upgrade: go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"; \
	  exit 1; }
endef

lint:
	$(REQUIRE_GOLANGCI_V2)
	golangci-lint run ./...

lint-fix:
	$(REQUIRE_GOLANGCI_V2)
	golangci-lint run --fix ./...

# Dependency vulnerability scan for both ecosystems: govulncheck (Go) and
# npm audit (frontend). Runs in `make ci` (needs network); not in ci-fast.
# --audit-level=high fails only on high/critical, matching govulncheck's
# called-vulnerability bar (low/moderate are reported but non-blocking).
security-scan:
	$(GVM_GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...
	$(NVM) cd web && npm audit --audit-level=high

# ── Database migrations ───────────────────────────────────────────────────────

migrate-up:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path $(MIGRATIONS) -database "$(DATABASE_URL)" down 1

# Usage: make migrate-create name=add_something
migrate-create:
	migrate create -ext sql -dir $(MIGRATIONS) -seq $(name)

# ── Docker ────────────────────────────────────────────────────────────────────

docker-up:
	docker compose -f deployments/docker/docker-compose.dev.yml up -d

docker-down:
	docker compose \
	  -f deployments/docker/docker-compose.dev.yml \
	  -f examples/docker-compose.examples.yml \
	  down -v --remove-orphans

# Start the dev stack plus the two containerised example MCP servers.
# The example build contexts are ../.. (repo root) because Compose resolves
# relative paths against the first -f file's directory (deployments/docker).
docker-up-examples:
	docker compose \
	  -f deployments/docker/docker-compose.dev.yml \
	  -f examples/docker-compose.examples.yml \
	  up -d

# ── Seed ─────────────────────────────────────────────────────────────────────

seed:
	$(GVM_GO) run ./cmd/mcpgate seed --config configs/mcpgate.dev.yaml

# Upsert the Auth0 dev SAML IdP config into saml_idp_configs.
# Idempotent — safe to run multiple times.
seed-idp:
	psql "$(DATABASE_URL)" -f scripts/seed_auth0_idp.sql

# DEV ONLY. Truncate the audit_logs table so the tamper-evident hash chain
# restarts from a clean seed. Needed once after the audit-log json-tag fix
# (the hash input representation changed, so entries written before the fix
# fail GET /logs/verify). DESTRUCTIVE — refuses to run against a non-local DB.
reset-audit-log:
	@echo "$(DATABASE_URL)" | grep -qE '@localhost([:/]|$$)' || { \
	  echo "Refusing: DATABASE_URL host is not exactly localhost."; exit 1; }
	@echo "Truncating audit_logs (dev) to reseed the hash chain..."
	@echo "  (TRUNCATE needs an exclusive lock — stop mcpgate first if this times out.)"
	psql "$(DATABASE_URL)" -v ON_ERROR_STOP=1 \
	  -c "SET lock_timeout = '5s';" -c "TRUNCATE audit_logs;"
	@echo "Done. Restart mcpgate so the chain reseeds from an empty table."

# Register the three example MCP servers via the admin REST API.
# Requires MCPGATE_TOKEN (a valid admin JWT) and a running mcpgate instance.
# Run 'make build-examples' first so the stdio binary is available.
seed-examples:
	@if [ -z "$(MCPGATE_TOKEN)" ]; then \
	  echo "MCPGATE_TOKEN is required."; \
	  echo "Get one with: curl -s -X POST http://localhost:8080/auth/local/login \\"; \
	  echo "  -H 'Content-Type: application/json' \\"; \
	  echo "  -d '{\"email\":\"admin@localhost\",\"password\":\"<password from make seed output>\"}' | jq -r .token"; \
	  exit 1; \
	fi
	MCPGATE_TOKEN=$(MCPGATE_TOKEN) bash examples/seed-example-servers.sh

# Register the example MCP servers (idempotent) and probe each transport with a
# tools/list call — a quick end-to-end check that the proxy reaches every upstream.
# Requires MCPGATE_TOKEN and a running mcpgate instance; run 'make build-examples'
# and 'make docker-up-examples' first. Exits non-zero if any probe fails.
smoke-examples:
	@if [ -z "$(MCPGATE_TOKEN)" ]; then \
	  echo "MCPGATE_TOKEN is required."; \
	  echo "Get one with: curl -s -X POST http://localhost:8080/auth/local/login \\"; \
	  echo "  -H 'Content-Type: application/json' \\"; \
	  echo "  -d '{\"email\":\"admin@localhost\",\"password\":\"<password from make seed output>\"}' | jq -r .token"; \
	  exit 1; \
	fi
	MCPGATE_TOKEN=$(MCPGATE_TOKEN) bash examples/smoke-example-servers.sh

# Exploratory client: walk every registered server and invoke its tools,
# resources, and prompts through the proxy, printing the responses. Requires
# MCPGATE_TOKEN and a running mcpgate instance with reachable upstreams.
# For a single raw call, run examples/call-example-tools.sh --call ... directly.
call-examples:
	@if [ -z "$(MCPGATE_TOKEN)" ]; then \
	  echo "MCPGATE_TOKEN is required."; \
	  echo "Get one with: curl -s -X POST http://localhost:8080/auth/local/login \\"; \
	  echo "  -H 'Content-Type: application/json' \\"; \
	  echo "  -d '{\"email\":\"admin@localhost\",\"password\":\"<password from make seed output>\"}' | jq -r .token"; \
	  exit 1; \
	fi
	MCPGATE_TOKEN=$(MCPGATE_TOKEN) bash examples/call-example-tools.sh

# ── Load tests (k6) ────────────────────────────────────────────────────────────
# Require k6 (brew install k6), MCPGATE_TOKEN, a running mcpgate, and a reachable
# registered upstream (make docker-up-examples && make seed-examples). See k6/README.md.

# 10.9 — proxy throughput: ramp 100→1000 req/s; asserts p95<50ms, p99<100ms, <1% errors.
load-test:
	@command -v k6 >/dev/null 2>&1 || { echo "k6 not found (brew install k6)."; exit 1; }
	@if [ -z "$(MCPGATE_TOKEN)" ]; then echo "MCPGATE_TOKEN is required."; exit 1; fi
	k6 run -e MCPGATE_TOKEN=$(MCPGATE_TOKEN) k6/proxy_throughput.js

# 10.10 — audit-log pipeline under stress: 1000 req/s for 10m (~600k entries).
load-test-pipeline:
	@command -v k6 >/dev/null 2>&1 || { echo "k6 not found (brew install k6)."; exit 1; }
	@if [ -z "$(MCPGATE_TOKEN)" ]; then echo "MCPGATE_TOKEN is required."; exit 1; fi
	k6 run -e MCPGATE_TOKEN=$(MCPGATE_TOKEN) k6/log_pipeline.js

# Full first-time dev environment setup.
# Starts services, applies migrations, seeds DB + IdP, then starts servers.
setup-dev:
	$(MAKE) build-web
	docker compose -f deployments/docker/docker-compose.dev.yml up -d --wait postgres redis
	$(MAKE) migrate-up
	$(MAKE) seed
	$(MAKE) seed-idp
	$(MAKE) dev &
	$(MAKE) dev-frontend

# ── Local CI ──────────────────────────────────────────────────────────────────

# Fast check: lint + Go unit + frontend unit + build — no Docker needed (~30s).
# Run by the pre-push git hook on every push.
ci-fast: lint test-unit test-frontend build

# Start only the CI services (Postgres + Redis) and wait until healthy.
ci-services:
	docker compose -f deployments/docker/docker-compose.dev.yml up -d --wait postgres redis

# Full local CI pipeline — mirrors the removed .github/workflows/ci.yml.
# Run this before opening a PR or merging to main.
# Playwright E2E (test-e2e-playwright) is NOT included here: it needs a running
# server + seeded admin, which a dedicated CI job provisions (docker-compose up →
# migrate-up → seed → serve & → health-wait → make test-e2e-playwright).
ci: lint test-unit test-frontend ci-services migrate-up test-integration build security-scan
	@echo ""
	@echo "All CI checks passed."

# Install the pre-push git hook that runs make ci-fast before every push.
install-hooks:
	@mkdir -p .git/hooks
	@cp scripts/pre-push.hook .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "pre-push hook installed (.git/hooks/pre-push)"

# ── Cleanup ───────────────────────────────────────────────────────────────────

stop:
	@lsof -ti :8080 -ti :9090 2>/dev/null | sort -u | xargs kill 2>/dev/null && echo "mcpgate stopped" || echo "mcpgate not running"

clean:
	rm -rf bin/ coverage.out coverage.html web/dist/

# ── Help ──────────────────────────────────────────────────────────────────────

help:
	@echo "mcpgate Makefile targets"
	@echo ""
	@echo "  build              Build web assets then compile binary to $(BIN)"
	@echo "  build-web          Build the React admin console (web/dist/)"
	@echo "  build-docker       Build production Docker image"
	@echo "  build-examples     Build example MCP server binaries to bin/"
	@echo "  dev                Run server with dev config (hot reload: use air if desired)"
	@echo "  dev-all            Start Docker services + backend + frontend"
	@echo "  test-unit          Run unit tests (no external services)"
	@echo "  test-integration   Run integration tests (requires Postgres + Redis)"
	@echo "  test-e2e           Self-provisioning Playwright E2E (services+seed+serve+run)"
	@echo "  test-coverage      Generate HTML coverage report"
	@echo "  lint               Run golangci-lint"
	@echo "  lint-fix           Run golangci-lint with auto-fix"
	@echo "  security-scan      Run govulncheck (Go) + npm audit (frontend)"
	@echo "  migrate-up         Apply all pending migrations"
	@echo "  migrate-down       Roll back the last migration"
	@echo "  migrate-create     Create a new migration (name=<suffix>)"
	@echo "  docker-up          Start Postgres + Redis via docker compose"
	@echo "  docker-down        Stop and remove Docker services + example containers if running"
	@echo "  docker-up-examples Start dev stack + containerised example MCP servers"
	@echo "  ci-fast            Lint + unit tests + build (no Docker, used by pre-push hook)"
	@echo "  ci                 Full local CI pipeline (requires Docker)"
	@echo "  install-hooks      Install git pre-push hook"
	@echo "  seed               Seed database with default roles and admin user"
	@echo "  seed-idp           Upsert Auth0 dev SAML IdP config into Postgres"
	@echo "  reset-audit-log    DEV: truncate audit_logs to reseed the hash chain"
	@echo "  seed-examples      Register example MCP servers via admin API (needs MCPGATE_TOKEN)"
	@echo "  smoke-examples     Register + probe example MCP servers with tools/list (needs MCPGATE_TOKEN)"
	@echo "  call-examples      Invoke tools/resources/prompts on each server via the proxy (needs MCPGATE_TOKEN)"
	@echo "  load-test          k6 proxy throughput load test (needs k6 + MCPGATE_TOKEN)"
	@echo "  load-test-pipeline k6 audit-log pipeline stress test (needs k6 + MCPGATE_TOKEN)"
	@echo "  setup-dev          Full first-time setup: services + migrate + seed + seed-idp + start"
	@echo "  stop               Kill the running mcpgate server (ports 8080 + 9090)"
	@echo "  clean              Remove build artifacts"
