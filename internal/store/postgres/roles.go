package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// RoleStore implements store.RoleStore against PostgreSQL.
// Sprint 2: only GetUserRoles is implemented. All other methods return a
// "not implemented" error and will be completed in Sprint 4.
type RoleStore struct {
	db *pgxpool.Pool
}

// NewRoleStore creates a RoleStore backed by db.
func NewRoleStore(db *pgxpool.Pool) *RoleStore {
	return &RoleStore{db: db}
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
		var ro store.Role
		var idPG pgtype.UUID
		if err := rows.Scan(&idPG, &ro.Name, &ro.Description, &ro.IsSystem, &ro.CreatedAt); err != nil {
			return nil, fmt.Errorf("roles: scan: %w", err)
		}
		ro.ID = uuid.UUID(idPG.Bytes)
		roles = append(roles, ro)
	}
	return roles, rows.Err()
}

func (s *RoleStore) Create(_ context.Context, _ *store.Role) error {
	return fmt.Errorf("roles: Create not implemented — Sprint 4")
}

func (s *RoleStore) GetByID(_ context.Context, _ uuid.UUID) (*store.Role, error) {
	return nil, fmt.Errorf("roles: GetByID not implemented — Sprint 4")
}

func (s *RoleStore) GetByName(_ context.Context, _ string) (*store.Role, error) {
	return nil, fmt.Errorf("roles: GetByName not implemented — Sprint 4")
}

func (s *RoleStore) List(_ context.Context) ([]store.Role, error) {
	return nil, fmt.Errorf("roles: List not implemented — Sprint 4")
}

func (s *RoleStore) Update(_ context.Context, _ *store.Role) error {
	return fmt.Errorf("roles: Update not implemented — Sprint 4")
}

func (s *RoleStore) Delete(_ context.Context, _ uuid.UUID) error {
	return fmt.Errorf("roles: Delete not implemented — Sprint 4")
}

func (s *RoleStore) AssignToUser(_ context.Context, _, _, _ uuid.UUID) error {
	return fmt.Errorf("roles: AssignToUser not implemented — Sprint 4")
}

func (s *RoleStore) RevokeFromUser(_ context.Context, _, _ uuid.UUID) error {
	return fmt.Errorf("roles: RevokeFromUser not implemented — Sprint 4")
}
