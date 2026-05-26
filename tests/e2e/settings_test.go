//go:build integration

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestE2E_Settings_GetReturnsDefaultKeys verifies that GET /settings returns a
// JSON object containing the keys seeded by migration 005.
func TestE2E_Settings_GetReturnsDefaultKeys(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/settings", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}

	var settings map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	expected := []string{
		"session_ttl_seconds",
		"rate_limit_default_rpm",
		"rate_limit_default_rpd",
		"audit_log_retention_days",
	}
	for _, key := range expected {
		if _, ok := settings[key]; !ok {
			t.Errorf("missing key %q in settings", key)
		}
	}
}

// TestE2E_Settings_PutUpdatesValue verifies that PUT /settings round-trips a
// value change: write a new value, then read it back.
func TestE2E_Settings_PutUpdatesValue(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	body := strings.NewReader(`{"session_ttl_seconds": 7200}`)
	resp := ts.do(t, http.MethodPut, "/api/v1/admin/settings", body,
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT want 200, got %d: %s", resp.StatusCode, b)
	}

	// Read back and confirm the value was updated.
	resp2 := ts.do(t, http.MethodGet, "/api/v1/admin/settings", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp2.Body.Close() //nolint:errcheck

	var settings map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}

	val, ok := settings["session_ttl_seconds"]
	if !ok {
		t.Fatal("session_ttl_seconds missing after PUT")
	}
	// JSON numbers decode as float64.
	if v, _ := val.(float64); v != 7200 {
		t.Errorf("session_ttl_seconds: got %v, want 7200", val)
	}
}

// TestE2E_Settings_PutRejectsUnknownKey verifies that PUT /settings with an
// unknown key returns 400 (key allowlist enforcement).
func TestE2E_Settings_PutRejectsUnknownKey(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	body := strings.NewReader(`{"unknown_setting_xyz": 42}`)
	resp := ts.do(t, http.MethodPut, "/api/v1/admin/settings", body,
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 400, got %d: %s", resp.StatusCode, b)
	}
}

// TestE2E_Settings_RequiresAuth verifies that unauthenticated requests to
// settings endpoints are rejected with 401.
func TestE2E_Settings_RequiresAuth(t *testing.T) {
	ts := newTestServer(t)

	for _, method := range []string{http.MethodGet, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			var body io.Reader
			headers := map[string]string{}
			if method == http.MethodPut {
				body = strings.NewReader(`{"session_ttl_seconds": 3600}`)
				headers["Content-Type"] = "application/json"
			}
			resp := ts.do(t, method, "/api/v1/admin/settings", body, headers)
			defer resp.Body.Close() //nolint:errcheck

			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", resp.StatusCode)
			}
		})
	}
}
