//go:build integration

package e2e_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

// TestE2E_Metrics_EndpointReturnsPrometheusText verifies that the dedicated
// metrics port serves Prometheus text format at /metrics.
func TestE2E_Metrics_EndpointReturnsPrometheusText(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get("http://" + ts.srv.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type: got %q, want text/plain prefix", ct)
	}
}

// TestE2E_Metrics_RequestCounterIncrements verifies that mcpgate_requests_total
// appears in the metrics output after proxied requests are made.
func TestE2E_Metrics_RequestCounterIncrements(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)
	ts.registerMCPServer(t, adminToken, "metrics-server", mock.URL())

	for i := 0; i < 3; i++ {
		resp := ts.do(t, http.MethodPost, "/mcp/metrics-server/message",
			newJSONReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}

	metricsResp, err := http.Get("http://" + ts.srv.MetricsAddr() + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer metricsResp.Body.Close() //nolint:errcheck

	body, _ := io.ReadAll(metricsResp.Body)
	if !strings.Contains(string(body), "mcpgate_requests_total") {
		t.Errorf("mcpgate_requests_total not found in metrics output:\n%s", body)
	}
	if mock.RequestCount() == 0 {
		t.Error("upstream received no requests — proxy did not forward")
	}
}
