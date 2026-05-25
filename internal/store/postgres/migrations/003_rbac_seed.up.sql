BEGIN;

-- Rebuild casbin_rule to match pckhoi/casbin-pgx-adapter/v3 schema.
-- Migration 001 used id BIGSERIAL and column "ptype"; the adapter expects
-- id TEXT (meow hash) and column "p_type". The table is empty in all
-- environments prior to Sprint 4 so the DROP is safe.
DROP TABLE casbin_rule;

CREATE TABLE casbin_rule (
    id     TEXT PRIMARY KEY,
    p_type TEXT,
    v0     TEXT,
    v1     TEXT,
    v2     TEXT,
    v3     TEXT,
    v4     TEXT,
    v5     TEXT
);

CREATE INDEX idx_casbin_rule_ptype ON casbin_rule (p_type);

-- Seed system roles. is_system = true prevents deletion via the admin API.
INSERT INTO roles (id, name, description, is_system)
VALUES
    (gen_random_uuid(), 'admin',     'Full access to all MCP servers and admin API', true),
    (gen_random_uuid(), 'developer', 'Read and call access to allowed servers',       true),
    (gen_random_uuid(), 'readonly',  'List and read access only',                     true)
ON CONFLICT (name) DO NOTHING;

-- Note: the default admin wildcard policy (p, admin, *, *) is seeded at
-- application startup via enforcer.SeedDefaultPolicies() because the row ID
-- is computed as a meow hash by the adapter and cannot be predicted in SQL.

COMMIT;
