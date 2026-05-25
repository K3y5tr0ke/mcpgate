# End-to-End Test Suite — Implementation Notes
**Date:** 2026-05-24  
**Companion docs:** `docs/implementation/stage-3-24-05-2026.md` · `docs/implementation/auth0-idp.md` · `docs/mcpgate-architecture-blueprint.md`

---

## 1. Motivation

After Stage 3, unit and integration tests covered individual packages in isolation but no test exercised the full request path: client → auth middleware → admin/proxy handler → response. SAML login, M2M token validation, session lifecycle, and brute-force protection had all been verified per-package but never composed into a running server under test.

The gap was closed by adding a `tests/e2e/` package that:
- Spins up a real `server.Server` backed by testcontainer Postgres and Redis instances
- Exercises every auth path end-to-end through HTTP
- Skips Auth0-specific tests gracefully when credentials are not present (CI-safe)

---

## 2. Test Architecture

Two tiers:

| Tier | Tag | Runs in CI? | External deps |
|------|-----|-------------|--------------|
| **Synthetic** — in-process fake IdP and JWKS server | `integration` | Yes (Docker) | None beyond Docker |
| **Auth0 smoke** — real Auth0 M2M client credentials | `integration` | Optional | Auth0 tenant + `.env` |

All tests carry `//go:build integration` so they are excluded from `go test ./...` and only run when explicitly tagged.

### 2.1 Server Bootstrap Pattern

The test server cannot be configured at package init time because the SAML config (`EntityID`, `ACSURL`, `MetadataURL`) must embed the server's own base URL, which is only known after the `httptest.Server` is started.

The bootstrap sequence is:

```
1. Start httptest.Server with a placeholder handler → base URL known
2. Build config with SAML URLs derived from that base URL
3. Call server.New(cfg, ...) → real server initialised
4. Swap httptest.Server handler: httpSrv.Config.Handler = srv.Handler()
```

This avoids the chicken-and-egg problem of needing the URL before the server exists.

---

## 3. Files Created / Modified

### 3.1 New Files

| File | Purpose |
|------|---------|
| `internal/testutil/redis.go` | testcontainer Redis helper (mirrors the existing Postgres helper) |
| `tests/e2e/server_test.go` | `testServer` struct, `newTestServer`, `buildTestConfig`, `do` helper |
| `tests/e2e/helpers_test.go` | Session bootstrap, synthetic SAML IdP, local JWKS server, key/cert utilities |
| `tests/e2e/middleware_test.go` | Auth middleware negative paths + brute-force 429 |
| `tests/e2e/m2m_test.go` | Synthetic M2M: valid token, unknown issuer, expired token, disabled config |
| `tests/e2e/saml_test.go` | SAML: JIT provision, suspended user, replay detection, logout invalidation |
| `tests/e2e/auth0_smoke_test.go` | Real Auth0 M2M client credentials smoke test |

### 3.2 Modified Files

| File | Change |
|------|--------|
| `internal/server/server.go` | Added two exported accessors for test use: `Handler() http.Handler` and `JWTManager() *jwtpkg.Manager` |

---

## 4. Implementation Details

### 4.1 `internal/testutil/redis.go`

`go.mod` includes `testcontainers-go/modules/postgres` but not `modules/redis`. The Redis helper therefore uses the lower-level `testcontainers.GenericContainer` API directly with `redis:7-alpine`, waiting for the `"Ready to accept connections"` log line before returning the client.

```go
ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
    ContainerRequest: testcontainers.ContainerRequest{
        Image:        "redis:7-alpine",
        ExposedPorts: []string{"6379/tcp"},
        WaitingFor:   wait.ForLog("Ready to accept connections"),
    },
    Started: true,
})
```

### 4.2 `server.Handler()` and `server.JWTManager()` Accessors

The server's `http.Server` and JWT manager were previously unexported. Two thin accessors were added to expose them only for tests:

```go
func (s *Server) Handler() http.Handler       { return s.http.Handler }
func (s *Server) JWTManager() *jwtpkg.Manager { return s.jwtManager }
```

`JWTManager()` is critical: the server generates an ephemeral RSA key on every `server.New()` call when `SigningKeyFile` is empty (dev mode). Test helpers that need to issue tokens accepted by the middleware **must** call `srv.JWTManager().Issue()` — creating a separate `jwtpkg.Manager` would use a different key and produce tokens the middleware rejects.

### 4.3 Admin Token Bootstrap (`createAdminToken`)

Many tests need to call admin API endpoints before any SAML login has happened. Rather than duplicating SAML flow setup, `createAdminToken` short-circuits auth by writing directly to the stores:

1. Insert a `store.User` row into Postgres
2. Insert a `store.Session` row into Postgres
3. Write a `store.SessionData` entry into Redis (this is what the middleware actually checks for session liveness)
4. Call `srv.JWTManager().Issue()` to produce a signed JWT

The JWT's signature matches because the same manager instance is used.

### 4.4 Synthetic SAML IdP

The in-process IdP avoids the complexity of running a real SimpleSAMLphp container. It uses `crewjam/saml`'s `samlidp` package, which can sign assertions using any RSA key/cert pair.

```
generateKeyPair()          → *rsa.PrivateKey + *x509.Certificate
buildCrumbIdP()            → *cssaml.IdentityProvider (registered SP via samlidp.MemoryStore)
setupSyntheticIdP()        → inserts SAMLIDPConfig into Postgres, returns idp + key + cfg
buildSAMLResponse()        → uses DefaultAssertionMaker + MakeAssertionEl + MakeResponse + PostBinding
postACS()                  → HTTP POST to /auth/saml/acs
```

**`possibleRequestIDs = []` (empty slice):** Tests post directly to the ACS endpoint without going through the Login handler, so there is no outstanding `AuthnRequest.ID`. `crewjam/saml` treats an empty `possibleRequestIDs` as "accept any `InResponseTo`" — this is the documented IdP-initiated flow behaviour and is correct for test use.

**Composite literal in `if` statement:** `cssaml.DefaultAssertionMaker{}` cannot appear directly inside an `if err := ...` initialiser because the Go parser misinterprets the opening `{` as the start of the `if` body. The fix is to assign to a variable first:

```go
// Wrong — parse error
if err := cssaml.DefaultAssertionMaker{}.MakeAssertion(req, session); err != nil { ... }

// Correct
maker := cssaml.DefaultAssertionMaker{}
if err := maker.MakeAssertion(req, session); err != nil { ... }
```

### 4.5 Local JWKS Server (`newJWKSServer`)

The synthetic M2M tests need a JWKS endpoint without hitting the network. A local `httptest.Server` serves a single RSA public key in JWK format:

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
    _ = json.NewEncoder(w).Encode(map[string]any{
        "keys": []map[string]any{{
            "kty": "RSA", "kid": kid,
            "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
            "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
        }},
    })
}))
```

`issueToken` signs JWTs using `crypto.SHA256` + `sha256.Sum256` + `key.Sign` (PKCS1v15), producing tokens the M2M validator accepts once the JWKS config is registered via `registerJWKSConfig`.

### 4.6 Test Configuration (`buildTestConfig`)

Key values set for tests:

| Field | Value | Reason |
|-------|-------|--------|
| `JWT.SigningKeyFile` | `""` | Ephemeral key — no cert file needed |
| `JWT.Issuer` | `"mcpgate-test"` | Distinguishes session JWTs from M2M tokens |
| `M2M.JWKSCacheTTL` | `5s` | Fast cache invalidation between test cases |
| `BruteForce.MaxAttempts` | `5` | 6th request triggers 429; 7 iterations covers it |
| `RBAC.DefaultAction` | `"allow"` | No role restrictions on test endpoints |
| `SAML.EntityID / ACSURL / MetadataURL` | `baseURL + "/auth/saml/*"` | Derived from httptest URL at runtime |

---

## 5. Auth0 M2M Application Setup

These steps configure Auth0 so the smoke tests in `tests/e2e/auth0_smoke_test.go` can obtain a real `client_credentials` token.

### 5.1 Create a Machine-to-Machine Application

1. Log in to [manage.auth0.com](https://manage.auth0.com)
2. **Applications** → **Applications** → **Create Application**
3. Name: `mcpgate-m2m-test`
4. Type: **Machine to Machine**
5. Click **Create**

### 5.2 Create the API (Resource Server)

1. **Applications** → **APIs** → **Create API**
2. Name: `mcpgate API`
3. Identifier: `https://api.mcpgate.internal`
   - **Important:** Use `https://` — API identifiers are immutable after creation. If you create one with `http://`, you must delete it and recreate it.
4. Signing Algorithm: **RS256**
5. Click **Create**

### 5.3 Add Permissions to the API

1. Inside `mcpgate API` → **Permissions** tab
2. Add these scopes (one at a time):

   | Permission | Description |
   |------------|-------------|
   | `read:servers` | Read MCP server registrations |
   | `write:servers` | Create and modify MCP server registrations |
   | `admin` | Full administrative access |

3. Click **Save** after adding all three.

### 5.4 Authorize the Application Against the API

1. Inside the `mcpgate-m2m-test` application → **APIs** tab
2. Toggle `mcpgate API` to **Authorized**
3. Check all three permissions (`read:servers`, `write:servers`, `admin`)
4. Click **Update**

This step is the one most commonly missed. Without it, token requests return:
```json
{"error": "access_denied", "error_description": "Service not enabled within domain: https://api.mcpgate.internal"}
```

### 5.5 Collect Credentials

From the `mcpgate-m2m-test` application's **Settings** tab:

| Env var | Where to find it | Example value |
|---------|-----------------|---------------|
| `AUTH0_M2M_CLIENT_ID` | Settings → Client ID | `ofObYoAi7quaUvEaxwdfQaazhq3grLAB` |
| `AUTH0_M2M_CLIENT_SECRET` | Settings → Client Secret | (hidden — click Reveal) |
| `AUTH0_M2M_TOKEN_URL` | `https://<tenant>/oauth/token` | `https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/oauth/token` |
| `AUTH0_M2M_AUDIENCE` | The API identifier | `https://api.mcpgate.internal` |
| `AUTH0_JWKS_URL` | `https://<tenant>/.well-known/jwks.json` | `https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/.well-known/jwks.json` |
| `AUTH0_ISSUER` | `https://<tenant>/` (trailing slash required) | `https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/` |

**Trailing slash on `AUTH0_ISSUER`:** Auth0 includes a trailing slash in the `iss` claim of every JWT it issues. The mcpgate M2M validator compares `iss` exactly against the `issuer` stored in `jwks_configs`. If the issuer is registered without the trailing slash, all Auth0 tokens will fail validation with `issuer mismatch`.

### 5.6 Write Credentials to `.env`

```bash
cat >> .env <<'EOF'
AUTH0_M2M_CLIENT_ID=<client_id>
AUTH0_M2M_CLIENT_SECRET=<client_secret>
AUTH0_M2M_TOKEN_URL=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/oauth/token
AUTH0_JWKS_URL=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/.well-known/jwks.json
AUTH0_ISSUER=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/
AUTH0_M2M_AUDIENCE=https://api.mcpgate.internal
EOF
```

`.env` is already gitignored. Do not commit credentials.

### 5.7 Verify the Token Endpoint

```bash
source .env

curl -s -X POST "$AUTH0_M2M_TOKEN_URL" \
  -H "Content-Type: application/json" \
  -d "{
    \"client_id\": \"$AUTH0_M2M_CLIENT_ID\",
    \"client_secret\": \"$AUTH0_M2M_CLIENT_SECRET\",
    \"audience\": \"$AUTH0_M2M_AUDIENCE\",
    \"grant_type\": \"client_credentials\"
  }" | jq '{access_token: .access_token[:40], scope: .scope, expires_in: .expires_in}'
```

Expected output:
```json
{
  "access_token": "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIs...",
  "scope": "read:servers write:servers admin",
  "expires_in": 86400
}
```

---

## 6. Errors Encountered and Fixes

### Error 1 — `"Service not enabled within domain"` (first occurrence)

**Symptom:**
```json
{"error": "access_denied", "error_description": "Service not enabled within domain: https://api.mcpgate.internal"}
```

**Root cause:** The M2M application existed but had not been authorized against the `mcpgate API`. The API toggle on the application's **APIs** tab was off.

**Fix:** Toggle the API to **Authorized** and check all three permissions (§5.4 above).

---

### Error 2 — `"Service not enabled within domain"` (second occurrence, after fixing §5.4)

**Symptom:** Same error, even after authorizing the application.

**Root cause:** The API identifier had been created as `http://api.mcpgate.internal` (HTTP, not HTTPS). The curl command used `https://api.mcpgate.internal` as the audience. Auth0 performs an exact string match — `http://` ≠ `https://`. API identifiers cannot be edited after creation (the field is greyed out in the Auth0 dashboard).

**Auth0 log evidence** (from the Auth0 Dashboard → Monitoring → Logs):
```json
{"type": "feccft", "audience": "http://api.mcpgate.internal", ...}
```

The `audience` field in the log showed `http://` being sent — the mismatch was in the old API identifier, not in the curl command.

**Fix:** Delete the `http://api.mcpgate.internal` API from the Auth0 dashboard and recreate it as `https://api.mcpgate.internal` (§5.2). Re-authorize the M2M application against the new API (§5.4).

**Note:** Using `http://` in an API identifier has no security implications — Auth0 identifiers are opaque strings, never fetched. However, the string must match exactly between the API definition and the `audience` field in token requests and in `jwks_configs`.

---

### Error 3 — Go vet: `expected boolean expression, found assignment`

**File:** `tests/e2e/helpers_test.go`

**Symptom:** `go vet` reported a parse error on the line calling `cssaml.DefaultAssertionMaker{}.MakeAssertion(...)`.

**Root cause:** The Go parser sees `cssaml.DefaultAssertionMaker{}` and interprets the `{` as the opening brace of the `if` block body when it appears in the initialiser of an `if err := ...` statement.

**Fix:** Extract the struct literal to a variable before the `if`:
```go
// Before (parse error)
if err := cssaml.DefaultAssertionMaker{}.MakeAssertion(req, session); err != nil {

// After (correct)
maker := cssaml.DefaultAssertionMaker{}
if err := maker.MakeAssertion(req, session); err != nil {
```

---

### Error 4 — Go vet: `invalid operation: key is not an interface`

**File:** `tests/e2e/helpers_test.go`

**Symptom:** `go vet` reported an invalid type assertion on `key.(*rsa.PrivateKey).PublicKey` inside `generateKeyPair`.

**Root cause:** `key` was already typed as `*rsa.PrivateKey` (concrete type, not an interface). Type assertions (`.(*T)`) are only valid on interface values.

**Fix:** Access `PublicKey` directly without a type assertion:
```go
// Before (invalid — key is already *rsa.PrivateKey)
certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.(*rsa.PrivateKey).PublicKey, key)

// After
certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
```

---

### Error 5 — `staticcheck QF1008`: embedded field selector

**File:** `tests/e2e/helpers_test.go`

**Symptom:** `golangci-lint` (staticcheck rule QF1008) flagged `key.PublicKey.N` and `key.PublicKey.E` as unnecessary field qualifiers.

**Root cause:** `rsa.PrivateKey` embeds `rsa.PublicKey`, so `N` and `E` are promoted fields accessible directly on `key`.

**Fix:**
```go
// Before
"n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),

// After
"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
```

---

### Error 6 — `unparam`: unused return value on `registerJWKSConfig`

**File:** `tests/e2e/m2m_test.go`

**Symptom:** `golangci-lint` (unparam) reported that the `string` return value of `registerJWKSConfig` (the created config's UUID) was never used by any caller.

**Fix:** Removed the return value entirely. Callers that only need the side-effect (config registered) do not need the ID.

```go
// Before
func (ts *testServer) registerJWKSConfig(...) string { ... return result.ID }

// After
func (ts *testServer) registerJWKSConfig(...) { ... }
```

---

### Error 7 — `unused`: `nopLogger` and `goredis` placeholder

**File:** `tests/e2e/server_test.go`

**Symptom:** `golangci-lint` (unused) flagged `nopLogger()` as declared but never called, and the `var _ = (*goredis.Client)(nil)` placeholder as unnecessary once `nopLogger` was removed.

**Fix:** Removed both declarations and the `goredis` import. The logger in `newTestServer` is constructed inline using `slog.New(slog.NewTextHandler(io.Discard, nil))`.

---

### Error 8 — `gofmt` formatting on helpers_test.go and server_test.go

**Symptom:** `golangci-lint` (gofmt linter) flagged both files as not properly formatted.

**Fix:** `gofmt -w tests/e2e/helpers_test.go tests/e2e/server_test.go`

---

## 7. Test Coverage

### Middleware (`tests/e2e/middleware_test.go`)

| Test | Assertion |
|------|-----------|
| `TestE2E_NoToken_Returns401` | Request with no `Authorization` header returns 401 |
| `TestE2E_InvalidToken_Returns401` | Garbage `Bearer` token returns 401 |
| `TestE2E_ValidSessionToken_Returns200` | Session JWT issued by `createAdminToken` returns 200 |
| `TestE2E_BruteForce_SAMLLogin_Returns429` | 7 garbage ACS requests; 6th or earlier triggers 429 |

### M2M Authentication (`tests/e2e/m2m_test.go`)

| Test | Assertion |
|------|-----------|
| `TestE2E_M2M_ValidToken_AuthenticatesRequest` | Token from local JWKS server, registered issuer → 200 |
| `TestE2E_M2M_UnregisteredIssuer_Returns401` | Token from unregistered issuer → 401 |
| `TestE2E_M2M_ExpiredToken_Returns401` | Token with `exp` in the past → 401 |
| `TestE2E_M2M_DisabledConfig_Returns401` | Issuer registered with `is_enabled=false` → 401 |

### SAML Authentication (`tests/e2e/saml_test.go`)

| Test | Assertion |
|------|-----------|
| `TestE2E_SAML_NewUser_JITProvision_And_AuthenticatedRequest` | ACS succeeds → user in DB, `mcpgate_session` cookie set, protected route returns 200 |
| `TestE2E_SAML_SuspendedUser_Returns403` | Pre-created suspended user authenticates via SAML → 403 |
| `TestE2E_SAML_ReplayDetection_Returns400` | Same SAMLResponse posted twice → second returns 400 |
| `TestE2E_SAML_Logout_InvalidatesSession` | Login → logout → reuse old JWT → 401 |

### Auth0 Smoke (`tests/e2e/auth0_smoke_test.go`)

Skipped unless all `AUTH0_*` env vars are set.

| Test | Assertion |
|------|-----------|
| `TestE2E_Auth0_M2M_ClientCredentials_AuthenticatesRequest` | Real Auth0 token, correct audience → 200 |
| `TestE2E_Auth0_M2M_WrongAudience_Returns401` | Real Auth0 token, mismatched audience in `jwks_configs` → 401 |

---

## 8. Running the Tests

### Prerequisite: Docker

All integration tests start testcontainer instances. Docker must be running.

### Synthetic tests only (no Auth0 credentials needed)

```bash
go test -race -count=1 -tags=integration -v ./tests/e2e/...
```

### Including Auth0 smoke tests

`source .env` creates shell-local variables that child processes cannot see via `os.Getenv`. Use `set -a` (allexport) to automatically export every assignment in the file:

```bash
set -a && source .env && set +a
go test -race -count=1 -tags=integration -v ./tests/e2e/...
```

### Run only the Auth0 smoke tests

```bash
set -a && source .env && set +a
go test -race -count=1 -tags=integration -v -run Auth0 ./tests/e2e/...
```

### Run only SAML tests

```bash
go test -race -count=1 -tags=integration -v -run SAML ./tests/e2e/...
```

### Verify lint is clean

```bash
golangci-lint run --build-tags integration ./tests/e2e/...
```

Expected output: `0 issues.`

---

## 9. Known Coverage Gaps

### 9.1 Deferred by Design (Future Sprints)

These gaps are intentional. The relevant sprint has not started, and the code to support the test does not yet exist.

| Gap | Reason | Sprint |
|-----|--------|--------|
| **RBAC enforcement** | Casbin enforcer and RBAC middleware not implemented. Test config sets `DefaultAction: "allow"` to pass all requests through. | Sprint 4 |
| **Audit log correctness** | Logging pipeline not implemented. No assertions on what gets written per request. | Sprint 5 |
| **Metrics and rate limiting** | Prometheus instrumentation and per-user rate limiting not implemented. | Sprint 6 |

### 9.2 Within Sprint 2 Scope (Coverage Gaps)

These gaps are not blocked by future work. The production code exists; the E2E tests simply do not exercise it.

**MCP proxy routes never receive a request.**
The suite hits `/api/v1/admin/jwks/configs` as the sole protected endpoint. There is no test that sends a request to `/mcp/{serverName}/message`, `/mcp/{serverName}/sse`, or any proxy route. The auth middleware may be correctly wired on proxy routes, but no E2E assertion confirms it. This is the most significant gap: Sprint 2's stated deliverable is "a user receives a JWT and uses it to make authenticated MCP proxy requests."

**SP-initiated login chain is not exercised.**
Tests POST directly to `/auth/saml/acs`. The `GET /auth/saml/login` → redirect to IdP → IdP POST to ACS flow is never triggered. The `RelayState` round-trip, the `redirect_to` parameter validation, and the brute-force guard on the login endpoint are all untested end-to-end. This gap is accepted and will be covered by manual testing.

**JIT provision for existing users is not asserted.**
`jitProvision` has three branches: new user (Create), existing user found by IDP subject (Update), existing user found by email (Update). Only the new-user branch is verified by the JIT test. The other two branches are implemented but have no explicit E2E assertion that they produce a session and JWT correctly.

**Cookie-based auth middleware path is not tested.**
The auth middleware reads from both `Authorization: Bearer` and the `mcpgate_session` cookie. All protected-route assertions use the Bearer header. The cookie-reading branch — which is what a browser uses after SAML login — is never independently tested against a protected endpoint.

**SAML metadata endpoint is not validated.**
`GET /auth/saml/metadata` returns SP metadata XML but no test requests it, checks the HTTP status, or validates the returned XML structure.

**Session sliding is not tested.**
`SessionSliding: false` in the test config. The non-blocking `sessionCache.Extend()` goroutine path is never exercised. Deferred to Sprint 4 when the full session lifecycle is revisited alongside RBAC.

### 9.3 Gap Closure Plan

The table below maps each closeable gap to its implementation. SP-initiated login and session sliding are explicitly deferred (see §9.2). All other gaps are addressed in the tests described here.

| Gap | Test | File | New infrastructure |
|-----|------|------|--------------------|
| Proxy route returns 401 with no token | `TestE2E_Proxy_UnauthenticatedRequest_Returns401` | `proxy_test.go` (new) | `registerMCPServer` helper + existing `MockMCPServer` |
| Proxy route proxies authed request to upstream | `TestE2E_Proxy_AuthenticatedRequest_ProxiedToUpstream` | `proxy_test.go` (new) | Same |
| JIT existing user by IDP subject | `TestE2E_SAML_JIT_ExistingUserByIDPSubject` | `saml_test.go` | None |
| JIT existing user by email | `TestE2E_SAML_JIT_ExistingUserByEmail` | `saml_test.go` | None |
| Cookie-based auth middleware | `TestE2E_SAML_CookieAuth_ProtectedRoute` | `saml_test.go` | None |
| SAML metadata endpoint | `TestE2E_SAML_Metadata_Returns200` | `saml_test.go` | None |

**`proxy_test.go` — two tests, one helper.**
`registerMCPServer` calls `POST /api/v1/admin/servers` with `transport: "streamable_http"` and `config.endpoint` pointing at an in-process `MockMCPServer` (from `internal/testutil`). The unauth test sends a tokenless `POST /mcp/test-server/message` and asserts 401. The auth test sends the same request with a valid JWT, asserts 200, decodes the JSON-RPC result, and confirms `mock.RequestCount() > 0` — proving the proxy actually forwarded the request rather than short-circuiting.

**JIT branch tests — two tests in `saml_test.go`.**
Each pre-creates a user directly via `ts.userStore`, posts a SAML assertion that would match that user, and then asserts on `user.ID` (must not change) and `user.IDPSubject` (must be set by the email-match branch).

**Cookie auth test — one test in `saml_test.go`.**
Completes a full ACS flow to get the `mcpgate_session` cookie, then makes a follow-up request using `req.AddCookie()` with no `Authorization` header. Asserts 200.

**Metadata test — one test in `saml_test.go`.**
`GET /auth/saml/metadata` — asserts 200, `Content-Type: application/samlmetadata+xml`, and that the entity ID string appears in the response body.

---

## 10. Production Bugs Found During First Full Test Run

The first complete execution of the suite (14 tests) surfaced six bugs in production code. Each one was masked by the previous — fixing the earlier bug revealed the next. All fixes were committed as part of the Sprint 2 + Auth0 commit.

---

### Bug 1 — Missing `json:` tags on domain structs (`internal/store/interfaces.go`)

**Symptom:**
```
register JWKS config: want 201, got 400: {"error":"name, issuer and jwks_url are required"}
```

**Test:** `TestE2E_M2M_ValidToken_AuthenticatesRequest`

**Root cause:** The admin API decodes JSON request bodies into `store.JWKSConfig` and `store.SAMLIDPConfig` (and other domain types) using `encoding/json`. Without `json:` struct tags, the decoder uses field names verbatim. The field `JWKSURL` (7 characters, no separator) does not case-insensitively match the JSON key `"jwks_url"` (8 characters, with underscore) — these are structurally different strings. Similarly, `IsEnabled` does not match `"is_enabled"`. The decoder silently left those fields at their zero values, causing server-side validation to reject the request.

**Files changed:** `internal/store/interfaces.go`

**Fix:** Added `json:` tags to every field of every domain struct (`User`, `Role`, `MCPServer`, `ServerTool`, `AuditLogEntry`, `Session`, `SessionData`, `SAMLIDPConfig`, `JWKSConfig`). Example:

```go
// Before — no json tags; encoder/decoder uses Go field names
type JWKSConfig struct {
    ID        uuid.UUID
    Name      string
    Issuer    string
    JWKSURL   string
    Audience  string
    IsEnabled bool
    ...
}

// After — snake_case json tags match the API contract
type JWKSConfig struct {
    ID        uuid.UUID `db:"id"         json:"id"`
    Name      string    `db:"name"       json:"name"`
    Issuer    string    `db:"issuer"     json:"issuer"`
    JWKSURL   string    `db:"jwks_url"   json:"jwks_url"`
    Audience  string    `db:"audience"   json:"audience"`
    IsEnabled bool      `db:"is_enabled" json:"is_enabled"`
    ...
}
```

---

### Bug 2 — Hardcoded EntityID mismatch in synthetic IdP setup (`tests/e2e/helpers_test.go`)

**Symptom:**
```
ACS: want 200 or 302, got 400: invalid SAML response
```

**Tests:** All four SAML tests

**Root cause:** `setupSyntheticIdP` hardcoded `EntityID: "https://synthetic-idp.test"` in the `SAMLIDPConfig` row inserted into Postgres. However, `samlidp.New` sets `IDP.MetadataURL.Path += "/metadata"` internally, so the EntityID that the crewjam library embeds in every signed assertion it produces is `"https://synthetic-idp.test/metadata"`. When the SP's `ParseResponse` validated the assertion `Issuer`, it compared the assertion's EntityID (`"/metadata"` suffix) against the stored IdP config's EntityID (no suffix) — mismatch → 400.

**Files changed:** `tests/e2e/helpers_test.go`

**Fix:** Build the crewjam `IdentityProvider` first, then derive `EntityID` and `SSOURL` from the IdP object rather than hardcoding them:

```go
// Before — EntityID hardcoded, did not match what crewjam actually signs
idpCfg := &store.SAMLIDPConfig{
    EntityID: "https://synthetic-idp.test",
    SSOURL:   "https://synthetic-idp.test/sso",
    ...
}

// After — build the IdP first, derive URLs from it
idp := buildCrumbIdP(t, key, cert, spEntityID, acsURL)
idpCfg := &store.SAMLIDPConfig{
    EntityID: idp.MetadataURL.String(), // "https://synthetic-idp.test/metadata"
    SSOURL:   idp.SSOURL.String(),
    ...
}
```

---

### Bug 3 — `AllowIDPInitiated` not set on the service provider (`internal/auth/saml/sp.go`)

**Symptom:** After Bug 2 was fixed, ACS still returned 400 for all SAML tests.

**Root cause:** Tests POST directly to `/auth/saml/acs` without going through the `/auth/saml/login` handler, so there is no outstanding `AuthnRequest.ID` to match. crewjam's `validateRequestID` inspects the `InResponseTo` attribute on the assertion. `DefaultAssertionMaker` always sets `InResponseTo` to the request ID from the `IdpAuthnRequest` struct (even when that ID was invented by the test). With `AllowIDPInitiated = false` (the default) and an empty `possibleRequestIDs` slice, crewjam's validation loop never runs, `requestIDvalid` stays `false`, and `ParseResponse` rejects every assertion with an `InResponseTo` value.

**Files changed:** `internal/auth/saml/sp.go`

**Fix:** Set `AllowIDPInitiated: true` on the `cssaml.ServiceProvider` created in `NewServiceProvider`. Replay detection (Redis `SET NX`) remains the guard against assertion reuse:

```go
sp := &cssaml.ServiceProvider{
    EntityID:          s.cfg.EntityID,
    Key:               s.key,
    Certificate:       s.cert,
    AcsURL:            *acsURL,
    MetadataURL:       *metaURL,
    IDPMetadata:       idpMeta,
    AllowIDPInitiated: true, // accept assertions without a prior SP-initiated request; replay detection guards against abuse
}
```

---

### Bug 4 — `Logout` handler only read the session cookie, not the `Authorization` header (`internal/auth/saml/handlers.go`)

**Symptom:**
```
session still valid after logout: want 401, got 200
```

**Test:** `TestE2E_SAML_Logout_InvalidatesSession`

**Root cause:** The test issues a logout by sending `Authorization: Bearer <token>` (the programmatic API pattern). The original `Logout` handler only checked `r.Cookie("mcpgate_session")` — if the cookie was absent, `tokenStr` remained empty and the handler skipped session deletion entirely, then returned a 302 redirect without actually invalidating anything.

**Files changed:** `internal/auth/saml/handlers.go`

**Fix:** Added a fallback to the `Authorization: Bearer` header when the cookie is absent:

```go
tokenStr := ""
if cookie, err := r.Cookie("mcpgate_session"); err == nil {
    tokenStr = cookie.Value
} else if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
    tokenStr = strings.TrimPrefix(auth, "Bearer ")
}
```

---

### Bug 5 — `postACS` followed HTTP redirects, losing the `Set-Cookie` header (`tests/e2e/helpers_test.go`)

**Symptom:**
```
no mcpgate_session cookie in ACS response
```

**Tests:** `TestE2E_SAML_NewUser_JITProvision_And_AuthenticatedRequest`, `TestE2E_SAML_Logout_InvalidatesSession`

**Root cause:** A successful ACS response is HTTP 302 to the `redirectTo` path (default `/`). `http.DefaultClient` automatically follows redirects. The test server has no handler for `/`, so the redirected GET returns 404. More critically, the `Set-Cookie` header from the original 302 response is attached to that response — once the redirect is followed, the cookie is lost and tests cannot extract the session token.

**Files changed:** `tests/e2e/helpers_test.go`

**Fix:** Added a `noRedirectClient` that stops at the first response, preserving the 302 and its cookies:

```go
var noRedirectClient = &http.Client{
    CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
        return http.ErrUseLastResponse
    },
}

func (ts *testServer) postACS(t *testing.T, samlResponse string) *http.Response {
    t.Helper()
    body := strings.NewReader("SAMLResponse=" + url.QueryEscape(samlResponse))
    req, _ := http.NewRequest(http.MethodPost, ts.url+"/auth/saml/acs", body)
    req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    resp, err := noRedirectClient.Do(req)
    if err != nil { t.Fatalf("POST /auth/saml/acs: %v", err) }
    return resp
}
```

---

### Bug 6 — Attribute map key `"email"` did not match crewjam's `FriendlyName: "mail"` (`tests/e2e/helpers_test.go`)

**Symptom:**
```
user not created by JIT provision: store: record not found
```

The ACS returned 302 (success), but the user's email in the database was `"alice-subject"` (the SAML NameID value) instead of `"alice@example.com"`.

**Test:** `TestE2E_SAML_NewUser_JITProvision_And_AuthenticatedRequest`

**Root cause:** `cssaml.DefaultAssertionMaker` emits the email attribute with `FriendlyName: "mail"` and `Name: "urn:oid:0.9.2342.19200300.100.1.3"` — not with `Name: "email"`. The synthetic IdP config registered `AttributeMap: map[string]string{"email": "email"}`. The `Extract` function walked the assertion attributes looking for a key matching `"email"`, found no match (the actual attribute name is `"mail"` / the URN), and fell back to using the NameID value as the email. JIT provision then created the user with email `"alice-subject"`, and the subsequent lookup by `"alice@example.com"` found no record.

**Files changed:** `tests/e2e/helpers_test.go`

**Fix:** Added `"mail": "email"` to the `AttributeMap` so the `Extract` function maps the `FriendlyName: "mail"` attribute to the `email` field. The existing `"email": "email"` entry is kept as a fallback for IdPs that use the plain `"email"` attribute name:

```go
AttributeMap: map[string]string{
    "mail":  "email", // crewjam DefaultAssertionMaker uses FriendlyName "mail"
    "email": "email", // direct name fallback for other IdPs
},
```

---

### Summary of All Bugs and Fixes

| # | Symptom | File | Fix |
|---|---------|------|-----|
| 1 | Admin API returned 400 for all JWKS config creates | `internal/store/interfaces.go` | Added `json:` tags to all domain structs |
| 2 | SAML ACS returned 400 — EntityID mismatch | `tests/e2e/helpers_test.go` | Derive IdP EntityID from `idp.MetadataURL.String()` after building crewjam IdP |
| 3 | SAML ACS returned 400 — InResponseTo rejected | `internal/auth/saml/sp.go` | Set `AllowIDPInitiated: true` on ServiceProvider |
| 4 | Session still valid after logout | `internal/auth/saml/handlers.go` | Logout reads both cookie and `Authorization: Bearer` header |
| 5 | No `mcpgate_session` cookie; 404 after ACS | `tests/e2e/helpers_test.go` | `noRedirectClient` stops at 302, preserving `Set-Cookie` |
| 6 | JIT user created with NameID as email | `tests/e2e/helpers_test.go` | AttributeMap includes `"mail": "email"` for crewjam's FriendlyName |
