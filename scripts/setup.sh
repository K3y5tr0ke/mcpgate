#!/usr/bin/env bash
# setup.sh — first-time dev environment setup
# Run once after cloning: bash scripts/setup.sh

set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[setup]${NC} $*"; }
warn()  { echo -e "${YELLOW}[setup]${NC} $*"; }
error() { echo -e "${RED}[setup]${NC} $*" >&2; }

cd "$(dirname "$0")/.."

# ── gvm + Go ──────────────────────────────────────────────────────────────────

GO_VERSION="go1.26.4"

if [[ ! -s "$HOME/.gvm/scripts/gvm" ]]; then
    error "gvm not found at \$HOME/.gvm/scripts/gvm"
    error "Install gvm: https://github.com/moovweb/gvm"
    exit 1
fi

# shellcheck source=/dev/null
source "$HOME/.gvm/scripts/gvm"

if ! gvm list | grep -q "$GO_VERSION"; then
    info "Installing $GO_VERSION via gvm..."
    gvm install "$GO_VERSION" -B          # -B installs a pre-built binary
fi

gvm use "$GO_VERSION"
info "Using $(go version)"

# ── Prerequisite checks ────────────────────────────────────────────────────────

check_cmd() {
    if ! command -v "$1" &>/dev/null; then
        error "Required tool '$1' not found. $2"
        exit 1
    fi
}

check_cmd docker     "Install Docker: https://docs.docker.com/get-docker/"
check_cmd node       "Install Node.js 22 LTS: https://nodejs.org/"
check_cmd openssl    "Install openssl via your package manager"

# ── Go tooling ────────────────────────────────────────────────────────────────

info "Installing Go tools..."

go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest

info "Go tools installed"

# ── Docker services ───────────────────────────────────────────────────────────

info "Starting PostgreSQL and Redis..."
docker compose -f deployments/docker/docker-compose.dev.yml up -d postgres redis

info "Waiting for PostgreSQL to be healthy..."
until docker compose -f deployments/docker/docker-compose.dev.yml exec -T postgres \
    pg_isready -U mcpgate -q; do
    sleep 1
done
info "PostgreSQL ready"

info "Waiting for Redis to be healthy..."
until docker compose -f deployments/docker/docker-compose.dev.yml exec -T redis \
    redis-cli ping | grep -q PONG; do
    sleep 1
done
info "Redis ready"

# ── Database migrations ────────────────────────────────────────────────────────

info "Running database migrations..."
DATABASE_URL="postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable"
migrate -path internal/store/postgres/migrations -database "$DATABASE_URL" up
info "Migrations applied"

# ── Dev config ────────────────────────────────────────────────────────────────
# mcpgate.dev.yaml is gitignored (it may hold local secrets), so derive it from
# the committed example on first setup and point it at the docker dev stack.

DEV_CFG="configs/mcpgate.dev.yaml"
if [ -f "$DEV_CFG" ]; then
    info "$DEV_CFG already exists — leaving it as-is"
else
    info "Creating $DEV_CFG from mcpgate.example.yaml..."
    cp configs/mcpgate.example.yaml "$DEV_CFG"
    # Match the docker-compose dev Postgres (devpassword is the public dev
    # password from docker-compose.dev.yml; TLS disabled for the local container).
    # The sed range is scoped to the database: block so Redis's empty password is
    # left untouched.
    sed -i.bak \
        -e '/^database:/,/^[a-zA-Z]/ s/password: ""/password: "devpassword"/' \
        -e '/^database:/,/^[a-zA-Z]/ s/ssl_mode: "prefer"/ssl_mode: "disable"/' \
        "$DEV_CFG"
    rm -f "$DEV_CFG.bak"
    # Fail loud if the example's format drifted so the sed silently no-ops —
    # otherwise the dev config would ship with an empty DB password and surface
    # later as a confusing connection failure.
    if ! grep -q 'password: "devpassword"' "$DEV_CFG"; then
        error "Could not set the dev DB password in $DEV_CFG."
        error "mcpgate.example.yaml's database block format changed — update scripts/setup.sh."
        rm -f "$DEV_CFG"
        exit 1
    fi
    info "  $DEV_CFG created (gitignored) — adjust for your environment"
    info "  (derived from mcpgate.example.yaml: only the database section is"
    info "   patched; all other settings follow the example's defaults)"
fi

# ── Dev certificates ──────────────────────────────────────────────────────────

info "Generating dev TLS and SAML certificates..."
bash scripts/generate-certs.sh
info "Certificates generated in certs/"

# ── Verify build ──────────────────────────────────────────────────────────────

info "Verifying build..."
make build
info "Build OK: bin/mcpgate"

info "Running unit tests..."
make test-unit
info "Unit tests passed"

echo ""
echo -e "${GREEN}========================================${NC}"
echo -e "${GREEN}  Setup complete!${NC}"
echo -e "${GREEN}========================================${NC}"
echo ""
echo "  Start the server:    make dev"
echo "  Run tests:           make test-unit"
echo "  Apply migrations:    make migrate-up"
echo "  See all targets:     make help"
echo ""
