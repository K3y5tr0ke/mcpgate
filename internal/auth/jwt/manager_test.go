package jwt_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

func newTestManager(t *testing.T) *jwt.Manager {
	t.Helper()

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}

	// Write PKCS1 PEM to a temp file.
	f, err := os.CreateTemp(t.TempDir(), "jwt-*.pem")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	defer f.Close() //nolint:errcheck
	if err := pem.Encode(f, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	}); err != nil {
		t.Fatalf("encode PEM: %v", err)
	}

	mgr, err := jwt.NewManager(config.JWTConfig{
		SigningKeyFile: f.Name(),
		Issuer:         "test-issuer",
		AccessTokenTTL: time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return mgr
}

func TestIssueValidateRoundTrip(t *testing.T) {
	mgr := newTestManager(t)

	userID := uuid.New()
	sessionID := uuid.New()
	roles := []string{"admin", "viewer"}

	token, err := mgr.Issue(userID, "alice@example.com", roles, sessionID)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	claims, err := mgr.Validate(token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}

	if claims.Subject != userID.String() {
		t.Errorf("Subject: got %q, want %q", claims.Subject, userID.String())
	}
	if claims.Email != "alice@example.com" {
		t.Errorf("Email: got %q", claims.Email)
	}
	if claims.SessionID != sessionID.String() {
		t.Errorf("SessionID: got %q", claims.SessionID)
	}
	if len(claims.Roles) != 2 || claims.Roles[0] != "admin" {
		t.Errorf("Roles: got %v", claims.Roles)
	}
	if claims.Issuer != "test-issuer" {
		t.Errorf("Issuer: got %q", claims.Issuer)
	}
}

func TestValidateExpiredToken(t *testing.T) {
	mgr, err := jwt.NewManager(config.JWTConfig{
		// Empty SigningKeyFile → ephemeral key (dev mode).
		Issuer:         "test-issuer",
		AccessTokenTTL: -time.Second, // already expired
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	token, err := mgr.Issue(uuid.New(), "bob@example.com", nil, uuid.New())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	_, err = mgr.Validate(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
	if err.Error() != "jwt: token expired" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateTamperedToken(t *testing.T) {
	mgr := newTestManager(t)

	token, err := mgr.Issue(uuid.New(), "carol@example.com", nil, uuid.New())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Replace the signature segment with a same-length string of 'A's.
	parts := strings.SplitN(token, ".", 3)
	parts[2] = strings.Repeat("A", len(parts[2]))
	tampered := strings.Join(parts, ".")
	_, err = mgr.Validate(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token, got nil")
	}
}

func TestValidateWrongIssuer(t *testing.T) {
	mgr1 := newTestManager(t)

	// mgr2 shares the same key file but a different issuer.
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	f, _ := os.CreateTemp(t.TempDir(), "jwt-*.pem")
	defer f.Close() //nolint:errcheck
	_ = pem.Encode(f, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	mgr2, err := jwt.NewManager(config.JWTConfig{
		SigningKeyFile: f.Name(),
		Issuer:         "other-issuer",
		AccessTokenTTL: time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager mgr2: %v", err)
	}

	// Issue with mgr2 (different key + issuer), validate with mgr1.
	token, _ := mgr2.Issue(uuid.New(), "dave@example.com", nil, uuid.New())
	_, err = mgr1.Validate(token)
	if err == nil {
		t.Fatal("expected error for wrong issuer/key, got nil")
	}
}

func TestDevModeEphemeralKey(t *testing.T) {
	mgr, err := jwt.NewManager(config.JWTConfig{
		Issuer:         "dev",
		AccessTokenTTL: time.Minute,
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewManager (ephemeral): %v", err)
	}

	token, err := mgr.Issue(uuid.New(), "dev@example.com", nil, uuid.New())
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if _, err := mgr.Validate(token); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}
