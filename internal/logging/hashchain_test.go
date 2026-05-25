package logging

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

func makeEntry(method string) store.AuditLogEntry {
	return store.AuditLogEntry{
		Timestamp:  time.Now(),
		TraceID:    uuid.New().String(),
		UserEmail:  "test@example.com",
		ServerName: "test-server",
		MCPMethod:  method,
		Status:     "success",
		LatencyMS:  5,
	}
}

func TestHashChain_SingleEntry(t *testing.T) {
	hc := NewHashChain("genesis")
	e := makeEntry("tools/list")
	if err := hc.Next(&e); err != nil {
		t.Fatalf("Next: %v", err)
	}
	if e.HashChain == "" {
		t.Error("HashChain not set")
	}
	if hc.Tip() != e.HashChain {
		t.Errorf("Tip() %q != entry.HashChain %q", hc.Tip(), e.HashChain)
	}
}

func TestHashChain_ChainOf10_Sequential(t *testing.T) {
	hc := NewHashChain("genesis")
	entries := make([]store.AuditLogEntry, 10)
	for i := range entries {
		entries[i] = makeEntry("tools/call")
		if err := hc.Next(&entries[i]); err != nil {
			t.Fatalf("Next[%d]: %v", i, err)
		}
	}
	// Each entry must have a distinct hash.
	seen := make(map[string]bool)
	for i, e := range entries {
		if seen[e.HashChain] {
			t.Errorf("duplicate hash at index %d: %s", i, e.HashChain)
		}
		seen[e.HashChain] = true
	}
}

func TestHashChain_TamperDetection(t *testing.T) {
	hc := NewHashChain("genesis")
	entries := make([]store.AuditLogEntry, 5)
	for i := range entries {
		entries[i] = makeEntry("tools/list")
		if err := hc.Next(&entries[i]); err != nil {
			t.Fatalf("Next[%d]: %v", i, err)
		}
	}

	// Tamper with entry 2.
	entries[2].MCPMethod = "tampered"

	ok, err := VerifyChain(entries, "genesis")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if ok {
		t.Error("VerifyChain returned true despite tampered entry")
	}
}

func TestHashChain_IntactChainVerifies(t *testing.T) {
	hc := NewHashChain("genesis")
	entries := make([]store.AuditLogEntry, 5)
	for i := range entries {
		entries[i] = makeEntry("tools/list")
		if err := hc.Next(&entries[i]); err != nil {
			t.Fatalf("Next[%d]: %v", i, err)
		}
	}

	ok, err := VerifyChain(entries, "genesis")
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !ok {
		t.Error("VerifyChain returned false for intact chain")
	}
}

func TestHashChain_DifferentSeedsDifferentChains(t *testing.T) {
	e1 := makeEntry("tools/list")
	e2 := makeEntry("tools/list")
	e2.TraceID = e1.TraceID
	e2.Timestamp = e1.Timestamp

	hc1 := NewHashChain("seed-a")
	if err := hc1.Next(&e1); err != nil {
		t.Fatal(err)
	}

	hc2 := NewHashChain("seed-b")
	if err := hc2.Next(&e2); err != nil {
		t.Fatal(err)
	}

	if e1.HashChain == e2.HashChain {
		t.Error("different seeds produced identical hashes")
	}
}
