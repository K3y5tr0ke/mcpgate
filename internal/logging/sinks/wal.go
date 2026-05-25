package sinks

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Sink is the interface both PostgresSink and WALSink satisfy.
// It is defined here (not in the pipeline package) to avoid an import cycle.
type Sink interface {
	Write(ctx context.Context, entries []store.AuditLogEntry) error
}

// WALSink appends entries as newline-delimited JSON to a file when the primary
// sink fails. Only instantiated when cfg.Pipeline.WALPath is non-empty.
type WALSink struct {
	path   string
	mu     sync.Mutex
	logger *slog.Logger
}

// NewWALSink returns a WALSink that writes to path.
func NewWALSink(path string, logger *slog.Logger) *WALSink {
	return &WALSink{path: path, logger: logger}
}

// Write appends each entry as a JSON line to the WAL file, creating it if absent.
func (s *WALSink) Write(_ context.Context, entries []store.AuditLogEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("wal: open %q: %w", s.path, err)
	}
	defer f.Close() //nolint:errcheck

	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return fmt.Errorf("wal: encode entry: %w", err)
		}
	}
	return nil
}

// Replay reads all entries from the WAL, passes them to primary, and on success
// truncates the file. Safe to call concurrently — acquires the write mutex.
func (s *WALSink) Replay(ctx context.Context, primary Sink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil // nothing to replay
	}
	if err != nil {
		return fmt.Errorf("wal: open for replay: %w", err)
	}

	var entries []store.AuditLogEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e store.AuditLogEntry
		if err := json.Unmarshal(line, &e); err != nil {
			s.logger.Warn("wal: skip malformed line", "err", err)
			continue
		}
		entries = append(entries, e)
	}
	f.Close() //nolint:errcheck,gosec

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("wal: scan: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}

	if err := primary.Write(ctx, entries); err != nil {
		return fmt.Errorf("wal: replay to primary: %w", err)
	}

	// Truncate on success — entries are now in the primary store.
	if err := os.Truncate(s.path, 0); err != nil {
		s.logger.Warn("wal: truncate after replay failed", "err", err)
	}
	s.logger.Info("wal: replayed entries to primary", "count", len(entries))
	return nil
}

// HasContent reports whether the WAL file exists and is non-empty.
// Used by the background replay goroutine to decide whether to attempt Replay.
func (s *WALSink) HasContent() bool {
	fi, err := os.Stat(s.path)
	return err == nil && fi.Size() > 0
}
