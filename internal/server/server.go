package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/admin/api"
	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	jwkspkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwks"
	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	localpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/local"
	"github.com/K3y5tr0ke/mcpgate/internal/auth/rbac"
	samlpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/saml"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/logging"
	logSinks "github.com/K3y5tr0ke/mcpgate/internal/logging/sinks"
	"github.com/K3y5tr0ke/mcpgate/internal/metrics"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/ratelimit"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	redisstore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
	"github.com/K3y5tr0ke/mcpgate/internal/tracing"
)

// Server is the main HTTP server wrapping the chi router and all dependencies.
type Server struct {
	cfg              *config.Config
	logger           *slog.Logger
	db               *pgxpool.Pool
	redis            *redis.Client
	proxyHandler     *proxy.Handler
	overrideStore    store.RateLimitOverrideStore
	adminRouter      *api.Router
	samlHandlers     *samlpkg.Handlers
	localHandlers    *localpkg.Handlers
	jwtManager       *jwtpkg.Manager
	sessionCache     *redisstore.SessionCache
	sessionStore     store.SessionStore
	enforcer         *rbac.Enforcer
	jwksValidator    *jwkspkg.Validator
	auditStore       *pgstore.AuditLogStore
	pipeline         *logging.Pipeline
	walSink          *logSinks.WALSink // nil if WALPath == ""
	serverStore      store.ServerStore
	authMiddleware   func(http.Handler) http.Handler
	metricsCollector *metrics.Collector
	tracingProvider  *tracing.Provider
	rateLimiter      store.RateLimiter
	metricsServer    *http.Server
	metricsAddr      string // actual listen address, e.g. "127.0.0.1:9090"
	http             *http.Server
	bgCancel         context.CancelFunc // stops background goroutines (eviction, cleanup, WAL, retention)
}

// New builds a Server and wires the router.
// Returns an error if JWT key loading or SAML SP initialisation fails.
func New(cfg *config.Config, logger *slog.Logger, db *pgxpool.Pool, redisClient *redis.Client) (*Server, error) {
	serverStore := pgstore.NewServerStore(db)
	userStore := pgstore.NewUserStore(db)
	sessionStore := pgstore.NewSessionStore(db)
	roleStore := pgstore.NewRoleStore(db)
	overrideStore := pgstore.NewRateLimitOverrideStore(db)
	samlIDPStore := pgstore.NewSAMLIDPConfigStore(db)
	jwksStore := pgstore.NewJWKSConfigStore(db)
	auditStore := pgstore.NewAuditLogStore(db)
	settingsStore := pgstore.NewSettingsStore(db)
	sessionCache := redisstore.NewSessionCache(redisClient)

	jwtManager, err := jwtpkg.NewManager(cfg.Auth.JWT, logger)
	if err != nil {
		return nil, fmt.Errorf("init JWT manager: %w", err)
	}

	samlSP, err := samlpkg.NewSP(cfg.SAML, samlIDPStore, logger)
	if err != nil {
		return nil, fmt.Errorf("init SAML SP: %w", err)
	}

	samlHandlers := samlpkg.NewHandlers(
		samlSP,
		userStore,
		sessionStore,
		sessionCache,
		roleStore,
		samlIDPStore,
		jwtManager,
		redisClient,
		cfg.SAML,
		cfg.Auth,
		logger,
	)

	jwksTTL := cfg.Auth.M2M.JWKSCacheTTL
	if jwksTTL == 0 {
		jwksTTL = time.Hour
	}
	jwksValidator := jwkspkg.NewValidator(jwksStore, jwksTTL, logger)

	authMiddleware := auth.Authenticate(jwtManager, jwksValidator, sessionCache, cfg.Auth, logger)

	policyCache := redisstore.NewPolicyCache(redisClient)
	enforcer, err := rbac.NewEnforcer(db, policyCache, cfg.RBAC, logger)
	if err != nil {
		return nil, fmt.Errorf("init rbac enforcer: %w", err)
	}
	if err := enforcer.SeedDefaultPolicies(context.Background()); err != nil {
		return nil, fmt.Errorf("seed rbac policies: %w", err)
	}

	// Metrics collector (dedicated registry — safe for concurrent test instances).
	collector := metrics.NewCollector()
	collector.WithRedis(redisClient)

	// Tracing provider (no-op when OTLP is disabled).
	tracingProvider, err := tracing.New(cfg.Metrics.OTLP, "mcpgate")
	if err != nil {
		return nil, fmt.Errorf("init tracing: %w", err)
	}

	// Per-user rate limiter (only when enabled).
	var rateLimiter store.RateLimiter
	if cfg.RateLimiting.Enabled {
		rl, err := redisstore.NewRateLimiter(redisClient)
		if err != nil {
			return nil, fmt.Errorf("init rate limiter: %w", err)
		}
		rateLimiter = rl
	}

	// Initialise audit log pipeline.
	initHash, err := auditStore.LatestHashChain(context.Background())
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("init hash chain: %w", err)
	}
	if initHash == "" {
		initHash = cfg.Logging.Pipeline.HashChainSeed
	}
	if initHash == "" {
		initHash = "genesis"
	}

	primarySink := logSinks.NewPostgresSink(auditStore)
	var fallbackSink logging.Sink
	var walSink *logSinks.WALSink
	if cfg.Logging.Pipeline.WALPath != "" {
		walSink = logSinks.NewWALSink(cfg.Logging.Pipeline.WALPath, logger)
		fallbackSink = walSink
	}

	pipeline := logging.NewPipeline(cfg.Logging.Pipeline, primarySink, fallbackSink, initHash, logger)

	// Start the pipeline with context.Background() so the final Drain() flush
	// succeeds even after the server's lifecycle context is cancelled.
	pipeline.Start(context.Background())

	// Wire pipeline dropped-count into the metrics gauge.
	collector.RegisterPipelineGauge(pipeline.DroppedCount)

	// Metrics HTTP server on the dedicated port (separate from the main API).
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", collector.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	metricsServer := &http.Server{
		Handler:           metricsMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	// Create the listener synchronously so MetricsAddr() is available immediately.
	metricsLn, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Server.MetricsPort))
	if err != nil {
		return nil, fmt.Errorf("metrics server listen: %w", err)
	}
	go func() {
		if err := metricsServer.Serve(metricsLn); err != nil && err != http.ErrServerClosed {
			logger.Error("metrics server error", "err", err)
		}
	}()

	// WAL crash recovery: if a .replaying file was left by a previous crash,
	// rename it back so the replay goroutine picks it up normally.
	if cfg.Logging.Pipeline.WALPath != "" {
		replayingPath := cfg.Logging.Pipeline.WALPath + ".replaying"
		if _, err := os.Stat(replayingPath); err == nil {
			_ = os.Rename(replayingPath, cfg.Logging.Pipeline.WALPath)
			logger.Warn("wal: .replaying file found on startup; crash recovery rename done")
		}
	}

	// Background goroutines use a cancellable context stopped in Shutdown/Run.
	bgCtx, bgCancel := context.WithCancel(context.Background())
	go jwksValidator.Start(bgCtx)
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				n, err := sessionStore.DeleteExpired(bgCtx)
				if err != nil {
					logger.Warn("session cleanup failed", "err", err)
				} else {
					logger.Info("session cleanup", "deleted", n)
				}
			case <-bgCtx.Done():
				return
			}
		}
	}()
	if walSink != nil {
		primary := logSinks.NewPostgresSink(auditStore)
		go func() {
			ticker := time.NewTicker(5 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if walSink.HasContent() {
						if err := walSink.Replay(bgCtx, primary); err != nil {
							logger.Error("WAL replay failed", "err", err)
						}
					}
				case <-bgCtx.Done():
					return
				}
			}
		}()
	}

	// Audit log retention: delete entries older than HotDays once per day.
	if cfg.Logging.Retention.HotDays > 0 {
		go func() {
			ticker := time.NewTicker(24 * time.Hour)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					n, err := auditStore.DeleteExpired(bgCtx, cfg.Logging.Retention.HotDays)
					if err != nil {
						logger.Warn("audit log retention cleanup failed", "err", err)
					} else if n > 0 {
						logger.Info("audit log retention cleanup", "deleted", n, "hot_days", cfg.Logging.Retention.HotDays)
					}
				case <-bgCtx.Done():
					return
				}
			}
		}()
	}

	proxyHandler := proxy.NewHandler(cfg.Proxy, serverStore, logger)
	proxyHandler.WithMetrics(collector)

	localHandlers := localpkg.NewHandlers(
		userStore, sessionStore, sessionCache, roleStore,
		jwtManager, redisClient, cfg.Auth, logger,
	)

	s := &Server{
		cfg:           cfg,
		logger:        logger,
		db:            db,
		redis:         redisClient,
		proxyHandler:  proxyHandler,
		overrideStore: overrideStore,
		adminRouter: api.NewRouter(
			serverStore, samlIDPStore, jwksStore, jwksValidator, roleStore, overrideStore, userStore,
			auditStore, settingsStore, pipeline, enforcer,
			proxyHandler, collector, db, redisClient, logger,
		),
		samlHandlers:     samlHandlers,
		localHandlers:    localHandlers,
		jwtManager:       jwtManager,
		sessionCache:     sessionCache,
		sessionStore:     sessionStore,
		enforcer:         enforcer,
		jwksValidator:    jwksValidator,
		auditStore:       auditStore,
		pipeline:         pipeline,
		walSink:          walSink,
		serverStore:      serverStore,
		authMiddleware:   authMiddleware,
		metricsCollector: collector,
		tracingProvider:  tracingProvider,
		rateLimiter:      rateLimiter,
		metricsServer:    metricsServer,
		metricsAddr:      metricsLn.Addr().String(),
		bgCancel:         bgCancel,
	}

	s.http = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: s.buildRouter(),
		// Bounds the time to read request headers — defends against slow-header
		// (slowloris) clients independently of the full-request ReadTimeout.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
	}

	return s, nil
}

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(s.tracingProvider.Middleware())
	r.Use(s.requestLogger)

	// Auth-exempt system endpoints.
	r.Get("/health", s.handleHealth)
	r.Get("/healthz", s.handleHealth) // Kubernetes liveness probe alias
	r.Get("/ready", s.handleReady)
	r.Get("/readyz", s.handleReady) // Kubernetes readiness probe alias

	// SAML auth routes — no auth middleware (these ARE the login flow).
	r.Route("/auth/saml", func(r chi.Router) {
		r.Get("/login", s.samlHandlers.Login)
		r.Post("/acs", s.samlHandlers.ACS)
		r.Get("/metadata", s.samlHandlers.Metadata)
		r.Get("/logout", s.samlHandlers.Logout)
	})

	// Local admin login — no auth middleware.
	r.Post("/auth/local/login", s.localHandlers.Login)

	// Identity endpoint — auth only, no role required.
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Get("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
			identity := auth.MustIdentity(r.Context())
			writeJSON(w, http.StatusOK, map[string]any{
				"user_id":    identity.UserID,
				"email":      identity.Email,
				"roles":      identity.Roles,
				"session_id": identity.SessionID,
			})
		})
	})

	// MCP proxy routes — auth → log (pre-parse body) → metrics → rate limit → RBAC → handler.
	r.Route("/mcp/{serverName}", func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Use(logging.Middleware(s.pipeline, s.serverStore))
		r.Use(s.metricsCollector.Middleware())
		r.Use(ratelimit.Middleware(s.rateLimiter, s.overrideStore, s.cfg.RateLimiting, s.metricsCollector, s.logger))
		r.Use(rbac.Authorize(s.enforcer, s.logger))
		r.Post("/message", s.proxyHandler.HandleStreamableHTTP)
		r.Get("/sse", s.proxyHandler.HandleSSEStream)
		r.Post("/sse", s.proxyHandler.HandleSSEMessage)
	})

	// Admin REST API — auth then admin role check (no Casbin).
	r.Group(func(r chi.Router) {
		r.Use(s.authMiddleware)
		r.Use(rbac.RequireRole("admin", s.logger))
		r.Mount("/api/v1/admin", s.adminRouter.Handler())
	})

	// Admin console SPA — served at /console/* without auth middleware so that
	// the browser can load the app before the JWT cookie is set.
	// The SPA itself redirects unauthenticated users to /auth/saml/login.
	// r.Mount strips the /console prefix before the handler sees r.URL.Path,
	// so consoleSPAHandler receives "/dashboard", "/assets/…", etc.
	r.Mount("/console", consoleSPAHandler())
	// Redirect bare /console to /console/dashboard.
	r.Get("/console", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/console/dashboard", http.StatusFound)
	})

	return r
}

// requestLogger is a minimal structured-logging middleware.
func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"latency_ms", time.Since(start).Milliseconds(),
			"request_id", chimw.GetReqID(r.Context()),
		)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	type componentStatus struct {
		Status    string `json:"status"`
		LatencyMS *int64 `json:"latency_ms,omitempty"`
		Detail    string `json:"detail,omitempty"`
	}

	overall := "ok"
	components := map[string]componentStatus{}

	// Database — critical component.
	t0 := time.Now()
	if err := s.db.Ping(ctx); err != nil {
		s.logger.Error("readiness: database unavailable", "error", err)
		overall = "error"
		components["database"] = componentStatus{Status: "error", Detail: err.Error()}
	} else {
		ms := time.Since(t0).Milliseconds()
		components["database"] = componentStatus{Status: "ok", LatencyMS: &ms}
	}

	// Redis — critical component.
	t0 = time.Now()
	if err := s.redis.Ping(ctx).Err(); err != nil {
		s.logger.Error("readiness: redis unavailable", "error", err)
		if overall == "ok" {
			overall = "error"
		}
		components["redis"] = componentStatus{Status: "error", Detail: err.Error()}
	} else {
		ms := time.Since(t0).Milliseconds()
		components["redis"] = componentStatus{Status: "ok", LatencyMS: &ms}
	}

	// Audit pipeline — non-critical (degraded, not error).
	dropped := s.pipeline.DroppedCount()
	pipelineStatus := "ok"
	if dropped > 0 {
		if overall == "ok" {
			overall = "degraded"
		}
		pipelineStatus = "degraded"
	}
	components["pipeline"] = componentStatus{Status: pipelineStatus, Detail: fmt.Sprintf("dropped_total=%d", dropped)}

	// Upstream circuit breaker states — non-critical.
	type upstreamStatus struct {
		Status       string `json:"status"`
		CircuitState string `json:"circuit_state"`
	}
	circuitStates := s.proxyHandler.ServerCircuitStates()
	upstreams := make(map[string]upstreamStatus, len(circuitStates))
	for server, state := range circuitStates {
		us := upstreamStatus{Status: "ok", CircuitState: state}
		if state == "open" || state == "half-open" {
			us.Status = "degraded"
			if overall == "ok" {
				overall = "degraded"
			}
		}
		upstreams[server] = us
	}

	status := http.StatusOK
	if overall == "error" {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{
		"status":     overall,
		"components": components,
		"upstreams":  upstreams,
	})
}

// Handler returns the HTTP handler. Used by integration tests.
func (s *Server) Handler() http.Handler { return s.http.Handler }

// MetricsAddr returns the actual listen address of the Prometheus metrics server,
// e.g. "127.0.0.1:9090". Used by integration tests to scrape metrics.
func (s *Server) MetricsAddr() string { return s.metricsAddr }

// JWTManager returns the JWT manager. Used by integration tests.
func (s *Server) JWTManager() *jwtpkg.Manager { return s.jwtManager }

// Enforcer returns the RBAC enforcer. Used by integration tests.
func (s *Server) Enforcer() *rbac.Enforcer { return s.enforcer }

// Shutdown stops background goroutines and drains the audit log pipeline.
// Called automatically by Run() on signal; also safe to call from tests.
func (s *Server) Shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Stop metrics scrapes and flush pending OTLP spans before goroutines exit.
	if s.metricsServer != nil {
		_ = s.metricsServer.Shutdown(ctx)
	}
	if s.tracingProvider != nil {
		_ = s.tracingProvider.Shutdown(ctx)
	}

	s.bgCancel()
	s.pipeline.Drain()
}

// Run starts the HTTP server and blocks until a shutdown signal is received.
// Graceful shutdown waits up to 30 seconds for in-flight requests, then calls
// Shutdown() to stop background goroutines and drain the audit log pipeline.
func (s *Server) Run() error {
	ln, err := net.Listen("tcp", s.http.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", s.http.Addr, err)
	}

	s.logger.Info("server starting", "addr", s.http.Addr)

	errCh := make(chan error, 1)
	go func() {
		if err := s.http.Serve(ln); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-errCh:
		s.Shutdown()
		return err
	case sig := <-quit:
		s.logger.Info("shutdown signal received", "signal", sig)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	// Drain pipeline after all in-flight handlers have completed.
	s.Shutdown()

	s.logger.Info("server stopped")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
