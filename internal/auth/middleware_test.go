package auth_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// --- Mocks ---

type mockValidator struct {
	claims *jwtpkg.Claims
	err    error
}

func (m *mockValidator) Validate(_ string) (*jwtpkg.Claims, error) {
	return m.claims, m.err
}

type mockM2MValidator struct {
	identity *auth.UserIdentity
	err      error
}

func (m *mockM2MValidator) ValidateToken(_ context.Context, _ string) (*auth.UserIdentity, error) {
	return m.identity, m.err
}

type mockSessionCache struct {
	data *store.SessionData
	err  error
}

func (m *mockSessionCache) Get(_ context.Context, _ uuid.UUID) (*store.SessionData, error) {
	return m.data, m.err
}
func (m *mockSessionCache) Set(_ context.Context, _ uuid.UUID, _ store.SessionData, _ time.Duration) error {
	return nil
}
func (m *mockSessionCache) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockSessionCache) Extend(_ context.Context, _ uuid.UUID, _ time.Duration) error {
	return nil
}

// --- Helpers ---

func validClaims(sessionID uuid.UUID) *jwtpkg.Claims {
	return &jwtpkg.Claims{
		Email:     "alice@example.com",
		Roles:     []string{"viewer"},
		SessionID: sessionID.String(),
	}
}

func validSessionData(userID, sessionID uuid.UUID) *store.SessionData {
	return &store.SessionData{
		UserID:    userID,
		Email:     "alice@example.com",
		Roles:     []string{"viewer"},
		SessionID: sessionID,
	}
}

func buildMiddleware(validator auth.TokenValidator, cache store.SessionCache) func(http.Handler) http.Handler {
	return auth.Authenticate(validator, nil, cache, config.AuthConfig{}, slog.Default())
}

// buildM2MToken constructs a minimal JWT with the given issuer for routing tests.
// The signature is intentionally fake — these tests only exercise routing, not verification.
func buildM2MToken(iss string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + iss + `"}`))
	return header + "." + payload + ".fakesig"
}

func applyMiddleware(mw func(http.Handler) http.Handler, next http.Handler) http.Handler {
	return mw(next)
}

func newRequest(token string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	return r
}

func bodyError(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var m map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&m); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return m["error"]
}

// --- Tests ---

func TestAuthenticate_NoToken(t *testing.T) {
	mw := buildMiddleware(&mockValidator{}, &mockSessionCache{})
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest(""))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if msg := bodyError(t, rec); msg != "authentication required" {
		t.Errorf("error message: %q", msg)
	}
}

func TestAuthenticate_InvalidToken(t *testing.T) {
	validator := &mockValidator{err: fmt.Errorf("jwt: invalid token: bad sig")}
	mw := buildMiddleware(validator, &mockSessionCache{})
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest("bad.token.here"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if msg := bodyError(t, rec); msg != "invalid token" {
		t.Errorf("error message: %q", msg)
	}
}

func TestAuthenticate_ExpiredToken(t *testing.T) {
	validator := &mockValidator{err: fmt.Errorf("jwt: token expired")}
	mw := buildMiddleware(validator, &mockSessionCache{})
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest("expired.token.here"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if msg := bodyError(t, rec); msg != "token expired" {
		t.Errorf("error message: %q", msg)
	}
}

func TestAuthenticate_SessionCacheMiss(t *testing.T) {
	sessionID := uuid.New()
	validator := &mockValidator{claims: validClaims(sessionID)}
	cache := &mockSessionCache{err: store.ErrNotFound}
	mw := buildMiddleware(validator, cache)
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest("valid.token.here"))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if msg := bodyError(t, rec); msg != "session expired or revoked" {
		t.Errorf("error message: %q", msg)
	}
}

func TestAuthenticate_ValidToken_IdentityInContext(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	validator := &mockValidator{claims: validClaims(sessionID)}
	cache := &mockSessionCache{data: validSessionData(userID, sessionID)}
	mw := buildMiddleware(validator, cache)

	var gotIdentity auth.UserIdentity
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			t.Error("identity not in context")
		}
		gotIdentity = id
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest("valid.token.here"))

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if gotIdentity.UserID != userID {
		t.Errorf("UserID: got %v, want %v", gotIdentity.UserID, userID)
	}
	if gotIdentity.Email != "alice@example.com" {
		t.Errorf("Email: got %q", gotIdentity.Email)
	}
	if len(gotIdentity.Roles) != 1 || gotIdentity.Roles[0] != "viewer" {
		t.Errorf("Roles: got %v", gotIdentity.Roles)
	}
}

func TestAuthenticate_CookieFallback(t *testing.T) {
	userID := uuid.New()
	sessionID := uuid.New()
	validator := &mockValidator{claims: validClaims(sessionID)}
	cache := &mockSessionCache{data: validSessionData(userID, sessionID)}
	mw := buildMiddleware(validator, cache)

	called := false
	handler := applyMiddleware(mw, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	r.AddCookie(&http.Cookie{Name: "mcpgate_session", Value: "cookie.token.here"}) //nolint:gosec // G124: test cookie, secure attributes not needed
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if !called {
		t.Error("next handler was not called")
	}
}

// --- M2M routing tests ---

func TestAuthenticate_M2M_ValidToken_IdentityInContext(t *testing.T) {
	userID := uuid.New()
	wantIdentity := &auth.UserIdentity{
		UserID: userID,
		Email:  "service@m2m",
		Roles:  []string{"read"},
	}
	m2m := &mockM2MValidator{identity: wantIdentity}
	mw := auth.Authenticate(
		&mockValidator{},
		m2m,
		&mockSessionCache{},
		config.AuthConfig{JWT: config.JWTConfig{Issuer: "mcpgate"}},
		slog.Default(),
	)

	token := buildM2MToken("https://external.example.com/")

	var gotIdentity auth.UserIdentity
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := auth.IdentityFromContext(r.Context())
		if !ok {
			t.Error("identity not in context")
		}
		gotIdentity = id
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest(token))

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want 200", rec.Code)
	}
	if gotIdentity.Email != "service@m2m" {
		t.Errorf("Email: got %q, want %q", gotIdentity.Email, "service@m2m")
	}
}

func TestAuthenticate_M2M_InvalidToken_Returns401(t *testing.T) {
	m2m := &mockM2MValidator{err: fmt.Errorf("invalid signature")}
	mw := auth.Authenticate(
		&mockValidator{},
		m2m,
		&mockSessionCache{},
		config.AuthConfig{JWT: config.JWTConfig{Issuer: "mcpgate"}},
		slog.Default(),
	)

	token := buildM2MToken("https://external.example.com/")
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest(token))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
	if msg := bodyError(t, rec); msg != "invalid token" {
		t.Errorf("error message: %q, want %q", msg, "invalid token")
	}
}

func TestAuthenticate_M2M_Disabled_ExternalIssuer_Returns401(t *testing.T) {
	mw := auth.Authenticate(
		&mockValidator{},
		nil, // M2M disabled
		&mockSessionCache{},
		config.AuthConfig{JWT: config.JWTConfig{Issuer: "mcpgate"}},
		slog.Default(),
	)

	token := buildM2MToken("https://external.example.com/")
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, newRequest(token))

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want 401", rec.Code)
	}
}
