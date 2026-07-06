# Semgrep SAST Findings — Review & Decisions

**Date:** 2026-07-05
**Branch:** `fix/semgrep-sast-findings`
**Scanner:** Semgrep AppSec Platform (`prathiksaisb-2595-personal-org`)
**Repository:** `K3y5tr0ke/mcpgate` · ref `main` (`a08ac53`)
**Scan type:** SAST (code security)
**Findings reviewed:** 2 (all open high-severity findings)

---

## Finding 1 — Insecure gRPC connection (missing TLS for non-insecure path)

| Field | Value |
|-------|-------|
| **Severity** | HIGH |
| **Confidence** | HIGH |
| **Rule** | `go.grpc.tls.grpc-client-new-insecure-connection` |
| **File** | `internal/tracing/provider.go:48` |
| **Status** | **Fixed** |

### Description

The OTLP tracing provider builds its gRPC connection to the collector with an `insecure` branch correctly gated on `cfg.Insecure`, but the `else` path was missing. When `cfg.Insecure` is `false` (the production default), no transport credentials were added to `opts`, leaving `grpc.NewClient` without any explicit TLS configuration.

**Before:**
```go
opts := []grpc.DialOption{}
if cfg.Insecure {
    opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
conn, err := grpc.NewClient(cfg.Endpoint, opts...)
```

The gap: `cfg.Insecure == false` → `opts` remains empty → OTLP connection has no transport security.

### Fix Applied

Added an explicit TLS branch with a TLS 1.2 floor for the production path:

```go
opts := []grpc.DialOption{}
if cfg.Insecure {
    opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
} else {
    opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
        MinVersion: tls.VersionTLS12,
    })))
}
```

New imports: `"crypto/tls"`, `"google.golang.org/grpc/credentials"`.

### Decision

**Fix and ship.** The finding is real — any deployment with `otlp.insecure: false` (production) would have had an unprotected gRPC channel to the OTLP collector. Dev config sets `insecure: true` intentionally (local Jaeger/OTel Collector), so the dev path is unchanged. The TLS 1.2 floor aligns with the floor already enforced on the mcpgate server itself (`internal/server/server.go`).

---

## Finding 2 — Dynamic exec.Command (admin-controlled stdio subprocess)

| Field | Value |
|-------|-------|
| **Severity** | HIGH |
| **Confidence** | LOW |
| **Rule** | `go.lang.security.audit.dangerous-exec-command.dangerous-exec-command` |
| **File** | `internal/proxy/transport/stdio.go:146` |
| **Status** | **False positive — suppressed** |

### Description

The Semgrep rule flagged `exec.Command(t.command, t.args...)` as a potential code injection risk because `t.command` and `t.args` are not compile-time constants.

### Why this is safe

`t.command` and `t.args` are populated from `MCPServer.Config.Command` / `Config.Args`, which are stored in Postgres and written **exclusively by authenticated admin users** via `POST /api/v1/admin/servers` (protected by `RequireRole("admin")` middleware). There is no code path from end-user MCP requests to this exec call.

The existing `//nolint:gosec` annotation already suppresses the gosec `G204` rule for the same reason. Semgrep's rule fires independently because it uses a different analysis engine.

The scanner pre-dates the Semgrep integration (Sprint 1 implementation, gosec nolint added in Sprint 10); the low confidence score (LOW) reflects the tool's own uncertainty.

### Suppression Applied

A Semgrep-specific nosemgrep comment was added on the line preceding the exec call:

```go
// nosemgrep: go.lang.security.audit.dangerous-exec-command.dangerous-exec-command
cmd := exec.Command(t.command, t.args...) //nolint:gosec
```

### Decision

**Suppress as false positive.** The exec call is intentional and safe within the admin-only server registration flow. The correct mitigation is admin RBAC enforcement (already in place), not removing the exec call — stdio transport fundamentally requires spawning a subprocess. If the trust boundary for server registration ever changes (e.g., delegated admin), this decision should be revisited.

---

## Findings Not Present (confirmed clean)

The following finding categories were checked and returned no results:

| Category | Result |
|----------|--------|
| Critical SAST | None |
| High SAST (beyond the two above) | None |

Medium and low-severity findings were not reviewed in this pass; they can be addressed in a follow-up triage.

---

## Remaining Semgrep Scans

| Scan type | Status |
|-----------|--------|
| SAST (code security) | ✅ Reviewed — this document |
| Secrets detection | Not yet reviewed |
| Supply chain (SCA) | Not yet reviewed |

---

## Pre-existing Diagnostic (not a Semgrep finding)

During the fix, the Go LSP flagged a pre-existing warning in `internal/proxy/transport/stdio.go`:

> `bufio.Scanner "scanner" is used in Scan loop without final check of scanner.Err()`

This was present before this branch and is unrelated to the Semgrep findings. It is tracked separately for the next triage pass.
