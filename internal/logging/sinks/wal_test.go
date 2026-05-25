package sinks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"log/slog"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/logging/sinks"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

func makeEntry(method string) store.AuditLogEntry {
	return store.AuditLogEntry{
		Timestamp:  time.Now().UTC(),
		TraceID:    uuid.New().String(),
		UserEmail:  "test@example.com",
		ServerName: "srv",
		MCPMethod:  method,
		Status:     "success",
		HashChain:  uuid.New().String(),
	}
}

func TestWALSink_WriteCreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.wal")
	w := sinks.NewWALSink(path, slog.Default())

	if err := w.Write(context.Background(), []store.AuditLogEntry{makeEntry("tools/list")}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("WAL file not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("WAL file is empty after write")
	}
}

type captureSink struct{ entries []store.AuditLogEntry }

func (c *captureSink) Write(_ context.Context, entries []store.AuditLogEntry) error {
	c.entries = append(c.entries, entries...)
	return nil
}

func TestWALSink_Replay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.wal")
	w := sinks.NewWALSink(path, slog.Default())
	ctx := context.Background()

	entries := []store.AuditLogEntry{makeEntry("tools/call"), makeEntry("tools/list")}
	if err := w.Write(ctx, entries); err != nil {
		t.Fatalf("Write: %v", err)
	}

	primary := &captureSink{}
	if err := w.Replay(ctx, primary); err != nil {
		t.Fatalf("Replay: %v", err)
	}

	if len(primary.entries) != 2 {
		t.Errorf("replayed %d entries, want 2", len(primary.entries))
	}

	// WAL must be truncated after successful replay.
	if w.HasContent() {
		t.Error("WAL should be empty after successful replay")
	}
}

func TestWALSink_ReplayEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.wal")
	w := sinks.NewWALSink(path, slog.Default())

	primary := &captureSink{}
	if err := w.Replay(context.Background(), primary); err != nil {
		t.Fatalf("Replay on missing file: %v", err)
	}
	if len(primary.entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(primary.entries))
	}
}

func TestWALSink_HasContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.wal")
	w := sinks.NewWALSink(path, slog.Default())

	if w.HasContent() {
		t.Error("HasContent should be false before any write")
	}

	_ = w.Write(context.Background(), []store.AuditLogEntry{makeEntry("ping")})

	if !w.HasContent() {
		t.Error("HasContent should be true after write")
	}
}
