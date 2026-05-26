package api

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/client_golang/prometheus"

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
		DroppedAuditLogs:  int64(sumGauge(mfs, "mcpgate_audit_pipeline_dropped_total")),
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
		type bkt struct{ ub, count float64 }
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

// timeseries returns bucketed request counts for the dashboard sparklines.
// This is a placeholder implementation that returns the current RPM as a single-point series.
// A full implementation would read per-minute Redis keys (planned for Sprint 8).
func (h *metricsHandler) timeseries(w http.ResponseWriter, r *http.Request) {
	type bucket struct {
		Requests int64 `json:"requests"`
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"buckets": []bucket{{Requests: h.collector.RequestsLastMinute()}},
	})
}

