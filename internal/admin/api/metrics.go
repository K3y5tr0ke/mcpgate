package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	goredis "github.com/redis/go-redis/v9"

	"github.com/K3y5tr0ke/mcpgate/internal/proxy"
)

// metricsProvider is the interface the metrics handler needs from the Collector.
// Defined here to avoid importing the metrics package (would create a cycle with
// proxy → metrics → admin/api).
type metricsProvider interface {
	Gatherer() prometheus.Gatherer
	RequestsLastMinute() int64
}

type metricsHandler struct {
	collector    metricsProvider
	proxyHandler *proxy.Handler
	db           *pgxpool.Pool
	redis        *goredis.Client
	logger       *slog.Logger
}

type metricsSummary struct {
	RequestsPerMinute float64   `json:"requests_per_minute"`
	ErrorRatePercent  float64   `json:"error_rate_percent"`
	P95LatencyMS      float64   `json:"p95_latency_ms"`
	ActiveRequests    int64     `json:"active_requests"`
	ActiveSessions    int64     `json:"active_sessions"`
	CircuitBreakers   []cbState `json:"circuit_breakers"`
	DroppedAuditLogs  int64     `json:"dropped_audit_logs"`
}

type cbState struct {
	Server string `json:"server"`
	State  string `json:"state"`
}

func (h *metricsHandler) summary(w http.ResponseWriter, r *http.Request) {
	mfs, err := h.collector.Gatherer().Gather()
	if err != nil {
		h.logger.Error("metrics: gather", "err", err)
		writeJSONError(w, http.StatusInternalServerError, "failed to gather metrics")
		return
	}

	var activeSessions int64
	if h.db != nil {
		_ = h.db.QueryRow(r.Context(),
			`SELECT COUNT(*) FROM sessions WHERE expires_at > now()`,
		).Scan(&activeSessions)
	}

	// Build circuit breaker list.
	var cbs []cbState
	for server, state := range h.proxyHandler.ServerCircuitStates() {
		cbs = append(cbs, cbState{Server: server, State: state})
	}
	if cbs == nil {
		cbs = []cbState{}
	}

	s := metricsSummary{
		RequestsPerMinute: float64(h.collector.RequestsLastMinute()),
		ActiveRequests:    sumGauge(mfs, "mcpgate_active_requests"),
		P95LatencyMS:      histogramP95MS(mfs, "mcpgate_request_duration_seconds"),
		ErrorRatePercent:  errorRatePercent(mfs, "mcpgate_requests_total"),
		ActiveSessions:    activeSessions,
		CircuitBreakers:   cbs,
		DroppedAuditLogs:  sumGauge(mfs, "mcpgate_audit_pipeline_dropped_total"),
	}

	writeJSON(w, http.StatusOK, s)
}

// sumGauge sums all label combinations for a gauge metric family.
func sumGauge(mfs []*dto.MetricFamily, name string) int64 {
	for _, mf := range mfs {
		if mf.GetName() == name {
			var sum float64
			for _, m := range mf.GetMetric() {
				sum += m.GetGauge().GetValue()
			}
			return int64(sum)
		}
	}
	return 0
}

// errorRatePercent computes error rate from a counter vec with a "status" label.
// It treats 4xx and 5xx label values (as set by statusLabel) as errors.
func errorRatePercent(mfs []*dto.MetricFamily, name string) float64 {
	var total, errors float64
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			v := m.GetCounter().GetValue()
			total += v
			for _, lp := range m.GetLabel() {
				if lp.GetName() == "status" && (lp.GetValue() == "4xx" || lp.GetValue() == "5xx") {
					errors += v
				}
			}
		}
	}
	if total == 0 {
		return 0
	}
	return (errors / total) * 100
}

// histogramP95MS estimates the p95 latency in milliseconds from a histogram family.
// Uses linear interpolation within the highest non-empty bucket that contains the 95th percentile.
func histogramP95MS(mfs []*dto.MetricFamily, name string) float64 {
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		var totalCount uint64
		for _, m := range mf.GetMetric() {
			totalCount += m.GetHistogram().GetSampleCount()
		}
		if totalCount == 0 {
			return 0
		}
		target := float64(totalCount) * 0.95

		// Aggregate bucket counts across all label combinations.
		bucketMap := map[float64]float64{}
		for _, m := range mf.GetMetric() {
			for _, b := range m.GetHistogram().GetBucket() {
				bucketMap[b.GetUpperBound()] += float64(b.GetCumulativeCount())
			}
		}
		// Sort by upper bound and find the bucket containing the 95th percentile.
		type pair struct{ ub, count float64 }
		var pairs []pair
		for ub, cnt := range bucketMap {
			pairs = append(pairs, pair{ub, cnt})
		}
		// Insertion sort (small slice, typically ≤10 buckets).
		for i := 1; i < len(pairs); i++ {
			for j := i; j > 0 && pairs[j].ub < pairs[j-1].ub; j-- {
				pairs[j], pairs[j-1] = pairs[j-1], pairs[j]
			}
		}
		var prevBound, prevCount float64
		for _, p := range pairs {
			if p.count >= target {
				// Linearly interpolate within this bucket.
				if p.count == prevCount {
					return p.ub * 1000
				}
				frac := (target - prevCount) / (p.count - prevCount)
				return (prevBound + frac*(p.ub-prevBound)) * 1000
			}
			prevBound = p.ub
			prevCount = p.count
		}
		// Observation fell beyond the last bucket upper bound.
		if len(pairs) > 0 {
			return pairs[len(pairs)-1].ub * 1000
		}
	}
	return 0
}

// windowSpec defines, for a supported ?window= value, the total span and the
// display-bucket size — both in minutes. The bucket size is server-derived so
// the response stays bounded (≤ ~100 points) regardless of window length.
// The number of display buckets is totalMins / bucketMins.
type windowSpec struct {
	label      string
	totalMins  int
	bucketMins int
}

// windowSpecs is the lookup table of supported windows. Each window divides
// evenly by its bucket size (no ragged final bucket).
//
//	1h  →  60 ×  1m  (60 pts)
//	6h  → 360 ×  5m  (72 pts)
//	24h → 1440 × 15m (96 pts)
//	7d  → 10080 × 2h (84 pts)
//
// A future ?bucket= override can replace bucketMins here, clamped so that
// totalMins/bucketMins stays under a sane cap (~500).
var windowSpecs = map[string]windowSpec{
	"1h":  {label: "1h", totalMins: 60, bucketMins: 1},
	"6h":  {label: "6h", totalMins: 360, bucketMins: 5},
	"24h": {label: "24h", totalMins: 1440, bucketMins: 15},
	"7d":  {label: "7d", totalMins: 10080, bucketMins: 120},
}

// bucketLabel renders a bucket size in minutes as a compact string (e.g. "1m", "15m", "2h").
func bucketLabel(mins int) string {
	if mins%60 == 0 {
		return fmt.Sprintf("%dh", mins/60)
	}
	return fmt.Sprintf("%dm", mins)
}

// timeseries returns per-bucket request counts for the dashboard charts.
//
// Query params:
//   - window: one of 1h/6h/24h/7d (default 1h). Unknown values → 400.
//   - server: optional server name; when set, reads the per-server keys
//     "metrics:ts:srv:<server>:<minute>" instead of the global "metrics:ts:<minute>".
//
// The middleware only writes 1-minute keys, so for bucket sizes > 1m the handler
// sums every per-minute key spanning a display bucket. The response reports the
// chosen window, bucket size, and resolution_secs so the client can format axes.
// Falls back to a single-point series when Redis is unavailable.
func (h *metricsHandler) timeseries(w http.ResponseWriter, r *http.Request) {
	type bucket struct {
		Minute   int64 `json:"minute"` // unix minute (epoch seconds / 60) of the bucket's start
		Requests int64 `json:"requests"`
	}

	windowParam := r.URL.Query().Get("window")
	if windowParam == "" {
		windowParam = "1h"
	}
	spec, ok := windowSpecs[windowParam]
	if !ok {
		writeJSONError(w, http.StatusBadRequest, "invalid window (use 1h, 6h, 24h, or 7d)")
		return
	}

	server := r.URL.Query().Get("server")
	keyPrefix := "metrics:ts:"
	if server != "" {
		keyPrefix = fmt.Sprintf("metrics:ts:srv:%s:", server)
	}

	numBuckets := spec.totalMins / spec.bucketMins
	resolutionSecs := spec.bucketMins * 60
	now := time.Now().Unix() / 60

	respond := func(buckets []bucket) {
		writeJSON(w, http.StatusOK, map[string]any{
			"window":          spec.label,
			"bucket":          bucketLabel(spec.bucketMins),
			"resolution_secs": resolutionSecs,
			"buckets":         buckets,
		})
	}

	if h.redis == nil {
		// Without Redis we can only surface the in-process last-minute count, and
		// only for the global series (no per-server counter exists in memory).
		var req int64
		if server == "" {
			req = h.collector.RequestsLastMinute()
		}
		respond([]bucket{{Minute: now, Requests: req}})
		return
	}

	// The oldest minute we need is the start of the oldest display bucket.
	startMin := now - int64(numBuckets*spec.bucketMins) + 1

	ctx := r.Context()
	pipe := h.redis.Pipeline()
	totalMins := numBuckets * spec.bucketMins
	cmds := make([]*goredis.StringCmd, totalMins)
	for m := 0; m < totalMins; m++ {
		cmds[m] = pipe.Get(ctx, fmt.Sprintf("%s%d", keyPrefix, startMin+int64(m)))
	}
	_, _ = pipe.Exec(ctx)

	buckets := make([]bucket, numBuckets)
	for i := 0; i < numBuckets; i++ {
		bucketStart := startMin + int64(i*spec.bucketMins)
		buckets[i].Minute = bucketStart
		var sum int64
		for j := 0; j < spec.bucketMins; j++ {
			cmd := cmds[i*spec.bucketMins+j]
			if val, err := cmd.Result(); err == nil {
				if n, perr := strconv.ParseInt(val, 10, 64); perr == nil {
					sum += n
				}
			}
		}
		buckets[i].Requests = sum
	}
	respond(buckets)
}
