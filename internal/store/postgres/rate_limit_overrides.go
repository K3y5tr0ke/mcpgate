package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// RateLimitOverrideStore implements store.RateLimitOverrideStore against PostgreSQL.
type RateLimitOverrideStore struct {
	db *pgxpool.Pool
}

// NewRateLimitOverrideStore creates a RateLimitOverrideStore backed by db.
func NewRateLimitOverrideStore(db *pgxpool.Pool) *RateLimitOverrideStore {
	return &RateLimitOverrideStore{db: db}
}

// GetByRole returns the override for roleName, or store.ErrNotFound if none exists.
func (s *RateLimitOverrideStore) GetByRole(ctx context.Context, roleName string) (*store.RateLimitOverrideEntry, error) {
	var e store.RateLimitOverrideEntry
	err := s.db.QueryRow(ctx,
		`SELECT id, role_name, rpm, rpd, updated_at
		 FROM rate_limit_overrides WHERE role_name = $1`,
		roleName,
	).Scan(&e.ID, &e.RoleName, &e.RPM, &e.RPD, &e.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("rate_limit_overrides: get by role %q: %w", roleName, err)
	}
	return &e, nil
}

// ListAll returns all rate limit overrides ordered by role name.
func (s *RateLimitOverrideStore) ListAll(ctx context.Context) ([]store.RateLimitOverrideEntry, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id, role_name, rpm, rpd, updated_at
		 FROM rate_limit_overrides ORDER BY role_name`,
	)
	if err != nil {
		return nil, fmt.Errorf("rate_limit_overrides: list all: %w", err)
	}
	defer rows.Close()

	var result []store.RateLimitOverrideEntry
	for rows.Next() {
		var e store.RateLimitOverrideEntry
		if err := rows.Scan(&e.ID, &e.RoleName, &e.RPM, &e.RPD, &e.UpdatedAt); err != nil {
			return nil, fmt.Errorf("rate_limit_overrides: scan: %w", err)
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// Upsert inserts or updates the override for entry.RoleName.
func (s *RateLimitOverrideStore) Upsert(ctx context.Context, entry *store.RateLimitOverrideEntry) error {
	entry.UpdatedAt = time.Now().UTC()
	_, err := s.db.Exec(ctx,
		`INSERT INTO rate_limit_overrides (role_name, rpm, rpd, updated_at)
		 VALUES ($1, $2, $3, $4)
		 ON CONFLICT (role_name) DO UPDATE
		   SET rpm = EXCLUDED.rpm, rpd = EXCLUDED.rpd, updated_at = EXCLUDED.updated_at`,
		entry.RoleName, entry.RPM, entry.RPD, entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("rate_limit_overrides: upsert %q: %w", entry.RoleName, err)
	}
	return nil
}

// Delete removes the override for roleName. Does not return an error if not found.
func (s *RateLimitOverrideStore) Delete(ctx context.Context, roleName string) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM rate_limit_overrides WHERE role_name = $1`, roleName,
	); err != nil {
		return fmt.Errorf("rate_limit_overrides: delete %q: %w", roleName, err)
	}
	return nil
}
