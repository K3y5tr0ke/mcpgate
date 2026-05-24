-- Migration 002 rollback

BEGIN;
DROP TRIGGER IF EXISTS jwks_configs_updated_at ON jwks_configs;
DROP INDEX  IF EXISTS idx_jwks_configs_issuer;
DROP TABLE  IF EXISTS jwks_configs;
COMMIT;
