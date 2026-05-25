package transport

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// Stdio implements Transport by spawning a child process and communicating
// via stdin/stdout using newline-delimited JSON-RPC 2.0.
//
// Each Send() writes a message to stdin and blocks until the matching response
// arrives (matched by JSON-RPC id). A background readLoop goroutine routes
// responses to the waiting callers via per-id channels stored in pending.
//
// If the child process exits unexpectedly (not via Close()), the transport
// resets itself so the next Send() transparently restarts the process.
type Stdio struct {
	command string
	args    []string

	procMu  sync.Mutex // guards proc, stdin, and done
	proc    *exec.Cmd
	stdin   io.WriteCloser
	done    chan struct{} // closed when the current process exits or Close() is called
	writeMu sync.Mutex   // serialises stdin writes

	pending sync.Map // idKey(string) -> chan []byte

	closed     atomic.Bool // true once Close() has been called (intentional shutdown)
	closeOnce  sync.Once   // ensures Close() only kills the process once
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

	// Snapshot done under procMu so the read is race-free with readLoop resets.
	t.procMu.Lock()
	done := t.done
	t.procMu.Unlock()

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-done:
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
	t.closed.Store(true)
	var err error
	t.closeOnce.Do(func() {
		t.procMu.Lock()
		defer t.procMu.Unlock()
		// Close done only if it hasn't been closed by readLoop already.
		select {
		case <-t.done:
		default:
			close(t.done)
		}
		if t.stdin != nil {
			_ = t.stdin.Close()
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
//
// When the scanner exits (process died), readLoop checks whether the shutdown
// was intentional (Close() was called). If not, it resets the transport state
// so the next Send() can restart the process automatically.
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

	if t.closed.Load() {
		// Intentional shutdown — Close() already closed t.done or will momentarily.
		return
	}

	// Unexpected exit: unblock pending callers on the current done channel, then
	// reset state so the next ensureRunning() can spawn a fresh process.
	t.procMu.Lock()
	oldDone := t.done
	t.done = make(chan struct{}) // fresh channel for the next process lifecycle
	t.proc = nil
	t.stdin = nil
	t.procMu.Unlock()

	close(oldDone) // wake all Send() callers that are blocked waiting for this process
}
