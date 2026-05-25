package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// AuditLogStore implements store.AuditLogStore against PostgreSQL.
type AuditLogStore struct {
	db *pgxpool.Pool
}

// NewAuditLogStore creates an AuditLogStore backed by db.
func NewAuditLogStore(db *pgxpool.Pool) *AuditLogStore {
	return &AuditLogStore{db: db}
}

// LatestHashChain returns the hash_chain of the most recently written row.
// Returns store.ErrNotFound if the table is empty.
// Not part of the AuditLogStore interface — called directly by server.New() at startup.
func (s *AuditLogStore) LatestHashChain(ctx context.Context) (string, error) {
	var h string
	err := s.db.QueryRow(ctx,
		`SELECT hash_chain FROM audit_logs ORDER BY timestamp DESC, id DESC LIMIT 1`,
	).Scan(&h)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("audit_logs: latest hash chain: %w", err)
	}
	return h, nil
}

// BatchInsert writes entries to audit_logs in a single multi-row INSERT.
func (s *AuditLogStore) BatchInsert(ctx context.Context, entries []store.AuditLogEntry) error {
	if len(entries) == 0 {
		return nil
	}

	const cols = 20
	sb := &strings.Builder{}
	sb.WriteString(`INSERT INTO audit_logs (
		timestamp, trace_id, session_id, user_id, user_email, client_ip,
		server_id, server_name, mcp_method, tool_name, direction, status,
		error_code, error_message, latency_ms, request_size, response_size,
		rbac_decision, metadata, hash_chain
	) VALUES `)

	args := make([]any, 0, len(entries)*cols)
	for i, e := range entries {
		if i > 0 {
			sb.WriteString(", ")
		}
		b := i*cols + 1
		fmt.Fprintf(sb,
			"($%d,$%d,$%d,$%d,$%d,$%d::inet,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d,$%d)",
			b, b+1, b+2, b+3, b+4, b+5,
			b+6, b+7, b+8, b+9, b+10, b+11,
			b+12, b+13, b+14, b+15, b+16,
			b+17, b+18, b+19,
		)

		meta, _ := json.Marshal(e.Metadata)
		if meta == nil {
			meta = []byte("{}")
		}
		args = append(args,
			e.Timestamp, e.TraceID,
			nullUUID(e.SessionID), nullUUID(e.UserID),
			e.UserEmail, nullString(e.ClientIP),
			nullUUID(e.ServerID), e.ServerName,
			e.MCPMethod, e.ToolName,
			e.Direction, e.Status,
			e.ErrorCode, e.ErrorMessage,
			e.LatencyMS, e.RequestSize, e.ResponseSize,
			e.RBACDecision, string(meta), e.HashChain,
		)
	}

	if _, err := s.db.Exec(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("audit_logs: batch insert: %w", err)
	}
	return nil
}

// Query returns audit log entries matching filter with pagination.
// The second return value is the total count of rows that match (ignoring pagination).
func (s *AuditLogStore) Query(
	ctx context.Context,
	filter store.LogFilter,
	page store.Pagination,
) ([]store.AuditLogEntry, int64, error) {
	where, args := buildAuditWhere(filter)

	pageSize := page.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 200 {
		pageSize = 200
	}
	pageNum := page.Page
	if pageNum <= 0 {
		pageNum = 1
	}
	offset := (pageNum - 1) * pageSize

	n := len(args)
	query := fmt.Sprintf(`
		SELECT id, timestamp, trace_id, session_id, user_id, user_email, client_ip::text,
		       server_id, server_name, mcp_method, tool_name, direction, status,
		       error_code, error_message, latency_ms, request_size, response_size,
		       rbac_decision, metadata, hash_chain,
		       COUNT(*) OVER() AS total
		FROM audit_logs
		%s
		ORDER BY timestamp DESC, id DESC
		LIMIT $%d OFFSET $%d`,
		where, n+1, n+2,
	)
	args = append(args, pageSize, offset)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("audit_logs: query: %w", err)
	}
	defer rows.Close()

	var entries []store.AuditLogEntry
	var total int64
	for rows.Next() {
		e, t, err := scanAuditEntry(rows)
		if err != nil {
			return nil, 0, err
		}
		entries = append(entries, e)
		total = t
	}
	return entries, total, rows.Err()
}

// Export streams log entries matching filter to w in the given format ("json" or "csv").
func (s *AuditLogStore) Export(
	ctx context.Context,
	filter store.LogFilter,
	format string,
	w io.Writer,
) error {
	where, args := buildAuditWhere(filter)
	query := fmt.Sprintf(`
		SELECT id, timestamp, trace_id, session_id, user_id, user_email, client_ip::text,
		       server_id, server_name, mcp_method, tool_name, direction, status,
		       error_code, error_message, latency_ms, request_size, response_size,
		       rbac_decision, metadata, hash_chain, 0::bigint
		FROM audit_logs
		%s
		ORDER BY timestamp ASC, id ASC`,
		where,
	)

	rows, err := s.db.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("audit_logs: export: %w", err)
	}
	defer rows.Close()

	var cw *csv.Writer
	if format == "csv" {
		cw = csv.NewWriter(w)
		_ = cw.Write(auditCSVHeader())
	}

	for rows.Next() {
		e, _, err := scanAuditEntry(rows)
		if err != nil {
			return err
		}
		if cw != nil {
			_ = cw.Write(auditCSVRow(e))
		} else {
			b, _ := json.Marshal(e)
			_, _ = w.Write(b)
			_, _ = w.Write([]byte("\n"))
		}
	}
	if cw != nil {
		cw.Flush()
	}
	return rows.Err()
}

// VerifyHashChain re-derives the hash chain for all entries in [from, to] and
// returns false on the first mismatch. Entries are checked in ascending time order.
// genesisHash is used as the chain seed when no entries exist before from.
func (s *AuditLogStore) VerifyHashChain(ctx context.Context, from, to time.Time, genesisHash string) (bool, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, timestamp, trace_id, session_id, user_id, user_email, client_ip::text,
		       server_id, server_name, mcp_method, tool_name, direction, status,
		       error_code, error_message, latency_ms, request_size, response_size,
		       rbac_decision, metadata, hash_chain, 0::bigint
		FROM audit_logs
		WHERE timestamp >= $1 AND timestamp <= $2
		ORDER BY timestamp ASC, id ASC`,
		from, to,
	)
	if err != nil {
		return false, fmt.Errorf("audit_logs: verify query: %w", err)
	}
	defer rows.Close()

	var entries []store.AuditLogEntry
	for rows.Next() {
		e, _, err := scanAuditEntry(rows)
		if err != nil {
			return false, err
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(entries) == 0 {
		return true, nil
	}

	// Fetch the chain tip immediately before our window.
	// If no prior row exists the chain started inside this window; use genesisHash.
	prevHash := genesisHash
	_ = s.db.QueryRow(ctx, `
		SELECT hash_chain FROM audit_logs
		WHERE timestamp < $1 ORDER BY timestamp DESC, id DESC LIMIT 1`, from,
	).Scan(&prevHash)

	return auditVerifyChain(entries, prevHash)
}

// auditVerifyChain recomputes the SHA-256 chain for entries starting from prevHash.
// Avoids importing internal/logging (would be a cycle since logging imports store).
func auditVerifyChain(entries []store.AuditLogEntry, prevHash string) (bool, error) {
	prev := prevHash
	for i := range entries {
		stored := entries[i].HashChain

		type shadow store.AuditLogEntry
		tmp := shadow(entries[i])
		tmp.HashChain = ""
		tmp.ID = 0 // BIGSERIAL assigned after insert; was 0 at hash-compute time
		content, err := json.Marshal(tmp)
		if err != nil {
			return false, fmt.Errorf("audit_logs: verify marshal entry %d: %w", i, err)
		}

		combined := prev + string(content)
		sum := sha256.Sum256([]byte(combined))
		computed := fmt.Sprintf("%x", sum)

		if computed != stored {
			return false, nil
		}
		prev = computed
	}
	return true, nil
}

func buildAuditWhere(f store.LogFilter) (string, []any) {
	var conds []string
	var args []any

	add := func(cond string, val any) {
		args = append(args, val)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}

	if f.From != nil {
		add("timestamp >= $%d", *f.From)
	}
	if f.To != nil {
		add("timestamp <= $%d", *f.To)
	}
	if f.UserID != nil {
		add("user_id = $%d", *f.UserID)
	}
	if f.ServerID != nil {
		add("server_id = $%d", *f.ServerID)
	}
	if f.ToolName != "" {
		add("tool_name ILIKE $%d", "%"+f.ToolName+"%")
	}
	if f.MCPMethod != "" {
		add("mcp_method = $%d", f.MCPMethod)
	}
	if f.Status != "" {
		add("status = $%d", f.Status)
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		n := len(args)
		conds = append(conds, fmt.Sprintf("(user_email ILIKE $%d OR error_message ILIKE $%d)", n, n))
	}

	if len(conds) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(conds, " AND "), args
}

// rowScanner is satisfied by pgx.Row and pgx.Rows.
type auditRowScanner interface {
	Scan(dest ...any) error
}

func scanAuditEntry(row auditRowScanner) (store.AuditLogEntry, int64, error) {
	var e store.AuditLogEntry
	var total int64
	var sessionID, userID, serverID *[16]byte
	var clientIP *string
	var metaJSON []byte

	if err := row.Scan(
		&e.ID, &e.Timestamp, &e.TraceID,
		&sessionID, &userID, &e.UserEmail, &clientIP,
		&serverID, &e.ServerName,
		&e.MCPMethod, &e.ToolName, &e.Direction, &e.Status,
		&e.ErrorCode, &e.ErrorMessage,
		&e.LatencyMS, &e.RequestSize, &e.ResponseSize,
		&e.RBACDecision, &metaJSON, &e.HashChain,
		&total,
	); err != nil {
		return store.AuditLogEntry{}, 0, fmt.Errorf("audit_logs: scan: %w", err)
	}

	if sessionID != nil {
		e.SessionID = uuid.UUID(*sessionID)
	}
	if userID != nil {
		e.UserID = uuid.UUID(*userID)
	}
	if serverID != nil {
		e.ServerID = uuid.UUID(*serverID)
	}
	if clientIP != nil {
		// INET ::text includes the prefix length (e.g. "127.0.0.1/32"); strip it.
		e.ClientIP = strings.SplitN(*clientIP, "/", 2)[0]
	}
	// pgx decodes timestamptz with the local timezone; normalise to UTC so the
	// hash recomputed here matches what the pipeline computed before INSERT.
	e.Timestamp = e.Timestamp.UTC()
	if len(metaJSON) > 0 {
		_ = json.Unmarshal(metaJSON, &e.Metadata)
	}

	return e, total, nil
}

func nullUUID(id uuid.UUID) any {
	if id == uuid.Nil {
		return nil
	}
	return id
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func auditCSVHeader() []string {
	return []string{
		"id", "timestamp", "trace_id", "user_email", "client_ip",
		"server_name", "mcp_method", "tool_name", "direction",
		"status", "error_code", "latency_ms", "rbac_decision", "hash_chain",
	}
}

func auditCSVRow(e store.AuditLogEntry) []string {
	code := ""
	if e.ErrorCode != nil {
		code = fmt.Sprintf("%d", *e.ErrorCode)
	}
	return []string{
		fmt.Sprintf("%d", e.ID),
		e.Timestamp.Format(time.RFC3339Nano),
		e.TraceID, e.UserEmail, e.ClientIP,
		e.ServerName, e.MCPMethod, e.ToolName, e.Direction,
		e.Status, code,
		fmt.Sprintf("%d", e.LatencyMS),
		e.RBACDecision, e.HashChain,
	}
}
