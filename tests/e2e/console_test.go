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
