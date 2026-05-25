package logging

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// HashChain maintains the running tip of the tamper-evident SHA-256 hash chain.
// All methods must be called from the same goroutine — the pipeline writer goroutine
// is the only caller, so no synchronisation is needed.
type HashChain struct {
	prev string // hex SHA-256 of the previous entry (or the seed for the first entry)
}

// NewHashChain initialises the chain. seed is the prev-hash for the very first entry.
func NewHashChain(seed string) *HashChain {
	return &HashChain{prev: seed}
}

// Next computes the hash for entry, sets entry.HashChain, and advances the chain tip.
// All fields except HashChain must be populated before calling Next.
func (hc *HashChain) Next(entry *store.AuditLogEntry) error {
	content, err := marshalWithoutHashChain(entry)
	if err != nil {
		return fmt.Errorf("hashchain: marshal: %w", err)
	}
	combined := hc.prev + string(content)
	sum := sha256.Sum256([]byte(combined))
	entry.HashChain = fmt.Sprintf("%x", sum)
	hc.prev = entry.HashChain
	return nil
}

// Tip returns the current chain tip — the hash of the last processed entry.
func (hc *HashChain) Tip() string { return hc.prev }

// marshalWithoutHashChain serialises entry with fields excluded from the hash zeroed:
//   - HashChain: to avoid circularity
//   - ID: BIGSERIAL assigned by Postgres after insert; 0 at hash-compute time
func marshalWithoutHashChain(entry *store.AuditLogEntry) ([]byte, error) {
	// shadow type prevents infinite recursion while preserving all other fields.
	type shadow store.AuditLogEntry
	tmp := shadow(*entry)
	tmp.HashChain = ""
	tmp.ID = 0
	return json.Marshal(tmp)
}

// VerifyChain recomputes the hash for each entry in order and returns false on the
// first mismatch. entries must be in ascending time/id order (the same order they
// were written). prevHash is the chain tip before the first entry in the slice.
func VerifyChain(entries []store.AuditLogEntry, prevHash string) (bool, error) {
	hc := NewHashChain(prevHash)
	for i := range entries {
		stored := entries[i].HashChain
		entries[i].HashChain = "" // zero so Next computes from scratch
		if err := hc.Next(&entries[i]); err != nil {
			return false, fmt.Errorf("hashchain: verify entry %d: %w", i, err)
		}
		if entries[i].HashChain != stored {
			return false, nil
		}
	}
	return true, nil
}
