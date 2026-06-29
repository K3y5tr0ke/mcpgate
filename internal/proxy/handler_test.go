//go:build integration

package proxy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

// staticServerStore is a minimal in-memory ServerStore for handler tests.
type staticServerStore struct {
	servers map[string]*store.MCPServer
}

func (s *staticServerStore) Create(_ context.Context, _ *store.MCPServer) error { return nil }
func (s *staticServerStore) GetByID(_ context.Context, _ uuid.UUID) (*store.MCPServer, error) {
	return nil, store.ErrNotFound
}
func (s *staticServerStore) GetByName(_ context.Context, name string) (*store.MCPServer, error) {
	if srv, ok := s.servers[name]; ok {
		return srv, nil
	}
	return nil, store.ErrNotFound
}
func (s *staticServerStore) List(_ context.Context) ([]store.MCPServer, error)  { return nil, nil }
func (s *staticServerStore) Update(_ context.Context, _ *store.MCPServer) error { return nil }
func (s *staticServerStore) Delete(_ context.Context, _ uuid.UUID) error        { return nil }
func (s *staticServerStore) UpdateTools(_ context.Context, _ uuid.UUID, _ []store.ServerTool) error {
	return nil
}

func TestHandler_StreamableHTTP_RoundTrip(t *testing.T) {
	mock := testutil.NewMockMCPServer()
	defer mock.Close()

	servers := &staticServerStore{
		servers: map[string]*store.MCPServer{
			"test-server": {
				Name:      "test-server",
				Transport: "streamable_http",
				Config:    map[string]any{"endpoint": mock.URL()},
				IsEnabled: true,
			},
		},
	}

	cfg := config.ProxyConfig{
		DefaultTimeout: 5 * time.Second,
		MaxRequestSize: 1 << 20,
		ConnectionPool: config.ConnectionPoolConfig{MaxPerServer: 10},
	}

	logger := slog.Default()
	handler := proxy.NewHandler(cfg, servers, logger)

	r := chi.NewRouter()
	r.Post("/mcp/{serverName}/message", handler.HandleStreamableHTTP)

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/test-server/message", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, ok := resp["result"]; !ok {
		t.Errorf("response missing result field: %v", resp)
	}

	if mock.RequestCount() != 1 {
		t.Errorf("mock received %d requests, want 1", mock.RequestCount())
	}
}

func TestHandler_UnknownServer(t *testing.T) {
	servers := &staticServerStore{servers: map[string]*store.MCPServer{}}
	cfg := config.ProxyConfig{DefaultTimeout: 5 * time.Second, MaxRequestSize: 1 << 20}
	handler := proxy.NewHandler(cfg, servers, slog.Default())

	r := chi.NewRouter()
	r.Post("/mcp/{serverName}/message", handler.HandleStreamableHTTP)

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/nonexistent/message", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]any
	json.NewDecoder(rec.Body).Decode(&resp) //nolint:errcheck
	if resp["error"] == nil {
		t.Errorf("expected error field in JSON-RPC response for unknown server")
	}
}

func TestHandler_ForceFail(t *testing.T) {
	mock := testutil.NewMockMCPServer(testutil.WithForceFail())
	defer mock.Close()

	servers := &staticServerStore{
		servers: map[string]*store.MCPServer{
			"fail-server": {
				Name:      "fail-server",
				Transport: "streamable_http",
				Config:    map[string]any{"endpoint": mock.URL()},
				IsEnabled: true,
			},
		},
	}
	cfg := config.ProxyConfig{DefaultTimeout: 5 * time.Second, MaxRequestSize: 1 << 20,
		ConnectionPool: config.ConnectionPoolConfig{MaxPerServer: 10}}
	handler := proxy.NewHandler(cfg, servers, slog.Default())

	r := chi.NewRouter()
	r.Post("/mcp/{serverName}/message", handler.HandleStreamableHTTP)

	reqBody := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	req := httptest.NewRequest(http.MethodPost, "/mcp/fail-server/message", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	// Force-fail server returns a JSON-RPC error response, which the proxy should pass through.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
