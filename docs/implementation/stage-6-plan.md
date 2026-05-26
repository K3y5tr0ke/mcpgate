# Stage 6 Implementation Plan — Metrics, Tracing, Rate Limiting & Circuit Breakers
**Date:** 2026-05-25  
**Sprint:** 6 (Observability + Resilience)  
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/mcpgate-architecture-blueprint.md`  
**Predecessor:** `docs/implementation/stage-5-25-05-2026.md`

---

## 1. Context

Sprints 1–5 delivered the full request pipeline: proxy engine, SAML + M2M authentication, RBAC enforcement, and a tamper-evident audit log. Every request is authenticated, authorized, and recorded. The system is functionally complete but operationally blind — there are no runtime metrics, no distributed traces, no rate limits on the hot path, and no protection against cascading failures when upstream MCP servers become unhealthy.

Sprint 6 closes all four gaps and folds in six carry-forward items from Sprint 5 that are small enough to fit without displacing primary work.

**End state:** a running mcpgate instance exposes a Prometheus `/metrics` endpoint on port 9090, instruments every proxied request with an OpenTelemetry span (exported via OTLP when configured), rejects callers exceeding per-user rate limits with a standards-compliant 429, and shields itself from slow upstream servers using a per-server circuit breaker.

---

## 2. What Already Exists

| Artifact | Location | State |
|----------|----------|-------|
| `RateLimiter` interface | `internal/store/interfaces.go:78` | Defined; no implementation |
| `RateLimit`, `RateLimitResult` structs | `internal/store/interfaces.go:211–222` | Defined |
| `RateLimitingConfig`, `RateLimitDefaults` | `internal/config/config.go` | Defined; not wired |
| `CircuitBreakerConfig` | `internal/config/config.go` | Defined; not wired |
| `MetricsConfig`, `OTLPConfig` | `internal/config/config.go` | Defined; not wired |
| `Server.MetricsPort` | `internal/config/config.go` | Configured (default 9090) |
| OTel packages (transitive) | `go.mod` | Indirect; `otel v1.41.0`, `otelhttp v0.60.0` |
| `proxy.Handler` | `internal/proxy/handler.go` | No CB hooks or trace spans |
| `proxy.MessageFromContext` | `internal/proxy/context.go` | Available for metric labelling |
| `auth.IdentityFromContextOrZero` | `internal/auth/context.go` | Available for metric labelling |
| Audit pipeline `DroppedCount()` | `internal/logging/pipeline.go` | Available; not exposed as metric |
| `/health`, `/ready` endpoints | `internal/server/server.go` | Exist; return flat JSON |

No `internal/metrics/`, `internal/tracing/`, `internal/ratelimit/`, or `internal/proxy/circuitbreaker/` packages exist yet.

---

## 3. Carry-Forward Items In Scope

Six items from Sprint 5 are bundled into Sprint 6. They are small and self-contained.

| Label | Item | Source |
|-------|------|--------|
| CF1 | WAL replay atomicity — atomic rename to prevent double-insert on crash | Stage 5 §16 |
| CF2 | `ClientIP` INET suffix — replace `::text` cast with `host()` SQL function | Stage 5 §16 |
| CF3 | Hash chain tamper-detection API — `GET /api/v1/admin/logs/verify` | Stage 5 §16 |
| CF4 | Log retention enforcement — daily DELETE goroutine using `RetentionConfig.HotDays` | Stage 5 §16 |
| CF5 | WS stream auth — verify middleware order, document cookie pattern, add `?token=` note | Stage 5 §16 |
| CF6 | Proxy transport alias — accept `"http"` as alias for `"streamable_http"` in `buildTransport` | Observed in E2E tests |

---

## 4. New Package Structure

```
internal/
├── metrics/
│   ├── collector.go          Collector: prometheus registrations, Handler, Middleware
│   └── collector_test.go     Unit: counter increments, histogram observations, middleware label extraction
│
├── tracing/
│   ├── provider.go           Provider: New, Middleware, Shutdown; no-op when OTLP disabled
│   └── provider_test.go      Unit: span creation, attribute propagation, no-op path
│
├── ratelimit/
│   ├── middleware.go         Middleware(limiter, cfg, logger): RPM + RPD per user, 429 + JSON-RPC -32000
│   └── middleware_test.go    Unit: mock limiter — allow path, RPM exceeded, RPD exceeded, limiter error (fail open)
│
└── proxy/
    └── circuitbreaker/
        ├── breaker.go        CircuitBreaker: Allow, RecordSuccess, RecordFailure, State; atomic state machine
        └── breaker_test.go   Unit: transitions closed→open→half-open→closed, half-open failure→open, concurrent

internal/store/redis/
└── rate_limiter.go           RateLimiter Redis sliding window via Lua script
    rate_limiter_test.go      Integration: allow under limit, deny at limit, RPM/RPD independence, expiry

internal/store/postgres/
└── audit_logs.go             + DeleteExpired (CF4), host(client_ip) fix (CF2)
```

Files modified:

| File | Change |
|------|--------|
| `internal/proxy/handler.go` | CB registry (`sync.Map` per server), wrap `transportFor` with Allow/Record |
| `internal/logging/sinks/wal.go` | `Replay` atomic rename (CF1) |
| `internal/admin/api/logs.go` | `verify` handler (CF3) |
| `internal/admin/api/router.go` | wire `GET /logs/verify` |
| `internal/store/interfaces.go` | `AuditLogStore` gains `DeleteExpired(ctx, hotDays int) (int64, error)` (CF4) |
| `internal/server/server.go` | metrics server (port 9090), tracing middleware, rate limit middleware, log retention goroutine (CF4), enhanced `/ready` |
| `go.mod` / `go.sum` | `prometheus/client_golang`, promote OTel to direct |

---

## 5. New Dependencies

```bash
go get github.com/prometheus/client_golang@v1.22.0
go get go.opentelemetry.io/otel/sdk@v1.41.0
go get go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc@v1.41.0
go get go.opentelemetry.io/otel/propagation@v1.41.0
go get go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp@v0.60.0
```

The last four packages are already indirect dependencies (pulled in by testcontainers-go); promoting them to direct is a `go.mod` metadata change only — no new code is downloaded.

---

## 6. Implementation Details

### 6.1 Metrics (`internal/metrics/collector.go`)

#### Registered metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `mcpgate_requests_total` | Counter | `server`, `method`, `status`, `auth_type` | Total proxied MCP requests |
| `mcpgate_request_duration_seconds` | Histogram | `server`, `method`, `status` | End-to-end latency from middleware entry to handler return |
| `mcpgate_active_requests` | Gauge | `server` | In-flight requests |
| `mcpgate_rate_limit_hits_total` | Counter | `window` | Requests rejected by rate limiter (`rpm` or `rpd`) |
| `mcpgate_circuit_breaker_state` | Gauge | `server` | `0`=closed, `1`=open, `2`=half-open |
| `mcpgate_upstream_errors_total` | Counter | `server`, `error_type` | Upstream transport errors (timeout, connection_refused, etc.) |
| `mcpgate_auth_failures_total` | Counter | `reason` | Auth rejections (expired, invalid, no_session, no_token) |
| `mcpgate_audit_pipeline_dropped_total` | Gauge | — | `pipeline.DroppedCount()` sampled at scrape time via `prometheus.NewGaugeFunc` |

All metrics use a dedicated `prometheus.Registry` (not the default global registry) to avoid polluting tests. The `Handler()` method returns `promhttp.HandlerFor(registry, promhttp.HandlerOpts{})`.

#### `Collector` struct

```go
type Collector struct {
    RequestsTotal   *prometheus.CounterVec
    RequestDuration *prometheus.HistogramVec
    ActiveRequests  *prometheus.GaugeVec
    RateLimitHits   *prometheus.CounterVec
    CircuitState    *prometheus.GaugeVec
    UpstreamErrors  *prometheus.CounterVec
    AuthFailures    *prometheus.CounterVec
    registry        *prometheus.Registry
}

func NewCollector() *Collector
func (c *Collector) Handler() http.Handler
func (c *Collector) Middleware() func(http.Handler) http.Handler

// RegisterPipelineGauge wires pipeline.DroppedCount() as a GaugeFunc.
// Called from server.New() after both Collector and Pipeline are created.
func (c *Collector) RegisterPipelineGauge(droppedFn func() int64)
```

#### Metrics middleware

```go
func (c *Collector) Middleware() func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            server := chi.URLParam(r, "serverName")
            c.ActiveRequests.WithLabelValues(server).Inc()
            defer c.ActiveRequests.WithLabelValues(server).Dec()

            ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
            start := time.Now()
            next.ServeHTTP(ww, r)

            method := ""
            if msg := proxy.MessageFromContext(r.Context()); msg != nil {
                method = msg.Method
            }
            authType := "session"
            if identity := auth.IdentityFromContextOrZero(r.Context()); identity.SessionID == uuid.Nil {
                authType = "m2m"
            }
            status := statusLabel(ww.Status())
            elapsed := time.Since(start).Seconds()

            c.RequestsTotal.WithLabelValues(server, method, status, authType).Inc()
            c.RequestDuration.WithLabelValues(server, method, status).Observe(elapsed)
        })
    }
}
```

The `Middleware()` method is wired on the MCP proxy route group only (not admin routes). It reads `serverName` from the chi URL param, so it is placed after the chi router resolves the route.

`statusLabel` maps HTTP status codes to coarse strings (`"2xx"`, `"4xx"`, `"5xx"`) to keep cardinality bounded.

#### Metrics server

The Prometheus endpoint runs on a dedicated `net.Listener` on `cfg.Server.MetricsPort`. It is started in `server.New()` alongside the pipeline and background goroutines:

```go
metricsMux := http.NewServeMux()
metricsMux.Handle("/metrics", collector.Handler())
metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
    w.WriteHeader(http.StatusOK)
})
metricsHTTP := &http.Server{Handler: metricsMux}
ln, _ := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.MetricsPort))
go metricsHTTP.Serve(ln) //nolint:errcheck
```

The metrics server is stopped in `Shutdown()` via `metricsHTTP.Close()`. It is intentionally separate from the main API server so operators can firewall port 9090 without affecting port 8080.

---

### 6.2 Tracing (`internal/tracing/provider.go`)

#### Design

The `Provider` wraps an `*sdktrace.TracerProvider`. When `cfg.Metrics.OTLP.Enabled == false`, a no-op `trace.TracerProvider` is used — spans are created (so trace context is propagated), but nothing is exported. This means code downstream of the tracing middleware can always call `trace.SpanFromContext(ctx)` safely.

```go
type Provider struct {
    tp     *sdktrace.TracerProvider // nil when no-op
    tracer trace.Tracer
}

func New(cfg config.OTLPConfig, serviceName string) (*Provider, error)
func (p *Provider) Middleware() func(http.Handler) http.Handler
func (p *Provider) Shutdown(ctx context.Context) error
```

#### `New`

```go
func New(cfg config.OTLPConfig, serviceName string) (*Provider, error) {
    if !cfg.Enabled {
        otel.SetTracerProvider(trace.NewNoopTracerProvider())
        return &Provider{tracer: otel.Tracer(serviceName)}, nil
    }

    conn, err := grpc.NewClient(cfg.Endpoint, grpcOpts(cfg)...)
    if err != nil {
        return nil, fmt.Errorf("tracing: dial OTLP: %w", err)
    }
    exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
    ...
    tp := sdktrace.NewTracerProvider(
        sdktrace.WithBatcher(exporter),
        sdktrace.WithResource(resource.NewWithAttributes(
            semconv.SchemaURL,
            semconv.ServiceName(serviceName),
        )),
        sdktrace.WithSampler(sdktrace.AlwaysSample()),
    )
    otel.SetTracerProvider(tp)
    otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
        propagation.TraceContext{},
        propagation.Baggage{},
    ))
    return &Provider{tp: tp, tracer: tp.Tracer(serviceName)}, nil
}
```

`grpcOpts` returns `grpc.WithTransportCredentials(insecure.NewCredentials())` when `cfg.Insecure == true`, and TLS credentials otherwise. This handles the common case of a local Jaeger/Tempo collector without TLS.

#### Middleware

```go
func (p *Provider) Middleware() func(http.Handler) http.Handler {
    return otelhttp.NewMiddleware("mcpgate",
        otelhttp.WithTracerProvider(otel.GetTracerProvider()),
        otelhttp.WithPropagators(otel.GetTextMapPropagator()),
        otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string {
            // Use route pattern, not URL (avoids high-cardinality span names)
            if rctx := chi.RouteContext(r.Context()); rctx != nil && rctx.RoutePattern() != "" {
                return r.Method + " " + rctx.RoutePattern()
            }
            return r.Method + " " + r.URL.Path
        }),
    )
}
```

The `otelhttp` middleware is wired globally (applied to all routes via the root chi router), after `chimw.RequestID` and `chimw.Recoverer`. This ensures all requests — including health checks, SAML routes, and auth failures — produce spans.

MCP-specific attributes (`mcp.server`, `mcp.method`, `rbac.decision`) are set by a separate span-enrichment step in `logging/middleware.go` after the message is parsed:

```go
// In logging/middleware.go, after proxy.Parse():
if span := trace.SpanFromContext(r.Context()); span.IsRecording() {
    span.SetAttributes(
        attribute.String("mcp.server", chi.URLParam(r, "serverName")),
        attribute.String("mcp.method", msg.Method),
    )
}
// After handler returns, add RBAC decision:
span.SetAttributes(attribute.String("rbac.decision", entry.RBACDecision))
```

This avoids the tracing package importing the logging or proxy packages.

#### `Shutdown`

Called from `server.Shutdown()` with a 5-second timeout to flush the OTLP batch exporter:

```go
func (p *Provider) Shutdown(ctx context.Context) error {
    if p.tp == nil {
        return nil // no-op provider
    }
    return p.tp.Shutdown(ctx)
}
```

---

### 6.3 Rate Limiting

#### Redis implementation (`internal/store/redis/rate_limiter.go`)

```go
type RateLimiter struct {
    client *redis.Client
}

func NewRateLimiter(client *redis.Client) *RateLimiter
func (r *RateLimiter) Allow(ctx context.Context, key string, limit store.RateLimit) (store.RateLimitResult, error)
```

`Allow` checks `RPM` (requests per minute) first, then `RPD` (requests per day). It calls a Lua script for each window that needs checking. If either window denies the request, `Allowed: false` is returned with the most restrictive `RetryAfter`.

The Lua script implements an atomic sliding window using a sorted set. Members are random UUIDs (to avoid collisions), scores are Unix milliseconds:

```lua
-- KEYS[1]: sorted set key
-- ARGV[1]: now_ms (current time in milliseconds)
-- ARGV[2]: window_ms (window size in milliseconds)
-- ARGV[3]: limit (integer)
-- ARGV[4]: member (random UUID for this request)
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

-- Remove entries outside the sliding window
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window)
-- Count entries within the window
local count = redis.call('ZCARD', key)
if count < limit then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window)
    return {1, limit - count - 1, 0}   -- allowed, remaining, retry_after_ms
else
    -- Oldest entry + window gives reset time
    local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
    local reset_ms = 0
    if #oldest > 0 then
        reset_ms = tonumber(oldest[2]) + window - now
    end
    return {0, 0, reset_ms}  -- denied, remaining, retry_after_ms
end
```

The script is loaded with `redis.ScriptLoad` at construction time and executed with `redis.EvalSha` for efficiency.

**Key scheme:**
- Per-user RPM: `ratelimit:rpm:<userID>`
- Per-user RPD: `ratelimit:rpd:<userID>`
- `ConcurrentConnections` is enforced separately via an `INCR` / `DECR` pair on a Redis counter with TTL equal to the request timeout.

**Error behaviour:** If the Redis call fails (network partition, timeout), `Allow` returns `(RateLimitResult{Allowed: true}, err)`. The middleware logs the error and fails open — a brief Redis outage should not deny all requests.

#### Middleware (`internal/ratelimit/middleware.go`)

```go
func Middleware(
    limiter store.RateLimiter,
    cfg config.RateLimitingConfig,
    metrics *metrics.Collector,
    logger *slog.Logger,
) func(http.Handler) http.Handler
```

Middleware logic:
1. If `!cfg.Enabled`, call `next` immediately.
2. `identity := auth.MustIdentity(ctx)` — rate limit is always per authenticated user.
3. Check RPM: `limiter.Allow(ctx, "rpm:"+identity.UserID.String(), RateLimit{RPM: cfg.Default.RPM, ...})`
4. If denied: `metrics.RateLimitHits.WithLabelValues("rpm").Inc()`, respond 429, return.
5. Check RPD: `limiter.Allow(ctx, "rpd:"+identity.UserID.String(), RateLimit{RPD: cfg.Default.RPD, ...})`
6. If denied: `metrics.RateLimitHits.WithLabelValues("rpd").Inc()`, respond 429, return.
7. Call `next.ServeHTTP(w, r)`.

The 429 response for MCP routes (`/mcp/*`) must be a valid JSON-RPC error (callers expect JSON-RPC on that path):

```json
{
  "jsonrpc": "2.0",
  "id": null,
  "error": {
    "code": -32000,
    "message": "rate limit exceeded",
    "data": {"retry_after_seconds": 42}
  }
}
```

`Retry-After` is also set as an HTTP header for HTTP-layer clients.

The middleware reads the parsed message from context (`proxy.MessageFromContext`) to include the request ID in the JSON-RPC error response, avoiding `"id": null` when the message ID is known.

**Wiring position:** After `authMiddleware` (identity is required), after `logging.Middleware` (message is in context), before `rbac.Authorize` (no point enforcing RBAC on a rate-limited request):

```go
r.Route("/mcp/{serverName}", func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(logging.Middleware(s.pipeline, s.serverStore))
    r.Use(s.metricsCollector.Middleware())      // ← new
    r.Use(ratelimit.Middleware(s.rateLimiter, s.cfg.RateLimiting, s.metricsCollector, s.logger)) // ← new
    r.Use(rbac.Authorize(s.enforcer, s.logger))
    r.Post("/message", s.proxyHandler.HandleStreamableHTTP)
    r.Get("/sse", s.proxyHandler.HandleSSEStream)
    r.Post("/sse", s.proxyHandler.HandleSSEMessage)
})
```

---

### 6.4 Circuit Breaker (`internal/proxy/circuitbreaker/breaker.go`)

#### State machine

```
         failures >= threshold
Closed ───────────────────────► Open
  ▲                               │
  │     all half-open succeed     │ recovery timeout elapsed
  │                               ▼
  └──────────────────────── Half-Open ──► Open (on any failure)
```

```go
type State int32

const (
    StateClosed   State = 0
    StateOpen     State = 1
    StateHalfOpen State = 2
)

type CircuitBreaker struct {
    state         atomic.Int32
    failures      atomic.Int32  // consecutive failures in Closed state
    halfOpenCount atomic.Int32  // successful requests in HalfOpen state
    openedAt      atomic.Int64  // UnixNano when entered Open state
    cfg           config.CircuitBreakerConfig
}

func New(cfg config.CircuitBreakerConfig) *CircuitBreaker
```

#### `Allow() bool`

```
if state == Closed:   return true
if state == Open:
    if time.Since(openedAt) >= RecoveryTimeout:
        CAS Closed → HalfOpen   (only first goroutine wins)
        return true              (optimistic: let one request through)
    else:
        return false             (fast-fail)
if state == HalfOpen:
    if halfOpenCount < HalfOpenMaxRequests:
        return true
    else:
        return false
```

`Allow()` uses compare-and-swap (`atomic.CompareAndSwapInt32`) for the Open→HalfOpen transition so only the first goroutine after the recovery timeout enters HalfOpen; concurrent goroutines during the recovery window are still rejected.

#### `RecordSuccess()` and `RecordFailure()`

```
RecordSuccess():
    if state == HalfOpen:
        halfOpenCount.Add(1)
        if halfOpenCount.Load() >= HalfOpenMaxRequests:
            CAS HalfOpen → Closed
            failures.Store(0)
            halfOpenCount.Store(0)
    else: // Closed
        failures.Store(0)  // reset consecutive failure count on success

RecordFailure():
    if state == HalfOpen:
        CAS HalfOpen → Open
        openedAt.Store(time.Now().UnixNano())
        halfOpenCount.Store(0)
    else: // Closed
        n := failures.Add(1)
        if n >= FailureThreshold:
            CAS Closed → Open
            openedAt.Store(time.Now().UnixNano())
```

All transitions use CAS to be safe under concurrent goroutines. `State() State` exposes the current state for health checks and metrics.

#### Integration in `proxy.Handler`

`Handler` gains a `breakers sync.Map` (server name → `*CircuitBreaker`) and a `metrics *metrics.Collector` field. `transportFor` wraps the upstream call:

```go
func (h *Handler) callWithCircuitBreaker(
    ctx context.Context,
    serverName string,
    fn func() ([]byte, error),
) ([]byte, error) {
    cb := h.circuitBreakerFor(serverName)
    if !cb.Allow() {
        if h.metrics != nil {
            h.metrics.CircuitState.WithLabelValues(serverName).Set(float64(circuitbreaker.StateOpen))
        }
        return nil, fmt.Errorf("circuit open for server %q", serverName)
    }
    resp, err := fn()
    if err != nil {
        cb.RecordFailure()
        if h.metrics != nil {
            h.metrics.UpstreamErrors.WithLabelValues(serverName, classifyError(err)).Inc()
        }
    } else {
        cb.RecordSuccess()
    }
    if h.metrics != nil {
        h.metrics.CircuitState.WithLabelValues(serverName).Set(float64(cb.State()))
    }
    return resp, err
}
```

`circuitBreakerFor` uses `LoadOrStore` to create breakers lazily per server — no startup cost for servers that are never used.

When the circuit is open, `HandleStreamableHTTP` returns HTTP 502 with a JSON-RPC internal error (`-32603`). The `Retry-After` header is set to the remaining recovery timeout duration.

---

### 6.5 Enhanced Health Check

The existing `/ready` endpoint is extended to return component-level status:

```json
{
  "status": "ok",
  "components": {
    "database": { "status": "ok",       "latency_ms": 2 },
    "redis":    { "status": "ok",       "latency_ms": 1 },
    "pipeline": { "status": "ok",       "dropped_total": 0 },
    "upstreams": {
      "my-server":    { "status": "ok",       "circuit_state": "closed" },
      "other-server": { "status": "degraded", "circuit_state": "open"   }
    }
  }
}
```

Overall `"status"` is `"ok"` if all components are ok, `"degraded"` if any non-critical component (upstream, pipeline) is unhealthy, `"error"` if a critical component (database, Redis) is down.

`/health` remains a simple `{"status": "ok"}` liveness probe (no I/O, always 200 while the process is alive).

New aliases are added for Kubernetes probe compatibility:
- `GET /healthz` → same as `/health` (liveness)
- `GET /readyz` → same as `/ready` (readiness)

---

### 6.6 Carry-Forward Items

#### CF1 — WAL Replay Atomicity (`internal/logging/sinks/wal.go`)

**Problem:** Current `Replay` reads entries → calls `primary.Write` → truncates file. If the process crashes between Write and Truncate, the same entries are replayed again on the next start, producing duplicate rows in `audit_logs`.

**Fix:** Use an atomic rename pattern:
1. `os.Rename(path, path+".replaying")` — atomic on POSIX; if this succeeds, the original file is gone.
2. Read entries from `path + ".replaying"`.
3. Call `primary.Write`.
4. On success: `os.Remove(path + ".replaying")`.
5. On failure: `os.Rename(path+".replaying", path)` — restore for next attempt.

On startup (`server.New`), if `path + ".replaying"` exists, rename it back to `path` before starting the replay goroutine (crash recovery: the previous Write may or may not have succeeded — replay conservatively):

```go
if _, err := os.Stat(walPath + ".replaying"); err == nil {
    _ = os.Rename(walPath+".replaying", walPath)
    logger.Warn("wal: found .replaying file on startup; previous replay may have partially committed — replaying again")
}
```

This is still not perfectly idempotent (a crash after `primary.Write` but before `Remove` will produce duplicates), but it reduces the window from "truncate after any write" to "remove only after confirmed success". True idempotency would require a `UNIQUE` constraint on `hash_chain` and `INSERT ... ON CONFLICT DO NOTHING`, which is left as a future improvement.

#### CF2 — ClientIP INET Suffix (`internal/store/postgres/audit_logs.go`)

Replace every occurrence of `client_ip::text` in SQL queries with `host(client_ip)`. The `host()` PostgreSQL function returns the IP address without the prefix length (e.g., `127.0.0.1` instead of `127.0.0.1/32`).

Affected queries: the `SELECT` in `Query`, `Export`, and `VerifyHashChain`.

Remove the `strings.SplitN(*clientIP, "/", 2)[0]` workaround from `scanAuditEntry`. The comment explaining the fix is also removed.

No migration is needed — the `INET` column type is unchanged; only the SQL cast function changes.

#### CF3 — Hash Chain Tamper-Detection API (`internal/admin/api/logs.go`)

Add `verify` method to `logHandler`:

```go
// verify handles GET /api/v1/admin/logs/verify?from=...&to=...&genesis=...
func (h *logHandler) verify(w http.ResponseWriter, r *http.Request) {
    q := r.URL.Query()

    from, err := time.Parse(time.RFC3339, q.Get("from"))
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid 'from' timestamp (RFC3339 required)")
        return
    }
    to, err := time.Parse(time.RFC3339, q.Get("to"))
    if err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid 'to' timestamp (RFC3339 required)")
        return
    }
    if from.After(to) {
        writeJSONError(w, http.StatusBadRequest, "'from' must be before 'to'")
        return
    }
    genesis := q.Get("genesis") // empty string is a valid (though weak) genesis hash

    ok, err := h.store.VerifyHashChain(r.Context(), from, to, genesis)
    if err != nil {
        h.logger.Error("verify hash chain failed", "err", err)
        writeJSONError(w, http.StatusInternalServerError, "verification failed")
        return
    }
    writeJSON(w, http.StatusOK, map[string]any{
        "ok":   ok,
        "from": from,
        "to":   to,
    })
}
```

Route: `r.Get("/logs/verify", lh.verify)` in `router.go`, under the admin + `RequireRole("admin")` group. Requires the admin role — a non-admin cannot trigger chain verification.

**Security note:** The `genesis` parameter is caller-supplied. An admin supplying the wrong genesis hash will get `ok: false`, not a security bypass. The actual verification logic cannot be tricked by an incorrect genesis — it either matches the stored hashes or it does not.

#### CF4 — Log Retention Enforcement (`internal/store/postgres/audit_logs.go` + `internal/server/server.go`)

Add to `AuditLogStore` interface:

```go
// AuditLogStore interface addition:
DeleteExpired(ctx context.Context, hotDays int) (int64, error)
```

Implementation:

```go
func (s *AuditLogStore) DeleteExpired(ctx context.Context, hotDays int) (int64, error) {
    tag, err := s.db.Exec(ctx,
        `DELETE FROM audit_logs WHERE timestamp < now() - ($1 || ' days')::interval`,
        hotDays,
    )
    if err != nil {
        return 0, fmt.Errorf("audit_logs: delete expired: %w", err)
    }
    return tag.RowsAffected(), nil
}
```

The interval cast `($1 || ' days')::interval` avoids parameter type ambiguity with `INTERVAL` literals. `RowsAffected()` is used for the log message.

Background goroutine in `server.New()` (only if `HotDays > 0`):

```go
if cfg.Logging.Retention.HotDays > 0 {
    go func() {
        ticker := time.NewTicker(24 * time.Hour)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                n, err := auditStore.DeleteExpired(bgCtx, cfg.Logging.Retention.HotDays)
                if err != nil {
                    logger.Warn("audit log retention cleanup failed", "err", err)
                } else if n > 0 {
                    logger.Info("audit log retention cleanup", "deleted", n, "hot_days", cfg.Logging.Retention.HotDays)
                }
            case <-bgCtx.Done():
                return
            }
        }
    }()
}
```

The goroutine is stopped by `bgCancel()` in `Shutdown()`. The first tick is after 24 hours — there is no immediate deletion at startup, which is appropriate for a data-retention operation.

#### CF5 — WS Stream Authentication

**Verification:** chi evaluates all `r.Use(...)` middleware before calling the handler. The `RequireRole("admin")` middleware is registered on the admin route group, so it runs before `stream` is called and before `wsUpgrader.Upgrade`. If the role check fails, the middleware writes a 403 and returns — the WebSocket upgrade never happens. This is the correct behaviour.

**Browser WS clients:** Browsers cannot set `Authorization` headers on WebSocket upgrade requests (`new WebSocket(url)` does not accept headers). The mcpgate auth middleware already falls back to the `mcpgate_session` cookie (set by the SAML ACS handler). Browser clients that authenticate via SAML will have this cookie set and the auth middleware will validate it transparently.

**Action:** Add a comment to the `stream` handler documenting this behaviour. No code change is required. A `?token=` URL parameter is explicitly **not** added — URL parameters appear in server access logs and proxy logs, leaking the bearer token. The cookie path is the secure alternative.

#### CF6 — Proxy Transport Type Alias

`proxy.Handler.buildTransport` currently fails silently for the transport type `"http"` (used in some admin API calls and the E2E test helpers). Add an alias:

```go
// In buildTransport, inside the switch:
case "http", "streamable_http":
    endpoint, _ := srv.Config["endpoint"].(string)
    if endpoint == "" {
        endpoint, _ = srv.Config["url"].(string) // common alias used in API payloads
    }
    ...
```

Similarly, accept `"url"` as an alias for `"endpoint"` in the config map. This makes the admin API more tolerant of common naming variants without breaking the existing `"streamable_http"` + `"endpoint"` convention.

---

## 7. Migration 004

No new tables are required for Sprint 6. Rate limiting is Redis-only. The carry-forward fixes are code-only changes. However, a housekeeping migration is added to improve query performance for the retention cleanup (CF4) and to index the `hash_chain` column for future deduplication support:

**`004_audit_log_indexes.up.sql`:**

```sql
-- Composite index for retention cleanup: timestamp is the leftmost column so the
-- planner can use it for the range scan in DeleteExpired without a full table scan.
-- audit_logs is partitioned by timestamp RANGE; child partitions already have local
-- indexes on timestamp; this migration adds a covering index on the parent for
-- queries spanning multiple partitions.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_timestamp_id
    ON audit_logs (timestamp ASC, id ASC);

-- hash_chain index: supports future ON CONFLICT (hash_chain) DO NOTHING for WAL
-- replay idempotency. hash_chain is a fixed-length hex string (64 chars) so the
-- B-tree index is narrow and cheap to maintain.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_hash_chain
    ON audit_logs (hash_chain);
```

**`004_audit_log_indexes.down.sql`:**

```sql
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_timestamp_id;
DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_hash_chain;
```

`CONCURRENTLY` avoids a table lock on the (potentially large) `audit_logs` table. Both are `IF NOT EXISTS` / `IF EXISTS` for idempotency.

---

## 8. Server Wiring Changes (`internal/server/server.go`)

### New fields on `Server`

```go
type Server struct {
    // ... existing fields ...
    metricsCollector *metrics.Collector
    tracingProvider  *tracing.Provider
    rateLimiter      store.RateLimiter
    metricsServer    *http.Server    // runs on MetricsPort
}
```

### `New()` additions (in order)

```go
// 1. Metrics collector (before pipeline so RegisterPipelineGauge can be called after)
collector := metrics.NewCollector()

// 2. Tracing provider (no-op if OTLP disabled)
tracingProvider, err := tracing.New(cfg.Metrics.OTLP, "mcpgate")
if err != nil {
    return nil, fmt.Errorf("init tracing: %w", err)
}

// 3. Rate limiter
var rateLimiter store.RateLimiter
if cfg.RateLimiting.Enabled {
    rateLimiter = redisstore.NewRateLimiter(redisClient)
}

// 4. After pipeline is created:
collector.RegisterPipelineGauge(pipeline.DroppedCount)

// 5. Metrics HTTP server
metricsMux := http.NewServeMux()
metricsMux.Handle("/metrics", collector.Handler())
metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
metricsServer := &http.Server{Handler: metricsMux}
go func() {
    ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.MetricsPort))
    if err != nil {
        logger.Error("metrics server failed to start", "err", err)
        return
    }
    if err := metricsServer.Serve(ln); err != nil && err != http.ErrServerClosed {
        logger.Error("metrics server error", "err", err)
    }
}()

// 6. WAL startup recovery (CF1)
if cfg.Logging.Pipeline.WALPath != "" {
    replayingPath := cfg.Logging.Pipeline.WALPath + ".replaying"
    if _, err := os.Stat(replayingPath); err == nil {
        _ = os.Rename(replayingPath, cfg.Logging.Pipeline.WALPath)
        logger.Warn("wal: .replaying file found on startup; crash recovery rename done")
    }
}

// 7. Audit log retention goroutine (CF4)
if cfg.Logging.Retention.HotDays > 0 {
    go retentionLoop(bgCtx, auditStore, cfg.Logging.Retention.HotDays, logger)
}
```

### `buildRouter()` final middleware order

```go
// Global middleware (all routes)
r.Use(chimw.RequestID)
r.Use(chimw.Recoverer)
r.Use(s.tracingProvider.Middleware())   // ← new: all routes traced
r.Use(s.requestLogger)

// MCP proxy routes
r.Route("/mcp/{serverName}", func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(logging.Middleware(s.pipeline, s.serverStore))     // pre-parse body, submit entry
    r.Use(s.metricsCollector.Middleware())                   // ← new: count + latency
    r.Use(ratelimit.Middleware(s.rateLimiter, s.cfg.RateLimiting, s.metricsCollector, s.logger)) // ← new
    r.Use(rbac.Authorize(s.enforcer, s.logger))
    r.Post("/message", s.proxyHandler.HandleStreamableHTTP)
    r.Get("/sse", s.proxyHandler.HandleSSEStream)
    r.Post("/sse", s.proxyHandler.HandleSSEMessage)
})
```

### `Shutdown()` additions

```go
func (s *Server) Shutdown() {
    // Stop accepting new metrics scrapes
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    _ = s.metricsServer.Shutdown(ctx)

    // Flush OTLP spans
    _ = s.tracingProvider.Shutdown(ctx)

    // Stop background goroutines (JWKS eviction, session cleanup, WAL replay, retention)
    s.bgCancel()

    // Drain audit log pipeline (must be last — handlers may still be running)
    s.pipeline.Drain()
}
```

---

## 9. Test Plan

### Unit tests (no services, `-short` compatible)

| File | Tests |
|------|-------|
| `internal/metrics/collector_test.go` | Counter increments after Middleware is called; histogram observations recorded; `statusLabel` mapping; `RegisterPipelineGauge` returns non-zero when DroppedCount>0 |
| `internal/tracing/provider_test.go` | No-op provider creates spans without panicking; `Middleware` propagates trace context header; `Shutdown` is idempotent |
| `internal/ratelimit/middleware_test.go` | Mock limiter `Allow → true` calls next; mock limiter `Allow → false` returns 429; `Retry-After` header set; JSON-RPC error format for MCP route; limiter error (fail open) calls next with warning logged |
| `internal/proxy/circuitbreaker/breaker_test.go` | Starts Closed; N failures → Open; recovery timeout → HalfOpen; M successes in HalfOpen → Closed; failure in HalfOpen → Open; concurrent Allow under race detector |

### Integration tests (`//go:build integration`)

| File | Tests |
|------|-------|
| `internal/store/redis/rate_limiter_test.go` | Under limit: `Allowed=true`; at limit: `Allowed=false`; RPM and RPD are independent windows; key expires after window; concurrent requests from multiple goroutines do not exceed limit |
| `internal/store/postgres/audit_logs_test.go` | Add `TestAuditLogStore_DeleteExpired` — insert rows with old timestamps, call `DeleteExpired(7)`, verify only old rows deleted; add `TestAuditLogStore_VerifyHashChain_HostIP` — verify `host(client_ip)` returns bare IP, not CIDR |

### E2E tests (`//go:build integration`, `tests/e2e/`)

| File | Tests |
|------|-------|
| `tests/e2e/metrics_test.go` | `TestE2E_Metrics_EndpointReturnsPrometheusText` — GET port 9090 `/metrics` → 200, Content-Type `text/plain; version=0.0.4`; `TestE2E_Metrics_RequestCounterIncrements` — send 3 MCP requests, scrape metrics, verify `mcpgate_requests_total` ≥ 3 |
| `tests/e2e/rate_limit_test.go` | `TestE2E_RateLimit_RPM_Exceeded` — set RPM=5, send 10 requests, verify at least 5 return 429; `TestE2E_RateLimit_AdminExempt` — (if admin role gets higher limits) admin requests not rate-limited by default config |
| `tests/e2e/circuit_breaker_test.go` | `TestE2E_CircuitBreaker_OpensAfterThreshold` — configure CB threshold=3, send 3 failing requests (to non-existent server), verify 4th request returns 502 without hitting upstream (confirmed via circuit state metric) |
| `tests/e2e/logging_test.go` | Add `TestE2E_AuditLog_VerifyEndpoint` — send requests, call `GET /api/v1/admin/logs/verify?from=...&to=...&genesis=test-genesis`, verify `{"ok": true}` |
| `tests/e2e/logging_test.go` | Add `TestE2E_AuditLog_Retention` — insert entries with past timestamps via `BatchInsert`, call `DeleteExpired` directly, verify old entries removed |

---

## 10. Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Metrics on port 9090, separate `http.Server` from main API | Security: operators can firewall 9090 independently. A single process restart does not require re-opening two ports. `MetricsPort` already defined in config. |
| D2 | Dedicated `prometheus.Registry` (not global) | The global registry accumulates metrics from test runs, making assertions on counter values unreliable between tests. An isolated registry per `Collector` makes unit testing deterministic. |
| D3 | Tracing middleware using `otelhttp.NewMiddleware` (not a custom span) | `otelhttp` correctly handles the span lifecycle including panic recovery and context propagation. Writing a bespoke span wrapper would duplicate that logic. The span name formatter uses the chi route pattern (not the URL) to avoid high-cardinality span names. |
| D4 | No-op tracer when OTLP is disabled | Code downstream of the tracing middleware can call `trace.SpanFromContext(ctx)` unconditionally. If the no-op path returned `nil`, all callers would need nil-guards. A no-op tracer is cheap and simplifies all instrumented code. |
| D5 | Rate limiter fails open on Redis error | A Redis partition should not cause a complete service outage. Rate limiting is a capacity protection mechanism, not a security control. Failing open under infrastructure failure is the correct trade-off. Auth failures always fail closed regardless. |
| D6 | Sliding window (sorted set + Lua) not fixed window (INCR + TTL) | A fixed window allows a burst of `2×limit` requests spanning a window boundary. The sliding window prevents this. The Lua script ensures atomicity — ZREMRANGEBYSCORE + ZCARD + ZADD cannot race. |
| D7 | Circuit breaker per server, not per transport instance | Multiple transport instances can be created for the same server (LoadOrStore race). The circuit breaker should reflect the health of the server, not any particular transport instance. Keying by server name is the correct scope. |
| D8 | WAL rename before read, not truncate after write | Truncating after write leaves a window where a crash produces duplicates. Renaming before read means a crash before write leaves the original `.replaying` file, which is replayed on restart. The file is only deleted after a confirmed successful write. |
| D9 | `host(client_ip)` SQL function instead of `::text` cast | `INET::text` in PostgreSQL appends the prefix length (`/32`). `host()` returns only the IP address. This is a database-level fix that eliminates the application-level workaround in `scanAuditEntry`. Keeping the fix at the SQL level is preferable to stripping the suffix in Go. |
| D10 | No `?token=` for WS auth | URL parameters appear in access logs, proxy logs, and browser history. Leaking a bearer token in a URL parameter is a well-known security anti-pattern (OWASP A02). The existing cookie auth path (`mcpgate_session`) handles the browser WS use case correctly and securely. |
| D11 | `ConcurrentConnections` via Redis INCR/DECR, not sliding window | SSE and WebSocket connections are long-lived and do not generate per-request events to score into a sorted set. A simple atomic counter with TTL equal to the connection timeout correctly tracks concurrency without left-over sorted set members from dropped connections. |
| D12 | Tracing middleware at the global level, not per-route group | Every request — auth failures, health checks, SAML redirects — benefits from a trace span. Global placement also means the span exists before any route-specific middleware runs, so errors in auth middleware are captured in the span rather than lost. |

---

## 11. Ordered File List (Implementation Order)

Dependencies flow downward; each step can begin once the items it depends on are complete.

```
 1. internal/proxy/circuitbreaker/breaker.go         — no external deps
 2. internal/proxy/circuitbreaker/breaker_test.go    — unit tests

 3. internal/store/redis/rate_limiter.go             — requires store.RateLimiter interface (exists)
 4. internal/store/redis/rate_limiter_test.go        — integration

 5. internal/metrics/collector.go                    — requires proxy.MessageFromContext, auth.IdentityFromContextOrZero (both exist)
 6. internal/metrics/collector_test.go               — unit tests

 7. internal/tracing/provider.go                     — requires OTel promoted to direct
 8. internal/tracing/provider_test.go                — unit tests

 9. internal/ratelimit/middleware.go                 — requires store.RateLimiter, metrics.Collector (step 5), proxy.MessageFromContext
10. internal/ratelimit/middleware_test.go            — unit tests

11. internal/store/interfaces.go                     — add AuditLogStore.DeleteExpired (CF4)
12. internal/store/postgres/audit_logs.go            — implement DeleteExpired (CF4), host(client_ip) (CF2)
13. internal/logging/sinks/wal.go                   — atomic rename Replay (CF1)
14. internal/admin/api/logs.go                       — add verify handler (CF3)
15. internal/admin/api/router.go                     — wire GET /logs/verify

16. internal/proxy/handler.go                        — inject circuit breaker, metrics, transport alias (CF6)

17. internal/store/postgres/migrations/004_audit_log_indexes.up.sql
18. internal/store/postgres/migrations/004_audit_log_indexes.down.sql

19. internal/server/server.go                        — metrics server, tracing middleware, rate limit middleware,
                                                       WAL crash recovery (CF1), retention goroutine (CF4),
                                                       enhanced /ready + /healthz + /readyz

20. go get ... + go mod tidy + go build ./...        — verify clean build

21. tests/e2e/metrics_test.go
22. tests/e2e/rate_limit_test.go
23. tests/e2e/circuit_breaker_test.go
24. tests/e2e/logging_test.go                        — add verify + retention tests (CF3, CF4)
```

---

## 12. Verification

```bash
# Unit tests (no services)
go test -race -count=1 -short ./...

# Integration tests (requires Docker)
make docker-up && make migrate-up
go test -race -count=1 -tags=integration ./internal/...

# E2E tests
go test -race -count=1 -tags=integration -timeout=120s ./tests/e2e/...

# Manual: Prometheus scrape
curl -s http://localhost:9090/metrics | grep mcpgate_requests_total

# Manual: rate limit
for i in $(seq 1 20); do
  curl -s -o /dev/null -w "%{http_code}\n" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"jsonrpc":"2.0","id":1,"method":"ping"}' \
    http://localhost:8080/mcp/test/message
done
# Expect: first N return 200/502, remainder return 429

# Manual: circuit breaker
# Send threshold+1 requests to a non-existent server, observe 502 response
# that skips the upstream call (latency much lower than timeout)

# Manual: hash chain verification
curl -s "http://localhost:8080/api/v1/admin/logs/verify?from=2026-01-01T00:00:00Z&to=2026-12-31T23:59:59Z&genesis=genesis" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
# → {"ok":true,"from":"...","to":"..."}

# Manual: OTLP traces
# Start Jaeger: docker run -p 4317:4317 -p 16686:16686 jaegertracing/all-in-one
# Set MCPGATE_METRICS_OTLP_ENABLED=true MCPGATE_METRICS_OTLP_ENDPOINT=localhost:4317 MCPGATE_METRICS_OTLP_INSECURE=true
# Open http://localhost:16686 and search for service "mcpgate"

# Build verification
go build ./...
golangci-lint run ./...
```

---

## 13. TODOs and Carry-Forward

### Completed in Sprint 5 (not repeated here)
See `docs/implementation/stage-5-25-05-2026.md §16`.

### Sprint 6 scope
- [ ] Prometheus metrics — collector, middleware, separate port 9090
- [ ] OpenTelemetry tracing — provider, OTLP export, span enrichment from logging middleware
- [ ] Redis rate limiter — Lua sliding window, RPM + RPD per user
- [ ] Rate limit middleware — 429 + JSON-RPC -32000 + Retry-After
- [ ] Circuit breaker — per-server state machine, integrated into proxy handler
- [ ] Enhanced `/ready` — component-level JSON, upstream circuit states
- [ ] `/healthz` and `/readyz` aliases
- [ ] CF1: WAL replay atomic rename
- [ ] CF2: `host(client_ip)` SQL fix
- [ ] CF3: `GET /api/v1/admin/logs/verify` endpoint
- [ ] CF4: `AuditLogStore.DeleteExpired` + daily retention goroutine
- [ ] CF5: WS stream auth — verify + document cookie pattern
- [ ] CF6: `"http"` transport alias + `"url"` config key alias
- [ ] Migration 004 — audit log indexes

### New TODOs (deferred from Sprint 6)

- [ ] **Per-role rate limit overrides** — the current implementation enforces global defaults from config. Add a `rate_limit_overrides` table (or extend the `roles` table) to store per-role RPM/RPD limits, queried by the rate limit middleware.
- [ ] **Prometheus alerting rules** — provide a `mcpgate-alerts.yaml` PrometheusRule manifest alongside the Kubernetes deployment. Recommended alerts: high error rate (`rate(mcpgate_upstream_errors_total[5m]) > 0.1`), circuit breaker open (`mcpgate_circuit_breaker_state == 1`), rate limit storm (`rate(mcpgate_rate_limit_hits_total[1m]) > 50`), pipeline backlog (`mcpgate_audit_pipeline_dropped_total > 0`).
- [ ] **Sampling strategy** — the Sprint 6 tracer uses `AlwaysSample()`. For production deployments with >100 RPS, add a `ParentBased(TraceIDRatioBased(0.1))` sampler configurable via `cfg.Metrics.OTLP.SampleRate`.
- [ ] **WAL replay true idempotency** — add `INSERT ... ON CONFLICT (hash_chain) DO NOTHING` to `BatchInsert`. Requires the unique index on `hash_chain` from migration 004 to be deployed first. Once deployed, WAL replay can be truly idempotent without the rename dance.
- [ ] **ConcurrentConnections enforcement** — rate limiter currently enforces RPM and RPD. The `ConcurrentConnections` field in `RateLimitDefaults` is parsed from config but not enforced. Implement via Redis INCR/DECR keyed by user ID with TTL equal to the proxy request timeout.
- [ ] **Admin UI (Sprint 7)** — the metrics, rate limit, and circuit breaker state are all exposed via API/metrics endpoints but have no admin console UI. Sprint 7 (React admin console) will consume these.
- [ ] **`WarmDays` retention** — `RetentionConfig.WarmDays` is parsed but not enforced. Sprint 6 adds `HotDays` deletion. `WarmDays` (moving data to cheaper storage) requires either pg_partman partition management or an external archival process. Deferred to the production packaging sprint.

### Carry-forward to Sprint 7 (still open from Sprint 5)
- [ ] **Rate limiting** — now resolved in Sprint 6.
- [ ] **OTLP metrics** — now resolved in Sprint 6.
- [ ] **WAL replay idempotency** — partially resolved (atomic rename in Sprint 6; full ON CONFLICT idempotency deferred above).
