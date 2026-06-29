package rbac

import (
	"testing"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
)

// newModelEnforcer builds an in-memory Casbin enforcer from the production
// casbinModel string (no Postgres/Redis), so the matcher logic — wildcards,
// role isolation, deny-by-default — can be unit-tested in isolation. This is the
// security-critical authorization core; a regression in the model string would
// silently widen or break access.
func newModelEnforcer(t *testing.T, policies ...[3]string) *casbin.Enforcer {
	t.Helper()
	m, err := model.NewModelFromString(casbinModel)
	if err != nil {
		t.Fatalf("parse model: %v", err)
	}
	e, err := casbin.NewEnforcer(m)
	if err != nil {
		t.Fatalf("new enforcer: %v", err)
	}
	for _, p := range policies {
		if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
			t.Fatalf("add policy %v: %v", p, err)
		}
	}
	return e
}

func TestCasbinModel_Matching(t *testing.T) {
	e := newModelEnforcer(t,
		[3]string{"developer", "github", "call"}, // exact grant
		[3]string{"developer", "*", "list"},      // any server, list only
		[3]string{"ops", "jira", "*"},            // any action on jira
		[3]string{"admin", "*", "*"},             // full wildcard
	)

	cases := []struct {
		name          string
		sub, obj, act string
		want          bool
	}{
		{"exact match allows", "developer", "github", "call", true},
		{"exact match wrong action denies", "developer", "github", "write", false},
		{"wildcard obj allows any server for list", "developer", "billing", "list", true},
		{"wildcard obj does not widen action", "developer", "billing", "call", false},
		{"wildcard act allows any action on that server", "ops", "jira", "write", true},
		{"wildcard act scoped to its server", "ops", "github", "write", false},
		{"admin full wildcard allows anything", "admin", "anything", "write", true},
		{"deny-by-default: unknown role", "viewer", "github", "list", false},
		{"role isolation: dev grant not shared", "viewer", "github", "call", false},
		{"unknown action denied", "developer", "github", "exfiltrate", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := e.Enforce(c.sub, c.obj, c.act)
			if err != nil {
				t.Fatalf("enforce: %v", err)
			}
			if got != c.want {
				t.Errorf("Enforce(%q,%q,%q) = %v, want %v", c.sub, c.obj, c.act, got, c.want)
			}
		})
	}
}

// TestCasbinModel_NoPolicies_DeniesAll confirms the model is deny-by-default:
// with zero policies loaded, every request is rejected.
func TestCasbinModel_NoPolicies_DeniesAll(t *testing.T) {
	e := newModelEnforcer(t)
	for _, act := range []string{"list", "call", "read", "write", "connect", "*"} {
		ok, err := e.Enforce("anyone", "anyserver", act)
		if err != nil {
			t.Fatalf("enforce: %v", err)
		}
		if ok {
			t.Errorf("expected deny with no policies for action %q", act)
		}
	}
}
