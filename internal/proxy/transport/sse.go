package transport

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// SSE implements Transport using the MCP SSE transport.
// Requests are POSTed to the message endpoint; the server pushes responses
// back via a long-lived SSE stream opened against the SSE endpoint.
//
// For Send(), we use a simplified approach: POST the message to the message
// endpoint and parse the first data event from the response stream.
type SSE struct {
	sseEndpoint     string
	messageEndpoint string
	pool            *Pool
}

// NewSSE creates an SSE transport. sseEndpoint is the GET endpoint for the
// event stream; messageEndpoint is the POST endpoint for sending messages.
func NewSSE(sseEndpoint, messageEndpoint string, pool *Pool) *SSE {
	return &SSE{
		sseEndpoint:     sseEndpoint,
		messageEndpoint: messageEndpoint,
		pool:            pool,
	}
}

func (t *SSE) Send(ctx context.Context, message []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.messageEndpoint, bytes.NewReader(message))
	if err != nil {
		return nil, fmt.Errorf("sse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := t.pool.Do(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("sse: send: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sse: upstream returned %d", resp.StatusCode)
	}

	// If the response is SSE, read the first data event.
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return readFirstSSEData(resp.Body)
	}

	// Otherwise treat as plain JSON response.
	return io.ReadAll(resp.Body)
}

func (t *SSE) Stream(ctx context.Context, message []byte, messages chan<- []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.sseEndpoint, http.NoBody)
	if err != nil {
		return fmt.Errorf("sse: open stream: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.pool.Do(ctx, req)
	if err != nil {
		return fmt.Errorf("sse: open stream: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		select {
		case messages <- data:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return scanner.Err()
}

func (t *SSE) Health(ctx context.Context) HealthStatus {
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

func (t *SSE) Close() error { return nil }

// readFirstSSEData reads lines from r until it finds one starting with "data:"
// and returns the payload bytes.
func readFirstSSEData(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			return []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("sse: read stream: %w", err)
	}
	return nil, fmt.Errorf("sse: stream ended without data event")
}
