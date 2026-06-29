//go:build integration

package saml_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	cssaml "github.com/crewjam/saml"
	"github.com/crewjam/saml/samlidp"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	goredis "github.com/redis/go-redis/v9"

	"github.com/jackc/pgx/v5/pgxpool"

	jwtpkg "github.com/K3y5tr0ke/mcpgate/internal/auth/jwt"
	"github.com/K3y5tr0ke/mcpgate/internal/auth/saml"
	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	redisstore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
)

// --- test helpers ---

func generateKeyPair(t *testing.T) (crypto.Signer, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return key, cert
}

func certPEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
}

// buildSAMLResponse creates a signed SAMLResponse using the test IdP and returns
// the base64-encoded value suitable for posting to an ACS endpoint.
func buildSAMLResponse(t *testing.T, idp *cssaml.IdentityProvider, sp *cssaml.ServiceProvider, email, nameID string) string {
	t.Helper()

	spMeta := sp.Metadata()
	if len(spMeta.SPSSODescriptors) == 0 || len(spMeta.SPSSODescriptors[0].AssertionConsumerServices) == 0 {
		t.Fatal("SP metadata has no ACS endpoints")
	}
	acsEndpoint := &spMeta.SPSSODescriptors[0].AssertionConsumerServices[0]

	req := &cssaml.IdpAuthnRequest{
		IDP:         idp,
		HTTPRequest: httptest.NewRequest(http.MethodPost, "/sso", http.NoBody),
		Now:         time.Now(),
		// Empty request ID → IdP-initiated flow: no InResponseTo in assertion.
		// The SP has AllowIDPInitiated: true, so ParseResponse accepts it.
		Request:                 cssaml.AuthnRequest{},
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

	assertionMaker := cssaml.DefaultAssertionMaker{}
	if err := assertionMaker.MakeAssertion(req, session); err != nil {
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

// --- test fixture ---

type testEnv struct {
	db           *pgxpool.Pool
	redisClient  *goredis.Client
	userStore    *pgstore.UserStore
	sessionStore *pgstore.SessionStore
	sessionCache *redisstore.SessionCache
	roleStore    *pgstore.RoleStore
	idpStore     *pgstore.SAMLIDPConfigStore
	jwtManager   *jwtpkg.Manager
	idpKey       crypto.Signer
	idpCert      *x509.Certificate
	idpCfg       *store.SAMLIDPConfig
	spCfg        config.SAMLConfig
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	redisClient := goredis.NewClient(&goredis.Options{Addr: "localhost:6379"})
	if err := redisClient.Ping(context.Background()).Err(); err != nil {
		t.Skipf("Redis not available: %v", err)
	}
	t.Cleanup(func() { _ = redisClient.Close() })

	idpKey, idpCert := generateKeyPair(t)
	_, spCert := generateKeyPair(t)

	idpCfg := &store.SAMLIDPConfig{
		ID:           uuid.New(),
		Name:         "test-idp",
		EntityID:     "https://idp.example.com",
		SSOURL:       "https://idp.example.com/sso",
		Certificate:  certPEM(idpCert),
		AttributeMap: map[string]string{"mail": "email", "cn": "display_name"},
		GroupRoleMap: map[string]string{},
		IsEnabled:    true,
	}

	idpStore := pgstore.NewSAMLIDPConfigStore(db)
	if err := idpStore.Create(context.Background(), idpCfg); err != nil {
		t.Fatalf("create IdP config: %v", err)
	}

	jwtMgr, err := jwtpkg.NewManager(config.JWTConfig{
		Issuer: "test", AccessTokenTTL: time.Minute,
	}, nopLogger())
	if err != nil {
		t.Fatalf("new JWT manager: %v", err)
	}

	env := &testEnv{
		db:           db,
		redisClient:  redisClient,
		userStore:    pgstore.NewUserStore(db),
		sessionStore: pgstore.NewSessionStore(db),
		sessionCache: redisstore.NewSessionCache(redisClient),
		roleStore:    pgstore.NewRoleStore(db),
		idpStore:     idpStore,
		jwtManager:   jwtMgr,
		idpKey:       idpKey,
		idpCert:      idpCert,
		idpCfg:       idpCfg,
		spCfg: config.SAMLConfig{
			EntityID:         "https://sp.example.com",
			ACSURL:           "https://sp.example.com/auth/saml/acs",
			MetadataURL:      "https://sp.example.com/auth/saml/metadata",
			AttributeMapping: map[string]string{"email": "email", "displayName": "display_name"},
		},
	}

	// Store SP cert PEM in spCfg (using the generated SP cert as ephemeral cert file).
	spCertPEM := certPEM(spCert)
	_ = spCertPEM // SP cert is ephemeral; spCfg uses empty file paths → ephemeral mode
	return env
}

func (e *testEnv) buildTestIDP(t *testing.T, acsURL string) *cssaml.IdentityProvider {
	t.Helper()
	acsU, _ := url.Parse(acsURL)
	spMeta := &cssaml.EntityDescriptor{
		EntityID: e.spCfg.EntityID,
		SPSSODescriptors: []cssaml.SPSSODescriptor{
			{
				SSODescriptor: cssaml.SSODescriptor{
					RoleDescriptor: cssaml.RoleDescriptor{
						ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol",
					},
				},
				AssertionConsumerServices: []cssaml.IndexedEndpoint{
					{
						Binding:  cssaml.HTTPPostBinding,
						Location: acsU.String(),
						Index:    1,
					},
				},
			},
		},
	}

	idpStore := samlidp.MemoryStore{}
	_ = idpStore.Put("/services/"+e.spCfg.EntityID, spMeta)

	idpServer, err := samlidp.New(samlidp.Options{
		URL:         url.URL{Scheme: "https", Host: "idp.example.com"},
		Key:         e.idpKey,
		Certificate: e.idpCert,
		Store:       &idpStore,
	})
	if err != nil {
		t.Fatalf("create samlidp: %v", err)
	}

	return &idpServer.IDP
}

func (e *testEnv) buildHandlers(t *testing.T, acsURL string) (*saml.Handlers, *cssaml.IdentityProvider) {
	t.Helper()

	// Override ACSURL with the live test-server URL so that ParseResponse can
	// validate the Recipient attribute in the assertion against sp.AcsURL.
	cfg := e.spCfg
	cfg.ACSURL = acsURL

	// Build the test IdP first so we can sync its EntityID into the stored config.
	// samlidp.New sets EntityID = MetadataURL = URL + "/metadata", not URL itself.
	idp := e.buildTestIDP(t, acsURL)

	// Sync stored IdP EntityID with what the test IdP actually advertises.
	e.idpCfg.EntityID = idp.Metadata().EntityID
	if err := e.idpStore.Update(context.Background(), e.idpCfg); err != nil {
		t.Fatalf("update IdP config EntityID: %v", err)
	}

	sp, err := saml.NewSP(cfg, e.idpStore, nopLogger())
	if err != nil {
		t.Fatalf("new SP: %v", err)
	}

	handlers := saml.NewHandlers(
		sp,
		e.userStore,
		e.sessionStore,
		e.sessionCache,
		e.roleStore,
		e.idpStore,
		e.jwtManager,
		e.redisClient,
		cfg,
		config.AuthConfig{SessionTTL: time.Hour},
		nopLogger(),
	)
	return handlers, idp
}

func (e *testEnv) acsRequest(t *testing.T, samlResponse string) *http.Request {
	t.Helper()
	form := url.Values{"SAMLResponse": {samlResponse}}
	r := httptest.NewRequest(http.MethodPost, "/auth/saml/acs", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// nopLogger returns a slog.Logger that discards all output.
func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// buildACSRouter wraps the handler in a chi router matching the test server path.
func buildACSRouter(handlers *saml.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Post("/auth/saml/acs", handlers.ACS)
	r.Get("/auth/saml/metadata", handlers.Metadata)
	r.Get("/auth/saml/logout", handlers.Logout)
	return r
}

// --- tests ---

func TestACS_NewUserJITProvision(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	acsURL := ts.URL + "/auth/saml/acs"

	handlers, idp := env.buildHandlers(t, acsURL)
	router := buildACSRouter(handlers)
	ts.Config.Handler = router // redirect test server to the real handler

	// Build signed SAML response for a new user.
	samlResp := buildSAMLResponse(t, idp,
		&cssaml.ServiceProvider{
			EntityID:    env.spCfg.EntityID,
			AcsURL:      mustParseURL(t, acsURL),
			MetadataURL: mustParseURL(t, env.spCfg.MetadataURL),
			IDPMetadata: idp.Metadata(),
		},
		"alice@example.com", "alice-subject",
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, env.acsRequest(t, samlResp))

	if rec.Code != http.StatusOK && rec.Code != http.StatusFound {
		t.Errorf("ACS: got status %d, want 200 or 302\nbody: %s", rec.Code, rec.Body.String())
	}

	// Verify user was JIT-provisioned.
	user, err := env.userStore.GetByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("user not created by JIT provision: %v", err)
	}
	if user.Status != "active" {
		t.Errorf("user status: got %q, want active", user.Status)
	}
}

func TestACS_ReplayDetection(t *testing.T) {
	env := newTestEnv(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	acsURL := ts.URL + "/auth/saml/acs"

	handlers, idp := env.buildHandlers(t, acsURL)
	router := buildACSRouter(handlers)
	ts.Config.Handler = router

	sp := &cssaml.ServiceProvider{
		EntityID:    env.spCfg.EntityID,
		AcsURL:      mustParseURL(t, acsURL),
		MetadataURL: mustParseURL(t, env.spCfg.MetadataURL),
		IDPMetadata: idp.Metadata(),
	}

	samlResp := buildSAMLResponse(t, idp, sp, "bob@example.com", "bob-subject")

	// First POST: should succeed (or at least not return replay error).
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, env.acsRequest(t, samlResp))

	// Second POST with same assertion: must return 400.
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, env.acsRequest(t, samlResp))
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("replay: got status %d, want 400", rec2.Code)
	}
}

func TestACS_SuspendedUser(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()
	acsURL := ts.URL + "/auth/saml/acs"

	handlers, idp := env.buildHandlers(t, acsURL)
	router := buildACSRouter(handlers)
	ts.Config.Handler = router

	// Pre-create a suspended user.
	suspendedUser := &store.User{
		ID: uuid.New(), Email: "suspended@example.com",
		IDPSubject: "suspended-subject", IDPName: "test-idp",
		Status: "suspended", Metadata: map[string]any{},
	}
	if err := env.userStore.Create(ctx, suspendedUser); err != nil {
		t.Fatalf("create suspended user: %v", err)
	}

	sp := &cssaml.ServiceProvider{
		EntityID:    env.spCfg.EntityID,
		AcsURL:      mustParseURL(t, acsURL),
		MetadataURL: mustParseURL(t, env.spCfg.MetadataURL),
		IDPMetadata: idp.Metadata(),
	}
	samlResp := buildSAMLResponse(t, idp, sp, "suspended@example.com", "suspended-subject")

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, env.acsRequest(t, samlResp))

	if rec.Code != http.StatusForbidden {
		t.Errorf("suspended user: got status %d, want 403", rec.Code)
	}
}

func mustParseURL(t *testing.T, s string) url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatalf("parse URL %q: %v", s, err)
	}
	return *u
}

// Needed for base64 in buildSAMLResponse.
var _ = base64.StdEncoding
