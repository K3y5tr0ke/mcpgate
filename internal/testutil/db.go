//go:build integration

package testutil

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// NewPostgresPool starts a Postgres container, runs all migrations, and returns
// a connected pgxpool. migrations is the embed.FS containing *.up.sql files.
// The container and pool are closed when the test ends.
func NewPostgresPool(t *testing.T, migrations fs.FS) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	ctr, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("mcpgate_test"),
		postgres.WithUsername("mcpgate"),
		postgres.WithPassword("testpassword"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(ctx); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get postgres connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := applyMigrations(ctx, pool, migrations); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	return pool
}

// applyMigrations reads all *.up.sql files from migrations (in lexical order)
// and executes them against the pool.
func applyMigrations(ctx context.Context, pool *pgxpool.Pool, migrations fs.FS) error {
	return fs.WalkDir(migrations, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".up.sql") {
			return nil
		}
		data, err := fs.ReadFile(migrations, path)
		if err != nil {
			return err
		}
		_, err = pool.Exec(ctx, string(data))
		return err
	})
}
