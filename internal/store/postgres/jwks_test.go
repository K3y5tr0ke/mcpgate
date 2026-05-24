//go:build integration

package postgres_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func TestJWKSConfigStore_CRUD(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewJWKSConfigStore(db)
	ctx := t.Context()

	cfg := &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "auth0-test",
		Issuer:    "https://dev-test.us.auth0.com/",
		JWKSURL:   "https://dev-test.us.auth0.com/.well-known/jwks.json",
		Audience:  "https://api.mcpgate.internal",
		IsEnabled: true,
	}

	t.Run("Create", func(t *testing.T) {
		if err := s.Create(ctx, cfg); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("CreateDuplicate_Name", func(t *testing.T) {
		dup := *cfg
		dup.ID = uuid.New()
		dup.Issuer = "https://other.example.com/"
		if err := s.Create(ctx, &dup); err != store.ErrAlreadyExists {
			t.Errorf("want ErrAlreadyExists on duplicate name, got %v", err)
		}
	})

	t.Run("CreateDuplicate_Issuer", func(t *testing.T) {
		dup := *cfg
		dup.ID = uuid.New()
		dup.Name = "different-name"
		if err := s.Create(ctx, &dup); err != store.ErrAlreadyExists {
			t.Errorf("want ErrAlreadyExists on duplicate issuer, got %v", err)
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		got, err := s.GetByID(ctx, cfg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.Issuer != cfg.Issuer {
			t.Errorf("Issuer: got %q, want %q", got.Issuer, cfg.Issuer)
		}
		if got.Audience != cfg.Audience {
			t.Errorf("Audience: got %q, want %q", got.Audience, cfg.Audience)
		}
	})

	t.Run("GetByName", func(t *testing.T) {
		got, err := s.GetByName(ctx, "auth0-test")
		if err != nil {
			t.Fatalf("GetByName: %v", err)
		}
		if got.ID != cfg.ID {
			t.Error("ID mismatch")
		}
	})

	t.Run("GetByIssuer", func(t *testing.T) {
		got, err := s.GetByIssuer(ctx, cfg.Issuer)
		if err != nil {
			t.Fatalf("GetByIssuer: %v", err)
		}
		if got.ID != cfg.ID {
			t.Error("ID mismatch")
		}
	})

	t.Run("GetNotFound", func(t *testing.T) {
		_, err := s.GetByID(ctx, uuid.New())
		if err != store.ErrNotFound {
			t.Errorf("want ErrNotFound, got %v", err)
		}
	})

	t.Run("List", func(t *testing.T) {
		all, err := s.List(ctx)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(all) == 0 {
			t.Error("expected at least one config")
		}
	})

	t.Run("ListEnabled", func(t *testing.T) {
		enabled, err := s.ListEnabled(ctx)
		if err != nil {
			t.Fatalf("ListEnabled: %v", err)
		}
		if len(enabled) == 0 {
			t.Error("expected at least one enabled config")
		}
	})

	t.Run("Update", func(t *testing.T) {
		cfg.IsEnabled = false
		cfg.Audience = "https://api.mcpgate.internal/v2"
		if err := s.Update(ctx, cfg); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, _ := s.GetByID(ctx, cfg.ID)
		if got.IsEnabled {
			t.Error("expected IsEnabled=false after update")
		}
		if got.Audience != "https://api.mcpgate.internal/v2" {
			t.Errorf("Audience: got %q", got.Audience)
		}
	})

	t.Run("GetByIssuer_Disabled", func(t *testing.T) {
		// cfg is now disabled — GetByIssuer must return ErrNotFound.
		_, err := s.GetByIssuer(ctx, cfg.Issuer)
		if err != store.ErrNotFound {
			t.Errorf("want ErrNotFound for disabled issuer, got %v", err)
		}
	})

	t.Run("ListEnabledFiltersDisabled", func(t *testing.T) {
		enabled, err := s.ListEnabled(ctx)
		if err != nil {
			t.Fatalf("ListEnabled: %v", err)
		}
		for _, c := range enabled {
			if c.ID == cfg.ID {
				t.Error("disabled config should not appear in ListEnabled")
			}
		}
	})

	t.Run("Delete", func(t *testing.T) {
		if err := s.Delete(ctx, cfg.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		_, err := s.GetByID(ctx, cfg.ID)
		if err != store.ErrNotFound {
			t.Errorf("after delete: want ErrNotFound, got %v", err)
		}
	})
}
