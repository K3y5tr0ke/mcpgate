//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// waitForAuditEntries polls the audit store until at least minCount entries
// match the filter, or the deadline passes.
func waitForAuditEntries(
	t *testing.T,
	ts *testServer,
	filter store.LogFilter,
	minCount int,
	timeout time.Duration,
) []store.AuditLogEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		entries, total, err := ts.auditStore.Query(
			context.Background(), filter, store.Pagination{Page: 1, PageSize: 100},
		)
		if err != nil {
			t.Fatalf("poll audit store: %v", err)
		}
		if int(total) >= minCount {
			return entries
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d audit entries (filter: %+v)", minCount, filter)
	return nil
}

// TestE2E_AuditLog_ProxyRequest_CreatesEntry verifies that a proxied MCP
// request produces an audit log entry with the correct fields.
func TestE2E_AuditLog_ProxyRequest_CreatesEntry(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	// Grant admin access to all MCP servers so the request passes RBAC.
	// (SeedDefaultPolicies already does this for the admin role.)

	// Register a mock MCP server that returns a valid tools/list response.
	serverID := uuid.New()
	serverName := "audit-test-server-" + serverID.String()[:8]

	// Register the server via the admin API.
	createBody := map[string]any{
		"name":       serverName,
		"transport":  "http",
		"is_enabled": true,
		"config":     map[string]any{"url": "http://127.0.0.1:1"},
	}
	createJSON, _ := json.Marshal(createBody)
	createResp := ts.do(t, http.MethodPost, "/api/v1/admin/servers",
		newJSONReader(createJSON),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer createResp.Body.Close() //nolint:errcheck

	// Send a proxied request (will fail upstream, but the audit entry is still written).
	proxyResp := ts.do(t, http.MethodPost, "/mcp/"+serverName+"/message",
		newJSONReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer proxyResp.Body.Close() //nolint:errcheck

	// Wait for the pipeline to flush.
	entries := waitForAuditEntries(t, ts, store.LogFilter{
		MCPMethod: "tools/list",
	}, 1, 2*time.Second)

	if len(entries) == 0 {
		t.Fatal("no audit entries for tools/list")
	}
	e := entries[0]
	if e.MCPMethod != "tools/list" {
		t.Errorf("MCPMethod: got %q, want tools/list", e.MCPMethod)
	}
	if e.UserEmail != "admin@test.internal" {
		t.Errorf("UserEmail: got %q, want admin@test.internal", e.UserEmail)
	}
	if e.HashChain == "" {
		t.Error("HashChain is empty — pipeline did not compute hash")
	}
	if e.ServerName != serverName {
		t.Errorf("ServerName: got %q, want %q", e.ServerName, serverName)
	}
}

// TestE2E_AuditLog_RBACDenied_ProducesEntryWithDeniedStatus verifies that a
// request blocked by the RBAC enforcer still produces a "denied" audit entry.
func TestE2E_AuditLog_RBACDenied_ProducesEntryWithDeniedStatus(t *testing.T) {
	ts := newTestServer(t)

	// Create a regular (non-admin) user token with no MCP permissions.
	ctx := context.Background()
	userID := uuid.New()
	sessionID := uuid.New()
	_ = ts.userStore.Create(ctx, &store.User{
		ID:       userID,
		Email:    "regular@test.internal",
		Status:   "active",
		Metadata: map[string]any{},
	})
	_ = ts.sessionStore.Create(ctx, &store.Session{
		ID:        sessionID,
		UserID:    userID,
		ClientIP:  "127.0.0.1",
		UserAgent: "test",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	_ = ts.sessionCache.Set(ctx, sessionID, store.SessionData{
		UserID:    userID,
		Email:     "regular@test.internal",
		Roles:     []string{"developer"},
		SessionID: sessionID,
	}, time.Hour)
	devToken, _ := ts.srv.JWTManager().Issue(userID, "regular@test.internal", []string{"developer"}, sessionID)

	resp := ts.do(t, http.MethodPost, "/mcp/any-server/message",
		newJSONReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call"}`)),
		map[string]string{
			"Authorization": "Bearer " + devToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403, got %d", resp.StatusCode)
	}

	entries := waitForAuditEntries(t, ts, store.LogFilter{
		Status: "denied",
	}, 1, 2*time.Second)

	if len(entries) == 0 {
		t.Fatal("no denied audit entries")
	}
	if entries[0].Status != "denied" {
		t.Errorf("Status: got %q, want denied", entries[0].Status)
	}
}

// TestE2E_AuditLog_AdminAPI_ListLogs verifies that the admin logs API returns
// entries and requires the admin role.
func TestE2E_AuditLog_AdminAPI_ListLogs(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	// Non-admin must get 403.
	ctx := context.Background()
	userID := uuid.New()
	sessionID := uuid.New()
	_ = ts.userStore.Create(ctx, &store.User{ID: userID, Email: "dev@test.internal", Status: "active", Metadata: map[string]any{}})
	_ = ts.sessionStore.Create(ctx, &store.Session{ID: sessionID, UserID: userID, ClientIP: "127.0.0.1", UserAgent: "t", ExpiresAt: time.Now().Add(time.Hour)})
	_ = ts.sessionCache.Set(ctx, sessionID, store.SessionData{UserID: userID, Email: "dev@test.internal", Roles: []string{"developer"}, SessionID: sessionID}, time.Hour)
	devToken, _ := ts.srv.JWTManager().Issue(userID, "dev@test.internal", []string{"developer"}, sessionID)

	nonAdminResp := ts.do(t, http.MethodGet, "/api/v1/admin/logs", nil,
		map[string]string{"Authorization": "Bearer " + devToken})
	defer nonAdminResp.Body.Close() //nolint:errcheck
	if nonAdminResp.StatusCode != http.StatusForbidden {
		t.Errorf("non-admin: want 403, got %d", nonAdminResp.StatusCode)
	}

	// Admin must get 200 with a valid JSON response.
	adminResp := ts.do(t, http.MethodGet, "/api/v1/admin/logs", nil,
		map[string]string{"Authorization": "Bearer " + adminToken})
	defer adminResp.Body.Close() //nolint:errcheck

	if adminResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(adminResp.Body)
		t.Fatalf("admin logs: want 200, got %d: %s", adminResp.StatusCode, body)
	}

	var result map[string]any
	if err := json.NewDecoder(adminResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode logs response: %v", err)
	}
	if _, ok := result["entries"]; !ok {
		t.Error("response missing 'entries' field")
	}
}

// TestE2E_AuditLog_HashChain_Intact verifies that the hash chain is unbroken
// for a sequence of proxied requests.
func TestE2E_AuditLog_HashChain_Intact(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	const numRequests = 5
	for i := 0; i < numRequests; i++ {
		resp := ts.do(t, http.MethodPost, "/mcp/any-server/message",
			newJSONReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)),
			map[string]string{
				"Authorization": "Bearer " + adminToken,
				"Content-Type":  "application/json",
			},
		)
		resp.Body.Close() //nolint:errcheck
	}

	// Wait for all entries to flush.
	waitForAuditEntries(t, ts, store.LogFilter{MCPMethod: "ping"}, numRequests, 3*time.Second)

	from := time.Now().Add(-time.Minute)
	to := time.Now().Add(time.Minute)
	ok, err := ts.auditStore.VerifyHashChain(context.Background(), from, to, "test-genesis")
	if err != nil {
		t.Fatalf("VerifyHashChain: %v", err)
	}
	if !ok {
		t.Error("hash chain verification failed — entries may be out of order or tampered")
	}
}

// newJSONReader returns an io.Reader for a JSON byte slice.
func newJSONReader(b []byte) io.Reader {
	return newBytesReader(b)
}

type bytesReader struct{ data []byte; pos int }

func newBytesReader(b []byte) *bytesReader { return &bytesReader{data: b} }

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, io.EOF
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
