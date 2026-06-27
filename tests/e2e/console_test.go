//go:build integration

package e2e_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// TestE2E_Console_IndexHTMLServed verifies that /console/dashboard (and any
// other /console/* path) serves the SPA index.html so client-side routing works.
func TestE2E_Console_IndexHTMLServed(t *testing.T) {
	ts := newTestServer(t)

	paths := []string{"/console/dashboard", "/console/servers", "/console/settings"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get(ts.url + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close() //nolint:errcheck

			if resp.StatusCode != http.StatusOK {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
			}

			ct := resp.Header.Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type: got %q, want text/html prefix", ct)
			}
		})
	}
}

// TestE2E_Console_LoginPageServed verifies that /console/login serves the SPA
// shell (200 + text/html). This is the unauthenticated landing route the 401
// redirect points at; a routing regression here would break local admin login.
func TestE2E_Console_LoginPageServed(t *testing.T) {
	ts := newTestServer(t)

	resp, err := http.Get(ts.url + "/console/login")
	if err != nil {
		t.Fatalf("GET /console/login: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q, want text/html prefix", ct)
	}
}

// TestE2E_SecurityHeaders_OnConsoleAndAdmin verifies the securityHeaders
// middleware is wired onto the browser-facing surfaces (console SPA + admin API)
// and is NOT applied to the /mcp proxy path (whose responses pass through verbatim).
func TestE2E_SecurityHeaders_OnConsoleAndAdmin(t *testing.T) {
	ts := newTestServer(t)
	adminToken := ts.createAdminToken(t)

	wantHeaders := map[string]string{
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}

	assertSecure := func(t *testing.T, h http.Header) {
		t.Helper()
		for k, v := range wantHeaders {
			if got := h.Get(k); got != v {
				t.Errorf("%s = %q, want %q", k, got, v)
			}
		}
		if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
			t.Errorf("CSP missing or weak: %q", csp)
		}
	}

	t.Run("console", func(t *testing.T) {
		resp, err := http.Get(ts.url + "/console/dashboard")
		if err != nil {
			t.Fatalf("GET console: %v", err)
		}
		defer resp.Body.Close() //nolint:errcheck
		assertSecure(t, resp.Header)
	})

	t.Run("admin API", func(t *testing.T) {
		resp := ts.do(t, http.MethodGet, "/api/v1/admin/servers", nil,
			map[string]string{"Authorization": "Bearer " + adminToken})
		defer resp.Body.Close() //nolint:errcheck
		assertSecure(t, resp.Header)
	})

	t.Run("proxy path excluded", func(t *testing.T) {
		// Unauthenticated → 401, but the point is the /mcp group has no
		// securityHeaders middleware, so no CSP is added to proxy responses.
		resp := ts.do(t, http.MethodPost, "/mcp/whatever/message",
			newJSONReader([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)),
			map[string]string{"Content-Type": "application/json"})
		defer resp.Body.Close() //nolint:errcheck
		if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
			t.Errorf("proxy response should not carry CSP, got: %q", csp)
		}
	})
}

// TestE2E_Console_RootRedirectsToDashboard verifies that /console redirects to
// /console/dashboard rather than returning 404 or a blank page.
func TestE2E_Console_RootRedirectsToDashboard(t *testing.T) {
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	ts := newTestServer(t)

	resp, err := client.Get(ts.url + "/console")
	if err != nil {
		t.Fatalf("GET /console: %v", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("want 302, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if loc != "/console/dashboard" {
		t.Errorf("Location: got %q, want /console/dashboard", loc)
	}
}
