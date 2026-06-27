package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

type Config struct {
	Server       ServerConfig       `koanf:"server"`
	Database     DatabaseConfig     `koanf:"database"`
	Redis        RedisConfig        `koanf:"redis"`
	Auth         AuthConfig         `koanf:"auth"`
	SAML         SAMLConfig         `koanf:"saml"`
	Proxy        ProxyConfig        `koanf:"proxy"`
	RBAC         RBACConfig         `koanf:"rbac"`
	Logging      LoggingConfig      `koanf:"logging"`
	RateLimiting RateLimitingConfig `koanf:"rate_limiting"`
	Metrics      MetricsConfig      `koanf:"metrics"`
	Admin        AdminConfig        `koanf:"admin"`
	Servers      []ServerEntry      `koanf:"servers"`
}

type ServerConfig struct {
	Host           string        `koanf:"host"`
	Port           int           `koanf:"port"`
	MetricsPort    int           `koanf:"metrics_port"`
	TLS            TLSConfig     `koanf:"tls"`
	ReadTimeout    time.Duration `koanf:"read_timeout"`
	WriteTimeout   time.Duration `koanf:"write_timeout"`
	IdleTimeout    time.Duration `koanf:"idle_timeout"`
	MaxHeaderBytes int           `koanf:"max_header_bytes"`
}

type TLSConfig struct {
	Enabled  bool   `koanf:"enabled"`
	CertFile string `koanf:"cert_file"`
	KeyFile  string `koanf:"key_file"`
}

type DatabaseConfig struct {
	Host               string        `koanf:"host"`
	Port               int           `koanf:"port"`
	Name               string        `koanf:"name"`
	User               string        `koanf:"user"`
	Password           string        `koanf:"password"`
	SSLMode            string        `koanf:"ssl_mode"`
	MaxConnections     int32         `koanf:"max_connections"`
	MaxIdleConnections int32         `koanf:"max_idle_connections"`
	ConnMaxLifetime    time.Duration `koanf:"connection_max_lifetime"`
}

type RedisConfig struct {
	Host     string `koanf:"host"`
	Port     int    `koanf:"port"`
	Password string `koanf:"password"`
	DB       int    `koanf:"db"`
	PoolSize int    `koanf:"pool_size"`
}

type AuthConfig struct {
	SessionTTL     time.Duration    `koanf:"session_ttl"`
	SessionSliding bool             `koanf:"session_sliding"`
	JWT            JWTConfig        `koanf:"jwt"`
	M2M            M2MConfig        `koanf:"m2m"`
	BruteForce     BruteForceConfig `koanf:"brute_force"`
	// SessionRevalidateInterval is how often a long-lived WebSocket (the audit
	// log stream) re-checks that its session is still live, so a revoked session
	// is disconnected mid-stream rather than only at upgrade. Default 30s.
	SessionRevalidateInterval time.Duration `koanf:"session_revalidate_interval"`
}

type M2MConfig struct {
	JWKSCacheTTL time.Duration `koanf:"jwks_cache_ttl"` // default 1h
}

type BruteForceConfig struct {
	Enabled     bool          `koanf:"enabled"`
	MaxAttempts int           `koanf:"max_attempts"` // default 10
	Window      time.Duration `koanf:"window"`       // default 15m
	BlockFor    time.Duration `koanf:"block_for"`    // default 1h
}

type JWTConfig struct {
	Issuer          string        `koanf:"issuer"`
	AccessTokenTTL  time.Duration `koanf:"access_token_ttl"`
	RefreshTokenTTL time.Duration `koanf:"refresh_token_ttl"`
	SigningKeyFile  string        `koanf:"signing_key_file"`
}

type SAMLConfig struct {
	EntityID                   string            `koanf:"entity_id"`
	ACSURL                     string            `koanf:"acs_url"`
	MetadataURL                string            `koanf:"metadata_url"`
	CertificateFile            string            `koanf:"certificate_file"`
	PrivateKeyFile             string            `koanf:"private_key_file"`
	SignRequests               bool              `koanf:"sign_requests"`
	RequireEncryptedAssertions bool              `koanf:"require_encrypted_assertions"`
	AttributeMapping           map[string]string `koanf:"attribute_mapping"`
}

type ProxyConfig struct {
	DefaultTimeout time.Duration        `koanf:"default_timeout"`
	MaxRequestSize int64                `koanf:"max_request_size"`
	ConnectionPool ConnectionPoolConfig `koanf:"connection_pool"`
	CircuitBreaker CircuitBreakerConfig `koanf:"circuit_breaker"`
	HealthCheck    HealthCheckConfig    `koanf:"health_check"`
}

type ConnectionPoolConfig struct {
	MaxPerServer int           `koanf:"max_per_server"`
	IdleTimeout  time.Duration `koanf:"idle_timeout"`
}

type CircuitBreakerConfig struct {
	FailureThreshold    int           `koanf:"failure_threshold"`
	RecoveryTimeout     time.Duration `koanf:"recovery_timeout"`
	HalfOpenMaxRequests int           `koanf:"half_open_max_requests"`
}

type HealthCheckConfig struct {
	Interval time.Duration `koanf:"interval"`
	Timeout  time.Duration `koanf:"timeout"`
}

type RBACConfig struct {
	CacheTTL      time.Duration `koanf:"cache_ttl"`
	DefaultAction string        `koanf:"default_action"`
}

type LoggingConfig struct {
	Level     string          `koanf:"level"`
	Format    string          `koanf:"format"`
	Pipeline  PipelineConfig  `koanf:"pipeline"`
	Retention RetentionConfig `koanf:"retention"`
}

type PipelineConfig struct {
	BufferSize    int           `koanf:"buffer_size"`
	FlushInterval time.Duration `koanf:"flush_interval"`
	BatchSize     int           `koanf:"batch_size"`
	WriterCount   int           `koanf:"writer_count"`    // reserved; pipeline currently uses a single writer
	WALPath       string        `koanf:"wal_path"`        // "" = no WAL fallback
	HashChainSeed string        `koanf:"hash_chain_seed"` // sentinel prev-hash for first entry
}

type RetentionConfig struct {
	HotDays  int `koanf:"hot_days"`
	WarmDays int `koanf:"warm_days"`
}

type RateLimitingConfig struct {
	Enabled bool              `koanf:"enabled"`
	Default RateLimitDefaults `koanf:"default"`
}

type RateLimitDefaults struct {
	RPM                   int `koanf:"rpm"`
	RPD                   int `koanf:"rpd"`
	ConcurrentConnections int `koanf:"concurrent_connections"`
}

type MetricsConfig struct {
	Enabled bool       `koanf:"enabled"`
	OTLP    OTLPConfig `koanf:"otlp"`
}

type OTLPConfig struct {
	Enabled    bool    `koanf:"enabled"`
	Endpoint   string  `koanf:"endpoint"`
	Insecure   bool    `koanf:"insecure"`
	SampleRate float64 `koanf:"sample_rate"` // 0 = AlwaysSample; 0 < r ≤ 1 = ParentBased(TraceIDRatioBased(r))
}

// RateLimitOverride holds a per-role RPM/RPD override stored in the database.
// A value of 0 means "use the global default".
type RateLimitOverride struct {
	RoleName string `json:"role_name"`
	RPM      int    `json:"rpm"`
	RPD      int    `json:"rpd"`
}

type AdminConfig struct {
	WebRoot    string `koanf:"web_root"`
	CORSOrigin string `koanf:"cors_origin"`
}

type ServerEntry struct {
	Name      string         `koanf:"name"`
	Transport string         `koanf:"transport"`
	Config    map[string]any `koanf:"config"`
	Enabled   bool           `koanf:"enabled"`
}

func defaults() *Config {
	return &Config{
		Server: ServerConfig{
			Host:           "0.0.0.0",
			Port:           8080,
			MetricsPort:    9090,
			ReadTimeout:    30 * time.Second,
			WriteTimeout:   30 * time.Second,
			IdleTimeout:    120 * time.Second,
			MaxHeaderBytes: 1 << 20, // 1MB
		},
		Database: DatabaseConfig{
			Host:               "localhost",
			Port:               5432,
			Name:               "mcpgate",
			User:               "mcpgate",
			SSLMode:            "prefer",
			MaxConnections:     25,
			MaxIdleConnections: 5,
			ConnMaxLifetime:    time.Hour,
		},
		Redis: RedisConfig{
			Host:     "localhost",
			Port:     6379,
			DB:       0,
			PoolSize: 20,
		},
		Auth: AuthConfig{
			SessionTTL:                time.Hour,
			SessionSliding:            true,
			SessionRevalidateInterval: 30 * time.Second,
			JWT: JWTConfig{
				Issuer:          "mcpgate",
				AccessTokenTTL:  15 * time.Minute,
				RefreshTokenTTL: 7 * 24 * time.Hour,
			},
			M2M: M2MConfig{
				JWKSCacheTTL: time.Hour,
			},
			BruteForce: BruteForceConfig{
				Enabled:     true,
				MaxAttempts: 10,
				Window:      15 * time.Minute,
				BlockFor:    time.Hour,
			},
		},
		Proxy: ProxyConfig{
			DefaultTimeout: 30 * time.Second,
			MaxRequestSize: 10 << 20, // 10MB
			ConnectionPool: ConnectionPoolConfig{
				MaxPerServer: 50,
				IdleTimeout:  90 * time.Second,
			},
			CircuitBreaker: CircuitBreakerConfig{
				FailureThreshold:    5,
				RecoveryTimeout:     30 * time.Second,
				HalfOpenMaxRequests: 3,
			},
			HealthCheck: HealthCheckConfig{
				Interval: 30 * time.Second,
				Timeout:  5 * time.Second,
			},
		},
		RBAC: RBACConfig{
			CacheTTL:      30 * time.Second,
			DefaultAction: "deny",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
			Pipeline: PipelineConfig{
				BufferSize:    10000,
				FlushInterval: 100 * time.Millisecond,
				BatchSize:     500,
				WriterCount:   4,
				WALPath:       "",
				HashChainSeed: "genesis",
			},
			Retention: RetentionConfig{
				HotDays:  30,
				WarmDays: 90,
			},
		},
		RateLimiting: RateLimitingConfig{
			Enabled: true,
			Default: RateLimitDefaults{
				RPM:                   600,
				RPD:                   50000,
				ConcurrentConnections: 10,
			},
		},
		Metrics: MetricsConfig{
			Enabled: true,
			OTLP: OTLPConfig{
				Enabled:  false,
				Endpoint: "localhost:4317",
				Insecure: true,
			},
		},
		Admin: AdminConfig{
			WebRoot: "/web",
		},
	}
}

// Load reads config from the given YAML file and overlays MCPGATE_* env vars on top.
// Passing an empty configFile skips file loading and uses defaults + env only.
func Load(configFile string) (*Config, error) {
	k := koanf.New(".")
	cfg := defaults()

	if configFile != "" {
		if err := k.Load(file.Provider(configFile), yaml.Parser()); err != nil {
			return nil, fmt.Errorf("load config file %q: %w", configFile, err)
		}
	}

	if err := k.Load(env.Provider("MCPGATE_", ".", func(s string) string {
		return strings.ToLower(strings.TrimPrefix(s, "MCPGATE_"))
	}), nil); err != nil {
		return nil, fmt.Errorf("load env config: %w", err)
	}

	if err := k.UnmarshalWithConf("", cfg, koanf.UnmarshalConf{Tag: "koanf"}); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535, got %d", c.Server.Port)
	}
	if c.Database.Host == "" {
		return fmt.Errorf("database.host is required")
	}
	if c.Database.Name == "" {
		return fmt.Errorf("database.name is required")
	}
	if c.RBAC.DefaultAction != "allow" && c.RBAC.DefaultAction != "deny" {
		return fmt.Errorf("rbac.default_action must be 'allow' or 'deny', got %q", c.RBAC.DefaultAction)
	}
	return nil
}

// DatabaseDSN returns a DSN string suitable for pgx.
func (c *Config) DatabaseDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d dbname=%s user=%s password=%s sslmode=%s",
		c.Database.Host, c.Database.Port, c.Database.Name,
		c.Database.User, c.Database.Password, c.Database.SSLMode,
	)
}

// RedisAddr returns host:port for the Redis connection.
func (c *Config) RedisAddr() string {
	return fmt.Sprintf("%s:%d", c.Redis.Host, c.Redis.Port)
}
