package store

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"
)

// UserStore manages user records synced from SAML and managed via admin console.
type UserStore interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id uuid.UUID) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	GetByIDPSubject(ctx context.Context, idpName, subject string) (*User, error)
	List(ctx context.Context, filter UserFilter, page Pagination) ([]User, int64, error)
	Update(ctx context.Context, user *User) error
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
}

// ServerStore manages upstream MCP server configurations.
type ServerStore interface {
	Create(ctx context.Context, server *MCPServer) error
	GetByID(ctx context.Context, id uuid.UUID) (*MCPServer, error)
	GetByName(ctx context.Context, name string) (*MCPServer, error)
	List(ctx context.Context) ([]MCPServer, error)
	Update(ctx context.Context, server *MCPServer) error
	Delete(ctx context.Context, id uuid.UUID) error
	UpdateTools(ctx context.Context, serverID uuid.UUID, tools []ServerTool) error
}

// AuditLogStore manages the append-only audit log with hash-chain tamper evidence.
type AuditLogStore interface {
	BatchInsert(ctx context.Context, entries []AuditLogEntry) error
	Query(ctx context.Context, filter LogFilter, page Pagination) ([]AuditLogEntry, int64, error)
	Export(ctx context.Context, filter LogFilter, format string, w io.Writer) error
	VerifyHashChain(ctx context.Context, from, to time.Time) (bool, error)
}

// SessionStore manages durable session records in PostgreSQL (Redis is the cache).
type SessionStore interface {
	Create(ctx context.Context, session *Session) error
	GetByID(ctx context.Context, id uuid.UUID) (*Session, error)
	Delete(ctx context.Context, id uuid.UUID) error
	DeleteExpired(ctx context.Context) (int64, error)
}

// RoleStore manages roles and user-role assignments.
type RoleStore interface {
	Create(ctx context.Context, role *Role) error
	GetByID(ctx context.Context, id uuid.UUID) (*Role, error)
	GetByName(ctx context.Context, name string) (*Role, error)
	List(ctx context.Context) ([]Role, error)
	Update(ctx context.Context, role *Role) error
	Delete(ctx context.Context, id uuid.UUID) error
	AssignToUser(ctx context.Context, userID, roleID, assignedBy uuid.UUID) error
	RevokeFromUser(ctx context.Context, userID, roleID uuid.UUID) error
	GetUserRoles(ctx context.Context, userID uuid.UUID) ([]Role, error)
}

// SessionCache is the Redis-backed session cache (fast path for auth middleware).
type SessionCache interface {
	Set(ctx context.Context, sessionID uuid.UUID, data SessionData, ttl time.Duration) error
	Get(ctx context.Context, sessionID uuid.UUID) (*SessionData, error)
	Delete(ctx context.Context, sessionID uuid.UUID) error
	Extend(ctx context.Context, sessionID uuid.UUID, ttl time.Duration) error
}

// PolicyCache is the Redis-backed RBAC decision cache.
// nil return from Get means cache miss; *true = allow, *false = deny.
type PolicyCache interface {
	Get(ctx context.Context, key string) (*bool, error)
	Set(ctx context.Context, key string, allowed bool, ttl time.Duration) error
	Invalidate(ctx context.Context, pattern string) error
}

// RateLimiter enforces per-user and per-role rate limits using Redis sliding windows.
type RateLimiter interface {
	Allow(ctx context.Context, key string, limit RateLimit) (RateLimitResult, error)
}

// JWKSConfigStore manages trusted external JWT issuer configurations for M2M auth.
type JWKSConfigStore interface {
	Create(ctx context.Context, cfg *JWKSConfig) error
	GetByID(ctx context.Context, id uuid.UUID) (*JWKSConfig, error)
	GetByName(ctx context.Context, name string) (*JWKSConfig, error)
	GetByIssuer(ctx context.Context, issuer string) (*JWKSConfig, error) // auth hot path
	List(ctx context.Context) ([]JWKSConfig, error)
	ListEnabled(ctx context.Context) ([]JWKSConfig, error)
	Update(ctx context.Context, cfg *JWKSConfig) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// SAMLIDPConfigStore manages SAML Identity Provider configurations.
type SAMLIDPConfigStore interface {
	Create(ctx context.Context, cfg *SAMLIDPConfig) error
	GetByID(ctx context.Context, id uuid.UUID) (*SAMLIDPConfig, error)
	GetByName(ctx context.Context, name string) (*SAMLIDPConfig, error)
	List(ctx context.Context) ([]SAMLIDPConfig, error)
	ListEnabled(ctx context.Context) ([]SAMLIDPConfig, error)
	Update(ctx context.Context, cfg *SAMLIDPConfig) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// --- Domain types ---

type User struct {
	ID          uuid.UUID      `db:"id"`
	Email       string         `db:"email"`
	DisplayName string         `db:"display_name"`
	IDPSubject  string         `db:"idp_subject"`
	IDPName     string         `db:"idp_name"`
	Status      string         `db:"status"` // active, suspended, deactivated
	Metadata    map[string]any `db:"metadata"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

type Role struct {
	ID          uuid.UUID `db:"id"`
	Name        string    `db:"name"`
	Description string    `db:"description"`
	IsSystem    bool      `db:"is_system"`
	CreatedAt   time.Time `db:"created_at"`
}

type MCPServer struct {
	ID          uuid.UUID      `db:"id"`
	Name        string         `db:"name"`
	Description string         `db:"description"`
	Transport   string         `db:"transport"` // streamable_http, sse, stdio
	Config      map[string]any `db:"config"`
	HealthCheck map[string]any `db:"health_check"`
	IsEnabled   bool           `db:"is_enabled"`
	CreatedAt   time.Time      `db:"created_at"`
	UpdatedAt   time.Time      `db:"updated_at"`
}

type ServerTool struct {
	ServerID    uuid.UUID      `db:"server_id"`
	ToolName    string         `db:"tool_name"`
	Description string         `db:"description"`
	InputSchema map[string]any `db:"input_schema"`
}

type AuditLogEntry struct {
	ID           int64          `db:"id"`
	Timestamp    time.Time      `db:"timestamp"`
	TraceID      string         `db:"trace_id"`
	SessionID    uuid.UUID      `db:"session_id"`
	UserID       uuid.UUID      `db:"user_id"`
	UserEmail    string         `db:"user_email"`
	ClientIP     string         `db:"client_ip"`
	ServerID     uuid.UUID      `db:"server_id"`
	ServerName   string         `db:"server_name"`
	MCPMethod    string         `db:"mcp_method"`
	ToolName     string         `db:"tool_name"`
	Direction    string         `db:"direction"` // request, response, notification
	Status       string         `db:"status"`    // success, error, denied
	ErrorCode    *int           `db:"error_code"`
	ErrorMessage *string        `db:"error_message"`
	LatencyMS    int            `db:"latency_ms"`
	RequestSize  int            `db:"request_size"`
	ResponseSize int            `db:"response_size"`
	RBACDecision string         `db:"rbac_decision"`
	Metadata     map[string]any `db:"metadata"`
	HashChain    string         `db:"hash_chain"`
}

type Session struct {
	ID        uuid.UUID `db:"id"`
	UserID    uuid.UUID `db:"user_id"`
	ClientIP  string    `db:"client_ip"`
	UserAgent string    `db:"user_agent"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
}

type SessionData struct {
	UserID    uuid.UUID `json:"user_id"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	SessionID uuid.UUID `json:"session_id"`
}

// --- Filter and pagination types ---

type UserFilter struct {
	Email  string
	Role   string
	Status string
}

type LogFilter struct {
	From      *time.Time
	To        *time.Time
	UserID    *uuid.UUID
	ServerID  *uuid.UUID
	ToolName  string
	MCPMethod string
	Status    string
	Search    string
}

type Pagination struct {
	Page     int
	PageSize int
}

type RateLimit struct {
	RPM                   int
	RPD                   int
	ConcurrentConnections int
}

type RateLimitResult struct {
	Allowed    bool
	Remaining  int64
	ResetAt    time.Time
	RetryAfter time.Duration
}

type SAMLIDPConfig struct {
	ID           uuid.UUID         `db:"id"`
	Name         string            `db:"name"`
	EntityID     string            `db:"entity_id"`
	SSOURL       string            `db:"sso_url"`
	SLOURL       *string           `db:"slo_url"` // nullable
	Certificate  string            `db:"certificate"`
	AttributeMap map[string]string `db:"attribute_map"`  // JSONB
	GroupRoleMap map[string]string `db:"group_role_map"` // JSONB
	IsEnabled    bool              `db:"is_enabled"`
	CreatedAt    time.Time         `db:"created_at"`
	UpdatedAt    time.Time         `db:"updated_at"`
}

// JWKSConfig represents a trusted external JWT issuer.
// The Issuer field is matched exactly against the JWT iss claim.
// Audience == "" means skip the audience check.
type JWKSConfig struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	Issuer    string    `db:"issuer"`
	JWKSURL   string    `db:"jwks_url"`
	Audience  string    `db:"audience"`
	IsEnabled bool      `db:"is_enabled"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}
