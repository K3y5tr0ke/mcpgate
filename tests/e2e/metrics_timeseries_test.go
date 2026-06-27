//go:build integration

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

type timeseriesResp struct {
	Window         string `json:"window"`
	Bucket         string `json:"bucket"`
	ResolutionSecs int    `json:"resolution_secs"`
	Buckets        []struct {
		Minute   int64 `json:"minute"`
		Requests int64 `json:"requests"`
	} `json:"buckets"`
}

func getTimeseries(t *testing.T, ts *testServer, token, query string) timeseriesResp {
	t.Helper()
	resp := ts.do(t, http.MethodGet, "/api/v1/admin/metrics/timeseries"+query, nil,
		map[string]string{"Authorization": "Bearer " + token},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET timeseries%s: want 200, got %d: %s", query, resp.StatusCode, body)
	}
	var out timeseriesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode timeseries: %v", err)
	}
	return out
}

// TestE2E_MetricsAPI_TimeseriesWindows verifies each supported window returns the
// expected number of buckets at the expected resolution, and echoes window/bucket.
func TestE2E_MetricsAPI_TimeseriesWindows(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	cases := []struct {
		window     string
		wantPoints int
		wantRes    int
		wantBucket string
	}{
		{"1h", 60, 60, "1m"},
		{"6h", 72, 300, "5m"},
		{"24h", 96, 900, "15m"},
		{"7d", 84, 7200, "2h"},
	}
	for _, c := range cases {
		t.Run(c.window, func(t *testing.T) {
			got := getTimeseries(t, ts, adminToken, "?window="+c.window)
			if len(got.Buckets) != c.wantPoints {
				t.Errorf("window %s: got %d buckets, want %d", c.window, len(got.Buckets), c.wantPoints)
			}
			if got.ResolutionSecs != c.wantRes {
				t.Errorf("window %s: got resolution_secs %d, want %d", c.window, got.ResolutionSecs, c.wantRes)
			}
			if got.Window != c.window {
				t.Errorf("window %s: echoed window %q", c.window, got.Window)
			}
			if got.Bucket != c.wantBucket {
				t.Errorf("window %s: got bucket %q, want %q", c.window, got.Bucket, c.wantBucket)
			}
		})
	}
}

// TestE2E_MetricsAPI_TimeseriesDefaultWindow verifies the no-param request still
// returns the 60-bucket 1h series (backward compatibility for existing callers).
func TestE2E_MetricsAPI_TimeseriesDefaultWindow(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	got := getTimeseries(t, ts, adminToken, "")
	if len(got.Buckets) != 60 {
		t.Errorf("default window: got %d buckets, want 60", len(got.Buckets))
	}
	if got.ResolutionSecs != 60 {
		t.Errorf("default window: got resolution_secs %d, want 60", got.ResolutionSecs)
	}
}

// TestE2E_MetricsAPI_TimeseriesInvalidWindow verifies an unknown window → 400.
func TestE2E_MetricsAPI_TimeseriesInvalidWindow(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/metrics/timeseries?window=bogus", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("invalid window: want 400, got %d", resp.StatusCode)
	}
}

// TestE2E_MetricsAPI_TimeseriesPerServer drives one real proxied request through a
// registered server, then asserts the per-server series reflects that traffic while
// an unrelated server's series stays empty.
func TestE2E_MetricsAPI_TimeseriesPerServer(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t) // admin role → seeded (admin,*,*) policy
	ts.registerMCPServer(t, adminToken, "ts-server", mock.URL())

	// Drive one authenticated request through the proxy so the per-server key increments.
	resp := ts.do(t, http.MethodPost, "/mcp/ts-server/message",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
		map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + adminToken,
		},
	)
	resp.Body.Close() //nolint:errcheck

	// The per-server series should reflect ≥1 request in the window.
	got := getTimeseries(t, ts, adminToken, "?window=1h&server=ts-server")
	if len(got.Buckets) != 60 {
		t.Fatalf("per-server: got %d buckets, want 60", len(got.Buckets))
	}
	var total int64
	for _, b := range got.Buckets {
		total += b.Requests
	}
	if total < 1 {
		t.Errorf("per-server ts-server: got %d requests, want ≥1", total)
	}

	// An unregistered/idle server name should yield an all-zero series.
	idle := getTimeseries(t, ts, adminToken, "?window=1h&server=no-such-server")
	var idleTotal int64
	for _, b := range idle.Buckets {
		idleTotal += b.Requests
	}
	if idleTotal != 0 {
		t.Errorf("idle server: got %d requests, want 0", idleTotal)
	}
}

// seriesTotal sums the request counts across every bucket in a timeseries.
func seriesTotal(s timeseriesResp) int64 {
	var total int64
	for _, b := range s.Buckets {
		total += b.Requests
	}
	return total
}

// TestE2E_MetricsAPI_TimeseriesPerServerIsolation drives *different* request
// volumes through two distinct registered servers and asserts each per-server
// series reflects exactly its own traffic — neither leaks into the other. This
// is the property the per-server drilldown relies on: a key-prefix bug that
// counted one server's requests under another would still pass the single-server
// test above but must fail here.
func TestE2E_MetricsAPI_TimeseriesPerServerIsolation(t *testing.T) {
	ts := newTestServer(t)

	mockA := testutil.NewMockMCPServer()
	t.Cleanup(mockA.Close)
	mockB := testutil.NewMockMCPServer()
	t.Cleanup(mockB.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "iso-a", mockA.URL())
	ts.registerMCPServer(t, adminToken, "iso-b", mockB.URL())

	// Distinct, non-equal volumes so differentiation (not just isolation) is asserted.
	const reqA, reqB = 4, 2
	send := func(server string, n int) {
		for i := 0; i < n; i++ {
			resp := ts.do(t, http.MethodPost, "/mcp/"+server+"/message",
				bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
				map[string]string{
					"Content-Type":  "application/json",
					"Authorization": "Bearer " + adminToken,
				},
			)
			resp.Body.Close() //nolint:errcheck
		}
	}
	send("iso-a", reqA)
	send("iso-b", reqB)

	totalA := seriesTotal(getTimeseries(t, ts, adminToken, "?window=1h&server=iso-a"))
	totalB := seriesTotal(getTimeseries(t, ts, adminToken, "?window=1h&server=iso-b"))

	if totalA != reqA {
		t.Errorf("iso-a: got %d requests, want %d (leakage or miscount)", totalA, reqA)
	}
	if totalB != reqB {
		t.Errorf("iso-b: got %d requests, want %d (leakage or miscount)", totalB, reqB)
	}
	if totalA == totalB {
		t.Errorf("expected the two per-server series to differ (a=%d, b=%d)", totalA, totalB)
	}

	// The global series should account for at least both servers' traffic combined.
	if global := seriesTotal(getTimeseries(t, ts, adminToken, "?window=1h")); global < reqA+reqB {
		t.Errorf("global series: got %d requests, want ≥ %d", global, reqA+reqB)
	}
}
