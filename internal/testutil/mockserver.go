package testutil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"
)

// MockMCPServer is a minimal httptest.Server that speaks Streamable HTTP MCP.
// It handles tools/list and tools/call, supports configurable latency and error
// injection, and exposes counters for assertions in tests.
type MockMCPServer struct {
	Server   *httptest.Server
	requests atomic.Int64

	latency   time.Duration
	tools     []map[string]any
	forceFail bool
}

// MockMCPOption configures a MockMCPServer.
type MockMCPOption func(*MockMCPServer)

// WithLatency adds artificial delay to every response.
func WithLatency(d time.Duration) MockMCPOption {
	return func(s *MockMCPServer) { s.latency = d }
}

// WithTools registers the tool list returned by tools/list.
func WithTools(tools []map[string]any) MockMCPOption {
	return func(s *MockMCPServer) { s.tools = tools }
}

// WithForceFail makes the server return a JSON-RPC internal error for every request.
func WithForceFail() MockMCPOption {
	return func(s *MockMCPServer) { s.forceFail = true }
}

// NewMockMCPServer creates and starts a test server. Call Close() when done.
func NewMockMCPServer(opts ...MockMCPOption) *MockMCPServer {
	s := &MockMCPServer{
		tools: []map[string]any{
			{"name": "echo", "description": "Echo the input", "inputSchema": map[string]any{"type": "object"}},
		},
	}
	for _, o := range opts {
		o(s)
	}
	s.Server = httptest.NewServer(s)
	return s
}

// URL returns the base URL of the mock server.
func (s *MockMCPServer) URL() string { return s.Server.URL }

// RequestCount returns the number of requests received.
func (s *MockMCPServer) RequestCount() int64 { return s.requests.Load() }

// Close shuts the underlying httptest.Server.
func (s *MockMCPServer) Close() { s.Server.Close() }

func (s *MockMCPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)

	if s.latency > 0 {
		time.Sleep(s.latency)
	}

	var req struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
		Params  json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error")
		return
	}

	if s.forceFail {
		writeRPCError(w, req.ID, -32603, "forced failure")
		return
	}

	switch req.Method {
	case "initialize":
		writeRPCResult(w, req.ID, map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "mock-mcp", "version": "0.1.0"},
		})

	case "tools/list":
		writeRPCResult(w, req.ID, map[string]any{"tools": s.tools})

	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		writeRPCResult(w, req.ID, map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "called " + p.Name},
			},
		})

	case "ping":
		writeRPCResult(w, req.ID, map[string]any{})

	default:
		writeRPCError(w, req.ID, -32601, "method not found")
	}
}

func writeRPCResult(w http.ResponseWriter, id json.RawMessage, result any) {
	resp := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}

func writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": msg},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp) //nolint:errcheck
}
