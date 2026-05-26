package redis

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// NewClient creates and validates a go-redis client from the given config.
func NewClient(ctx context.Context, cfg *config.Config) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr(),
		Password: cfg.Redis.Password,
		DB:       cfg.Redis.DB,
		PoolSize: cfg.Redis.PoolSize,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return client, nil
}
