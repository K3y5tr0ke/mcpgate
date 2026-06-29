package rbac

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
)

// Authorize is a chi middleware that enforces RBAC on /mcp/{serverName}/* routes.
//
// GET requests (SSE stream open) carry no JSON-RPC body and use action "connect"
// so that users must have explicit permission to establish the SSE channel — this
// prevents information leakage via server-push events and resource exhaustion.
//
// POST requests buffer the body, parse the JSON-RPC method, restore the body for
// the proxy handler, then enforce the mapped action.
func Authorize(enforcer *Enforcer, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := auth.MustIdentity(r.Context())
			serverName := chi.URLParam(r, "serverName")

			var action string
			if r.Method == http.MethodGet {
				// SSE stream open — no body, enforce the "connect" action.
				action = "connect"
			} else {
				// Logging middleware pre-parses the body and stores the message in
				// context; use it if available to avoid a second body read.
				if msg := proxy.MessageFromContext(r.Context()); msg != nil {
					action = proxy.Action(msg.Method)
				} else {
					buf, err := io.ReadAll(r.Body)
					if err != nil {
						writeJSONError(w, http.StatusBadRequest, "failed to read request body")
						return
					}
					r.Body = io.NopCloser(bytes.NewReader(buf))

					msg, parseErr := proxy.Parse(buf)
					if parseErr != nil || msg == nil {
						action = "unknown"
					} else {
						action = proxy.Action(msg.Method)
						r = r.WithContext(proxy.WithMessage(r.Context(), msg))
					}
				}
			}

			allowed, err := enforcer.Enforce(r.Context(), identity.Roles, serverName, action)
			if err != nil {
				// Fail CLOSED on enforcement error (e.g. policy store unreachable):
				// a transient failure must never grant access, regardless of the
				// configured default action.
				logger.Error("rbac: enforce error — denying", "server", serverName, "action", action, "err", err)
				allowed = false
			}

			if !allowed {
				writeJSONError(w, http.StatusForbidden, "forbidden")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole is a chi middleware that enforces a single required role on admin
// routes. Does not use Casbin — a simple slice scan is sufficient for the flat
// "admin role required" check on the admin API group.
func RequireRole(role string, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			identity := auth.MustIdentity(r.Context())
			for _, assigned := range identity.Roles {
				if assigned == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			logger.Info("rbac: admin access denied", "user", identity.Email, "required_role", role)
			writeJSONError(w, http.StatusForbidden, "forbidden")
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + message + `"}`))
}
