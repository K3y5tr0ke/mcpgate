package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityHeaders_SetsAllHeaders(t *testing.T) {
	var called bool
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/console/dashboard", http.NoBody))

	if !called {
		t.Fatal("next handler was not called")
	}

	want := map[string]string{
		"X-Frame-Options":           "DENY",
		"X-Content-Type-Options":    "nosniff",
		"Referrer-Policy":           "no-referrer",
		"Strict-Transport-Security": "max-age=31536000; includeSubDomains",
	}
	for k, v := range want {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}

	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header not set")
	}
	// Key directives the console depends on / that harden the surface.
	for _, want := range []string{
		"default-src 'self'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"style-src 'self' 'unsafe-inline'", // React/Recharts runtime inline styles
		"base-uri 'self'",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP missing %q; got: %s", want, csp)
		}
	}
	// script-src must NOT permit unsafe-inline (assets are external, hashed).
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Errorf("script-src should not allow 'unsafe-inline'; got: %s", csp)
	}
}

// TestSecurityHeaders_PreservesStatus ensures the middleware does not interfere
// with the wrapped handler's status code (e.g. a 401/403 from auth still flows).
func TestSecurityHeaders_PreservesStatus(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/servers", http.NoBody))

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusForbidden)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers should be set even on error responses")
	}
}
