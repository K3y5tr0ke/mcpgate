package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// SessionCache implements store.SessionCache using Redis.
type SessionCache struct {
	client *goredis.Client
}

// NewSessionCache creates a SessionCache backed by client.
func NewSessionCache(client *goredis.Client) *SessionCache {
	return &SessionCache{client: client}
}

func (c *SessionCache) Set(ctx context.Context, sessionID uuid.UUID, data store.SessionData, ttl time.Duration) error {
	b, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("session cache: marshal: %w", err)
	}
	if err := c.client.Set(ctx, sessionKey(sessionID), b, ttl).Err(); err != nil {
		return fmt.Errorf("session cache: set: %w", err)
	}
	return nil
}

func (c *SessionCache) Get(ctx context.Context, sessionID uuid.UUID) (*store.SessionData, error) {
	val, err := c.client.Get(ctx, sessionKey(sessionID)).Bytes()
	if err != nil {
		if errors.Is(err, goredis.Nil) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("session cache: get: %w", err)
	}

	var data store.SessionData
	if err := json.Unmarshal(val, &data); err != nil {
		return nil, fmt.Errorf("session cache: unmarshal: %w", err)
	}
	return &data, nil
}

func (c *SessionCache) Delete(ctx context.Context, sessionID uuid.UUID) error {
	if err := c.client.Del(ctx, sessionKey(sessionID)).Err(); err != nil {
		return fmt.Errorf("session cache: delete: %w", err)
	}
	return nil
}

func (c *SessionCache) Extend(ctx context.Context, sessionID uuid.UUID, ttl time.Duration) error {
	ok, err := c.client.Expire(ctx, sessionKey(sessionID), ttl).Result()
	if err != nil {
		return fmt.Errorf("session cache: extend: %w", err)
	}
	if !ok {
		// Key no longer exists — session was already deleted or expired.
		return fmt.Errorf("session cache: extend: key not found")
	}
	return nil
}

func sessionKey(id uuid.UUID) string {
	return "session:" + id.String()
}
