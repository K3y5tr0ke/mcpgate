package logging

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Middleware instruments every request through the MCP proxy routes.
// It must be wired BEFORE the RBAC middleware so that 403 responses from
// denied requests are also captured. It pre-parses the request body and stores
// the *proxy.Message in context so the RBAC middleware can read it without
// re-parsing.
func Middleware(pipeline *Pipeline, serverStore store.ServerStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)

			// For POST requests, parse and store the message before calling next.
			// Restore the body so downstream middleware (RBAC, proxy) can re-read it.
			if r.Method == http.MethodPost && r.Body != nil {
				buf, err := io.ReadAll(r.Body)
				if err == nil {
					r.Body = io.NopCloser(bytes.NewReader(buf))
					if msg, err := proxy.Parse(buf); err == nil && msg != nil {
						r = r.WithContext(proxy.WithMessage(r.Context(), msg))
					}
					// Restore body again after Parse (Parse doesn't read from r.Body,
					// but reset for correctness in case the handler reads it).
					r.Body = io.NopCloser(bytes.NewReader(buf))
				}
			}

			next.ServeHTTP(ww, r)

			serverID := resolveServerID(r.Context(), serverStore, chi.URLParam(r, "serverName"))
			entry := BuildEntry(r, ww, serverID, start)
			pipeline.Submit(entry)
		})
	}
}

// resolveServerID looks up the server UUID by name with a short timeout.
// Returns uuid.Nil if the lookup fails or times out — acceptable; the log entry
// still records server_name.
func resolveServerID(ctx context.Context, ss store.ServerStore, name string) uuid.UUID {
	if name == "" {
		return uuid.Nil
	}
	lctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	srv, err := ss.GetByName(lctx, name)
	if err != nil {
		return uuid.Nil
	}
	return srv.ID
}
