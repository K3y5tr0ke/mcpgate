#!/usr/bin/env bash
# run-playwright.sh — provision the full stack, run the Playwright E2E suite
# headless, and tear down. This is the CI-callable orchestration for the admin
# console E2E tests (Sprint 10, S9-D7: "Playwright in CI").
#
# It mirrors mcpgate's local-make CI model (no GitHub Actions): a CI job — or a
# developer — runs `make test-e2e`, which calls this script. To wire into GitHub
# Actions, a job would `services: [postgres, redis]` (or docker) then run
# `make test-e2e`; nothing here is CI-vendor specific.
#
# Steps: services → migrate → build web+binary → seed (capture admin password)
# → serve (background) → health-wait → playwright → teardown (trap).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

GVM_GO="${GVM_GO:-$HOME/.gvm/gos/go1.26.4/bin/go}"
export GOROOT="${GOROOT:-$HOME/.gvm/gos/go1.26.4}"
BASE_URL="${PLAYWRIGHT_BASE_URL:-http://localhost:8080}"
DATABASE_URL="${DATABASE_URL:-postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable}"
CONFIG="configs/mcpgate.dev.yaml"

SERVER_PID=""
cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

# Fail fast if the dev server is already bound — this script owns :8080.
if curl -sf "$BASE_URL/health" >/dev/null 2>&1; then
  echo "[e2e] a server is already running at $BASE_URL — stop it first (make stop)."
  exit 1
fi

echo "[e2e] starting Postgres + Redis..."
docker compose -f deployments/docker/docker-compose.dev.yml up -d --wait postgres redis

echo "[e2e] applying migrations..."
migrate -path internal/store/postgres/migrations -database "$DATABASE_URL" up

echo "[e2e] building web assets + binary..."
export NVM_DIR="${NVM_DIR:-$HOME/.nvm}"
# shellcheck source=/dev/null
[ -s "$NVM_DIR/nvm.sh" ] && . "$NVM_DIR/nvm.sh"
( cd web && npm ci && npm run build )
CGO_ENABLED=0 "$GVM_GO" build -o bin/mcpgate ./cmd/mcpgate

echo "[e2e] seeding admin user (capturing password)..."
SEED_OUT="$("$GVM_GO" run ./cmd/mcpgate seed --config "$CONFIG" 2>&1)"
ADMIN_PASSWORD="$(printf '%s\n' "$SEED_OUT" | sed -n 's/.*Password:[[:space:]]*//p' | head -1 | tr -d '[:space:]')"
if [ -z "$ADMIN_PASSWORD" ]; then
  echo "[e2e] could not capture admin password from seed output:"
  printf '%s\n' "$SEED_OUT"
  exit 1
fi
export ADMIN_PASSWORD

echo "[e2e] starting mcpgate server..."
./bin/mcpgate serve --config "$CONFIG" >/tmp/mcpgate-e2e.log 2>&1 &
SERVER_PID=$!

echo "[e2e] waiting for /health..."
for _ in $(seq 1 60); do
  curl -sf "$BASE_URL/health" >/dev/null 2>&1 && break
  sleep 1
done
if ! curl -sf "$BASE_URL/health" >/dev/null 2>&1; then
  echo "[e2e] server did not become healthy; last log lines:"
  tail -30 /tmp/mcpgate-e2e.log || true
  exit 1
fi

echo "[e2e] running Playwright (chromium)..."
( cd web \
  && npx playwright install chromium \
  && PLAYWRIGHT_BASE_URL="$BASE_URL" ADMIN_PASSWORD="$ADMIN_PASSWORD" CI=1 \
     npx playwright test --project=chromium )

echo "[e2e] passed."
