//go:build integration

package jwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/jwks"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

func TestValidator_Integration_EndToEnd(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	jwksStore := pgstore.NewJWKSConfigStore(db)
	ctx := context.Background()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		eBytes := big.NewInt(int64(key.PublicKey.E)).Bytes()
		body, _ := json.Marshal(map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": "k1",
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(eBytes),
				},
			},
		})
		w.Write(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "integration-test-issuer",
		Issuer:    "https://issuer.integration.example.com/",
		JWKSURL:   srv.URL,
		Audience:  "https://api.integration.example.com",
		IsEnabled: true,
	}
	if err := jwksStore.Create(ctx, cfg); err != nil {
		t.Fatalf("seed jwks_configs: %v", err)
	}

	type m2mClaims struct {
		jwt.RegisteredClaims
		Permissions []string `json:"permissions"`
	}
	claims := m2mClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    cfg.Issuer,
			Subject:   "service-account@m2m",
			Audience:  jwt.ClaimStrings{cfg.Audience},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Permissions: []string{"read", "write"},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	tokenStr, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	v := jwks.NewValidator(jwksStore, time.Hour, slog.Default())
	identity, err := v.ValidateToken(ctx, tokenStr)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if identity.Email != "service-account@m2m" {
		t.Errorf("Email: got %q, want %q", identity.Email, "service-account@m2m")
	}
	if len(identity.Roles) != 2 {
		t.Errorf("Roles: got %v, want 2 entries", identity.Roles)
	}
	if identity.UserID != uuid.Nil {
		t.Errorf("UserID: got %v, want Nil", identity.UserID)
	}
	if identity.SessionID != uuid.Nil {
		t.Errorf("SessionID: got %v, want Nil", identity.SessionID)
	}
}

func TestValidator_Integration_DisabledIssuer(t *testing.T) {
	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	jwksStore := pgstore.NewJWKSConfigStore(db)
	ctx := context.Background()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		eBytes := big.NewInt(int64(key.PublicKey.E)).Bytes()
		body, _ := json.Marshal(map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "kid": "k1",
					"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e": base64.RawURLEncoding.EncodeToString(eBytes)},
			},
		})
		w.Write(body) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "disabled-issuer",
		Issuer:    "https://disabled.integration.example.com/",
		JWKSURL:   srv.URL,
		Audience:  "",
		IsEnabled: false,
	}
	if err := jwksStore.Create(ctx, cfg); err != nil {
		t.Fatalf("seed jwks_configs: %v", err)
	}

	type m2mClaims struct {
		jwt.RegisteredClaims
		Permissions []string `json:"permissions"`
	}
	claims := m2mClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    cfg.Issuer,
			Subject:   "sub",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = "k1"
	tokenStr, err := tok.SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	v := jwks.NewValidator(jwksStore, time.Hour, slog.Default())
	_, err = v.ValidateToken(ctx, tokenStr)
	if err == nil {
		t.Fatal("expected error for disabled issuer")
	}
}
