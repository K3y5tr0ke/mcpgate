//go:build integration

package e2e_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/server"
	pgstore "github.com/K3y5tr0ke/mcpgate/internal/store/postgres"
	redisstore "github.com/K3y5tr0ke/mcpgate/internal/store/redis"
	"github.com/K3y5tr0ke/mcpgate/internal/testutil"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// testServer wraps a real server.Server running inside an httptest.Server.
// All store instances are exposed so tests can seed data directly.
type testServer struct {
	t            *testing.T
	url          string // base URL, e.g. "http://127.0.0.1:PORT"
	userStore    *pgstore.UserStore
	sessionStore *pgstore.SessionStore
	sessionCache *redisstore.SessionCache
	jwksStore    *pgstore.JWKSConfigStore
	idpStore     *pgstore.SAMLIDPConfigStore
	roleStore    *pgstore.RoleStore
	auditStore   store.AuditLogStore
	srv          *server.Server
}

// newTestServer spins up testcontainer Postgres + Redis, runs migrations, and
// starts a full server.Server on a random port via httptest.
func newTestServer(t *testing.T) *testServer {
	t.Helper()

	db := testutil.NewPostgresPool(t, pgstore.Migrations)
	redisClient := testutil.NewRedisClient(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Start the httptest server first so we know the base URL before building
	// the SAML config (which embeds the ACS and metadata URLs).
	httpSrv := httptest.NewUnstartedServer(http.NotFoundHandler())
	httpSrv.Start()
	t.Cleanup(httpSrv.Close)

	baseURL := httpSrv.URL

	cfg := buildTestConfig(baseURL)

	srv, err := server.New(cfg, logger, db, redisClient)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	// Replace the placeholder handler with the real server handler.
	httpSrv.Config.Handler = srv.Handler()
	t.Cleanup(srv.Shutdown)

	return &testServer{
		t:            t,
		url:          baseURL,
		userStore:    pgstore.NewUserStore(db),
		sessionStore: pgstore.NewSessionStore(db),
		// Independent SessionCache instance — shares the same Redis as the server,
		// so sessions inserted here are visible to the auth middleware.
		sessionCache: redisstore.NewSessionCache(redisClient),
		jwksStore:    pgstore.NewJWKSConfigStore(db),
		idpStore:     pgstore.NewSAMLIDPConfigStore(db),
		roleStore:    pgstore.NewRoleStore(db),
		auditStore:   pgstore.NewAuditLogStore(db),
		srv:          srv,
	}
}

func buildTestConfig(baseURL string) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:           "127.0.0.1",
			Port:           8080,
			ReadTimeout:    10 * time.Second,
			WriteTimeout:   10 * time.Second,
			IdleTimeout:    30 * time.Second,
			MaxHeaderBytes: 1 << 20,
		},
		Database: config.DatabaseConfig{
			Host: "localhost",
			Name: "test",
		},
		Redis: config.RedisConfig{
			Host: "localhost",
			Port: 6379,
		},
		Auth: config.AuthConfig{
			SessionTTL:     time.Hour,
			SessionSliding: false,
			JWT: config.JWTConfig{
				Issuer:         "mcpgate-test",
				AccessTokenTTL: time.Hour,
				SigningKeyFile: "",
			},
			M2M: config.M2MConfig{
				JWKSCacheTTL: 5 * time.Second,
			},
			BruteForce: config.BruteForceConfig{
				Enabled:     true,
				MaxAttempts: 5,
				Window:      time.Minute,
				BlockFor:    5 * time.Minute,
			},
		},
		SAML: config.SAMLConfig{
			EntityID:    baseURL + "/auth/saml/metadata",
			ACSURL:      baseURL + "/auth/saml/acs",
			MetadataURL: baseURL + "/auth/saml/metadata",
		},
		RBAC: config.RBACConfig{
			DefaultAction: "deny",
			CacheTTL:      30 * time.Second,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
			Pipeline: config.PipelineConfig{
				BufferSize:    1000,
				FlushInterval: 20 * time.Millisecond,
				BatchSize:     100,
				WriterCount:   1,
				HashChainSeed: "test-genesis",
			},
			Retention: config.RetentionConfig{HotDays: 30, WarmDays: 90},
		},
		Proxy: config.ProxyConfig{
			DefaultTimeout: 10 * time.Second,
			MaxRequestSize: 10 << 20,
			ConnectionPool: config.ConnectionPoolConfig{
				MaxPerServer: 10,
				IdleTimeout:  30 * time.Second,
			},
			CircuitBreaker: config.CircuitBreakerConfig{
				FailureThreshold:    5,
				RecoveryTimeout:     10 * time.Second,
				HalfOpenMaxRequests: 3,
			},
			HealthCheck: config.HealthCheckConfig{
				Interval: 30 * time.Second,
				Timeout:  5 * time.Second,
			},
		},
	}
}

// do sends a request to the test server and returns the response.
// Caller is responsible for closing the body.
func (ts *testServer) do(t *testing.T, method, path string, body io.Reader, headers map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.url+path, body)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}
