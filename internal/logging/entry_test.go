package logging

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
)

type mockWriter struct {
	status int
	bytes  int
}

func (m *mockWriter) Status() int       { return m.status }
func (m *mockWriter) BytesWritten() int { return m.bytes }

func TestBuildEntry_WithIdentityAndMessage(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp/myserver/message", http.NoBody)
	identity := auth.UserIdentity{
		UserID:    uuid.New(),
		Email:     "alice@example.com",
		Roles:     []string{"developer"},
		SessionID: uuid.New(),
	}
	r = r.WithContext(auth.WithIdentity(r.Context(), identity))

	msg := &proxy.Message{JSONRPC: "2.0", Method: "tools/list"}
	r = r.WithContext(proxy.WithMessage(r.Context(), msg))

	ww := &mockWriter{status: 200, bytes: 512}
	start := time.Now().Add(-10 * time.Millisecond)

	entry := BuildEntry(r, ww, uuid.Nil, start)

	if entry.UserEmail != "alice@example.com" {
		t.Errorf("UserEmail: got %q, want alice@example.com", entry.UserEmail)
	}
	if entry.MCPMethod != "tools/list" {
		t.Errorf("MCPMethod: got %q, want tools/list", entry.MCPMethod)
	}
	if entry.Status != "success" {
		t.Errorf("Status: got %q, want success", entry.Status)
	}
	if entry.RBACDecision != "allow" {
		t.Errorf("RBACDecision: got %q, want allow", entry.RBACDecision)
	}
	if entry.LatencyMS < 0 {
		t.Errorf("LatencyMS negative: %d", entry.LatencyMS)
	}
	if entry.ResponseSize != 512 {
		t.Errorf("ResponseSize: got %d, want 512", entry.ResponseSize)
	}
}

func TestBuildEntry_NilMessage(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/mcp/myserver/sse", http.NoBody)

	ww := &mockWriter{status: 200, bytes: 0}
	entry := BuildEntry(r, ww, uuid.Nil, time.Now())

	if entry.MCPMethod != "" {
		t.Errorf("MCPMethod should be empty for GET, got %q", entry.MCPMethod)
	}
	if entry.Status != "success" {
		t.Errorf("Status: got %q, want success", entry.Status)
	}
}

func TestBuildEntry_ForbiddenStatus(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp/myserver/message", http.NoBody)
	ww := &mockWriter{status: 403, bytes: 0}
	entry := BuildEntry(r, ww, uuid.Nil, time.Now())

	if entry.Status != "denied" {
		t.Errorf("Status: got %q, want denied", entry.Status)
	}
	if entry.RBACDecision != "deny" {
		t.Errorf("RBACDecision: got %q, want deny", entry.RBACDecision)
	}
}

func TestBuildEntry_ErrorStatus(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/mcp/myserver/message", http.NoBody)
	ww := &mockWriter{status: 502, bytes: 0}
	entry := BuildEntry(r, ww, uuid.Nil, time.Now())

	if entry.Status != "error" {
		t.Errorf("Status: got %q, want error", entry.Status)
	}
	if entry.ErrorCode == nil || *entry.ErrorCode != 502 {
		t.Errorf("ErrorCode: got %v, want 502", entry.ErrorCode)
	}
}

func TestExtractClientIP_RemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.RemoteAddr = "192.168.1.1:54321"

	ip := extractClientIP(r)
	if ip != "192.168.1.1" {
		t.Errorf("got %q, want 192.168.1.1", ip)
	}
}

func TestExtractClientIP_XForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.Header.Set("X-Forwarded-For", "10.0.0.1")

	ip := extractClientIP(r)
	if ip != "10.0.0.1" {
		t.Errorf("got %q, want 10.0.0.1", ip)
	}
}
