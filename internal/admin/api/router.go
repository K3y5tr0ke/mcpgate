package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/rbac"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Router is the admin REST API sub-router.
type Router struct {
	servers  store.ServerStore
	idps     store.SAMLIDPConfigStore
	jwks     store.JWKSConfigStore
	roles    store.RoleStore
	users    store.UserStore
	enforcer *rbac.Enforcer
	logger   *slog.Logger
}

// NewRouter creates an admin API Router wired to the given stores.
func NewRouter(
	servers store.ServerStore,
	idps store.SAMLIDPConfigStore,
	jwks store.JWKSConfigStore,
	roles store.RoleStore,
	users store.UserStore,
	enforcer *rbac.Enforcer,
	logger *slog.Logger,
) *Router {
	return &Router{
		servers:  servers,
		idps:     idps,
		jwks:     jwks,
		roles:    roles,
		users:    users,
		enforcer: enforcer,
		logger:   logger,
	}
}

// Handler returns the chi http.Handler for all /api/v1/admin routes.
func (rt *Router) Handler() http.Handler {
	r := chi.NewRouter()

	sh := &serverHandler{servers: rt.servers, logger: rt.logger}
	ih := &idpHandler{idps: rt.idps, logger: rt.logger}
	jh := &jwksHandler{store: rt.jwks, logger: rt.logger}
	rh := &roleHandler{store: rt.roles, logger: rt.logger}
	uh := &userHandler{store: rt.users, logger: rt.logger}
	ph := &policyHandler{enforcer: rt.enforcer, logger: rt.logger}

	r.Route("/servers", func(r chi.Router) {
		r.Get("/", sh.list)
		r.Post("/", sh.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", sh.get)
			r.Put("/", sh.update)
			r.Delete("/", sh.delete)
		})
	})

	r.Route("/idps", func(r chi.Router) {
		r.Get("/", ih.list)
		r.Post("/", ih.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", ih.get)
			r.Put("/", ih.update)
			r.Delete("/", ih.delete)
		})
	})

	r.Route("/jwks/configs", func(r chi.Router) {
		r.Get("/", jh.list)
		r.Post("/", jh.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", jh.get)
			r.Put("/", jh.update)
			r.Delete("/", jh.delete)
		})
	})

	r.Route("/roles", func(r chi.Router) {
		r.Get("/", rh.list)
		r.Post("/", rh.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", rh.get)
			r.Put("/", rh.update)
			r.Delete("/", rh.delete)
			r.Post("/users", rh.assign)
			r.Delete("/users/{userID}", rh.revoke)
		})
	})

	r.Route("/users", func(r chi.Router) {
		r.Get("/", uh.list)
		r.Get("/{id}", uh.get)
		r.Patch("/{id}", uh.patch)
	})

	r.Route("/policies", func(r chi.Router) {
		r.Get("/", ph.list)
		r.Post("/", ph.add)
		r.Delete("/", ph.remove)
	})

	// Placeholder routes for later sprints.
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
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
