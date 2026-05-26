-- Migration 005 rollback

BEGIN;

DROP TABLE IF EXISTS rate_limit_overrides;

DELETE FROM settings WHERE key IN (
    'session_ttl_seconds',
    'rate_limit_default_rpm',
    'rate_limit_default_rpd',
    'log_retention_hot_days',
    'proxy_default_timeout_seconds',
    'circuit_breaker_failure_threshold',
    'circuit_breaker_recovery_seconds'
);

DROP TABLE IF EXISTS settings;

COMMIT;
