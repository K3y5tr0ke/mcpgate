-- Migration 001 rollback: drop all initial schema objects

BEGIN;

DROP TRIGGER IF EXISTS saml_idp_updated_at    ON saml_idp_configs;
DROP TRIGGER IF EXISTS mcp_servers_updated_at ON mcp_servers;
DROP TRIGGER IF EXISTS users_updated_at       ON users;
DROP FUNCTION IF EXISTS set_updated_at();

DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS oauth_clients;
DROP TABLE IF EXISTS saml_idp_configs;
DROP TABLE IF EXISTS audit_logs_default;
DROP TABLE IF EXISTS audit_logs;
DROP TABLE IF EXISTS server_tools;
DROP TABLE IF EXISTS mcp_servers;
DROP TABLE IF EXISTS casbin_rule;
DROP TABLE IF EXISTS user_roles;
DROP TABLE IF EXISTS roles;
DROP TABLE IF EXISTS users;

COMMIT;
