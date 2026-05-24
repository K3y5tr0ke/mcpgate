package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	"github.com/google/uuid"
)

// TokenValidator is the interface Authenticate() requires for mcpgate session JWTs.
// *jwt.Manager satisfies it.
type TokenValidator interface {
	Validate(tokenString string) (*jwtpkg.Claims, error)
}

// M2MValidator validates externally-issued JWTs from configured JWKS providers.
// Nil disables M2M authentication. *jwks.Validator satisfies it.
type M2MValidator interface {
	ValidateToken(ctx context.Context, tokenString string) (*UserIdentity, error)
}

// Authenticate returns a chi middleware that enforces JWT authentication.
//
// Token extraction order:
//  1. Authorization: Bearer <token> header
//  2. mcpgate_session cookie
//
// Routing: if the JWT iss matches cfg.JWT.Issuer (or is empty), the session
// JWT path is taken (jwtpkg.Manager + Redis session liveness check). Otherwise
// the M2M path is taken if m2mValidator is non-nil; else 401.
func Authenticate(
	validator TokenValidator,
	m2mValidator M2MValidator,
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

			iss := peekIssuer(tokenStr)

			var identity UserIdentity

			switch {
			case iss == cfg.JWT.Issuer || iss == "":
				// Session JWT path — unchanged from Sprint 2.
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

				identity = UserIdentity{
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
			case m2mValidator != nil:
				// M2M path — external issuer resolved via JWKS.
				id, err := m2mValidator.ValidateToken(r.Context(), tokenStr)
				if err != nil {
					writeAuthError(w, "invalid token")
					return
				}
				identity = *id
			default:
				writeAuthError(w, "invalid token")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), identity)))
		})
	}
}

// peekIssuer decodes the JWT payload without verifying the signature and returns
// the iss claim. Used only to route to the correct validator — the chosen validator
// performs full signature verification.
func peekIssuer(tokenString string) string {
	parts := strings.SplitN(tokenString, ".", 3)
	if len(parts) != 3 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Issuer string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	return claims.Issuer
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
