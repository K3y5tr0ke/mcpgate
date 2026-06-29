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

func TestUserStore_CRUD(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewUserStore(db)
	ctx := t.Context()

	user := &store.User{
		ID:          uuid.New(),
		Email:       "alice@example.com",
		DisplayName: "Alice",
		IDPSubject:  "alice-subject",
		IDPName:     "test-idp",
		Status:      "active",
		Metadata:    map[string]any{"dept": "eng"},
	}

	t.Run("Create", func(t *testing.T) {
		if err := s.Create(ctx, user); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("CreateDuplicate", func(t *testing.T) {
		dup := *user
		dup.ID = uuid.New()
		err := s.Create(ctx, &dup)
		if err != store.ErrAlreadyExists {
			t.Errorf("want ErrAlreadyExists, got %v", err)
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		got, err := s.GetByID(ctx, user.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Email != user.Email {
			t.Errorf("Email: got %q, want %q", got.Email, user.Email)
		}
		if v, ok := got.Metadata["dept"]; !ok || v != "eng" {
			t.Errorf("Metadata: got %v", got.Metadata)
		}
	})

	t.Run("GetByEmail", func(t *testing.T) {
		got, err := s.GetByEmail(ctx, "alice@example.com")
		if err != nil {
			t.Fatalf("GetByEmail: %v", err)
		}
		if got.ID != user.ID {
			t.Errorf("ID mismatch")
		}
	})

	t.Run("GetByIDPSubject", func(t *testing.T) {
		got, err := s.GetByIDPSubject(ctx, "test-idp", "alice-subject")
		if err != nil {
			t.Fatalf("GetByIDPSubject: %v", err)
		}
		if got.ID != user.ID {
			t.Errorf("ID mismatch")
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := s.GetByID(ctx, uuid.New())
		if err != store.ErrNotFound {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("Update", func(t *testing.T) {
		user.DisplayName = "Alice Updated"
		user.Status = "suspended"
		if err := s.Update(ctx, user); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, _ := s.GetByID(ctx, user.ID)
		if got.DisplayName != "Alice Updated" {
			t.Errorf("DisplayName: got %q", got.DisplayName)
		}
		if got.Status != "suspended" {
			t.Errorf("Status: got %q", got.Status)
		}
	})

	t.Run("UpdateStatus", func(t *testing.T) {
		if err := s.UpdateStatus(ctx, user.ID, "active"); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		got, _ := s.GetByID(ctx, user.ID)
		if got.Status != "active" {
			t.Errorf("Status: got %q", got.Status)
		}
	})

	t.Run("List", func(t *testing.T) {
		users, total, err := s.List(ctx, store.UserFilter{}, store.Pagination{Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total < 1 {
			t.Errorf("total: got %d", total)
		}
		if len(users) == 0 {
			t.Error("expected at least one user")
		}
	})

	t.Run("ListFilterByEmail", func(t *testing.T) {
		users, _, err := s.List(ctx, store.UserFilter{Email: "alice"}, store.Pagination{Page: 1, PageSize: 10})
		if err != nil {
			t.Fatalf("List with filter: %v", err)
		}
		if len(users) == 0 {
			t.Error("expected alice in results")
		}
	})
}

func TestUserStore_UpdateNotFound(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewUserStore(db)
	ctx := t.Context()

	err := s.Update(ctx, &store.User{
		ID:        uuid.New(),
		Email:     "ghost@example.com",
		Status:    "active",
		Metadata:  map[string]any{},
		UpdatedAt: time.Now(),
	})
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}
