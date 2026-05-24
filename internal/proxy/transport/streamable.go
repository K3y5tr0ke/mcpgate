package transport

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// StreamableHTTP implements Transport using the MCP Streamable HTTP transport.
// One POST per JSON-RPC message; response body is the reply JSON.
type StreamableHTTP struct {
	endpoint string
	pool     *Pool
}

// NewStreamableHTTP creates a Streamable HTTP transport targeting endpoint.
func NewStreamableHTTP(endpoint string, pool *Pool) *StreamableHTTP {
	return &StreamableHTTP{endpoint: endpoint, pool: pool}
}

func (t *StreamableHTTP) Send(ctx context.Context, message []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.endpoint, bytes.NewReader(message))
	if err != nil {
		return nil, fmt.Errorf("streamable: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := t.pool.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("streamable: send: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("streamable: upstream returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("streamable: read response: %w", err)
	}
	return body, nil
}

func (t *StreamableHTTP) Stream(ctx context.Context, message []byte, messages chan<- []byte) error {
	resp, err := t.Send(ctx, message)
	if err != nil {
		return err
	}
	select {
	case messages <- resp:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (t *StreamableHTTP) Health(ctx context.Context) HealthStatus {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ping := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	_, err := t.Send(ctx, ping)
	return HealthStatus{
		Healthy: err == nil,
		Latency: time.Since(start),
		Err:     err,
	}
}

func (t *StreamableHTTP) Close() error { return nil }
