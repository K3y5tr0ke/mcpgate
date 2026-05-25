package proxy

import "context"

type contextKey string

const messageKey contextKey = "mcp_message"

// WithMessage stores the parsed JSON-RPC message in ctx.
// Called by the RBAC Authorize middleware after parsing the request body.
func WithMessage(ctx context.Context, msg *Message) context.Context {
	return context.WithValue(ctx, messageKey, msg)
}

// MessageFromContext retrieves the parsed message, or nil if not set.
func MessageFromContext(ctx context.Context) *Message {
	m, _ := ctx.Value(messageKey).(*Message)
	return m
}
