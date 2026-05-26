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
func Middleware(
	limiter store.RateLimiter,
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
