// log_pipeline.js — Sprint 10 task 10.10: audit-log pipeline under stress.
//
// Sustains ~1000 req/s for 10 minutes (~600k proxied requests = ~600k audit
// entries) to verify the async logging pipeline keeps up without silently
// dropping entries, and that the hash chain stays intact under load.
//
//   k6 run \
//     -e BASE_URL=http://localhost:8080 \
//     -e MCPGATE_TOKEN=$TOKEN \
//     -e MCP_SERVER=example-streamable \
//     k6/log_pipeline.js
//
// Capture verification: teardown reads the audit total and calls
// GET /logs/verify. Compare the reported `total` against this run's
// `http_reqs` (k6 summary) — they should match modulo entries from other
// traffic; the PRD requires 100% capture (no silent drops). The Prometheus
// gauge `mcpgate_audit_pipeline_dropped_total` must remain 0.

import http from 'k6/http';
import { check } from 'k6';

const BASE = __ENV.BASE_URL || 'http://localhost:8080';
const TOKEN = __ENV.MCPGATE_TOKEN;
const SERVER = __ENV.MCP_SERVER || 'example-streamable';
const auth = { Authorization: `Bearer ${TOKEN}` };

export const options = {
  scenarios: {
    sustained: {
      executor: 'constant-arrival-rate',
      rate: 1000,
      timeUnit: '1s',
      duration: '10m',
      preAllocatedVUs: 300,
      maxVUs: 1500,
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
};

const body = JSON.stringify({ jsonrpc: '2.0', id: 1, method: 'tools/list' });

export function setup() {
  if (!TOKEN) {
    throw new Error('MCPGATE_TOKEN is required (-e MCPGATE_TOKEN=...)');
  }
  // Record the starting audit total so capture can be measured as a delta.
  const res = http.get(`${BASE}/api/v1/admin/logs?page=1&page_size=1`, { headers: auth });
  let startTotal = 0;
  try {
    startTotal = JSON.parse(res.body).total || 0;
  } catch (_) {
    // ignore — best-effort baseline
  }
  return { startTotal };
}

export default function () {
  const res = http.post(`${BASE}/mcp/${SERVER}/message`, body, {
    headers: { 'Content-Type': 'application/json', ...auth },
    tags: { name: 'proxy_tools_list' },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}

export function teardown(data) {
  const totalRes = http.get(`${BASE}/api/v1/admin/logs?page=1&page_size=1`, { headers: auth });
  let endTotal = 0;
  try {
    endTotal = JSON.parse(totalRes.body).total || 0;
  } catch (_) {
    endTotal = -1;
  }
  console.log(`audit entries captured during run (delta): ${endTotal - data.startTotal}`);

  // Verify the hash chain over the run window.
  const verifyRes = http.get(`${BASE}/api/v1/admin/logs/verify`, { headers: auth });
  console.log(`/logs/verify -> ${verifyRes.status}: ${verifyRes.body}`);
}
