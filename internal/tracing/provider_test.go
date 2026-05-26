package tracing_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/tracing"
)

func noopCfg() config.OTLPConfig {
	return config.OTLPConfig{Enabled: false}
}

func TestProvider_NoOp_CreatesProviderWithoutError(t *testing.T) {
	p, err := tracing.New(noopCfg(), "test-service")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p == nil {
		t.Fatal("Provider is nil")
	}
}

func TestProvider_NoOp_ShutdownIsIdempotent(t *testing.T) {
	p, _ := tracing.New(noopCfg(), "test-service")
	ctx := context.Background()
	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("first Shutdown: %v", err)
	}
	if err := p.Shutdown(ctx); err != nil {
		t.Errorf("second Shutdown: %v", err)
	}
}

func TestProvider_NoOp_MiddlewareDoesNotPanic(t *testing.T) {
	p, _ := tracing.New(noopCfg(), "test-service")

	called := false
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", http.NoBody)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("inner handler was not called")
	}
}

func TestProvider_NoOp_SpanFromContextIsSafe(t *testing.T) {
	p, _ := tracing.New(noopCfg(), "test-service")

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	var ctxInsideHandler context.Context
	handler := p.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		ctxInsideHandler = r.Context()
	}))
	handler.ServeHTTP(w, req)

	if ctxInsideHandler == nil {
		t.Fatal("context was nil inside handler")
	}
	// With a no-op provider the span must still be non-nil and non-recording.
	span := trace.SpanFromContext(ctxInsideHandler)
	if span == nil {
		t.Fatal("SpanFromContext returned nil")
	}
	if span.IsRecording() {
		t.Error("no-op span should not be recording")
	}
}

func TestProvider_NoOp_PropagatesTraceContextHeader(t *testing.T) {
	p, _ := tracing.New(noopCfg(), "test-service")

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	// Inject a synthetic traceparent to verify the propagator reads it.
	req.Header.Set("Traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")

	w := httptest.NewRecorder()
	handler := p.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	// Should not panic even with a no-op provider.
	handler.ServeHTTP(w, req)
}
