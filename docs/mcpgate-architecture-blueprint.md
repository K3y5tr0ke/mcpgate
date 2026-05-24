# mcpgate — Architectural Blueprint

**Version:** 1.0  
**Date:** May 22, 2026  
**Companion to:** MCP Proxy Gateway PRD v1.0, Development Plan v1.0

---

## 1. Architectural Principles

This section codifies the decisions that constrain the design. Every component in mcpgate traces back to one or more of these principles.

**Protocol transparency.** The proxy must not alter MCP message semantics. Clients and upstream servers must behave identically whether the proxy is present or absent. The proxy operates at the transport layer, not the application-protocol layer. This means mcpgate never modifies, reorders, or synthesizes MCP messages — it only inspects them enough to make routing, auth, and authorization decisions.

**Defense in depth.** No single layer is trusted to enforce security alone. Authentication (is this a known user?) and authorization (is this user allowed to do this?) are separate middleware stages. The proxy, the admin API, and the admin console each enforce access control independently. Secrets are encrypted at rest and in transit.

**Observability as a first-class citizen.** Every request that passes through the proxy produces a structured log entry, updates metrics counters, and participates in a distributed trace. The logging pipeline is designed to never silently drop entries, even under backpressure.

**Fail-open for availability, fail-closed for security.** If Redis is unavailable, rate limiting becomes permissive (fail-open) so that legitimate requests continue flowing. However, if the auth middleware cannot validate a JWT, the request is rejected (fail-closed). This asymmetry is intentional: an authorization bypass is worse than a brief period without rate limits.

**Modular middleware chain.** The proxy handler is composed from a chain of middleware functions, each responsible for one concern (auth, RBAC, rate limiting, metrics, logging). Middleware can be added, removed, or reordered via configuration. Each middleware is independently testable.

**Stateless proxy, stateful services.** The proxy process itself holds no request state — all state lives in PostgreSQL (durable) or Redis (ephemeral). This allows horizontal scaling: multiple mcpgate instances behind a load balancer share the same database and cache.

---

## 2. System Context

```
┌─────────────────────────────────────────────────────────────────────────┐
│                           External Systems                              │
│                                                                         │
│  ┌──────────────┐  ┌──────────────┐  ┌────────────────┐               │
│  │  SAML IdP    │  │  Prometheus  │  │  OTLP          │               │
│  │  (Okta,      │  │  Server      │  │  Collector     │               │
│  │   Azure AD)  │  │              │  │  (Jaeger, etc) │               │
│  └──────┬───────┘  └──────┬───────┘  └───────┬────────┘               │
│         │                 │                   │                         │
│         │ SAML 2.0        │ /metrics scrape   │ OTLP gRPC              │
│         │ (HTTPS)         │ (HTTP)            │ (gRPC/HTTP)            │
└─────────┼─────────────────┼───────────────────┼────────────────────────┘
          │                 │                   │
          ▼                 ▼                   ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                                                                         │
│                        mcpgate Process                                  │
│                                                                         │
│   Port 8080: Proxy + Auth + Admin API + Admin Console (static assets)  │
│   Port 9090: Prometheus Metrics                                         │
│                                                                         │
└──────────┬──────────────────────────────────────┬───────────────────────┘
           │                                      │
           │ pgx (TCP 5432)                       │ go-redis (TCP 6379)
           ▼                                      ▼
     ┌───────────┐                          ┌───────────┐
     │PostgreSQL │                          │   Redis   │
     │  16       │                          │   7       │
     └───────────┘                          └───────────┘

           │
           │  stdio / HTTP / SSE
           ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                      Upstream MCP Servers                                │
│                                                                         │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │Filesystem│  │  GitHub  │  │  Slack   │  │ Database │  ...          │
│  │  (stdio) │  │  (HTTP)  │  │  (HTTP)  │  │  (stdio) │              │
│  └──────────┘  └──────────┘  └──────────┘  └──────────┘              │
└─────────────────────────────────────────────────────────────────────────┘
```

### Port Allocation

| Port | Protocol | Purpose | Auth Required |
|------|----------|---------|--------------|
| 8080 | HTTPS | Proxy endpoints, auth endpoints, admin API, admin console | Varies by path |
| 9090 | HTTP | Prometheus metrics exposition | IP allowlist (no JWT) |

A single port (8080) serves all client-facing traffic. This simplifies TLS termination, load balancer configuration, and firewall rules. The metrics port (9090) is separate because Prometheus scrape targets typically bypass application-level auth.

---

## 3. Module Architecture

### 3.1 Module Dependency Graph

```
cmd/mcpgate/main.go
    │
    ├── internal/config          (no internal deps)
    ├── internal/server          → config, proxy, auth, rbac, admin, metrics, logging
    │
    ├── internal/proxy
    │   ├── handler.go           → transport, session, jsonrpc
    │   ├── jsonrpc.go           (no internal deps)
    │   ├── session.go           → store/redis, store/postgres
    │   ├── transport/
    │   │   ├── streamable.go    → pool
    │   │   ├── sse.go           → pool
    │   │   ├── stdio.go         (no internal deps)
    │   │   └── pool.go          (no internal deps)
    │   ├── circuit/breaker.go   (no internal deps)
    │   └── health/checker.go    → store/postgres, metrics
    │
    ├── internal/auth
    │   ├── middleware.go         → auth/oauth (JWT validation)
    │   ├── saml/
    │   │   ├── sp.go            → store/postgres
    │   │   ├── handlers.go      → sp, session, store/postgres
    │   │   └── attribute_map.go (no internal deps)
    │   └── oauth/
    │       ├── server.go        → store/postgres
    │       ├── handlers.go      → server
    │       └── jwt.go           (no internal deps)
    │
    ├── internal/rbac
    │   ├── enforcer.go          → store/postgres (casbin adapter)
    │   ├── middleware.go         → enforcer, store/redis, proxy/jsonrpc
    │   └── policies.go          → enforcer, store/postgres
    │
    ├── internal/logging
    │   ├── pipeline.go          → sinks
    │   ├── entry.go             (no internal deps)
    │   ├── hashchain.go         (no internal deps)
    │   └── sinks/
    │       ├── postgres.go      → store/postgres
    │       ├── otlp.go          (external: OTLP SDK)
    │       └── file.go          (no internal deps)
    │
    ├── internal/metrics
    │   └── collector.go         (external: prometheus/client_golang)
    │
    ├── internal/admin/api
    │   ├── router.go            → all admin handlers
    │   ├── servers.go           → store/postgres
    │   ├── users.go             → store/postgres
    │   ├── roles.go             → rbac, store/postgres
    │   ├── logs.go              → store/postgres
    │   ├── metrics.go           → store/postgres, store/redis
    │   ├── saml.go              → auth/saml, store/postgres
    │   ├── oauth.go             → auth/oauth, store/postgres
    │   ├── settings.go          → config, store/postgres
    │   └── websocket.go         → logging/pipeline
    │
    └── internal/store
        ├── postgres/
        │   ├── users.go         (no internal deps)
        │   ├── servers.go       (no internal deps)
        │   ├── logs.go          (no internal deps)
        │   ├── sessions.go      (no internal deps)
        │   └── migrations/      (SQL files, no Go deps)
        └── redis/
            ├── session_cache.go (no internal deps)
            ├── rate_limiter.go  (no internal deps)
            └── policy_cache.go  (no internal deps)
```

### 3.2 Dependency Rules

1. **Downward only.** Modules may depend on modules below them in the graph, never above. `store` packages have no internal dependencies. `proxy`, `auth`, `rbac`, `logging`, and `metrics` depend on `store` but not on each other (except through the middleware chain in `server`).
2. **No circular imports.** Go enforces this at compile time, but the architecture is designed to prevent it structurally.
3. **Interfaces at boundaries.** Every store package exports an interface (e.g., `UserStore`, `ServerStore`, `SessionCache`). Higher-level modules depend on the interface, not the concrete implementation. This enables testing with mocks and future database swaps.
4. **External libraries are isolated.** Third-party libraries (Casbin, crewjam/saml, ory/fosite) are wrapped in internal packages. No external library type appears in a module's exported interface. If a library is replaced, only its wrapper changes.

---

## 4. Request Processing Pipeline

### 4.1 Proxy Request Flow

This is the hot path — the critical performance path that every MCP request traverses.

```
Client HTTP Request
    │
    ▼
┌──────────────────────────────────┐
│  1. TLS Termination (net/http)   │  Go stdlib TLS with configured cert/key.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  2. Recovery Middleware          │  Catches panics, logs stack trace, returns 500.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  3. Request ID Middleware        │  Generates UUID v7 request ID. Sets X-Request-ID
│                                  │  header. Injects into context for logging/tracing.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  4. Tracing Middleware (OTel)    │  Starts root span. Extracts or generates trace ID.
│                                  │  Propagates W3C trace context.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  5. Metrics Middleware           │  Records: request start time, increments
│                                  │  active_connections gauge.
│                                  │  On response: observes latency histogram,
│                                  │  increments requests_total counter.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  6. Auth Middleware              │  Extracts JWT from Authorization header
│                                  │  or session cookie. Validates signature,
│                                  │  expiry, issuer. Injects UserContext
│                                  │  (user_id, email, roles, session_id)
│                                  │  into request context.
│                                  │  FAIL → 401 Unauthorized (end of chain)
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  7. Rate Limit Middleware        │  Checks per-user and per-role rate limits
│                                  │  in Redis (sliding window).
│                                  │  EXCEEDED → MCP error -32000 + Retry-After
│                                  │  REDIS DOWN → pass through (fail-open)
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  8. Server Resolution           │  Extracts {server_name} from URL path.
│                                  │  Looks up server config (PostgreSQL, cached).
│                                  │  NOT FOUND → 404
│                                  │  DISABLED → MCP error -32603
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│  9. Message Parsing              │  Reads request body. Parses JSON-RPC 2.0
│                                  │  envelope: method, id, params.name.
│                                  │  MALFORMED → MCP error -32600
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│ 10. RBAC Middleware              │  Evaluates Casbin policy:
│                                  │  (user_role, server_name, tool_name, action)
│                                  │  ALLOW → continue
│                                  │  DENY → MCP error -32600 "Unauthorized"
│                                  │  Logs decision regardless.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│ 11. Circuit Breaker Check        │  Checks circuit state for target server.
│                                  │  OPEN → MCP error -32603 "Server unavailable"
│                                  │  CLOSED/HALF-OPEN → continue
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│ 12. Upstream Transport           │  Dispatches request body to upstream server
│                                  │  via the appropriate transport adapter
│                                  │  (Streamable HTTP, SSE, or stdio bridge).
│                                  │  Waits for response (with timeout).
│                                  │  Records circuit breaker success/failure.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│ 13. Response Relay               │  Writes upstream response body to client.
│                                  │  For streaming (SSE), relays events as they
│                                  │  arrive. Copies headers. Sets status code.
└──────────────┬───────────────────┘
               │
               ▼
┌──────────────────────────────────┐
│ 14. Audit Log Emission           │  Constructs AuditLogEntry with all metadata.
│                                  │  Pushes to log pipeline channel (non-blocking).
│                                  │  This is the LAST step — never blocks response.
└──────────────────────────────────┘
```

### 4.2 Middleware Chain Implementation

```go
// internal/server/server.go — middleware chain assembly

func (s *Server) buildProxyRouter() http.Handler {
    r := chi.NewRouter()

    // Global middleware (applied to all routes)
    r.Use(middleware.Recoverer(s.logger))
    r.Use(middleware.RequestID)
    r.Use(middleware.Tracing(s.tracer))
    r.Use(middleware.Metrics(s.metrics))

    // Auth-exempt routes
    r.Group(func(r chi.Router) {
        r.Get("/health", s.handleHealth)
        r.Get("/ready", s.handleReady)
        r.Mount("/auth", s.authRouter())
    })

    // Authenticated proxy routes
    r.Group(func(r chi.Router) {
        r.Use(s.authMiddleware.Authenticate)
        r.Use(s.rateLimiter.Limit)

        r.Route("/mcp/{serverName}", func(r chi.Router) {
            r.Use(s.serverResolver.Resolve)
            r.Post("/message", s.proxyHandler.HandleStreamableHTTP)
            r.Get("/sse", s.proxyHandler.HandleSSEStream)
            r.Post("/sse", s.proxyHandler.HandleSSEMessage)
        })
    })

    // Admin API routes (authenticated + admin role required)
    r.Group(func(r chi.Router) {
        r.Use(s.authMiddleware.Authenticate)
        r.Use(s.authMiddleware.RequireRole("admin"))
        r.Mount("/api/v1/admin", s.adminRouter())
    })

    // Static assets (admin console)
    r.Handle("/console/*", http.StripPrefix("/console",
        http.FileServer(http.Dir(s.config.Admin.WebRoot))))

    return r
}
```

### 4.3 Context Propagation

Every request carries context values through the middleware chain:

```go
// internal/auth/context.go

type contextKey string

const (
    UserContextKey    contextKey = "user"
    SessionContextKey contextKey = "session"
    RequestIDKey      contextKey = "request_id"
    TraceIDKey        contextKey = "trace_id"
    ServerContextKey  contextKey = "server"
)

type UserContext struct {
    UserID    uuid.UUID
    Email     string
    Roles     []string
    SessionID uuid.UUID
}

type ServerContext struct {
    ServerID  uuid.UUID
    Name      string
    Transport string
    Config    ServerConfig
}

// Helpers
func UserFromContext(ctx context.Context) *UserContext { ... }
func ServerFromContext(ctx context.Context) *ServerContext { ... }
func RequestIDFromContext(ctx context.Context) string { ... }
```

---

## 5. Authentication Architecture

### 5.1 SAML Authentication Flow

```
┌──────┐          ┌──────────┐          ┌──────────┐
│Client│          │ mcpgate  │          │  SAML    │
│      │          │          │          │  IdP     │
└──┬───┘          └────┬─────┘          └────┬─────┘
   │                   │                     │
   │  GET /auth/saml/login                   │
   │──────────────────>│                     │
   │                   │                     │
   │                   │  Generate AuthnRequest (signed, with RelayState)
   │                   │                     │
   │  302 Redirect to IdP SSO URL           │
   │<──────────────────│                     │
   │                   │                     │
   │  GET IdP SSO URL (with SAMLRequest)     │
   │────────────────────────────────────────>│
   │                   │                     │
   │         User authenticates at IdP       │
   │<────────────────────────────────────────│
   │                   │                     │
   │  POST /auth/saml/acs (SAMLResponse)     │
   │──────────────────>│                     │
   │                   │                     │
   │                   │  Validate:
   │                   │   - XML signature (RSA-SHA256)
   │                   │   - Audience restriction (mcpgate entity ID)
   │                   │   - Time conditions (NotBefore / NotOnOrAfter)
   │                   │   - Replay (assertion ID not seen before, Redis)
   │                   │                     │
   │                   │  Extract:
   │                   │   - NameID → user identifier
   │                   │   - Attributes → email, display_name, groups
   │                   │                     │
   │                   │  Provision:
   │                   │   - Create/update user in PostgreSQL (JIT)
   │                   │   - Map IdP groups → mcpgate roles
   │                   │                     │
   │                   │  Session:
   │                   │   - Create session (PostgreSQL + Redis)
   │                   │   - Issue JWT (RS256, claims: sub, email, roles,
   │                   │     session_id, exp, iat, iss)
   │                   │                     │
   │  302 Redirect to /console (Set-Cookie: jwt=...)
   │<──────────────────│                     │
   │                   │                     │
   │  Subsequent requests include JWT        │
   │  (Authorization: Bearer <jwt>           │
   │   or Cookie: mcpgate_session=<jwt>)     │
   │──────────────────>│                     │
```

### 5.2 Auth0 Machine-to-Machine (M2M) Flow

Service accounts (AI agents, CI/CD pipelines) authenticate directly with Auth0 using the OAuth 2.0 Client Credentials grant. mcpgate does **not** host a token endpoint — it only validates the resulting JWT by fetching Auth0's JWKS public keys.

```
┌──────────┐        ┌──────────┐        ┌──────────┐
│  Service  │        │  Auth0   │        │ mcpgate  │
│  Account  │        │          │        │          │
└────┬──────┘        └────┬─────┘        └────┬─────┘
     │                    │                   │
     │  POST /oauth/token │                   │
     │  grant_type=client_credentials         │
     │  client_id=<id>    │                   │
     │  client_secret=<s> │                   │
     │  audience=<api>    │                   │
     │───────────────────>│                   │
     │                    │                   │
     │  200 OK            │                   │
     │  { "access_token": "<jwt>",            │
     │    "token_type": "Bearer",             │
     │    "expires_in": 86400 }               │
     │<───────────────────│                   │
     │                    │                   │
     │  POST /mcp/server/message              │
     │  Authorization: Bearer <auth0-jwt>     │
     │────────────────────────────────────────>
     │                    │                   │
     │                    │  Validate JWT via  │
     │                    │  Auth0 JWKS        │
     │                    │  (cached, 1h TTL)  │
     │                    │                   │
     │  200 OK (proxied response)             │
     │<────────────────────────────────────────
```

Auth0 M2M applications are created and managed in the Auth0 dashboard. mcpgate trusts tokens issued by the configured Auth0 tenant (`iss` claim) and validates the audience (`aud`) against a configured value.

### 5.3 JWT Structure

mcpgate handles two distinct JWT types. The middleware checks the `iss` claim to route validation.

**mcpgate session JWT** (issued after SAML login, RS256, ephemeral key in dev):

```json
{
  "header": { "alg": "RS256", "typ": "JWT", "kid": "mcpgate-signing-key-2026" },
  "payload": {
    "iss": "mcpgate-dev",
    "sub": "550e8400-e29b-41d4-a716-446655440000",
    "email": "jane.doe@company.com",
    "roles": ["developer", "viewer"],
    "session_id": "01902e8a-1234-7890-abcd-ef1234567890",
    "iat": 1748000000,
    "exp": 1748000900,
    "nbf": 1748000000
  }
}
```

**Auth0 M2M JWT** (issued by Auth0 Client Credentials grant, validated via Auth0 JWKS):

```json
{
  "header": { "alg": "RS256", "typ": "JWT", "kid": "<auth0-key-id>" },
  "payload": {
    "iss": "https://dev-<tenant>.us.auth0.com/",
    "sub": "<client_id>@clients",
    "aud": "<api-identifier>",
    "permissions": ["mcp:proxy"],
    "iat": 1748000000,
    "exp": 1748086400
  }
}
```

### 5.4 Key Management

| Key | Algorithm | Storage | Rotation |
|-----|----------|---------|----------|
| JWT signing key | RSA 2048-bit (RS256) | PEM file on disk or secret store | Manual rotation. Old key accepted for validation during grace period (configurable, default 24h). JWKS endpoint serves both keys during rotation. |
| SAML SP signing key | RSA 2048-bit (RSA-SHA256) | PEM file on disk | Manual rotation. New SP metadata must be re-imported into IdP. |
| SAML SP encryption key | RSA 2048-bit (RSA-OAEP) | PEM file on disk | Same as signing key (can be same key pair or separate). |

---

## 6. Authorization Architecture

### 6.1 Casbin Model

```
[request_definition]
r = sub, obj_server, obj_tool, act

[policy_definition]
p = sub, obj_server, obj_tool, act, eft

[role_definition]
g = _, _

[policy_effect]
e = some(where (p.eft == allow)) && !some(where (p.eft == deny))

[matchers]
m = g(r.sub, p.sub) && \
    (p.obj_server == "*" || r.obj_server == p.obj_server) && \
    (p.obj_tool == "*" || r.obj_tool == p.obj_tool) && \
    (p.act == "*" || r.act == p.act)
```

### 6.2 Policy Examples

```csv
# ptype, sub (role), obj_server, obj_tool, act, eft

# super_admin: full access to everything
p, super_admin, *, *, *, allow

# admin: full access to all MCP servers, plus admin API
p, admin, *, *, *, allow

# developer: can list and call tools on assigned servers
p, developer, github-mcp, *, list, allow
p, developer, github-mcp, *, call, allow
p, developer, filesystem-mcp, *, list, allow
p, developer, filesystem-mcp, read_file, call, allow
p, developer, filesystem-mcp, write_file, call, deny   # explicit deny

# viewer: can list tools and read resources, cannot call
p, viewer, *, *, list, allow
p, viewer, *, *, read, allow
p, viewer, *, *, call, deny

# Role hierarchy
g, super_admin, admin
g, admin, developer
g, developer, viewer
```

### 6.3 Policy Evaluation Sequence

```
1. Auth middleware extracts roles from JWT → ["developer"]
2. RBAC middleware extracts from MCP message:
   - server_name: "filesystem-mcp" (from URL path)
   - tool_name: "write_file" (from params.name in tools/call)
   - action: "call" (derived from method "tools/call")
3. Check Redis cache: key = "rbac:developer:filesystem-mcp:write_file:call"
   - HIT → return cached decision
   - MISS → continue to step 4
4. Casbin enforcer evaluates:
   Enforce("developer", "filesystem-mcp", "write_file", "call")
   - Checks developer policies:
     p, developer, filesystem-mcp, write_file, call, deny → DENY match
   - Policy effect: has allow AND has deny → deny wins
   - Result: DENY
5. Cache result in Redis (TTL: 30s)
6. Return MCP error: {"jsonrpc":"2.0","id":1,"error":{"code":-32600,"message":"Unauthorized: role 'developer' cannot call 'write_file' on 'filesystem-mcp'"}}
7. Log: RBAC denial with full context
```

### 6.4 Action Derivation from MCP Methods

| MCP Method | Derived Action | Notes |
|-----------|---------------|-------|
| `tools/list` | `list` | Discovery — usually allowed broadly |
| `tools/call` | `call` | Execution — primary authorization target |
| `resources/list` | `list` | Resource discovery |
| `resources/read` | `read` | Resource content access |
| `resources/subscribe` | `read` | Treated as read access |
| `prompts/list` | `list` | Prompt discovery |
| `prompts/get` | `read` | Prompt content access |
| `logging/setLevel` | `admin` | Administrative action |
| `initialize` | `connect` | Connection establishment (usually allowed) |
| `ping` | `connect` | Health check (always allowed) |
| Unknown method | `unknown` | Deny by default |

---

## 7. Transport Adapter Architecture

### 7.1 Transport Interface

All transport adapters implement a common interface:

```go
// internal/proxy/transport/transport.go

type Transport interface {
    // Send sends a JSON-RPC message to the upstream server and returns the response.
    // For streaming transports, the response may be delivered via the provided channel.
    Send(ctx context.Context, message []byte) ([]byte, error)

    // Stream establishes a persistent connection for server-initiated messages.
    // Messages are sent to the provided channel. Blocks until ctx is cancelled.
    Stream(ctx context.Context, messages chan<- []byte) error

    // Close gracefully shuts down the transport connection.
    Close() error

    // Health returns the current health status of the upstream connection.
    Health() HealthStatus
}

type HealthStatus struct {
    Healthy   bool
    LastCheck time.Time
    LastError error
    Latency   time.Duration
}
```

### 7.2 Streamable HTTP Adapter

```
Client                    mcpgate                   Upstream Server
  │                         │                              │
  │ POST /mcp/srv/message   │                              │
  │ Content-Type:           │                              │
  │   application/json      │                              │
  │ Body: {"jsonrpc":...}   │                              │
  │────────────────────────>│                              │
  │                         │  POST <upstream_url>         │
  │                         │  Body: {"jsonrpc":...}       │
  │                         │─────────────────────────────>│
  │                         │                              │
  │                         │  200 OK                      │
  │                         │  Content-Type:               │
  │                         │    application/json          │
  │                         │  (or text/event-stream       │
  │                         │   for streaming response)    │
  │                         │<─────────────────────────────│
  │  200 OK                 │                              │
  │  Body: {"jsonrpc":...}  │                              │
  │<────────────────────────│                              │
```

For streaming responses (Content-Type: text/event-stream), the adapter relays SSE events from the upstream server to the client in real-time, flushing after each event.

### 7.3 SSE Adapter

```
Client                    mcpgate                   Upstream Server
  │                         │                              │
  │ GET /mcp/srv/sse        │                              │
  │ Accept: text/event-     │                              │
  │   stream                │                              │
  │────────────────────────>│                              │
  │                         │  GET <upstream_sse_url>      │
  │                         │─────────────────────────────>│
  │                         │                              │
  │  SSE: event: endpoint   │  SSE: event: endpoint       │
  │  data: /mcp/srv/sse     │  data: <upstream_post_url>  │
  │<────────────────────────│<─────────────────────────────│
  │                         │                              │
  │ POST /mcp/srv/sse       │                              │
  │ Body: {"jsonrpc":...}   │                              │
  │────────────────────────>│                              │
  │                         │  POST <upstream_post_url>    │
  │                         │  Body: {"jsonrpc":...}       │
  │                         │─────────────────────────────>│
  │                         │                              │
  │  SSE: event: message    │  SSE: event: message         │
  │  data: {"jsonrpc":...}  │  data: {"jsonrpc":...}      │
  │<────────────────────────│<─────────────────────────────│
```

### 7.4 stdio Bridge

```
mcpgate Process
    │
    ├── stdio Process Manager
    │   │
    │   ├── Process Pool (max_concurrent configurable per server)
    │   │   │
    │   │   ├── Process 1: npx @mcp/server-filesystem /data
    │   │   │   stdin  ◄── JSON-RPC messages (one per line)
    │   │   │   stdout ──► JSON-RPC responses (one per line)
    │   │   │   stderr ──► Captured to log (level: warn)
    │   │   │
    │   │   ├── Process 2: (idle, waiting for assignment)
    │   │   │
    │   │   └── Process N: ...
    │   │
    │   ├── Process Lifecycle
    │   │   - Start: spawn process, wait for initialize response
    │   │   - Health: periodic ping, track consecutive failures
    │   │   - Restart: kill + respawn after N failures
    │   │   - Stop: send SIGTERM, wait grace period, SIGKILL
    │   │
    │   └── Session Binding
    │       - Each client session is bound to one process
    │       - Session end → process returned to pool
    │       - Process crash → session gets new process, re-initialize
```

### 7.5 Connection Pool Design

```go
// internal/proxy/transport/pool.go

type Pool struct {
    mu          sync.Mutex
    server      ServerConfig
    connections []*pooledConn
    maxSize     int
    idleTimeout time.Duration
    waiters     []chan *pooledConn
    metrics     *PoolMetrics
}

type PoolMetrics struct {
    Active  int64  // Currently in use
    Idle    int64  // Available for reuse
    Waiting int64  // Goroutines waiting for a connection
    Total   int64  // Created since startup
}

// Get acquires a connection from the pool.
// Blocks if pool is at max capacity until one is returned or ctx is cancelled.
func (p *Pool) Get(ctx context.Context) (*pooledConn, error)

// Put returns a connection to the pool.
// If the connection is unhealthy, it is discarded and the pool creates a replacement.
func (p *Pool) Put(conn *pooledConn)

// Drain closes all connections and prevents new ones from being created.
func (p *Pool) Drain()
```

---

## 8. Logging Pipeline Architecture

### 8.1 Pipeline Data Flow

```
┌──────────────────┐
│  Proxy Handler   │
│  (hot path)      │
│                  │
│  Build log entry │
│  Push to channel │─── non-blocking send ───┐
│  (< 1µs)        │                          │
└──────────────────┘                          │
                                              ▼
                                 ┌────────────────────────┐
                                 │   Buffered Channel     │
                                 │   capacity: 10,000     │
                                 │   type: AuditLogEntry  │
                                 └────────────┬───────────┘
                                              │
                          ┌───────────────────┼───────────────────┐
                          │                   │                   │
                          ▼                   ▼                   ▼
                   ┌─────────────┐   ┌─────────────┐   ┌─────────────┐
                   │  Writer     │   │  Writer     │   │  Writer     │
                   │  Goroutine  │   │  Goroutine  │   │  Goroutine  │
                   │  #1         │   │  #2         │   │  #3 / #4    │
                   └──────┬──────┘   └──────┬──────┘   └──────┬──────┘
                          │                 │                  │
                          ▼                 ▼                  ▼
                   ┌───────────────────────────────────────────────┐
                   │              Batch Accumulator                │
                   │  Flush when: batch_size reached (500)         │
                   │           OR flush_interval elapsed (100ms)   │
                   │           OR pipeline is draining             │
                   └──────────────────────┬────────────────────────┘
                                          │
                          ┌───────────────┼───────────────┐
                          ▼               │               ▼
                   ┌─────────────┐        │        ┌─────────────┐
                   │  PostgreSQL │        │        │  OTLP       │
                   │  Sink       │        │        │  Export Sink │
                   │  (COPY)     │        │        │  (async)    │
                   └──────┬──────┘        │        └─────────────┘
                          │               │
                     ┌────┴────┐          │
                     │ Success │          │
                     └─────────┘          │
                          │               │
                     ┌────┴────┐     ┌────┴────┐
                     │ Failure │────>│  WAL    │
                     └─────────┘     │  File   │
                                     │  Sink   │
                                     └────┬────┘
                                          │
                                     ┌────┴──────────┐
                                     │  WAL Replayer  │
                                     │  (background   │
                                     │   goroutine)   │
                                     │  Checks every  │
                                     │  10s for WAL   │
                                     │  files to      │
                                     │  replay to PG  │
                                     └────────────────┘
```

### 8.2 Hash Chain Integrity

```
Entry 1:
  content_hash = SHA256(serialize(entry_1))
  hash_chain   = SHA256(SEED + content_hash)

Entry 2:
  content_hash = SHA256(serialize(entry_2))
  hash_chain   = SHA256(entry_1.hash_chain + content_hash)

Entry N:
  content_hash = SHA256(serialize(entry_N))
  hash_chain   = SHA256(entry_{N-1}.hash_chain + content_hash)

Verification:
  For each entry i from 1 to N:
    expected = SHA256(previous_hash + SHA256(serialize(entry_i)))
    actual   = entry_i.hash_chain
    MATCH → entry is authentic
    MISMATCH → entry i or a predecessor has been tampered with
```

The hash chain provides tamper evidence, not tamper prevention. An attacker with database write access could recompute the chain. For stronger guarantees, the chain anchor (seed hash) should be stored externally (e.g., printed, stored in a separate system, or published to a transparency log).

---

## 9. Data Architecture

### 9.1 PostgreSQL Schema Relationships

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│    users     │     │    roles     │     │ casbin_rule  │
│──────────────│     │──────────────│     │──────────────│
│ id (PK)      │◄──┐ │ id (PK)      │     │ ptype        │
│ email        │   │ │ name         │     │ v0 (role)    │
│ display_name │   │ │ description  │     │ v1 (server)  │
│ idp_subject  │   │ │ is_system    │     │ v2 (tool)    │
│ status       │   │ └──────┬───────┘     │ v3 (action)  │
└──────────────┘   │        │             │ v4 (effect)  │
                   │        │             └──────────────┘
                   │  ┌─────┴──────┐
                   │  │ user_roles │
                   │  │────────────│
                   └──│ user_id(FK)│
                      │ role_id(FK)│
                      │ assigned_by│
                      └────────────┘

┌──────────────┐     ┌──────────────┐
│ mcp_servers  │     │ server_tools │
│──────────────│     │──────────────│
│ id (PK)      │◄────│ server_id(FK)│
│ name         │     │ tool_name    │
│ transport    │     │ description  │
│ config       │     │ input_schema │
│ is_enabled   │     └──────────────┘
└──────┬───────┘
       │
       ▼
┌────────────────┐    ┌──────────────────┐
│  audit_logs    │    │  saml_idp_configs │
│────────────────│    │──────────────────│
│ id (PK, BIGSERIAL)│ │ id (PK)          │
│ timestamp      │    │ name             │
│ session_id     │    │ entity_id        │
│ user_id (FK)   │    │ sso_url          │
│ server_id (FK) │    │ certificate      │
│ mcp_method     │    │ attribute_map    │
│ tool_name      │    │ group_role_map   │
│ status         │    └──────────────────┘
│ latency_ms     │
│ hash_chain     │    ┌──────────────────┐
│ ...            │    │  oauth_clients   │
└────────────────┘    │──────────────────│
                      │ id (PK)          │
┌────────────────┐    │ client_id        │
│   sessions     │    │ client_secret    │
│────────────────│    │ grant_types      │
│ id (PK, UUID)  │    │ scopes           │
│ user_id (FK)   │    │ owner_id (FK)    │
│ expires_at     │    └──────────────────┘
└────────────────┘
```

### 9.2 PostgreSQL Partitioning Strategy

The `audit_logs` table is the highest-volume table and benefits from range partitioning by timestamp:

```sql
CREATE TABLE audit_logs (
    id              BIGSERIAL,
    timestamp       TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- ... other columns ...
    hash_chain      TEXT,
    PRIMARY KEY (id, timestamp)  -- partition key must be in PK
) PARTITION BY RANGE (timestamp);

-- Create monthly partitions automatically via pg_partman or a cron job
CREATE TABLE audit_logs_2026_05 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');

CREATE TABLE audit_logs_2026_06 PARTITION OF audit_logs
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');
```

Benefits: queries with time-range filters scan only relevant partitions. Retention is implemented by dropping old partitions (instant, no vacuum needed). Each partition can be independently backed up or exported to cold storage.

### 9.3 Redis Data Model

| Key Pattern | Type | TTL | Purpose |
|------------|------|-----|---------|
| `session:{session_id}` | String (JSON) | 1h (sliding) | Session cache — user_id, email, roles |
| `rbac:{role}:{server}:{tool}:{action}` | String ("allow"/"deny") | 30s | RBAC decision cache |
| `ratelimit:{user_id}:{window}` | Sorted Set | window duration | Sliding window rate limiter timestamps |
| `saml:assertion:{assertion_id}` | String ("1") | 5min | SAML assertion replay detection |
| `auth:failures:{ip}` | String (count) | 15min | Brute-force detection counter |
| `circuit:{server_id}` | Hash | none | Circuit breaker state (state, failure_count, last_failure, open_since) |

### 9.4 Data Retention

| Data | Hot (PostgreSQL) | Warm (Compressed JSON) | Cold (S3/external) |
|------|-----------------|----------------------|-------------------|
| Audit logs | 30 days | 30–90 days | 90 days+ |
| Sessions | Until expiry | N/A | N/A |
| Users | Indefinite | N/A | N/A |
| Server configs | Indefinite | N/A | N/A |
| RBAC policies | Indefinite | N/A | N/A |
| Metrics (Prometheus) | 15 days (default) | Configured in Prometheus | N/A |

---

## 10. Scalability Architecture

### 10.1 Horizontal Scaling

```
                    ┌──────────────┐
                    │ Load Balancer │
                    │ (L7, sticky  │
                    │  sessions    │
                    │  optional)   │
                    └──────┬───────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ mcpgate  │ │ mcpgate  │ │ mcpgate  │
        │ Instance │ │ Instance │ │ Instance │
        │    1     │ │    2     │ │    3     │
        └────┬─────┘ └────┬─────┘ └────┬─────┘
             │            │            │
             └──────┬─────┴────────────┘
                    │
         ┌──────────┼──────────┐
         │          │          │
         ▼          ▼          ▼
   ┌──────────┐ ┌──────┐ ┌────────────┐
   │PostgreSQL│ │Redis │ │ Prometheus │
   │ (primary │ │      │ │ (scrapes   │
   │  + read  │ │      │ │  all       │
   │  replicas)│ │      │ │  instances)│
   └──────────┘ └──────┘ └────────────┘
```

Multiple mcpgate instances can run behind a load balancer because all state is in PostgreSQL and Redis. Considerations:

- **SSE connections are long-lived.** The load balancer should support WebSocket/SSE passthrough (L7) and optionally sticky sessions so an SSE stream isn't interrupted by re-balancing.
- **stdio bridges are per-instance.** If an upstream MCP server uses stdio transport, each mcpgate instance spawns its own processes. This is fine for most use cases but means stdio servers scale with mcpgate instances.
- **Log pipeline is per-instance.** Each instance writes to PostgreSQL independently. The hash chain is per-instance (each instance maintains its own chain). A global chain would require coordination (not worth the complexity).
- **Background jobs need leader election.** Log retention, server discovery, and health checks should run on one instance only to avoid duplicate work. Use Redis-based leader election (SETNX with TTL) or a Kubernetes leader election sidecar.

### 10.2 Performance Targets

| Metric | Target | Measurement |
|--------|--------|-------------|
| Proxy latency overhead (p50) | < 2ms | k6 load test, comparing direct vs proxied |
| Proxy latency overhead (p99) | < 5ms | k6 load test |
| Throughput (single instance, 4 cores) | > 1,000 RPS | k6 ramping test |
| Throughput (3 instances) | > 2,500 RPS | k6, accounting for coordination overhead |
| Log pipeline throughput | > 5,000 entries/sec | Benchmark test with mock sink |
| RBAC evaluation (cached) | < 0.5ms | Go benchmark |
| RBAC evaluation (uncached) | < 5ms | Go benchmark with PostgreSQL |
| Session validation (Redis) | < 1ms | Go benchmark |
| Memory per 1000 concurrent connections | < 200MB | pprof heap analysis |

### 10.3 Resource Sizing Guide

| Deployment Size | Users | RPS | Instances | PostgreSQL | Redis | Notes |
|----------------|-------|-----|-----------|-----------|-------|-------|
| Small | < 50 | < 100 | 1 | 2 vCPU, 4GB, 50GB | 1 vCPU, 2GB | Single docker-compose |
| Medium | 50–500 | 100–1,000 | 2–3 | 4 vCPU, 8GB, 200GB | 2 vCPU, 4GB | Kubernetes, PostgreSQL managed |
| Large | 500+ | 1,000+ | 3–5 | 8 vCPU, 16GB, 500GB+ | 4 vCPU, 8GB | Kubernetes with HPA, read replicas |

---

## 11. Failure Modes and Recovery

### 11.1 Component Failure Matrix

| Failed Component | Impact | Detection | Behavior | Recovery |
|-----------------|--------|-----------|----------|----------|
| PostgreSQL | Cannot write logs, cannot load new policies, cannot provision new users | Readiness probe fails, pgx pool errors | Proxy continues with Redis-cached sessions and RBAC decisions. Logs write to WAL. New auth impossible. | WAL replayed on recovery. Sessions resume from Redis. |
| Redis | Session validation falls back to PostgreSQL. Rate limiting disabled. RBAC cache miss on every request. | Health check goroutine detects ping failure | Proxy continues with degraded performance. All requests hit PostgreSQL for session/RBAC. Rate limits fail-open. | Automatic reconnection. Cache rebuilds on demand. |
| Upstream MCP server | Tools on that server unavailable | Health checker, circuit breaker | Circuit breaker opens. Requests get fast MCP error. Other servers unaffected. | Circuit half-opens after recovery_timeout. Successful probe closes circuit. |
| mcpgate instance | Traffic shifts to remaining instances | Load balancer health check (GET /health) | Load balancer removes instance. Other instances handle traffic. | Kubernetes restarts pod. New instance joins pool. |
| Log pipeline full | Potential log entry loss | mcpgate_log_pipeline_backlog metric, alert | WAL fallback captures entries to disk. Metric alert fires. | WAL replayed when pipeline drains. |

### 11.2 Graceful Shutdown Sequence

```
1. Receive SIGTERM
2. Stop accepting new connections (listener.Close())
3. Set /health to return 503 (load balancer drains)
4. Wait for in-flight proxy requests to complete (configurable timeout: 30s)
5. Drain log pipeline (flush remaining entries to PostgreSQL)
6. Close upstream transport connections (connection pool drain)
7. Close database and Redis connections
8. Stop background goroutines (health checker, WAL replayer, retention job)
9. Exit 0
```

---

## 12. Deployment Architecture

### 12.1 Docker Compose (Development / Small Production)

```
┌─────────────────────────────────────────────────┐
│                Docker Host                       │
│                                                  │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│  │ mcpgate  │ │PostgreSQL│ │  Redis   │        │
│  │ :8080    │ │ :5432    │ │ :6379    │        │
│  │ :9090    │ │          │ │          │        │
│  └──────────┘ └──────────┘ └──────────┘        │
│                                                  │
│  ┌──────────┐ ┌──────────┐                      │
│  │Prometheus│ │ Grafana  │  (optional)          │
│  │ :9091    │ │ :3000    │                      │
│  └──────────┘ └──────────┘                      │
│                                                  │
│  Network: mcpgate-net (bridge)                  │
│  Volumes: pgdata (persistent)                    │
└─────────────────────────────────────────────────┘
```

### 12.2 Kubernetes (Production)

```
┌─────────────────────────────────────────────────────────────────┐
│                    Kubernetes Cluster                             │
│                                                                  │
│  Namespace: mcpgate                                              │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Deployment: mcpgate (replicas: 3, HPA: 2-10)          │    │
│  │  ┌─────────┐ ┌─────────┐ ┌─────────┐                  │    │
│  │  │  Pod 1  │ │  Pod 2  │ │  Pod 3  │                  │    │
│  │  │mcpgate  │ │mcpgate  │ │mcpgate  │                  │    │
│  │  │container│ │container│ │container│                  │    │
│  │  └─────────┘ └─────────┘ └─────────┘                  │    │
│  └─────────────────────────────────────────────────────────┘    │
│                          │                                       │
│  ┌───────────────────────┴────────────────────────┐             │
│  │  Service: mcpgate (ClusterIP)                   │             │
│  │  Ports: 8080 (proxy/api), 9090 (metrics)       │             │
│  └───────────────────────┬────────────────────────┘             │
│                          │                                       │
│  ┌───────────────────────┴────────────────────────┐             │
│  │  Ingress: mcpgate.example.com                   │             │
│  │  TLS: cert-manager (Let's Encrypt)             │             │
│  │  Paths: / → mcpgate:8080                       │             │
│  └────────────────────────────────────────────────┘             │
│                                                                  │
│  ┌────────────────────┐  ┌────────────────────┐                 │
│  │  ConfigMap:         │  │  Secret:           │                 │
│  │  mcpgate-config    │  │  mcpgate-secrets   │                 │
│  │  (mcpgate.yaml)    │  │  (DB pass, Redis   │                 │
│  │                    │  │   pass, JWT key,   │                 │
│  │                    │  │   SAML cert)       │                 │
│  └────────────────────┘  └────────────────────┘                 │
│                                                                  │
│  ┌────────────────────────────────────────────────┐             │
│  │  NetworkPolicy: mcpgate-netpol                  │             │
│  │  Ingress: allow from ingress controller only   │             │
│  │  Egress: allow to PostgreSQL, Redis,           │             │
│  │          upstream MCP servers, SAML IdP         │             │
│  └────────────────────────────────────────────────┘             │
│                                                                  │
│  External (managed or in-cluster):                              │
│  ┌──────────────┐  ┌──────────────┐                             │
│  │ PostgreSQL   │  │    Redis     │                             │
│  │ (RDS/Cloud   │  │  (Elasticache│                             │
│  │  SQL/in-     │  │   /in-       │                             │
│  │  cluster)    │  │   cluster)   │                             │
│  └──────────────┘  └──────────────┘                             │
│                                                                  │
│  ┌────────────────────────────────────────────────┐             │
│  │  ServiceMonitor: mcpgate-metrics               │             │
│  │  Prometheus scrapes :9090/metrics              │             │
│  └────────────────────────────────────────────────┘             │
└─────────────────────────────────────────────────────────────────┘
```

### 12.3 Pod Security Context

```yaml
securityContext:
  runAsNonRoot: true
  runAsUser: 65534        # nobody
  runAsGroup: 65534
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
      - ALL
  seccompProfile:
    type: RuntimeDefault

# Volume mounts for writable directories
volumeMounts:
  - name: tmp
    mountPath: /tmp
  - name: wal
    mountPath: /var/lib/mcpgate/wal

volumes:
  - name: tmp
    emptyDir:
      sizeLimit: 100Mi
  - name: wal
    emptyDir:
      sizeLimit: 1Gi
```

---

## 13. Security Architecture

### 13.1 Trust Boundaries

```
┌─────────────────────────────────────────────────────────┐
│  UNTRUSTED ZONE                                         │
│                                                         │
│  ┌──────────────┐  ┌──────────────┐                    │
│  │  MCP Clients │  │  Browsers    │                    │
│  │  (agents)    │  │  (admin UI)  │                    │
│  └──────┬───────┘  └──────┬───────┘                    │
│         │                 │                             │
└─────────┼─────────────────┼─────────────────────────────┘
          │  TLS 1.3        │  TLS 1.3
          │                 │
┌─────────┼─────────────────┼─────────────────────────────┐
│  DMZ    │                 │                              │
│         ▼                 ▼                              │
│  ┌────────────────────────────────────┐                 │
│  │         mcpgate Process            │                 │
│  │                                    │                 │
│  │  ┌─────────────────────────────┐  │                 │
│  │  │ TRUST BOUNDARY: Auth Layer  │  │                 │
│  │  │ (SAML + OAuth + JWT)        │  │                 │
│  │  └─────────────┬───────────────┘  │                 │
│  │                │                   │                 │
│  │  ┌─────────────┴───────────────┐  │                 │
│  │  │ TRUST BOUNDARY: RBAC Layer  │  │                 │
│  │  │ (Casbin policy evaluation)  │  │                 │
│  │  └─────────────┬───────────────┘  │                 │
│  │                │                   │                 │
│  │         AUTHENTICATED +            │                 │
│  │         AUTHORIZED ZONE            │                 │
│  └────────────────┬───────────────────┘                 │
│                   │                                     │
└───────────────────┼─────────────────────────────────────┘
                    │  Internal network (TLS optional)
                    │
┌───────────────────┼─────────────────────────────────────┐
│  TRUSTED ZONE     │                                      │
│                   ▼                                      │
│  ┌──────────┐ ┌──────┐ ┌─────────────────────┐         │
│  │PostgreSQL│ │Redis │ │ Upstream MCP Servers │         │
│  └──────────┘ └──────┘ └─────────────────────┘         │
└─────────────────────────────────────────────────────────┘
```

### 13.2 Secret Storage

| Secret | At Rest | In Transit | Access |
|--------|---------|-----------|--------|
| PostgreSQL password | Kubernetes Secret (base64) or external secret store | TLS to PostgreSQL | mcpgate process only |
| Redis password | Kubernetes Secret or external secret store | TLS to Redis (if enabled) | mcpgate process only |
| JWT signing private key | PEM file in Secret volume mount | Never transmitted | mcpgate process only |
| SAML SP private key | PEM file in Secret volume mount | Never transmitted | mcpgate process only |
| OAuth client secrets | bcrypt hash in PostgreSQL | TLS to mcpgate | Admin API only |
| SAML IdP certificates | PEM in PostgreSQL (public certificate, not a secret but integrity-critical) | TLS from IdP | SAML validation code |

### 13.3 TLS Configuration

```go
// internal/server/tls.go

func secureTLSConfig(certFile, keyFile string) *tls.Config {
    return &tls.Config{
        MinVersion:               tls.VersionTLS13,
        CurvePreferences:         []tls.CurveID{tls.X25519, tls.CurveP256},
        PreferServerCipherSuites: true,
        // TLS 1.3 cipher suites are not configurable in Go (all are secure)
    }
}
```

### 13.4 HTTP Security Headers (Admin Console)

```go
func securityHeaders(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Security-Policy",
            "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self' wss:")
        w.Header().Set("X-Frame-Options", "DENY")
        w.Header().Set("X-Content-Type-Options", "nosniff")
        w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
        w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
        w.Header().Set("X-XSS-Protection", "0") // Disabled per OWASP (CSP is sufficient)
        next.ServeHTTP(w, r)
    })
}
```

---

## 14. Interface Contracts

This section defines the Go interfaces that form the module boundaries. These are the contracts that unit tests mock and integration tests verify.

### 14.1 Store Interfaces

```go
// internal/store/interfaces.go

type UserStore interface {
    Create(ctx context.Context, user *User) error
    GetByID(ctx context.Context, id uuid.UUID) (*User, error)
    GetByEmail(ctx context.Context, email string) (*User, error)
    GetByIDPSubject(ctx context.Context, idpName, subject string) (*User, error)
    List(ctx context.Context, filter UserFilter, page Pagination) ([]User, int64, error)
    Update(ctx context.Context, user *User) error
    UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
}

type ServerStore interface {
    Create(ctx context.Context, server *MCPServer) error
    GetByID(ctx context.Context, id uuid.UUID) (*MCPServer, error)
    GetByName(ctx context.Context, name string) (*MCPServer, error)
    List(ctx context.Context) ([]MCPServer, error)
    Update(ctx context.Context, server *MCPServer) error
    Delete(ctx context.Context, id uuid.UUID) error
    UpdateTools(ctx context.Context, serverID uuid.UUID, tools []ServerTool) error
}

type AuditLogStore interface {
    BatchInsert(ctx context.Context, entries []AuditLogEntry) error
    Query(ctx context.Context, filter LogFilter, page Pagination) ([]AuditLogEntry, int64, error)
    Export(ctx context.Context, filter LogFilter, format string, w io.Writer) error
    VerifyHashChain(ctx context.Context, from, to time.Time) (bool, error)
}

type SessionStore interface {
    Create(ctx context.Context, session *Session) error
    GetByID(ctx context.Context, id uuid.UUID) (*Session, error)
    Delete(ctx context.Context, id uuid.UUID) error
    DeleteExpired(ctx context.Context) (int64, error)
}

type RoleStore interface {
    Create(ctx context.Context, role *Role) error
    GetByID(ctx context.Context, id uuid.UUID) (*Role, error)
    GetByName(ctx context.Context, name string) (*Role, error)
    List(ctx context.Context) ([]Role, error)
    Update(ctx context.Context, role *Role) error
    Delete(ctx context.Context, id uuid.UUID) error
    AssignToUser(ctx context.Context, userID, roleID uuid.UUID, assignedBy uuid.UUID) error
    RevokeFromUser(ctx context.Context, userID, roleID uuid.UUID) error
    GetUserRoles(ctx context.Context, userID uuid.UUID) ([]Role, error)
}
```

### 14.2 Cache Interfaces

```go
// internal/store/interfaces.go

type SessionCache interface {
    Set(ctx context.Context, sessionID uuid.UUID, data SessionData, ttl time.Duration) error
    Get(ctx context.Context, sessionID uuid.UUID) (*SessionData, error)
    Delete(ctx context.Context, sessionID uuid.UUID) error
    Extend(ctx context.Context, sessionID uuid.UUID, ttl time.Duration) error
}

type PolicyCache interface {
    Get(ctx context.Context, key string) (*bool, error) // nil = miss, *true = allow, *false = deny
    Set(ctx context.Context, key string, allowed bool, ttl time.Duration) error
    Invalidate(ctx context.Context, pattern string) error
}

type RateLimiter interface {
    Allow(ctx context.Context, key string, limit RateLimit) (RateLimitResult, error)
}

type RateLimitResult struct {
    Allowed   bool
    Remaining int64
    ResetAt   time.Time
    RetryAfter time.Duration
}
```

### 14.3 Proxy Interfaces

```go
// internal/proxy/interfaces.go

type ProxyHandler interface {
    HandleStreamableHTTP(w http.ResponseWriter, r *http.Request)
    HandleSSEStream(w http.ResponseWriter, r *http.Request)
    HandleSSEMessage(w http.ResponseWriter, r *http.Request)
}

type CircuitBreaker interface {
    Allow(serverID string) (bool, error)
    RecordSuccess(serverID string)
    RecordFailure(serverID string)
    State(serverID string) CircuitState
}

type HealthChecker interface {
    Start(ctx context.Context)
    Status(serverID string) HealthStatus
    AllStatuses() map[string]HealthStatus
}
```

### 14.4 Auth Interfaces

```go
// internal/auth/interfaces.go

type Authenticator interface {
    // ValidateToken validates a JWT and returns the user context
    ValidateToken(ctx context.Context, tokenString string) (*UserContext, error)
}

type SAMLProvider interface {
    // InitiateLogin generates a SAML AuthnRequest and returns the redirect URL
    InitiateLogin(w http.ResponseWriter, r *http.Request)
    // HandleACS processes a SAML Response at the ACS endpoint
    HandleACS(w http.ResponseWriter, r *http.Request)
    // Metadata returns the SP metadata XML
    Metadata() ([]byte, error)
}

type M2MTokenValidator interface {
    // Validate validates an Auth0-issued M2M JWT via JWKS and returns the identity
    Validate(ctx context.Context, tokenString string) (*UserIdentity, error)
}

type RBACEnforcer interface {
    // Enforce evaluates an RBAC policy decision
    Enforce(ctx context.Context, sub, server, tool, action string) (bool, error)
    // AddPolicy adds a new policy rule
    AddPolicy(ctx context.Context, rule PolicyRule) error
    // RemovePolicy removes a policy rule
    RemovePolicy(ctx context.Context, rule PolicyRule) error
    // Reload reloads policies from the database
    Reload(ctx context.Context) error
}
```

---

## 15. Error Response Contracts

### 15.1 MCP JSON-RPC Error Responses (Proxy Endpoints)

```json
// Authentication failure (no valid JWT)
// HTTP 401 Unauthorized
{
    "error": "unauthorized",
    "message": "Valid authentication required"
}

// Authorization failure (RBAC deny)
// HTTP 200 (MCP error in JSON-RPC envelope)
{
    "jsonrpc": "2.0",
    "id": 1,
    "error": {
        "code": -32600,
        "message": "Unauthorized: insufficient permissions for this operation"
    }
}

// Rate limit exceeded
// HTTP 200 (MCP error in JSON-RPC envelope)
// Header: Retry-After: 12
{
    "jsonrpc": "2.0",
    "id": 1,
    "error": {
        "code": -32000,
        "message": "Rate limit exceeded. Retry after 12 seconds.",
        "data": {
            "retry_after_seconds": 12,
            "limit": "60/min",
            "remaining": 0
        }
    }
}

// Upstream server unavailable (circuit open)
// HTTP 200 (MCP error in JSON-RPC envelope)
{
    "jsonrpc": "2.0",
    "id": 1,
    "error": {
        "code": -32603,
        "message": "Upstream server temporarily unavailable"
    }
}
```

### 15.2 Admin API Error Responses

```json
// HTTP 400 Bad Request
{
    "error": "validation_error",
    "message": "Request validation failed",
    "details": [
        {"field": "name", "message": "name is required"},
        {"field": "transport", "message": "must be one of: streamable_http, sse, stdio"}
    ]
}

// HTTP 404 Not Found
{
    "error": "not_found",
    "message": "Server with ID '550e8400-...' not found"
}

// HTTP 409 Conflict
{
    "error": "conflict",
    "message": "A server with name 'github-mcp' already exists"
}

// HTTP 500 Internal Server Error
{
    "error": "internal_error",
    "message": "An internal error occurred",
    "request_id": "01902e8a-..."
}
```

---

## 16. Observability Architecture

### 16.1 Metrics Collection Flow

```
                Proxy Handler
                     │
                     │ records latency, increments counters
                     ▼
            ┌──────────────────┐
            │  prometheus/     │
            │  client_golang   │
            │  (in-process     │
            │   registry)      │
            └────────┬─────────┘
                     │
        ┌────────────┼────────────┐
        │            │            │
        ▼            ▼            ▼
  GET :9090/   Grafana         AlertManager
   metrics     (dashboards)    (configured
  (Prometheus                   thresholds)
   scrapes)
```

### 16.2 Distributed Tracing Flow

```
Client Request
  │
  │ W3C Traceparent header (or generated)
  ▼
┌────────────────────────────────────────┐
│ mcpgate trace (parent span)            │
│                                        │
│  ├── span: proxy.auth (1ms)           │
│  ├── span: proxy.rbac (0.5ms)        │
│  ├── span: proxy.upstream (140ms)     │  ←── this is where time is spent
│  │     └── HTTP client span to        │
│  │         upstream server             │
│  └── span: proxy.log (0.1ms)         │
│                                        │
└────────────────────────────────────────┘
  │
  │ OTLP export
  ▼
Jaeger / Tempo / Honeycomb / Datadog
```

### 16.3 Alerting Recommendations

| Alert | Condition | Severity |
|-------|-----------|----------|
| High error rate | `rate(mcpgate_requests_total{status="error"}[5m]) / rate(mcpgate_requests_total[5m]) > 0.05` | Critical |
| High latency | `histogram_quantile(0.95, mcpgate_request_duration_seconds) > 1.0` | Warning |
| Upstream server down | `mcpgate_upstream_health == 0` for 2 minutes | Critical |
| Log pipeline backlog | `mcpgate_log_pipeline_backlog > 5000` | Warning |
| Auth failure spike | `rate(mcpgate_auth_attempts_total{status="failure"}[5m]) > 10` | Warning |
| RBAC denial spike | `rate(mcpgate_rbac_denials_total[5m]) > 20` | Info |

---

## 17. Decision Log

This section records key architectural decisions with rationale. Updated as decisions are made during implementation.

| # | Decision | Rationale | Alternatives Considered |
|---|----------|-----------|------------------------|
| D1 | Single binary serves proxy, admin API, and static assets | Simplifies deployment. One port, one process, one config. Kubernetes sidecar for separation is overkill for this use case. | Separate proxy and admin binaries. Rejected: doubles deployment complexity for marginal benefit. |
| D2 | chi router over gin or fiber | chi is stdlib-compatible (net/http handlers), middleware-friendly, and has no reflection magic. gin is larger and opinionated. fiber uses fasthttp (non-stdlib). | gin (popular but larger), fiber (fast but non-stdlib), stdlib only (lacks routing). |
| D3 | Casbin for RBAC over hand-rolled or OPA | Casbin has a PostgreSQL adapter, Go-native, flexible model language, and is well-tested. OPA is more powerful but heavier (Rego language, separate process). Hand-rolled is error-prone. | OPA (overkill), hand-rolled (risky), ladon (abandoned). |
| D4 | crewjam/saml over custom SAML implementation | SAML is notoriously complex. crewjam/saml is the most maintained Go SAML library with good IdP compatibility. Custom implementation would take months and have more bugs. | Custom (too risky), saml2 (less maintained). |
| D5 | PostgreSQL over MySQL or SQLite | PostgreSQL has native JSONB (for flexible metadata), table partitioning (for audit logs), and excellent Go driver support (pgx). | MySQL (weaker JSON support), SQLite (not suitable for concurrent writes). |
| D6 | Redis for caching over in-process cache | Redis supports horizontal scaling (shared cache across instances), has built-in TTL, and supports pub/sub for cache invalidation. In-process cache requires per-instance invalidation logic. | sync.Map or ristretto (per-instance, no shared state). |
| D7 | Async log pipeline over synchronous logging | Synchronous logging would add 5-10ms to every request (database write). Async pipeline with buffered channel adds < 1µs. WAL fallback prevents data loss. | Synchronous (too slow), fire-and-forget (data loss risk). |
| D8 | RS256 for JWTs over HS256 | RS256 allows public key verification (JWKS endpoint) without sharing the signing secret. Enables external services to validate mcpgate JWTs. | HS256 (simpler but shared secret), EdDSA (less tooling support). |
| D9 | Removed token estimation | Token estimation is inherently non-deterministic — different models use different tokenizers, and the proxy doesn't know which model the client is using. Approximate counts would be misleading for cost attribution. Payload size (bytes) is a reliable, deterministic proxy for message complexity. | tiktoken-go (approximate, model-dependent), custom tokenizer (maintenance burden). |

---

*This architectural blueprint is a companion to the MCP Proxy Gateway PRD v1.0 and Development Plan v1.0. It should be updated when architectural decisions change or new components are added.*
