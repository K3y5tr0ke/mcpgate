//go:build integration

package postgres_test

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func makeAuditEntry(method, status string) store.AuditLogEntry {
	return store.AuditLogEntry{
		Timestamp:  time.Now().UTC().Truncate(time.Millisecond),
		TraceID:    uuid.New().String(),
		UserEmail:  "test@example.com",
		ServerName: "test-server",
		MCPMethod:  method,
		Direction:  "response",
		Status:     status,
		LatencyMS:  10,
	}
}

func TestAuditLogStore_BatchInsert_Single(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	entry := makeAuditEntry("tools/list", "success")
	entry.HashChain = "genesis-hash"

	if err := s.BatchInsert(ctx, []store.AuditLogEntry{entry}); err != nil {
		t.Fatalf("BatchInsert: %v", err)
	}
}

func TestAuditLogStore_BatchInsert_Multiple(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	entries := make([]store.AuditLogEntry, 5)
	for i := range entries {
		entries[i] = makeAuditEntry("tools/call", "success")
		entries[i].HashChain = uuid.New().String()
	}

	if err := s.BatchInsert(ctx, entries); err != nil {
		t.Fatalf("BatchInsert multiple: %v", err)
	}
}

func TestAuditLogStore_Query_NoFilter(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	for i := 0; i < 3; i++ {
		e := makeAuditEntry("tools/list", "success")
		e.HashChain = uuid.New().String()
		if err := s.BatchInsert(ctx, []store.AuditLogEntry{e}); err != nil {
			t.Fatalf("BatchInsert[%d]: %v", i, err)
		}
	}

	entries, total, err := s.Query(ctx, store.LogFilter{}, store.Pagination{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if total < 3 {
		t.Errorf("total: got %d, want >= 3", total)
	}
	if len(entries) == 0 {
		t.Error("entries should not be empty")
	}
}

func TestAuditLogStore_Query_ByStatus(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	success := makeAuditEntry("tools/list", "success")
	success.HashChain = "h1"
	denied := makeAuditEntry("tools/call", "denied")
	denied.HashChain = "h2"

	_ = s.BatchInsert(ctx, []store.AuditLogEntry{success, denied})

	entries, _, err := s.Query(ctx, store.LogFilter{Status: "denied"}, store.Pagination{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Query by status: %v", err)
	}
	for _, e := range entries {
		if e.Status != "denied" {
			t.Errorf("got status %q, want denied", e.Status)
		}
	}
}

func TestAuditLogStore_Query_ByMCPMethod(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	e1 := makeAuditEntry("tools/list", "success")
	e1.HashChain = "h1"
	e2 := makeAuditEntry("tools/call", "success")
	e2.HashChain = "h2"
	_ = s.BatchInsert(ctx, []store.AuditLogEntry{e1, e2})

	entries, _, err := s.Query(ctx, store.LogFilter{MCPMethod: "tools/list"}, store.Pagination{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("Query by method: %v", err)
	}
	for _, e := range entries {
		if e.MCPMethod != "tools/list" {
			t.Errorf("got method %q, want tools/list", e.MCPMethod)
		}
	}
}

func TestAuditLogStore_Query_Pagination(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	for i := 0; i < 6; i++ {
		e := makeAuditEntry("ping", "success")
		e.HashChain = uuid.New().String()
		_ = s.BatchInsert(ctx, []store.AuditLogEntry{e})
	}

	page1, total, err := s.Query(ctx, store.LogFilter{MCPMethod: "ping"}, store.Pagination{Page: 1, PageSize: 3})
	if err != nil {
		t.Fatalf("page 1: %v", err)
	}
	if total < 6 {
		t.Errorf("total: got %d, want >= 6", total)
	}
	if len(page1) != 3 {
		t.Errorf("page1 len: got %d, want 3", len(page1))
	}
}

func TestAuditLogStore_LatestHashChain_Empty(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	_, err := s.LatestHashChain(ctx)
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestAuditLogStore_LatestHashChain_AfterInsert(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	e := makeAuditEntry("tools/list", "success")
	e.HashChain = "expected-hash"
	_ = s.BatchInsert(ctx, []store.AuditLogEntry{e})

	h, err := s.LatestHashChain(ctx)
	if err != nil {
		t.Fatalf("LatestHashChain: %v", err)
	}
	if h != "expected-hash" {
		t.Errorf("got %q, want expected-hash", h)
	}
}

func TestAuditLogStore_Export_JSON(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	e := makeAuditEntry("tools/list", "success")
	e.TraceID = "export-trace-" + uuid.New().String()
	e.HashChain = "h1"
	_ = s.BatchInsert(ctx, []store.AuditLogEntry{e})

	var buf bytes.Buffer
	if err := s.Export(ctx, store.LogFilter{}, "json", &buf); err != nil {
		t.Fatalf("Export JSON: %v", err)
	}
	if !strings.Contains(buf.String(), e.TraceID) {
		t.Errorf("export output does not contain trace_id %q: %s", e.TraceID, buf.String())
	}
}

func TestAuditLogStore_Export_CSV(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	e := makeAuditEntry("tools/call", "success")
	e.HashChain = "h1"
	_ = s.BatchInsert(ctx, []store.AuditLogEntry{e})

	var buf bytes.Buffer
	if err := s.Export(ctx, store.LogFilter{}, "csv", &buf); err != nil {
		t.Fatalf("Export CSV: %v", err)
	}
	if !strings.Contains(buf.String(), "hash_chain") {
		t.Errorf("CSV header missing: %s", buf.String())
	}
}

func TestAuditLogStore_VerifyHashChain_Empty(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewAuditLogStore(db)
	ctx := t.Context()

	ok, err := s.VerifyHashChain(ctx, time.Now().Add(-time.Hour), time.Now(), "")
	if err != nil {
		t.Fatalf("VerifyHashChain: %v", err)
	}
	if !ok {
		t.Error("empty range should verify OK")
	}
}
