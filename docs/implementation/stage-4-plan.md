# Sprint 4 Implementation Plan — Role-Based Access Control (RBAC)

**Date:** 2026-05-25  
**Status:** Pre-implementation  
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/mcpgate-architecture-blueprint.md` · `docs/implementation/stage-3-plan.md`

---

## Context

Sprints 1–3 delivered the proxy engine, SAML 2.0 login, JWT/session management, and provider-agnostic M2M JWKS validation. Every protected route is now authenticated but not authorised — any authenticated user or M2M client can call any MCP server with any method. Sprint 4 closes this gap.

The design uses [Casbin v2](https://github.com/casbin/casbin) with a PostgreSQL adapter to evaluate per-role, per-server, per-action policy rules stored in the `casbin_rule` table. Redis caches individual policy decisions to keep the per-request overhead below a single microsecond on cache hit. The `DefaultAction` in production is `"deny"`: authenticated principals with no matching policy are rejected with 403.

**End state:** an admin creates roles and Casbin policy rules via the admin REST API; users are assigned roles at login or via the admin API; the `Authorize` middleware checks policy before forwarding any MCP request to the upstream. Unauthenticated requests return 401 (existing behaviour). Authenticated requests with no matching policy return 403. The admin API itself is protected by a lightweight `RequireRole("admin")` middleware that does not go through Casbin.

The `casbin_rule` table already exists from migration 001, but its schema must be rebuilt to match the adapter's expectations (see §1). The `roles`, `user_roles`, `PolicyCache` interface, `RBACConfig`, `UserIdentity.Roles`, and `Action()` method-to-action mapping are all already in place from Sprint 2. Migration 003 seeds the three system roles and the default admin wildcard policy.

---

## Architecture: RBAC Enforcement Flow

```
Request
  │
  ├─ auth.Authenticate (existing — 401 on no/invalid token, populates UserIdentity)
  │
  └─ rbac.Authorize (new Sprint 4)
       │
       ├─ GET /mcp/{serverName}/sse  → action = "connect"   (no body to parse)
       │
       └─ POST /mcp/{serverName}/message
          POST /mcp/{serverName}/sse  → buffer body → jsonrpc.ParseMessage() → Action(method)
       │
       ├─ for each role in UserIdentity.Roles:
       │    check PolicyCache("rbac:{role}:{server}:{action}") → cache hit: use result
       │    casbin.Enforce(role, serverName, action)          → cache miss: evaluate + cache
       │    short-circuit on first allow
       │
       ├─ no role matched → apply cfg.RBAC.DefaultAction ("deny" in production)
       │
       ├─ allowed  → pass through, body already rebuffered
       └─ denied   → 403 {"error":"forbidden"}
```

The admin API is not routed through Casbin. It uses a separate `RequireRole("admin")` check immediately after the auth middleware — a simple slice scan of `UserIdentity.Roles`.

---

## New Package Structure

```
internal/
├── auth/
│   └── rbac/
│       ├── enforcer.go          Enforcer: NewEnforcer, Enforce, ReloadPolicy, policy CRUD
│       ├── enforcer_test.go     Unit: allow/deny, wildcard, defaultAllow, cache hit/miss
│       ├── middleware.go        Authorize, RequireRole
│       └── middleware_test.go   Unit: GET→connect, POST body parse, 403 path, cache miss
└── store/
    └── redis/
        ├── policy_cache.go      PolicyCache Redis implementation
        └── policy_cache_test.go integration

internal/admin/api/
├── policies.go   Casbin policy CRUD (list, add, remove)
├── roles.go      Role CRUD + user-role assignment/revocation
└── users.go      User management (list, get, patch status)
```

Files modified:
- `internal/store/postgres/roles.go` — implement all 8 stubs
- `internal/store/postgres/roles_test.go` — integration tests (new file)
- `internal/admin/api/router.go` — add roles/users/policies store fields + wire handlers
- `internal/server/server.go` — add Enforcer field, wire `Authorize` on `/mcp/*`, `RequireRole` on admin
- `tests/e2e/setup_test.go` — change `DefaultAction` from `"allow"` to `"deny"`, seed admin role

---

## New Dependencies

```bash
go get github.com/casbin/casbin/v2@latest
go get github.com/pckhoi/casbin-pgx-adapter/v3@latest
go mod tidy
```

`pckhoi/casbin-pgx-adapter/v3` accepts a `*pgxpool.Pool` via the `WithConnectionPool()` option,
matching the existing pgx v5 pool in mcpgate. No second database connection is needed.

**Schema note:** the adapter auto-creates its table with column `p_type` (underscore) and `id text`
(hash-based PK). Our migration 001 used `ptype` (no underscore) and `id BIGSERIAL`. Migration 003
drops and recreates `casbin_rule` to match the adapter before calling `WithSkipTableCreate()` — see §1.

> **Implementation note:** verify the adapter's exact DDL against
> `github.com/pckhoi/casbin-pgx-adapter/v3` source before running migration 003 in any environment.
> If the adapter uses a different column name (e.g. `ptype` not `p_type`), update the migration to
> match rather than the other way around — the adapter is the authoritative source of truth for its
> own schema.

---

## Implementation Details

### 1. Migration 003 — `casbin_rule` Rebuild and Role Seed

**`db/migrations/003_rbac_seed.up.sql`:**

```sql
BEGIN;

-- Rebuild casbin_rule to match pckhoi/casbin-pgx-adapter/v3 schema.
-- The table is empty in all environments (no policies written before Sprint 4).
DROP TABLE casbin_rule;

CREATE TABLE casbin_rule (
    id     TEXT PRIMARY KEY,         -- hash computed by adapter
    p_type TEXT,                     -- 'p' = policy rule
    v0     TEXT NOT NULL DEFAULT '', -- subject  (role name)
    v1     TEXT NOT NULL DEFAULT '', -- object   (server name or '*')
    v2     TEXT NOT NULL DEFAULT '', -- action   (list/call/read/write/connect/admin or '*')
    v3     TEXT NOT NULL DEFAULT '',
    v4     TEXT NOT NULL DEFAULT '',
    v5     TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_casbin_rule_ptype ON casbin_rule (p_type);

-- Seed system roles (is_system prevents deletion via admin API).
INSERT INTO roles (id, name, description, is_system)
VALUES
    (gen_random_uuid(), 'admin',     'Full access to all MCP servers and admin API', true),
    (gen_random_uuid(), 'developer', 'Read and call access to allowed servers',       true),
    (gen_random_uuid(), 'readonly',  'List and read access only',                     true)
ON CONFLICT (name) DO NOTHING;

-- Default policy: admin role has wildcard access to every server and action.
-- ID is a deterministic md5 so re-running the migration is idempotent.
INSERT INTO casbin_rule (id, p_type, v0, v1, v2)
VALUES (md5('p|admin|*|*'), 'p', 'admin', '*', '*')
ON CONFLICT (id) DO NOTHING;

COMMIT;
```

**`db/migrations/003_rbac_seed.down.sql`:**

```sql
BEGIN;
DELETE FROM casbin_rule WHERE p_type = 'p' AND v0 = 'admin' AND v1 = '*' AND v2 = '*';
DELETE FROM roles WHERE name IN ('admin', 'developer', 'readonly') AND is_system = true;
DROP INDEX  IF EXISTS idx_casbin_rule_ptype;
DROP TABLE  casbin_rule;
-- Recreate the original schema from migration 001 so down-migration is clean.
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT      NOT NULL,
    v0    TEXT      NOT NULL DEFAULT '',
    v1    TEXT      NOT NULL DEFAULT '',
    v2    TEXT      NOT NULL DEFAULT '',
    v3    TEXT      NOT NULL DEFAULT '',
    v4    TEXT      NOT NULL DEFAULT '',
    v5    TEXT      NOT NULL DEFAULT ''
);
CREATE INDEX idx_casbin_rule_ptype ON casbin_rule (ptype);
COMMIT;
```

---

### 2. PolicyCache — `internal/store/redis/policy_cache.go`

The `store.PolicyCache` interface is already defined in `internal/store/interfaces.go`. This file provides the Redis implementation.

```go
package redis

import (
    "context"
    "encoding/json"
    "errors"
    "time"

    "github.com/redis/go-redis/v9"

    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

type PolicyCache struct{ client *redis.Client }

func NewPolicyCache(client *redis.Client) *PolicyCache {
    return &PolicyCache{client: client}
}

// Get returns nil, nil on cache miss. *true = allow, *false = deny.
func (c *PolicyCache) Get(ctx context.Context, key string) (*bool, error) {
    val, err := c.client.Get(ctx, key).Result()
    if errors.Is(err, redis.Nil) {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }
    var allowed bool
    if err := json.Unmarshal([]byte(val), &allowed); err != nil {
        return nil, err
    }
    return &allowed, nil
}

func (c *PolicyCache) Set(ctx context.Context, key string, allowed bool, ttl time.Duration) error {
    v, _ := json.Marshal(allowed)
    return c.client.Set(ctx, key, v, ttl).Err()
}

// Invalidate deletes all keys matching the glob pattern using SCAN + DEL batches.
func (c *PolicyCache) Invalidate(ctx context.Context, pattern string) error {
    var cursor uint64
    for {
        keys, next, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
        if err != nil {
            return err
        }
        if len(keys) > 0 {
            if err := c.client.Del(ctx, keys...).Err(); err != nil {
                return err
            }
        }
        cursor = next
        if cursor == 0 {
            break
        }
    }
    return nil
}
```

Cache key format: `rbac:<role>:<serverName>:<action>` — e.g. `rbac:developer:my-server:call`.

---

### 3. RoleStore Completion — `internal/store/postgres/roles.go`

Implement all eight stubs following the same pgx pattern as `users.go` and `sessions.go`.

```go
func (s *RoleStore) Create(ctx context.Context, role *store.Role) error
// INSERT INTO roles (name, description) VALUES ($1, $2) RETURNING id, created_at
// pgconn "23505" → store.ErrAlreadyExists

func (s *RoleStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Role, error)
// SELECT id, name, description, is_system, created_at FROM roles WHERE id = $1
// pgx.ErrNoRows → store.ErrNotFound

func (s *RoleStore) GetByName(ctx context.Context, name string) (*store.Role, error)
// SELECT ... FROM roles WHERE name = $1
// pgx.ErrNoRows → store.ErrNotFound

func (s *RoleStore) List(ctx context.Context) ([]store.Role, error)
// SELECT ... FROM roles ORDER BY name

func (s *RoleStore) Update(ctx context.Context, role *store.Role) error
// UPDATE roles SET name = $1, description = $2 WHERE id = $3
// Handlers prevent renaming system roles before calling this.
// RowsAffected == 0 → store.ErrNotFound

func (s *RoleStore) Delete(ctx context.Context, id uuid.UUID) error
// First: SELECT is_system FROM roles WHERE id = $1 → ErrNotFound or store.ErrConflict if true
// Then: DELETE FROM roles WHERE id = $1

func (s *RoleStore) AssignToUser(ctx context.Context, userID, roleID, assignedBy uuid.UUID) error
// INSERT INTO user_roles (user_id, role_id, assigned_by) VALUES ($1, $2, $3)
// ON CONFLICT DO NOTHING — idempotent; already-assigned is not an error

func (s *RevokeFromUser) RevokeFromUser(ctx context.Context, userID, roleID uuid.UUID) error
// DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2
// RowsAffected == 0 → store.ErrNotFound
```

`GetUserRoles` is already implemented and unchanged.

---

### 4. RBAC Enforcer — `internal/auth/rbac/enforcer.go`

```go
package rbac

import (
    "context"
    "fmt"
    "log/slog"
    "time"

    "github.com/casbin/casbin/v2"
    "github.com/casbin/casbin/v2/model"
    pgxadapter "github.com/pckhoi/casbin-pgx-adapter/v3"
    "github.com/jackc/pgx/v5/pgxpool"

    "github.com/K3y5tr0ke/mcpgate/internal/config"
    "github.com/K3y5tr0ke/mcpgate/internal/store"
)

const casbinModel = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
`

type PolicyRule struct {
    Role   string `json:"role"`
    Server string `json:"server"`
    Action string `json:"action"`
}

type Enforcer struct {
    e            *casbin.Enforcer
    cache        store.PolicyCache
    cacheTTL     time.Duration
    defaultAllow bool
    logger       *slog.Logger
}

func NewEnforcer(
    db     *pgxpool.Pool,
    cache  store.PolicyCache,
    cfg    config.RBACConfig,
    logger *slog.Logger,
) (*Enforcer, error) {
    adapter, err := pgxadapter.NewAdapter(
        "",  // connection string unused when pool is provided
        pgxadapter.WithConnectionPool(db),
        pgxadapter.WithSkipTableCreate(),  // table managed by our migration
    )
    if err != nil {
        return nil, fmt.Errorf("rbac: create pgx adapter: %w", err)
    }

    m, err := model.NewModelFromString(casbinModel)
    if err != nil {
        return nil, fmt.Errorf("rbac: parse model: %w", err)
    }

    e, err := casbin.NewEnforcer(m, adapter)
    if err != nil {
        return nil, fmt.Errorf("rbac: new enforcer: %w", err)
    }
    if err := e.LoadPolicy(); err != nil {
        return nil, fmt.Errorf("rbac: load policy: %w", err)
    }

    return &Enforcer{
        e:            e,
        cache:        cache,
        cacheTTL:     cfg.CacheTTL,
        defaultAllow: cfg.DefaultAction == "allow",
        logger:       logger,
    }, nil
}

// Enforce returns true if any role in roles is permitted to perform action on serverName.
// Checks PolicyCache first; writes through on miss.
func (e *Enforcer) Enforce(ctx context.Context, roles []string, serverName, action string) (bool, error) {
    for _, role := range roles {
        key := fmt.Sprintf("rbac:%s:%s:%s", role, serverName, action)
        if cached, err := e.cache.Get(ctx, key); err == nil && cached != nil {
            if *cached {
                return true, nil
            }
            continue
        }

        allowed, err := e.e.Enforce(role, serverName, action)
        if err != nil {
            return false, fmt.Errorf("rbac: enforce: %w", err)
        }

        if cacheErr := e.cache.Set(ctx, key, allowed, e.cacheTTL); cacheErr != nil {
            e.logger.Warn("rbac: policy cache set failed", "err", cacheErr)
        }

        if allowed {
            return true, nil
        }
    }
    return false, nil
}

// DefaultAllow reports the configured DefaultAction.
func (e *Enforcer) DefaultAllow() bool { return e.defaultAllow }

// ReloadPolicy reloads rules from Postgres and invalidates the decision cache.
// Called after any policy mutation via the admin API.
func (e *Enforcer) ReloadPolicy(ctx context.Context) error {
    if err := e.e.LoadPolicy(); err != nil {
        return fmt.Errorf("rbac: reload policy: %w", err)
    }
    if err := e.cache.Invalidate(ctx, "rbac:*"); err != nil {
        e.logger.Warn("rbac: cache invalidation failed after reload", "err", err)
    }
    return nil
}

// --- Policy CRUD (used by admin API handler) ---

// ListPolicies returns all 'p' rules.
func (e *Enforcer) ListPolicies() []PolicyRule {
    rules := e.e.GetPolicy()
    out := make([]PolicyRule, 0, len(rules))
    for _, r := range rules {
        if len(r) >= 3 {
            out = append(out, PolicyRule{Role: r[0], Server: r[1], Action: r[2]})
        }
    }
    return out
}

// AddPolicy inserts a new 'p' rule and reloads the enforcer.
func (e *Enforcer) AddPolicy(ctx context.Context, role, server, action string) error {
    if _, err := e.e.AddPolicy(role, server, action); err != nil {
        return fmt.Errorf("rbac: add policy: %w", err)
    }
    return e.ReloadPolicy(ctx)
}

// RemovePolicy deletes a 'p' rule and reloads the enforcer.
func (e *Enforcer) RemovePolicy(ctx context.Context, role, server, action string) error {
    if _, err := e.e.RemovePolicy(role, server, action); err != nil {
        return fmt.Errorf("rbac: remove policy: %w", err)
    }
    return e.ReloadPolicy(ctx)
}
```

---

### 5. RBAC Middleware — `internal/auth/rbac/middleware.go`

```go
package rbac

import (
    "bytes"
    "context"
    "io"
    "net/http"
    "log/slog"

    "github.com/go-chi/chi/v5"

    "github.com/K3y5tr0ke/mcpgate/internal/auth"
    "github.com/K3y5tr0ke/mcpgate/internal/proxy/jsonrpc"
)

// Authorize is a chi middleware that enforces RBAC on /mcp/{serverName}/* routes.
//
// GET requests (SSE stream open) carry no JSON-RPC body and use action "connect".
// POST requests buffer the body, parse the JSON-RPC method, and restore the body
// before passing to the proxy handler.
func Authorize(enforcer *Enforcer, logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            identity := auth.MustIdentity(r.Context())
            serverName := chi.URLParam(r, "serverName")

            var action string
            if r.Method == http.MethodGet {
                // SSE stream open: no body — enforce "connect" permission.
                action = "connect"
            } else {
                // POST /message or POST /sse: parse JSON-RPC method from body.
                buf, err := io.ReadAll(r.Body)
                if err != nil {
                    http.Error(w, `{"error":"failed to read request body"}`, http.StatusBadRequest)
                    return
                }
                r.Body = io.NopCloser(bytes.NewReader(buf))

                msg, parseErr := jsonrpc.ParseMessage(buf)
                if parseErr != nil || msg == nil {
                    action = "unknown"
                } else {
                    action = jsonrpc.Action(msg.Method)
                }
            }

            allowed, err := enforcer.Enforce(r.Context(), identity.Roles, serverName, action)
            if err != nil {
                logger.Error("rbac: enforce error", "server", serverName, "action", action, "err", err)
                allowed = enforcer.DefaultAllow()
            }

            if !allowed {
                w.Header().Set("Content-Type", "application/json")
                w.WriteHeader(http.StatusForbidden)
                _, _ = w.Write([]byte(`{"error":"forbidden"}`))
                return
            }

            next.ServeHTTP(w, r)
        })
    }
}

// RequireRole is a chi middleware that enforces a single required role on the
// admin API. It does not use Casbin — a simple slice scan is sufficient.
func RequireRole(role string, logger *slog.Logger) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            identity := auth.MustIdentity(r.Context())
            for _, assigned := range identity.Roles {
                if assigned == role {
                    next.ServeHTTP(w, r)
                    return
                }
            }
            logger.Info("rbac: admin access denied", "user", identity.Email, "required", role)
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusForbidden)
            _, _ = w.Write([]byte(`{"error":"forbidden"}`))
        })
    }
}
```

**Why `"connect"` on `GET /sse`:** the SSE stream channel is a long-lived connection; even before any
JSON-RPC method is sent, the upstream server may push events. A user without a matching policy should
not be able to establish the channel and receive those events. Enforcing `"connect"` closes the
information-leakage and resource-exhaustion vectors. The admin wildcard `p, admin, *, *` covers
`"connect"` transparently. For other roles, the policy documentation should make clear that `connect`
is a prerequisite for SSE access.

---

### 6. Admin API — `internal/admin/api/policies.go`

```go
package api

import (
    "encoding/json"
    "net/http"

    "github.com/K3y5tr0ke/mcpgate/internal/auth/rbac"
)

type policyHandler struct {
    enforcer *rbac.Enforcer
}

// list returns all 'p' policy rules as [{role, server, action}].
func (h *policyHandler) list(w http.ResponseWriter, r *http.Request) {
    writeJSON(w, http.StatusOK, h.enforcer.ListPolicies())
}

// add inserts a new policy rule.
// Body: {"role":"developer","server":"my-server","action":"call"}
func (h *policyHandler) add(w http.ResponseWriter, r *http.Request) {
    var body rbac.PolicyRule
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    if body.Role == "" || body.Server == "" || body.Action == "" {
        writeJSONError(w, http.StatusBadRequest, "role, server, and action are required")
        return
    }
    if err := h.enforcer.AddPolicy(r.Context(), body.Role, body.Server, body.Action); err != nil {
        writeJSONError(w, http.StatusInternalServerError, "failed to add policy")
        return
    }
    writeJSON(w, http.StatusCreated, body)
}

// remove deletes a policy rule.
// Body: {"role":"developer","server":"my-server","action":"call"}
func (h *policyHandler) remove(w http.ResponseWriter, r *http.Request) {
    var body rbac.PolicyRule
    if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
        writeJSONError(w, http.StatusBadRequest, "invalid request body")
        return
    }
    if err := h.enforcer.RemovePolicy(r.Context(), body.Role, body.Server, body.Action); err != nil {
        writeJSONError(w, http.StatusInternalServerError, "failed to remove policy")
        return
    }
    w.WriteHeader(http.StatusNoContent)
}
```

Routes: `GET /api/v1/admin/policies`, `POST /api/v1/admin/policies`, `DELETE /api/v1/admin/policies`.

---

### 7. Admin API — `internal/admin/api/roles.go`

```go
type roleHandler struct {
    store  store.RoleStore
    logger *slog.Logger
}

// list     GET  /roles          → []Role
// create   POST /roles          → Role (201); 409 on duplicate name
// get      GET  /roles/{id}     → Role
// update   PUT  /roles/{id}     → Role; 409 on name conflict; description only for system roles
// delete   DELETE /roles/{id}   → 204; 409 if is_system = true
// assign   POST /roles/{id}/users    body: {"user_id":"..."}   → 204
// revoke   DELETE /roles/{id}/users/{userID}                   → 204
```

Business rules enforced in handlers (not the store):
- `DELETE /roles/{id}`: read role first; if `is_system = true` → 409 `"system roles cannot be deleted"`.
- `PUT /roles/{id}`: if `is_system = true`, silently ignore any attempt to change the `name` field (only update `description`).

---

### 8. Admin API — `internal/admin/api/users.go`

Minimal user management needed to assign roles and suspend accounts.

```go
type userHandler struct {
    store  store.UserStore
    logger *slog.Logger
}

// list    GET  /users           → []User (all, no pagination in Sprint 4)
// get     GET  /users/{id}      → User
// patch   PATCH /users/{id}     body: {"status":"suspended"|"active"} → User
```

`PATCH /users/{id}` maps directly to `UserStore.Update()`. Only `status` is accepted; all other fields in the body are silently ignored.

---

### 9. Admin API — `internal/admin/api/router.go` Changes

**Updated `Router` struct:**

```go
type Router struct {
    servers  store.ServerStore
    idps     store.SAMLIDPConfigStore
    jwks     store.JWKSConfigStore
    roles    store.RoleStore     // NEW
    users    store.UserStore     // NEW
    enforcer *rbac.Enforcer      // NEW — for policy CRUD
    logger   *slog.Logger
}
```

**Updated `NewRouter` signature:**

```go
func NewRouter(
    servers  store.ServerStore,
    idps     store.SAMLIDPConfigStore,
    jwks     store.JWKSConfigStore,
    roles    store.RoleStore,
    users    store.UserStore,
    enforcer *rbac.Enforcer,
    logger   *slog.Logger,
) *Router
```

**New route groups added to `Handler()`:**

```go
rh := &roleHandler{store: rt.roles, logger: rt.logger}
r.Route("/roles", func(r chi.Router) {
    r.Get("/",    rh.list)
    r.Post("/",   rh.create)
    r.Route("/{id}", func(r chi.Router) {
        r.Get("/",    rh.get)
        r.Put("/",    rh.update)
        r.Delete("/", rh.delete)
        r.Post("/users",          rh.assign)
        r.Delete("/users/{userID}", rh.revoke)
    })
})

uh := &userHandler{store: rt.users, logger: rt.logger}
r.Route("/users", func(r chi.Router) {
    r.Get("/",       uh.list)
    r.Get("/{id}",   uh.get)
    r.Patch("/{id}", uh.patch)
})

ph := &policyHandler{enforcer: rt.enforcer}
r.Route("/policies", func(r chi.Router) {
    r.Get("/",    ph.list)
    r.Post("/",   ph.add)
    r.Delete("/", ph.remove)
})
```

Remove the placeholder `notImplemented` stubs for `/users` and `/roles`. Keep `/logs` and `/metrics`
stubs — those are Sprint 5 and 6.

---

### 10. Server Wiring — `internal/server/server.go`

**New field on `Server`:**

```go
enforcer *rbac.Enforcer
```

**In `New()`, after existing component initialisation:**

```go
policyCache := redistore.NewPolicyCache(redisClient)

enforcer, err := rbac.NewEnforcer(db, policyCache, cfg.RBAC, logger)
if err != nil {
    return nil, fmt.Errorf("init rbac enforcer: %w", err)
}

adminRouter := api.NewRouter(serverStore, samlIDPStore, jwksStore, roleStore, userStore, enforcer, logger)
```

**Updated `buildRouter()`:**

```go
// MCP proxy routes — auth + RBAC
r.Route("/mcp/{serverName}", func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(rbac.Authorize(s.enforcer, s.logger))  // NEW
    r.Post("/message", s.proxyHandler.HandleStreamableHTTP)
    r.Get("/sse",      s.proxyHandler.HandleSSEStream)
    r.Post("/sse",     s.proxyHandler.HandleSSEMessage)
})

// Admin API — auth + role check (not Casbin)
r.Group(func(r chi.Router) {
    r.Use(s.authMiddleware)
    r.Use(rbac.RequireRole("admin", s.logger))  // NEW
    r.Mount("/api/v1/admin", s.adminRouter.Handler())
})
```

---

### 11. Test Harness Update — `tests/e2e/setup_test.go`

Two changes are required because Sprint 4 flips `DefaultAction` from `"allow"` to `"deny"`:

**Change 1 — config:**

```go
// Before:
RBAC: config.RBACConfig{DefaultAction: "allow", CacheTTL: 30 * time.Second},

// After:
RBAC: config.RBACConfig{DefaultAction: "deny", CacheTTL: 30 * time.Second},
```

**Change 2 — `createAdminToken`:** store roles in SessionData and ensure the `admin` role is present
in the session cache so the auth middleware populates `UserIdentity.Roles` with `["admin"]`. Verify
that `"admin"` is included in the existing `ts.sessionCache.Set(...)` call:

```go
if err := ts.sessionCache.Set(ctx, sessionID, store.SessionData{
    UserID:    userID,
    Email:     "admin@test.internal",
    Roles:     []string{"admin"},   // must match seed policy v0 = 'admin'
    SessionID: sessionID,
}, time.Hour); err != nil { ... }
```

The `p, admin, *, *` seed policy (from migration 003) covers all RBAC checks for admin tokens in E2E
tests. No test-specific policy injection is required for the existing SAML and proxy tests.

---

## Test Plan

### Unit tests (no services, `-short` compatible)

| File | Tests |
|------|-------|
| `internal/auth/rbac/enforcer_test.go` | In-memory Casbin model (no DB): allow on matching rule, deny on no rule, wildcard server, wildcard action, short-circuit on first role match, defaultAllow=true fallback, defaultAllow=false fallback, cache hit bypasses enforcer call |
| `internal/auth/rbac/middleware_test.go` | GET→action="connect", POST with valid JSON-RPC→parsed action, POST with empty body→action="unknown", allowed→next called, denied→403, enforcer error + defaultAllow=true→pass, enforcer error + defaultAllow=false→403 |
| `internal/store/redis/policy_cache_test.go` | Set+Get hit, cache miss returns nil, expired key returns nil, Invalidate clears matching keys, Invalidate does not clear non-matching keys |

### Integration tests (`//go:build integration`)

| File | Tests |
|------|-------|
| `internal/store/postgres/roles_test.go` | Create, duplicate name (ErrAlreadyExists), GetByID, GetByName (ErrNotFound), List, Update, Delete system role (ErrConflict), Delete non-system role, AssignToUser idempotent, RevokeFromUser, GetUserRoles |
| `tests/e2e/rbac_test.go` | See §E2E below |

### E2E tests (`tests/e2e/rbac_test.go`)

```
TestE2E_RBAC_AdminRole_PassesWildcardPolicy
    Token with roles=["admin"] → POST /mcp/any-server/message tools/call → 200
    (admin wildcard from migration 003 seed)

TestE2E_RBAC_AdminRole_SSEConnect_Allowed
    Token with roles=["admin"] → GET /mcp/any-server/sse → 200 (or 101/SSE upgrade)
    Confirms "connect" action passes under wildcard.

TestE2E_RBAC_NoMatchingPolicy_Returns403
    Token with roles=["readonly"] (no policy added) → POST /mcp/any-server/message → 403

TestE2E_RBAC_DeveloperRole_AllowedServer_CanCall
    Seed policy: developer, mock-server, call
    Token with roles=["developer"] → POST /mcp/mock-server/message tools/call → 200
    Upstream MockMCPServer receives request (mock.RequestCount() > 0)

TestE2E_RBAC_DeveloperRole_AllowedServer_CanConnect
    Seed policy: developer, mock-server, connect
    Token with roles=["developer"] → GET /mcp/mock-server/sse → not 403

TestE2E_RBAC_DeveloperRole_WrongServer_Returns403
    Policy exists for mock-server only
    Token with roles=["developer"] → POST /mcp/other-server/message → 403

TestE2E_RBAC_ReadonlyRole_CanList_CannotCall
    Seed policy: readonly, *, list
    Token with roles=["readonly"] → tools/list → 200
    Token with roles=["readonly"] → tools/call → 403

TestE2E_RBAC_PolicyInvalidation_TakesEffect
    Add policy, verify 200, delete policy, verify 403
    Confirms ReloadPolicy + cache invalidation works end-to-end.

TestE2E_RBAC_AdminAPI_NonAdminRole_Returns403
    Token with roles=["developer"] → GET /api/v1/admin/servers → 403
    Confirms RequireRole("admin") is wired.

TestE2E_RBAC_AdminAPI_AdminRole_Allowed
    Token with roles=["admin"] → GET /api/v1/admin/servers → 200
```

Each test that requires non-admin roles seeds its own user + session + session cache entry
(following the `createAdminToken` pattern) with the appropriate `Roles` slice.

Policy mutations in tests call `POST /api/v1/admin/policies` using an admin token, then call
`DELETE /api/v1/admin/policies` in `t.Cleanup()` to leave the DB clean for parallel test runs.

---

## Ordered File List (Implementation Order)

| # | File | Reason for Order |
|---|------|-----------------|
| 1 | `db/migrations/003_rbac_seed.up.sql` | Schema and seed before any Go code |
| 2 | `db/migrations/003_rbac_seed.down.sql` | Paired rollback |
| 3 | `go get` casbin/v2 + pckhoi adapter + `go mod tidy` | Imports before compilation |
| 4 | `internal/store/redis/policy_cache.go` | No upstream deps, tested early |
| 5 | `internal/store/redis/policy_cache_test.go` | Integration test |
| 6 | `internal/store/postgres/roles.go` | Complete 8 stubs |
| 7 | `internal/store/postgres/roles_test.go` | Integration test |
| 8 | `internal/auth/rbac/enforcer.go` | Middleware depends on Enforcer |
| 9 | `internal/auth/rbac/enforcer_test.go` | Unit test |
| 10 | `internal/auth/rbac/middleware.go` | Server wiring depends on middleware |
| 11 | `internal/auth/rbac/middleware_test.go` | Unit test |
| 12 | `internal/admin/api/policies.go` | Router depends on handler |
| 13 | `internal/admin/api/roles.go` | Router depends on handler |
| 14 | `internal/admin/api/users.go` | Router depends on handler |
| 15 | `internal/admin/api/router.go` | Update struct, signature, wire routes |
| 16 | `internal/server/server.go` | Wire enforcer, update NewRouter call, add middlewares |
| 17 | `tests/e2e/setup_test.go` | Flip DefaultAction, verify admin roles |
| 18 | `tests/e2e/rbac_test.go` | E2E tests (new file) |
| 19 | `go build ./... && make ci-fast` | Verify clean build + lint |

---

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | `pckhoi/casbin-pgx-adapter/v3` with `WithConnectionPool()` + `WithSkipTableCreate()` | Reuses the existing `*pgxpool.Pool`; we own the schema via migrations, not the adapter's auto-create |
| D2 | Migration 003 drops and recreates `casbin_rule` | Schema mismatch (`ptype` vs `p_type`, `BIGSERIAL` vs `text`); table is empty in all environments so drop is safe |
| D3 | Skip Casbin `g` (group) rules; loop over `UserIdentity.Roles` | Roles are already in session data from Sprint 2; no dual-write to `casbin_rule` needed; keeps the model simple |
| D4 | `"connect"` action enforced on `GET /mcp/{serverName}/sse` | SSE channels can carry server-push events; unauthorised users must not establish the channel. An explicit `connect` permission requirement is consistent with the principle of least privilege and closes the information-leakage vector |
| D5 | Body buffered in `Authorize`, not inside proxy handler | Keeps the proxy handler transport-agnostic; enforcer sees the method before it is forwarded |
| D6 | `RequireRole("admin")` for admin API, not Casbin | Admin API access is a flat role check; routing it through Casbin would require an additional policy table for admin sub-resources with no practical benefit in Sprint 4 |
| D7 | `PolicyCache.Invalidate()` called synchronously after every policy mutation | Stale allow/deny cached decisions are a security risk; correctness takes priority over cache efficiency |
| D8 | `Enforcer.AddPolicy`/`RemovePolicy` wraps Casbin and calls `ReloadPolicy` | All policy state changes go through one code path; no way to mutate Casbin without invalidating the cache |
| D9 | System roles (`is_system=true`) cannot be deleted; name immutable | Prevents operators from breaking the bootstrap admin role that is referenced in seed policies |
| D10 | Seed policy uses deterministic `md5('p|admin|*|*')` as id | Makes the INSERT idempotent across migration re-runs; adapter's own hash strategy cannot be predicted without reading its source |

---

## Verification

### Unit and fast integration

```bash
make ci-fast
```

### Migration

```bash
make docker-up
make migrate-up
psql "$DATABASE_URL" -c "\d casbin_rule"
# expect: id text, p_type text, v0-v5 text
psql "$DATABASE_URL" -c "SELECT name, is_system FROM roles ORDER BY name;"
# expect: admin t, developer t, readonly t
psql "$DATABASE_URL" -c "SELECT v0, v1, v2 FROM casbin_rule WHERE p_type = 'p';"
# expect: admin | * | *
```

### Full integration suite

```bash
make docker-up && make migrate-up
go test -race -count=1 -tags=integration ./internal/...
```

### E2E with RBAC active (`DefaultAction: "deny"`)

```bash
set -a && source .env && set +a
go test -race -count=1 -tags=integration -v -timeout=300s ./tests/e2e/...
# Expected: all existing tests pass (admin token carries admin role → wildcard policy)
#           all RBAC tests pass
#           Auth0 smoke tests skip if AUTH0_* not exported (by design)
```

### Manual smoke

```bash
make dev

ADMIN_TOKEN="<token from SAML login>"

# No policy for developer role → 403
curl -si http://localhost:8080/mcp/any-server/message \
  -H "Authorization: Bearer $DEV_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
# → HTTP 403

# Admin wildcard → 200
curl -si http://localhost:8080/mcp/any-server/message \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
# → HTTP 200

# Add policy for developer
curl -s -X POST http://localhost:8080/api/v1/admin/policies \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role":"developer","server":"any-server","action":"list"}' | jq .

# Now developer can list
curl -si http://localhost:8080/mcp/any-server/message \
  -H "Authorization: Bearer $DEV_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
# → HTTP 200
```

---

## Security Properties Delivered

| Threat | Mitigation |
|--------|-----------|
| Authenticated user calls any MCP server | `Authorize` middleware denies without a matching Casbin policy |
| Unauthorised SSE stream connection leaks push events | `"connect"` action enforced on `GET /sse`; no policy → 403 before stream is opened |
| Stale cached allow decision after policy revocation | `PolicyCache.Invalidate("rbac:*")` called synchronously on every policy mutation |
| Non-admin user accesses admin API | `RequireRole("admin")` blocks before the handler is reached |
| Operator deletes the admin role | `is_system = true` check in the delete handler returns 409 |
| Admin wildcard policy deleted, locking out all admins | Seed migration is idempotent via `ON CONFLICT DO NOTHING`; re-running `make migrate-up` restores it |
| Casbin policy injection via JSON body | `AddPolicy` only accepts individual `role`/`server`/`action` strings; no raw Casbin rule strings are accepted from the API |
| Resource exhaustion via open SSE channels | Unauthorised connect attempts are rejected at the RBAC middleware before reaching the upstream |
