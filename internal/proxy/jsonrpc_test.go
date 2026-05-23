package proxy

import (
	"encoding/json"
	"testing"
)

func TestParse_Request(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsRequest() {
		t.Error("expected IsRequest() true")
	}
	if m.IsNotification() {
		t.Error("expected IsNotification() false")
	}
	if m.Method != "tools/list" {
		t.Errorf("method = %q, want tools/list", m.Method)
	}
}

func TestParse_Notification(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","method":"notifications/progress","params":{}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsNotification() {
		t.Error("expected IsNotification() true")
	}
	if m.IDKey() != "" {
		t.Errorf("IDKey = %q, want empty", m.IDKey())
	}
}

func TestParse_Response(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1,"result":{"tools":[]}}`)
	m, err := Parse(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !m.IsResponse() {
		t.Error("expected IsResponse() true")
	}
}

func TestParse_BadVersion(t *testing.T) {
	raw := []byte(`{"jsonrpc":"1.0","id":1,"method":"tools/list"}`)
	_, err := Parse(raw)
	if err == nil {
		t.Error("expected error for version 1.0")
	}
}

func TestParse_MalformedJSON(t *testing.T) {
	_, err := Parse([]byte(`not json`))
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestAction(t *testing.T) {
	cases := []struct {
		method string
		want   string
	}{
		{"tools/list", "list"},
		{"resources/list", "list"},
		{"prompts/list", "list"},
		{"tools/call", "call"},
		{"resources/read", "read"},
		{"resources/subscribe", "read"},
		{"prompts/get", "read"},
		{"resources/write", "write"},
		{"logging/setLevel", "admin"},
		{"initialize", "connect"},
		{"ping", "connect"},
		{"unknown/method", "unknown"},
	}
	for _, c := range cases {
		if got := Action(c.method); got != c.want {
			t.Errorf("Action(%q) = %q, want %q", c.method, got, c.want)
		}
	}
}

func TestToolName(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"read_file","arguments":{}}}`)
	m, _ := Parse(raw)
	if m.ToolName() != "read_file" {
		t.Errorf("ToolName() = %q, want read_file", m.ToolName())
	}
}

func TestToolName_NotToolsCall(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	m, _ := Parse(raw)
	if m.ToolName() != "" {
		t.Errorf("ToolName() = %q, want empty", m.ToolName())
	}
}

func TestIDKey_StringID(t *testing.T) {
	raw := []byte(`{"jsonrpc":"2.0","id":"abc-123","method":"tools/list"}`)
	m, _ := Parse(raw)
	if m.IDKey() != `"abc-123"` {
		t.Errorf("IDKey() = %q, want %q", m.IDKey(), `"abc-123"`)
	}
}

func TestErrorResponse(t *testing.T) {
	id := json.RawMessage(`1`)
	b := ErrorResponse(id, CodeInternalError, "internal error")
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("ErrorResponse produced invalid JSON: %v", err)
	}
	if out["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", out["jsonrpc"])
	}
}
