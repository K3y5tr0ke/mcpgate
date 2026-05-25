BEGIN;

DELETE FROM roles WHERE name IN ('admin', 'developer', 'readonly') AND is_system = true;

DROP INDEX  IF EXISTS idx_casbin_rule_ptype;
DROP TABLE  casbin_rule;

-- Restore the original schema from migration 001.
CREATE TABLE casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype TEXT      NOT NULL,
    v0    TEXT      NOT NULL DEFAULT '',
    v1    TEXT      NOT NULL DEFAULT '',
    v2    TEXT      NOT NULL DEFAULT '',
    v3    TEXT      NOT NULL DEFAULT '',
    v4    TEXT      NOT NULL DEFAULT '',
    v5    TEXT      NOT NULL DEFAULT ''
);
-- Note: down migration restores the original schema; the adapter will not work
-- with this schema — run migrate-up again to re-enable RBAC.

CREATE INDEX idx_casbin_rule_ptype ON casbin_rule (ptype);

COMMIT;
