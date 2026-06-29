# OWASP Top 10 (2021) — mcpgate Mitigation Checklist

Sprint 10 task **10.1**. Each OWASP category is mapped to mcpgate's concrete mitigation and the evidence in the codebase. Status legend: **✅ Implemented**, **🟡 Partial / verification pending this sprint**, **⏭ Deferred** (with target sprint).

> The verification suites that *exercise* these mitigations are now implemented (10.4 SQLi, 10.5 XSS, 10.6 JWT, 10.7 SAML, 10.8 privesc — across the Sprint 10 PRs); **10.3 (OWASP ZAP scan)** is the only one still outstanding. This document is the static checklist; the suites are the dynamic proof.

---

## A01:2021 — Broken Access Control — ✅
**Mitigation:** Deny-by-default RBAC on every proxied request; admin API gated by an explicit role check.
- `/mcp/{serverName}/*` passes through `rbac.Authorize` (Casbin `(role, server, action)` tuples) — `internal/server/server.go` `buildRouter()`; `internal/auth/rbac/`.
- `/api/v1/admin/*` requires `rbac.RequireRole("admin")` (`server.go`).
- No default-allow: a request with no matching policy is denied (403) and audited as `denied` (`tests/e2e/logging_test.go` `TestE2E_AuditLog_RBACDenied_*`).
- **Verified by:** 10.8 privilege-escalation suite — `tests/e2e/rbac_test.go` `TestE2E_RBAC_AdminAPI_NonAdmin_CannotMutate` (+ the RBAC e2e matrix: non-admin/wrong-server/readonly/suspended) — and S4-T1 RBAC unit tests (`internal/auth/rbac/{enforcer,middleware}_test.go`).

## A02:2021 — Cryptographic Failures — ✅
**Mitigation:** Strong, modern algorithms throughout; no weak/none algorithms accepted.
- **JWT RS256** issuance and validation; HS256/`none` rejected — `internal/auth/jwt/manager.go`, `internal/auth/jwks/validator.go` (explicit `SigningMethodRSA` type check).
- **Local admin password** stored with **bcrypt** — `internal/auth/local/handlers.go`.
- **SAML** assertion signature verification — `internal/auth/saml/`.
- **Short-lived access tokens** (default `15m`), refresh `7d`, session `1h` — `internal/config/config.go`.
- **TLS** termination expected at ingress/in front; HSTS now emitted (`internal/server/security_headers.go`).
- **Verified by:** 10.6 JWT security tests — `internal/auth/jwt/manager_security_test.go` (none/HS256 algorithm-confusion, role-tamper) plus the existing tampered/expired/wrong-issuer tests.

## A03:2021 — Injection — ✅
**Mitigation:** Parameterized SQL everywhere; admin input allowlisting; MCP payloads forwarded verbatim (never interpolated/evaluated).
- All `pgx` queries use positional params (`$1, $2, …`) — e.g. `internal/store/postgres/audit_logs.go`; no string-built SQL.
- Settings `PUT` validates keys against a hardcoded allowlist — `internal/admin/api/settings.go`.
- The proxy forwards raw JSON-RPC bytes; it does not parse-and-rebuild upstream commands.
- **Verified by:** 10.4 SQL-injection suite (`tests/e2e/sqli_test.go`) and 10.5 stored-XSS test (`web/tests/e2e/xss.spec.ts`, Sprint 10B). **Remaining:** 10.3 OWASP ZAP scan.

## A04:2021 — Insecure Design — 🟡
**Mitigation:** Layered middleware chain with a single enforcement order; security-sensitive changes flagged for second review (per `CLAUDE.md` / `README.md`).
- Fixed chain `auth → logging → metrics → rate limit → RBAC → proxy` (`server.go`) — no path bypasses auth/RBAC.
- **This sprint:** 10.1 (this doc) + a documented pre-launch security review checklist (§7.2 of the PRD).

## A05:2021 — Security Misconfiguration — 🟡
**Mitigation:** Secure-by-default headers; secrets externalized; debug surfaces minimal.
- **Security headers** (CSP, `X-Frame-Options: DENY`, nosniff, Referrer-Policy, HSTS) on console + admin — `internal/server/security_headers.go` (Sprint 10 task 10.2, ✅).
- Dev secrets (`devpassword`) are dev-only; prod must supply via env/secret store (`internal/config`, `MCPGATE_` overrides).
- **⏭ Deferred to Sprint 11:** hardened container (distroless, non-root UID 65534, read-only FS, no shell) — task 10.15 moved to packaging.

## A06:2021 — Vulnerable and Outdated Components — ✅
**Mitigation:** Automated dependency scanning in the local CI gate; clean baseline.
- `make security-scan` runs **govulncheck** *and* **npm audit** (`--audit-level=high`) — both ecosystems gated in `make ci` (npm-audit gating added in the security-review follow-ups). Currently **0 vulnerabilities** (PRs #2/#3 cleared Go stdlib + x/net + goxmldsig + vite + undici; toolchain on go1.26.4).
- golangci-lint pinned to v2 via a Makefile guard.
- **Remaining:** 10.14 Trivy image scan (gated on the Sprint 11 production image).

## A07:2021 — Identification and Authentication Failures — ✅
**Mitigation:** Replay protection, brute-force throttling, short sessions, fresh post-auth session IDs.
- **SAML replay detection** via Redis `SET NX` on assertion ID — `internal/auth/saml/handlers.go`, `sp.go`.
- **Brute-force throttling** (Redis INCR per IP, default window `15m`) on SAML + local login — `internal/config/config.go`, `internal/ratelimit/`.
- **Short access-token TTL** (`15m`) limits stolen-token window — `config.go`.
- **Session fixation prevented:** session created only *after* successful auth (no pre-auth session).
- **Verified by:** 10.7 SAML security tests — `tests/e2e/saml_test.go` `TestE2E_SAML_TamperedAssertion_Rejected` (signature) plus existing replay-detection and suspended-user coverage. (Audience/time-conditions are enforced by crewjam/saml's `ParseResponse`.)

## A08:2021 — Software and Data Integrity Failures — ✅
**Mitigation:** Tamper-evident audit log; verifiable without external dependency.
- Audit entries form a **SHA-256 hash chain** — `internal/logging/hashchain.go`; seeded from the latest DB hash on startup.
- Verifiable via `GET /api/v1/admin/logs/verify` — covered by `tests/e2e/logging_test.go`.
- API serialization shape guarded against regression — `TestE2E_AuditLog_AdminAPI_EntryJSONIsSnakeCase`.
- **⏭ Sprint 11:** signed images + SBOM (Cosign/Syft) with production packaging.

## A09:2021 — Security Logging and Monitoring Failures — ✅
**Mitigation:** Comprehensive structured audit logging with a no-silent-drop guarantee and health surfacing.
- Async audit `Pipeline` with a **WAL fallback sink** when Postgres is down + crash-recovery replay — `internal/logging/pipeline.go`, `internal/logging/sinks/wal.go`.
- Dropped-count exposed as Prometheus gauge `mcpgate_audit_pipeline_dropped_total` and degrades `/ready` — `internal/metrics/collector.go` `RegisterPipelineGauge`.
- Per-request + per-server metrics timeseries — `internal/metrics/`.
- **Verified by:** 10.10 k6 log-pipeline stress script (`k6/log_pipeline.js`, Sprint 10B) — run against a live stack to assert 100% capture (no silent drops) under load.

## A10:2021 — Server-Side Request Forgery (SSRF) — ✅
**Mitigation:** Upstream URLs come only from admin-configured records; no user-controlled URL construction.
- Proxy targets resolved from the `mcp_servers` table (`internal/store`, `internal/proxy` `ServerStore`); clients address servers by **name**, never URL.
- The JWKS rotate fetch hits the same admin-configured `jwks_url` the validator already uses — justified `//nolint:gosec` in `internal/admin/api/jwks.go`.
- **Resolved:** outbound JWKS fetches (both the validator client and the rotate endpoint) now refuse to follow redirects (`CheckRedirect → ErrUseLastResponse`), so an issuer URL can't 302-pivot to an internal address — security-review follow-ups.

---

## Summary

| Category | Status |
|----------|--------|
| A01 Broken Access Control | ✅ |
| A02 Cryptographic Failures | ✅ |
| A03 Injection | ✅ |
| A04 Insecure Design | 🟡 (review checklist this sprint) |
| A05 Security Misconfiguration | 🟡 (headers ✅; container hardening → S11) |
| A06 Vulnerable Components | ✅ |
| A07 Auth Failures | ✅ |
| A08 Data Integrity | ✅ (signing/SBOM → S11) |
| A09 Logging/Monitoring | ✅ |
| A10 SSRF | ✅ (redirect-pinning added) |

All ten categories have a primary mitigation in place, and the Sprint 10 verification suites (10.4/10.5/10.6/10.7/10.8) are implemented. Remaining items: (a) **10.3 OWASP ZAP** and **10.14 Trivy** image scan (the latter gated on the Sprint 11 image), and (b) items deliberately deferred to Sprint 11 packaging — container hardening (10.15) and image signing/SBOM.
