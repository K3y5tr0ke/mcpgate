-- Composite index for timestamp-range scans (retention cleanup, hash chain verify).
-- The leftmost column is timestamp so the planner uses it for range predicates
-- in DeleteExpired and VerifyHashChain without a full table scan.
CREATE INDEX IF NOT EXISTS idx_audit_logs_timestamp_id
    ON audit_logs (timestamp ASC, id ASC);

-- Unique index on hash_chain: enables future ON CONFLICT (hash_chain) DO NOTHING
-- for fully idempotent WAL replay. Also detects accidental duplicate inserts.
-- timestamp is the partition key and must be included in all unique indexes on
-- partitioned tables (PostgreSQL requirement). hash_chain encodes entry content
-- including timestamp, so (hash_chain, timestamp) is still collision-proof.
CREATE UNIQUE INDEX IF NOT EXISTS idx_audit_logs_hash_chain
    ON audit_logs (hash_chain, timestamp);
