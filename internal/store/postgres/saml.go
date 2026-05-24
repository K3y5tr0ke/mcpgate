package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// SAMLIDPConfigStore implements store.SAMLIDPConfigStore against PostgreSQL.
type SAMLIDPConfigStore struct {
	db *pgxpool.Pool
}

// NewSAMLIDPConfigStore creates a SAMLIDPConfigStore backed by db.
func NewSAMLIDPConfigStore(db *pgxpool.Pool) *SAMLIDPConfigStore {
	return &SAMLIDPConfigStore{db: db}
}

func (s *SAMLIDPConfigStore) Create(ctx context.Context, cfg *store.SAMLIDPConfig) error {
	attrJSON, err := json.Marshal(cfg.AttributeMap)
	if err != nil {
		return fmt.Errorf("saml_idp: marshal attribute_map: %w", err)
	}
	grpJSON, err := json.Marshal(cfg.GroupRoleMap)
	if err != nil {
		return fmt.Errorf("saml_idp: marshal group_role_map: %w", err)
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO saml_idp_configs
			(id, name, entity_id, sso_url, slo_url, certificate, attribute_map, group_role_map, is_enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
		cfg.ID, cfg.Name, cfg.EntityID, cfg.SSOURL, cfg.SLOURL,
		cfg.Certificate, attrJSON, grpJSON, cfg.IsEnabled,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("saml_idp: create: %w", err)
	}
	return nil
}

func (s *SAMLIDPConfigStore) GetByID(ctx context.Context, id uuid.UUID) (*store.SAMLIDPConfig, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, entity_id, sso_url, slo_url, certificate,
		       attribute_map, group_role_map, is_enabled, created_at, updated_at
		FROM saml_idp_configs WHERE id = $1`, id)
	return scanIDPConfig(row)
}

func (s *SAMLIDPConfigStore) GetByName(ctx context.Context, name string) (*store.SAMLIDPConfig, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, entity_id, sso_url, slo_url, certificate,
		       attribute_map, group_role_map, is_enabled, created_at, updated_at
		FROM saml_idp_configs WHERE name = $1`, name)
	return scanIDPConfig(row)
}

func (s *SAMLIDPConfigStore) List(ctx context.Context) ([]store.SAMLIDPConfig, error) {
	return s.query(ctx, `
		SELECT id, name, entity_id, sso_url, slo_url, certificate,
		       attribute_map, group_role_map, is_enabled, created_at, updated_at
		FROM saml_idp_configs ORDER BY name`)
}

func (s *SAMLIDPConfigStore) ListEnabled(ctx context.Context) ([]store.SAMLIDPConfig, error) {
	return s.query(ctx, `
		SELECT id, name, entity_id, sso_url, slo_url, certificate,
		       attribute_map, group_role_map, is_enabled, created_at, updated_at
		FROM saml_idp_configs WHERE is_enabled = true ORDER BY name`)
}

func (s *SAMLIDPConfigStore) Update(ctx context.Context, cfg *store.SAMLIDPConfig) error {
	attrJSON, err := json.Marshal(cfg.AttributeMap)
	if err != nil {
		return fmt.Errorf("saml_idp: marshal attribute_map: %w", err)
	}
	grpJSON, err := json.Marshal(cfg.GroupRoleMap)
	if err != nil {
		return fmt.Errorf("saml_idp: marshal group_role_map: %w", err)
	}

	ct, err := s.db.Exec(ctx, `
		UPDATE saml_idp_configs
		SET name=$1, entity_id=$2, sso_url=$3, slo_url=$4,
		    certificate=$5, attribute_map=$6, group_role_map=$7, is_enabled=$8
		WHERE id=$9`,
		cfg.Name, cfg.EntityID, cfg.SSOURL, cfg.SLOURL,
		cfg.Certificate, attrJSON, grpJSON, cfg.IsEnabled, cfg.ID,
	)
	if err != nil {
		return fmt.Errorf("saml_idp: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *SAMLIDPConfigStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM saml_idp_configs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("saml_idp: delete: %w", err)
	}
	return nil
}

// --- helpers ---

func (s *SAMLIDPConfigStore) query(ctx context.Context, sql string, args ...any) ([]store.SAMLIDPConfig, error) {
	rows, err := s.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("saml_idp: query: %w", err)
	}
	defer rows.Close()

	var cfgs []store.SAMLIDPConfig
	for rows.Next() {
		cfg, err := scanIDPConfigRow(rows)
		if err != nil {
			return nil, err
		}
		cfgs = append(cfgs, *cfg)
	}
	return cfgs, rows.Err()
}

type idpConfigScanner interface {
	Scan(dest ...any) error
}

func scanIDPConfig(row pgx.Row) (*store.SAMLIDPConfig, error) {
	return scanIDPConfigRow(row)
}

func scanIDPConfigRow(row idpConfigScanner) (*store.SAMLIDPConfig, error) {
	var cfg store.SAMLIDPConfig
	var idPG pgtype.UUID
	var attrJSON, grpJSON []byte

	if err := row.Scan(
		&idPG, &cfg.Name, &cfg.EntityID, &cfg.SSOURL, &cfg.SLOURL,
		&cfg.Certificate, &attrJSON, &grpJSON,
		&cfg.IsEnabled, &cfg.CreatedAt, &cfg.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("saml_idp: scan: %w", err)
	}

	cfg.ID = uuid.UUID(idPG.Bytes)

	if err := json.Unmarshal(attrJSON, &cfg.AttributeMap); err != nil {
		return nil, fmt.Errorf("saml_idp: unmarshal attribute_map: %w", err)
	}
	if err := json.Unmarshal(grpJSON, &cfg.GroupRoleMap); err != nil {
		return nil, fmt.Errorf("saml_idp: unmarshal group_role_map: %w", err)
	}
	return &cfg, nil
}
