//go:build integration

package e2e_test

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

// TestE2E_RateLimit_RPM_Exceeded verifies that once a user exceeds the
// configured requests-per-minute limit, subsequent requests return HTTP 429
// with a Retry-After header.
func TestE2E_RateLimit_RPM_Exceeded(t *testing.T) {
	// Use a very low RPM so we can exceed it quickly in the test.
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.RateLimiting.Enabled = true
		cfg.RateLimiting.Default.RPM = 3
	})

	adminToken := ts.createAdminToken(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)
	ts.registerMCPServer(t, adminToken, "rl-server", mock.URL())

	// Send more requests than the RPM limit allows.
	const total = 10
	statusCodes := make([]int, total)
	for i := 0; i < total; i++ {
		resp := ts.do(t, http.MethodPost, "/mcp/rl-server/message",
			bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		statusCodes[i] = resp.StatusCode
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}

	got429 := false
	for _, code := range statusCodes {
		if code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Errorf("expected at least one 429 after exceeding RPM=%d, got status codes: %v",
			3, statusCodes)
	}
}

// TestE2E_RateLimit_RetryAfterHeader verifies that a 429 response includes the
// Retry-After header so clients know when to retry.
func TestE2E_RateLimit_RetryAfterHeader(t *testing.T) {
	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.RateLimiting.Enabled = true
		cfg.RateLimiting.Default.RPM = 1
	})

	adminToken := ts.createAdminToken(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)
	ts.registerMCPServer(t, adminToken, "rl-retry-server", mock.URL())

	var rateLimitedResp *http.Response
	for i := 0; i < 5; i++ {
		resp := ts.do(t, http.MethodPost, "/mcp/rl-retry-server/message",
			bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		if resp.StatusCode == http.StatusTooManyRequests {
			rateLimitedResp = resp
			break
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}

	if rateLimitedResp == nil {
		t.Fatal("never received a 429 response")
	}
	defer rateLimitedResp.Body.Close() //nolint:errcheck

	if rateLimitedResp.Header.Get("Retry-After") == "" {
		t.Error("429 response missing Retry-After header")
	}
}
