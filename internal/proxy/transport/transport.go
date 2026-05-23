package transport

import (
	"context"
	"time"
)

// HealthStatus describes the current connectivity state of a transport.
type HealthStatus struct {
	Healthy bool
	Latency time.Duration
	Err     error
}

// Transport is the abstraction over a connection to an upstream MCP server.
// Each implementation handles one wire format (Streamable HTTP, SSE, stdio).
type Transport interface {
	// Send delivers a single JSON-RPC message and returns the response bytes.
	Send(ctx context.Context, message []byte) ([]byte, error)

	// Stream delivers a JSON-RPC message and pushes response/notification bytes
	// to the provided channel until the context is cancelled.
	Stream(ctx context.Context, message []byte, messages chan<- []byte) error

	// Health returns the current health of the upstream connection.
	Health(ctx context.Context) HealthStatus

	// Close releases all resources associated with the transport.
	Close() error
}
