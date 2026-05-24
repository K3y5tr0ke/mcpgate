# Local CI Setup & Lint Cleanup

**Date:** 2026-05-23  
**Scope:** Replace GitHub Actions with a local `make ci-fast` gate; resolve all `golangci-lint` issues across the Sprint 1 codebase.

---

## Background

The project's original `.github/workflows/ci.yml` ran on GitHub-hosted runners. Because the repository is private and the team does not want to pay for runner minutes, GitHub Actions was removed entirely and replaced with a local CI workflow that runs in the developer's terminal before every push.

---

## Local CI: What Was Added

### `make ci-fast`

A lightweight pre-push gate that runs in seconds with no external services:

```
lint → unit tests (−short) → build
```

Defined in `Makefile`:

```makefile
ci-fast: lint test-unit build
```

- **`lint`** — runs `golangci-lint run ./...`
- **`test-unit`** — `go test -race -count=1 -short ./internal/...` (skips tests tagged `integration`)
- **`build`** — `CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mcpgate ./cmd/mcpgate`

### `make ci`

The full integration suite (requires Docker):

```
lint → unit tests → docker up → migrate → integration tests → build → security scan
```

Run manually before merging a branch. Not automated.

### `make install-hooks`

Installs `scripts/pre-push.hook` as `.git/hooks/pre-push`. The hook runs `make ci-fast` automatically on every `git push`. If it fails the push is aborted.

### `.github/workflows/ci.yml`

Deleted. No GitHub Actions pipeline remains. Future pushes to `main` trigger nothing remotely.

---

## Lint Configuration: `.golangci.yml`

### Linters enabled

| Linter | Purpose |
|---|---|
| `govet` | Static analysis (all checks enabled) |
| `errcheck` | Unhandled error returns |
| `staticcheck` | SA-family checks (deprecations, misuse) |
| `ineffassign` | Assignments whose value is never used |
| `unused` | Exported/unexported symbols with no callers |
| `gosec` | Security-focused checks |
| `gocritic` | Diagnostic, style, and performance checks |
| `exhaustive` | Missing cases in switch on enum types |
| `misspell` | Spelling in comments and strings |
| `unconvert` | Unnecessary type conversions |
| `unparam` | Parameters always passed the same value |
| `bodyclose` | Unclosed HTTP response bodies |

`revive` was considered but rejected — it produces 60+ doc-comment noise issues for application code where comments are intentionally minimal.

### Formatters enabled

`gofmt` and `goimports` (local prefix: `github.com/K3y5tr0ke/mcpgate`).

---

## Lint Issues Fixed

### 1. `chimw.RealIP` removed from `internal/server/server.go`

**Issue:** `SA1019: chimw.RealIP is deprecated` (staticcheck)

**Why it was there:** `chimw.RealIP` was added in Sprint 0 as a convenience middleware to populate `r.RemoteAddr` from `X-Forwarded-For` / `X-Real-IP` headers.

**Why it was removed:** The deprecation is a security issue, not just API churn. `RealIP` unconditionally trusts and overwrites `r.RemoteAddr` with whatever the client sends in those headers — allowing any client to spoof its apparent IP address (CVE tracked under GHSA-3fxj-6jh8-hvhx and related advisories). Removing it is the correct fix; the field can be re-introduced in Sprint 3/4 behind a configurable trusted-proxy CIDR list. No exclusion rule was added — the root cause was fixed.

---

### 2. Intentionally-ignored error returns — `_ =` assignment idiom

**Issue:** `errcheck` and `gosec G104` both flag unhandled error return values.

**Affected calls:**

| File | Call | Reason error is intentionally ignored |
|---|---|---|
| `internal/proxy/handler.go` | `w.Write(resp)` (×2) | HTTP response write — if it fails the client has disconnected; no recovery is possible |
| `internal/proxy/handler.go` | `fmt.Fprintf(w, ...)` | Same: SSE stream write into disconnected client |
| `internal/proxy/handler.go` | `w.Write(ErrorResponse(...))` | Same: error-response write |
| `internal/proxy/handler.go` | `tr.Close()` | Transport evicted by a race on first creation; the loser is discarded |
| `internal/admin/api/router.go` | `json.NewEncoder(w).Encode(v)` | HTTP response encode write |
| `internal/testutil/mockserver.go` | `json.NewEncoder(w).Encode(resp)` (×2) | Test mock response writes |
| `internal/proxy/transport/stdio.go` | `t.stdin.Close()` | Stdin close during process shutdown; error is non-actionable |
| `internal/store/redis/client.go` | `client.Close()` | Redis client close during failed-ping teardown |

**Fix:** Changed each call from bare `call()` (or `call() //nolint:errcheck`) to the `_ = call()` / `_, _ = call()` idiom. This is explicit in the source — a future reader sees the intent without needing to know what lint rules are in effect.

**Why not `//nolint:errcheck` comments?**

`//nolint:errcheck` suppresses `errcheck` but `gosec G104` (which checks the same thing) fires separately. Config-level suppression via `gosec.excludes: [G104]` and `issues.exclude` were both attempted and neither took effect reliably in golangci-lint v2. The `_ =` assignment idiom satisfies both linters at the language level — no config workaround needed.

**Deferred closes** (`defer resp.Body.Close()` in `sse.go` and `streamable.go`) keep their `//nolint:errcheck` comments because:
- `gosec G104` does not flag deferred calls (only direct calls)
- The `_ =` idiom requires a closure wrapper with defers (`defer func() { _ = f.Close() }()`), which is more verbose and less clear than a single-line nolint

---

### 3. `gosec G705` (XSS taint analysis) — `internal/proxy/handler.go`

**Issue:** gosec traces `resp` (bytes read from an upstream HTTP response) to `w.Write(resp)` and flags it as a potential XSS sink.

**Why the finding is a false positive here:** The proxy writes JSON-RPC response payloads, not HTML. The upstream servers are registered by an administrator in the database — they are not user-controlled. There is no HTML rendering path; `Content-Type: application/json` is set before every write.

**Fix:** `//nolint:gosec` added to the two affected `w.Write(resp)` lines with a comment explaining the justification:

```go
_, _ = w.Write(resp) //nolint:gosec // G705: proxy forwards JSON-RPC from admin-configured servers, not user input
```

This is the only place where a lint suppression comment rather than a code change was the right answer — the finding is categorically wrong for this call site and there is no language-level idiom that removes the taint.

---

### 4. `config_test.go` — deferred `f.Close()`

**Issue:** `errcheck` flagged `defer f.Close()` in `TestLoadFile`.

**Fix:** `defer f.Close() //nolint:errcheck` — the temporary file is created by `os.CreateTemp` into `t.TempDir()`, which is automatically removed when the test completes. The close error is irrelevant in a temp-file test helper.

**Why not `_ =`:** Same reasoning as other deferred closes — the wrapper idiom is noisier than the nolint comment for a single-line defer.

---

## Final State

```
make ci-fast
```

Output:

```
golangci-lint run ./...
0 issues.
go test -race -count=1 -short ./internal/...
ok  github.com/K3y5tr0ke/mcpgate/internal/config    1.288s
ok  github.com/K3y5tr0ke/mcpgate/internal/proxy     1.261s
CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/mcpgate ./cmd/mcpgate
```

All three stages pass clean. The pre-push hook is installed and will block pushes that fail either lint or unit tests.

---

## TODOs

| # | Item | Sprint |
|---|---|---|
| T1 | Re-introduce trusted-proxy IP resolution (replace `chimw.RealIP` with a configurable CIDR-scoped implementation) | 3 or 4 |
| T2 | Add `make security-scan` to `ci-fast` once `govulncheck` is confirmed available in the dev environment | 2 |
| T3 | Investigate why `gosec.excludes: [G104]` and `issues.exclude` do not suppress G104 in golangci-lint v2 — may be a version-specific bug | Backlog |
