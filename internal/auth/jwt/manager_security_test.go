package jwt_test

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	gojwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
)

// forgedClaims builds claims that match the test issuer so that only the
// signing method / signature differs — isolating the failure to the attack
// under test rather than an issuer/expiry mismatch.
func forgedClaims(roles []string) jwt.Claims {
	now := time.Now()
	return jwt.Claims{
		RegisteredClaims: gojwt.RegisteredClaims{
			Subject:   uuid.New().String(),
			Issuer:    "test-issuer",
			IssuedAt:  gojwt.NewNumericDate(now),
			NotBefore: gojwt.NewNumericDate(now),
			ExpiresAt: gojwt.NewNumericDate(now.Add(time.Minute)),
		},
		Email:     "attacker@evil.example",
		Roles:     roles,
		SessionID: uuid.New().String(),
	}
}

// TestValidate_NoneAlgorithmRejected ensures the classic "alg: none" forgery is
// rejected — the keyfunc accepts only RSA signing methods.
func TestValidate_NoneAlgorithmRejected(t *testing.T) {
	mgr := newTestManager(t)

	tok := gojwt.NewWithClaims(gojwt.SigningMethodNone, forgedClaims([]string{"admin"}))
	signed, err := tok.SignedString(gojwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none token: %v", err)
	}

	if _, err := mgr.Validate(signed); err == nil {
		t.Fatal("expected 'none' algorithm token to be rejected, got nil error")
	}
}

// TestValidate_HS256AlgorithmRejected covers the RS256→HS256 algorithm-confusion
// attack (attacker signs with HMAC using the public key as the secret). The
// keyfunc's RSA-only check rejects it before the key is ever consulted.
func TestValidate_HS256AlgorithmRejected(t *testing.T) {
	mgr := newTestManager(t)

	tok := gojwt.NewWithClaims(gojwt.SigningMethodHS256, forgedClaims([]string{"admin"}))
	signed, err := tok.SignedString([]byte("public-key-as-hmac-secret"))
	if err != nil {
		t.Fatalf("sign HS256 token: %v", err)
	}

	if _, err := mgr.Validate(signed); err == nil {
		t.Fatal("expected HS256 token to be rejected, got nil error")
	}
}

// TestValidate_TamperedRoles_Rejected issues a legitimate token, escalates the
// roles claim in the payload without re-signing, and asserts validation fails —
// proving roles cannot be modified without invalidating the RS256 signature.
func TestValidate_TamperedRoles_Rejected(t *testing.T) {
	mgr := newTestManager(t)

	token, err := mgr.Issue(uuid.New(), "user@example.com", []string{"viewer"}, uuid.New())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT segments, got %d", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	claims["roles"] = []string{"admin"} // privilege escalation attempt
	newPayload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	parts[1] = base64.RawURLEncoding.EncodeToString(newPayload)
	tampered := strings.Join(parts, ".")

	if _, err := mgr.Validate(tampered); err == nil {
		t.Fatal("expected role-tampered token to be rejected, got nil error")
	}
}
