package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// Claims is the JWT payload issued by Manager.Issue.
type Claims struct {
	jwt.RegisteredClaims
	Email     string   `json:"email"`
	Roles     []string `json:"roles"`
	SessionID string   `json:"session_id"`
}

// Manager issues and validates RS256 JWTs.
type Manager struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
	issuer     string
	accessTTL  time.Duration
}

// NewManager creates a Manager from cfg.
// If cfg.SigningKeyFile is empty an ephemeral RSA-2048 key is generated in
// memory (dev mode); a warning is logged because tokens won't survive restart.
func NewManager(cfg config.JWTConfig, logger *slog.Logger) (*Manager, error) {
	var priv *rsa.PrivateKey

	if cfg.SigningKeyFile == "" {
		logger.Warn("JWT: using ephemeral signing key — tokens will not survive restart; set jwt.signing_key_file for production")
		var err error
		priv, err = rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return nil, fmt.Errorf("jwt: generate ephemeral key: %w", err)
		}
	} else {
		raw, err := os.ReadFile(cfg.SigningKeyFile)
		if err != nil {
			return nil, fmt.Errorf("jwt: read signing key %q: %w", cfg.SigningKeyFile, err)
		}
		priv, err = parseRSAPrivateKey(raw)
		if err != nil {
			return nil, fmt.Errorf("jwt: parse signing key: %w", err)
		}
	}

	ttl := cfg.AccessTokenTTL
	if ttl == 0 {
		ttl = 15 * time.Minute
	}

	return &Manager{
		privateKey: priv,
		publicKey:  &priv.PublicKey,
		issuer:     cfg.Issuer,
		accessTTL:  ttl,
	}, nil
}

// Issue creates and signs a JWT for the given user / session.
func (m *Manager) Issue(userID uuid.UUID, email string, roles []string, sessionID uuid.UUID) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID.String(),
			Issuer:    m.issuer,
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(m.accessTTL)),
		},
		Email:     email,
		Roles:     roles,
		SessionID: sessionID.String(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(m.privateKey)
	if err != nil {
		return "", fmt.Errorf("jwt: sign: %w", err)
	}
	return signed, nil
}

// Validate parses and verifies tokenString. It checks signature (RS256),
// expiry, issuer, and not-before. Session liveness is the caller's responsibility.
func (m *Manager) Validate(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("jwt: unexpected signing method: %v", t.Header["alg"])
		}
		return m.publicKey, nil
	}, jwt.WithIssuer(m.issuer), jwt.WithExpirationRequired())

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, fmt.Errorf("jwt: token expired")
		}
		return nil, fmt.Errorf("jwt: invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("jwt: invalid claims")
	}
	return claims, nil
}

// parseRSAPrivateKey tries PKCS8 then PKCS1 DER decoding.
func parseRSAPrivateKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM block found")
	}

	// Try PKCS8 first (openssl genpkey output).
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rk, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("PKCS8 key is not RSA")
		}
		return rk, nil
	}

	// Fall back to PKCS1 (openssl genrsa output).
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}
