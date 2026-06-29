# Sprint 10 Plan — Hardening, Security & Carry-over Burndown

**Sprint:** 10 (Hardening and Security)
**Goal:** Production security hardening, performance validation, and chaos/resilience testing — plus a deliberate burndown of open carry-over TODOs from Sprints 0–9 that fit this theme.
**Sources:** `docs/mcp-proxy-gateway-prd.md` (§1 goals, §6.4 logging, §7 security, §13.6 perf), `docs/mcpgate-development-plan.md` (Sprint 10 = tasks 10.1–10.15), open carry-overs from Sprints 0–8, and Sprint 9 deferrals (`stage-9-13-06-2026.md`, S9-D1–D8).

> **Scope decisions locked during planning:**
> 1. Multi-IdP SAML dispatch + SAML SLO (S2/S3 carry-overs) are **deferred to Sprint 12** — they are auth-completeness features, not hardening.
> 2. Docker image hardening (10.15) **moves to Sprint 11** (production packaging) since no production Dockerfile exists yet; Sprint 10 keeps only the Trivy scan, gated on an image being available.
> 3. This plan is the committed `stage-10-plan.md`.

---

## 0. Progress (updated 2026-06-28)

The original draft PR #7 was split into two no-overlap PRs: **#8** (`sprint-10-backend-hardening` — security headers, auth/RBAC suites, WS heartbeat, chaos) and **#9** (`sprint-10-tooling-e2e` — k6 load, Playwright-in-CI, axe-core, XSS). A security-review follow-up (**#10**) adds SSRF/body-limit/RBAC-fail-closed/TLS hardening.

| Task | Status | Commit / note |
|------|--------|---------------|
| 10.1 OWASP checklist | ✅ done | `docs/security-owasp.md` |
| 10.2 Security headers | ✅ done | `securityHeaders` middleware + unit/e2e tests |
| 10.4 SQL-injection suite | ✅ done | `tests/e2e/sqli_test.go` |
| 10.6 JWT security | ✅ done | alg-confusion (`none`/HS256) + role-tamper |
| 10.7 SAML security | ✅ done | tampered-assertion (signature) rejection |
| 10.8 Privilege escalation | ✅ done | non-admin cannot read/mutate admin API |
| 10.9 / 10.10 k6 load | ✅ scripts authored | `k6/`, `make load-test[-pipeline]` (manual; needs k6 + stack) |
| 10.13 upstream chaos | ✅ done | breaker open **and** recovery cycle |
| 10.11 / 10.12 PG/Redis chaos | ✅ logic unit-covered | WAL fallback + rate-limit fail-open; container-kill e2e deferred |
| 10.14 dependency scan | ✅ govulncheck + npm audit | Trivy image scan → Sprint 11 |
| S4-T1 RBAC unit tests | ✅ done | Casbin model + `RequireRole` |
| S9-D3 WS heartbeat | ✅ done | session re-validation on the log stream |
| **Lint gate hardening** | ✅ done | `.golangci.yml` now lints `-tags=integration` |
| 10.5 XSS | ✅ done | Playwright `web/tests/e2e/xss.spec.ts` (stored payload rendered inert) |
| S9-D7 Playwright-in-CI | ✅ done | `scripts/run-playwright.sh` + `make test-e2e` (self-provisioning) |
| S9-D5 axe-core | ✅ done | `web/tests/e2e/a11y.spec.ts` (serious/critical WCAG 2 A/AA gate) |
| 10.3 ZAP scan | ⏳ todo | external scanner |
| 10.15 Docker hardening | ⏭ Sprint 11 | no prod Dockerfile yet |

**Substantive code/test work for the sprint is complete.** The only remaining items are external-tooling integration: **10.3 OWASP ZAP** and **10.14 Trivy** image scan (the latter gated on the Sprint 11 production image).

---

## 1. Reconciliation against current code

The carry-over harvest surfaced several items already resolved in later sprints. Corrected against the current tree:

- **S5-T6 (pipeline dropped-count → Prometheus): RESOLVED** — `collector.go` `RegisterPipelineGauge` exposes `mcpgate_audit_pipeline_dropped_total`.
- **S4-T2 (server-scoped RBAC): appears RESOLVED** — Casbin checks `(role, server, action)` tuples (per `CLAUDE.md`). *Verify before closing.*
- **Security headers: CONFIRMED ABSENT** — no CSP/X-Frame-Options/HSTS middleware in `internal/server/`. Real gap → task 10.2.
- **Production Dockerfile: ABSENT** under `deployments/` — `make build-docker` references a file that does not exist. 10.15 is greenfield → Sprint 11.
- **10.14 dependency scan: DONE** this session — `govulncheck` (0 vulns, go1.26.4 toolchain bump) and `npm audit` (0 vulns, vite/undici bumps) are clean and merged. Only the Trivy image scan remains.

---

## 2. Track 1 — Original Sprint 10 (Hardening & Security)

### 2.1 Security

| # | Task | Current state | Action / acceptance |
|---|------|---------------|---------------------|
| 10.2 | **Security headers middleware** | ❌ absent | chi middleware adding CSP, `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, HSTS, `Referrer-Policy` to console + admin responses. Highest-value gap. |
| 10.1 | OWASP Top 10 walkthrough | undocumented | `docs/security-owasp.md` mapping A01–A10 → mcpgate evidence (PRD §7.1). All items pass. |
| 10.3 | Input sanitization audit | parameterized SQL | OWASP ZAP scan of `/api/v1/admin/*`; fix findings. |
| 10.4 | SQL-injection test suite | parameterized queries | Injection-payload tests on all text inputs; assert no string concatenation in SQL. |
| 10.5 | XSS prevention suite | depends on 10.2 | XSS payloads in console inputs; CSP blocks inline; no reflected/stored XSS. |
| 10.6 | JWT security tests | alg guard exists | Reject: tampered, expired, wrong-iss, modified-roles, `none`-alg. |
| 10.7 | SAML security tests | replay detection exists | Reject: replayed, wrong-audience, expired, bad-signature assertions. |
| 10.8 | Privilege-escalation tests | RBAC + RequireRole | Deny: viewer→admin-API, developer→policies, suspended user, revoked M2M. |

### 2.2 Performance (PRD targets: **p99 ≤ 5ms passthrough overhead**, error rate < 1%)

| # | Task | Action / acceptance |
|---|------|---------------------|
| 10.9 | k6 load: proxy throughput | Ramp 100→1000 RPS / 10 min; assert p95 < 50ms, p99 < 100ms (PRD §1 stricter: p99 ≤ 5ms overhead), error < 1%; profile with pprof; fix bottlenecks. |
| 10.10 | k6 load: log pipeline under stress | 1000 RPS × 10 min ≈ 600k entries; assert **100% capture** (PRD §6.4.4 "no silent drops"), hash chain intact, measure pipeline lag. |

### 2.3 Chaos / resilience (infra exists; automate end-to-end)

| # | Task | Existing infra | Action / acceptance |
|---|------|----------------|---------------------|
| 10.11 | Chaos: Postgres failure | WAL sink, `/ready` | Kill PG → proxy serves (Redis sessions), logs → WAL, WAL replays on recovery with **no duplicates** (`ON CONFLICT`). |
| 10.12 | Chaos: Redis failure | fail-open limiter | Kill Redis → auth falls back to PG, RBAC falls back to uncached Casbin, rate limiter fail-open; no crash. |
| 10.13 | Chaos: upstream failure | circuit breaker + tests | Kill upstream → breaker opens → fast MCP error → console shows unhealthy → breaker closes on recovery. |

### 2.4 Supply chain

| # | Task | State | Action |
|---|------|-------|--------|
| 10.14 | Dependency scan | ✅ govulncheck + npm audit clean | Remaining: **Trivy** scan — gated on a built image (→ produced in Sprint 11). |
| 10.15 | Docker image hardening | ❌ no prod Dockerfile | **Moved to Sprint 11** (packaging): distroless, non-root (65534), read-only FS, no shell, < 50MB. |

---

## 3. Track 2 — Carry-over TODOs folded into Sprint 10 (hardening-aligned)

| Source | Item | Rationale |
|--------|------|-----------|
| S4-T1 | **RBAC unit tests** (`enforcer.go`, `middleware.go`) | Security-critical, only E2E coverage today; pairs with 10.8. |
| S1-T1 | Transport cache not invalidated on `PUT /servers/{id}` | Stale upstream config = correctness/SSRF-adjacent risk. |
| S0-T5 | `audit_logs` DEFAULT partition | Pre-launch must-do; tamper-evident store needs real monthly partitions (pg_partman or scheduled). |
| S3-T2 / S4-T4 | M2M identity is `uuid.Nil` | Blocks per-client rate limits and clean audit identity for machine accounts. |
| S4-T3 | `/api/v1/admin/policies` has no pagination | Unbounded response in large deployments. |
| S6-T6 | `PoolMetrics` not wired to Prometheus | Observability gap; originally tagged "Sprint 10". |
| S1-T5 | Admin-API handler unit tests | Coverage for the surface 10.3 audits. |

---

## 4. Track 3 — Sprint 9 → Sprint 10 carry-overs (console / observability)

| ID | Item | Notes |
|----|------|-------|
| S9-D3 | **WebSocket session-revalidation heartbeat** | The `t.Skip`-guarded `tests/e2e/websocket_test.go` is already written and waiting on this implementation. |
| S9-D7 | **Run Playwright in CI** | Specs + config exist; needs a CI job that provisions the stack (docker-compose → migrate → seed → serve → health-wait → `make test-e2e-playwright`). Unblocks S9-D5. |
| S9-D5 | Accessibility audit (axe-core) | Was blocked on Playwright stability → do after S9-D7. |
| S9-D1 | Per-user metrics drilldown | Needs a per-user aggregation endpoint + Redis key scheme (analogous to the per-server `metrics:ts:srv:*` keys). |

---

## 5. Explicitly deferred beyond Sprint 10

| Item | Target | Reason |
|------|--------|--------|
| OpenAPI generation (S9-D4) + API reference | Sprint 11 | Documentation/packaging phase. |
| Production Dockerfile + Helm/K8s (incl. 10.15) | Sprint 11 | Packaging phase. |
| Error-rate & p95-latency timeseries charts (S9-D6) | future metrics work | Summary endpoint has no error/latency time series yet. |
| `?bucket=` client override (S9-D8) | future | Endpoint is structured for it; needs UI + points-cap clamp. |
| SAML wizard "Test Login" (S9-D2) | future | Needs partial-save state. |
| **Multi-IdP SAML dispatch** (S2-T2/S3-T3) + **SAML SLO** (S2-T3/S3-T4) | **Sprint 12** | Auth-completeness, not hardening (locked decision). |
| Multi-tenancy / webhooks / quota | Sprint 12–13 | Per dev-plan roadmap. |

---

## 6. Already done — reduces Sprint 10 scope

- ✅ **10.14 (partial):** `govulncheck` + `npm audit` both clean (PRs #2, #3 merged); toolchain on `go1.26.4`.
- ✅ Regression tests landed (PR #5): audit-log JSON snake-case shape + per-server metrics isolation — supports the 10.4–10.8 test work and the resilience assertions.

---

## 7. Proposed sequencing (2 weeks)

**Week 1 — Security:**
10.2 headers → 10.1 OWASP doc → 10.6/10.7/10.8 auth + privesc tests + S4-T1 RBAC unit tests → 10.4/10.5 injection/XSS → 10.3 ZAP scan.

**Week 2 — Resilience & performance:**
10.11/10.12/10.13 chaos automation → 10.9/10.10 k6 load (validate p99 ≤ 5ms, 100% log capture) → S9-D3 WS heartbeat → S9-D7 Playwright-in-CI → S9-D5 axe-core.

**Continuous (fold in as related code is touched):** S1-T1 cache invalidation, S0-T5 partitioning, S6-T6 pool metrics, S4-T3 policy pagination, S3-T2/S4-T4 M2M identity, S1-T5 handler tests.

---

## 8. Sprint 10 deliverable

All security tests pass; OWASP checklist documented with evidence; security headers enforced; performance targets met (p99 ≤ 5ms passthrough, error < 1%); 100% audit-log capture under stress; graceful degradation verified for Postgres, Redis, and upstream failures via automated chaos tests; Playwright E2E running in CI. Carry-over hardening TODOs (RBAC tests, cache invalidation, partitioning, pool metrics, policy pagination, M2M identity) closed.

---

## 9. Post-sprint security review — outcomes & decisions

A 4-dimension security audit (auth/sessions, authz/identity, crypto/secrets, network/input) was run after the main sprint work. Posture was strong; findings were edge/transport hardening. Verified findings fixed in the **security-followups PR**: SSRF redirect-pinning (JWKS validator + rotate), admin request-body limits (1 MiB), M2M rate-limit isolation (per-identity key, not shared `uuid.Nil`), RBAC fail-closed on enforcer error, `ReadHeaderTimeout`, and **npm audit added to `make ci` security-scan** (both ecosystems now gated).

### Decisions

| Item | Decision |
|------|----------|
| **Trusted-proxy-aware client IP** (audit `ClientIP` trusts `X-Forwarded-For`; brute-force keys on `RemoteAddr`) | **Deferred to the load balancer.** When deployed behind an LB/ingress, the LB sets a trusted `X-Forwarded-For`. mcpgate will only add trusted-proxy CIDR parsing if a deployment runs *without* an LB. Re-open if that materializes. |
| **TLS** (config was defined but unused) | **Resolved.** Direct TLS serving is now wired (`server.tls.enabled` → `ServeTLS`, TLS 1.2 floor); dev certs via `scripts/generate-certs.sh` → `certs/` (gitignored). Production typically still terminates TLS at the LB/ingress; in-process TLS is now available when needed. |

### Low-priority backlog (pick up when convenient)

| Item | Location | Note |
|------|----------|------|
| JWT signing-key file-permission check + "prod requires `signing_key_file`" guard | `internal/auth/jwt/manager.go` | reject world-readable key; fail if ephemeral key in prod |
| Session JWT missing `aud` claim | `internal/auth/jwt/manager.go` | add `Audience` + validate, like M2M tokens |
| WS `CheckOrigin` always-true + no message-size cap | `internal/admin/api/logs.go` | defense-in-depth (SameSite=Lax already blocks cross-origin cookie) |
| `Permissions-Policy` header not set | `internal/server/security_headers.go` | add to the security-headers middleware |
| Unused `AdminConfig.CORSOrigin` | `internal/config/config.go` | implement CORS or remove the dead field |
| DB password in DSN string | `internal/config/config.go` | ensure DSN is never logged on pgx errors |
| `generatePassword` modulo bias | `cmd/mcpgate/main.go` | negligible (62 chars); use rejection sampling if ever tightened |

### Confirmed non-issues (audit false positives)
- "No security headers" — already shipped (task 10.2).
- "Secrets in `.env` repo history" — `.env` is gitignored and was never committed (verified).
