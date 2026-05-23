package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Router is the admin REST API sub-router.
type Router struct {
	servers store.ServerStore
	logger  *slog.Logger
}

// NewRouter creates an admin API Router wired to the given stores.
func NewRouter(servers store.ServerStore, logger *slog.Logger) *Router {
	return &Router{servers: servers, logger: logger}
}

// Handler returns the chi http.Handler for all /api/v1/admin routes.
func (rt *Router) Handler() http.Handler {
	r := chi.NewRouter()

	sh := &serverHandler{servers: rt.servers, logger: rt.logger}

	r.Route("/servers", func(r chi.Router) {
		r.Get("/", sh.list)
		r.Post("/", sh.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", sh.get)
			r.Put("/", sh.update)
			r.Delete("/", sh.delete)
		})
	})

	// Placeholder routes for later sprints.
	r.Get("/users", notImplemented)
	r.Get("/roles", notImplemented)
	r.Get("/logs", notImplemented)
	r.Get("/metrics", notImplemented)

	return r
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	writeJSONError(w, http.StatusNotImplemented, "not yet implemented")
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
