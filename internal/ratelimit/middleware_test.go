package ratelimit_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/metrics"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/ratelimit"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// mockLimiter is a test double for store.RateLimiter.
type mockLimiter struct {
	result store.RateLimitResult
	err    error
}

func (m *mockLimiter) Allow(_ context.Context, _ string, _ store.RateLimit) (store.RateLimitResult, error) {
	return m.result, m.err
}

// withIdentity injects a UserIdentity into the request context.
func withIdentity(r *http.Request) *http.Request {
	id := auth.UserIdentity{
		UserID:    uuid.New(),
		Email:     "user@example.com",
		SessionID: uuid.New(),
	}
	return r.WithContext(auth.WithIdentity(r.Context(), id))
}

func enabledCfg(rpm int) config.RateLimitingConfig {
	return config.RateLimitingConfig{
		Enabled: true,
		Default: config.RateLimitDefaults{RPM: rpm},
	}
}

func TestMiddleware_Disabled_PassesThrough(t *testing.T) {
	mw := ratelimit.Middleware(nil, config.RateLimitingConfig{Enabled: false}, nil, slog.Default())
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("inner handler should be called when middleware is disabled")
	}
}

func TestMiddleware_Allow_CallsNext(t *testing.T) {
	lim := &mockLimiter{result: store.RateLimitResult{Allowed: true}}
	mw := ratelimit.Middleware(lim, enabledCfg(10), nil, slog.Default())
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("inner handler should be called when request is allowed")
	}
}

func TestMiddleware_Denied_Returns429(t *testing.T) {
	lim := &mockLimiter{result: store.RateLimitResult{
		Allowed:    false,
		RetryAfter: 30 * time.Second,
	}}
	mw := ratelimit.Middleware(lim, enabledCfg(5), nil, slog.Default())
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	if called {
		t.Error("inner handler must not be called when rate limited")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", w.Code)
	}
	if w.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header must be set")
	}
}

func TestMiddleware_Denied_JSONRPCErrorBody(t *testing.T) {
	lim := &mockLimiter{result: store.RateLimitResult{Allowed: false, RetryAfter: 5 * time.Second}}
	mw := ratelimit.Middleware(lim, enabledCfg(1), nil, slog.Default())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	r = r.WithContext(proxy.WithMessage(r.Context(), &proxy.Message{Method: "tools/list"}))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	body, _ := io.ReadAll(w.Body)
	var resp map[string]any
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("response body is not valid JSON: %v\nbody: %s", err, body)
	}
	if resp["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", resp["jsonrpc"])
	}
	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("error field missing or wrong type: %v", resp["error"])
	}
	if errObj["code"] != float64(-32000) {
		t.Errorf("error.code = %v, want -32000", errObj["code"])
	}
}

func TestMiddleware_LimiterError_FailOpen(t *testing.T) {
	lim := &mockLimiter{err: errors.New("redis down")}
	mw := ratelimit.Middleware(lim, enabledCfg(5), nil, slog.Default())
	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if !called {
		t.Error("should fail open when limiter returns an error")
	}
}

func TestMiddleware_Denied_IncreasesMetric(t *testing.T) {
	lim := &mockLimiter{result: store.RateLimitResult{Allowed: false, RetryAfter: time.Second}}
	col := metrics.NewCollector()
	mw := ratelimit.Middleware(lim, enabledCfg(1), col, slog.Default())
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))

	r := withIdentity(httptest.NewRequest(http.MethodPost, "/mcp/srv/message", http.NoBody))
	handler.ServeHTTP(httptest.NewRecorder(), r)
	handler.ServeHTTP(httptest.NewRecorder(), r)

	req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	w := httptest.NewRecorder()
	col.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Body)
	if !contains(string(body), "mcpgate_rate_limit_hits_total") {
		t.Error("rate limit hits metric not found in output")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || s != "" && findSubstring(s, sub))
}

func findSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
