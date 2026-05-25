//go:build integration

package e2e_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestE2E_NoToken_Returns401(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, nil)
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestE2E_InvalidToken_Returns401(t *testing.T) {
	ts := newTestServer(t)

	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer not.a.real.token",
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", resp.StatusCode)
	}
}

func TestE2E_ValidSessionToken_Returns200(t *testing.T) {
	ts := newTestServer(t)

	token := ts.createAdminToken(t)
	resp := ts.do(t, http.MethodGet, "/api/v1/admin/jwks/configs", nil, map[string]string{
		"Authorization": "Bearer " + token,
	})
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("want 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestE2E_BruteForce_SAMLLogin_Returns429(t *testing.T) {
	ts := newTestServer(t)

	// Post garbage to the ACS endpoint — brute force guard fires before SAML parsing.
	// MaxAttempts is 5 in buildTestConfig; the 6th request must be 429.
	var lastStatus int
	for i := range 7 {
		body := strings.NewReader("SAMLResponse=notvalidbase64")
		req, _ := http.NewRequest(http.MethodPost, ts.url+"/auth/saml/acs", body)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		lastStatus = resp.StatusCode
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			return // guard fired as expected
		}
	}
	t.Errorf("expected 429 after 6 ACS requests, last status was %d", lastStatus)
}
