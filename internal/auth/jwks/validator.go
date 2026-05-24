package jwks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// m2mClaims extends RegisteredClaims with the permissions claim used by Auth0
// and other OIDC providers to carry scopes/roles on M2M tokens.
type m2mClaims struct {
	jwt.RegisteredClaims
	Permissions []string `json:"permissions"`
}

// Validator validates externally-issued JWTs using JWKS from configured issuers.
// One Client per issuer is created lazily and cached for the process lifetime.
type Validator struct {
	store     store.JWKSConfigStore
	clientTTL time.Duration
	logger    *slog.Logger

	mu      sync.RWMutex
	clients map[string]*Client // issuer → Client
}

// NewValidator creates a Validator backed by the given store.
func NewValidator(s store.JWKSConfigStore, clientTTL time.Duration, logger *slog.Logger) *Validator {
	return &Validator{
		store:     s,
		clientTTL: clientTTL,
		logger:    logger,
		clients:   make(map[string]*Client),
	}
}

// ValidateToken looks up the issuer in jwks_configs, fetches/caches the JWKS
// public key, verifies the JWT, and returns the caller's UserIdentity.
// Implements auth.M2MValidator.
func (v *Validator) ValidateToken(ctx context.Context, tokenStr string) (*auth.UserIdentity, error) {
	iss, err := peekClaim(tokenStr, "iss")
	if err != nil {
		return nil, fmt.Errorf("jwks: read iss claim: %w", err)
	}
	if iss == "" {
		return nil, fmt.Errorf("jwks: token has no iss claim")
	}

	cfg, err := v.store.GetByIssuer(ctx, iss)
	if err != nil {
		return nil, fmt.Errorf("jwks: unknown issuer %q", iss)
	}

	client := v.clientFor(cfg.Issuer, cfg.JWKSURL)

	claims := &m2mClaims{}
	_, err = jwt.ParseWithClaims(tokenStr, claims,
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
			}
			kid, _ := t.Header["kid"].(string)
			return client.GetKey(ctx, kid)
		},
		jwt.WithIssuer(cfg.Issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, fmt.Errorf("jwks: token invalid: %w", err)
	}

	if cfg.Audience != "" {
		found := false
		for _, aud := range claims.Audience {
			if aud == cfg.Audience {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("jwks: audience mismatch: token aud=%v, want %q", claims.Audience, cfg.Audience)
		}
	}

	roles := claims.Permissions
	if roles == nil {
		roles = []string{}
	}

	return &auth.UserIdentity{
		UserID:    uuid.Nil,       // M2M clients have no users table row
		Email:     claims.Subject, // use sub as the identity string
		Roles:     roles,
		SessionID: uuid.Nil, // no session record for M2M
	}, nil
}

// clientFor returns the cached Client for the given issuer, creating one if needed.
func (v *Validator) clientFor(issuer, jwksURL string) *Client {
	v.mu.RLock()
	if c, ok := v.clients[issuer]; ok {
		v.mu.RUnlock()
		return c
	}
	v.mu.RUnlock()

	v.mu.Lock()
	defer v.mu.Unlock()
	if c, ok := v.clients[issuer]; ok {
		return c
	}
	c := NewClient(jwksURL, v.clientTTL)
	v.clients[issuer] = c
	return c
}

// peekClaim base64url-decodes the JWT payload and returns the named string claim.
// The signature is NOT verified — this is used only for routing.
func peekClaim(tokenStr, name string) (string, error) {
	parts := strings.SplitN(tokenStr, ".", 3)
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("decode payload: %w", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("unmarshal payload: %w", err)
	}
	val, _ := claims[name].(string)
	return val, nil
}
