# Sprint 3 Implementation Plan — M2M JWKS Validation + Session Hardening

**Date:** 2026-05-24  
**Status:** Pre-implementation  
**Companion docs:** `docs/mcpgate-development-plan.md` · `docs/implementation/auth0-idp.md` · `docs/mcpgate-architecture-blueprint.md`

---

## Context

Sprint 2 delivered SAML 2.0 SP-initiated login via Auth0, JWT session tokens, and auth middleware guarding all proxy routes. Sprint 3 closes the programmatic access gap: AI agents and service accounts must be able to authenticate without a browser.

The design is intentionally **provider-agnostic**. Trusted M2M issuers are stored in a `jwks_configs` database table (mirroring `saml_idp_configs`). Adding a new provider (Okta, Azure AD, internal Vault PKI) requires only an admin API call — no code changes, no restart.

Auth0 is the first configured issuer; nothing in the implementation may hardcode Auth0-specific values.

Additionally: brute-force detection on SAML endpoints (Redis counters) and confirmation that session fixation is already prevented by design.

---

## Architecture: Middleware Routing Decision

The existing `Authenticate` middleware calls a single `TokenValidator` on every token. After Sprint 3 it must route to one of two validators based on the `iss` claim, which can be read without signature verification:

```
1. peekIssuer(tokenStr) → read iss from base64-decoded payload (no verification)

2. if iss == cfg.Auth.JWT.Issuer:
       → existing session JWT path (jwtpkg.Manager + Redis session liveness check)
   else if m2mValidator != nil:
       → JWKS validator path (issuer lookup → JWKS fetch → verify sig/aud/exp)
   else:
       → 401 "invalid token"
```

The session JWT path is byte-for-byte unchanged — all existing tests continue to pass.

---

## New Package Structure

```
internal/
├── auth/
│   ├── middleware.go          add M2MValidator interface, peekIssuer, update Authenticate()
│   ├── middleware_test.go     add M2M routing tests
│   └── jwks/
│       ├── client.go          JWKS HTTP client with TTL cache + kid-miss refresh
│       ├── client_test.go     unit tests (httptest fake JWKS server)
│       ├── validator.go       M2M JWT validator (issuer lookup → JWKS → verify)
│       ├── validator_test.go  unit tests (mock store + httptest JWKS)
│       └── validator_integration_test.go  //go:build integration
└── store/
    ├── interfaces.go          + JWKSConfig struct + JWKSConfigStore interface
    └── postgres/
        ├── jwks.go            JWKSConfigStore pgx implementation
        └── jwks_test.go       //go:build integration

internal/admin/api/
├── jwks.go                    JWKS config CRUD handler
└── router.go                  + jwks store field + /jwks/configs routes

internal/auth/saml/
├── brute_force.go             BruteForceGuard (Redis INCR-based)
├── brute_force_test.go        unit tests (mock Redis interface)
└── handlers.go                + guard field, guard checks in Login/ACS, fixation comment

internal/config/
└── config.go                  + M2MConfig + BruteForceConfig in AuthConfig

internal/server/
└── server.go                  instantiate JWKSConfigStore, jwks.Validator, wire middleware

migrations/
├── 002_jwks_configs.up.sql
└── 002_jwks_configs.down.sql
```

---

## New Dependencies

None. Everything needed is already imported or in the standard library:

- `encoding/json`, `crypto/rsa`, `crypto/x509`, `math/big`, `encoding/base64`, `sync` — stdlib
- `github.com/golang-jwt/jwt/v5` — already in `go.mod`

No `go get` required.

---

## Implementation Details

### 1. Migration `002_jwks_configs`

**`migrations/002_jwks_configs.up.sql`:**

```sql
BEGIN;

CREATE TABLE jwks_configs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        UNIQUE NOT NULL,
    issuer     TEXT        UNIQUE NOT NULL,
    jwks_url   TEXT        NOT NULL,
    audience   TEXT        NOT NULL DEFAULT '',
    is_enabled BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_jwks_configs_issuer ON jwks_configs (issuer) WHERE is_enabled = true;

CREATE TRIGGER jwks_configs_updated_at
    BEFORE UPDATE ON jwks_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
```

**`migrations/002_jwks_configs.down.sql`:**

```sql
BEGIN;
DROP TRIGGER IF EXISTS jwks_configs_updated_at ON jwks_configs;
DROP INDEX  IF EXISTS idx_jwks_configs_issuer;
DROP TABLE  IF EXISTS jwks_configs;
COMMIT;
```

Notes:
- Both `name` and `issuer` carry `UNIQUE` constraints — two configs cannot claim the same issuer.
- `audience` defaults to `''` (empty string = skip audience check). Single string; extend to `TEXT[]` in a future migration if needed.
- `set_updated_at()` trigger function already exists from migration `001`.
- The partial index on `issuer WHERE is_enabled = true` optimises the auth hot path (`GetByIssuer` always filters by enabled).

---

### 2. Store Layer

#### 2.1 `internal/store/interfaces.go` — additions

Add after the `SAMLIDPConfig`/`SAMLIDPConfigStore` block:

```go
type JWKSConfig struct {
    ID        uuid.UUID
    Name      string
    Issuer    string    // exact match against JWT iss claim
    JWKSURL   string
    Audience  string    // empty string = skip aud check
    IsEnabled bool
    CreatedAt time.Time
    UpdatedAt time.Time
}

type JWKSConfigStore interface {
    Create(ctx context.Context, cfg *JWKSConfig) error
    GetByID(ctx context.Context, id uuid.UUID) (*JWKSConfig, error)
    GetByName(ctx context.Context, name string) (*JWKSConfig, error)
    GetByIssuer(ctx context.Context, issuer string) (*JWKSConfig, error) // auth hot path
    List(ctx context.Context) ([]JWKSConfig, error)
    ListEnabled(ctx context.Context) ([]JWKSConfig, error)
    Update(ctx context.Context, cfg *JWKSConfig) error
    Delete(ctx context.Context, id uuid.UUID) error
}
```

`GetByIssuer` must only return enabled configs (`WHERE is_enabled = true`). A disabled config returns `store.ErrNotFound`, causing the middleware to reject the token — correct security posture.

#### 2.2 `internal/store/postgres/jwks.go`

Pattern mirrors `saml.go` exactly.

```go
type JWKSConfigStore struct{ db *pgxpool.Pool }

func NewJWKSConfigStore(db *pgxpool.Pool) *JWKSConfigStore
```

All columns are scalar — no JSONB marshalling. Key query:

```go
// GetByIssuer — auth hot path, filters by is_enabled = true
const getByIssuerSQL = `
SELECT id, name, issuer, jwks_url, audience, is_enabled, created_at, updated_at
FROM jwks_configs
WHERE issuer = $1 AND is_enabled = true`
```

- `Create`: detect unique violation (pgconn `"23505"`) on both `name` and `issuer` → `store.ErrAlreadyExists`.
- `Update`: `RowsAffected() == 0` → `store.ErrNotFound`.
- Reuse the `idpConfigScanner` interface already defined in `saml.go` for the scan helper (same package).

---

### 3. JWKS Client — `internal/auth/jwks/client.go`

```go
package jwks

// Client fetches and caches a JWKS from a remote URL.
// One Client is created per JWKSConfig; the Validator owns the map.
type Client struct {
    url        string
    httpClient *http.Client
    mu         sync.RWMutex
    keys       map[string]*rsa.PublicKey // kid → key
    fetchedAt  time.Time
    ttl        time.Duration
}

func NewClient(url string, ttl time.Duration) *Client

// GetKey returns the RSA public key for kid.
// If kid is absent from the cache, forces a refresh from the JWKS URL.
// Returns error if kid is still absent after refresh.
func (c *Client) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error)
```

**`GetKey` logic (double-checked locking):**
1. Read lock: cache fresh and `kid` present → return.
2. Write lock: re-check, then call `fetch` if still stale or kid absent.
3. Look up kid after fetch; error if missing.

**`fetch` logic:**
1. `GET c.url` with `httpClient` (30s timeout).
2. Decode `{"keys": [{kid, kty, n, e, ...}]}`.
3. For each RSA key: base64url-decode `n` and `e`, construct `rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(eBig.Int64())}`.
4. Replace `c.keys` map under write lock, update `c.fetchedAt`.

Internal types (unexported):

```go
type jwksResponse struct { Keys []jwk `json:"keys"` }
type jwk struct {
    Kid string `json:"kid"`
    Kty string `json:"kty"`
    N   string `json:"n"`
    E   string `json:"e"`
}
```

---

### 4. M2M Validator — `internal/auth/jwks/validator.go`

```go
package jwks

type Validator struct {
    store     store.JWKSConfigStore
    clients   map[string]*Client // issuer → Client, protected by mu
    mu        sync.RWMutex
    clientTTL time.Duration
    logger    *slog.Logger
}

func NewValidator(store store.JWKSConfigStore, clientTTL time.Duration, logger *slog.Logger) *Validator

// ValidateToken implements auth.M2MValidator.
func (v *Validator) ValidateToken(ctx context.Context, tokenStr string) (*auth.UserIdentity, error)
```

**`ValidateToken` steps:**
1. Parse JWT header (no verification) → extract `kid`, reject if `alg != "RS256"`.
2. Parse JWT payload (no verification) → extract `iss`.
3. `store.GetByIssuer(ctx, iss)` → `ErrNotFound` → error "unknown issuer".
4. Get or create `Client` for `cfg.JWKSURL` (write-locked on first creation).
5. `client.GetKey(ctx, kid)`.
6. Verify with `golang-jwt/jwt/v5`:
   ```go
   token, err := jwt.ParseWithClaims(tokenStr, &m2mClaims{},
       func(t *jwt.Token) (any, error) {
           if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
               return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
           }
           return client.GetKey(ctx, t.Header["kid"].(string))
       },
       jwt.WithIssuer(cfg.Issuer),
       jwt.WithExpirationRequired(),
   )
   ```
7. If `cfg.Audience != ""`: verify `claims.Audience` contains `cfg.Audience`.
8. Return `auth.UserIdentity`:
   ```go
   &auth.UserIdentity{
       UserID:    uuid.Nil,          // M2M clients have no users table row
       Email:     claims.Subject,    // sub used as identity string
       Roles:     claims.Permissions,
       SessionID: uuid.Nil,          // no session record
   }
   ```

**Custom claims type:**
```go
type m2mClaims struct {
    jwt.RegisteredClaims
    Permissions []string `json:"permissions"` // Auth0 + standard OIDC convention
}
```

**Client cache:** `Validator.clients` is an in-memory map, never evicted. Changing a `jwks_url` in the DB takes effect on the next process restart. This is acceptable for Sprint 3 — document as a known limitation.

---

### 5. Config Changes — `internal/config/config.go`

Add to `AuthConfig`:

```go
type AuthConfig struct {
    SessionTTL     time.Duration    `koanf:"session_ttl"`
    SessionSliding bool             `koanf:"session_sliding"`
    JWT            JWTConfig        `koanf:"jwt"`
    M2M            M2MConfig        `koanf:"m2m"`
    BruteForce     BruteForceConfig `koanf:"brute_force"`
}

type M2MConfig struct {
    JWKSCacheTTL time.Duration `koanf:"jwks_cache_ttl"` // default 1h
}

type BruteForceConfig struct {
    Enabled     bool          `koanf:"enabled"`
    MaxAttempts int           `koanf:"max_attempts"` // default 10
    Window      time.Duration `koanf:"window"`       // default 15m
    BlockFor    time.Duration `koanf:"block_for"`    // default 1h
}
```

Defaults:
```go
M2M: M2MConfig{JWKSCacheTTL: time.Hour},
BruteForce: BruteForceConfig{
    Enabled:     true,
    MaxAttempts: 10,
    Window:      15 * time.Minute,
    BlockFor:    time.Hour,
},
```

Env var overrides follow existing koanf convention (e.g. `MCPGATE_AUTH_BRUTE_FORCE_MAX_ATTEMPTS`).

---

### 6. Middleware Changes — `internal/auth/middleware.go`

#### 6.1 New `M2MValidator` interface

```go
// M2MValidator validates externally-issued JWTs from configured JWKS providers.
// Nil disables M2M authentication.
type M2MValidator interface {
    ValidateToken(ctx context.Context, tokenString string) (*UserIdentity, error)
}
```

`*jwks.Validator` satisfies this interface.

#### 6.2 `peekIssuer` helper

```go
// peekIssuer decodes the JWT payload without verifying the signature
// and returns the iss claim. Used to route to the correct validator.
func peekIssuer(tokenString string) (string, error) {
    parts := strings.SplitN(tokenString, ".", 3)
    if len(parts) != 3 {
        return "", fmt.Errorf("not a JWT")
    }
    payload, err := base64.RawURLEncoding.DecodeString(parts[1])
    if err != nil {
        return "", fmt.Errorf("decode payload: %w", err)
    }
    var claims struct {
        Issuer string `json:"iss"`
    }
    if err := json.Unmarshal(payload, &claims); err != nil {
        return "", fmt.Errorf("unmarshal payload: %w", err)
    }
    return claims.Issuer, nil
}
```

#### 6.3 Updated `Authenticate` signature

```go
func Authenticate(
    validator    TokenValidator,  // existing — mcpgate session JWTs
    m2mValidator M2MValidator,    // new — nil disables M2M
    sessionCache store.SessionCache,
    cfg          config.AuthConfig,
    logger       *slog.Logger,
) func(http.Handler) http.Handler
```

#### 6.4 Routing logic inside `Authenticate`

```go
iss, _ := peekIssuer(tokenStr) // error → iss == "", falls to session path

if iss == cfg.JWT.Issuer || iss == "" {
    // Session JWT path (unchanged)
    claims, err := validator.Validate(tokenStr) → 401 on failure
    sessionID  := uuid.Parse(claims.SessionID)  → 401 on failure
    sessionData := sessionCache.Get(ctx, sessionID) → 401 on miss
    identity := UserIdentity from sessionData
    if cfg.SessionSliding { go extend() }
} else if m2mValidator != nil {
    // M2M path
    identity, err := m2mValidator.ValidateToken(ctx, tokenStr) → 401 on failure
} else {
    writeAuthError(w, "invalid token") // unknown issuer, M2M disabled
    return
}

next.ServeHTTP(w, r.WithContext(WithIdentity(ctx, identity)))
```

The session JWT path is byte-for-byte equivalent to the current code — all existing tests pass.

---

### 7. Brute-Force Detection — `internal/auth/saml/brute_force.go`

Extract a minimal Redis interface so the guard is testable without a real Redis:

```go
type redisCounter interface {
    Incr(ctx context.Context, key string) *redis.IntCmd
    Expire(ctx context.Context, key string, d time.Duration) *redis.BoolCmd
    Del(ctx context.Context, keys ...string) *redis.IntCmd
}

type BruteForceGuard struct {
    redis       redisCounter
    maxAttempts int64
    window      time.Duration
    blockFor    time.Duration
    enabled     bool
}

func NewBruteForceGuard(rc redisCounter, cfg config.BruteForceConfig) *BruteForceGuard

// Check returns true if the IP is rate-limited. Always increments the counter.
func (g *BruteForceGuard) Check(ctx context.Context, key string) bool

// Reset deletes the counter on successful auth.
func (g *BruteForceGuard) Reset(ctx context.Context, key string)
```

**`Check` implementation:**
```go
func (g *BruteForceGuard) Check(ctx context.Context, key string) bool {
    if !g.enabled {
        return false
    }
    count, err := g.redis.Incr(ctx, key).Result()
    if err != nil {
        return false // Redis failure → allow (availability over enforcement)
    }
    if count == 1 {
        g.redis.Expire(ctx, key, g.window)
    }
    if count > g.maxAttempts {
        g.redis.Expire(ctx, key, g.blockFor) // extend block for repeat offenders
        return true
    }
    return false
}
```

**Redis key scheme:** `brute:<endpoint>:<ip>` — e.g. `brute:saml_login:192.0.2.1`.

**Integration with `Handlers`:**
- Add `guard *BruteForceGuard` to `Handlers`. Initialise inside `NewHandlers` from `authCfg.BruteForce`.
- `Login`: check `brute:saml_login:<ip>` → 429 if blocked.
- `ACS`: check `brute:saml_acs:<ip>` → 429 if blocked. On success: `Reset` both keys.

IP extraction reuses the existing `net.SplitHostPort(r.RemoteAddr)` pattern from the ACS handler.

---

### 8. Session Fixation Review

**Current ACS handler (`handlers.go:213`):**
```go
sessionID := uuid.New()
```

A new UUID is generated at ACS completion. No session cookie or session record exists before this point — there is nothing for an attacker to fix. The SAML anti-CSRF protection is the `AuthnRequest.ID` embedded in `RelayState`.

**Conclusion: session fixation is already prevented by design. No code change required.**

Add a comment above the line to make the intent explicit:

```go
// sessionID is always newly generated here — no pre-auth session exists to fix.
sessionID := uuid.New()
```

---

### 9. Admin API — `internal/admin/api/jwks.go` and `router.go`

#### 9.1 `jwks.go`

Mirrors `idp.go` exactly.

```go
type jwksHandler struct {
    store  store.JWKSConfigStore
    logger *slog.Logger
}

func (h *jwksHandler) list(w http.ResponseWriter, r *http.Request)
func (h *jwksHandler) create(w http.ResponseWriter, r *http.Request)
func (h *jwksHandler) get(w http.ResponseWriter, r *http.Request)
func (h *jwksHandler) update(w http.ResponseWriter, r *http.Request)
func (h *jwksHandler) delete(w http.ResponseWriter, r *http.Request)
```

**`create` required fields:** `name`, `issuer`, `jwks_url`. `audience` is optional (empty = skip aud check).

**Request body:**
```json
{
  "name":       "auth0-m2m",
  "issuer":     "https://dev-xyz.us.auth0.com/",
  "jwks_url":   "https://dev-xyz.us.auth0.com/.well-known/jwks.json",
  "audience":   "https://api.mcpgate.internal",
  "is_enabled": true
}
```

**409 Conflict message:** `"JWKS config with this name or issuer already exists"` (covers both unique constraints).

#### 9.2 `router.go` changes

Add `jwks store.JWKSConfigStore` to `Router` struct and `NewRouter` signature:

```go
func NewRouter(
    servers store.ServerStore,
    idps    store.SAMLIDPConfigStore,
    jwks    store.JWKSConfigStore,
    logger  *slog.Logger,
) *Router
```

Register routes after the `/idps` group:

```go
jh := &jwksHandler{store: rt.jwks, logger: rt.logger}
r.Route("/jwks/configs", func(r chi.Router) {
    r.Get("/",      jh.list)
    r.Post("/",     jh.create)
    r.Get("/{id}",  jh.get)
    r.Put("/{id}",  jh.update)
    r.Delete("/{id}", jh.delete)
})
```

Full path: `/api/v1/admin/jwks/configs[/{id}]`.

---

### 10. Server Wiring — `internal/server/server.go`

**New struct fields:**
```go
jwksStore     *pgstore.JWKSConfigStore
jwksValidator *jwkspkg.Validator
```

**In `New()`:**
```go
jwksStore := pgstore.NewJWKSConfigStore(db)

jwksTTL := cfg.Auth.M2M.JWKSCacheTTL
if jwksTTL == 0 {
    jwksTTL = time.Hour
}
jwksValidator := jwkspkg.NewValidator(jwksStore, jwksTTL, logger)

authMiddleware := auth.Authenticate(jwtManager, jwksValidator, sessionCache, cfg.Auth, logger)

adminRouter := api.NewRouter(serverStore, samlIDPStore, jwksStore, logger)
```

The `saml.NewHandlers` call gains `BruteForceGuard` indirectly — `NewHandlers` reads `authCfg.BruteForce` from the `config.AuthConfig` it already receives. No signature change needed for `saml.NewHandlers`.

---

## Test Plan

### Unit tests (no services, `-short` compatible)

| File | Tests |
|------|-------|
| `internal/auth/jwks/client_test.go` | Cache hit, cache miss → refresh, unknown kid after refresh, expired cache triggers re-fetch, server 500, malformed JWKS JSON, concurrent access (run with `-race`) |
| `internal/auth/jwks/validator_test.go` | Valid token, unknown issuer, disabled issuer (ErrNotFound), expired token, wrong audience, empty audience skips check, wrong alg (HS256), `permissions` → Roles, no permissions → empty slice |
| `internal/auth/middleware_test.go` (additions) | M2M valid token → identity in context, M2M invalid token → 401, nil m2mValidator + external iss → 401, session JWT path unchanged, `peekIssuer` valid/invalid/malformed-base64 |
| `internal/auth/saml/brute_force_test.go` | Under limit → allow, at limit → block, reset clears counter, disabled → always allow, Redis error → allow |

### Integration tests (`//go:build integration`)

| File | Tests |
|------|-------|
| `internal/store/postgres/jwks_test.go` | Full CRUD, duplicate name (409), duplicate issuer (409), GetByIssuer, GetByIssuer on disabled (ErrNotFound), ListEnabled filters disabled |
| `internal/auth/jwks/validator_integration_test.go` | End-to-end: seed jwks_configs row → httptest JWKS server with generated RSA key → sign JWT offline → validate → UserIdentity returned |

---

## Ordered File List (Implementation Order)

| # | File | Reason for Order |
|---|------|-----------------|
| 1 | `migrations/002_jwks_configs.up.sql` | Schema before any Go code |
| 2 | `migrations/002_jwks_configs.down.sql` | Paired rollback |
| 3 | `internal/store/interfaces.go` | All Go files import this |
| 4 | `internal/config/config.go` | Server wiring imports config |
| 5 | `internal/store/postgres/jwks.go` | Store impl |
| 6 | `internal/store/postgres/jwks_test.go` | Integration test |
| 7 | `internal/auth/jwks/client.go` | Validator depends on Client |
| 8 | `internal/auth/jwks/client_test.go` | Unit test |
| 9 | `internal/auth/jwks/validator.go` | Middleware depends on Validator |
| 10 | `internal/auth/jwks/validator_test.go` | Unit test |
| 11 | `internal/auth/jwks/validator_integration_test.go` | Integration test |
| 12 | `internal/auth/saml/brute_force.go` | Handlers depend on guard |
| 13 | `internal/auth/saml/brute_force_test.go` | Unit test |
| 14 | `internal/auth/saml/handlers.go` | Add guard + fixation comment |
| 15 | `internal/auth/middleware.go` | M2MValidator interface + peekIssuer + routing |
| 16 | `internal/auth/middleware_test.go` | Extend with M2M cases |
| 17 | `internal/admin/api/jwks.go` | Admin handler |
| 18 | `internal/admin/api/router.go` | Wire routes |
| 19 | `internal/server/server.go` | Wire all components |
| 20 | `go build ./... && make ci-fast` | Verify clean build + lint |

---

## Key Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | `jwks_configs` table mirrors `saml_idp_configs` pattern | Consistent admin API surface; adding a provider is a data operation not a code deploy |
| D2 | `peekIssuer` reads iss without signature verification | Safe — we only use iss to route to the correct validator; the validator verifies the signature |
| D3 | `M2MValidator` is a separate interface from `TokenValidator` | `TokenValidator` returns `*jwtpkg.Claims` (mcpgate-specific). M2M returns `*UserIdentity`. Keeping them separate avoids forcing a large refactor of the session JWT path |
| D4 | `UserID: uuid.Nil` for M2M identities | M2M clients are not users table rows; roles come from the `permissions` JWT claim |
| D5 | `Validator.clients` map never evicted | JWKS configs change rarely; acceptable for Sprint 3. Changing jwks_url takes effect on restart. Documented as known limitation |
| D6 | `BruteForceGuard` takes a `redisCounter` interface not `*redis.Client` | Makes guard unit-testable without a real Redis instance |
| D7 | Session fixation: no code change | ACS always generates `uuid.New()` — no pre-auth session exists to fix |
| D8 | `audience == ""` skips aud check | Some JWKS providers do not set aud; making it optional avoids breaking those configs |

---

## Verification

### Unit tests (no services)

```bash
make ci-fast
```

### Migration

```bash
make docker-up
make migrate-up
psql "$DATABASE_URL" -c "\d jwks_configs"
psql "$DATABASE_URL" -c "\di idx_jwks_configs_issuer"
```

### Seed Auth0 as first JWKS issuer

```bash
# Requires a valid admin JWT in $TOKEN (from SAML login)
curl -s -X POST http://localhost:8080/api/v1/admin/jwks/configs \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name":       "auth0-m2m",
    "issuer":     "https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/",
    "jwks_url":   "https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/.well-known/jwks.json",
    "audience":   "https://api.mcpgate.internal",
    "is_enabled": true
  }' | jq .
```

### M2M authentication end-to-end

```bash
# 1. Get Auth0 M2M token (requires Auth0 M2M application configured)
M2M_TOKEN=$(curl -s -X POST \
  https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/oauth/token \
  -H "Content-Type: application/json" \
  -d '{
    "client_id":     "<AUTH0_CLIENT_ID>",
    "client_secret": "<AUTH0_CLIENT_SECRET>",
    "audience":      "https://api.mcpgate.internal",
    "grant_type":    "client_credentials"
  }' | jq -r .access_token)

# 2. Use M2M token on a protected route
curl -s http://localhost:8080/api/v1/admin/jwks/configs \
  -H "Authorization: Bearer $M2M_TOKEN" | jq .

# 3. Verify unknown issuer → 401
curl -si http://localhost:8080/api/v1/admin/jwks/configs \
  -H "Authorization: Bearer eyJhbGciOiJSUzI1NiJ9.eyJpc3MiOiJodHRwczovL3Vua25vd24uZXhhbXBsZS5jb20vIn0.sig"
# → HTTP 401
```

### Brute-force detection

```bash
# Fire 12 rapid requests — 11th+ should return 429
for i in $(seq 1 12); do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/auth/saml/login)
  echo "Request $i: $STATUS"
done
```

### Integration tests

```bash
make docker-up && make migrate-up
go test -race -count=1 -tags=integration ./internal/...
```

---

## Security Properties Delivered

| Threat | Mitigation |
|--------|-----------|
| Forged M2M tokens | RS256 signature verified against JWKS from the registered issuer URL |
| Tokens from unknown issuers | `GetByIssuer` returns `ErrNotFound` → 401 |
| Expired M2M tokens | `jwt.WithExpirationRequired()` enforced |
| Wrong audience | `cfg.Audience != ""` → `aud` claim checked exactly |
| Adding a provider requires code deploy | Admin API only — new row in `jwks_configs`, no restart needed |
| SAML endpoint brute force | Redis INCR per IP per endpoint, 429 after configurable threshold |
| Session fixation | Not possible — no session exists before ACS, `uuid.New()` on every completion |
| JWKS cache poisoning | Keys fetched via HTTPS only; bounded `http.Client` timeout |
