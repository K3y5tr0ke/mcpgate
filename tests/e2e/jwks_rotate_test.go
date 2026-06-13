//go:build integration

package e2e_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// jwksConfigID looks up the stored config ID for an issuer (the register helper
// does not return it).
func jwksConfigID(t *testing.T, ts *testServer, issuer string) uuid.UUID {
	t.Helper()
	cfg, err := ts.jwksStore.GetByIssuer(context.Background(), issuer)
	if err != nil {
		t.Fatalf("lookup jwks config by issuer %q: %v", issuer, err)
	}
	return cfg.ID
}

// TestE2E_JWKSRotate_Success seeds a config pointing at a reachable JWKS server
// and asserts rotate returns 200 with the advertised key count.
func TestE2E_JWKSRotate_Success(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	issuer := "https://rotate-ok-" + uuid.New().String()[:8] + ".example.com/"
	jwks := newJWKSServer(t, issuer) // serves exactly one key
	ts.registerJWKSConfig(t, adminToken, "rotate-ok", issuer, jwks.URL, "")

	id := jwksConfigID(t, ts, issuer)
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/jwks/configs/"+id.String()+"/rotate", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("rotate: want 200, got %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Issuer   string `json:"issuer"`
		KeyCount int    `json:"key_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode rotate response: %v", err)
	}
	if out.KeyCount < 1 {
		t.Errorf("key_count = %d, want ≥1", out.KeyCount)
	}
	if out.Issuer != issuer {
		t.Errorf("issuer = %q, want %q", out.Issuer, issuer)
	}
}

// TestE2E_JWKSRotate_UnknownID asserts a rotate for a non-existent config → 404.
func TestE2E_JWKSRotate_UnknownID(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	resp := ts.do(t, http.MethodPost, "/api/v1/admin/jwks/configs/"+uuid.New().String()+"/rotate", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown id: want 404, got %d", resp.StatusCode)
	}
}

// TestE2E_JWKSRotate_Unreachable asserts that when the configured JWKS URL is
// unreachable the rotate endpoint returns 502.
func TestE2E_JWKSRotate_Unreachable(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	// Start a JWKS server, register against it, then close it so the URL is dead.
	issuer := "https://rotate-dead-" + uuid.New().String()[:8] + ".example.com/"
	dead := newJWKSServer(t, issuer)
	deadURL := dead.URL
	ts.registerJWKSConfig(t, adminToken, "rotate-dead", issuer, deadURL, "")
	dead.Close()

	id := jwksConfigID(t, ts, issuer)
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/jwks/configs/"+id.String()+"/rotate", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("unreachable: want 502, got %d", resp.StatusCode)
	}
}

// TestE2E_JWKSRotate_EmptyKeys asserts a JWKS endpoint returning an empty key set → 502.
func TestE2E_JWKSRotate_EmptyKeys(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"keys":[]}`))
	}))
	t.Cleanup(empty.Close)

	issuer := "https://rotate-empty-" + uuid.New().String()[:8] + ".example.com/"
	if err := ts.jwksStore.Create(context.Background(), &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "rotate-empty",
		Issuer:    issuer,
		JWKSURL:   empty.URL,
		IsEnabled: true,
	}); err != nil {
		t.Fatalf("create config: %v", err)
	}

	id := jwksConfigID(t, ts, issuer)
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/jwks/configs/"+id.String()+"/rotate", nil,
		map[string]string{"Authorization": "Bearer " + adminToken},
	)
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("empty keys: want 502, got %d", resp.StatusCode)
	}
}
