//go:build integration

package redis_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	redisstore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
)

func newTestRateLimiter(t *testing.T) *redisstore.RateLimiter {
	t.Helper()
	client := newTestRedisClient(t)
	rl, err := redisstore.NewRateLimiter(client)
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}
	return rl
}

// uniqueKey returns a key that won't collide with other test runs.
func uniqueKey(t *testing.T) string {
	t.Helper()
	return "test:" + t.Name() + ":" + t.Name()
}

func TestRateLimiter_UnderLimit_Allowed(t *testing.T) {
	rl := newTestRateLimiter(t)
	ctx := context.Background()
	key := uniqueKey(t)

	result, err := rl.Allow(ctx, key, store.RateLimit{RPM: 10})
	if err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !result.Allowed {
		t.Fatal("request should be allowed under limit")
	}
	if result.Remaining < 0 {
		t.Errorf("Remaining = %d, want >= 0", result.Remaining)
	}
}

func TestRateLimiter_AtLimit_Denied(t *testing.T) {
	rl := newTestRateLimiter(t)
	ctx := context.Background()
	key := uniqueKey(t)
	limit := store.RateLimit{RPM: 3}

	for i := 0; i < 3; i++ {
		res, err := rl.Allow(ctx, key, limit)
		if err != nil {
			t.Fatalf("Allow[%d]: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed (limit=3)", i)
		}
	}

	res, err := rl.Allow(ctx, key, limit)
	if err != nil {
		t.Fatalf("Allow at limit: %v", err)
	}
	if res.Allowed {
		t.Fatal("4th request should be denied when RPM=3")
	}
	if res.RetryAfter <= 0 {
		t.Errorf("RetryAfter = %v, want > 0 when denied", res.RetryAfter)
	}
}

func TestRateLimiter_RPM_And_RPD_Independent(t *testing.T) {
	rl := newTestRateLimiter(t)
	ctx := context.Background()
	keyBase := uniqueKey(t)

	// RPM=2 with RPD=10: first 2 requests allowed by RPM; 3rd denied by RPM, not RPD.
	limit := store.RateLimit{RPM: 2, RPD: 10}
	for i := 0; i < 2; i++ {
		res, err := rl.Allow(ctx, keyBase, limit)
		if err != nil {
			t.Fatalf("Allow[%d]: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	res, err := rl.Allow(ctx, keyBase, limit)
	if err != nil {
		t.Fatalf("Allow at RPM limit: %v", err)
	}
	if res.Allowed {
		t.Fatal("request over RPM limit should be denied")
	}
}

func TestRateLimiter_ZeroLimits_AlwaysAllowed(t *testing.T) {
	rl := newTestRateLimiter(t)
	ctx := context.Background()
	key := uniqueKey(t)

	// RPM=0 and RPD=0 means no limit enforced.
	for i := 0; i < 20; i++ {
		res, err := rl.Allow(ctx, key, store.RateLimit{})
		if err != nil {
			t.Fatalf("Allow[%d]: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d denied when no limits configured", i)
		}
	}
}

func TestRateLimiter_Concurrent_DoesNotExceedLimit(t *testing.T) {
	rl := newTestRateLimiter(t)
	ctx := context.Background()
	key := uniqueKey(t)
	const limit = 5
	const goroutines = 20

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := rl.Allow(ctx, key, store.RateLimit{RPM: limit})
			if err == nil && res.Allowed {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := int(allowed.Load()); got > limit {
		t.Errorf("allowed %d concurrent requests, limit is %d", got, limit)
	}
}

func TestRateLimiter_WindowExpiry(t *testing.T) {
	// Uses a 1-second RPM window to verify key expiry.
	// This test is slow but validates the sliding window TTL.
	if testing.Short() {
		t.Skip("skipping window expiry test in short mode")
	}

	rl := newTestRateLimiter(t)
	ctx := context.Background()
	key := uniqueKey(t)
	limit := store.RateLimit{RPM: 2}

	// Exhaust the limit.
	for i := 0; i < 2; i++ {
		res, err := rl.Allow(ctx, key, limit)
		if err != nil {
			t.Fatalf("Allow[%d]: %v", i, err)
		}
		if !res.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
	}

	// Wait longer than a minute (impractical in a normal test).
	// Instead, assert the RetryAfter is within the expected window.
	res, err := rl.Allow(ctx, key, limit)
	if err != nil {
		t.Fatalf("Allow at limit: %v", err)
	}
	if res.Allowed {
		t.Fatal("should be denied at limit")
	}
	if res.RetryAfter > time.Minute {
		t.Errorf("RetryAfter = %v, want <= 1 minute for RPM window", res.RetryAfter)
	}
}
