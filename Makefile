SHELL := /bin/bash

GO_VERSION := go1.26.0
GVM_GO     := $(HOME)/.gvm/gos/$(GO_VERSION)/bin/go
DATABASE_URL ?= postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable
MIGRATIONS  := internal/store/postgres/migrations
BIN         := bin/mcpgate

.PHONY: build build-web build-docker dev dev-frontend \
        test-unit test-integration test-coverage \
        lint lint-fix security-scan \
        migrate-up migrate-down migrate-create \
        docker-up docker-down \
        ci ci-fast ci-services \
        seed seed-idp setup-dev clean install-hooks help

# ── Build ─────────────────────────────────────────────────────────────────────

build-web:
	cd web && npm ci && npm run build

build: build-web
	CGO_ENABLED=0 $(GVM_GO) build -ldflags="-s -w" -o $(BIN) ./cmd/mcpgate

build-docker:
	docker build -t mcpgate:dev -f deployments/docker/Dockerfile .

# ── Development ───────────────────────────────────────────────────────────────

dev:
	$(GVM_GO) run ./cmd/mcpgate serve --config configs/mcpgate.dev.yaml

dev-frontend:
	cd web && npm run dev

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

test-coverage:
	$(GVM_GO) test -race -coverprofile=coverage.out -covermode=atomic ./internal/...
	$(GVM_GO) tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report: coverage.html"

# ── Linting and security ──────────────────────────────────────────────────────

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

security-scan:
	$(GVM_GO) run golang.org/x/vuln/cmd/govulncheck@latest ./...

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
	docker compose -f deployments/docker/docker-compose.dev.yml down -v

# ── Seed ─────────────────────────────────────────────────────────────────────

seed:
	$(GVM_GO) run ./cmd/mcpgate seed --config configs/mcpgate.dev.yaml

# Upsert the Auth0 dev SAML IdP config into saml_idp_configs.
# Idempotent — safe to run multiple times.
seed-idp:
	psql "$(DATABASE_URL)" -f scripts/seed_auth0_idp.sql

# Full first-time dev environment setup.
# Starts services, applies migrations, seeds DB + IdP, then starts servers.
setup-dev:
	docker compose -f deployments/docker/docker-compose.dev.yml up -d --wait postgres redis
	$(MAKE) migrate-up
	$(MAKE) seed
	$(MAKE) seed-idp
	$(MAKE) dev &
	$(MAKE) dev-frontend

# ── Local CI ──────────────────────────────────────────────────────────────────

# Fast check: lint + unit tests + build — no Docker needed (~30s).
# Run by the pre-push git hook on every push.
ci-fast: lint test-unit build

# Start only the CI services (Postgres + Redis) and wait until healthy.
ci-services:
	docker compose -f deployments/docker/docker-compose.dev.yml up -d --wait postgres redis

# Full local CI pipeline — mirrors the removed .github/workflows/ci.yml.
# Run this before opening a PR or merging to main.
ci: lint test-unit ci-services migrate-up test-integration build security-scan
	@echo ""
	@echo "All CI checks passed."

# Install the pre-push git hook that runs make ci-fast before every push.
install-hooks:
	@mkdir -p .git/hooks
	@cp scripts/pre-push.hook .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "pre-push hook installed (.git/hooks/pre-push)"

# ── Cleanup ───────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/ coverage.out coverage.html web/dist/

# ── Help ──────────────────────────────────────────────────────────────────────

help:
	@echo "mcpgate Makefile targets"
	@echo ""
	@echo "  build              Build web assets then compile binary to $(BIN)"
	@echo "  build-web          Build the React admin console (web/dist/)"
	@echo "  build-docker       Build production Docker image"
	@echo "  dev                Run server with dev config (hot reload: use air if desired)"
	@echo "  dev-all            Start Docker services + backend + frontend"
	@echo "  test-unit          Run unit tests (no external services)"
	@echo "  test-integration   Run integration tests (requires Postgres + Redis)"
	@echo "  test-coverage      Generate HTML coverage report"
	@echo "  lint               Run golangci-lint"
	@echo "  lint-fix           Run golangci-lint with auto-fix"
	@echo "  security-scan      Run govulncheck"
	@echo "  migrate-up         Apply all pending migrations"
	@echo "  migrate-down       Roll back the last migration"
	@echo "  migrate-create     Create a new migration (name=<suffix>)"
	@echo "  docker-up          Start Postgres + Redis via docker compose"
	@echo "  docker-down        Stop and remove Docker services"
	@echo "  ci-fast            Lint + unit tests + build (no Docker, used by pre-push hook)"
	@echo "  ci                 Full local CI pipeline (requires Docker)"
	@echo "  install-hooks      Install git pre-push hook"
	@echo "  seed               Seed database with default roles and admin user"
	@echo "  seed-idp           Upsert Auth0 dev SAML IdP config into Postgres"
	@echo "  setup-dev          Full first-time setup: services + migrate + seed + seed-idp + start"
	@echo "  clean              Remove build artifacts"
