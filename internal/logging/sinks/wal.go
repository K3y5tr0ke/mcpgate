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
// removes the replay file. Uses an atomic rename to prevent double-insert on crash:
//
//  1. Rename path → path+".replaying" (atomic on POSIX). If this succeeds, the
//     original file is gone — new writes go to a fresh file while we read.
//  2. Read all entries from the ".replaying" file.
//  3. Write to primary.
//  4. On success: remove ".replaying".
//  5. On failure: rename ".replaying" back to path so the next attempt retries.
//
// Safe to call concurrently — acquires the write mutex.
func (s *WALSink) Replay(ctx context.Context, primary Sink) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	replayingPath := s.path + ".replaying"

	if err := os.Rename(s.path, replayingPath); err != nil {
		if os.IsNotExist(err) {
			return nil // nothing to replay
		}
		return fmt.Errorf("wal: rename for replay: %w", err)
	}

	f, err := os.Open(replayingPath) //nolint:gosec // replayingPath is derived from the configured WALPath, not user input
	if err != nil {
		// Restore so we don't silently lose the file on the next attempt.
		_ = os.Rename(replayingPath, s.path)
		return fmt.Errorf("wal: open .replaying: %w", err)
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
		_ = os.Rename(replayingPath, s.path)
		return fmt.Errorf("wal: scan: %w", err)
	}
	if len(entries) == 0 {
		_ = os.Remove(replayingPath) //nolint:errcheck
		return nil
	}

	if err := primary.Write(ctx, entries); err != nil {
		// Restore for next attempt.
		_ = os.Rename(replayingPath, s.path)
		return fmt.Errorf("wal: replay to primary: %w", err)
	}

	// Only delete after confirmed write — crash here still causes a retry on
	// next startup but the unique hash_chain index will prevent duplicates
	// once migration 004 is deployed.
	if err := os.Remove(replayingPath); err != nil {
		s.logger.Warn("wal: remove .replaying after replay failed", "err", err)
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
