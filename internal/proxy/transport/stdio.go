package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// Stdio implements Transport by spawning a child process and communicating
// via stdin/stdout using newline-delimited JSON-RPC 2.0.
//
// Each Send() writes a message to stdin and blocks until the matching response
// arrives (matched by JSON-RPC id). A background readLoop goroutine routes
// responses to the waiting callers via per-id channels stored in pending.
type Stdio struct {
	command string
	args    []string

	procMu  sync.Mutex // guards proc lifecycle
	proc    *exec.Cmd
	stdin   io.WriteCloser
	writeMu sync.Mutex // serialises stdin writes

	pending sync.Map // idKey(string) -> chan []byte

	closeOnce sync.Once
	done      chan struct{}
}

// NewStdio creates a Stdio transport. The process is started lazily on first Send.
func NewStdio(command string, args []string) *Stdio {
	return &Stdio{
		command: command,
		args:    args,
		done:    make(chan struct{}),
	}
}

func (t *Stdio) Send(ctx context.Context, message []byte) ([]byte, error) {
	if err := t.ensureRunning(); err != nil {
		return nil, fmt.Errorf("stdio: start process: %w", err)
	}

	var parsed struct {
		ID json.RawMessage `json:"id"`
	}
	if err := json.Unmarshal(message, &parsed); err != nil {
		return nil, fmt.Errorf("stdio: parse message id: %w", err)
	}
	idKey := string(parsed.ID)

	ch := make(chan []byte, 1)
	t.pending.Store(idKey, ch)
	defer t.pending.Delete(idKey)

	t.writeMu.Lock()
	_, err := fmt.Fprintf(t.stdin, "%s\n", message)
	t.writeMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("stdio: write: %w", err)
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		return nil, fmt.Errorf("stdio: process exited")
	}
}

func (t *Stdio) Stream(ctx context.Context, message []byte, messages chan<- []byte) error {
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

func (t *Stdio) Health(ctx context.Context) HealthStatus {
	start := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	ping := []byte(`{"jsonrpc":"2.0","id":"__health__","method":"ping"}`)
	_, err := t.Send(ctx, ping)
	return HealthStatus{
		Healthy: err == nil,
		Latency: time.Since(start),
		Err:     err,
	}
}

func (t *Stdio) Close() error {
	var err error
	t.closeOnce.Do(func() {
		close(t.done)
		t.procMu.Lock()
		defer t.procMu.Unlock()
		if t.stdin != nil {
			t.stdin.Close()
		}
		if t.proc != nil && t.proc.Process != nil {
			err = t.proc.Process.Kill()
		}
	})
	return err
}

// ensureRunning starts the child process if it isn't already running.
func (t *Stdio) ensureRunning() error {
	t.procMu.Lock()
	defer t.procMu.Unlock()

	if t.proc != nil {
		return nil
	}

	cmd := exec.Command(t.command, t.args...) //nolint:gosec
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	t.proc = cmd
	t.stdin = stdin
	go t.readLoop(stdout)
	return nil
}

// readLoop reads newline-delimited JSON from stdout and routes each message
// to the pending channel for its id.
func (t *Stdio) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		var parsed struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(line, &parsed); err != nil {
			continue
		}
		idKey := string(parsed.ID)
		if v, ok := t.pending.Load(idKey); ok {
			ch := v.(chan []byte)
			buf := make([]byte, len(line))
			copy(buf, line)
			select {
			case ch <- buf:
			default:
			}
		}
	}
	// Signal all pending callers that the process has exited.
	select {
	case <-t.done:
	default:
		close(t.done)
	}
}
