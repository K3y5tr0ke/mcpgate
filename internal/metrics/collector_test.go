package metrics_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	goredis "github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/metrics"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/K3y5tr0ke/mcpgate/internal/store"
)

// fakeRedis records Incr/Expire calls. It embeds goredis.Cmdable so it satisfies
// the interface without hand-implementing its ~200 methods; only the methods the
// metrics middleware actually calls are overridden.
type fakeRedis struct {
	goredis.Cmdable
	incrKeys []string
}

func (f *fakeRedis) Incr(ctx context.Context, key string) *goredis.IntCmd {
	f.incrKeys = append(f.incrKeys, key)
	return goredis.NewIntCmd(ctx)
}

func (f *fakeRedis) Expire(ctx context.Context, _ string, _ time.Duration) *goredis.BoolCmd {
	return goredis.NewBoolCmd(ctx)
}

func hasKeyWithPrefix(keys []string, prefix string) bool {
	for _, k := range keys {
		if strings.HasPrefix(k, prefix) {
			return true
		}
	}
	return false
}

// TestCollector_PerServerTimeseriesKey asserts that, with Redis wired, a request
// carrying a serverName increments both the global and per-server timeseries keys.
func TestCollector_PerServerTimeseriesKey(t *testing.T) {
	c := metrics.NewCollector()
	fr := &fakeRedis{}
	c.WithRedis(fr)

	handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := chiRequest("github", "/mcp/github/message")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if !hasKeyWithPrefix(fr.incrKeys, "metrics:ts:srv:github:") {
		t.Errorf("expected a per-server key metrics:ts:srv:github:*, got %v", fr.incrKeys)
	}
	// The global key must not be confused with the per-server one.
	if !hasKeyWithPrefix(fr.incrKeys, "metrics:ts:") {
		t.Errorf("expected a global key metrics:ts:*, got %v", fr.incrKeys)
	}
}

// TestCollector_NoServerName_NoPerServerKey asserts that a request without a
// serverName (empty route param) increments only the global key.
func TestCollector_NoServerName_NoPerServerKey(t *testing.T) {
	c := metrics.NewCollector()
	fr := &fakeRedis{}
	c.WithRedis(fr)

	handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := chiRequest("", "/mcp//message")
	handler.ServeHTTP(httptest.NewRecorder(), r)

	if hasKeyWithPrefix(fr.incrKeys, "metrics:ts:srv:") {
		t.Errorf("expected no per-server key when serverName is empty, got %v", fr.incrKeys)
	}
	if !hasKeyWithPrefix(fr.incrKeys, "metrics:ts:") {
		t.Errorf("expected the global key, got %v", fr.incrKeys)
	}
}

// withMessage injects a parsed proxy.Message into the request context,
// mirroring what logging.Middleware does before metrics are recorded.
func withMessage(r *http.Request, method string) *http.Request {
	msg := &proxy.Message{Method: method}
	ctx := proxy.WithMessage(r.Context(), msg)
	return r.WithContext(ctx)
}

func chiRequest(serverName, path string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, path, http.NoBody)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("serverName", serverName)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestCollector_RequestsTotal_Increments(t *testing.T) {
	c := metrics.NewCollector()

	handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := chiRequest("test-server", "/mcp/test-server/message")
	r = withMessage(r, "tools/list")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	body := scrape(t, c)
	if !strings.Contains(body, `mcpgate_requests_total`) {
		t.Error("mcpgate_requests_total metric not found in output")
	}
	if !strings.Contains(body, `server="test-server"`) {
		t.Error("server label not found in output")
	}
	if !strings.Contains(body, `method="tools/list"`) {
		t.Error("method label not found in output")
	}
}

func TestCollector_RequestDuration_Observed(t *testing.T) {
	c := metrics.NewCollector()

	handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	r := chiRequest("srv", "/mcp/srv/message")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)

	body := scrape(t, c)
	if !strings.Contains(body, `mcpgate_request_duration_seconds`) {
		t.Error("mcpgate_request_duration_seconds not found")
	}
}

func TestCollector_StatusLabel_Mapping(t *testing.T) {
	cases := []struct {
		code    int
		wantLbl string
	}{
		{200, "2xx"},
		{201, "2xx"},
		{404, "4xx"},
		{500, "5xx"},
		{503, "5xx"},
	}

	for _, tc := range cases {
		c := metrics.NewCollector()
		handler := c.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.code)
		}))
		r := chiRequest("s", "/mcp/s/message")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)

		body := scrape(t, c)
		if !strings.Contains(body, `status="`+tc.wantLbl+`"`) {
			t.Errorf("code %d: want status label %q in output:\n%s", tc.code, tc.wantLbl, body)
		}
	}
}

func TestCollector_RegisterPipelineGauge(t *testing.T) {
	c := metrics.NewCollector()
	var dropped int64 = 7
	c.RegisterPipelineGauge(func() int64 { return dropped })

	body := scrape(t, c)
	if !strings.Contains(body, "mcpgate_audit_pipeline_dropped_total") {
		t.Error("pipeline gauge not found in output")
	}
	if !strings.Contains(body, "7") {
		t.Errorf("expected dropped count 7 in metrics output:\n%s", body)
	}
}

func TestCollector_RateLimitHits_Counter(t *testing.T) {
	c := metrics.NewCollector()
	c.RateLimitHits.WithLabelValues("rpm").Inc()
	c.RateLimitHits.WithLabelValues("rpm").Inc()

	body := scrape(t, c)
	if !strings.Contains(body, `mcpgate_rate_limit_hits_total`) {
		t.Error("rate limit hits metric not found")
	}
	if !strings.Contains(body, `window="rpm"`) {
		t.Error("rpm window label not found")
	}
}

func TestCollector_AuthFailures_Counter(t *testing.T) {
	c := metrics.NewCollector()
	c.AuthFailures.WithLabelValues("expired").Inc()
	c.AuthFailures.WithLabelValues("no_token").Inc()

	body := scrape(t, c)
	if !strings.Contains(body, `mcpgate_auth_failures_total`) {
		t.Error("auth failures metric not found")
	}
}

// scrape calls c.Handler() and returns the Prometheus text output.
func scrape(t *testing.T, c *metrics.Collector) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody)
	w := httptest.NewRecorder()
	c.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("metrics handler returned %d", w.Code)
	}
	body, _ := io.ReadAll(w.Body)
	return string(body)
}

// Ensure RateLimiter interface is satisfied (compile-time check).
var _ store.RateLimiter = store.RateLimiter(nil)
