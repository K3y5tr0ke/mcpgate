-- Migration 005: settings store + per-role rate limit overrides

BEGIN;

-- Flat key-value store for mutable config values.
-- Keys are validated by the settings handler against a known allowlist.
CREATE TABLE IF NOT EXISTS settings (
    key        TEXT        PRIMARY KEY,
    value      JSONB       NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed default settings so the settings API always has values to return.
INSERT INTO settings (key, value) VALUES
    ('session_ttl_seconds',               '3600'::jsonb),
    ('rate_limit_default_rpm',            '600'::jsonb),
    ('rate_limit_default_rpd',            '50000'::jsonb),
    ('audit_log_retention_days',          '30'::jsonb),
    ('proxy_default_timeout_seconds',     '10'::jsonb),
    ('circuit_breaker_failure_threshold', '5'::jsonb),
    ('circuit_breaker_recovery_seconds',  '30'::jsonb)
ON CONFLICT (key) DO NOTHING;

-- Per-role rate limit overrides. A value of 0 means "use the global default".
-- role_name references roles(name); roles(name) has a UNIQUE constraint.
-- ON DELETE CASCADE removes the override when the role is deleted.
CREATE TABLE IF NOT EXISTS rate_limit_overrides (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    role_name  TEXT        NOT NULL REFERENCES roles(name) ON DELETE CASCADE,
    rpm        INT         NOT NULL DEFAULT 0,
    rpd        INT         NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (role_name)
);

COMMIT;
