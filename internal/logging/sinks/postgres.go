package sinks

import (
	"context"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// PostgresSink writes batches to PostgreSQL via AuditLogStore.BatchInsert.
// Intentionally thin — all batching and retry logic lives in the pipeline.
type PostgresSink struct {
	store store.AuditLogStore
}

// NewPostgresSink returns a Sink backed by s.
func NewPostgresSink(s store.AuditLogStore) *PostgresSink {
	return &PostgresSink{store: s}
}

func (s *PostgresSink) Write(ctx context.Context, entries []store.AuditLogEntry) error {
	return s.store.BatchInsert(ctx, entries)
}
