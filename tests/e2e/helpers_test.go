//go:build integration

package e2e_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	cssaml "github.com/crewjam/saml"
	"github.com/crewjam/saml/samlidp"
	"github.com/google/uuid"

	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// --- session / JWT bootstrap ---

// createAdminToken inserts a user + session into the test stores and returns a
// signed JWT accepted by the test server's auth middleware.
// Use this to make authenticated admin API calls without going through SAML.
func (ts *testServer) createAdminToken(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	userID := uuid.New()
	if err := ts.userStore.Create(ctx, &store.User{
		ID:       userID,
		Email:    "admin-" + uuid.New().String()[:8] + "@test.internal",
		Status:   "active",
		Metadata: map[string]any{},
	}); err != nil {
		t.Fatalf("create admin user: %v", err)
	}

	sessionID := uuid.New()
	if err := ts.sessionStore.Create(ctx, &store.Session{
		ID:        sessionID,
		UserID:    userID,
		ClientIP:  "127.0.0.1",
		UserAgent: "e2e-test",
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	// Auth middleware reads session liveness from Redis via SessionCache.
	if err := ts.sessionCache.Set(ctx, sessionID, store.SessionData{
		UserID:    userID,
		Email:     "admin@test.internal",
		Roles:     []string{"admin"},
		SessionID: sessionID,
	}, time.Hour); err != nil {
		t.Fatalf("cache session: %v", err)
	}

	// Use the server's jwtManager so the signing key matches.
	token, err := ts.srv.JWTManager().Issue(userID, "admin@test.internal", []string{"admin"}, sessionID)
	if err != nil {
		t.Fatalf("issue JWT: %v", err)
	}
	return token
}

// --- synthetic SAML IdP ---

// syntheticIdP bundles the key material needed to build crewjam SAML responses.
type syntheticIdP struct {
	key crypto.Signer
	idp *cssaml.IdentityProvider
	cfg *store.SAMLIDPConfig
}

// setupSyntheticIdP generates an IdP key/cert, inserts a SAMLIDPConfig row, and
// returns a crewjam IdentityProvider that signs assertions the server SP accepts.
func (ts *testServer) setupSyntheticIdP(t *testing.T) *syntheticIdP {
	t.Helper()
	ctx := context.Background()

	key, cert := generateKeyPair(t)
	acsURL := ts.url + "/auth/saml/acs"
	spEntityID := ts.url + "/auth/saml/metadata"

	// Build the crewjam IdP first so we can derive the entity ID and SSO URL
	// it will embed in assertions, rather than hardcoding them.
	idp := buildCrumbIdP(t, key, cert, spEntityID, acsURL)

	idpCfg := &store.SAMLIDPConfig{
		ID:          uuid.New(),
		Name:        "synthetic-idp-" + uuid.New().String()[:8],
		EntityID:    idp.MetadataURL.String(), // e.g. "https://synthetic-idp.test/metadata"
		SSOURL:      idp.SSOURL.String(),
		Certificate: certToPEM(cert),
		AttributeMap: map[string]string{
			"mail":  "email", // crewjam DefaultAssertionMaker uses FriendlyName "mail"
			"email": "email", // direct name fallback
		},
		GroupRoleMap: map[string]string{},
		IsEnabled:    true,
	}
	if err := ts.idpStore.Create(ctx, idpCfg); err != nil {
		t.Fatalf("create synthetic IdP config: %v", err)
	}

	return &syntheticIdP{key: key, idp: idp, cfg: idpCfg}
}

// buildSAMLResponse returns a base64-encoded SAMLResponse for the given identity,
// signed by the synthetic IdP, ready to POST to the ACS endpoint.
func (ts *testServer) buildSAMLResponse(t *testing.T, idp *cssaml.IdentityProvider, email, nameID string) string {
	t.Helper()

	spEntityID := ts.url + "/auth/saml/metadata"
	acsURL := ts.url + "/auth/saml/acs"

	acsU, _ := url.Parse(acsURL)
	metaU, _ := url.Parse(spEntityID)

	sp := &cssaml.ServiceProvider{
		EntityID:    spEntityID,
		AcsURL:      *acsU,
		MetadataURL: *metaU,
		IDPMetadata: idp.Metadata(),
	}
	spMeta := sp.Metadata()

	if len(spMeta.SPSSODescriptors) == 0 || len(spMeta.SPSSODescriptors[0].AssertionConsumerServices) == 0 {
		t.Fatal("SP metadata has no ACS endpoints")
	}
	acsEndpoint := &spMeta.SPSSODescriptors[0].AssertionConsumerServices[0]

	req := &cssaml.IdpAuthnRequest{
		IDP:                     idp,
		HTTPRequest:             httptest.NewRequest(http.MethodPost, "/sso", nil),
		Now:                     time.Now(),
		Request:                 cssaml.AuthnRequest{ID: "_e2e-" + uuid.New().String()},
		ServiceProviderMetadata: spMeta,
		SPSSODescriptor:         &spMeta.SPSSODescriptors[0],
		ACSEndpoint:             acsEndpoint,
	}

	session := &cssaml.Session{
		ID:         uuid.New().String(),
		CreateTime: time.Now(),
		ExpireTime: time.Now().Add(time.Hour),
		NameID:     nameID,
		UserEmail:  email,
	}

	maker := cssaml.DefaultAssertionMaker{}
	if err := maker.MakeAssertion(req, session); err != nil {
		t.Fatalf("MakeAssertion: %v", err)
	}
	if err := req.MakeAssertionEl(); err != nil {
		t.Fatalf("MakeAssertionEl: %v", err)
	}
	if err := req.MakeResponse(); err != nil {
		t.Fatalf("MakeResponse: %v", err)
	}
	form, err := req.PostBinding()
	if err != nil {
		t.Fatalf("PostBinding: %v", err)
	}
	return form.SAMLResponse
}

// noRedirectClient is an HTTP client that does not follow redirects.
// ACS returns 302 on success; following it loses the Set-Cookie header.
var noRedirectClient = &http.Client{
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// postACS posts a SAMLResponse to the test server's ACS endpoint.
func (ts *testServer) postACS(t *testing.T, samlResponse string) *http.Response {
	t.Helper()
	body := strings.NewReader("SAMLResponse=" + url.QueryEscape(samlResponse))
	req, err := http.NewRequest(http.MethodPost, ts.url+"/auth/saml/acs", body)
	if err != nil {
		t.Fatalf("build ACS request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatalf("POST /auth/saml/acs: %v", err)
	}
	return resp
}

// buildCrumbIdP builds a crewjam IdentityProvider for use in tests.
func buildCrumbIdP(t *testing.T, key crypto.Signer, cert *x509.Certificate, spEntityID, acsURL string) *cssaml.IdentityProvider {
	t.Helper()

	acsU, _ := url.Parse(acsURL)
	spMeta := &cssaml.EntityDescriptor{
		EntityID: spEntityID,
		SPSSODescriptors: []cssaml.SPSSODescriptor{
			{
				SSODescriptor: cssaml.SSODescriptor{
					RoleDescriptor: cssaml.RoleDescriptor{
						ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
					},
				},
				AssertionConsumerServices: []cssaml.IndexedEndpoint{
					{Binding: cssaml.HTTPPostBinding, Location: acsU.String(), Index: 1},
				},
			},
		},
	}

	ms := samlidp.MemoryStore{}
	if err := ms.Put("/services/"+spEntityID, spMeta); err != nil {
		t.Fatalf("register SP in IdP store: %v", err)
	}

	idpURL, _ := url.Parse("https://synthetic-idp.test")
	idpServer, err := samlidp.New(samlidp.Options{
		URL:         *idpURL,
		Key:         key,
		Certificate: cert,
		Store:       &ms,
	})
	if err != nil {
		t.Fatalf("create samlidp: %v", err)
	}
	return &idpServer.IDP
}

// --- local JWKS server ---

// jwksServer is a local httptest server that serves a single RSA public key.
type jwksServer struct {
	*httptest.Server
	key    *rsa.PrivateKey
	kid    string
	issuer string
}

// newJWKSServer starts a local JWKS server for the given issuer string.
func newJWKSServer(t *testing.T, issuer string) *jwksServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	kid := uuid.New().String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"kid": kid,
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
				},
			},
		})
	}))
	t.Cleanup(srv.Close)

	return &jwksServer{Server: srv, key: key, kid: kid, issuer: issuer}
}

// issueToken signs a JWT using the JWKS server's private key.
func (j *jwksServer) issueToken(t *testing.T, audience string, exp time.Time, permissions []string) string {
	t.Helper()

	if permissions == nil {
		permissions = []string{}
	}

	now := time.Now()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": j.kid})
	payloadJSON, _ := json.Marshal(map[string]any{
		"iss":         j.issuer,
		"sub":         "m2m-client@test",
		"aud":         audience,
		"exp":         exp.Unix(),
		"iat":         now.Unix(),
		"permissions": permissions,
	})

	header := base64.RawURLEncoding.EncodeToString(headerJSON)
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	message := header + "." + payload

	digest := sha256.Sum256([]byte(message))
	sig, err := j.key.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	return message + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// --- key / cert helpers ---

func generateKeyPair(t *testing.T) (crypto.Signer, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return key, cert
}

func certToPEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}
