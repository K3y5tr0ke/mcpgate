// proxy_throughput.js — Sprint 10 task 10.9: proxy throughput load test.
//
// Ramps arrival rate from 100 → 1000 req/s through the proxy and asserts the
// latency / error-rate targets. Run against a running mcpgate with at least one
// reachable upstream registered (e.g. `make docker-up-examples && make seed-examples`).
//
//   k6 run \
//     -e BASE_URL=http://localhost:8080 \
//     -e MCPGATE_TOKEN=$TOKEN \
//     -e MCP_SERVER=example-streamable \
//     k6/proxy_throughput.js
//
// Thresholds (from docs/mcpgate-development-plan.md 10.9): p95 < 50ms,
// p99 < 100ms, error rate < 1%. The PRD's stricter "p99 ≤ 5ms passthrough
// overhead" is the proxy's own added latency; measure it by subtracting the
// upstream's direct latency from these numbers when profiling with pprof.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const TOKEN = __ENV.MCPGATE_TOKEN;
const SERVER = __ENV.MCP_SERVER || 'example-streamable';

export const options = {
  scenarios: {
    ramp: {
      executor: 'ramping-arrival-rate',
      startRate: 100,
      timeUnit: '1s',
      preAllocatedVUs: 200,
      maxVUs: 1200,
      stages: [
        { target: 100, duration: '1m' }, // warm up
        { target: 1000, duration: '8m' }, // ramp to peak
        { target: 1000, duration: '1m' }, // hold at peak
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'], // < 1% errors
    http_req_duration: ['p(95)<50', 'p(99)<100'], // milliseconds
  },
};

const body = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' });

export function setup() {
  if (!TOKEN) {
    throw new Error('MCPGATE_TOKEN is required (-e MCPGATE_TOKEN=...)');
  }
}

export default function () {
  const res = http.post(`${BASE}/mcp/${SERVER}/message`, body, {
    headers: { 'Content-Type': 'application/json', Authorization: `Bearer ${TOKEN}` },
    tags: { name: 'proxy_tools_list' },
  });
  check(res, {
    'status is 200': (r) => r.status === 200,
    'has jsonrpc result': (r) => typeof r.body === 'string' && r.body.indexOf('"result"') !== -1,
  });
}
