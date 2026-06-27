//go:build integration

package postgres_test

import (
	"testing"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func TestSAMLIDPConfigStore_CRUD(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	s := pgstore.NewSAMLIDPConfigStore(db)
	ctx := t.Context()

	cfg := &store.SAMLIDPConfig{
		ID:           uuid.New(),
		Name:         "test-idp",
		EntityID:     "https://idp.example.com",
		SSOURL:       "https://idp.example.com/sso",
		SLOURL:       nil,
		Certificate:  "-----BEGIN CERTIFICATE-----\nMIIB...\n-----END CERTIFICATE-----",
		AttributeMap: map[string]string{"mail": "email"},
		GroupRoleMap: map[string]string{"admins": "admin"},
		IsEnabled:    true,
	}

	t.Run("Create", func(t *testing.T) {
		if err := s.Create(ctx, cfg); err != nil {
			t.Fatalf("Create: %v", err)
		}
	})

	t.Run("CreateDuplicate", func(t *testing.T) {
		dup := *cfg
		dup.ID = uuid.New()
		err := s.Create(ctx, &dup)
		if err != store.ErrAlreadyExists {
			t.Errorf("want ErrAlreadyExists, got %v", err)
		}
	})

	t.Run("GetByID", func(t *testing.T) {
		got, err := s.GetByID(ctx, cfg.ID)
		if err != nil {
			t.Fatalf("GetByID: %v", err)
		}
		if got.EntityID != cfg.EntityID {
			t.Errorf("EntityID: got %q", got.EntityID)
		}
		if got.AttributeMap["mail"] != "email" {
			t.Errorf("AttributeMap: got %v", got.AttributeMap)
		}
	})

	t.Run("GetByName", func(t *testing.T) {
		got, err := s.GetByName(ctx, "test-idp")
		if err != nil {
			t.Fatalf("GetByName: %v", err)
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
		cfg.SSOURL = "https://idp.example.com/sso/v2"
		if err := s.Update(ctx, cfg); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, _ := s.GetByID(ctx, cfg.ID)
		if got.IsEnabled {
			t.Error("expected IsEnabled=false after update")
		}
		if got.SSOURL != "https://idp.example.com/sso/v2" {
			t.Errorf("SSOURL: got %q", got.SSOURL)
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
