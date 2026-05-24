-- Migration 002: JWKS trusted issuer configurations for M2M JWT auth

BEGIN;

CREATE TABLE jwks_configs (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT        UNIQUE NOT NULL,
    issuer     TEXT        UNIQUE NOT NULL,
    jwks_url   TEXT        NOT NULL,
    audience   TEXT        NOT NULL DEFAULT '',
    is_enabled BOOLEAN     NOT NULL DEFAULT true,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Partial index optimises the auth hot path (GetByIssuer always filters enabled).
CREATE INDEX idx_jwks_configs_issuer ON jwks_configs (issuer) WHERE is_enabled = true;

CREATE TRIGGER jwks_configs_updated_at
    BEFORE UPDATE ON jwks_configs
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

COMMIT;
