package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/google/uuid"
)

// TokenValidator is the interface Authenticate() requires.
// *jwt.Manager satisfies it.
type TokenValidator interface {
	Validate(tokenString string) (*jwtpkg.Claims, error)
}

// Authenticate returns a chi middleware that enforces JWT authentication.
//
// Token extraction order:
//  1. Authorization: Bearer <token> header
//  2. mcpgate_session cookie
//
// On success the validated UserIdentity is stored in the request context via
// WithIdentity. Session liveness is checked against sessionCache; if the
// session is missing the token is rejected even if the JWT signature is valid.
func Authenticate(
	validator TokenValidator,
	sessionCache store.SessionCache,
	cfg config.AuthConfig,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tokenStr := extractToken(r)
			if tokenStr == "" {
				writeAuthError(w, "authentication required")
				return
			}

			claims, err := validator.Validate(tokenStr)
			if err != nil {
				msg := "invalid token"
				if err.Error() == "jwt: token expired" {
					msg = "token expired"
				}
				writeAuthError(w, msg)
				return
			}

			sessionID, err := uuid.Parse(claims.SessionID)
			if err != nil {
				writeAuthError(w, "invalid token")
				return
			}

			sessionData, err := sessionCache.Get(r.Context(), sessionID)
			if err != nil {
				writeAuthError(w, "session expired or revoked")
				return
			}

			identity := UserIdentity{
				UserID:    sessionData.UserID,
				Email:     sessionData.Email,
				Roles:     sessionData.Roles,
				SessionID: sessionID,
			}

			if cfg.SessionSliding {
				go func() {
					// Non-blocking: sliding expiry must not delay the proxied request.
					ttl := claims.ExpiresAt.Sub(claims.IssuedAt.Time)
					if err := sessionCache.Extend(r.Context(), sessionID, ttl); err != nil {
						logger.Warn("session extend failed", "session_id", sessionID, "err", err)
					}
				}()
			}

			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	if c, err := r.Cookie("mcpgate_session"); err == nil {
		return c.Value
	}
	return ""
}

func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
