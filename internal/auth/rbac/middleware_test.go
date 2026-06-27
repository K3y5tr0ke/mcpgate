package rbac

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestRequireRole enforces the flat admin-role check used by the admin API
// group: the wrapped handler runs only when the identity carries the required
// role; otherwise the request is rejected with 403.
func TestRequireRole(t *testing.T) {
	cases := []struct {
		name     string
		roles    []string
		wantNext bool
		wantCode int
	}{
		{"exact role allows", []string{"admin"}, true, http.StatusOK},
		{"role among several allows", []string{"developer", "admin"}, true, http.StatusOK},
		{"wrong role denied", []string{"developer"}, false, http.StatusForbidden},
		{"no roles denied", []string{}, false, http.StatusForbidden},
		{"nil roles denied", nil, false, http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var nextCalled bool
			h := RequireRole("admin", discardLogger())(http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					nextCalled = true
					w.WriteHeader(http.StatusOK)
				}))

			req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/servers", http.NoBody)
			req = req.WithContext(auth.WithIdentity(req.Context(), auth.UserIdentity{
				Email: "u@test", Roles: c.roles,
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if nextCalled != c.wantNext {
				t.Errorf("next called = %v, want %v", nextCalled, c.wantNext)
			}
			if rec.Code != c.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, c.wantCode)
			}
		})
	}
}
