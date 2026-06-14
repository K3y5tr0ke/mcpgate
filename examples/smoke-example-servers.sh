#!/usr/bin/env bash
# smoke-example-servers.sh — quick end-to-end check of the example MCP servers.
#
# Registers the three example servers (idempotently, via seed-example-servers.sh)
# and then sends a `tools/list` JSON-RPC request through the proxy to each
# transport, asserting a successful `result`. Exits non-zero if any probe fails.
#
# Prerequisites:
#   1. mcpgate is running (make dev).
#   2. The containerised servers are up (make docker-up-examples).
#   3. For stdio: the binary is built (make build-examples).
#   4. MCPGATE_TOKEN is set to a valid admin JWT.
#
# Usage:
#   MCPGATE_TOKEN=$TOKEN bash examples/smoke-example-servers.sh
#   (forwards --base-url / --upstream-host to the seed script)

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
MCPGATE_TOKEN="${MCPGATE_TOKEN:?MCPGATE_TOKEN is required (a valid admin JWT).}"

# Parse --base-url so probes hit the same instance the seed script targets.
ARGS=("$@")
i=0
while [[ $i -lt ${#ARGS[@]} ]]; do
    case "${ARGS[$i]}" in
        --base-url) BASE_URL="${ARGS[$((i+1))]}" ;;
    esac
    i=$((i+1))
done

command -v jq >/dev/null 2>&1 || { echo "jq is required for smoke probes (brew install jq)."; exit 1; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
STDIO_PATH="${REPO_ROOT}/bin/mcp-stdio-example"

echo "== Registering example servers =="
MCPGATE_TOKEN="$MCPGATE_TOKEN" BASE_URL="$BASE_URL" bash "${SCRIPT_DIR}/seed-example-servers.sh" "$@"

echo ""
echo "== Probing each transport with tools/list =="

FAILED=0

# probe <label> <proxy-path>
probe() {
    local label="$1" path="$2"
    local resp
    resp=$(curl -s -X POST "${BASE_URL}${path}" \
        -H "Authorization: Bearer ${MCPGATE_TOKEN}" \
        -H "Content-Type: application/json" \
        -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' || true)
    if printf '%s' "$resp" | jq -e '.result' >/dev/null 2>&1; then
        local n
        n=$(printf '%s' "$resp" | jq -r '(.result.tools // []) | length')
        echo "  PASS  ${label}  (${n} tools)"
    else
        echo "  FAIL  ${label}"
        printf '        response: %s\n' "$resp"
        FAILED=1
    fi
}

probe "example-streamable (streamable_http)" "/mcp/example-streamable/message"
probe "example-sse (sse)"                    "/mcp/example-sse/sse"

if [[ -x "$STDIO_PATH" ]]; then
    probe "example-stdio (stdio)" "/mcp/example-stdio/message"
else
    echo "  SKIP  example-stdio (stdio)  — binary not built (run 'make build-examples')"
fi

echo ""
if [[ "$FAILED" -eq 0 ]]; then
    echo "Smoke check passed."
else
    echo "Smoke check FAILED — see responses above."
    exit 1
fi
