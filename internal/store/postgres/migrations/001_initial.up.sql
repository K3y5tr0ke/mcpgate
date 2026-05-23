-- Migration 001: initial schema
-- All core tables for mcpgate v1.

BEGIN;

-- Users synced from SAML / managed via admin console
CREATE TABLE users (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    email        TEXT        UNIQUE NOT NULL,
    display_name TEXT        NOT NULL DEFAULT '',
    idp_subject  TEXT        NOT NULL DEFAULT '',
    idp_name     TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active', 'suspended', 'deactivated')),
    metadata     JSONB       NOT NULL DEFAULT '{}',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Roles
CREATE TABLE roles (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        UNIQUE NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    is_system   BOOLEAN     NOT NULL DEFAULT false,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- User-role assignments
CREATE TABLE user_roles (
    user_id     UUID        NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    role_id     UUID        NOT NULL REFERENCES roles(id)  ON DELETE CASCADE,
    assigned_by UUID        REFERENCES users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, role_id)
);

-- Casbin RBAC policy rules
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT      NOT NULL,   -- 'p' = policy, 'g' = role grouping
    v0    TEXT      NOT NULL DEFAULT '',
    v1    TEXT      NOT NULL DEFAULT '',
    v2    TEXT      NOT NULL DEFAULT '',
    v3    TEXT      NOT NULL DEFAULT '',
    v4    TEXT      NOT NULL DEFAULT '',
    v5    TEXT      NOT NULL DEFAULT ''
);

CREATE INDEX idx_casbin_rule_ptype ON casbin_rule (ptype);

-- Upstream MCP server configurations
CREATE TABLE mcp_servers (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        UNIQUE NOT NULL,
    description  TEXT        NOT NULL DEFAULT '',
    transport    TEXT        NOT NULL
                             CHECK (transport IN ('streamable_http', 'sse', 'stdio')),
    config       JSONB       NOT NULL,
    health_check JSONB       NOT NULL DEFAULT '{}',
    is_enabled   BOOLEAN     NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Cached tool capabilities per server
CREATE TABLE server_tools (
    server_id     UUID        NOT NULL REFERENCES mcp_servers(id) ON DELETE CASCADE,
    tool_name     TEXT        NOT NULL,
    description   TEXT        NOT NULL DEFAULT '',
    input_schema  JSONB,
    discovered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (server_id, tool_name)
);

-- Audit log (append-only, partitioned by month)
CREATE TABLE audit_logs (
    id             BIGSERIAL,
    timestamp      TIMESTAMPTZ NOT NULL DEFAULT now(),
    trace_id       TEXT        NOT NULL DEFAULT '',
    session_id     UUID,
    user_id        UUID        REFERENCES users(id)       ON DELETE SET NULL,
    user_email     TEXT        NOT NULL DEFAULT '',
    client_ip      INET,
    server_id      UUID        REFERENCES mcp_servers(id) ON DELETE SET NULL,
    server_name    TEXT        NOT NULL DEFAULT '',
    mcp_method     TEXT        NOT NULL DEFAULT '',
    tool_name      TEXT        NOT NULL DEFAULT '',
    direction      TEXT        NOT NULL DEFAULT ''
                               CHECK (direction IN ('request', 'response', 'notification', '')),
    status         TEXT        NOT NULL DEFAULT ''
                               CHECK (status IN ('success', 'error', 'denied', '')),
    error_code     INTEGER,
    error_message  TEXT,
    latency_ms     INTEGER     NOT NULL DEFAULT 0,
    request_size   INTEGER     NOT NULL DEFAULT 0,
    response_size  INTEGER     NOT NULL DEFAULT 0,
    rbac_decision  TEXT        NOT NULL DEFAULT '',
    metadata       JSONB       NOT NULL DEFAULT '{}',
    hash_chain     TEXT        NOT NULL DEFAULT '',
    PRIMARY KEY (id, timestamp)
) PARTITION BY RANGE (timestamp);

-- Bootstrap partition covering initial deployment; add monthly partitions via cron / pg_partman
CREATE TABLE audit_logs_default PARTITION OF audit_logs DEFAULT;

CREATE INDEX idx_audit_logs_timestamp  ON audit_logs (timestamp DESC);
CREATE INDEX idx_audit_logs_user_id    ON audit_logs (user_id);
CREATE INDEX idx_audit_logs_server     ON audit_logs (server_name);
CREATE INDEX idx_audit_logs_tool       ON audit_logs (tool_name);
CREATE INDEX idx_audit_logs_status     ON audit_logs (status);
CREATE INDEX idx_audit_logs_session    ON audit_logs (session_id);

-- SAML IdP configurations
CREATE TABLE saml_idp_configs (
    id                UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name              TEXT        UNIQUE NOT NULL,
    entity_id         TEXT        NOT NULL,
    sso_url           TEXT        NOT NULL,
    slo_url           TEXT,
    certificate       TEXT        NOT NULL,   -- PEM-encoded X.509
    attribute_map     JSONB       NOT NULL DEFAULT '{}',
    group_role_map    JSONB       NOT NULL DEFAULT '{}',
    is_enabled        BOOLEAN     NOT NULL DEFAULT true,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- OAuth 2.0 clients for programmatic / agent access
CREATE TABLE oauth_clients (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    client_id     TEXT        UNIQUE NOT NULL,
    client_secret TEXT        NOT NULL,    -- bcrypt hashed
    name          TEXT        NOT NULL,
    grant_types   TEXT[]      NOT NULL,
    redirect_uris TEXT[],
    scopes        TEXT[],
    owner_id      UUID        REFERENCES users(id) ON DELETE SET NULL,
    is_active     BOOLEAN     NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Sessions (durable store; Redis is the fast cache)
CREATE TABLE sessions (
    id         UUID        PRIMARY KEY,
    user_id    UUID        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_ip  INET,
    user_agent TEXT        NOT NULL DEFAULT '',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_sessions_user_id    ON sessions (user_id);
CREATE INDEX idx_sessions_expires_at ON sessions (expires_at);

-- Auto-update updated_at columns via trigger
CREATE OR REPLACE FUNCTION set_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_updated_at        BEFORE UPDATE ON users        FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER mcp_servers_updated_at  BEFORE UPDATE ON mcp_servers  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
CREATE TRIGGER saml_idp_updated_at     BEFORE UPDATE ON saml_idp_configs FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
