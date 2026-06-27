package jwks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClient_GetKey_DoesNotFollowRedirect is a SSRF guard regression test: a
// JWKS endpoint that 302-redirects (e.g. toward an internal address) must not be
// followed. The client treats the redirect as the final response, so it never
// reaches the redirect target and fails to obtain a key.
func TestClient_GetKey_DoesNotFollowRedirect(t *testing.T) {
	key := newRSAKey(t)

	// The target a redirect would pivot to — serves a valid JWKS.
	var targetHit bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetHit = true
		w.Write(buildJWKS(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	defer target.Close()

	// The configured issuer endpoint 302-redirects to the target.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	c := NewClient(redirector.URL, time.Hour)
	if _, err := c.GetKey(context.Background(), "k1"); err == nil {
		t.Fatal("expected error — redirect must not be followed (SSRF guard)")
	}
	if targetHit {
		t.Error("redirect target was reached — client followed the redirect")
	}
}
