# Semgrep Findings — Remaining Review & Decisions

**Date:** 2026-07-05
**Branch:** `fix/semgrep-remaining-findings`
**Scanner:** Semgrep AppSec Platform (`prathiksaisb-2595-personal-org`)
**Repository:** `K3y5tr0ke/mcpgate` · ref `main` (`a08ac53`)
**Companion:** `docs/implementation/semgrep-sast-findings.md` (high-severity findings from the same scan)

---

## Scan Summary

| Category | Findings reviewed | Real | Suppressed |
|----------|------------------|------|------------|
| SAST medium | 14 | 2 fixed | 12 suppressed |
| Secrets | 0 | — | — |
| Supply chain (SCA) | 1 | 0 | 1 suppressed |

---

## SAST Findings

### Finding A — Open Redirect: `web/src/pages/login/LocalLoginPage.tsx:42`

| Field | Value |
|-------|-------|
| **Severity** | MEDIUM |
| **Confidence** | HIGH |
| **Rule** | `javascript.browser.security.open-redirect.js-open-redirect` |
| **Status** | **Fixed** |

**Vulnerability:**
```typescript
// BEFORE — redirect_to taken raw from the URL with no validation
const redirectTo =
  new URLSearchParams(window.location.search).get('redirect_to') ?? '/console/dashboard'
// ...
window.location.href = redirectTo   // ← direct open redirect
```

An attacker could craft `/console/login?redirect_to=https://evil.com` and, after a successful local login, the user's browser would follow the redirect to an external site — a classic open redirect usable for phishing.

**Fix:**
```typescript
const raw = new URLSearchParams(window.location.search).get('redirect_to') ?? ''
// Reject absolute URLs and protocol-relative paths (//evil.com) to prevent open redirect.
const redirectTo = raw.startsWith('/') && !raw.startsWith('//') ? raw : '/console/dashboard'
```

The validation requires the redirect target to start with `/` (same-origin relative path) and explicitly rejects the `//host` protocol-relative form which browsers treat as an absolute URL.

---

### Finding B — Open Redirect (double-slash bypass): `internal/auth/saml/handlers.go:170`

| Field | Value |
|-------|-------|
| **Severity** | MEDIUM |
| **Confidence** | HIGH (Semgrep flagged line 123; real gap at line 170) |
| **Rule** | `go.lang.security.injection.open-redirect.open-redirect` |
| **Status** | **Fixed** |

**Vulnerability:**

The SAML login handler embeds the `redirect_to` query parameter inside the `RelayState` field, which the IdP carries back to the ACS handler opaquely. The ACS handler extracted the destination from relayState with:

```go
// BEFORE — allows //evil.com (double-slash bypass)
if dest := rs[idx+1:]; strings.HasPrefix(dest, "/") {
    redirectTo = dest
}
```

`strings.HasPrefix("//evil.com", "/")` returns `true`, so a malicious `redirect_to=//evil.com` embedded in the relayState would survive the check and cause a post-authentication redirect to an external site.

**Fixes applied (two locations):**

1. **Login handler** — validate `redirect_to` before embedding in relayState, so the bad value never reaches the ACS:
```go
// Reject absolute URLs and protocol-relative paths to prevent open redirect via relayState.
if redirectTo == "" || !strings.HasPrefix(redirectTo, "/") || strings.HasPrefix(redirectTo, "//") {
    redirectTo = "/console/dashboard"
}
```

2. **ACS handler** — defence-in-depth: also guard the extraction site:
```go
if dest := rs[idx+1:]; strings.HasPrefix(dest, "/") && !strings.HasPrefix(dest, "//") {
    redirectTo = dest
}
```

**Note on the Semgrep finding location:** Semgrep flagged line 123 (`http.Redirect` to the IdP SSO endpoint). That specific redirect is a false positive — the destination comes from admin-configured IdP metadata, not user input. The actual gap was at line 170 (ACS handler). A `// nosemgrep` comment was added at line 123; the real fix is at lines 110–113 and 170.

---

### Finding C — XSS: direct `w.Write()` / `fmt.Fprintf()` to ResponseWriter (12 findings)

| Field | Value |
|-------|-------|
| **Severity** | MEDIUM |
| **Confidence** | LOW (all 12) |
| **Rules** | `go.lang.security.audit.xss.no-direct-write-to-responsewriter` · `go.lang.security.audit.xss.no-fprintf-to-responsewriter` |
| **Status** | **False positives — suppressed** |

**Why these are safe:**

The XSS rule targets code paths where user-controlled data is written to a `ResponseWriter` whose `Content-Type` is `text/html`. None of these call sites match that pattern:

| File | Lines | Content-Type | Data written |
|------|-------|-------------|-------------|
| `examples/mcp-servers/streamable-http/main.go` | 33, 39 | `application/json` | JSON-RPC (struct-serialised, no user HTML) |
| `examples/mcp-servers/sse/main.go` | 37, 46 | `text/event-stream` | SSE-framed JSON-RPC |
| `internal/auth/rbac/middleware.go` | 97 | `application/json` | Hardcoded `{"error":"<static-string>"}` |
| `internal/auth/saml/handlers.go` | 312, 313 | `application/samlmetadata+xml` | `xml.Header` constant + marshalled XML |
| `internal/proxy/handler.go` | 105, 136, 178, 377 | `application/json` / `text/event-stream` | JSON-RPC forwarded from upstream, JSON-RPC error |
| `internal/ratelimit/middleware.go` | 144 | `application/json` | Hardcoded rate-limit JSON structure |

None of these write HTML; none interpolate user-controlled strings into the response body as HTML. The `application/json` and `text/event-stream` content types also prevent browsers from treating the response as HTML even if the content were somehow exploitable.

**Decision:** Suppress all 12 with `// nosemgrep: <rule-id>` comments. No code changes made beyond the suppression comments.

---

## Supply Chain Finding

### Finding D — `golang.org/x/net` Uncontrolled Resource Consumption

| Field | Value |
|-------|-------|
| **Severity** | MEDIUM |
| **Confidence** | LOW |
| **Rule** | `ssc-parity-587e22a5b0de8ccfb3b54fd42c52234797c84552` |
| **File** | `go.mod:101` |
| **Status** | **False positive — govulncheck clean** |

**Current version:** `golang.org/x/net v0.53.0`

**Assessment:** `govulncheck ./...` (the authoritative Go vulnerability scanner, backed by [vuln.go.dev](https://vuln.go.dev)) returned **0 findings** against this codebase in Sprint 10 (after the toolchain was pinned to `go1.26.4` and `golang.org/x/net` was bumped to `v0.53.0`). The Semgrep SCA advisory database operates independently of the Go vulnerability database and can lag or diverge on advisory triage.

Since `govulncheck` — which also analyses reachability (whether the vulnerable code path is actually called by this module) — is clean, the Semgrep SCA finding reflects either a stale advisory or a vulnerability in an unreachable code path.

**Decision:** No dependency change. Mark as suppressed/ignored in the Semgrep platform. `govulncheck` will remain the primary gate for Go supply chain security (already wired into `make security-scan` and `make ci`).

---

## Changes Made

| File | Change type | Detail |
|------|-------------|--------|
| `web/src/pages/login/LocalLoginPage.tsx` | Fix | Validate `redirect_to` — same-origin only |
| `internal/auth/saml/handlers.go` | Fix + nosemgrep | Login handler: validate `redirect_to` before relayState; ACS handler: `!strings.HasPrefix(dest, "//")` guard; line 123: nosemgrep (false positive) |
| `examples/mcp-servers/streamable-http/main.go` | nosemgrep | 2 XSS false positives |
| `examples/mcp-servers/sse/main.go` | nosemgrep | 2 XSS false positives |
| `internal/auth/rbac/middleware.go` | nosemgrep | 1 XSS false positive |
| `internal/proxy/handler.go` | nosemgrep | 4 XSS false positives |
| `internal/ratelimit/middleware.go` | nosemgrep | 1 XSS false positive |

---

## Semgrep Scan Coverage — All Categories

| Scan type | Findings | Outcome |
|-----------|----------|---------|
| SAST — high severity | 2 | Resolved in PR #12 |
| SAST — medium severity | 14 | 2 fixed, 12 suppressed (this PR) |
| SAST — low severity | 0 | — |
| Secrets | 0 | Clean |
| Supply chain (SCA) | 1 | Suppressed — govulncheck authoritative and clean |

The Semgrep finding backlog for `K3y5tr0ke/mcpgate` is now fully triaged.
