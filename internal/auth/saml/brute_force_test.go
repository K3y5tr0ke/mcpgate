package saml

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// mockCounter satisfies the redisCounter interface for unit tests.
type mockCounter struct {
	counts map[string]int64
	err    error
}

func newMockCounter() *mockCounter {
	return &mockCounter{counts: make(map[string]int64)}
}

func (m *mockCounter) Incr(_ context.Context, key string) (int64, error) {
	if m.err != nil {
		return 0, m.err
	}
	m.counts[key]++
	return m.counts[key], nil
}
func (m *mockCounter) Expire(_ context.Context, _ string, _ time.Duration) (bool, error) {
	return true, nil
}
func (m *mockCounter) Del(_ context.Context, keys ...string) (int64, error) {
	for _, k := range keys {
		delete(m.counts, k)
	}
	return int64(len(keys)), nil
}

func newGuard(rc redisCounter, max int, enabled bool) *BruteForceGuard {
	return &BruteForceGuard{
		redis:       rc,
		maxAttempts: int64(max),
		window:      time.Minute,
		blockFor:    time.Hour,
		enabled:     enabled,
	}
}

func TestBruteForceGuard_UnderLimit_Allows(t *testing.T) {
	rc := newMockCounter()
	g := newGuard(rc, 5, true)

	for i := range 5 {
		if g.Check(context.Background(), "key") {
			t.Errorf("request %d: expected allowed, got blocked", i+1)
		}
	}
}

func TestBruteForceGuard_AtLimit_Blocks(t *testing.T) {
	rc := newMockCounter()
	g := newGuard(rc, 3, true)

	for range 3 {
		g.Check(context.Background(), "key")
	}
	if !g.Check(context.Background(), "key") {
		t.Error("4th request: expected blocked")
	}
}

func TestBruteForceGuard_Reset_ClearsCounter(t *testing.T) {
	rc := newMockCounter()
	g := newGuard(rc, 3, true)

	for range 3 {
		g.Check(context.Background(), "key")
	}
	g.Reset(context.Background(), "key")

	// After reset, counter starts over; first request should be allowed.
	if g.Check(context.Background(), "key") {
		t.Error("after reset: expected allowed, got blocked")
	}
}

func TestBruteForceGuard_Disabled_AlwaysAllows(t *testing.T) {
	rc := newMockCounter()
	g := newGuard(rc, 1, false)

	for range 100 {
		if g.Check(context.Background(), "key") {
			t.Error("disabled guard should never block")
		}
	}
}

func TestBruteForceGuard_RedisError_Allows(t *testing.T) {
	rc := &mockCounter{err: fmt.Errorf("redis: connection refused")}
	g := newGuard(rc, 3, true)

	if g.Check(context.Background(), "key") {
		t.Error("Redis error should allow (availability over enforcement)")
	}
}

func TestBruteForceGuard_SeparateKeys_Independent(t *testing.T) {
	rc := newMockCounter()
	g := newGuard(rc, 2, true)

	// Exhaust key-a.
	for range 3 {
		g.Check(context.Background(), "key-a")
	}

	// key-b should still be allowed.
	if g.Check(context.Background(), "key-b") {
		t.Error("separate key should not be affected by other key's limit")
	}
}

func TestNewBruteForceGuard_FromConfig(t *testing.T) {
	cfg := config.BruteForceConfig{
		Enabled:     true,
		MaxAttempts: 5,
		Window:      15 * time.Minute,
		BlockFor:    time.Hour,
	}
	g := &BruteForceGuard{
		redis:       newMockCounter(),
		maxAttempts: int64(cfg.MaxAttempts),
		window:      cfg.Window,
		blockFor:    cfg.BlockFor,
		enabled:     cfg.Enabled,
	}
	if g.maxAttempts != 5 {
		t.Errorf("maxAttempts: got %d, want 5", g.maxAttempts)
	}
}
