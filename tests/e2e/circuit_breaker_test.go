//go:build integration

package e2e_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// TestE2E_CircuitBreaker_OpensAfterThreshold verifies that repeated connection
// failures to an upstream server open the circuit breaker. Once open, the proxy
// returns a JSON-RPC error with "circuit open" in the message and a Retry-After
// header — without contacting the upstream at all.
//
// Design note: WithForceFail() on MockMCPServer returns HTTP 200 + JSON-RPC error,
// so tr.Send() returns (body, nil) and RecordFailure() is never called. To trip
// the circuit breaker we point the server at a port that refuses connections
// (127.0.0.1:1), which causes tr.Send() to return a Go error and RecordFailure()
// to be called on every attempt.
func TestE2E_CircuitBreaker_OpensAfterThreshold(t *testing.T) {
	const failureThreshold = 3

	ts := newTestServerWith(t, func(cfg *config.Config) {
		cfg.Proxy.CircuitBreaker.FailureThreshold = failureThreshold
		cfg.Proxy.CircuitBreaker.RecoveryTimeout = 60 * time.Second
		cfg.Proxy.CircuitBreaker.HalfOpenMaxRequests = 1
	})

	adminToken := ts.createAdminToken(t)

	// Register a server pointing to a port that is not listening.
	// Port 1 requires root to bind and is never open in CI environments.
	ts.registerMCPServer(t, adminToken, "cb-server", "http://127.0.0.1:1")

	// Send exactly FailureThreshold requests to exhaust the circuit breaker.
	// Each returns HTTP 200 with a JSON-RPC upstream error; we discard them.
	for i := 0; i < failureThreshold; i++ {
		resp := ts.do(t, http.MethodPost, "/mcp/cb-server/message",
			bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()              //nolint:errcheck
	}

	// The circuit should now be open. This request must be blocked before the
	// transport is even contacted.
	resp := ts.do(t, http.MethodPost, "/mcp/cb-server/message",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.Header.Get("Retry-After") == "" {
		t.Error("open circuit: expected Retry-After header on response")
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response body: %v", err)
	}

	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected JSON-RPC error field in response: %v", body)
	}
	msg, _ := errObj["message"].(string)
	if !strings.Contains(msg, "circuit open") {
		t.Errorf("expected 'circuit open' in error message, got: %q", msg)
	}
}
