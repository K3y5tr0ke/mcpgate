package jwks_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
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
)

func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- helpers ---

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func jwksBody(t *testing.T, kid string, pub *rsa.PublicKey) []byte { //nolint:unparam
	t.Helper()
	eBytes := big.NewInt(int64(pub.E)).Bytes()
	b, err := json.Marshal(map[string]any{
		"keys": []map[string]any{
			{
				"kty": "RSA",
				"kid": kid,
				"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(eBytes),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func signToken(t *testing.T, key *rsa.PrivateKey, kid, iss, sub, aud string, exp time.Time, permissions []string) string { //nolint:unparam
	t.Helper()
	type m2mClaims struct {
		jwt.RegisteredClaims
		Permissions []string `json:"permissions"`
	}
	claims := m2mClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss,
			Subject:   sub,
			Audience:  jwt.ClaimStrings{aud},
			ExpiresAt: jwt.NewNumericDate(exp),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
		Permissions: permissions,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// --- mock store ---

type mockStore struct {
	cfg *store.JWKSConfig
	err error
}

func (m *mockStore) GetByIssuer(_ context.Context, _ string) (*store.JWKSConfig, error) {
	return m.cfg, m.err
}
func (m *mockStore) Create(_ context.Context, _ *store.JWKSConfig) error { return nil }
func (m *mockStore) GetByID(_ context.Context, _ uuid.UUID) (*store.JWKSConfig, error) {
	return nil, store.ErrNotFound
}
func (m *mockStore) GetByName(_ context.Context, _ string) (*store.JWKSConfig, error) {
	return nil, store.ErrNotFound
}
func (m *mockStore) List(_ context.Context) ([]store.JWKSConfig, error)        { return nil, nil }
func (m *mockStore) ListEnabled(_ context.Context) ([]store.JWKSConfig, error) { return nil, nil }
func (m *mockStore) Update(_ context.Context, _ *store.JWKSConfig) error       { return nil }
func (m *mockStore) Delete(_ context.Context, _ uuid.UUID) error               { return nil }

// --- tests ---

func setup(t *testing.T) (key *rsa.PrivateKey, srv *httptest.Server, cfg *store.JWKSConfig) { //nolint:unparam
	t.Helper()
	key = newKey(t)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &key.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)
	cfg = &store.JWKSConfig{
		ID:        uuid.New(),
		Name:      "test",
		Issuer:    "https://test.example.com/",
		JWKSURL:   srv.URL,
		Audience:  "https://api.example.com",
		IsEnabled: true,
	}
	return
}

func TestValidator_ValidToken(t *testing.T) {
	key, _, cfg := setup(t)
	// Replace JWKS server URL in cfg after setup — use the real one.
	key2, srv2, cfg2 := setup(t)
	_ = key2
	_ = srv2
	_ = cfg2

	// Use dedicated key/srv/cfg for this test.
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)
	_ = key
	testCfg := &store.JWKSConfig{
		Issuer:   "https://issuer.example.com/",
		JWKSURL:  srv.URL,
		Audience: "https://api.example.com",
	}

	token := signToken(t, k, "k1",
		testCfg.Issuer, "client@m2m", testCfg.Audience,
		time.Now().Add(time.Hour), []string{"read", "write"})

	v := jwks.NewValidator(&mockStore{cfg: testCfg}, time.Hour, nil)
	identity, err := v.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if identity.Email != "client@m2m" {
		t.Errorf("Email: got %q", identity.Email)
	}
	if len(identity.Roles) != 2 {
		t.Errorf("Roles: got %v", identity.Roles)
	}
	_ = cfg
}

func TestValidator_UnknownIssuer(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	token := signToken(t, k, "k1",
		"https://unknown.example.com/", "sub", "aud",
		time.Now().Add(time.Hour), nil)

	v := jwks.NewValidator(&mockStore{err: store.ErrNotFound}, time.Hour, nil)
	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for unknown issuer")
	}
}

func TestValidator_ExpiredToken(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:   "https://issuer.example.com/",
		JWKSURL:  srv.URL,
		Audience: "aud",
	}
	token := signToken(t, k, "k1", cfg.Issuer, "sub", cfg.Audience,
		time.Now().Add(-time.Hour), nil) // already expired

	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestValidator_WrongAudience(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:   "https://issuer.example.com/",
		JWKSURL:  srv.URL,
		Audience: "https://correct.example.com",
	}
	token := signToken(t, k, "k1", cfg.Issuer, "sub",
		"https://wrong.example.com", time.Now().Add(time.Hour), nil)

	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	_, err := v.ValidateToken(context.Background(), token)
	if err == nil {
		t.Fatal("expected error for wrong audience")
	}
}

func TestValidator_EmptyAudience_SkipsCheck(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:   "https://issuer.example.com/",
		JWKSURL:  srv.URL,
		Audience: "", // skip aud check
	}
	token := signToken(t, k, "k1", cfg.Issuer, "sub",
		"any-audience", time.Now().Add(time.Hour), nil)

	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	if _, err := v.ValidateToken(context.Background(), token); err != nil {
		t.Fatalf("expected success with empty audience config: %v", err)
	}
}

func TestValidator_WrongAlgorithm(t *testing.T) {
	// Sign with HS256 instead of RS256.
	claims := jwt.RegisteredClaims{
		Issuer:    "https://issuer.example.com/",
		Subject:   "sub",
		Audience:  jwt.ClaimStrings{"aud"},
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tok.Header["kid"] = "k1"
	signed, _ := tok.SignedString([]byte("secret"))

	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:  "https://issuer.example.com/",
		JWKSURL: srv.URL,
	}
	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	_, err := v.ValidateToken(context.Background(), signed)
	if err == nil {
		t.Fatal("expected error for HS256 token")
	}
}

func TestValidator_PermissionsClaim(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:  "https://issuer.example.com/",
		JWKSURL: srv.URL,
	}
	token := signToken(t, k, "k1", cfg.Issuer, "sub", "aud",
		time.Now().Add(time.Hour), []string{"read", "write", "admin"})

	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	identity, err := v.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if len(identity.Roles) != 3 {
		t.Errorf("Roles: got %v, want [read write admin]", identity.Roles)
	}
}

// TestValidator_Invalidate verifies that invalidating an issuer forces the next
// validation to re-fetch the JWKS (a fresh HTTP hit), and that invalidating an
// unknown issuer is a harmless no-op.
func TestValidator_Invalidate(t *testing.T) {
	k := newKey(t)
	var fetches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetches++
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:  "https://issuer.example.com/",
		JWKSURL: srv.URL,
	}
	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, slogDiscard())

	mkToken := func() string {
		return signToken(t, k, "k1", cfg.Issuer, "sub", "aud", time.Now().Add(time.Hour), nil)
	}

	// First validation populates the cache (fetch #1).
	if _, err := v.ValidateToken(context.Background(), mkToken()); err != nil {
		t.Fatalf("first validate: %v", err)
	}
	// Second validation reuses the cached client — no new fetch.
	if _, err := v.ValidateToken(context.Background(), mkToken()); err != nil {
		t.Fatalf("second validate: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("before invalidate: got %d fetches, want 1", fetches)
	}

	// Invalidating an unknown issuer must not affect the cached one.
	v.Invalidate("https://unrelated.example.com/")
	if _, err := v.ValidateToken(context.Background(), mkToken()); err != nil {
		t.Fatalf("validate after no-op invalidate: %v", err)
	}
	if fetches != 1 {
		t.Fatalf("after no-op invalidate: got %d fetches, want 1", fetches)
	}

	// Invalidating the real issuer forces a re-fetch on the next validation.
	v.Invalidate(cfg.Issuer)
	if _, err := v.ValidateToken(context.Background(), mkToken()); err != nil {
		t.Fatalf("validate after invalidate: %v", err)
	}
	if fetches != 2 {
		t.Errorf("after invalidate: got %d fetches, want 2", fetches)
	}
}

func TestValidator_NoPermissions_EmptySlice(t *testing.T) {
	k := newKey(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(jwksBody(t, "k1", &k.PublicKey)) //nolint:errcheck,gosec
	}))
	t.Cleanup(srv.Close)

	cfg := &store.JWKSConfig{
		Issuer:  "https://issuer.example.com/",
		JWKSURL: srv.URL,
	}
	token := signToken(t, k, "k1", cfg.Issuer, "sub", "aud",
		time.Now().Add(time.Hour), nil)

	v := jwks.NewValidator(&mockStore{cfg: cfg}, time.Hour, nil)
	identity, err := v.ValidateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if identity.Roles == nil {
		t.Error("Roles should be empty slice, not nil")
	}
}
