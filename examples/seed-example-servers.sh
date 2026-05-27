#!/usr/bin/env bash
# seed-example-servers.sh — register the three example MCP servers in mcpgate.
#
# Prerequisites:
#   1. mcpgate is running (make dev or ./bin/mcpgate serve).
#   2. For stdio: the example binary has been built (make build-examples).
#   3. MCPGATE_TOKEN is set to a valid admin JWT.
#
# To get a token:
#   TOKEN=$(curl -s -X POST http://localhost:8080/auth/local/login \
#     -H 'Content-Type: application/json' \
#     -d '{"email":"admin@mcpgate.local","password":"<your-password>"}' \
#     | jq -r .token)
#   MCPGATE_TOKEN=$TOKEN bash examples/seed-example-servers.sh
#
# Options:
#   --base-url <url>      mcpgate base URL (default: http://localhost:8080)
#   --upstream-host <h>   hostname for upstream MCP servers (default: localhost)
#                         Use the docker-compose service name (e.g. mcp-streamable)
#                         when mcpgate itself runs in a container on mcpgate-net.

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
UPSTREAM_HOST="${UPSTREAM_HOST:-localhost}"
MCPGATE_TOKEN="${MCPGATE_TOKEN:?MCPGATE_TOKEN is required. See script header for how to obtain one.}"

while [[ $# -gt 0 ]]; do
    case $1 in
        --base-url)       BASE_URL="$2";       shift 2 ;;
        --upstream-host)  UPSTREAM_HOST="$2";  shift 2 ;;
        *)                echo "Unknown argument: $1"; exit 1 ;;
    esac
done

# Resolve the stdio binary path relative to the repo root (one level above examples/).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(dirname "$SCRIPT_DIR")"
STDIO_PATH="${REPO_ROOT}/bin/mcp-stdio-example"

if [[ ! -x "$STDIO_PATH" ]]; then
    echo "[seed-examples] WARNING: stdio binary not found at ${STDIO_PATH}"
    echo "                Run 'make build-examples' first, then re-run this script."
    echo "                The streamable-http and SSE servers will still be registered."
fi

api_post() {
    local body="$1"
    curl -sf -X POST "${BASE_URL}/api/v1/admin/servers" \
         -H "Authorization: Bearer ${MCPGATE_TOKEN}" \
         -H "Content-Type: application/json" \
         -d "$body"
}

echo "[seed-examples] Registering example-streamable (Streamable HTTP)..."
api_post "$(cat <<EOF
{
  "name":        "example-streamable",
  "description": "Example Streamable HTTP MCP server — echo, add, slow_echo tools",
  "transport":   "streamable_http",
  "config":      { "endpoint": "http://${UPSTREAM_HOST}:8081/mcp" },
  "is_enabled":  true
}
EOF
)"
echo ""

echo "[seed-examples] Registering example-sse (SSE)..."
api_post "$(cat <<EOF
{
  "name":        "example-sse",
  "description": "Example SSE MCP server — echo, add, slow_echo tools",
  "transport":   "sse",
  "config": {
    "sse_endpoint":     "http://${UPSTREAM_HOST}:8082/sse",
    "message_endpoint": "http://${UPSTREAM_HOST}:8082/message"
  },
  "is_enabled": true
}
EOF
)"
echo ""

if [[ -x "$STDIO_PATH" ]]; then
    echo "[seed-examples] Registering example-stdio (stdio)..."
    api_post "$(cat <<EOF
{
  "name":        "example-stdio",
  "description": "Example stdio MCP server — echo, add, slow_echo tools",
  "transport":   "stdio",
  "config":      { "command": "${STDIO_PATH}" },
  "is_enabled":  true
}
EOF
)"
    echo ""
fi

echo "[seed-examples] Done."
echo ""
echo "Test with:"
echo "  curl -s -X POST ${BASE_URL}/mcp/example-streamable/message \\"
echo "    -H 'Authorization: Bearer \$MCPGATE_TOKEN' \\"
echo "    -H 'Content-Type: application/json' \\"
echo "    -d '{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/list\"}' | jq ."
