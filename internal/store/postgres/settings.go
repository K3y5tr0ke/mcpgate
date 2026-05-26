package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// SettingsStore implements store.SettingsStore against PostgreSQL.
type SettingsStore struct {
	db *pgxpool.Pool
}

// NewSettingsStore creates a SettingsStore backed by db.
func NewSettingsStore(db *pgxpool.Pool) *SettingsStore {
	return &SettingsStore{db: db}
}

// GetAll returns all settings as a key→value map.
func (s *SettingsStore) GetAll(ctx context.Context) (map[string]any, error) {
	rows, err := s.db.Query(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("settings: get all: %w", err)
	}
	defer rows.Close()

	result := make(map[string]any)
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, fmt.Errorf("settings: scan: %w", err)
		}
		var val any
		if err := json.Unmarshal(raw, &val); err != nil {
			return nil, fmt.Errorf("settings: unmarshal %q: %w", key, err)
		}
		result[key] = val
	}
	return result, rows.Err()
}

// Get returns the value for key, or store.ErrNotFound if the key does not exist.
func (s *SettingsStore) Get(ctx context.Context, key string) (any, error) {
	var raw []byte
	err := s.db.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&raw)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("settings: get %q: %w", key, err)
	}
	var val any
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, fmt.Errorf("settings: unmarshal %q: %w", key, err)
	}
	return val, nil
}

// Set inserts or updates the value for key.
func (s *SettingsStore) Set(ctx context.Context, key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("settings: marshal %q: %w", key, err)
	}
	_, err = s.db.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at)
		 VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE
		   SET value = EXCLUDED.value, updated_at = now()`,
		key, raw,
	)
	if err != nil {
		return fmt.Errorf("settings: set %q: %w", key, err)
	}
	return nil
}
