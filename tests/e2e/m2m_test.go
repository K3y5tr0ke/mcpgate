//go:build integration

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// registerJWKSConfig calls the admin API to register a JWKS config.
func (ts *testServer) registerJWKSConfig(t *testing.T, adminToken, name, issuer, jwksURL, audience string) {
	t.Helper()

	body, _ := json.Marshal(map[string]any{
		"name":       name,
		"issuer":     issuer,
		"jwks_url":   jwksURL,
		"audience":   audience,
		"is_enabled": true,
	})
	resp := ts.do(t, http.MethodPost, "/api/v1/admin/jwks/configs",
		bytes.NewReader(body),
		map[string]string{
			"Authorization": "Bearer " + adminToken,
			"Content-Type":  "application/json",
		},
	)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("register JWKS config: want 201, got %d: %s", resp.StatusCode, raw)
	}
}

func TestE2E_M2M_ValidToken_AuthenticatesRequest(t *testing.T) {
	ts := newTestServer(t)

	issuer := "https://m2m-test-" + uuid.New().String()[:8] + ".example.com/"
	jwks := newJWKSServer(t, issuer)

	adminToken := ts.createAdminToken(t)
	ts.registerJWKSConfig(t, adminToken,
		"m2m-test-valid", issuer, jwks.URL, "https://api.test.internal")

	m2mToken := jwks.issueToken(t, "https://api.test.internal", time.Now().Add(time.Hour), []string{"read:servers"})

	resp := ts.do(t, http.MethodGet, "/api/v1/me", nil, map[string]string{
		"Authorization": "Bearer " + m2mToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_M2M_UnregisteredIssuer_Returns401(t *testing.T) {
	ts := newTestServer(t)

	// Issue a token from an issuer that is not registered in jwks_configs.
	unknownJWKS := newJWKSServer(t, "https://unknown-issuer.example.com/")
	token := unknownJWKS.issueToken(t, "any-audience", time.Now().Add(time.Hour), nil)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestE2E_M2M_ExpiredToken_Returns401(t *testing.T) {
	ts := newTestServer(t)

	issuer := "https://m2m-expired-" + uuid.New().String()[:8] + ".example.com/"
	jwks := newJWKSServer(t, issuer)

	adminToken := ts.createAdminToken(t)
	ts.registerJWKSConfig(t, adminToken,
		"m2m-test-expired", issuer, jwks.URL, "")

	expiredToken := jwks.issueToken(t, "", time.Now().Add(-time.Hour), nil) // already expired

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + expiredToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestE2E_M2M_DisabledConfig_Returns401(t *testing.T) {
	ts := newTestServer(t)

	issuer := fmt.Sprintf("https://m2m-disabled-%s.example.com/", uuid.New().String()[:8])
	jwks := newJWKSServer(t, issuer)

	// Insert the config directly via the store with is_enabled=false.
	if err := ts.jwksStore.Create(context.Background(), &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "m2m-disabled-" + uuid.New().String()[:8],
		Issuer:    issuer,
		JWKSURL:   jwks.URL,
		Audience:  "",
		IsEnabled: false,
	}); err != nil {
		t.Fatalf("create disabled JWKS config: %v", err)
	}

	token := jwks.issueToken(t, "", time.Now().Add(time.Hour), nil)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}
