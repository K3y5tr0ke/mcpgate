package postgres

import (
	"context"
	"fmt"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates and validates a pgx connection pool from the given config.
func NewPool(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseDSN())
	if err != nil {
		return nil, fmt.Errorf("parse pool config: %w", err)
	}

	poolCfg.MaxConns = cfg.Database.MaxConnections
	poolCfg.MinConns = cfg.Database.MaxIdleConnections
	poolCfg.MaxConnLifetime = cfg.Database.ConnMaxLifetime

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
