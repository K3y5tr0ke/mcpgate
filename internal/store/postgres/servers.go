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

// ServerStore implements store.ServerStore against PostgreSQL.
type ServerStore struct {
	db *pgxpool.Pool
}

// NewServerStore creates a ServerStore backed by db.
func NewServerStore(db *pgxpool.Pool) *ServerStore {
	return &ServerStore{db: db}
}

func (s *ServerStore) Create(ctx context.Context, server *store.MCPServer) error {
	configJSON, err := json.Marshal(server.Config)
	if err != nil {
		return fmt.Errorf("servers: marshal config: %w", err)
	}
	healthJSON, err := json.Marshal(server.HealthCheck)
	if err != nil {
		return fmt.Errorf("servers: marshal health_check: %w", err)
	}

	row := s.db.QueryRow(ctx, `
		INSERT INTO mcp_servers (name, description, transport, config, health_check, is_enabled)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, created_at, updated_at`,
		server.Name, server.Description, server.Transport,
		configJSON, healthJSON, server.IsEnabled,
	)
	var idPG pgtype.UUID
	if err := row.Scan(&idPG, &server.CreatedAt, &server.UpdatedAt); err != nil {
		return wrapPgError(err, "servers: create")
	}
	server.ID = uuid.UUID(idPG.Bytes)
	return nil
}

func (s *ServerStore) GetByID(ctx context.Context, id uuid.UUID) (*store.MCPServer, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, description, transport, config, health_check, is_enabled, created_at, updated_at
		FROM mcp_servers WHERE id = $1`, id)
	return scanServer(row)
}

func (s *ServerStore) GetByName(ctx context.Context, name string) (*store.MCPServer, error) {
	row := s.db.QueryRow(ctx, `
		SELECT id, name, description, transport, config, health_check, is_enabled, created_at, updated_at
		FROM mcp_servers WHERE name = $1`, name)
	return scanServer(row)
}

func (s *ServerStore) List(ctx context.Context) ([]store.MCPServer, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, name, description, transport, config, health_check, is_enabled, created_at, updated_at
		FROM mcp_servers ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("servers: list: %w", err)
	}
	defer rows.Close()

	var result []store.MCPServer
	for rows.Next() {
		srv, err := scanServerRow(rows)
		if err != nil {
			return nil, fmt.Errorf("servers: list scan: %w", err)
		}
		result = append(result, *srv)
	}
	return result, rows.Err()
}

func (s *ServerStore) Update(ctx context.Context, server *store.MCPServer) error {
	configJSON, err := json.Marshal(server.Config)
	if err != nil {
		return fmt.Errorf("servers: marshal config: %w", err)
	}
	healthJSON, err := json.Marshal(server.HealthCheck)
	if err != nil {
		return fmt.Errorf("servers: marshal health_check: %w", err)
	}

	tag, err := s.db.Exec(ctx, `
		UPDATE mcp_servers
		SET name=$1, description=$2, transport=$3, config=$4, health_check=$5, is_enabled=$6
		WHERE id=$7`,
		server.Name, server.Description, server.Transport,
		configJSON, healthJSON, server.IsEnabled, server.ID,
	)
	if err != nil {
		return wrapPgError(err, "servers: update")
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *ServerStore) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `DELETE FROM mcp_servers WHERE id = $1`, id)
	if err != nil {
		return wrapPgError(err, "servers: delete")
	}
	if tag.RowsAffected() == 0 {
		return store.ErrNotFound
	}
	return nil
}

func (s *ServerStore) UpdateTools(ctx context.Context, serverID uuid.UUID, tools []store.ServerTool) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("servers: update tools begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM server_tools WHERE server_id = $1`, serverID); err != nil {
		return fmt.Errorf("servers: clear tools: %w", err)
	}

	for _, t := range tools {
		schemaJSON, err := json.Marshal(t.InputSchema)
		if err != nil {
			return fmt.Errorf("servers: marshal input_schema: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO server_tools (server_id, tool_name, description, input_schema)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (server_id, tool_name) DO UPDATE
			  SET description = EXCLUDED.description, input_schema = EXCLUDED.input_schema`,
			serverID, t.ToolName, t.Description, schemaJSON,
		); err != nil {
			return fmt.Errorf("servers: upsert tool %q: %w", t.ToolName, err)
		}
	}

	return tx.Commit(ctx)
}

// --- helpers ---

func scanServer(row pgx.Row) (*store.MCPServer, error) {
	var srv store.MCPServer
	var idPG pgtype.UUID
	var configRaw, healthRaw []byte

	err := row.Scan(
		&idPG, &srv.Name, &srv.Description, &srv.Transport,
		&configRaw, &healthRaw,
		&srv.IsEnabled, &srv.CreatedAt, &srv.UpdatedAt,
	)
	if err != nil {
		return nil, wrapPgError(err, "servers: scan")
	}

	srv.ID = uuid.UUID(idPG.Bytes)
	if err := json.Unmarshal(configRaw, &srv.Config); err != nil {
		return nil, fmt.Errorf("servers: unmarshal config: %w", err)
	}
	if healthRaw != nil {
		if err := json.Unmarshal(healthRaw, &srv.HealthCheck); err != nil {
			return nil, fmt.Errorf("servers: unmarshal health_check: %w", err)
		}
	}
	return &srv, nil
}

func scanServerRow(rows pgx.Rows) (*store.MCPServer, error) {
	var srv store.MCPServer
	var idPG pgtype.UUID
	var configRaw, healthRaw []byte

	err := rows.Scan(
		&idPG, &srv.Name, &srv.Description, &srv.Transport,
		&configRaw, &healthRaw,
		&srv.IsEnabled, &srv.CreatedAt, &srv.UpdatedAt,
	)
	if err != nil {
		return nil, wrapPgError(err, "servers: scan row")
	}

	srv.ID = uuid.UUID(idPG.Bytes)
	if err := json.Unmarshal(configRaw, &srv.Config); err != nil {
		return nil, fmt.Errorf("servers: unmarshal config: %w", err)
	}
	if healthRaw != nil {
		if err := json.Unmarshal(healthRaw, &srv.HealthCheck); err != nil {
			return nil, fmt.Errorf("servers: unmarshal health_check: %w", err)
		}
	}
	return &srv, nil
}

// wrapPgError translates pgx sentinel errors to store sentinels.
func wrapPgError(err error, context string) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrNotFound
	}
	return fmt.Errorf("%s: %w", context, err)
}
