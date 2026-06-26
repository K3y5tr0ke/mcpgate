package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	jwkspkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwks"
	"github.com/K3y5tr0ke/mcpgate/internal/auth/rbac"
	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Router is the admin REST API sub-router.
type Router struct {
	servers       store.ServerStore
	idps          store.SAMLIDPConfigStore
	jwks          store.JWKSConfigStore
	jwksValidator *jwkspkg.Validator
	roles         store.RoleStore
	overrides     store.RateLimitOverrideStore
	users         store.UserStore
	auditStore    store.AuditLogStore
	settings      store.SettingsStore
	pipeline      *logging.Pipeline
	enforcer      *rbac.Enforcer
	proxyHandler  *proxy.Handler
	collector     metricsProvider
	db            *pgxpool.Pool
	redis         *goredis.Client
	logger        *slog.Logger
}

// NewRouter creates an admin API Router wired to the given stores.
func NewRouter(
	servers store.ServerStore,
	idps store.SAMLIDPConfigStore,
	jwks store.JWKSConfigStore,
	jwksValidator *jwkspkg.Validator,
	roles store.RoleStore,
	overrides store.RateLimitOverrideStore,
	users store.UserStore,
	auditStore store.AuditLogStore,
	settings store.SettingsStore,
	pipeline *logging.Pipeline,
	enforcer *rbac.Enforcer,
	proxyHandler *proxy.Handler,
	collector metricsProvider,
	db *pgxpool.Pool,
	redis *goredis.Client,
	logger *slog.Logger,
) *Router {
	return &Router{
		servers:       servers,
		idps:          idps,
		jwks:          jwks,
		jwksValidator: jwksValidator,
		roles:         roles,
		overrides:     overrides,
		users:         users,
		auditStore:    auditStore,
		settings:      settings,
		pipeline:      pipeline,
		enforcer:      enforcer,
		proxyHandler:  proxyHandler,
		collector:     collector,
		db:            db,
		redis:         redis,
		logger:        logger,
	}
}

// Handler returns the chi http.Handler for all /api/v1/admin routes.
func (rt *Router) Handler() http.Handler {
	r := chi.NewRouter()

	sh := &serverHandler{servers: rt.servers, proxyHandler: rt.proxyHandler, logger: rt.logger}
	ih := &idpHandler{idps: rt.idps, logger: rt.logger}
	jh := &jwksHandler{store: rt.jwks, validator: rt.jwksValidator, logger: rt.logger}
	rh := &roleHandler{store: rt.roles, overrides: rt.overrides, logger: rt.logger}
	uh := &userHandler{store: rt.users, logger: rt.logger}
	ph := &policyHandler{enforcer: rt.enforcer, logger: rt.logger}
	lh := &logHandler{store: rt.auditStore, pipeline: rt.pipeline, logger: rt.logger}
	mh := &metricsHandler{collector: rt.collector, proxyHandler: rt.proxyHandler, db: rt.db, redis: rt.redis, logger: rt.logger}
	sth := &settingsHandler{store: rt.settings, logger: rt.logger}

	r.Route("/servers", func(r chi.Router) {
		r.Get("/", sh.list)
		r.Post("/", sh.create)
		r.Route("/{id}", func(r chi.Router) {
			r.Get("/", sh.get)
			r.Put("/", sh.update)
			r.Delete("/", sh.delete)
			r.Post("/test", sh.test)
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
			r.Post("/rotate", jh.rotate)
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
			r.Get("/rate-limits", rh.getRateLimits)
			r.Put("/rate-limits", rh.putRateLimits)
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

	r.Get("/logs", lh.list)
	r.Get("/logs/export", lh.export)
	r.Get("/logs/stream", lh.stream)
	r.Get("/logs/verify", lh.verify)

	r.Get("/metrics/summary", mh.summary)
	r.Get("/metrics/timeseries", mh.timeseries)

	r.Get("/settings", sth.get)
	r.Put("/settings", sth.put)

	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
