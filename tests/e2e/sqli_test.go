//go:build integration

package e2e_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

// SQL-injection suite (Sprint 10, task 10.4). mcpgate uses parameterized pgx
// queries throughout, so attacker-controlled text must be treated as inert data:
// payloads are stored/searched literally and never alter the database. These
// tests drive classic injection strings through real admin text fields and
// assert (a) no table is dropped/altered and (b) input round-trips verbatim.

// TestE2E_SQLi_ServerName_IsInertData creates a server whose name is a
// DROP-TABLE payload, then confirms the mcp_servers table is intact and the name
// was stored literally (not executed).
func TestE2E_SQLi_ServerName_IsInertData(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)
	hdr := map[string]string{"Authorization": "Bearer " + adminToken, "Content-Type": "application/json"}

	const payload = "inj-'; DROP TABLE mcp_servers; --"
	body, _ := json.Marshal(map[string]any{
		"name":       payload,
		"transport":  "streamable_http",
		"config":     map[string]any{"endpoint": "http://127.0.0.1:1"},
		"is_enabled": true,
	})
	createResp := ts.do(t, http.MethodPost, "/api/v1/admin/servers", bytes.NewReader(body), hdr)
	createResp.Body.Close() //nolint:errcheck
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("create with payload name: want 201, got %d", createResp.StatusCode)
	}

	// If the injection had executed, mcp_servers would be gone and this 500s.
	listResp := ts.do(t, http.MethodGet, "/api/v1/admin/servers", nil, hdr)
	defer listResp.Body.Close() //nolint:errcheck
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list servers after payload: want 200, got %d (table dropped?)", listResp.StatusCode)
	}
	var listed struct {
		Servers []struct {
			Name string `json:"name"`
		} `json:"servers"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatalf("decode servers: %v", err)
	}
	found := false
	for _, s := range listed.Servers {
		if s.Name == payload {
			found = true
		}
	}
	if !found {
		t.Errorf("payload name not stored verbatim — got %+v", listed.Servers)
	}
}

// TestE2E_SQLi_SearchFilters_NoInjection drives injection payloads through the
// logs `search` and users `email` filters and confirms the endpoints respond
// normally and the underlying tables remain queryable afterward.
func TestE2E_SQLi_SearchFilters_NoInjection(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)
	hdr := map[string]string{"Authorization": "Bearer " + adminToken}

	payloads := []string{
		"' OR '1'='1",
		"'; DROP TABLE audit_logs; --",
		"\" OR 1=1 --",
		"%' OR '%'='",
	}

	for _, p := range payloads {
		// Logs search filter.
		logsResp := ts.do(t, http.MethodGet,
			"/api/v1/admin/logs?search="+url.QueryEscape(p), nil, hdr)
		code := logsResp.StatusCode
		logsResp.Body.Close() //nolint:errcheck
		if code != http.StatusOK {
			t.Errorf("logs search %q: want 200, got %d", p, code)
		}

		// Users email filter.
		usersResp := ts.do(t, http.MethodGet,
			"/api/v1/admin/users?email="+url.QueryEscape(p), nil, hdr)
		ucode := usersResp.StatusCode
		usersResp.Body.Close() //nolint:errcheck
		if ucode != http.StatusOK {
			t.Errorf("users email filter %q: want 200, got %d", p, ucode)
		}
	}

	// Both tables must still be intact and queryable after all payloads.
	for _, path := range []string{"/api/v1/admin/logs", "/api/v1/admin/users"} {
		resp := ts.do(t, http.MethodGet, path, nil, hdr)
		code := resp.StatusCode
		resp.Body.Close() //nolint:errcheck
		if code != http.StatusOK {
			t.Errorf("%s after injection payloads: want 200, got %d (table altered?)", path, code)
		}
	}
}
