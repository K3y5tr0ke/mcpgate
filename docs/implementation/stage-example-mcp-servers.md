# Example MCP Servers — Implementation Record

**Date:** 2026-05-27
**Status:** Complete
**Plan:** `docs/implementation/example-mcp-servers-plan.md`

---

## 1. What Was Delivered

Three standalone MCP servers sharing a common JSON-RPC dispatch package, one per supported transport:

| Server | Transport | Binary | Docker |
|--------|-----------|--------|--------|
| `examples/mcp-servers/streamable-http/` | Streamable HTTP | `bin/mcp-streamable-example` | `examples/mcp-servers/streamable-http/Dockerfile` |
| `examples/mcp-servers/sse/` | SSE | `bin/mcp-sse-example` | `examples/mcp-servers/sse/Dockerfile` |
| `examples/mcp-servers/stdio/` | stdio | `bin/mcp-stdio-example` | none (host binary only) |

Supporting files:

| File | Purpose |
|------|---------|
| `examples/mcp-servers/common/handler.go` | Shared JSON-RPC dispatch, all MCP method handlers |
| `examples/docker-compose.examples.yml` | Docker Compose overlay — adds `mcp-streamable` and `mcp-sse` to the dev stack |
| `examples/seed-example-servers.sh` | Registers all three servers via the mcpgate admin REST API |
| `Makefile` | New targets: `build-examples`, `docker-up-examples`, `seed-examples` |
| `.dockerignore` | Excludes `bin/`, `web/node_modules/`, `web/dist/`, `.git/`, `.env`, certs from Docker build context |

---

## 2. Plan Issues — Resolution Status

The plan identified 8 issues. All are resolved.

### Issue 1 — Stdio cannot be containerized

**Resolution: Option A (host binary).**

`stdio/main.go` reads from `os.Stdin` and writes to `os.Stdout`. There is no Dockerfile. The binary is compiled to `bin/mcp-stdio-example` via `make build-examples`. `seed-example-servers.sh` computes the absolute path to the binary using `REPO_ROOT="$(dirname "$SCRIPT_DIR")"` and registers it as `config.command`. If the binary is absent, the script prints a warning and skips the stdio registration (streamable and SSE still register).

**Sprint 10 note:** when mcpgate is containerized, the stdio binary will need to be bundled into the mcpgate Docker image.

---

### Issue 2 — SSE `Send()` requires an inline SSE response

**Resolution: implemented.**

`sse/main.go` `POST /message` handler always responds with `Content-Type: text/event-stream` and writes:

```
data: <json-rpc-response>\n\n
```

This exercises the proxy's `readFirstSSEData()` code path, which reads lines from the response body until it finds one starting with `data:`. Responding with `application/json` would silently bypass that path.

---

### Issue 3 — SSE `Stream()` path requires a separate proxy handler

**Resolution: GET /sse implemented; integration test is pending (future sprint).**

`sse/main.go` `GET /sse` sends an initial `event: connected\ndata: {}\n\n`, flushes, then blocks on `<-r.Context().Done()`. The `WriteTimeout` is set to `0` (disabled) for this server because the connection is intentionally held open indefinitely.

The proxy's `HandleSSEStream` path is exercised by issuing a `GET /mcp/{name}/sse` through the running mcpgate instance. A dedicated integration test covering this flow (open stream → send message → read response from stream) is deferred to Sprint 9's integration test pass.

---

### Issue 4 — Docker network hostname resolution for seed script

**Resolution: `--upstream-host` flag added.**

`seed-example-servers.sh` accepts:

```
--base-url <url>         mcpgate base URL (default: http://localhost:8080)
--upstream-host <host>   hostname for upstream MCP servers (default: localhost)
```

When mcpgate runs in a container on `mcpgate-net`, pass `--upstream-host mcp-streamable` (or the docker-compose service name). `docker-compose.examples.yml` joins `mcpgate-net` as an external network so the hostnames are resolvable from within the network.

---

### Issue 5 — `initialize` handshake

**Resolution: stateless dispatch handles both `initialize` and `initialized`.**

`common/handler.go` `Dispatch()` handles `initialize` at any point in the connection lifecycle (no state machine). The response includes the full capabilities block:

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

The `initialized` client lifecycle notification is handled as a method case returning `{}`. This is intentional: see §3 (Deviations) for why we respond to notifications rather than silently dropping them.

---

### Issue 6 — `slow_echo` and the circuit breaker

**Resolution: configurable delay with a 30,000 ms cap.**

`slow_echo` accepts `delay_ms` (number, optional). The delay is capped at 30,000 ms to prevent abuse in shared test environments. Integration tests that target the timeout path should use short delays (e.g. 100 ms) and use separate server registrations for isolation from other tests.

---

### Issue 7 — Go module scope for examples

**Resolution: examples are not built by default; linting is not excluded.**

`make build` builds only `./cmd/mcpgate`. `make build-examples` explicitly builds all three example binaries. The `//go:build ignore` tag was deliberately NOT used — all example packages are linted normally via `golangci-lint run ./...`.

All lint issues were resolved before merge:
- `errcheck`: all `fmt.Fprintf(w, ...)` calls use `_, _ =`
- `gosec G114`: both HTTP servers use `http.Server{...}` structs with timeouts instead of bare `http.ListenAndServe`

---

### Issue 8 — `.dockerignore` for example server images

**Resolution: `.dockerignore` created at repo root.**

Both Dockerfiles use `COPY . .` from the repo root build context. The `.dockerignore` excludes:

```
.git/
web/node_modules/
web/dist/
bin/
.env
certs/ *.pem *.key *.crt
.vscode/ .idea/ *.DS_Store
.cache/
```

---

## 3. Deviations from Plan

### 3.1 `Dispatch` signature — 3 return values, not 4

**Plan:** `Dispatch(id json.RawMessage, method string, params json.RawMessage) (any, int, string, bool)` where the `bool` indicated whether to write a response (false for notifications).

**Actual:** `Dispatch(method string, params json.RawMessage) (any, int, string)` with no `id` or `bool`.

**Rationale:** The stdio transport's `readLoop` always expects a response on stdout. If we silently drop a notification, the proxy's pending channel entry for that id (empty string `""` for absent-id messages) leaks and the call blocks until timeout. The solution is to always write a response — for the `initialized` notification case, return `{}` which is a valid, benign response that well-behaved MCP clients ignore. The `IsNotification()` method on `Request` is retained for server-side logging but does not gate response writing.

### 3.2 `initialized` returns `{}`, not silent drop

Follows directly from §3.1. The `initialized` case in `Dispatch()` returns `map[string]any{}` with error code 0. The response has the `id` field omitted (via `json:"id,omitempty"` on `json.RawMessage`) since notification requests have no `id`. The stdio transport pending lookup uses `""` as the key for absent-id messages and resolves correctly.

### 3.3 `prompts/get` — role is `"user"`, not `"system"`

**Plan:** prompt message role was listed as `"system"`.

**Actual:** `"role": "user"`.

**Rationale:** The MCP prompt message schema uses `"user"` and `"assistant"` as valid roles for message content arrays. `"system"` is not a valid MCP message role. The correction keeps the response spec-compliant.

### 3.4 Dockerfile improvements over plan's skeleton

The plan showed a minimal Dockerfile skeleton. The implementation adds:

- `COPY go.mod go.sum ./ && RUN go mod download` as a separate layer before `COPY . .` for better Docker build cache utilization (dependency layer is only invalidated when `go.mod` or `go.sum` changes).
- `RUN adduser -D -u 1000 mcp && USER mcp` so the container process runs as a non-root user (gosec best practice).
- `golang:1.26-alpine` and `alpine:3.21` — updated from the plan's `golang:1.25-alpine`/`alpine:3.20` to match the project's Go version.
- `-ldflags="-s -w"` to strip debug info from the binary.

### 3.5 `docker-compose.examples.yml` build context is `..`, not `../..`

**Plan:** `context: ../..`

**Actual:** `context: ..`

**Rationale:** The compose file lives at `examples/docker-compose.examples.yml`. Docker Compose resolves relative paths from the directory containing the compose file. `..` from `examples/` is the repo root. `../..` would be the parent of the repo root — wrong.

### 3.6 SSE healthcheck verifies response content

**Plan:** wget healthcheck checked only HTTP status.

**Actual:** `grep -q '"result"'` is appended to verify the response body contains a result key, not just a 200 status. A server that returns HTTP 200 with `{"error": ...}` would fail the healthcheck.

---

## 4. Key Implementation Details

### 4.1 `common/handler.go`

The shared package is the only place that knows about MCP method semantics. The three `main.go` files contain no business logic.

**`Request` struct:**

```go
type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"`
    Method  string          `json:"method,omitempty"`
    Params  json.RawMessage `json:"params,omitempty"`
}
```

`json:"id,omitempty"` on `json.RawMessage` means a nil/absent id is omitted from the marshalled response — no `"id": null`. This is the key property that makes the stdio transport's pending-key lookup work for notification responses.

**Response builder:**

```go
type resp struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      json.RawMessage `json:"id,omitempty"`
    Result  any             `json:"result,omitempty"`
    Error   *rpcErr         `json:"error,omitempty"`
}
```

Parse errors use `json.RawMessage("null")` as the id (literal JSON `null`) per JSON-RPC spec — used when the incoming bytes can't be decoded and the original id is unknown.

**Supported methods:**

| Method | Returns |
|--------|---------|
| `initialize` | `protocolVersion`, `capabilities`, `serverInfo` |
| `initialized` | `{}` (notification — response silently ignored by clients) |
| `ping` | `{}` |
| `tools/list` | 3 tools: `echo`, `add`, `slow_echo` |
| `tools/call` | `content` array |
| `resources/list` | 1 resource: `file://greeting.txt` |
| `resources/read` | text content for `file://greeting.txt`; -32602 for unknown URI |
| `prompts/list` | 1 prompt: `system-prompt` |
| `prompts/get` | rendered messages for `system-prompt`; -32602 for unknown name |
| anything else | -32601 method not found |

### 4.2 Streamable HTTP server

```
POST /mcp
  ← Content-Type: application/json
  → Content-Type: application/json
  → JSON-RPC response body
```

`ReadTimeout: 30s`, `WriteTimeout: 30s`. Single stateless request/response per POST.

### 4.3 SSE server

```
POST /message
  ← Content-Type: application/json
  → Content-Type: text/event-stream
  → data: <json-rpc-response>\n\n

GET /sse
  → Content-Type: text/event-stream
  → event: connected\ndata: {}\n\n
  → (holds open until client disconnects)
```

`ReadTimeout: 30s`, `WriteTimeout: 0` (disabled — GET /sse streams indefinitely).

`http.Flusher` is checked before the GET /sse handler proceeds; returns 500 if the response writer doesn't support flushing.

### 4.4 Stdio server

Reads newline-terminated JSON-RPC from `os.Stdin` via `bufio.Scanner` with a 1 MiB buffer (to handle large tool arguments). Writes newline-terminated responses to `os.Stdout`. All diagnostic output goes to `os.Stderr` only.

Parse failures write a `-32700` error response (not silent drop) so the proxy's pending channel is unblocked.

---

## 5. Smoke Tests

```bash
# Build all example binaries
make build-examples

# --- Streamable HTTP ---
./bin/mcp-streamable-example &

curl -s -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"ping"}' | jq .
# → {"jsonrpc":"2.0","id":1,"result":{}}

curl -s -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | jq '.result.tools[].name'
# → "echo"
# → "add"
# → "slow_echo"

curl -s -X POST http://localhost:8081/mcp \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"add","arguments":{"a":3,"b":4}}}' | jq .
# → {"jsonrpc":"2.0","id":3,"result":{"content":[{"text":"7","type":"text"}]}}

kill %1

# --- SSE ---
./bin/mcp-sse-example &

# POST /message must return text/event-stream
curl -sv -X POST http://localhost:8082/message \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' 2>&1 | grep -E "Content-Type|^data:"
# Content-Type: text/event-stream
# data: {"jsonrpc":"2.0","id":1,"result":{"tools":[...]}}

# GET /sse holds the connection open
curl -N http://localhost:8082/sse &
# → event: connected
# → data: {}
kill %2 %3

# --- Stdio ---
echo '{"jsonrpc":"2.0","id":1,"method":"ping"}' | ./bin/mcp-stdio-example
# → {"jsonrpc":"2.0","id":1,"result":{}}

echo '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello"}}}' \
  | ./bin/mcp-stdio-example
# → {"jsonrpc":"2.0","id":2,"result":{"content":[{"text":"hello","type":"text"}]}}

# Notification — response has no "id" field
echo '{"jsonrpc":"2.0","method":"initialized"}' | ./bin/mcp-stdio-example
# → {"jsonrpc":"2.0","result":{}}
```

---

## 6. Integration with mcpgate

```bash
# Start dev stack + example servers
make docker-up-examples
make migrate-up

# Build and start mcpgate
make build-examples
make dev &

# Get an admin token
TOKEN=$(curl -s -X POST http://localhost:8080/auth/local/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@mcpgate.local","password":"<password>"}' | jq -r .token)

# Register all three servers
MCPGATE_TOKEN=$TOKEN bash examples/seed-example-servers.sh

# Proxy round-trip via streamable HTTP
curl -s -X POST http://localhost:8080/mcp/example-streamable/message \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .

# Proxy round-trip via SSE
curl -s -X POST http://localhost:8080/mcp/example-sse/sse \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .

# Proxy round-trip via stdio
curl -s -X POST http://localhost:8080/mcp/example-stdio/message \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .
```

When running example servers in containers and mcpgate on the host, pass `--upstream-host localhost` (default). When both run in containers on `mcpgate-net`, pass `--upstream-host mcp-streamable` for the streamable server and `--upstream-host mcp-sse` for the SSE server.
