# Sprint 5 Implementation Plan — Audit Logging Pipeline

**Date:** 2026-05-25  
**Status:** Pre-implementation  
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/implementation/stage-4-25-05-2026.md`  
**Predecessor:** `docs/implementation/stage-4-plan.md`

---

## Context

Sprints 1–4 delivered the proxy engine, authentication (SAML + M2M JWKS), and RBAC enforcement. Every proxied request is now authenticated and authorised, but nothing is recorded — there is no audit trail, no operator visibility into what happened, and no tamper-evidence. Sprint 5 closes this gap.

**Primary deliverable:** every request that passes through the MCP proxy produces a structured `AuditLogEntry` written asynchronously to PostgreSQL via a buffered pipeline. Entries form a SHA-256 hash chain so that any deletion or modification is detectable. Operators can query, export, and tail logs in real-time via the admin REST API and a WebSocket stream.

**Secondary deliverables (carry-overs from prior sprints that fit naturally alongside logging):**
- `SessionStore.DeleteExpired` background goroutine (T5 — stages 3/4)
- `Stdio` transport auto-restart on unexpected child process exit (T6)
- JWKS validator client-map eviction (T1 — prevents unbounded memory growth)

---

## What Already Exists

The schema, interfaces, and config are pre-built from Sprint 0. No new migrations or config structs are required.

| Asset | Location | State |
|-------|----------|-------|
| `audit_logs` table | migration 001 | Exists; `PARTITION BY RANGE (timestamp)` with a DEFAULT partition. All column types correct. |
| `AuditLogStore` interface | `internal/store/interfaces.go:34` | Defined: `BatchInsert`, `Query`, `Export`, `VerifyHashChain` |
| `AuditLogEntry` struct | `internal/store/interfaces.go:147` | All 21 fields present |
| `LogFilter` / `Pagination` | `internal/store/interfaces.go:195` | Defined |
| `LoggingConfig` / `PipelineConfig` / `RetentionConfig` | `internal/config/config.go:132` | Defined with defaults: BufferSize 10k, FlushInterval 100ms, BatchSize 500, WriterCount 4, HotDays 30, WarmDays 90 |
| `internal/logging/logger.go` | `internal/logging/` | Exists: slog setup, `New()`, `WithLogger()`, `FromContext()` |

**Config additions needed** (two fields added to `PipelineConfig`):
```go
WALPath       string `koanf:"wal_path"`        // "" = no WAL fallback
HashChainSeed string `koanf:"hash_chain_seed"` // default "genesis"
```

---

## Architecture: Logging Data Flow

```
HTTP Request
  │
  ├─ authMiddleware (Sprint 2)
  ├─ rbac.Authorize (Sprint 4) ─── stores *proxy.Message in ctx via proxy.WithMessage(ctx, msg)
  ├─ logging.Middleware (Sprint 5)
  │     ├─ wraps ResponseWriter → captures status code + bytes written
  │     ├─ records start time
  │     └─ after next.ServeHTTP() returns:
  │           reads identity, serverName, *proxy.Message from ctx
  │           builds AuditLogEntry with timing + status
  │           Pipeline.Submit(entry)   ← non-blocking; drops if channel full
  │
  └─ proxy.Handler (forwards to upstream MCP server)

Pipeline (async, single writer goroutine)
  │
  ├─ entries chan *AuditLogEntry (BufferSize: 10,000)
  ├─ Writer goroutine:
  │     accumulate batch ──── timer fires (FlushInterval) OR len(batch) == BatchSize
  │     │
  │     ├─ compute hash chain for each entry in batch (sequential, see §5)
  │     ├─ primarySink.Write(ctx, batch)
  │     │     └─ on error → walSink.Write(ctx, batch) (if WALPath configured)
  │     └─ fan-out to WebSocket subscribers (non-blocking, drop if subscriber slow)
  │
  └─ Drain() on graceful shutdown: flush remaining entries before returning

Admin API (Sprint 5)
  GET  /api/v1/admin/logs          → paginated query with filters
  GET  /api/v1/admin/logs/export   → streaming JSON or CSV
  WS   /api/v1/admin/logs/stream   → real-time tail with optional filter
```

---

## Carry-Over Items

### C1. stdio Auto-Restart (T6)

`internal/proxy/transport/stdio.go` — when `readLoop` exits because the child process died unexpectedly (not via `Close()`), subsequent `Send()` calls return `"stdio: process exited"`. The transport needs to restart the child process on the next `Send()`.

**Design:** Add a `closed atomic.Bool` field. `Close()` sets it to `true` before closing `t.done`. When `readLoop` exits and `closed` is `false`, it resets `t.proc = nil` and recreates `t.done = make(chan struct{})`, leaving the transport in a state where the next `ensureRunning()` will restart the process.

```go
type Stdio struct {
    // existing fields...
    closed atomic.Bool
}

func (t *Stdio) Close() error {
    t.closed.Store(true)
    // existing close logic
}

// readLoop: on scanner.Scan() returning false (EOF / process exit):
if !t.closed.Load() {
    // unexpected exit — reset so ensureRunning() can restart
    t.procMu.Lock()
    t.proc = nil
    t.stdin = nil
    t.done = make(chan struct{}) // new done channel so next Send() doesn't see old close
    t.procMu.Unlock()
    // drain pending callers: close old done before resetting
}
```

**Subtlety:** pending callers blocked on `<-t.done` need to be notified of the restart failure, then they'll call `Send()` again (with their own retry logic, or the proxy handler returns an upstream error). The current design closes `t.done` on process exit — pending callers receive `"stdio: process exited"` and propagate the error. The transport then resets for the next caller. This is correct.

### C2. JWKS Validator Client Eviction (T1)

`internal/auth/jwks/validator.go` — `Validator.clients` is a `map[string]*Client` that is never evicted. If JWKS configs are deleted from the DB, their `Client` instances accumulate in memory forever.

**Design:** Wrap each client in a `clientEntry`:
```go
type clientEntry struct {
    client   *Client
    lastUsed atomic.Int64 // UnixNano
}
```

`clientFor()` updates `lastUsed` on every access. A background goroutine (started via `Validator.Start(ctx context.Context)`) runs every hour and removes entries whose `lastUsed` is more than `clientTTL * 3` ago.

`NewValidator` gains an optional `WithEvictionInterval(d time.Duration)` option, or more simply, `Validator.Start(ctx)` is called from `server.New()` in a goroutine. The eviction goroutine stops when `ctx` is cancelled (server shutdown).

### C3. SessionStore.DeleteExpired Background Goroutine (T5)

`internal/store/postgres/sessions.go` — `DeleteExpired(ctx) (int64, error)` is implemented but never called.

**Design:** Start a background goroutine in `server.Run()`:
```go
go func() {
    ticker := time.NewTicker(24 * time.Hour)
    defer ticker.Stop()
    for {
        select {
        case <-ticker.C:
            n, err := sessionStore.DeleteExpired(ctx)
            if err != nil {
                s.logger.Warn("session cleanup failed", "err", err)
            } else {
                s.logger.Info("session cleanup", "deleted", n)
            }
        case <-ctx.Done():
            return
        }
    }
}()
```

The goroutine uses the same context passed to `Run()`, so it stops cleanly on shutdown.

---

## New Package Structure

```
internal/
├── logging/
│   ├── logger.go       EXISTING: slog setup — no changes
│   ├── entry.go        NEW: Build AuditLogEntry from request context + timing
│   ├── pipeline.go     NEW: Pipeline struct, Submit, Start, Drain, Sink interface, subscriber registry
│   ├── hashchain.go    NEW: SHA-256 chain computation + verification helper
│   ├── middleware.go   NEW: HTTP middleware that wraps proxy routes + stores parsed msg in context
│   └── sinks/
│       ├── postgres.go NEW: PostgreSQL sink (wraps AuditLogStore.BatchInsert)
│       └── wal.go      NEW: WAL fallback sink (newline-delimited JSON file)
└── store/
    └── postgres/
        └── audit_logs.go  NEW: AuditLogStore pgx implementation

internal/admin/api/
└── logs.go             NEW: logHandler: list, export, stream (WebSocket)

internal/proxy/
└── context.go          NEW: WithMessage / MessageFromContext context helpers (proxy.Message key)

internal/auth/jwks/
└── validator.go        MODIFIED: clientEntry with lastUsed, Start(ctx) for eviction goroutine

internal/proxy/transport/
└── stdio.go            MODIFIED: closed atomic.Bool, auto-restart in readLoop

internal/admin/api/
└── router.go           MODIFIED: wire /logs routes, accept AuditLogStore

internal/server/
└── server.go           MODIFIED: instantiate AuditLogStore, Pipeline, start background jobs, wire logging middleware

internal/config/
└── config.go           MODIFIED: add WALPath + HashChainSeed to PipelineConfig
```

---

## New Dependencies

```bash
go get github.com/gorilla/websocket@latest
go mod tidy
```

`gorilla/websocket` is used for the admin WebSocket log stream (`WS /api/v1/admin/logs/stream`). No other new external dependencies: hash chain uses `crypto/sha256` (stdlib), WAL file uses `os` and `encoding/json` (stdlib).

---

## Implementation Details

### 1. Config Additions (`internal/config/config.go`)

Add two fields to the existing `PipelineConfig`:

```go
type PipelineConfig struct {
    BufferSize    int           `koanf:"buffer_size"`    // already exists
    FlushInterval time.Duration `koanf:"flush_interval"` // already exists
    BatchSize     int           `koanf:"batch_size"`     // already exists
    WriterCount   int           `koanf:"writer_count"`   // already exists
    WALPath       string        `koanf:"wal_path"`       // NEW: "" = no WAL
    HashChainSeed string        `koanf:"hash_chain_seed"` // NEW: default "genesis"
}
```

`WALPath = ""` disables WAL fallback (the sink logs errors and increments a dropped counter). `HashChainSeed` is the sentinel previous-hash for the very first log entry. Default: `"genesis"`.

Add to `defaults()`:
```go
Pipeline: PipelineConfig{
    // existing fields unchanged
    WALPath:       "",
    HashChainSeed: "genesis",
},
```

---

### 2. Context Key for Parsed MCP Message (`internal/proxy/context.go`)

The logging middleware needs the parsed `*proxy.Message` to record `mcp_method` and `tool_name` without re-reading the body. The RBAC middleware already parses the body via `proxy.Parse(buf)` — it stores the result in the request context for downstream consumers.

```go
package proxy

import "context"

type contextKey string

const messageKey contextKey = "mcp_message"

// WithMessage stores the parsed JSON-RPC message in ctx.
// Called by the RBAC Authorize middleware after parsing the request body.
func WithMessage(ctx context.Context, msg *Message) context.Context {
    return context.WithValue(ctx, messageKey, msg)
}

// MessageFromContext retrieves the parsed message, or nil if not set.
func MessageFromContext(ctx context.Context) *Message {
    m, _ := ctx.Value(messageKey).(*Message)
    return m
}
```

**Update `internal/auth/rbac/middleware.go`** — after parsing the body, store the message:

```go
msg, parseErr := proxy.Parse(buf)
if parseErr != nil || msg == nil {
    action = "unknown"
} else {
    action = proxy.Action(msg.Method)
    r = r.WithContext(proxy.WithMessage(r.Context(), msg)) // NEW
}
```

---

### 3. Audit Log Entry Builder (`internal/logging/entry.go`)

```go
package logging

import (
    "net/http"
    "time"

    chimw "github.com/go-chi/chi/v5/middleware"
    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "github.com/K3y5tr0ke/mcpgate/internal/auth"
    "github.com/K3y5tr0ke/mcpgate/internal/proxy"
    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

// BuildEntry constructs an AuditLogEntry from the request context after the
// proxy handler has returned. ww is the wrapped ResponseWriter (captures status + bytes).
func BuildEntry(
    r *http.Request,
    ww wrappedWriter,      // interface: Status() int, BytesWritten() int
    serverID  uuid.UUID,   // resolved from server config; uuid.Nil if unknown
    startTime time.Time,
) store.AuditLogEntry {
    ctx := r.Context()
    identity := auth.IdentityFromContextOrZero(ctx)
    msg := proxy.MessageFromContext(ctx) // nil for GET /sse (connect action)

    entry := store.AuditLogEntry{
        Timestamp:    startTime,
        TraceID:      chimw.GetReqID(ctx),
        SessionID:    identity.SessionID,
        UserID:       identity.UserID,
        UserEmail:    identity.Email,
        ClientIP:     extractClientIP(r),
        ServerID:     serverID,
        ServerName:   chi.URLParam(r, "serverName"),
        LatencyMS:    int(time.Since(startTime).Milliseconds()),
        RequestSize:  int(r.ContentLength),
        ResponseSize: ww.BytesWritten(),
    }

    if msg != nil {
        entry.MCPMethod = msg.Method
        entry.ToolName  = msg.ToolName()
    }

    switch ww.Status() {
    case 0, 200:
        entry.Status    = "success"
        entry.Direction = "response"
    case 403:
        entry.Status       = "denied"
        entry.RBACDecision = "deny"
    default:
        entry.Status = "error"
        code := ww.Status()
        entry.ErrorCode = &code
    }

    return entry
}
```

`auth.IdentityFromContextOrZero` is a new helper (returns zero-value `UserIdentity` if absent, for the rare case where the logging middleware runs on an unauthenticated route — practically impossible given auth is enforced upstream, but defensive). Add to `internal/auth/context.go`:

```go
func IdentityFromContextOrZero(ctx context.Context) UserIdentity {
    id, _ := IdentityFromContext(ctx)
    return id
}
```

**`wrappedWriter` interface:**

```go
type wrappedWriter interface {
    Status() int
    BytesWritten() int
}
```

`chimw.WrapResponseWriter` (already used in `requestLogger`) satisfies this interface.

---

### 4. Hash Chain (`internal/logging/hashchain.go`)

```go
package logging

import (
    "crypto/sha256"
    "encoding/json"
    "fmt"

    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

// HashChain maintains the running tip of the tamper-evident hash chain.
// All operations on a single HashChain instance must be called from the same goroutine.
type HashChain struct {
    prev string // hex-encoded SHA-256 of the previous entry (or seed)
}

// NewHashChain initialises the chain. If prev is "" the seed is used.
func NewHashChain(seed string) *HashChain {
    return &HashChain{prev: seed}
}

// Next computes the hash for entry, sets entry.HashChain, and advances the
// chain tip. entry must have all fields populated except HashChain.
func (hc *HashChain) Next(entry *store.AuditLogEntry) error {
    // Serialise the entry without the hash_chain field.
    content, err := marshalWithoutHashChain(entry)
    if err != nil {
        return fmt.Errorf("hashchain: marshal: %w", err)
    }
    combined := hc.prev + string(content)
    sum := sha256.Sum256([]byte(combined))
    entry.HashChain = fmt.Sprintf("%x", sum)
    hc.prev = entry.HashChain
    return nil
}

// Tip returns the current chain tip (last computed hash).
func (hc *HashChain) Tip() string { return hc.prev }

// marshalWithoutHashChain serialises entry with HashChain zeroed so the hash
// does not include itself (avoiding circularity).
func marshalWithoutHashChain(entry *store.AuditLogEntry) ([]byte, error) {
    type shadow store.AuditLogEntry
    tmp := shadow(*entry)
    tmp.HashChain = ""
    return json.Marshal(tmp)
}
```

**Chain initialisation at startup:** The pipeline queries the `AuditLogStore` for the latest row's `hash_chain` value before starting the writer goroutine. If the table is empty, it uses `cfg.Pipeline.HashChainSeed`.

**`VerifyHashChain`** is implemented in `AuditLogStore` (see §5.3) by re-reading rows in ascending order and recomputing each hash, comparing against the stored `hash_chain`. This is a read-only admin operation, not part of the hot path.

---

### 5. Audit Log Pipeline (`internal/logging/pipeline.go`)

```go
package logging

import (
    "context"
    "log/slog"
    "sync"
    "sync/atomic"
    "time"

    "github.com/K3y5tr0ke/mcpgate/internal/config"
    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Sink is implemented by any backend that can durably store a batch of entries.
type Sink interface {
    Write(ctx context.Context, entries []store.AuditLogEntry) error
}

// Subscriber receives a copy of each entry after it is written to the sink.
// Entries are dropped (not queued) if the subscriber channel is full.
type Subscriber struct {
    ch     chan store.AuditLogEntry
    cancel func()
}

// Pipeline is the async audit log ingestion pipeline.
// Submit() is safe to call from any goroutine.
// Start() and Drain() must be called from the same goroutine (server lifecycle).
type Pipeline struct {
    entries     chan store.AuditLogEntry
    cfg         config.PipelineConfig
    primary     Sink
    fallback    Sink   // nil if WALPath is ""
    chain       *HashChain
    logger      *slog.Logger
    dropped     atomic.Int64
    subMu       sync.RWMutex
    subscribers map[string]*Subscriber
    done        chan struct{}
    drain       chan struct{}
}

func NewPipeline(
    cfg      config.PipelineConfig,
    primary  Sink,
    fallback Sink,       // nil = no WAL
    initHash string,     // last hash from DB, or seed
    logger   *slog.Logger,
) *Pipeline {
    return &Pipeline{
        entries:     make(chan store.AuditLogEntry, cfg.BufferSize),
        cfg:         cfg,
        primary:     primary,
        fallback:    fallback,
        chain:       NewHashChain(initHash),
        logger:      logger,
        subscribers: make(map[string]*Subscriber),
        done:        make(chan struct{}),
        drain:       make(chan struct{}),
    }
}

// Submit enqueues entry for async write. Non-blocking: increments dropped counter
// if the channel is full. Should never happen with default buffer of 10k entries
// and a 100ms flush interval.
func (p *Pipeline) Submit(entry store.AuditLogEntry) {
    select {
    case p.entries <- entry:
    default:
        p.dropped.Add(1)
        p.logger.Warn("audit log pipeline full — entry dropped",
            "dropped_total", p.dropped.Load())
    }
}

// DroppedCount returns the total number of entries dropped due to channel backpressure.
func (p *Pipeline) DroppedCount() int64 { return p.dropped.Load() }

// Subscribe registers a channel to receive copies of written entries.
// The caller must call cancel() to deregister.
func (p *Pipeline) Subscribe(id string, bufSize int) (<-chan store.AuditLogEntry, func()) {
    ch := make(chan store.AuditLogEntry, bufSize)
    sub := &Subscriber{ch: ch}
    sub.cancel = func() {
        p.subMu.Lock()
        delete(p.subscribers, id)
        p.subMu.Unlock()
        close(ch)
    }
    p.subMu.Lock()
    p.subscribers[id] = sub
    p.subMu.Unlock()
    return ch, sub.cancel
}

// Start launches the writer goroutine. ctx is used for sink writes.
func (p *Pipeline) Start(ctx context.Context) {
    go p.writer(ctx)
}

// Drain signals the writer to flush all pending entries and waits for completion.
// Call this during graceful shutdown before closing the database connection.
func (p *Pipeline) Drain() {
    close(p.done)
    <-p.drain
}
```

**Writer goroutine logic:**

```go
func (p *Pipeline) writer(ctx context.Context) {
    defer close(p.drain)

    ticker := time.NewTicker(p.cfg.FlushInterval)
    defer ticker.Stop()

    batch := make([]store.AuditLogEntry, 0, p.cfg.BatchSize)

    flush := func() {
        if len(batch) == 0 {
            return
        }
        // Compute hash chain (sequential, single goroutine — correctness guaranteed).
        for i := range batch {
            if err := p.chain.Next(&batch[i]); err != nil {
                p.logger.Error("hashchain compute failed", "err", err)
            }
        }
        // Write to primary sink.
        if err := p.primary.Write(ctx, batch); err != nil {
            p.logger.Error("audit log write failed", "err", err, "count", len(batch))
            // Fall back to WAL if configured.
            if p.fallback != nil {
                if wErr := p.fallback.Write(ctx, batch); wErr != nil {
                    p.logger.Error("WAL fallback write failed", "err", wErr)
                    p.dropped.Add(int64(len(batch)))
                }
            } else {
                p.dropped.Add(int64(len(batch)))
            }
        } else {
            // Fan-out to subscribers (best-effort).
            p.subMu.RLock()
            for _, sub := range p.subscribers {
                for _, e := range batch {
                    select {
                    case sub.ch <- e:
                    default: // subscriber slow — drop
                    }
                }
            }
            p.subMu.RUnlock()
        }
        batch = batch[:0]
    }

    for {
        select {
        case entry := <-p.entries:
            batch = append(batch, entry)
            if len(batch) >= p.cfg.BatchSize {
                flush()
            }
        case <-ticker.C:
            flush()
        case <-p.done:
            // Drain remaining entries.
            for {
                select {
                case entry := <-p.entries:
                    batch = append(batch, entry)
                default:
                    flush()
                    return
                }
            }
        }
    }
}
```

**Note on `WriterCount`:** The config field `WriterCount: 4` is reserved for a future parallel-partition design where each writer owns a hash chain segment (e.g., sharded by user). For Sprint 5, a single writer goroutine ensures hash chain correctness without coordination overhead. The config field is not removed; it is documented as "reserved, currently 1".

---

### 6. PostgreSQL Sink (`internal/logging/sinks/postgres.go`)

```go
package sinks

import (
    "context"

    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

// PostgresSink writes batches to PostgreSQL via AuditLogStore.BatchInsert.
type PostgresSink struct {
    store store.AuditLogStore
}

func NewPostgresSink(s store.AuditLogStore) *PostgresSink {
    return &PostgresSink{store: s}
}

func (s *PostgresSink) Write(ctx context.Context, entries []store.AuditLogEntry) error {
    return s.store.BatchInsert(ctx, entries)
}
```

The complexity (batching, retry) is handled by the pipeline. The sink is intentionally thin.

### 7. WAL Fallback Sink (`internal/logging/sinks/wal.go`)

**Only instantiated when `cfg.Pipeline.WALPath != ""`.**

```go
type WALSink struct {
    path   string
    mu     sync.Mutex
    logger *slog.Logger
}

func NewWALSink(path string, logger *slog.Logger) *WALSink

// Write appends each entry as a newline-delimited JSON line to the WAL file.
// Creates the file if it does not exist. Uses a mutex to prevent concurrent writes.
func (s *WALSink) Write(ctx context.Context, entries []store.AuditLogEntry) error

// Replay reads all entries from the WAL file, writes them to primary, and on
// success truncates the WAL. Called by a background goroutine in server.go.
func (s *WALSink) Replay(ctx context.Context, primary Sink) error
```

WAL replay is triggered by a background goroutine in `server.Run()` that polls every 5 minutes when the WAL file exists and has content. The polling goroutine tries the primary sink directly; on success it calls `Replay`.

---

### 8. AuditLogStore Implementation (`internal/store/postgres/audit_logs.go`)

```go
package postgres

type AuditLogStore struct{ db *pgxpool.Pool }

func NewAuditLogStore(db *pgxpool.Pool) *AuditLogStore
```

#### 8.1 `BatchInsert`

Use a multi-row `INSERT` (not pgx COPY) for correctness with the hash chain and nullable columns. The `audit_logs` table uses `BIGSERIAL` for `id` — COPY protocol bypasses the sequence and triggers, which could corrupt the partition routing.

```sql
INSERT INTO audit_logs (
    timestamp, trace_id, session_id, user_id, user_email, client_ip,
    server_id, server_name, mcp_method, tool_name, direction,
    status, error_code, error_message, latency_ms, request_size,
    response_size, rbac_decision, metadata, hash_chain
) VALUES ($1, $2, ...) [, ($N+1, $N+2, ...)]
```

Build parameterised SQL dynamically using a `strings.Builder` with one row placeholder per entry. Limit to `BatchSize` rows per call. Return a wrapped error on `pgconn` failures.

**UUID nullable columns (`session_id`, `user_id`, `server_id`):** These are `uuid.UUID` which is always a 16-byte value. For rows where the UUID is `uuid.Nil` (M2M tokens, no server match), pass `nil` to pgx so the DB column receives NULL. Use a helper:
```go
func nullUUID(id uuid.UUID) any {
    if id == uuid.Nil { return nil }
    return id
}
```

**`client_ip`:** DB column is `INET`. Pass as `string` — pgx accepts text representation for INET.

#### 8.2 `Query`

```go
func (s *AuditLogStore) Query(ctx context.Context, filter LogFilter, page Pagination) ([]AuditLogEntry, int64, error)
```

Build dynamic WHERE from `LogFilter` using positional parameters:

```sql
SELECT [all columns], COUNT(*) OVER() AS total
FROM audit_logs
WHERE 1=1
  [AND timestamp >= $N]
  [AND timestamp <= $N]
  [AND user_id = $N]
  [AND server_name = $N]
  [AND tool_name ILIKE $N]
  [AND mcp_method = $N]
  [AND status = $N]
  [AND (user_email ILIKE $N OR error_message ILIKE $N)]
ORDER BY timestamp DESC
LIMIT $N OFFSET $N
```

Returns `([]AuditLogEntry, totalCount, error)`. The `COUNT(*) OVER()` window function returns the total matching rows without a separate query.

`LogFilter.Search` maps to an ILIKE on `user_email || error_message`.

#### 8.3 `Export`

```go
func (s *AuditLogStore) Export(ctx context.Context, filter LogFilter, format string, w io.Writer) error
```

Streams rows in batches of 1000 to `w` without buffering the entire result set. Uses pgx `Query` with a cursor-like pattern (LIMIT/OFFSET loop). Writes JSON (one object per line, no enclosing array) or CSV depending on `format`.

For CSV, write a header row on first iteration using `encoding/csv`.

#### 8.4 `VerifyHashChain`

```go
func (s *AuditLogStore) VerifyHashChain(ctx context.Context, from, to time.Time) (bool, error)
```

Reads all entries in `[from, to]` ordered by `(timestamp ASC, id ASC)`. For each entry, recomputes the hash using `logging.HashChain.Next()` and compares against `entry.HashChain`. Returns `(false, nil)` on first mismatch, `(true, nil)` if all match.

This is a potentially expensive admin operation. It is not called on the hot path.

**Initialisation query (for pipeline start):**
```sql
SELECT hash_chain FROM audit_logs ORDER BY timestamp DESC, id DESC LIMIT 1
```
Used by `NewPipeline` to initialise the chain tip from the last persisted row.

---

### 9. Logging Middleware (`internal/logging/middleware.go`)

```go
package logging

import (
    "net/http"
    "time"

    chimw "github.com/go-chi/chi/v5/middleware"
    "github.com/google/uuid"

    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Middleware returns a chi middleware that instruments every request through the
// MCP proxy routes, builds an AuditLogEntry, and submits it to the pipeline.
// It must be wired AFTER the auth and RBAC middlewares so that UserIdentity and
// the parsed *proxy.Message are available in the request context.
func Middleware(pipeline *Pipeline, serverStore store.ServerStore) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            start := time.Now()
            ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

            next.ServeHTTP(ww, r)

            // Resolve server UUID (best-effort; uuid.Nil if lookup fails or times out).
            serverID := resolveServerID(r.Context(), serverStore, chi.URLParam(r, "serverName"))

            entry := BuildEntry(r, ww, serverID, start)
            pipeline.Submit(entry)
        })
    }
}

func resolveServerID(ctx context.Context, ss store.ServerStore, name string) uuid.UUID {
    // Use a short timeout so a slow DB does not delay the logged response.
    ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
    defer cancel()
    srv, err := ss.GetByName(ctx, name)
    if err != nil {
        return uuid.Nil
    }
    return srv.ID
}
```

**Middleware order for MCP routes:**
```go
r.Route("/mcp/{serverName}", func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(rbac.Authorize(s.enforcer, s.logger))
    r.Use(logging.Middleware(s.pipeline, s.serverStore))  // NEW — must be after RBAC
    r.Post("/message", ...)
    r.Get("/sse", ...)
    r.Post("/sse", ...)
})
```

The logging middleware runs after RBAC so that:
1. The `*proxy.Message` is already in the context (stored by `Authorize`)
2. 403 responses from `Authorize` are captured (status code recorded from wrapped writer, `Status: "denied"`)

---

### 10. Admin API — Logs Handler (`internal/admin/api/logs.go`)

```go
type logHandler struct {
    store  store.AuditLogStore
    pipeline *logging.Pipeline
    logger *slog.Logger
}
```

#### 10.1 `list` — `GET /api/v1/admin/logs`

Query params: `from`, `to` (RFC3339), `user_id`, `server_name`, `tool_name`, `method`, `status`, `search`, `page` (default 1), `page_size` (default 50, max 200).

Response:
```json
{
  "entries": [...],
  "total":   1234,
  "page":    1,
  "pages":   25
}
```

Validation: `page_size` clamped to 200. `from` must be before `to` if both provided.

#### 10.2 `export` — `GET /api/v1/admin/logs/export`

Query params: same filters as `list`, plus `format=json|csv` (default `json`).

Sets `Content-Disposition: attachment; filename="mcpgate-logs-<timestamp>.<ext>"` and streams the response. Uses `store.AuditLogStore.Export` which writes directly to `w`. No buffering in the handler.

#### 10.3 `stream` — `WS /api/v1/admin/logs/stream`

WebSocket upgrade using `gorilla/websocket`. After upgrade:
1. Call `pipeline.Subscribe(connID, 256)` — gets a channel and a cancel func.
2. Read goroutine: accept filter messages from client (JSON-encoded `LogFilter`); apply filters server-side before forwarding. If client closes, cancel subscription.
3. Write goroutine: range over the subscriber channel; marshal each `AuditLogEntry` to JSON and write as a WebSocket text message. On write error (client disconnected), cancel subscription.

**Backpressure:** subscriber channel buffer size is 256. If the client is slow, entries are dropped silently (the subscriber channel select uses `default:`).

**Filter application:** the subscription fan-out delivers all entries; the WS handler filters them in the write goroutine using the client-supplied `LogFilter` (same field matching as `Query`). This avoids a per-subscriber filter in the pipeline itself.

```go
func (h *logHandler) stream(w http.ResponseWriter, r *http.Request) {
    upgrader := websocket.Upgrader{
        CheckOrigin: func(r *http.Request) bool { return true }, // admin API already requires auth
    }
    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil {
        h.logger.Warn("ws upgrade failed", "err", err)
        return
    }
    defer conn.Close()

    connID := uuid.New().String()
    ch, cancel := h.pipeline.Subscribe(connID, 256)
    defer cancel()

    var filter store.LogFilter // client can send updated filter over the WS

    // Read goroutine: accept filter updates from client.
    go func() {
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                cancel()
                return
            }
            _ = json.Unmarshal(msg, &filter)
        }
    }()

    // Write goroutine: forward matching entries to client.
    for entry := range ch {
        if !matchesFilter(entry, filter) {
            continue
        }
        data, _ := json.Marshal(entry)
        if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
            return
        }
    }
}
```

#### 10.4 Router Changes (`internal/admin/api/router.go`)

Add `auditStore store.AuditLogStore` and `pipeline *logging.Pipeline` to `Router` struct.

Update `NewRouter` to accept these as new parameters (now 9 params total). Wire new routes:
```go
lh := &logHandler{store: rt.auditStore, pipeline: rt.pipeline, logger: rt.logger}
r.Get("/logs",          lh.list)
r.Get("/logs/export",   lh.export)
r.Get("/logs/stream",   lh.stream)  // WS upgrade inside handler
```

Remove the existing `r.Get("/logs", notImplemented)` stub.

---

### 11. Server Wiring (`internal/server/server.go`)

#### 11.1 New Fields

```go
type Server struct {
    // existing fields...
    auditStore store.AuditLogStore
    pipeline   *logging.Pipeline
    serverStore store.ServerStore // already present, expose for logging middleware
}
```

#### 11.2 `New()` Changes

After existing store instantiation:

```go
auditStore := pgstore.NewAuditLogStore(db)

// Initialise hash chain from latest row, or seed if table is empty.
initHash, err := auditStore.LatestHashChain(context.Background())
if err != nil && !errors.Is(err, store.ErrNotFound) {
    return nil, fmt.Errorf("init hash chain: %w", err)
}
if initHash == "" {
    initHash = cfg.Logging.Pipeline.HashChainSeed
}
if initHash == "" {
    initHash = "genesis"
}

primarySink := logSinks.NewPostgresSink(auditStore)

var fallbackSink logging.Sink
if cfg.Logging.Pipeline.WALPath != "" {
    fallbackSink = logSinks.NewWALSink(cfg.Logging.Pipeline.WALPath, logger)
}

pipeline := logging.NewPipeline(cfg.Logging.Pipeline, primarySink, fallbackSink, initHash, logger)
```

Add `LatestHashChain(ctx) (string, error)` to `AuditLogStore` (returns `ErrNotFound` if empty, not part of the interface contract — add as a concrete method on `*postgres.AuditLogStore` and call it here directly before assigning to the interface).

Wire `auditStore` and `pipeline` into `api.NewRouter()`. Store the pipeline on the `Server` struct.

#### 11.3 `buildRouter()` Changes

Add `logging.Middleware` to the MCP route group after RBAC:
```go
r.Route("/mcp/{serverName}", func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(rbac.Authorize(s.enforcer, s.logger))
    r.Use(logging.Middleware(s.pipeline, s.servers)) // NEW
    ...
})
```

#### 11.4 `Run()` Changes

Start the pipeline and background jobs when the server starts, stop them on shutdown:

```go
func (s *Server) Run() error {
    // Start audit log pipeline.
    s.pipeline.Start(ctx) // ctx from Run's lifecycle

    // Start JWKS client eviction.
    go s.jwksValidator.Start(ctx)

    // Start session cleanup goroutine.
    go s.sessionCleanup(ctx)

    // Start WAL replay goroutine (if WAL configured).
    if s.cfg.Logging.Pipeline.WALPath != "" {
        go s.walReplay(ctx)
    }

    ln, err := net.Listen("tcp", s.http.Addr)
    // ...existing listen/serve logic...

    // On shutdown:
    shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    _ = s.http.Shutdown(shutdownCtx)

    // Drain audit log pipeline after HTTP server stops accepting new requests.
    s.pipeline.Drain()

    s.logger.Info("server stopped")
    return nil
}
```

**Important:** `pipeline.Drain()` is called after `s.http.Shutdown()` — in-flight requests complete first, so all `Submit()` calls from handlers complete before the pipeline drains. This guarantees zero entry loss on graceful shutdown.

The current `Run()` doesn't use a context. A `context.WithCancel` will be created inside `Run()` and cancelled on the shutdown signal, so background goroutines stop cleanly.

---

## Test Plan

### Unit tests (no services, `-short` compatible)

| File | Tests |
|------|-------|
| `internal/logging/hashchain_test.go` | Single entry, chain of 10 entries (sequential hashes), tamper detection (modify one entry's content → chain breaks at that index), seed variation (different seeds produce different chains), marshal/unmarshal round-trip |
| `internal/logging/pipeline_test.go` | Submit below capacity → entry delivered to sink, Submit above capacity → dropped counter increments, Drain flushes pending entries before returning, Flush on timer tick, subscriber receives entries after successful sink write, subscriber drop on full channel |
| `internal/logging/entry_test.go` | BuildEntry with identity in context, BuildEntry with nil message (GET/SSE), status mapping (200→success, 403→denied, 502→error), ClientIP extraction (RemoteAddr with/without port) |
| `internal/logging/sinks/wal_test.go` | Write entries → file created, Replay → entries passed to primary, Replay on empty file → no-op, concurrent Write safety |

### Integration tests (`//go:build integration`)

| File | Tests |
|------|-------|
| `internal/store/postgres/audit_logs_test.go` | `BatchInsert` single and multiple rows, `Query` with each filter field, `Query` pagination, `Export` JSON format, `Export` CSV format, `VerifyHashChain` passes on intact chain, `VerifyHashChain` fails on tampered row |
| `tests/e2e/logging_test.go` | Proxy 10 requests → verify 10 entries in audit_logs with correct server_name, user_email, mcp_method; verify hash chain intact via `VerifyHashChain`; verify 403 from RBAC produces status="denied" entry; admin-only access to /logs |

### Carry-over unit tests

| File | Tests |
|------|-------|
| `internal/proxy/transport/stdio_test.go` (additions) | `Send` succeeds after child process exits and restarts, intentional `Close()` does not restart, concurrent `Send` during restart |
| `internal/auth/jwks/validator_test.go` (additions) | Eviction goroutine removes stale client, recently-used client is not evicted |

---

## Ordered File List (Implementation Order)

| # | File | Reason |
|---|------|--------|
| 1 | `internal/config/config.go` | Add `WALPath` and `HashChainSeed` fields before any Go code references them |
| 2 | `internal/proxy/context.go` | Context helpers needed by RBAC middleware (step 3) |
| 3 | `internal/auth/rbac/middleware.go` | Add `proxy.WithMessage(ctx, msg)` call in `Authorize` |
| 4 | `internal/auth/context.go` | Add `IdentityFromContextOrZero` helper |
| 5 | `internal/logging/hashchain.go` | No upstream deps; store depends on it |
| 6 | `internal/logging/hashchain_test.go` | Unit test |
| 7 | `internal/logging/entry.go` | Depends on hashchain types; middleware depends on entry |
| 8 | `internal/logging/entry_test.go` | Unit test |
| 9 | `internal/store/postgres/audit_logs.go` | Required by PostgresSink and admin API handler |
| 10 | `internal/store/postgres/audit_logs_test.go` | Integration test |
| 11 | `internal/logging/sinks/postgres.go` | Wraps AuditLogStore |
| 12 | `internal/logging/sinks/wal.go` | WAL fallback |
| 13 | `internal/logging/sinks/wal_test.go` | Unit test |
| 14 | `internal/logging/pipeline.go` | Core pipeline — middleware depends on it |
| 15 | `internal/logging/pipeline_test.go` | Unit test |
| 16 | `internal/logging/middleware.go` | Depends on pipeline and entry builder |
| 17 | `internal/admin/api/logs.go` | Depends on AuditLogStore and Pipeline |
| 18 | `internal/admin/api/router.go` | Wire /logs routes, remove notImplemented stub |
| 19 | `internal/server/server.go` | Wire everything: AuditLogStore, Pipeline, logging middleware, background jobs |
| 20 | `internal/proxy/transport/stdio.go` | Carry-over C1: auto-restart |
| 21 | `internal/auth/jwks/validator.go` | Carry-over C2: client eviction |
| 22 | `tests/e2e/logging_test.go` | E2E: verify log completeness and hash chain |
| 23 | `go get github.com/gorilla/websocket && go build ./...` | Verify clean build + lint |

---

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Single writer goroutine despite `WriterCount: 4` in config | Hash chain requires sequential processing. Multiple parallel writers would need distributed sequence coordination. Config field reserved for a future sharded chain design. |
| D2 | Hash chain computed in the writer goroutine, not at Submit time | Submit must be non-blocking (called from request handlers). Hash computation requires knowledge of the previous hash, which changes with every batch. Only the writer, which processes entries sequentially, can compute this correctly. |
| D3 | Use multi-row INSERT (not pgx COPY) for `BatchInsert` | COPY bypasses triggers and sequence generation; the `audit_logs` table uses `BIGSERIAL` for `id` and is partitioned by timestamp. Multi-row INSERT is slightly slower but correct and simpler. |
| D4 | Logging middleware runs after RBAC, not before | RBAC middleware stores the parsed `*proxy.Message` in context. Running logging after RBAC ensures the method and tool name are available. 403 responses from RBAC are still captured (the wrapped ResponseWriter records the status). |
| D5 | WebSocket stream uses subscription fan-out from the pipeline, not DB polling | DB polling (every 1s) would add write amplification and latency. Fan-out from the in-memory pipeline delivers entries within 100ms of the flush interval with no additional DB reads. |
| D6 | WebSocket filter applied in the WS handler goroutine, not in the pipeline | The pipeline fan-out is simpler if it delivers all entries to all subscribers. Per-subscriber filter logic in the WS handler keeps the pipeline implementation generic and avoids coupling it to log filtering rules. |
| D7 | WAL fallback is optional (enabled by `WALPath`) | WAL adds operational complexity (file rotation, replay, disk pressure). Most deployments will have a redundant PostgreSQL setup and don't need it. Sprint 5 ships it as an opt-in feature; Sprint 10 chaos testing validates the replay path. |
| D8 | `pipeline.Drain()` called after `http.Shutdown()` | This ordering guarantees all in-flight request handlers complete (and call `Submit()`) before the pipeline drains. Zero entries are lost on graceful shutdown. |
| D9 | `resolveServerID` uses a 50ms timeout | Server config lookup has a 30s TTL cache in the proxy handler. A stale cache hit is possible but unlikely during graceful shutdown. A 50ms timeout prevents the logging middleware from holding up the response on a slow DB. `uuid.Nil` is an acceptable fallback. |
| D10 | `gorilla/websocket` for WS upgrade | Mature, widely-used, stable API. `nhooyr.io/websocket` would also work but adds an unconventional dependency. `gorilla/websocket` is the de-facto standard for chi-based servers. |
| D11 | stdio restart resets `t.done` to a new channel | A closed channel returns immediately on every `<-t.done` read. If we kept the closed channel, every new `Send()` would immediately see "process exited". Creating a new `done` channel on reset allows the next `Send()` to block correctly waiting for a response. |
| D12 | JWKS client eviction uses `lastUsed` rather than evicting on config deletion | We can't efficiently detect when a config is deleted from the DB (we'd need pub/sub or polling). `lastUsed` eviction handles both config deletion (client becomes stale when the config is disabled/deleted and no new tokens arrive) and memory pressure from long-lived deployments with many issuers. |

---

## Security Properties Delivered

| Threat | Mitigation |
|--------|-----------|
| Tampered or deleted audit log entries | SHA-256 hash chain: any modification to an entry's content or deletion of a row breaks the chain, detectable via `VerifyHashChain` |
| Log injection (malicious user crafts a log entry) | `AuditLogEntry` is built server-side from authenticated context values; user-controlled input (MCP method, tool name) is stored as data, not interpreted |
| Missing entries when pipeline is overloaded | `dropped` counter incremented and logged; WAL fallback (if configured) captures entries that fail the primary sink write |
| Admin log API accessible to non-admins | `/api/v1/admin/logs*` routes are inside the admin group protected by `RequireRole("admin")` |
| WebSocket stream leaks logs to unauthorised clients | WS route is inside the admin group; the WebSocket upgrade only happens after `RequireRole("admin")` has passed |
| Hash chain seed predictability | Seed is configurable; operators should set a random value in production. Default "genesis" is acceptable for development. |

---

## Deferred Items

| Item | Reason for Deferral |
|------|-------------------|
| Multi-IdP SAML dispatch (T3) | Separate feature, not related to logging; Sprint 6 |
| SAML SLO (T4) | Low priority; Sprint 6 |
| Full bidirectional SSE proxy (T8) | Requires upstream SSE connection management and a channel registry; Sprint 6 |
| Brute-force on MCP/admin endpoints (T9) | Sprint 6 alongside rate limiting |
| gosec G115 nolint review (T22) | Sprint 6 housekeeping pass |
| Prometheus metrics on pipeline (dropped counter, batch size histogram, flush latency) | Sprint 6 (Metrics sprint) |
| pg_partman or monthly partition cron for `audit_logs` (T21) | Pre-launch ops task |
| Admin log unit tests at the handler layer | Sprint 5 if time allows, otherwise open |
| `GET /api/v1/me` returning `uuid.Nil` for M2M tokens (T18) | Low priority; surfaced in the UI sprint when the admin console renders user IDs |

---

## Verification

```bash
# Unit tests (no services)
go test -race -count=1 -short ./internal/...

# Full integration suite (Docker required)
make docker-up && make migrate-up
go test -race -count=1 -tags=integration -timeout=120s ./...

# Verify audit_logs table receives entries
ADMIN_TOKEN="<token>"
curl -s -X POST http://localhost:8080/mcp/any-server/message \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | jq .
curl -s http://localhost:8080/api/v1/admin/logs \
  -H "Authorization: Bearer $ADMIN_TOKEN" | jq '.entries[0]'
# → entry with mcp_method="tools/list", status="success", hash_chain="<hex>"

# Verify hash chain integrity
psql "$DATABASE_URL" -c "
  SELECT id, mcp_method, status, LEFT(hash_chain, 16) AS hash_prefix
  FROM audit_logs ORDER BY timestamp ASC LIMIT 5;"

# Verify WAL fallback (optional)
# Stop Postgres, send a request, confirm WAL file created, restart Postgres, confirm replay

# WebSocket tail
ADMIN_TOKEN="<token>"
websocat ws://localhost:8080/api/v1/admin/logs/stream \
  -H "Authorization: Bearer $ADMIN_TOKEN"
# → new log entries appear in real-time as requests are proxied

# Export CSV
curl -s "http://localhost:8080/api/v1/admin/logs/export?format=csv" \
  -H "Authorization: Bearer $ADMIN_TOKEN" > logs.csv

# Non-admin blocked from logs
DEV_TOKEN="<developer token>"
curl -si http://localhost:8080/api/v1/admin/logs \
  -H "Authorization: Bearer $DEV_TOKEN"
# → HTTP 403

# stdio restart (manual test)
# 1. Register a stdio server, send a request (succeeds)
# 2. Kill the child process externally
# 3. Send another request → should succeed (process restarted)

# Session cleanup (verify via metrics or log output)
# grep "session cleanup" in server logs after 24h (or shorten interval for testing)
```
