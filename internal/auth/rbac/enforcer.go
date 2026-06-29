package rbac

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxadapter "github.com/pckhoi/casbin-pgx-adapter/v3"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// casbinModel defines the RBAC policy model used by mcpgate.
//
// sub = role name, obj = MCP server name (or "*"), act = action category.
// Wildcards in policy rules match any value in the request.
const casbinModel = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && (p.obj == "*" || r.obj == p.obj) && (p.act == "*" || r.act == p.act)
`

// PolicyRule is the wire type for policy CRUD operations.
type PolicyRule struct {
	Subject  string `json:"subject"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
	Effect   string `json:"effect,omitempty"`
}

// Enforcer wraps a Casbin enforcer with a PolicyCache for fast decision lookups.
type Enforcer struct {
	e        *casbin.Enforcer
	cache    store.PolicyCache
	cacheTTL time.Duration
	logger   *slog.Logger
}

// NewEnforcer creates an Enforcer backed by Postgres (via the pgx adapter) and
// a Redis PolicyCache. WithSkipTableCreate is set because the table schema is
// managed by migration 003.
func NewEnforcer(
	db *pgxpool.Pool,
	cache store.PolicyCache,
	cfg config.RBACConfig,
	logger *slog.Logger,
) (*Enforcer, error) {
	adapter, err := pgxadapter.NewAdapter(
		nil,
		pgxadapter.WithConnectionPool(db),
		pgxadapter.WithSkipTableCreate(),
	)
	if err != nil {
		return nil, fmt.Errorf("rbac: create pgx adapter: %w", err)
	}

	m, err := model.NewModelFromString(casbinModel)
	if err != nil {
		return nil, fmt.Errorf("rbac: parse casbin model: %w", err)
	}

	e, err := casbin.NewEnforcer(m, adapter)
	if err != nil {
		return nil, fmt.Errorf("rbac: new enforcer: %w", err)
	}
	if err := e.LoadPolicy(); err != nil {
		return nil, fmt.Errorf("rbac: load policy: %w", err)
	}

	cacheTTL := cfg.CacheTTL
	if cacheTTL == 0 {
		cacheTTL = 30 * time.Second
	}

	return &Enforcer{
		e:        e,
		cache:    cache,
		cacheTTL: cacheTTL,
		logger:   logger,
	}, nil
}

// SeedDefaultPolicies ensures the admin wildcard policy exists. It is called
// from server.New() and is idempotent — Casbin returns false (not an error)
// when the policy already exists.
func (e *Enforcer) SeedDefaultPolicies(ctx context.Context) error {
	if _, err := e.e.AddPolicy("admin", "*", "*"); err != nil {
		return fmt.Errorf("rbac: seed admin policy: %w", err)
	}
	return nil
}

// Enforce returns true if any role in roles is permitted to perform action on
// serverName. Checks PolicyCache first and writes through on a miss.
func (e *Enforcer) Enforce(ctx context.Context, roles []string, serverName, action string) (bool, error) {
	for _, role := range roles {
		key := fmt.Sprintf("rbac:%s:%s:%s", role, serverName, action)

		if cached, err := e.cache.Get(ctx, key); err == nil && cached != nil {
			if *cached {
				return true, nil
			}
			continue
		}

		allowed, err := e.e.Enforce(role, serverName, action)
		if err != nil {
			return false, fmt.Errorf("rbac: enforce: %w", err)
		}

		if cacheErr := e.cache.Set(ctx, key, allowed, e.cacheTTL); cacheErr != nil {
			e.logger.Warn("rbac: policy cache set failed", "err", cacheErr)
		}

		if allowed {
			return true, nil
		}
	}
	return false, nil
}

// ReloadPolicy reloads rules from Postgres and invalidates the decision cache.
// Must be called after any policy mutation.
func (e *Enforcer) ReloadPolicy(ctx context.Context) error {
	if err := e.e.LoadPolicy(); err != nil {
		return fmt.Errorf("rbac: reload policy: %w", err)
	}
	if err := e.cache.Invalidate(ctx, "rbac:*"); err != nil {
		e.logger.Warn("rbac: cache invalidation failed after reload", "err", err)
	}
	return nil
}

// ListPolicies returns all 'p' rules as PolicyRule slices.
func (e *Enforcer) ListPolicies() []PolicyRule {
	rules, _ := e.e.GetPolicy()
	out := make([]PolicyRule, 0, len(rules))
	for _, r := range rules {
		if len(r) >= 3 {
			out = append(out, PolicyRule{Subject: r[0], Resource: r[1], Action: r[2], Effect: "allow"})
		}
	}
	return out
}

// AddPolicy adds a 'p' rule and reloads the enforcer.
func (e *Enforcer) AddPolicy(ctx context.Context, role, server, action string) error {
	if _, err := e.e.AddPolicy(role, server, action); err != nil {
		return fmt.Errorf("rbac: add policy: %w", err)
	}
	return e.ReloadPolicy(ctx)
}

// RemovePolicy removes a 'p' rule and reloads the enforcer.
func (e *Enforcer) RemovePolicy(ctx context.Context, role, server, action string) error {
	if _, err := e.e.RemovePolicy(role, server, action); err != nil {
		return fmt.Errorf("rbac: remove policy: %w", err)
	}
	return e.ReloadPolicy(ctx)
}
