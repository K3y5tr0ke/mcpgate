SHELL := /bin/bash

GO_VERSION := go1.26.0
GVM_GO     := $(HOME)/.gvm/gos/$(GO_VERSION)/bin/go
DATABASE_URL ?= postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable
MIGRATIONS  := internal/store/postgres/migrations
BIN         := bin/mcpgate

.PHONY: build build-docker dev dev-frontend \
        test-unit test-integration test-coverage \
        lint lint-fix security-scan \
        migrate-up migrate-down migrate-create \
        docker-up docker-down \
        seed clean help

# ── Build ─────────────────────────────────────────────────────────────────────

build:
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

# ── Cleanup ───────────────────────────────────────────────────────────────────

clean:
	rm -rf bin/ coverage.out coverage.html

# ── Help ──────────────────────────────────────────────────────────────────────

help:
	@echo "mcpgate Makefile targets"
	@echo ""
	@echo "  build              Compile binary to $(BIN)"
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
	@echo "  seed               Seed database with default roles and admin user"
	@echo "  clean              Remove build artifacts"
