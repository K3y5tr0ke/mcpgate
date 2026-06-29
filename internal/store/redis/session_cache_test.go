//go:build integration

package redis_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	redisstore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
)

// newTestRedisClient connects to the Redis instance started by docker-compose.
func newTestRedisClient(t *testing.T) *goredis.Client {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
	if err := client.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available at localhost:6379: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestSessionCache_SetGetDelete(t *testing.T) {
	client := newTestRedisClient(t)
	cache := redisstore.NewSessionCache(client)
	ctx := context.Background()

	sessionID := uuid.New()
	data := store.SessionData{
		UserID:    uuid.New(),
		Email:     "alice@example.com",
		Roles:     []string{"admin", "viewer"},
		SessionID: sessionID,
	}

	t.Run("Set", func(t *testing.T) {
		if err := cache.Set(ctx, sessionID, data, time.Minute); err != nil {
			t.Fatalf("Set: %v", err)
		}
	})

	t.Run("Get", func(t *testing.T) {
		got, err := cache.Get(ctx, sessionID)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if got.Email != data.Email {
			t.Errorf("Email: got %q", got.Email)
		}
		if len(got.Roles) != 2 {
			t.Errorf("Roles: got %v", got.Roles)
		}
	})

	t.Run("Extend", func(t *testing.T) {
		if err := cache.Extend(ctx, sessionID, 2*time.Minute); err != nil {
			t.Fatalf("Extend: %v", err)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := cache.Delete(ctx, sessionID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := cache.Get(ctx, sessionID)
		if err != store.ErrNotFound {
			t.Errorf("after delete: want ErrNotFound, got %v", err)
		}
	})
}

func TestSessionCache_GetMiss(t *testing.T) {
	client := newTestRedisClient(t)
	cache := redisstore.NewSessionCache(client)
	ctx := context.Background()

	_, err := cache.Get(ctx, uuid.New())
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestSessionCache_ExtendMissingKey(t *testing.T) {
	client := newTestRedisClient(t)
	cache := redisstore.NewSessionCache(client)
	ctx := context.Background()

	err := cache.Extend(ctx, uuid.New(), time.Minute)
	if err == nil {
		t.Error("expected error for missing key, got nil")
	}
}
