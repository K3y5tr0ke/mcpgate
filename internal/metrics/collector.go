package metrics

import (
	"net/http"
	"sync"
	"time"

	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/K3y5tr0ke/mcpgate/internal/auth"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// MinuteCounter tracks requests in the previous complete minute for dashboards.
// It uses a fixed-window approach: when the current clock minute rolls over, the
// previous window's count is saved and the current counter resets. No goroutines needed.
type MinuteCounter struct {
	mu    sync.Mutex
	min   int   // clock minute (0-59) of current window
	count int64 // requests in the current (incomplete) minute
	prev  int64 // requests in the previous complete minute
}

func (c *MinuteCounter) Inc() {
	m := time.Now().Minute()
	c.mu.Lock()
	if m != c.min {
		c.prev = c.count
		c.count = 0
		c.min = m
	}
	c.count++
	c.mu.Unlock()
}

// Rate returns the number of requests counted in the previous complete minute.
func (c *MinuteCounter) Rate() int64 {
	c.mu.Lock()
	prev := c.prev
	c.mu.Unlock()
	return prev
}

// Collector holds all Prometheus metrics registered for mcpgate.
// It uses a dedicated Registry (not the default global) so test instances
// never contaminate one another.
type Collector struct {
	RequestsTotal   *prometheus.CounterVec
	RequestDuration *prometheus.HistogramVec
	ActiveRequests  *prometheus.GaugeVec
	RateLimitHits   *prometheus.CounterVec
	CircuitState    *prometheus.GaugeVec
	UpstreamErrors  *prometheus.CounterVec
	AuthFailures    *prometheus.CounterVec

	rpmCounter *MinuteCounter
	registry   *prometheus.Registry
}

// NewCollector registers all metrics and returns a ready-to-use Collector.
func NewCollector() *Collector {
	reg := prometheus.NewRegistry()

	c := &Collector{
		RequestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcpgate_requests_total",
			Help: "Total number of MCP proxy requests.",
		}, []string{"server", "method", "status", "auth_type"}),

		RequestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "mcpgate_request_duration_seconds",
			Help:    "End-to-end latency of MCP proxy requests.",
			Buckets: prometheus.DefBuckets,
		}, []string{"server", "method", "status"}),

		ActiveRequests: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mcpgate_active_requests",
			Help: "Number of in-flight MCP proxy requests.",
		}, []string{"server"}),

		RateLimitHits: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcpgate_rate_limit_hits_total",
			Help: "Requests rejected by the rate limiter.",
		}, []string{"window"}),

		CircuitState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mcpgate_circuit_breaker_state",
			Help: "Circuit breaker state per server: 0=closed, 1=open, 2=half-open.",
		}, []string{"server"}),

		UpstreamErrors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcpgate_upstream_errors_total",
			Help: "Transport errors contacting upstream MCP servers.",
		}, []string{"server", "error_type"}),

		AuthFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mcpgate_auth_failures_total",
			Help: "Authentication rejections by reason.",
		}, []string{"reason"}),

		rpmCounter: &MinuteCounter{},
		registry:   reg,
	}

	reg.MustRegister(
		c.RequestsTotal,
		c.RequestDuration,
		c.ActiveRequests,
		c.RateLimitHits,
		c.CircuitState,
		c.UpstreamErrors,
		c.AuthFailures,
	)

	return c
}

// RegisterPipelineGauge wires a GaugeFunc that reads pipeline.DroppedCount
// at scrape time. Call this after both the Collector and the Pipeline exist.
func (c *Collector) RegisterPipelineGauge(droppedFn func() int64) {
	g := prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "mcpgate_audit_pipeline_dropped_total",
		Help: "Cumulative count of audit log entries dropped due to full buffer.",
	}, func() float64 { return float64(droppedFn()) })
	c.registry.MustRegister(g)
}

// Handler returns an HTTP handler that serves the Prometheus text exposition format.
func (c *Collector) Handler() http.Handler {
	return promhttp.HandlerFor(c.registry, promhttp.HandlerOpts{})
}

// Gatherer exposes the internal registry as a prometheus.Gatherer.
// Used by the admin metrics summary endpoint to read histogram and counter data.
func (c *Collector) Gatherer() prometheus.Gatherer { return c.registry }

// RequestsLastMinute returns the number of requests counted in the previous
// complete clock minute. Returns 0 until the first minute boundary passes.
func (c *Collector) RequestsLastMinute() int64 { return c.rpmCounter.Rate() }

// Middleware returns a chi-compatible middleware that records per-request metrics.
// It must be placed inside the /mcp/{serverName} route group so chi can resolve
// the serverName URL parameter.
func (c *Collector) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			server := chi.URLParam(r, "serverName")

			c.ActiveRequests.WithLabelValues(server).Inc()
			c.rpmCounter.Inc()
			defer c.ActiveRequests.WithLabelValues(server).Dec()

			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			start := time.Now()
			next.ServeHTTP(ww, r)
			elapsed := time.Since(start).Seconds()

			method := ""
			if msg := proxy.MessageFromContext(r.Context()); msg != nil {
				method = msg.Method
			}

			authType := "session"
			if identity := auth.IdentityFromContextOrZero(r.Context()); identity.SessionID == uuid.Nil {
				authType = "m2m"
			}

			status := statusLabel(ww.Status())
			c.RequestsTotal.WithLabelValues(server, method, status, authType).Inc()
			c.RequestDuration.WithLabelValues(server, method, status).Observe(elapsed)
		})
	}
}

// RecordCircuitState implements proxy.ProxyMetrics.
func (c *Collector) RecordCircuitState(server string, state float64) {
	c.CircuitState.WithLabelValues(server).Set(state)
}

// RecordUpstreamError implements proxy.ProxyMetrics.
func (c *Collector) RecordUpstreamError(server, errorType string) {
	c.UpstreamErrors.WithLabelValues(server, errorType).Inc()
}

// statusLabel coerces an HTTP status code into a coarse string to bound label cardinality.
func statusLabel(code int) string {
	switch {
	case code >= 200 && code < 300:
		return "2xx"
	case code >= 300 && code < 400:
		return "3xx"
	case code >= 400 && code < 500:
		return "4xx"
	case code >= 500:
		return "5xx"
	default:
		return "unknown"
	}
}
