package logging

import (
	"net"
	"net/http"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// wrappedWriter is satisfied by chimw.WrapResponseWriter.
type wrappedWriter interface {
	Status() int
	BytesWritten() int
}

// BuildEntry constructs an AuditLogEntry from the request context after the
// proxy handler returns. serverID is uuid.Nil when the server name cannot be
// resolved (e.g. server not registered).
func BuildEntry(
	r *http.Request,
	ww wrappedWriter,
	serverID uuid.UUID,
	startTime time.Time,
) store.AuditLogEntry {
	ctx := r.Context()
	identity := auth.IdentityFromContextOrZero(ctx)
	msg := proxy.MessageFromContext(ctx) // nil for GET /sse (connect action)

	status := ww.Status()
	if status == 0 {
		status = http.StatusOK
	}

	entry := store.AuditLogEntry{
		Timestamp:    startTime,
		TraceID:      chimw.GetReqID(ctx),
		SessionID:    identity.SessionID,
		UserID:       identity.UserID,
		UserEmail:    identity.Email,
		ClientIP:     extractClientIP(r),
		ServerID:     serverID,
		ServerName:   chi.URLParam(r, "serverName"),
		Direction:    "response",
		LatencyMS:    int(time.Since(startTime).Milliseconds()),
		RequestSize:  int(r.ContentLength),
		ResponseSize: ww.BytesWritten(),
	}

	if msg != nil {
		entry.MCPMethod = msg.Method
		entry.ToolName = msg.ToolName()
	}

	switch status {
	case http.StatusForbidden:
		entry.Status = "denied"
		entry.RBACDecision = "deny"
	case http.StatusOK, http.StatusAccepted, http.StatusNoContent:
		entry.Status = "success"
		entry.RBACDecision = "allow"
	default:
		entry.Status = "error"
		code := status
		entry.ErrorCode = &code
	}

	return entry
}

func extractClientIP(r *http.Request) string {
	// Prefer X-Forwarded-For when behind a trusted proxy.
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
