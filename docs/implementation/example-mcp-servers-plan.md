# Example MCP Servers Plan — Containerized Integration Test Fixtures

**Date:** 2026-05-27
**Status:** Pre-implementation
**Companion docs:** `docs/implementation/stage-9-plan.md` · `docs/mcpgate-development-plan.md`

---

## 1. Purpose

The existing `internal/testutil/mockserver.go` is an in-process `httptest.Server` that only speaks the Streamable HTTP transport. It exists solely to satisfy Go unit tests. It is not a realistic MCP server and cannot be used from a running mcpgate instance, a browser, or an MCP client.

This plan delivers three standalone MCP servers — one per transport — that:

1. Run alongside mcpgate in the docker-compose dev stack (Streamable HTTP and SSE).
2. Act as realistic upstream targets for end-to-end and Playwright tests.
3. Exercise the proxy's three transport code paths: `transport/streamable.go`, `transport/sse.go`, and `transport/stdio.go`.
4. Are simple enough to read and understand — they double as living documentation of what a compliant MCP server looks like per transport.

---

## 2. Transport Recap

Before the design: a precise description of what each transport expects, as implemented in the proxy.

### 2.1 Streamable HTTP (`transport/streamable.go`)

**Proxy behaviour:** `Send()` makes a single `POST` to the configured endpoint with `Content-Type: application/json` and `Accept: application/json`. It expects `HTTP 200` with a JSON-RPC response body.

**What the server must do:**
- Accept `POST /mcp` (or any path configured in the server record).
- Read the JSON-RPC request from the body.
- Return `HTTP 200` with `Content-Type: application/json` and the JSON-RPC response.

**Health check:** the proxy sends `{"jsonrpc":"2.0","id":1,"method":"ping"}` via the same path.

### 2.2 SSE (`transport/sse.go`)

The proxy's SSE transport has two endpoints and two methods:

| Proxy method | HTTP verb | Endpoint | Purpose |
|---|---|---|---|
| `Send()` | POST | `messageEndpoint` | Single request; expects JSON or SSE response inline |
| `Stream()` | GET | `sseEndpoint` | Long-lived SSE stream for server-push notifications |

**`Send()` detail:** after POSTing, the proxy reads the `Content-Type` header. If it is `text/event-stream`, it calls `readFirstSSEData()` which reads lines until it finds one starting with `data:` and returns that payload. If it is `application/json`, it reads the body normally.

**What the server must do:**
- `POST /message` → respond with `Content-Type: text/event-stream`, write `data: <json-rpc-response>\n\n`, then close the response body. This is the "inline SSE response" pattern and is what exercises `readFirstSSEData()`.
- `GET /sse` → open a long-lived SSE stream (`Content-Type: text/event-stream`, `Connection: keep-alive`). This endpoint is connected to by `HandleSSEStream` which then sits waiting for the context to cancel.

**Health check:** same `ping` via `Send()` → POST to `messageEndpoint`.

### 2.3 Stdio (`transport/stdio.go`)

**Proxy behaviour:** `Send()` calls `ensureRunning()` which calls `exec.Command(command, args...)`. The process is started lazily on first use. JSON-RPC messages are written to the child's stdin as newline-terminated JSON. Responses are read from stdout by a background `readLoop` goroutine and matched to callers by `id`.

**What the server must do:**
- Read newline-terminated JSON-RPC requests from stdin.
- Write newline-terminated JSON-RPC responses to stdout.
- Stay alive (do not exit) until stdin is closed.
- Not write anything else to stdout (no startup messages — those go to stderr).

**What the server CANNOT do:**
- Run in a separate container. `exec.Command` requires the binary to be accessible on the same filesystem as the mcpgate process. See §4 (Issues) for the two viable approaches.

---

## 3. Design

### 3.1 Directory Structure

```
examples/
├── mcp-servers/
│   ├── common/
│   │   └── handler.go          # shared JSON-RPC dispatch: dispatch(method, params) → result/error
│   ├── streamable-http/
│   │   ├── main.go             # HTTP server: POST /mcp
│   │   └── Dockerfile
│   ├── sse/
│   │   ├── main.go             # HTTP server: GET /sse + POST /message
│   │   └── Dockerfile
│   └── stdio/
│       └── main.go             # stdin→stdout newline-delimited JSON-RPC (no Dockerfile)
├── docker-compose.examples.yml # overlays two new services onto docker-compose.dev.yml
└── seed-example-servers.sh     # registers the three servers via the admin REST API
```

All Go files belong to the same module (`github.com/K3y5tr0ke/mcpgate`). The example `main` packages live at `examples/mcp-servers/{transport}/main.go` and are built via explicit `go build` targets — not included in `./...` builds by default (see §3.5).

### 3.2 Shared Handler (`common/handler.go`)

All three servers implement identical MCP business logic. The shared handler is a pure function that takes a method name and raw params and returns a result value or an error code:

```go
package common

import "encoding/json"

// Dispatch handles an MCP JSON-RPC method and returns (result, errorCode, errorMsg).
// result is nil on error; errorCode is 0 on success.
// id == nil means the message is a notification — return nothing.
func Dispatch(id json.RawMessage, method string, params json.RawMessage) (any, int, string, bool)
// The 4th return value (bool) is true when the caller should write a response.
// Notifications (id == nil) return (nil, 0, "", false) — the server writes nothing.
```

**Supported methods — complete spec coverage:**

| Method | RBAC action | Response |
|--------|-------------|----------|
| `initialize` | `connect` | `protocolVersion`, `capabilities`, `serverInfo` |
| `initialized` | notification | _(no response — must be silently dropped)_ |
| `ping` | `connect` | `{}` |
| `tools/list` | `list` | tools array |
| `tools/call` | `call` | `content` array |
| `resources/list` | `list` | resources array |
| `resources/read` | `read` | resource content |
| `prompts/list` | `list` | prompts array |
| `prompts/get` | `read` | rendered prompt with messages |
| `notifications/*` | — | _(any notification — silently dropped)_ |
| anything else | — | `-32601` method not found |

**`initialize` capabilities — must match what the server actually supports:**

```json
{
  "protocolVersion": "2024-11-05",
  "capabilities": {
    "tools":     {},
    "resources": { "subscribe": false, "listChanged": false },
    "prompts":   { "listChanged": false }
  },
  "serverInfo": { "name": "<MCP_SERVER_NAME>", "version": "0.1.0" }
}
```

Without `resources` and `prompts` in capabilities, a real MCP client will never send `resources/list` or `prompts/list`, making those code paths unreachable in integration tests.

**Tools:**

| Tool | Parameters | Returns | Purpose |
|------|-----------|---------|---------|
| `echo` | `message: string` | `message` string | Basic round-trip; verifies parameter passing |
| `add` | `a: number, b: number` | sum as number | Verifies numeric parameter parsing |
| `slow_echo` | `message: string, delay_ms: number` | `message` string after sleep | Triggers timeout and circuit breaker tests |

**Resources:**

| URI | MIME type | Content |
|-----|-----------|---------|
| `file://greeting.txt` | `text/plain` | `"Hello from <MCP_SERVER_NAME> MCP server"` |

`resources/read` params: `{"uri": "file://greeting.txt"}`. Any other URI → `-32602` invalid params.

**Prompts:**

| Name | Arguments | Returns |
|------|-----------|---------|
| `system-prompt` | _(none)_ | A single `system` message: `"You are connected to the <MCP_SERVER_NAME> example MCP server."` |

`prompts/get` params: `{"name": "system-prompt"}`. Any other name → `-32602` invalid params.

**Notification handling:**

MCP notifications have no `id` field. They must be silently dropped — returning a JSON-RPC error for a notification is a protocol violation. The dispatcher checks `id == nil && method != ""` to detect notifications before calling `Dispatch`. The `initialized` notification is the most common; clients send it after `initialize` succeeds.

**Resource:**
- `file://greeting.txt` — content: `"Hello from <transport> MCP server"` where `<transport>` is set at startup via an env var `MCP_SERVER_NAME` (default: `"example"`).

### 3.3 Streamable HTTP Server (`streamable-http/main.go`)

```
Listen: :8081
Route:  POST /mcp
```

The handler reads the request body, calls `common.Dispatch`, and writes the JSON-RPC response with `Content-Type: application/json`.

No persistent connections, no sessions. Each POST is fully independent.

**Dockerfile (multi-stage):**

```dockerfile
FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY . .
RUN go build -o /mcp-streamable ./examples/mcp-servers/streamable-http

FROM alpine:3.20
COPY --from=builder /mcp-streamable /usr/local/bin/mcp-streamable
ENV MCP_SERVER_NAME=streamable-http
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/mcp-streamable"]
```

The `COPY . .` step copies the entire repo context, which is needed because the `common` package is at a sibling path. `.dockerignore` keeps the image small.

### 3.4 SSE Server (`sse/main.go`)

```
Listen: :8082
Routes:
  GET  /sse      → persistent SSE stream (currently just holds connection open)
  POST /message  → inline SSE response
```

**POST /message handler:**
```go
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
result, code, msg := common.Dispatch(req.Method, req.Params)
var resp jsonRPCResponse
if code != 0 {
    resp = errorResponse(req.ID, code, msg)
} else {
    resp = resultResponse(req.ID, result)
}
data, _ := json.Marshal(resp)
fmt.Fprintf(w, "data: %s\n\n", data)
// flush and close — the proxy reads the first data event then moves on
```

**GET /sse handler:**

Sends an `event: connected` ping line immediately, then holds the connection open until the client disconnects (context cancelled). This exercises the `HandleSSEStream` proxy path which simply waits for context cancellation.

```go
w.Header().Set("Content-Type", "text/event-stream")
w.Header().Set("Cache-Control", "no-cache")
w.Header().Set("Connection", "keep-alive")
flusher.Flush()
// send an initial event so the client knows the stream is alive
fmt.Fprintf(w, "event: connected\ndata: {}\n\n")
flusher.Flush()
<-r.Context().Done()
```

**Dockerfile:** identical structure to the streamable-http Dockerfile, pointing to `./examples/mcp-servers/sse` and exposing `:8082`.

### 3.5 Stdio Server (`stdio/main.go`)

No Dockerfile. The binary is compiled to `bin/mcp-stdio-example` on the host.

```go
scanner := bufio.NewScanner(os.Stdin)
for scanner.Scan() {
    line := scanner.Bytes()
    var req jsonRPCRequest
    if err := json.Unmarshal(line, &req); err != nil {
        continue  // silently drop malformed lines
    }
    result, code, msg := common.Dispatch(req.Method, req.Params)
    var resp any
    if code != 0 {
        resp = errorResponse(req.ID, code, msg)
    } else {
        resp = resultResponse(req.ID, result)
    }
    data, _ := json.Marshal(resp)
    os.Stdout.Write(append(data, '\n'))
}
```

Stderr is used for any startup/diagnostic messages (e.g., `fmt.Fprintln(os.Stderr, "mcp-stdio-example: ready")`).

**Makefile targets:**

```makefile
build-examples:
	$(GOPATH_CMD) go build -o bin/mcp-streamable-example ./examples/mcp-servers/streamable-http
	$(GOPATH_CMD) go build -o bin/mcp-sse-example      ./examples/mcp-servers/sse
	$(GOPATH_CMD) go build -o bin/mcp-stdio-example    ./examples/mcp-servers/stdio

.PHONY: build-examples
```

Added as a dependency of `make dev` so the stdio binary is always available when running locally.

### 3.6 Docker Compose Overlay (`docker-compose.examples.yml`)

A standalone Compose file that can be used alongside the dev stack:

```yaml
name: mcpgate-examples

services:
  mcp-streamable:
    build:
      context: ../..            # repo root (so common/ is reachable)
      dockerfile: examples/mcp-servers/streamable-http/Dockerfile
    environment:
      MCP_SERVER_NAME: streamable-http
    ports:
      - "8081:8081"
    restart: unless-stopped

  mcp-sse:
    build:
      context: ../..
      dockerfile: examples/mcp-servers/sse/Dockerfile
    environment:
      MCP_SERVER_NAME: sse
    ports:
      - "8082:8082"
    restart: unless-stopped

networks:
  default:
    name: mcpgate-net        # join the same network as docker-compose.dev.yml
    external: true
```

By joining `mcpgate-net`, the example servers are reachable from the mcpgate process by their service name when mcpgate is also containerized (Sprint 10). In the current dev setup where mcpgate runs natively on the host, the servers are reachable at `localhost:8081` and `localhost:8082`.

**Usage:**
```bash
# start dev stack + example servers
docker compose -f deployments/docker/docker-compose.dev.yml \
               -f examples/docker-compose.examples.yml up -d
```

This is documented in the `Makefile` as:
```makefile
docker-up-examples:
	docker compose -f deployments/docker/docker-compose.dev.yml \
	               -f examples/docker-compose.examples.yml up -d
```

### 3.7 Seed Script (`seed-example-servers.sh`)

Registers the three servers against a running mcpgate instance via the admin REST API. Requires a valid `$MCPGATE_TOKEN` (issued after local-admin login).

```bash
#!/usr/bin/env bash
# seed-example-servers.sh — register the three example MCP servers in mcpgate.
# Requires: mcpgate running and MCPGATE_TOKEN env var set.
# Usage: MCPGATE_TOKEN=<jwt> bash examples/seed-example-servers.sh [--base-url <url>]

set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
MCPGATE_TOKEN="${MCPGATE_TOKEN:?MCPGATE_TOKEN is required}"

api() {
    curl -sf -X POST "${BASE_URL}/api/v1/admin/servers" \
         -H "Authorization: Bearer ${MCPGATE_TOKEN}" \
         -H "Content-Type: application/json" \
         -d "$1"
}

echo "[seed-examples] Registering streamable-http example server..."
api '{
  "name": "example-streamable",
  "transport": "streamable_http",
  "endpoint": "http://localhost:8081/mcp",
  "description": "Example Streamable HTTP MCP server"
}'

echo "[seed-examples] Registering SSE example server..."
api '{
  "name": "example-sse",
  "transport": "sse",
  "endpoint": "http://localhost:8082/message",
  "sse_endpoint": "http://localhost:8082/sse",
  "description": "Example SSE MCP server"
}'

echo "[seed-examples] Registering stdio example server..."
STDIO_PATH="$(pwd)/bin/mcp-stdio-example"
api "{
  \"name\": \"example-stdio\",
  \"transport\": \"stdio\",
  \"command\": \"${STDIO_PATH}\",
  \"description\": \"Example stdio MCP server\"
}"

echo "[seed-examples] Done. Three example servers registered."
```

The stdio path is passed as an absolute path derived from `$(pwd)` at registration time. If mcpgate is run from a different working directory, the path must be updated manually (or set via `STDIO_PATH` env var).

---

## 4. Issues and Mitigations

These are the non-obvious problems to solve before and during implementation.

### Issue 1 — Stdio cannot be containerized (blocking design decision)

**Problem:** `transport/stdio.go` calls `exec.Command(command, args...)` which spawns a subprocess in the same OS process. You cannot spawn a subprocess in a different container — they have separate process namespaces.

**Options:**

| Option | Pros | Cons |
|--------|------|------|
| **A) Host binary (chosen)** — compile `bin/mcp-stdio-example`, register with absolute path | Mirrors real-world stdio MCP server usage (e.g. Claude Desktop spawning `npx`) | Path is host-specific; breaks if mcpgate itself runs in a container |
| B) Multi-stage mcpgate image — include stdio binary in the mcpgate Docker image | Works when mcpgate is containerized | Requires a mcpgate Dockerfile (doesn't exist yet); adds example binaries to the production image |
| C) Init container — a shared volume copies the stdio binary into a mount that both containers see | No mcpgate Dockerfile changes needed | Complex; volume mount must be set up before mcpgate starts |

**Recommendation:** Option A for Sprint 9 (development). Option B should be revisited in Sprint 10 when a production mcpgate Dockerfile is needed anyway. The seed script documents the assumption in a `# NOTE:` comment.

### Issue 2 — SSE `Send()` requires an inline SSE response, not plain JSON

**Problem:** The proxy's `Send()` in `transport/sse.go` branches on `Content-Type: text/event-stream`. If the POST /message handler responds with `application/json`, the SSE-specific code path (`readFirstSSEData`) is never exercised. The example server must return `text/event-stream` from POST /message to provide real coverage.

**Mitigation:** The SSE example server always responds to POST /message with `Content-Type: text/event-stream`. An integration test (`proxy_test.go`) should assert that the SSE path produces the correct tool result — not just that the request succeeds.

### Issue 3 — SSE `Stream()` path is exercised by a different proxy handler

**Problem:** The proxy has two separate handlers: `HandleStreamableHTTP` (POST /mcp/{name}/message) and `HandleSSEStream` (GET /mcp/{name}/sse). `HandleSSEStream` calls `tr.Stream()` via the SSE transport's `Stream()` method. In the current handler code, `HandleSSEStream` actually just opens the transport and waits for context cancellation — the message is sent separately via `HandleSSEMessage` (POST /mcp/{name}/sse).

**Mitigation:** Write an integration test that:
1. Opens a GET /mcp/example-sse/sse (exercising `HandleSSEStream` and the transport's upstream GET /sse).
2. Sends a POST /mcp/example-sse/sse with a `tools/list` request (exercising `HandleSSEMessage`).
3. Reads the SSE events from the GET stream (or the POST response, depending on how the handler routes the response).

This is more complex than the Streamable HTTP path and may reveal gaps in the current handler implementation.

### Issue 4 — Docker network hostname resolution for seed script

**Problem:** When mcpgate runs natively on the host and the example servers run in Docker containers, the endpoints are `localhost:8081` and `localhost:8082`. When mcpgate runs in a container on `mcpgate-net`, the endpoints must be `http://mcp-streamable:8081/mcp` and `http://mcp-sse:8082/message`.

The seed script is parameterised (`--base-url`) but the server endpoint URLs (the upstream MCP targets) are hardcoded to `localhost`. This breaks as soon as mcpgate is also containerized.

**Mitigation:** The seed script accepts `--upstream-host` (default: `localhost`). In CI or container environments, pass `--upstream-host mcp-streamable` etc. Document this in the script's header comment.

### Issue 5 — `initialize` handshake

**Problem:** A real MCP client sends `initialize` before any other method. The proxy does **not** call `initialize` — it proxies whatever the downstream client sends, including `initialize`. The health check sends `ping` directly.

The example servers must handle both `initialize` (returning valid capabilities) and `ping` (returning `{}`), and must handle them in any order (stateless dispatch, not a state machine).

**Mitigation:** The `common.Dispatch` function handles both methods independently with no state. The `initialize` response includes a standard capabilities map so any real MCP client connecting through the proxy will get a valid negotiation response.

### Issue 6 — `slow_echo` and the circuit breaker

**Problem:** `slow_echo` with a large `delay_ms` will trigger the proxy's 30-second timeout. If called repeatedly, the circuit breaker will open. This is intentional for integration testing, but must not interfere with other tests running against the same server instance.

**Mitigation:** `slow_echo` accepts a `delay_ms` parameter (not a fixed delay). Integration tests that use `slow_echo` should use short delays (e.g. 100ms) for timeout path coverage, and must reset the circuit breaker state (or use a separate server registration) for isolation.

### Issue 7 — Go module scope for examples

**Problem:** `go build ./...` from the repo root will attempt to build the example `main` packages, which could slow the build and add binary artifacts unexpectedly.

**Mitigation:** Use explicit build paths (`go build ./examples/...`) in `build-examples`. The standard `make build` target only builds `./cmd/mcpgate`. A `//go:build ignore` tag is NOT used (it would exclude the package from `go vet` and `golangci-lint`). Instead, the example packages are fully valid and linted normally — they are simply not built by default.

### Issue 8 — `.dockerignore` for the example server images

**Problem:** The Dockerfiles use `COPY . .` from the repo root as build context. Without a `.dockerignore`, every file in the repo is sent to the Docker daemon, including `node_modules/`, `bin/`, `.git/`, and the frontend build artifacts. This will make the build slow.

**Mitigation:** Add a `.dockerignore` at the repo root (or verify the existing one covers `web/node_modules`, `web/dist`, `bin/`, `.git/`).

---

## 5. File Inventory

### New files

| File | Purpose |
|------|---------|
| `examples/mcp-servers/common/handler.go` | Shared JSON-RPC dispatch + all method handlers |
| `examples/mcp-servers/streamable-http/main.go` | HTTP server: POST /mcp → JSON response |
| `examples/mcp-servers/streamable-http/Dockerfile` | Multi-stage build for the streamable server |
| `examples/mcp-servers/sse/main.go` | HTTP server: GET /sse + POST /message (SSE response) |
| `examples/mcp-servers/sse/Dockerfile` | Multi-stage build for the SSE server |
| `examples/mcp-servers/stdio/main.go` | Stdin/stdout JSON-RPC server |
| `examples/docker-compose.examples.yml` | docker-compose overlay for the two HTTP example servers |
| `examples/seed-example-servers.sh` | Admin API calls to register all three servers |

### Modified files

| File | Change |
|------|--------|
| `Makefile` | Add `build-examples` and `docker-up-examples` targets |
| `.dockerignore` | Ensure `web/node_modules`, `web/dist`, `bin/` are excluded from Docker build context |

---

## 6. Implementation Order

1. `examples/mcp-servers/common/handler.go` — write and verify all method handlers with a simple `go test` (no server needed)
2. `examples/mcp-servers/streamable-http/main.go` — simplest transport; verify with `curl`
3. `examples/mcp-servers/streamable-http/Dockerfile` + `examples/docker-compose.examples.yml`
4. `examples/mcp-servers/sse/main.go` — verify POST /message returns `text/event-stream`; verify GET /sse stays open
5. `examples/mcp-servers/sse/Dockerfile`
6. `examples/mcp-servers/stdio/main.go` — verify with `echo '{"jsonrpc":"2.0","id":1,"method":"ping"}' | ./bin/mcp-stdio-example`
7. `Makefile` updates
8. `examples/seed-example-servers.sh`
9. `.dockerignore` verification

---

## 7. Verification

### Quick smoke tests (no mcpgate running)

```bash
# Build all example binaries
make build-examples

# Streamable HTTP: start + curl
./bin/mcp-streamable-example &
curl -s -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"ping"}' | jq .
# → {"jsonrpc":"2.0","id":1,"result":{}}

curl -s -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | jq .result.tools[].name
# → "echo", "add", "slow_echo"

# SSE: POST /message returns text/event-stream
curl -sv -X POST http://localhost:8082/message \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
# Headers must include: Content-Type: text/event-stream
# Body must start with:  data: {"jsonrpc":"2.0","id":1,"result":...}

# Stdio: pipe JSON-RPC via stdin
echo '{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}' \
  | ./bin/mcp-stdio-example
# → {"jsonrpc":"2.0","id":1,"result":{"content":[{"type":"text","text":"hello"}]}}
```

### Integration tests (requires docker-up-examples + mcpgate running + seed run)

```bash
make docker-up-examples
make migrate-up
make build-examples
./bin/mcpgate serve &
MCPGATE_TOKEN=$(./scripts/login.sh) bash examples/seed-example-servers.sh

# Proxy round-trip via streamable
curl -s http://localhost:8080/mcp/example-streamable/message \
  -H "Authorization: Bearer $MCPGATE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .

# Proxy round-trip via SSE
curl -s -X POST http://localhost:8080/mcp/example-sse/sse \
  -H "Authorization: Bearer $MCPGATE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .

# Proxy round-trip via stdio
curl -s http://localhost:8080/mcp/example-stdio/message \
  -H "Authorization: Bearer $MCPGATE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .
```

---

## 8. Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Pure Go, same module | No new language runtimes; consistent with the codebase; single `go build` for all binaries |
| D2 | SSE POST /message responds with `text/event-stream` (not JSON) | Exercises `readFirstSSEData()` in the proxy; plain JSON would silently skip that code path |
| D3 | Stdio as host binary, not container | `exec.Command` requires a subprocess; a separate container cannot satisfy that. Option B (include in mcpgate image) deferred to Sprint 10 |
| D4 | `common/` package shared by all three transports | Eliminates copy-paste of MCP method handlers; the three servers differ only in their wire format |
| D5 | `slow_echo` tool with configurable delay | Enables timeout/circuit-breaker integration tests without hardcoded sleep values |
| D6 | Seed script uses admin REST API, not SQL | Consistent with how operators register servers; tests the full server CRUD path |
| D7 | `docker-compose.examples.yml` as an overlay, not merged into dev | Keeps the dev compose file minimal; operators who don't need example servers don't start them |
