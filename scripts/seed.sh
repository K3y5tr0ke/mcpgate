#!/usr/bin/env bash
# seed.sh — seed the database with default roles and an initial admin user
# Requires: running PostgreSQL (make docker-up) with migrations applied (make migrate-up)
# Usage: bash scripts/seed.sh [--db-url <url>]

set -euo pipefail

DATABASE_URL="${DATABASE_URL:-postgres://mcpgate:devpassword@localhost:5432/mcpgate?sslmode=disable}"

while [[ $# -gt 0 ]]; do
    case $1 in
        --db-url) DATABASE_URL="$2"; shift 2 ;;
        *) echo "Unknown argument: $1"; exit 1 ;;
    esac
done

psql_cmd() {
    psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -c "$1"
}

echo "[seed] Inserting default roles..."

psql_cmd "
INSERT INTO roles (name, description, is_system) VALUES
    ('super_admin', 'Full access to admin console, all servers, and all tools', true),
    ('admin',       'Admin console access; can manage roles and view logs', true),
    ('operator',    'Can view logs and metrics; cannot modify configurations', true),
    ('developer',   'Can invoke tools on assigned servers', true),
    ('viewer',      'Can list tools and read resources; cannot invoke tools', true)
ON CONFLICT (name) DO NOTHING;
"

echo "[seed] Inserting default Casbin RBAC policies..."

psql_cmd "
INSERT INTO casbin_rule (ptype, v0, v1, v2, v3, v4) VALUES
    -- super_admin: full access
    ('p', 'super_admin', '*', '*', '*', 'allow'),
    -- admin: full MCP access
    ('p', 'admin',       '*', '*', '*', 'allow'),
    -- operator: can list and read, cannot call
    ('p', 'operator',    '*', '*', 'list',    'allow'),
    ('p', 'operator',    '*', '*', 'read',    'allow'),
    ('p', 'operator',    '*', '*', 'call',    'deny'),
    -- developer: can list and call
    ('p', 'developer',   '*', '*', 'list',    'allow'),
    ('p', 'developer',   '*', '*', 'call',    'allow'),
    ('p', 'developer',   '*', '*', 'read',    'allow'),
    -- viewer: read-only
    ('p', 'viewer',      '*', '*', 'list',    'allow'),
    ('p', 'viewer',      '*', '*', 'read',    'allow'),
    ('p', 'viewer',      '*', '*', 'call',    'deny'),
    -- Role hierarchy (g = grouping)
    ('g', 'super_admin', 'admin',     '', '', ''),
    ('g', 'admin',       'developer', '', '', ''),
    ('g', 'developer',   'viewer',    '', '', '')
ON CONFLICT DO NOTHING;
"

echo "[seed] Inserting bootstrap admin user (admin@mcpgate.local)..."

psql_cmd "
INSERT INTO users (email, display_name, status)
VALUES ('admin@mcpgate.local', 'Bootstrap Admin', 'active')
ON CONFLICT (email) DO NOTHING;
"

psql_cmd "
INSERT INTO user_roles (user_id, role_id)
SELECT u.id, r.id
FROM   users u, roles r
WHERE  u.email = 'admin@mcpgate.local'
  AND  r.name  = 'super_admin'
ON CONFLICT DO NOTHING;
"

echo "[seed] Done."
echo "       Bootstrap admin: admin@mcpgate.local (authenticate via SAML)"
