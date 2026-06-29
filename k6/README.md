# Load tests (k6)

Sprint 10 performance validation (tasks 10.9 / 10.10). Uses [Grafana k6](https://k6.io).

## Prerequisites
- `k6` installed (`brew install k6`).
- A running mcpgate (`make dev` or the full stack) with **at least one reachable upstream registered**:
  ```bash
  make docker-up-examples   # or: make build-examples && run host binaries
  MCPGATE_TOKEN=$TOKEN make seed-examples
  ```
- An admin JWT in `MCPGATE_TOKEN` (from `/auth/local/login`).

> **Use a fast, local upstream.** The `p95<50ms / p99<100ms` thresholds are
> *end-to-end* (proxy **+** upstream + network). A slow or remote example server
> will fail the gate for reasons unrelated to mcpgate's own overhead. Point
> `MCP_SERVER` at a local, near-instant upstream when validating proxy latency.

## Run

```bash
# 10.9 — proxy throughput (ramps 100→1000 req/s; asserts p95<50ms, p99<100ms, <1% errors)
MCPGATE_TOKEN=$TOKEN make load-test

# 10.10 — log pipeline under stress (1000 req/s for 10m; ~600k audit entries)
MCPGATE_TOKEN=$TOKEN make load-test-pipeline
```

Override the target server or base URL via env:
```bash
k6 run -e BASE_URL=http://localhost:8080 -e MCPGATE_TOKEN=$TOKEN -e MCP_SERVER=example-sse k6/proxy_throughput.js
```

## Interpreting results

**10.9 (`proxy_throughput.js`)** — k6 enforces the thresholds and exits non-zero if any fail. The PRD's stricter target is **p99 ≤ 5ms of proxy *overhead*** (mcpgate's added latency, not total). To isolate overhead, also load the upstream directly and subtract; profile mcpgate with `pprof` if the budget is exceeded.

**10.10 (`log_pipeline.js`)** — the goal is **100% audit capture with no silent drops** (PRD §6.4.4). After the run:
- Compare the teardown's "audit entries captured (delta)" against k6's `http_reqs` count — they should match (modulo unrelated traffic).
- Confirm the Prometheus gauge `mcpgate_audit_pipeline_dropped_total` is **0** (`curl localhost:9090/metrics | grep dropped`).
- Confirm `/logs/verify` returns ok (hash chain intact) — printed in teardown.
