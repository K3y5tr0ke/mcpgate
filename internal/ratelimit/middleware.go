package ratelimit

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/metrics"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// Middleware returns a chi-compatible middleware that enforces per-user rate limits.
// It must be placed after authMiddleware (identity is required) and after
// logging.Middleware (parsed message is in context for JSON-RPC error response).
//
// When cfg.Enabled is false the middleware is a pass-through.
// When the limiter returns an error, the request is allowed through (fail-open).
// When overrides is non-nil, per-role overrides are fetched and the most-permissive
// (highest RPM, highest RPD) wins; a zero value means "use global default".
func Middleware(
	limiter store.RateLimiter,
	overrides store.RateLimitOverrideStore,
	cfg config.RateLimitingConfig,
	col *metrics.Collector,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !cfg.Enabled || limiter == nil {
				next.ServeHTTP(w, r)
				return
			}

			identity := auth.MustIdentity(r.Context())
			userKey := identity.UserID.String()

			limit := store.RateLimit{
				RPM: cfg.Default.RPM,
				RPD: cfg.Default.RPD,
			}

			// Apply most-permissive per-role override (fail-open on lookup errors).
			if overrides != nil && len(identity.Roles) > 0 {
				bestRPM, bestRPD := 0, 0
				for _, role := range identity.Roles {
					entry, err := overrides.GetByRole(r.Context(), role)
					if err != nil {
						continue // ErrNotFound or transient error — skip
					}
					if entry.RPM > bestRPM {
						bestRPM = entry.RPM
					}
					if entry.RPD > bestRPD {
						bestRPD = entry.RPD
					}
				}
				if bestRPM > 0 {
					limit.RPM = bestRPM
				}
				if bestRPD > 0 {
					limit.RPD = bestRPD
				}
			}

			result, err := limiter.Allow(r.Context(), userKey, limit)
			if err != nil {
				logger.Warn("rate limiter error (fail open)", "user", userKey, "err", err)
				next.ServeHTTP(w, r)
				return
			}

			if !result.Allowed {
				window := "rpm"
				if limit.RPM == 0 {
					window = "rpd"
				}
				if col != nil {
					col.RateLimitHits.WithLabelValues(window).Inc()
				}
				writeRateLimitError(w, r, result.RetryAfter)
				return
			}

			if cfg.Default.ConcurrentConnections > 0 {
				overLimit, cleanup, cerr := limiter.StartConcurrent(r.Context(), userKey, cfg.Default.ConcurrentConnections)
				if cerr != nil {
					logger.Warn("concurrent limiter error (fail open)", "user", userKey, "err", cerr)
				} else {
					defer cleanup()
					if overLimit {
						if col != nil {
							col.RateLimitHits.WithLabelValues("concurrent").Inc()
						}
						writeRateLimitError(w, r, 0)
						return
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}

// writeRateLimitError writes a 429 response. For /mcp/* routes the body is a
// valid JSON-RPC error so MCP clients receive a well-formed response.
func writeRateLimitError(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	retrySeconds := int(retryAfter.Seconds())
	if retrySeconds < 1 {
		retrySeconds = 1
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", strconv.Itoa(retrySeconds))
	w.WriteHeader(http.StatusTooManyRequests)

	var msgID json.RawMessage
	if msg := proxy.MessageFromContext(r.Context()); msg != nil {
		msgID = msg.ID
	}

	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      msgID,
		"error": map[string]any{
			"code":    -32000,
			"message": "rate limit exceeded",
			"data":    map[string]any{"retry_after_seconds": retrySeconds},
		},
	})
	_, _ = w.Write(body)
}
