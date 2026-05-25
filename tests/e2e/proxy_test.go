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

// registerMCPServer calls the admin API to register a streamable-HTTP MCP server
// pointing at the given endpoint URL. Fatals if the server returns non-201.
func (ts *testServer) registerMCPServer(t *testing.T, adminToken, name, endpoint string) {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"name":       name,
		"transport":  "streamable_http",
		"config":     map[string]any{"endpoint": endpoint},
		"is_enabled": true,
	})
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/servers",
		bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("register MCP server: want 201, got %d: %s", resp.StatusCode, raw)
	}
}

// TestE2E_Proxy_UnauthenticatedRequest_Returns401 confirms that the auth
// middleware is wired on proxy routes: a request with no token must be
// rejected before reaching the upstream MCP server.
func TestE2E_Proxy_UnauthenticatedRequest_Returns401(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "auth-test-server", mock.URL())

	resp := ts.do(t, http.MethodPost, "/mcp/auth-test-server/message",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
		map[string]string{"Content-Type": "application/json"},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 401, got %d: %s", resp.StatusCode, body)
	}

	if mock.RequestCount() != 0 {
		t.Errorf("upstream received %d requests — auth middleware did not block", mock.RequestCount())
	}
}

// TestE2E_Proxy_AuthenticatedRequest_ProxiedToUpstream confirms the full
// authenticated proxy path: valid JWT → middleware passes → proxy forwards
// to upstream MockMCPServer → JSON-RPC result returned to client.
func TestE2E_Proxy_AuthenticatedRequest_ProxiedToUpstream(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "proxy-test-server", mock.URL())

	resp := ts.do(t, http.MethodPost, "/mcp/proxy-test-server/message",
		bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
		map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer " + adminToken,
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var rpc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&rpc); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if rpc["result"] == nil {
		t.Errorf("expected JSON-RPC result field, got: %v", rpc)
	}
	if mock.RequestCount() == 0 {
		t.Error("upstream received no requests — proxy did not forward")
	}
}
