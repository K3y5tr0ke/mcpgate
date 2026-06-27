//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

// createTokenWithRoles creates a user + session with the given roles and returns
// a signed JWT that the server's auth middleware accepts.
func (ts *testServer) createTokenWithRoles(t *testing.T, roles []string) string {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	if err := ts.userStore.Create(ctx, &store.User{
		ID:       userID,
		Email:    "user-" + uuid.New().String()[:8] + "@test.internal",
		Status:   "active",
		Metadata: map[string]any{},
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}

	sessionID := uuid.New()
	if err := ts.sessionStore.Create(ctx, &store.Session{
		ID:        sessionID,
		UserID:    userID,
		ClientIP:  "127.0.0.1",
		UserAgent: "e2e-rbac-test",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := ts.sessionCache.Set(ctx, sessionID, store.SessionData{
		UserID:    userID,
		Email:     "user@test.internal",
		Roles:     roles,
		SessionID: sessionID,
	}, time.Hour); err != nil {
		t.Fatalf("cache session: %v", err)
	}

	token, err := ts.srv.JWTManager().Issue(userID, "user@test.internal", roles, sessionID)
	if err != nil {
		t.Fatalf("issue JWT: %v", err)
	}
	return token
}

// addPolicy adds a Casbin policy rule via the admin API and registers a cleanup
// to remove it after the test.
func (ts *testServer) addPolicy(t *testing.T, adminToken, role, serverName, action string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"role": role, "server": serverName, "action": action,
	})
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/policies",
		bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("add policy (%s,%s,%s): want 201, got %d: %s", role, serverName, action, resp.StatusCode, raw)
	}

	t.Cleanup(func() {
		delBody, _ := json.Marshal(map[string]string{
			"role": role, "server": serverName, "action": action,
		})
		delResp := ts.do(t, http.MethodDelete, "/api/v1/admin/policies",
			bytes.NewReader(delBody),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		_ = delResp.Body.Close()
	})
}

// mcpPost sends a JSON-RPC POST to /mcp/{serverName}/message.
func (ts *testServer) mcpPost(t *testing.T, serverName, token, method string) *http.Response {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": method,
	})
	return ts.do(t, http.MethodPost, "/mcp/"+serverName+"/message",
		bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer " + token,
			"Content-Type":  "application/json",
		},
	)
}

// TestE2E_RBAC_AdminRole_PassesWildcardPolicy verifies the seeded admin wildcard
// policy (p, admin, *, *) allows any action on any server.
func TestE2E_RBAC_AdminRole_PassesWildcardPolicy(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "admin-wildcard-server", mock.URL())

	resp := ts.mcpPost(t, "admin-wildcard-server", adminToken, "tools/list")
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if mock.RequestCount() == 0 {
		t.Error("upstream received no requests — RBAC blocked admin unexpectedly")
	}
}

// TestE2E_RBAC_NoMatchingPolicy_Returns403 verifies that a token with a role
// that has no policy for the target server is rejected with 403.
func TestE2E_RBAC_NoMatchingPolicy_Returns403(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "no-policy-server", mock.URL())

	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	resp := ts.mcpPost(t, "no-policy-server", devToken, "tools/list")
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 403, got %d: %s", resp.StatusCode, body)
	}
	if mock.RequestCount() != 0 {
		t.Errorf("upstream received %d requests — RBAC did not block", mock.RequestCount())
	}
}

// TestE2E_RBAC_DeveloperRole_AllowedServer_CanCall verifies that a developer
// token with an explicit call policy reaches the upstream.
func TestE2E_RBAC_DeveloperRole_AllowedServer_CanCall(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "dev-allowed-server", mock.URL())
	ts.addPolicy(t, adminToken, "developer", "dev-allowed-server", "call")

	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	resp := ts.mcpPost(t, "dev-allowed-server", devToken, "tools/call")
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if mock.RequestCount() == 0 {
		t.Error("upstream received no requests — proxy did not forward")
	}
}

// TestE2E_RBAC_DeveloperRole_WrongServer_Returns403 verifies that a policy for
// one server does not grant access to a different server.
func TestE2E_RBAC_DeveloperRole_WrongServer_Returns403(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "allowed-server", mock.URL())
	ts.registerMCPServer(t, adminToken, "other-server", mock.URL())
	ts.addPolicy(t, adminToken, "developer", "allowed-server", "call")

	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	resp := ts.mcpPost(t, "other-server", devToken, "tools/call")
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 403, got %d: %s", resp.StatusCode, body)
	}
}

// TestE2E_RBAC_ReadonlyRole_CanList_CannotCall verifies that a wildcard-server
// readonly policy permits list but not call.
func TestE2E_RBAC_ReadonlyRole_CanList_CannotCall(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "readonly-server", mock.URL())
	ts.addPolicy(t, adminToken, "readonly", "*", "list")

	roToken := ts.createTokenWithRoles(t, []string{"readonly"})

	listResp := ts.mcpPost(t, "readonly-server", roToken, "tools/list")
	defer listResp.Body.Close() //nolint:errcheck
	if listResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(listResp.Body)
		t.Errorf("list: want 200, got %d: %s", listResp.StatusCode, body)
	}

	callResp := ts.mcpPost(t, "readonly-server", roToken, "tools/call")
	defer callResp.Body.Close() //nolint:errcheck
	if callResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(callResp.Body)
		t.Errorf("call: want 403, got %d: %s", callResp.StatusCode, body)
	}
}

// TestE2E_RBAC_PolicyInvalidation_TakesEffect verifies that deleting a policy
// causes subsequent requests to be rejected (cache invalidated).
func TestE2E_RBAC_PolicyInvalidation_TakesEffect(t *testing.T) {
	ts := newTestServer(t)

	mock := testutil.NewMockMCPServer()
	t.Cleanup(mock.Close)

	adminToken := ts.createAdminToken(t)
	ts.registerMCPServer(t, adminToken, "inval-server", mock.URL())

	// Add policy and verify access.
	body, _ := json.Marshal(map[string]string{
		"role": "developer", "server": "inval-server", "action": "list",
	})
	addResp := ts.do(t, http.MethodPost, "/api/v1/admin/policies",
		bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	_ = addResp.Body.Close()
	if addResp.StatusCode != http.StatusCreated {
		t.Fatalf("add policy: want 201, got %d", addResp.StatusCode)
	}

	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	preResp := ts.mcpPost(t, "inval-server", devToken, "tools/list")
	_ = preResp.Body.Close()
	if preResp.StatusCode != http.StatusOK {
		t.Fatalf("pre-delete: want 200, got %d", preResp.StatusCode)
	}

	// Delete policy and verify access is now denied.
	delBody, _ := json.Marshal(map[string]string{
		"role": "developer", "server": "inval-server", "action": "list",
	})
	delResp := ts.do(t, http.MethodDelete, "/api/v1/admin/policies",
		bytes.NewReader(delBody),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	_ = delResp.Body.Close()
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete policy: want 204, got %d", delResp.StatusCode)
	}

	postResp := ts.mcpPost(t, "inval-server", devToken, "tools/list")
	defer postResp.Body.Close() //nolint:errcheck
	if postResp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(postResp.Body)
		t.Errorf("post-delete: want 403, got %d: %s", postResp.StatusCode, body)
	}
}

// TestE2E_RBAC_AdminAPI_NonAdminRole_Returns403 verifies that RequireRole blocks
// non-admin tokens from reaching admin API routes.
func TestE2E_RBAC_AdminAPI_NonAdminRole_Returns403(t *testing.T) {
	ts := newTestServer(t)

	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	resp := ts.do(t, http.MethodGet, "/api/v1/admin/servers", nil, map[string]string{
		"Authorization": "Bearer " + devToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 403, got %d: %s", resp.StatusCode, body)
	}
}

// TestE2E_RBAC_AdminAPI_NonAdmin_CannotMutate verifies that a non-admin role is
// blocked from *writing* admin resources, not just reading them. RequireRole
// gates the whole admin group regardless of method, so create/update/delete on
// servers and policies must all return 403 — the privilege-escalation vector
// that a read-only denial test would miss.
func TestE2E_RBAC_AdminAPI_NonAdmin_CannotMutate(t *testing.T) {
	ts := newTestServer(t)
	devToken := ts.createTokenWithRoles(t, []string{"developer"})
	hdr := map[string]string{"Authorization": "Bearer " + devToken, "Content-Type": "application/json"}

	cases := []struct {
		name, method, path, body string
	}{
		{"create server", http.MethodPost, "/api/v1/admin/servers",
			`{"name":"evil","transport":"http","config":{"url":"http://x"}}`},
		{"create policy", http.MethodPost, "/api/v1/admin/policies",
			`{"subject":"developer","resource":"*","action":"*"}`},
		{"delete server", http.MethodDelete, "/api/v1/admin/servers/" + uuid.New().String(), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body io.Reader
			if c.body != "" {
				body = bytes.NewReader([]byte(c.body))
			}
			resp := ts.do(t, c.method, c.path, body, hdr)
			defer resp.Body.Close() //nolint:errcheck
			if resp.StatusCode != http.StatusForbidden {
				b, _ := io.ReadAll(resp.Body)
				t.Errorf("%s %s: want 403, got %d: %s", c.method, c.path, resp.StatusCode, b)
			}
		})
	}
}

// TestE2E_RBAC_AdminAPI_AdminRole_Allowed verifies that admin tokens can still
// reach all admin API routes after RequireRole is wired.
func TestE2E_RBAC_AdminAPI_AdminRole_Allowed(t *testing.T) {
	ts := newTestServer(t)

	adminToken := ts.createAdminToken(t)
	resp := ts.do(t, http.MethodGet, "/api/v1/admin/servers", nil, map[string]string{
		"Authorization": "Bearer " + adminToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 200, got %d: %s", resp.StatusCode, body)
	}
}
