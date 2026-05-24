# Auth0 SAML IdP — Setup and Debug Log
**Date:** 2026-05-23 / 2026-05-24  
**Companion docs:** `docs/implementation/stage-2-23-05-2026-2.md` · `docs/mcpgate-architecture-blueprint.md`

---

## 1. Decisions Made

### 1.1 Auth0 as Upstream IdP, Not ory/fosite as OAuth Server

The original Sprint 3 plan was for mcpgate to run its own OAuth 2.0 authorization server using `ory/fosite`. After discussion, this was replaced with Auth0 as the upstream identity provider for two reasons:

1. **Mirrors production** — delegating identity to Auth0 gives MFA, social login, audit logs, and threat detection without custom code
2. **Simpler architecture** — mcpgate issues its own session JWTs after SAML login; Auth0 M2M client credentials handle service account authentication, eliminating the need for a local OAuth server entirely

| Auth mechanism | Use case |
|---------------|----------|
| Auth0 as SAML IdP → mcpgate session JWT | Human browser login |
| Auth0 Machine-to-Machine (client credentials) | AI agents / service accounts |

### 1.2 SAML over OIDC (for Now)

SAML was chosen first because the SP implementation was already complete in Sprint 2. OIDC can be added later as an alternative login path using the same session issuance infrastructure.

### 1.3 No ngrok Required

SAML uses the **browser as the relay** — Auth0 never calls mcpgate directly. The flow is:

```
Browser → GET localhost:8080/auth/saml/login     → mcpgate
mcpgate → 302 to Auth0 SSO URL                  → browser
Browser → GET auth0.com/samlp/<client_id>        → Auth0
Auth0   → HTML page with auto-submit POST form    → browser
Browser → POST localhost:8080/auth/saml/acs       → mcpgate
```

Step 4 is an HTML form that the browser auto-submits (via JavaScript). Auth0 is never making a direct network connection to `localhost` — the browser is. This is exactly how enterprise apps behind corporate firewalls use SAML.

### 1.4 Chrome Required for Local Dev (Safari ITP Limitation)

Safari's Intelligent Tracking Prevention (ITP) blocks session cookies during Auth0's Universal Login redirect chain, causing the flow to fail with `access_denied`. Chrome does not apply ITP and works correctly for local dev. For production with Safari users, an [Auth0 Custom Domain](https://auth0.com/docs/customize/custom-domains) eliminates this by putting Auth0 on the same domain as the app.

---

## 2. Auth0 Dashboard Configuration

### 2.1 Create the Application

1. Log in to [manage.auth0.com](https://manage.auth0.com)
2. **Applications** → **Applications** → **Create Application**
3. Name: `mcpgate`
4. Type: **Regular Web Application**
5. Click **Create**

### 2.2 Enable the SAML2 Addon

1. Inside the `mcpgate` application → **Addons** tab
2. Click **SAML2 Web App**
3. In the settings modal, set **Application Callback URL** (ACS URL):
   ```
   http://localhost:8080/auth/saml/acs
   ```
4. In the **Settings** JSON box:
   ```json
   {
     "audience": "http://localhost:8080/auth/saml/metadata",
     "mappings": {
       "email": "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress",
       "given_name": "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/givenname",
       "family_name": "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/surname"
     },
     "nameIdentifierFormat": "urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress",
     "nameIdentifierProbes": [
       "http://schemas.xmlsoap.org/ws/2005/05/identity/claims/emailaddress"
     ]
   }
   ```
   The `audience` value must exactly match the SP's `entity_id` in mcpgate's config.
5. Click **Enable**

### 2.3 Set the Application Login URI

1. **Settings** tab → scroll to **Application Login URI**
2. Set to: `http://localhost:8080/auth/saml/login`
3. Click **Save Changes**

This tells Auth0 where to redirect users who land on Auth0's login page without a proper SAML request context, which suppresses the `w`-type warning in Auth0 logs.

### 2.4 Enable Connections

1. **Connections** tab
2. Toggle **Username-Password-Authentication** on

Without at least one connection enabled, Auth0 cannot show a login form.

### 2.5 Download the X.509 Certificate

1. **Addons** → **SAML2 Web App** → **Usage** tab
2. Click **Download Certificate** (PEM format)
3. Save to `certs/dev-0ziq1qjvgrwq4w2g.pem`

---

## 3. Values Collected from Auth0

These were recorded in `.env` and `certs/`:

| Field | Value |
|-------|-------|
| **SSO URL (IdP Login URL)** | `https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8` |
| **Entity ID (Issuer)** | `urn:dev-0ziq1qjvgrwq4w2g.us.auth0.com` |
| **Certificate file** | `certs/dev-0ziq1qjvgrwq4w2g.pem` |
| **Auth0 Tenant** | `dev-0ziq1qjvgrwq4w2g.us.auth0.com` |
| **Auth0 Client ID** | `k39kf4mjdKlScqCp6B1mtSgGpSmmROk8` |
| **Auth0 Metadata URL** | `https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/metadata/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8` |

The entity ID and certificate were verified by fetching Auth0's live metadata:

```bash
curl -s "https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/metadata/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8" \
  | python3 -c "
import sys, xml.etree.ElementTree as ET
tree = ET.parse(sys.stdin)
root = tree.getroot()
ns = {'md': 'urn:oasis:names:tc:SAML:2.0:metadata', 'ds': 'http://www.w3.org/2000/09/xmldsig#'}
print('EntityID:', root.attrib.get('entityID'))
for cert in root.findall('.//ds:X509Certificate', ns):
    print('Cert (first 60 chars):', cert.text.strip()[:60])
for sso in root.findall('.//md:SingleSignOnService', ns):
    print('SSO:', sso.attrib.get('Binding','').split(':')[-1], sso.attrib.get('Location'))
"
```

Output confirmed:
```
EntityID: urn:dev-0ziq1qjvgrwq4w2g.us.auth0.com
Cert (first 60 chars): MIIDHTCCAgWgAwIBAgIJOPaDmmzSx0MHMA0GCSqGSIb3DQEBCwUAMCwxKjAo
SSO: HTTP-Redirect https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8
SSO: HTTP-POST     https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8
```

---

## 4. Config Changes (`configs/mcpgate.dev.yaml`)

The following sections were missing from the dev config and were added before running the server:

```yaml
auth:
  session_ttl: 8h
  session_sliding: true
  jwt:
    issuer: "mcpgate-dev"
    access_token_ttl: 15m
    signing_key_file: ""    # empty = ephemeral RSA key (dev only — tokens lost on restart)

saml:
  entity_id: "http://localhost:8080/auth/saml/metadata"
  acs_url: "http://localhost:8080/auth/saml/acs"
  metadata_url: "http://localhost:8080/auth/saml/metadata"
  attribute_mapping:
    email: "email"
    display_name: "displayName"
```

Without `auth.jwt.access_token_ttl`, the JWT manager uses a zero TTL and all issued tokens expire immediately.

The `saml.entity_id` value matches the `audience` configured in Auth0's SAML2 addon. These must match exactly — Auth0 puts this value in the SAML assertion's `<AudienceRestriction>` element.

---

## 5. Database Setup

### 5.1 Apply Migrations

The `migrate` CLI was not installed, so migrations were applied directly via psql:

```bash
# Find the running postgres container
docker ps --format "{{.Names}}\t{{.Image}}"
# → mcpgate-dev-postgres-1   postgres:16-alpine

# Apply the migration
docker exec -i mcpgate-dev-postgres-1 psql -U mcpgate -d mcpgate \
  < internal/store/postgres/migrations/001_initial.up.sql
```

Output confirmed all tables were created including `saml_idp_configs`.

To avoid this workaround in future:
```bash
brew install golang-migrate
make migrate-up
```

### 5.2 Insert the Auth0 IdP Config

The admin API (`POST /api/v1/admin/idps`) requires a JWT, creating a chicken-and-egg dependency before the first login. The row was inserted directly via psql instead:

```bash
CERT=$(cat certs/dev-0ziq1qjvgrwq4w2g.pem)
docker exec -i mcpgate-dev-postgres-1 psql -U mcpgate -d mcpgate <<SQL
INSERT INTO saml_idp_configs (
  id, name, entity_id, sso_url, certificate,
  attribute_map, group_role_map, is_enabled, created_at, updated_at
) VALUES (
  gen_random_uuid(),
  'auth0',
  'urn:dev-0ziq1qjvgrwq4w2g.us.auth0.com',
  'https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/samlp/k39kf4mjdKlScqCp6B1mtSgGpSmmROk8',
  \$cert\$${CERT}\$cert\$,
  '{"email": "email", "displayName": "display_name"}',
  '{}',
  true,
  now(), now()
);
SQL
```

Dollar-quoting (`$cert$...$cert$`) is used for the certificate to avoid single-quote escaping issues with the PEM content.

Verification:
```bash
docker exec -i mcpgate-dev-postgres-1 psql -U mcpgate -d mcpgate -c \
  "SELECT id, name, entity_id, sso_url, is_enabled FROM saml_idp_configs;"
```

```
                  id                  | name  |               entity_id               |              sso_url              | is_enabled
--------------------------------------+-------+---------------------------------------+-----------------------------------+------------
 799a60e9-31ae-4a3b-b9eb-c6172b4eb226 | auth0 | urn:dev-0ziq1qjvgrwq4w2g.us.auth0.com | https://...k39kf4mjdKlScqCp6B1... | t
```

---

## 6. Errors Encountered and Root Causes

### Error 1 — `404 page not found` on `/auth/saml/login`

**Symptom:** Browser returns 404. `curl -sv http://localhost:8080/auth/saml/login` confirms 404.

**Debug:**
```bash
lsof -i :8080 -sTCP:LISTEN
```
```
COMMAND   PID     USER   FD   TYPE  DEVICE  SIZE/OFF  NODE  NAME
main    28473 xorchain    3u  IPv6  ...           0t0   TCP  *:http-alt (LISTEN)
mcpgate 48365 xorchain   13u  IPv4  ...           0t0   TCP  localhost:http-alt (LISTEN)
```

**Root cause:** Two processes were listening on port 8080. PID 28473 was a stale `go run` session from a previous sprint (binary named `main`, bound to all interfaces including IPv6). PID 48365 was the current Sprint 2 server (bound to IPv4 localhost only). When curl resolved `localhost` to `::1` (IPv6), it hit the stale server which had none of the Sprint 2 routes.

**Fix:** `kill 28473`. The stale process was a leftover from a session that was not properly terminated. Always `Ctrl+C` to stop `make dev` rather than closing the terminal.

---

### Error 2 — Auth0 Page Blank / Spinning (Safari)

**Symptom:** After the mcpgate redirect (302 to Auth0), the browser hung on Auth0's SSO URL with a spinning indicator.

**Auth0 log events observed:**
```json
{ "type": "s", "description": "Successful login" }           // 02:47:22Z
{ "type": "f", "description": "session not found",
  "error": { "oauthError": "access_denied" } }               // 02:47:52Z
{ "type": "w", "description":
  "A user attempted to access login page directly..." }       // 02:47:59Z
```

**Root cause:** Safari's **Intelligent Tracking Prevention (ITP)** blocks cookies during cross-site navigation. Auth0's Universal Login sets a session cookie at `/samlp/...`, then redirects the browser to the Universal Login page at a slightly different URL. Safari treats this as a cross-site navigation and blocks the session cookie from being read back. Auth0 cannot correlate the login result with the original SAML request → `access_denied`.

**Fix:** Use **Chrome** for local dev. Safari works in production with an Auth0 Custom Domain (same-domain cookies are not blocked by ITP).

**Note:** The `type: "w"` "access login page directly" warning is resolved by setting the **Application Login URI** in Auth0 app settings to `http://localhost:8080/auth/saml/login`.

---

### Error 3 — Stale Auth0 Session After Server Restart

**Symptom:** After restarting `make dev`, the browser hit `/auth/saml/login` and was immediately redirected back (no login prompt). The ACS POST followed within 433ms, which is too fast for an interactive Auth0 login.

**Root cause:** Auth0 had an existing browser session from the previous successful authentication attempt. When the new SAML request arrived, Auth0 reused the session and immediately posted the SAML assertion back. This is expected SAML IdP behavior (SSO), not a bug.

**Implication:** For first-time testing after a server restart, clear Auth0 browser cookies or use an incognito/private window to force a fresh login. The fast 302→POST pattern indicates a working SAML session, not an error.

---

### Error 4 — `Authentication failed` (Hidden Error)

**Symptom:**
```
level=WARN msg="saml acs: invalid assertion" err="Authentication failed"
```

**Root cause:** `crewjam/saml`'s `ParseResponse` wraps all validation errors in `InvalidResponseError` and only surfaces the generic public message `"Authentication failed"`. The actual error is in the unexported `PrivateErr` field.

**Debug — improved error logging** in `internal/auth/saml/handlers.go`:
```go
var invalidErr *cssaml.InvalidResponseError
if errors.As(err, &invalidErr) && invalidErr.PrivateErr != nil {
    h.logger.Warn("saml acs: invalid assertion", "reason", invalidErr.PrivateErr)
} else {
    h.logger.Warn("saml acs: invalid assertion", "err", err)
}
```

With this change, subsequent attempts surfaced the real error (see Error 5).

---

### Error 5 — `invalid xml: no root`

**Symptom (after Error 4 fix):**
```
level=WARN msg="saml acs: invalid assertion" reason="invalid xml: no root"
```

**Debug — decode the SAMLRequest** to verify the ACS URL being sent:
```bash
python3 -c "
import base64, zlib, urllib.parse
req = '<SAMLRequest value from browser URL>'
decoded = urllib.parse.unquote(req)
xml = zlib.decompress(base64.b64decode(decoded), -15)
print(xml.decode())
"
```

Output confirmed the SAMLRequest was correctly formed:
```xml
<samlp:AuthnRequest
  AssertionConsumerServiceURL="http://localhost:8080/auth/saml/acs"
  ProtocolBinding="urn:oasis:names:tc:SAML:2.0:bindings:HTTP-POST">
  <saml:Issuer>http://localhost:8080/auth/saml/metadata</saml:Issuer>
  ...
</samlp:AuthnRequest>
```

Entity ID and certificate in the database were verified against Auth0's live metadata (see §3). Both matched exactly.

**Root cause — library design:** `crewjam/saml`'s `parseResponseHTTP` function accesses `req.PostForm.Get("SAMLResponse")` directly without calling `req.ParseForm()` first. In Go, `req.PostForm` is a nil `url.Values` map until `ParseForm()` is explicitly called. Calling `.Get()` on a nil `url.Values` returns `""` (empty string). The empty string base64-decodes to zero bytes, which parse as valid but root-less XML.

```go
// crewjam/saml v0.5.1 — service_provider.go:807
rawResponseBuf, err := base64.StdEncoding.DecodeString(req.PostForm.Get("SAMLResponse"))
//                                                      ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^
//                                                      Returns "" if ParseForm not called first
```

This is confirmed by the library's own test helpers, which call `req.ParseForm()` before passing the request to `ParseResponse`.

**Fix** in `internal/auth/saml/handlers.go` — add `r.ParseForm()` before `sp.ParseResponse()`:
```go
// ParseForm must be called before ParseResponse — crewjam/saml reads req.PostForm directly.
if err := r.ParseForm(); err != nil {
    http.Error(w, "bad request", http.StatusBadRequest)
    return
}

assertion, err := sp.ParseResponse(r, nil)
```

---

## 7. Code Changes Summary

| File | Change | Reason |
|------|--------|--------|
| `internal/auth/saml/handlers.go` | Unwrap `*cssaml.InvalidResponseError` to log `PrivateErr` | Surface real validation error instead of generic "Authentication failed" |
| `internal/auth/saml/handlers.go` | Call `r.ParseForm()` before `sp.ParseResponse(r, nil)` | crewjam/saml reads `req.PostForm` directly; nil map returns empty string |
| `configs/mcpgate.dev.yaml` | Added `auth` and `saml` sections | JWT manager used zero TTL (immediate expiry) without `access_token_ttl`; SAML SP marked unconfigured without `entity_id`/`acs_url` |
| `internal/auth/saml/handlers.go` | Embed `authReq.ID` in RelayState as `"<id>\|<redirect_to>"`; parse back in ACS and pass as `possibleRequestIDs` | crewjam/saml rejects SP-initiated assertions when `InResponseTo` does not match `possibleRequestIDs`; passing `nil` only works for IdP-initiated flows. Cookie-based tracking was ruled out due to `SameSite=Lax` blocking cross-site POST. |
| `internal/auth/saml/handlers.go` | Strip port from `r.RemoteAddr` using `net.SplitHostPort` before storing as `client_ip` | Postgres `INET` type rejects `"IP:port"` format; `r.RemoteAddr` always includes the port |

---

## 8. SAML Flow — End-to-End (After Fixes)

```
User (Chrome)                mcpgate                        Auth0
     │                           │                             │
     │  GET /auth/saml/login     │                             │
     │──────────────────────────▶│                             │
     │                           │  Build SAMLRequest          │
     │  302 → Auth0 SSO URL      │  (RS256-signed, ACS URL,    │
     │◀──────────────────────────│   entity ID, POST binding)  │
     │                           │                             │
     │  GET /samlp/<client_id>?SAMLRequest=...                 │
     │────────────────────────────────────────────────────────▶│
     │                                                         │
     │  HTML login form (or reuse existing session)            │
     │◀────────────────────────────────────────────────────────│
     │  [User logs in]                                         │
     │  POST /samlp/<client_id> (credentials)                  │
     │────────────────────────────────────────────────────────▶│
     │                                                         │
     │  HTML auto-submit form (SAMLResponse base64, RelayState)│
     │◀────────────────────────────────────────────────────────│
     │                           │                             │
     │  POST /auth/saml/acs      │                             │
     │  (SAMLResponse + RelayState)                            │
     │──────────────────────────▶│                             │
     │                           │  r.ParseForm()              │
     │                           │  sp.ParseResponse()         │
     │                           │  checkReplay (Redis NX)     │
     │                           │  Extract attributes         │
     │                           │  JIT provision user (DB)    │
     │                           │  Create session (DB+Redis)  │
     │                           │  Issue JWT                  │
     │                           │                             │
     │  Set-Cookie: mcpgate_session=<JWT>                      │
     │  302 → redirectTo (from RelayState)                     │
     │◀──────────────────────────│                             │
     │                           │                             │
     │  GET /  (or redirect_to)  │                             │
     │──────────────────────────▶│                             │
```

**Verified working** (2026-05-24): `POST /auth/saml/acs` → 302, `mcpgate_session` cookie set, protected MCP route returns 200 (JSON-RPC error for unknown server, not 401).

---

## 9. Pending / TODOs

| # | Item | Notes |
|---|------|-------|
| ~~P1~~ | ~~Complete flow test after `r.ParseForm()` fix~~ | **Done** — verified 2026-05-24 |
| ~~P2~~ | ~~Install `golang-migrate` CLI~~ | **Done** — `brew install golang-migrate` |
| P3 | Safari support | Requires Auth0 Custom Domain; not needed for dev |
| P4 | Script the IdP registration | A `make seed-idp` target or admin API call (once a bootstrap token mechanism exists) would replace the direct SQL insert |
| P5 | Auth0 M2M for service accounts | Sprint 3: implement JWT validation for Auth0-issued M2M tokens as an alternative auth path for non-browser clients |
