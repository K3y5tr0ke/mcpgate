-- Migration 006: local admin account support
-- Adds a nullable password_hash column to users so a local bootstrap account
-- can be created by 'make seed' without requiring a SAML IdP to be configured.
-- Local accounts are identified by idp_name = 'local'.

BEGIN;

ALTER TABLE users ADD COLUMN IF NOT EXISTS password_hash TEXT;

COMMIT;
