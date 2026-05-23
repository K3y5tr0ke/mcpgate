package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// Server is the main HTTP server wrapping the chi router and all dependencies.
type Server struct {
	cfg    *config.Config
	logger *slog.Logger
	db     *pgxpool.Pool
	redis  *redis.Client
	http   *http.Server
}

// New builds a Server and wires the router. Call Run to start accepting connections.
func New(cfg *config.Config, logger *slog.Logger, db *pgxpool.Pool, redisClient *redis.Client) *Server {
	s := &Server{
		cfg:    cfg,
		logger: logger,
		db:     db,
		redis:  redisClient,
	}

	s.http = &http.Server{
		Addr:           fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler:        s.buildRouter(),
		ReadTimeout:    cfg.Server.ReadTimeout,
		WriteTimeout:   cfg.Server.WriteTimeout,
		IdleTimeout:    cfg.Server.IdleTimeout,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}

	return s
}

func (s *Server) buildRouter() http.Handler {
	r := chi.NewRouter()

	r.Use(chimw.RealIP)
	r.Use(chimw.RequestID)
	r.Use(chimw.Recoverer)
	r.Use(s.requestLogger)

	// Auth-exempt system endpoints
	r.Get("/health", s.handleHealth)
	r.Get("/ready", s.handleReady)

	// Placeholder groups — populated in later sprints
	r.Group(func(r chi.Router) {
		// Proxy routes: /mcp/{serverName}/message, /mcp/{serverName}/sse
	})

	r.Group(func(r chi.Router) {
		// Admin API: /api/v1/admin/...
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

	if err := s.db.Ping(ctx); err != nil {
		s.logger.Error("readiness: database unavailable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "error", "detail": "database unavailable",
		})
		return
	}

	if err := s.redis.Ping(ctx).Err(); err != nil {
		s.logger.Error("readiness: redis unavailable", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "error", "detail": "redis unavailable",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Run starts the HTTP server and blocks until a shutdown signal is received.
// Graceful shutdown waits up to 30 seconds for in-flight requests to complete.
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
		return err
	case sig := <-quit:
		s.logger.Info("shutdown signal received", "signal", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}

	s.logger.Info("server stopped")
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
