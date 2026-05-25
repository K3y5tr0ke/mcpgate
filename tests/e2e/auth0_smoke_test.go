//go:build integration

package e2e_test

// Auth0 M2M smoke test — exercises the full request path using a real Auth0
// client_credentials token against a locally-running test server.
//
// Skipped unless all four AUTH0_* env vars are present. Set them by exporting:
//
//	export AUTH0_M2M_CLIENT_ID=...
//	export AUTH0_M2M_CLIENT_SECRET=...
//	export AUTH0_M2M_TOKEN_URL=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/oauth/token
//	export AUTH0_M2M_AUDIENCE=https://api.mcpgate.internal
//	export AUTH0_JWKS_URL=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/.well-known/jwks.json
//	export AUTH0_ISSUER=https://dev-0ziq1qjvgrwq4w2g.us.auth0.com/

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
)

// auth0Env holds the Auth0 credentials read from environment variables.
type auth0Env struct {
	clientID     string
	clientSecret string
	tokenURL     string
	audience     string
	jwksURL      string
	issuer       string
}

// loadAuth0Env reads Auth0 credentials from env vars.
// Returns nil and skips the test if any required var is missing.
func loadAuth0Env(t *testing.T) *auth0Env {
	t.Helper()
	e := &auth0Env{
		clientID:     os.Getenv("AUTH0_M2M_CLIENT_ID"),
		clientSecret: os.Getenv("AUTH0_M2M_CLIENT_SECRET"),
		tokenURL:     os.Getenv("AUTH0_M2M_TOKEN_URL"),
		audience:     os.Getenv("AUTH0_M2M_AUDIENCE"),
		jwksURL:      os.Getenv("AUTH0_JWKS_URL"),
		issuer:       os.Getenv("AUTH0_ISSUER"),
	}
	if e.clientID == "" || e.clientSecret == "" || e.tokenURL == "" ||
		e.audience == "" || e.jwksURL == "" || e.issuer == "" {
		t.Skip("AUTH0_* env vars not set — skipping Auth0 smoke test")
	}
	return e
}

// fetchAuth0Token calls the Auth0 token endpoint and returns the access_token.
func fetchAuth0Token(t *testing.T, e *auth0Env) string {
	t.Helper()

	body, _ := json.Marshal(map[string]string{
		"client_id":     e.clientID,
		"client_secret": e.clientSecret,
		"audience":      e.audience,
		"grant_type":    "client_credentials",
	})

	resp, err := http.Post(e.tokenURL, "application/json", bytes.NewReader(body)) //nolint:gosec,noctx
	if err != nil {
		t.Fatalf("fetch Auth0 token: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("Auth0 token endpoint: want 200, got %d: %s", resp.StatusCode, raw)
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if result.AccessToken == "" {
		t.Fatal("Auth0 returned empty access_token")
	}
	return result.AccessToken
}

func TestE2E_Auth0_M2M_ClientCredentials_AuthenticatesRequest(t *testing.T) {
	auth0 := loadAuth0Env(t) // skips if env vars absent

	ts := newTestServer(t)

	// Register Auth0 as a trusted JWKS issuer via the admin API.
	adminToken := ts.createAdminToken(t)
	ts.registerJWKSConfig(t, adminToken,
		"auth0-smoke",
		auth0.issuer,
		auth0.jwksURL,
		auth0.audience,
	)

	// Get a real token from Auth0.
	m2mToken := fetchAuth0Token(t, auth0)

	// Use it against the protected admin endpoint.
	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + m2mToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("Auth0 M2M: want 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_Auth0_M2M_WrongAudience_Returns401(t *testing.T) {
	auth0 := loadAuth0Env(t)

	ts := newTestServer(t)

	// Register Auth0 but with a different expected audience.
	adminToken := ts.createAdminToken(t)
	ts.registerJWKSConfig(t, adminToken,
		"auth0-wrong-aud",
		auth0.issuer,
		auth0.jwksURL,
		"https://wrong-audience.example.com", // does not match the token's aud
	)

	m2mToken := fetchAuth0Token(t, auth0)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + m2mToken,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong audience: want 401, got %d", resp.StatusCode)
	}
}
