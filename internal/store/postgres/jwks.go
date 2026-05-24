package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// JWKSConfigStore implements store.JWKSConfigStore against PostgreSQL.
type JWKSConfigStore struct {
	db *pgxpool.Pool
}

// NewJWKSConfigStore creates a JWKSConfigStore backed by db.
func NewJWKSConfigStore(db *pgxpool.Pool) *JWKSConfigStore {
	return &JWKSConfigStore{db: db}
}

const jwksCols = `id, name, issuer, jwks_url, audience, is_enabled, created_at, updated_at`

func (s *JWKSConfigStore) Create(ctx context.Context, cfg *store.JWKSConfig) error {
	if cfg.ID == uuid.Nil {
		cfg.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO jwks_configs (id, name, issuer, jwks_url, audience, is_enabled)
		VALUES ($1,$2,$3,$4,$5,$6)`,
		cfg.ID, cfg.Name, cfg.Issuer, cfg.JWKSURL, cfg.Audience, cfg.IsEnabled,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("jwks_config: create: %w", err)
	}
	return nil
}

func (s *JWKSConfigStore) GetByID(ctx context.Context, id uuid.UUID) (*store.JWKSConfig, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+jwksCols+` FROM jwks_configs WHERE id = $1`, id)
	return scanJWKSConfig(row)
}

func (s *JWKSConfigStore) GetByName(ctx context.Context, name string) (*store.JWKSConfig, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+jwksCols+` FROM jwks_configs WHERE name = $1`, name)
	return scanJWKSConfig(row)
}

func (s *JWKSConfigStore) GetByIssuer(ctx context.Context, issuer string) (*store.JWKSConfig, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+jwksCols+` FROM jwks_configs WHERE issuer = $1 AND is_enabled = true`, issuer)
	return scanJWKSConfig(row)
}

func (s *JWKSConfigStore) List(ctx context.Context) ([]store.JWKSConfig, error) {
	return s.queryAll(ctx, `SELECT `+jwksCols+` FROM jwks_configs ORDER BY name`)
}

func (s *JWKSConfigStore) ListEnabled(ctx context.Context) ([]store.JWKSConfig, error) {
	return s.queryAll(ctx, `SELECT `+jwksCols+` FROM jwks_configs WHERE is_enabled = true ORDER BY name`)
}

func (s *JWKSConfigStore) Update(ctx context.Context, cfg *store.JWKSConfig) error {
	ct, err := s.db.Exec(ctx, `
		UPDATE jwks_configs
		SET name=$1, issuer=$2, jwks_url=$3, audience=$4, is_enabled=$5
		WHERE id=$6`,
		cfg.Name, cfg.Issuer, cfg.JWKSURL, cfg.Audience, cfg.IsEnabled, cfg.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("jwks_config: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *JWKSConfigStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM jwks_configs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("jwks_config: delete: %w", err)
	}
	return nil
}

// --- helpers ---

func (s *JWKSConfigStore) queryAll(ctx context.Context, sql string, args ...any) ([]store.JWKSConfig, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("jwks_config: query: %w", err)
	}
	defer rows.Close()

	var cfgs []store.JWKSConfig
	for rows.Next() {
		cfg, err := scanJWKSConfig(rows)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, *cfg)
	}
	return cfgs, rows.Err()
}

func scanJWKSConfig(row idpConfigScanner) (*store.JWKSConfig, error) {
	var cfg store.JWKSConfig
	var idPG pgtype.UUID
	if err := row.Scan(
		&idPG, &cfg.Name, &cfg.Issuer, &cfg.JWKSURL, &cfg.Audience,
		&cfg.IsEnabled, &cfg.CreatedAt, &cfg.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("jwks_config: scan: %w", err)
	}
	cfg.ID = uuid.UUID(idPG.Bytes)
	return &cfg, nil
}
