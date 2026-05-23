package proxy

import (
	"encoding/json"
	"fmt"
)

// Message is the minimal parsed form of a JSON-RPC 2.0 message.
// We only decode fields the proxy needs; the raw body is passed through unchanged.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`      // string, number, or null
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError is the JSON-RPC error object.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// IsRequest reports whether the message is a request (has a method).
func (m *Message) IsRequest() bool { return m.Method != "" }

// IsNotification reports whether the message is a notification (has method, no id).
func (m *Message) IsNotification() bool { return m.Method != "" && len(m.ID) == 0 }

// IsResponse reports whether the message is a response (has result or error, no method).
func (m *Message) IsResponse() bool { return m.Method == "" }

// IDKey returns a string key suitable for map lookups, or "" for notifications.
func (m *Message) IDKey() string {
	if len(m.ID) == 0 {
		return ""
	}
	return string(m.ID)
}

// ToolName extracts the tool name from a tools/call params object, returning ""
// if the params are absent or malformed.
func (m *Message) ToolName() string {
	if m.Method != "tools/call" || len(m.Params) == 0 {
		return ""
	}
	var p struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return ""
	}
	return p.Name
}

// Action maps an MCP method name to the RBAC action category used for
// enforcement decisions.
func Action(method string) string {
	switch method {
	case "tools/list", "resources/list", "prompts/list":
		return "list"
	case "tools/call":
		return "call"
	case "resources/read", "resources/subscribe", "prompts/get":
		return "read"
	case "resources/write":
		return "write"
	case "logging/setLevel":
		return "admin"
	case "initialize", "ping":
		return "connect"
	default:
		return "unknown"
	}
}

// Parse decodes raw JSON into a Message, validating that jsonrpc == "2.0".
func Parse(data []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("jsonrpc: malformed JSON: %w", err)
	}
	if m.JSONRPC != "2.0" {
		return nil, fmt.Errorf("jsonrpc: unsupported version %q (want 2.0)", m.JSONRPC)
	}
	return &m, nil
}

// ErrorResponse builds a JSON-RPC error response body for the given id and error.
// id may be nil (for parse errors where the id is unknown).
func ErrorResponse(id json.RawMessage, code int, message string) []byte {
	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    code,
			"message": message,
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

// Standard JSON-RPC 2.0 error codes.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
)
