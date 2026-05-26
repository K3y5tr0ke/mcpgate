//go:build integration

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// TestE2E_MetricsAPI_SummaryReturnsExpectedFields verifies that the admin API
// metrics summary endpoint returns a JSON object with all expected top-level fields.
func TestE2E_MetricsAPI_SummaryReturnsExpectedFields(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/metrics/summary", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var summary map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	required := []string{
		"requests_per_minute",
		"error_rate_percent",
		"p95_latency_ms",
		"active_requests",
		"active_sessions",
		"circuit_breakers",
		"dropped_audit_logs",
	}
	for _, field := range required {
		if _, ok := summary[field]; !ok {
			t.Errorf("missing field %q in metrics summary", field)
		}
	}
}

// TestE2E_MetricsAPI_SummaryRequiresAuth verifies that unauthenticated requests
// to the metrics summary endpoint are rejected with 401.
func TestE2E_MetricsAPI_SummaryRequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/metrics/summary", nil, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// TestE2E_MetricsAPI_TimeseriesReturnsBuckets verifies the timeseries endpoint
// returns a response with a non-nil buckets array.
func TestE2E_MetricsAPI_TimeseriesReturnsBuckets(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/metrics/timeseries", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var ts2 struct {
		Buckets []map[string]json.RawMessage `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ts2); err != nil {
		t.Fatalf("decode timeseries: %v", err)
	}
	if ts2.Buckets == nil {
		t.Error("buckets field is nil, want non-nil slice")
	}
}
