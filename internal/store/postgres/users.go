package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// UserStore implements store.UserStore against PostgreSQL.
type UserStore struct {
	db *pgxpool.Pool
}

// NewUserStore creates a UserStore backed by db.
func NewUserStore(db *pgxpool.Pool) *UserStore {
	return &UserStore{db: db}
}

func (s *UserStore) Create(ctx context.Context, user *store.User) error {
	metaJSON, err := json.Marshal(user.Metadata)
	if err != nil {
		return fmt.Errorf("users: marshal metadata: %w", err)
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO users (id, email, display_name, idp_subject, idp_name, status, metadata)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		user.ID, user.Email, user.DisplayName, user.IDPSubject, user.IDPName, user.Status, metaJSON,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return store.ErrAlreadyExists
		}
		return fmt.Errorf("users: create: %w", err)
	}
	return nil
}

func (s *UserStore) GetByID(ctx context.Context, id uuid.UUID) (*store.User, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, email, display_name, idp_subject, idp_name, status, metadata, created_at, updated_at, password_hash
		FROM users WHERE id = $1`, id)
	return scanUser(row)
}

func (s *UserStore) GetByEmail(ctx context.Context, email string) (*store.User, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, email, display_name, idp_subject, idp_name, status, metadata, created_at, updated_at, password_hash
		FROM users WHERE email = $1`, email)
	return scanUser(row)
}

func (s *UserStore) GetByIDPSubject(ctx context.Context, idpName, subject string) (*store.User, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, email, display_name, idp_subject, idp_name, status, metadata, created_at, updated_at, password_hash
		FROM users WHERE idp_name = $1 AND idp_subject = $2`, idpName, subject)
	return scanUser(row)
}

func (s *UserStore) List(ctx context.Context, filter store.UserFilter, page store.Pagination) ([]store.User, int64, error) {
	args := []any{}
	where := "WHERE 1=1"
	n := 1

	if filter.Email != "" {
		where += fmt.Sprintf(" AND email ILIKE $%d", n)
		args = append(args, "%"+filter.Email+"%")
		n++
	}
	if filter.Status != "" {
		where += fmt.Sprintf(" AND status = $%d", n)
		args = append(args, filter.Status)
		n++
	}
	if filter.Role != "" {
		where += fmt.Sprintf(` AND id IN (
			SELECT ur.user_id FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id
			WHERE r.name = $%d)`, n)
		args = append(args, filter.Role)
		n++
	}

	var total int64
	if err := s.db.QueryRow(ctx, "SELECT COUNT(*) FROM users "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("users: count: %w", err)
	}

	if page.PageSize <= 0 {
		page.PageSize = 50
	}
	if page.Page <= 0 {
		page.Page = 1
	}
	offset := (page.Page - 1) * page.PageSize

	args = append(args, page.PageSize, offset)
	rows, err := s.db.Query(ctx,
		"SELECT id, email, display_name, idp_subject, idp_name, status, metadata, created_at, updated_at, password_hash FROM users "+
			where+fmt.Sprintf(" ORDER BY email LIMIT $%d OFFSET $%d", n, n+1),
		args...)
	if err != nil {
		return nil, 0, fmt.Errorf("users: list: %w", err)
	}
	defer rows.Close()

	var users []store.User
	for rows.Next() {
		u, err := scanUserRow(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *u)
	}
	return users, total, rows.Err()
}

func (s *UserStore) Update(ctx context.Context, user *store.User) error {
	metaJSON, err := json.Marshal(user.Metadata)
	if err != nil {
		return fmt.Errorf("users: marshal metadata: %w", err)
	}

	ct, err := s.db.Exec(ctx, `
		UPDATE users
		SET email=$1, display_name=$2, idp_subject=$3, idp_name=$4, status=$5, metadata=$6
		WHERE id=$7`,
		user.Email, user.DisplayName, user.IDPSubject, user.IDPName, user.Status, metaJSON, user.ID,
	)
	if err != nil {
		return fmt.Errorf("users: update: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *UserStore) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	ct, err := s.db.Exec(ctx, `UPDATE users SET status=$1 WHERE id=$2`, status, id)
	if err != nil {
		return fmt.Errorf("users: update status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

// --- scan helpers ---

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row pgx.Row) (*store.User, error) {
	return scanUserRow(row)
}

func scanUserRow(row userScanner) (*store.User, error) {
	var u store.User
	var idPG pgtype.UUID
	var metaJSON []byte

	if err := row.Scan(
		&idPG, &u.Email, &u.DisplayName, &u.IDPSubject, &u.IDPName,
		&u.Status, &metaJSON, &u.CreatedAt, &u.UpdatedAt, &u.PasswordHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("users: scan: %w", err)
	}

	u.ID = uuid.UUID(idPG.Bytes)
	if err := json.Unmarshal(metaJSON, &u.Metadata); err != nil {
		return nil, fmt.Errorf("users: unmarshal metadata: %w", err)
	}
	return &u, nil
}

func (s *UserStore) SetPasswordHash(ctx context.Context, id uuid.UUID, hash string) error {
	ct, err := s.db.Exec(ctx, `UPDATE users SET password_hash = $1 WHERE id = $2`, hash, id)
	if err != nil {
		return fmt.Errorf("users: set password hash: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
