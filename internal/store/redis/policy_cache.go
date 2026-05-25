package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"
)

// PolicyCache implements store.PolicyCache using Redis.
// nil returned from Get means cache miss; *true = allow, *false = deny.
type PolicyCache struct {
	client *goredis.Client
}

// NewPolicyCache creates a PolicyCache backed by client.
func NewPolicyCache(client *goredis.Client) *PolicyCache {
	return &PolicyCache{client: client}
}

func (c *PolicyCache) Get(ctx context.Context, key string) (*bool, error) {
	val, err := c.client.Get(ctx, key).Result()
	if errors.Is(err, goredis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("policy cache: get %q: %w", key, err)
	}
	var allowed bool
	if err := json.Unmarshal([]byte(val), &allowed); err != nil {
		return nil, fmt.Errorf("policy cache: unmarshal %q: %w", key, err)
	}
	return &allowed, nil
}

func (c *PolicyCache) Set(ctx context.Context, key string, allowed bool, ttl time.Duration) error {
	v, err := json.Marshal(allowed)
	if err != nil {
		return fmt.Errorf("policy cache: marshal: %w", err)
	}
	if err := c.client.Set(ctx, key, v, ttl).Err(); err != nil {
		return fmt.Errorf("policy cache: set %q: %w", key, err)
	}
	return nil
}

// Invalidate deletes all keys matching the glob pattern via SCAN + DEL batches.
func (c *PolicyCache) Invalidate(ctx context.Context, pattern string) error {
	var cursor uint64
	for {
		keys, next, err := c.client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("policy cache: scan %q: %w", pattern, err)
		}
		if len(keys) > 0 {
			if err := c.client.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("policy cache: del: %w", err)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return nil
}
