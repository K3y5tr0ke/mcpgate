package saml

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// redisCounter abstracts the Redis operations BruteForceGuard needs.
// This narrow interface makes the guard unit-testable without a real Redis.
type redisCounter interface {
	Incr(ctx context.Context, key string) (int64, error)
	Expire(ctx context.Context, key string, d time.Duration) (bool, error)
	Del(ctx context.Context, keys ...string) (int64, error)
}

// redisAdapter wraps *redis.Client to satisfy redisCounter.
type redisAdapter struct{ c *redis.Client }

func (a *redisAdapter) Incr(ctx context.Context, key string) (int64, error) {
	return a.c.Incr(ctx, key).Result()
}
func (a *redisAdapter) Expire(ctx context.Context, key string, d time.Duration) (bool, error) {
	return a.c.Expire(ctx, key, d).Result()
}
func (a *redisAdapter) Del(ctx context.Context, keys ...string) (int64, error) {
	return a.c.Del(ctx, keys...).Result()
}

// BruteForceGuard tracks request counts per key using Redis INCR.
// Each key is namespaced by endpoint and client IP: "brute:<endpoint>:<ip>".
type BruteForceGuard struct {
	redis       redisCounter
	maxAttempts int64
	window      time.Duration
	blockFor    time.Duration
	enabled     bool
}

// NewBruteForceGuard creates a guard backed by the given Redis client.
func NewBruteForceGuard(rc *redis.Client, cfg config.BruteForceConfig) *BruteForceGuard {
	return &BruteForceGuard{
		redis:       &redisAdapter{rc},
		maxAttempts: int64(cfg.MaxAttempts),
		window:      cfg.Window,
		blockFor:    cfg.BlockFor,
		enabled:     cfg.Enabled,
	}
}

// Check returns true if the key is rate-limited. Always increments the counter.
// On Redis failure, allows the request (availability over strict enforcement).
func (g *BruteForceGuard) Check(ctx context.Context, key string) bool {
	if !g.enabled {
		return false
	}
	count, err := g.redis.Incr(ctx, key)
	if err != nil {
		return false
	}
	if count == 1 {
		_, _ = g.redis.Expire(ctx, key, g.window)
	}
	if count > g.maxAttempts {
		_, _ = g.redis.Expire(ctx, key, g.blockFor) // extend block for repeat offenders
		return true
	}
	return false
}

// Reset deletes the counter for the given key on successful auth.
func (g *BruteForceGuard) Reset(ctx context.Context, key string) {
	_, _ = g.redis.Del(ctx, key)
}
