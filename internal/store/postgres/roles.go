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

// RoleStore implements store.RoleStore against PostgreSQL.
type RoleStore struct {
	db *pgxpool.Pool
}

// NewRoleStore creates a RoleStore backed by db.
func NewRoleStore(db *pgxpool.Pool) *RoleStore {
	return &RoleStore{db: db}
}

func (s *RoleStore) Create(ctx context.Context, role *store.Role) error {
	err := s.db.QueryRow(ctx, `
		INSERT INTO roles (name, description)
		VALUES ($1, $2)
		RETURNING id, created_at`,
		role.Name, role.Description,
	).Scan(&role.ID, &role.CreatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("roles: create: %w", err)
	}
	return nil
}

func (s *RoleStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Role, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, description, is_system, created_at
		FROM roles WHERE id = $1`, id)
	return scanRole(row)
}

func (s *RoleStore) GetByName(ctx context.Context, name string) (*store.Role, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, description, is_system, created_at
		FROM roles WHERE name = $1`, name)
	return scanRole(row)
}

func (s *RoleStore) List(ctx context.Context) ([]store.Role, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, description, is_system, created_at
		FROM roles ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("roles: list: %w", err)
	}
	defer rows.Close()

	var roles []store.Role
	for rows.Next() {
		r, err := scanRoleRow(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *r)
	}
	return roles, rows.Err()
}

func (s *RoleStore) Update(ctx context.Context, role *store.Role) error {
	ct, err := s.db.Exec(ctx, `
		UPDATE roles SET name = $1, description = $2 WHERE id = $3`,
		role.Name, role.Description, role.ID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("roles: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *RoleStore) Delete(ctx context.Context, id uuid.UUID) error {
	role, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if role.IsSystem {
		return store.ErrConflict
	}
	ct, err := s.db.Exec(ctx, `DELETE FROM roles WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("roles: delete: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *RoleStore) AssignToUser(ctx context.Context, userID, roleID, assignedBy uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_roles (user_id, role_id, assigned_by)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`,
		userID, roleID, assignedBy,
	)
	if err != nil {
		return fmt.Errorf("roles: assign to user: %w", err)
	}
	return nil
}

func (s *RoleStore) RevokeFromUser(ctx context.Context, userID, roleID uuid.UUID) error {
	ct, err := s.db.Exec(ctx, `
		DELETE FROM user_roles WHERE user_id = $1 AND role_id = $2`,
		userID, roleID,
	)
	if err != nil {
		return fmt.Errorf("roles: revoke from user: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *RoleStore) GetUserRoles(ctx context.Context, userID uuid.UUID) ([]store.Role, error) {
	rows, err := s.db.Query(ctx, `
		SELECT r.id, r.name, r.description, r.is_system, r.created_at
		FROM roles r
		JOIN user_roles ur ON ur.role_id = r.id
		WHERE ur.user_id = $1
		ORDER BY r.name`, userID)
	if err != nil {
		return nil, fmt.Errorf("roles: get user roles: %w", err)
	}
	defer rows.Close()

	var roles []store.Role
	for rows.Next() {
		r, err := scanRoleRow(rows)
		if err != nil {
			return nil, err
		}
		roles = append(roles, *r)
	}
	return roles, rows.Err()
}

// --- scan helpers ---

type roleScanner interface {
	Scan(dest ...any) error
}

func scanRole(row pgx.Row) (*store.Role, error) {
	return scanRoleRow(row)
}

func scanRoleRow(row roleScanner) (*store.Role, error) {
	var r store.Role
	var idPG pgtype.UUID
	if err := row.Scan(&idPG, &r.Name, &r.Description, &r.IsSystem, &r.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("roles: scan: %w", err)
	}
	r.ID = uuid.UUID(idPG.Bytes)
	return &r, nil
}
