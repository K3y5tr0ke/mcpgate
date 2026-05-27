package common

import (
	"encoding/json"
	"fmt"
	"time"
)

// Request is a parsed JSON-RPC 2.0 message (request or notification).
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// IsNotification reports whether req is a JSON-RPC notification (method present,
// id absent). Per spec, servers must not send a response to notifications.
// However, the mcpgate stdio transport always expects a response, so we still
// write one — with the id field omitted so the transport's pending-key lookup
// (using "" for absent ids) succeeds.
func (r *Request) IsNotification() bool {
	return r.Method != "" && len(r.ID) == 0
}

// Handler dispatches MCP JSON-RPC methods and produces spec-compliant responses.
type Handler struct {
	// ServerName is embedded in initialize/serverInfo and resource content.
	// Set from the MCP_SERVER_NAME environment variable by each server's main.
	ServerName string
}

// NewHandler creates a Handler with the given server name.
func NewHandler(serverName string) *Handler {
	return &Handler{ServerName: serverName}
}

// Handle dispatches req and returns the serialised JSON-RPC response bytes.
// The response id field is omitted (not null) for notifications so that the
// mcpgate stdio transport can match it via the empty-string pending key.
func (h *Handler) Handle(req *Request) []byte {
	result, code, msg := h.Dispatch(req.Method, req.Params)
	return buildResponse(req.ID, result, code, msg)
}

// Dispatch maps an MCP JSON-RPC method to its handler.
// Returns (result, 0, "") on success or (nil, errorCode, errorMsg) on error.
func (h *Handler) Dispatch(method string, params json.RawMessage) (any, int, string) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{"subscribe": false, "listChanged": false},
				"prompts":   map[string]any{"listChanged": false},
			},
			"serverInfo": map[string]any{
				"name":    h.ServerName,
				"version": "0.1.0",
			},
		}, 0, ""

	case "initialized":
		// Client lifecycle notification — acknowledge with an empty result so the
		// proxy's tr.Send() can unblock. Well-behaved clients ignore this response.
		return map[string]any{}, 0, ""

	case "ping":
		return map[string]any{}, 0, ""

	case "tools/list":
		return map[string]any{"tools": toolList()}, 0, ""

	case "tools/call":
		return h.callTool(params)

	case "resources/list":
		return map[string]any{
			"resources": []map[string]any{
				{
					"uri":         "file://greeting.txt",
					"name":        "greeting",
					"mimeType":    "text/plain",
					"description": "A greeting from this MCP server",
				},
			},
		}, 0, ""

	case "resources/read":
		if len(params) == 0 {
			return nil, -32602, "invalid params: uri is required"
		}
		var p struct {
			URI string `json:"uri"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.URI == "" {
			return nil, -32602, "invalid params: uri is required"
		}
		if p.URI != "file://greeting.txt" {
			return nil, -32602, fmt.Sprintf("unknown resource %q", p.URI)
		}
		return map[string]any{
			"contents": []map[string]any{
				{
					"uri":      "file://greeting.txt",
					"mimeType": "text/plain",
					"text":     fmt.Sprintf("Hello from %s MCP server", h.ServerName),
				},
			},
		}, 0, ""

	case "prompts/list":
		return map[string]any{
			"prompts": []map[string]any{
				{
					"name":        "system-prompt",
					"description": "A system prompt for the example MCP server",
				},
			},
		}, 0, ""

	case "prompts/get":
		if len(params) == 0 {
			return nil, -32602, "invalid params: name is required"
		}
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
			return nil, -32602, "invalid params: name is required"
		}
		if p.Name != "system-prompt" {
			return nil, -32602, fmt.Sprintf("unknown prompt %q", p.Name)
		}
		return map[string]any{
			"description": "System prompt for the example MCP server",
			"messages": []map[string]any{
				{
					"role": "user",
					"content": map[string]any{
						"type": "text",
						"text": fmt.Sprintf("You are connected to the %s example MCP server.", h.ServerName),
					},
				},
			},
		}, 0, ""

	default:
		return nil, -32601, fmt.Sprintf("method not found: %s", method)
	}
}

func (h *Handler) callTool(params json.RawMessage) (any, int, string) {
	if len(params) == 0 {
		return nil, -32602, "invalid params: name is required"
	}
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Name == "" {
		return nil, -32602, "invalid params: name is required"
	}
	if p.Arguments == nil {
		p.Arguments = map[string]any{}
	}

	switch p.Name {
	case "echo":
		msg, _ := p.Arguments["message"].(string)
		return textContent(msg), 0, ""

	case "add":
		a, _ := p.Arguments["a"].(float64)
		b, _ := p.Arguments["b"].(float64)
		return textContent(fmt.Sprintf("%g", a+b)), 0, ""

	case "slow_echo":
		msg, _ := p.Arguments["message"].(string)
		delayMs, _ := p.Arguments["delay_ms"].(float64)
		if delayMs < 0 {
			delayMs = 0
		}
		if delayMs > 30_000 {
			delayMs = 30_000 // cap to prevent abuse in shared test environments
		}
		if delayMs > 0 {
			time.Sleep(time.Duration(delayMs) * time.Millisecond)
		}
		return textContent(msg), 0, ""

	default:
		return nil, -32602, fmt.Sprintf("unknown tool %q", p.Name)
	}
}

// toolList returns the static tool definitions for all three tools.
func toolList() []map[string]any {
	return []map[string]any{
		{
			"name":        "echo",
			"description": "Echo the input message back",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "The message to echo back",
					},
				},
				"required": []string{"message"},
			},
		},
		{
			"name":        "add",
			"description": "Add two numbers together",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "number", "description": "First number"},
					"b": map[string]any{"type": "number", "description": "Second number"},
				},
				"required": []string{"a", "b"},
			},
		},
		{
			"name":        "slow_echo",
			"description": "Echo the message after a configurable delay. Use for testing timeouts and circuit breakers.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"message": map[string]any{
						"type":        "string",
						"description": "The message to echo back",
					},
					"delay_ms": map[string]any{
						"type":        "number",
						"description": "Delay in milliseconds before responding (capped at 30000)",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

// textContent wraps text in an MCP tool result content array.
func textContent(text string) map[string]any {
	return map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
	}
}

// ParseErrorResponse returns a JSON-RPC -32700 parse error with a null id.
// Used when the incoming bytes cannot be decoded as valid JSON-RPC.
func ParseErrorResponse() []byte {
	return buildResponse(json.RawMessage("null"), nil, -32700, "parse error")
}

// buildResponse serialises a JSON-RPC 2.0 response.
// When id is nil (notification), the id field is omitted entirely so the
// mcpgate stdio transport can match it via the empty-string pending key.
func buildResponse(id json.RawMessage, result any, errCode int, errMsg string) []byte {
	type rpcErr struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	type resp struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id,omitempty"`
		Result  any             `json:"result,omitempty"`
		Error   *rpcErr         `json:"error,omitempty"`
	}
	r := resp{JSONRPC: "2.0", ID: id}
	if errCode != 0 {
		r.Error = &rpcErr{Code: errCode, Message: errMsg}
	} else {
		r.Result = result
	}
	b, _ := json.Marshal(r)
	return b
}
