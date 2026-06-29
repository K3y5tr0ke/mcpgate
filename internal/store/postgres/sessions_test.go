//go:build integration

package postgres_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func TestSessionStore_CRUD(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	us := pgstore.NewUserStore(db)
	ss := pgstore.NewSessionStore(db)
	ctx := t.Context()

	// Create a user to satisfy the FK.
	userID := uuid.New()
	if err := us.Create(ctx, &store.User{
		ID: userID, Email: "sess-user@example.com",
		Status: "active", Metadata: map[string]any{},
	}); err != nil {
		t.Fatalf("create user for session test: %v", err)
	}

	session := &store.Session{
		ID:        uuid.New(),
		UserID:    userID,
		ClientIP:  "127.0.0.1",
		UserAgent: "test-agent/1.0",
		ExpiresAt: time.Now().Add(time.Hour),
		CreatedAt: time.Now(),
	}

	t.Run("Create", func(t *testing.T) {
		if err := ss.Create(ctx, session); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		got, err := ss.GetByID(ctx, session.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.UserID != session.UserID {
			t.Errorf("UserID mismatch")
		}
		if got.UserAgent != "test-agent/1.0" {
			t.Errorf("UserAgent: got %q", got.UserAgent)
		}
	})

	t.Run("GetByIDNotFound", func(t *testing.T) {
		_, err := ss.GetByID(ctx, uuid.New())
		if err != store.ErrNotFound {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := ss.Delete(ctx, session.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := ss.GetByID(ctx, session.ID)
		if err != store.ErrNotFound {
			t.Errorf("after delete: want ErrNotFound, got %v", err)
		}
	})

	t.Run("DeleteExpired", func(t *testing.T) {
		// Insert an already-expired session.
		expired := &store.Session{
			ID:        uuid.New(),
			UserID:    userID,
			ClientIP:  "127.0.0.1",
			UserAgent: "expired-agent",
			ExpiresAt: time.Now().Add(-time.Hour),
			CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		if err := ss.Create(ctx, expired); err != nil {
			t.Fatalf("create expired session: %v", err)
		}

		n, err := ss.DeleteExpired(ctx)
		if err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		if n < 1 {
			t.Errorf("expected at least 1 deleted row, got %d", n)
		}
	})
}
