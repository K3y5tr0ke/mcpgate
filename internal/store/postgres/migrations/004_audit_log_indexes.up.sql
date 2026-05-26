-- Composite index for timestamp-range scans (retention cleanup, hash chain verify).
-- The leftmost column is timestamp so the planner uses it for range predicates
-- in DeleteExpired and VerifyHashChain without a full table scan.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_timestamp_id
    ON audit_logs (timestamp ASC, id ASC);

-- Unique index on hash_chain: enables future ON CONFLICT (hash_chain) DO NOTHING
-- for fully idempotent WAL replay. Also detects accidental duplicate inserts.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_hash_chain
    ON audit_logs (hash_chain);
