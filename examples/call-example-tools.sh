#!/usr/bin/env bash
# call-example-tools.sh — exercise MCP tools / resources / prompts through the
# mcpgate proxy. Unlike smoke-example-servers.sh (which is a pass/fail check),
# this is an exploratory client: it lists each registered server's capabilities
# and actually invokes them, printing the responses.
#
# Prerequisites:
#   1. mcpgate is running and the upstream servers are reachable.
#   2. MCPGATE_TOKEN is set to a valid admin JWT (or any token with access).
#   3. jq is installed.
#
# Usage:
#   MCPGATE_TOKEN=$TOKEN bash examples/call-example-tools.sh
#       → walk every registered server: tools/list + call known tools,
#         resources/list (+read), prompts/list (+get).
#
#   MCPGATE_TOKEN=$TOKEN bash examples/call-example-tools.sh --server example-sse
#       → only that server.
#
#   MCPGATE_TOKEN=$TOKEN bash examples/call-example-tools.sh \
#       --call example-streamable tools/call '{"name":"echo","arguments":{"text":"hi"}}'
#       → send one arbitrary JSON-RPC request and print the raw response.
#
# Options:
#   --base-url <url>   mcpgate base URL (default: http://localhost:8080)
#   --server <name>    restrict the walk to a single server
#   --call <server> <method> [params-json]   one-shot raw call, then exit

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
MCPGATE_TOKEN="${MCPGATE_TOKEN:?MCPGATE_TOKEN is required (a valid admin JWT).}"
ONLY_SERVER=""

command -v jq >/dev/null 2>&1 || { echo "jq is required (brew install jq)."; exit 1; }

# --- argument parsing ---
while [[ $# -gt 0 ]]; do
    case "$1" in
        --base-url) BASE_URL="$2"; shift 2 ;;
        --server)   ONLY_SERVER="$2"; shift 2 ;;
        --call)
            # --call <server> <method> [params-json]
            CALL_SERVER="${2:?--call needs a server}"
            CALL_METHOD="${3:?--call needs a method}"
            CALL_PARAMS="${4:-}"
            shift $(( $# >= 4 ? 4 : $# ))
            ONESHOT=1
            ;;
        *) echo "Unknown argument: $1"; exit 1 ;;
    esac
done

# Fetch the registered servers once; used to resolve each server's transport.
SERVERS_JSON="$(curl -s "${BASE_URL}/api/v1/admin/servers" -H "Authorization: Bearer ${MCPGATE_TOKEN}")"

server_transport() {
    printf '%s' "$SERVERS_JSON" | jq -r --arg n "$1" '.servers[]? | select(.name==$n) | .transport'
}

# The proxy exposes SSE servers at POST /mcp/{name}/sse and everything else
# (streamable_http, stdio) at POST /mcp/{name}/message.
proxy_path() {
    case "$(server_transport "$1")" in
        sse) echo "sse" ;;
        *)   echo "message" ;;
    esac
}

# rpc <server> <method> [params-json] → raw JSON-RPC response from the upstream.
rpc() {
    local server="$1" method="$2" params="${3:-}"
    local path body
    path="$(proxy_path "$server")"
    if [[ -n "$params" ]]; then
        body="$(jq -nc --arg m "$method" --argjson p "$params" '{jsonrpc:"2.0",id:1,method:$m,params:$p}')"
    else
        body="$(jq -nc --arg m "$method" '{jsonrpc:"2.0",id:1,method:$m}')"
    fi
    curl -s -X POST "${BASE_URL}/mcp/${server}/${path}" \
        -H "Authorization: Bearer ${MCPGATE_TOKEN}" \
        -H "Content-Type: application/json" \
        -d "$body"
}

# show <label> <response> — print a ✓/✗ line plus the result (or error) indented.
show() {
    local label="$1" resp="$2"
    if printf '%s' "$resp" | jq -e '.result' >/dev/null 2>&1; then
        echo "  ✓ ${label}"
        printf '%s' "$resp" | jq -c '.result' | sed 's/^/      /'
    else
        echo "  ✗ ${label}"
        printf '%s' "$resp" | jq -c '.error // .' | sed 's/^/      /'
    fi
}

# --- one-shot mode ---
if [[ "${ONESHOT:-0}" == "1" ]]; then
    echo "POST /mcp/${CALL_SERVER}/$(proxy_path "$CALL_SERVER")  →  ${CALL_METHOD}"
    rpc "$CALL_SERVER" "$CALL_METHOD" "${CALL_PARAMS:-}" | jq .
    exit 0
fi

# --- walk mode ---
if [[ -n "$ONLY_SERVER" ]]; then
    SERVERS="$ONLY_SERVER"
else
    SERVERS="$(printf '%s' "$SERVERS_JSON" | jq -r '.servers[]?.name')"
fi

if [[ -z "${SERVERS// }" ]]; then
    echo "No servers registered. Run 'make seed-examples' (or smoke-examples) first."
    exit 1
fi

while IFS= read -r server; do
    [[ -z "$server" ]] && continue
    echo ""
    echo "═══ ${server}  (transport: $(server_transport "$server"))  ═══"

    # Tools
    tools_resp="$(rpc "$server" "tools/list")"
    show "tools/list" "$tools_resp"
    while IFS= read -r tool; do
        [[ -z "$tool" ]] && continue
        case "$tool" in
            echo)      show "tools/call $tool" "$(rpc "$server" tools/call '{"name":"echo","arguments":{"message":"hello via proxy"}}')" ;;
            add)       show "tools/call $tool" "$(rpc "$server" tools/call '{"name":"add","arguments":{"a":2,"b":3}}')" ;;
            slow_echo) show "tools/call $tool" "$(rpc "$server" tools/call '{"name":"slow_echo","arguments":{"message":"slow","delay_ms":50}}')" ;;
            *)         echo "  • tools/call $tool — skipped (no demo args)" ;;
        esac
    done < <(printf '%s' "$tools_resp" | jq -r '.result.tools[]?.name')

    # Resources (best-effort)
    res_resp="$(rpc "$server" "resources/list")"
    if printf '%s' "$res_resp" | jq -e '.result.resources[0]?' >/dev/null 2>&1; then
        show "resources/list" "$res_resp"
        uri="$(printf '%s' "$res_resp" | jq -r '.result.resources[0].uri')"
        show "resources/read ${uri}" "$(rpc "$server" resources/read "$(jq -nc --arg u "$uri" '{uri:$u}')")"
    fi

    # Prompts (best-effort)
    pr_resp="$(rpc "$server" "prompts/list")"
    if printf '%s' "$pr_resp" | jq -e '.result.prompts[0]?' >/dev/null 2>&1; then
        show "prompts/list" "$pr_resp"
        pname="$(printf '%s' "$pr_resp" | jq -r '.result.prompts[0].name')"
        show "prompts/get ${pname}" "$(rpc "$server" prompts/get "$(jq -nc --arg n "$pname" '{name:$n}')")"
    fi
done <<< "$SERVERS"

echo ""
echo "Done."
