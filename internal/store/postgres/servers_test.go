//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func TestServerStore_CRUD(t *testing.T) {
	pool := testutil.NewPostgresPool(t, postgres.Migrations)
	s := postgres.NewServerStore(pool)
	ctx := context.Background()

	srv := &store.MCPServer{
		Name:        "test-server",
		Description: "integration test server",
		Transport:   "streamable_http",
		Config: map[string]any{
			"endpoint": "http://localhost:9000/mcp",
		},
		HealthCheck: map[string]any{},
		IsEnabled:   true,
	}

	// Create
	if err := s.Create(ctx, srv); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if srv.ID.String() == "00000000-0000-0000-0000-000000000000" {
		t.Error("Create: ID not populated")
	}

	// GetByID
	got, err := s.GetByID(ctx, srv.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != srv.Name {
		t.Errorf("GetByID: name = %q, want %q", got.Name, srv.Name)
	}

	// GetByName
	got2, err := s.GetByName(ctx, "test-server")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got2.ID != srv.ID {
		t.Errorf("GetByName: id mismatch")
	}

	// List
	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("List: got %d servers, want 1", len(list))
	}

	// Update
	srv.Description = "updated"
	if err := s.Update(ctx, srv); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got3, _ := s.GetByID(ctx, srv.ID)
	if got3.Description != "updated" {
		t.Errorf("Update: description = %q, want updated", got3.Description)
	}

	// UpdateTools
	tools := []store.ServerTool{
		{ServerID: srv.ID, ToolName: "echo", Description: "echo tool", InputSchema: map[string]any{"type": "object"}},
	}
	if err := s.UpdateTools(ctx, srv.ID, tools); err != nil {
		t.Fatalf("UpdateTools: %v", err)
	}

	// Delete
	if err := s.Delete(ctx, srv.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// GetByID after delete returns ErrNotFound
	_, err = s.GetByID(ctx, srv.ID)
	if err != store.ErrNotFound {
		t.Errorf("GetByID after delete: want ErrNotFound, got %v", err)
	}
}

func TestServerStore_NotFound(t *testing.T) {
	pool := testutil.NewPostgresPool(t, postgres.Migrations)
	s := postgres.NewServerStore(pool)
	ctx := context.Background()

	_, err := s.GetByName(ctx, "does-not-exist")
	if err != store.ErrNotFound {
		t.Errorf("GetByName missing: want ErrNotFound, got %v", err)
	}
}
