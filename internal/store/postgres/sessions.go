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

// SessionStore implements store.SessionStore against PostgreSQL.
type SessionStore struct {
	db *pgxpool.Pool
}

// NewSessionStore creates a SessionStore backed by db.
func NewSessionStore(db *pgxpool.Pool) *SessionStore {
	return &SessionStore{db: db}
}

func (s *SessionStore) Create(ctx context.Context, session *store.Session) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO sessions (id, user_id, client_ip, user_agent, expires_at, created_at)
		VALUES ($1, $2, $3::inet, $4, $5, $6)`,
		session.ID, session.UserID, session.ClientIP, session.UserAgent,
		session.ExpiresAt, session.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("sessions: create: %w", err)
	}
	return nil
}

func (s *SessionStore) GetByID(ctx context.Context, id uuid.UUID) (*store.Session, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, user_id, client_ip::text, user_agent, expires_at, created_at
		FROM sessions WHERE id = $1`, id)

	var sess store.Session
	var idPG, userIDPG pgtype.UUID

	if err := row.Scan(
		&idPG, &userIDPG, &sess.ClientIP, &sess.UserAgent,
		&sess.ExpiresAt, &sess.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("sessions: get by id: %w", err)
	}

	sess.ID = uuid.UUID(idPG.Bytes)
	sess.UserID = uuid.UUID(userIDPG.Bytes)
	return &sess, nil
}

func (s *SessionStore) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("sessions: delete: %w", err)
	}
	return nil
}

func (s *SessionStore) DeleteExpired(ctx context.Context) (int64, error) {
	ct, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	if err != nil {
		return 0, fmt.Errorf("sessions: delete expired: %w", err)
	}
	return ct.RowsAffected(), nil
}
