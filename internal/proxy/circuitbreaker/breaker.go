package circuitbreaker

import (
	"sync/atomic"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
)

// State represents the circuit breaker's current operating mode.
type State int32

const (
	StateClosed   State = 0 // normal operation
	StateOpen     State = 1 // fast-failing; no requests forwarded to upstream
	StateHalfOpen State = 2 // probing; limited requests allowed through
)

func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreaker is a lock-free, per-server circuit breaker.
// All state transitions use compare-and-swap so it is safe for concurrent use.
type CircuitBreaker struct {
	state         atomic.Int32 // stores State
	failures      atomic.Int32 // consecutive failures while Closed
	halfOpenCount atomic.Int32 // successful probes while HalfOpen
	openedAt      atomic.Int64 // time.Now().UnixNano() when state became Open

	cfg config.CircuitBreakerConfig
}

// New returns a CircuitBreaker that starts in the Closed state.
func New(cfg config.CircuitBreakerConfig) *CircuitBreaker {
	return &CircuitBreaker{cfg: cfg}
}

// State returns the current circuit state.
func (cb *CircuitBreaker) State() State {
	return State(cb.state.Load())
}

// Allow reports whether the next request should be forwarded to the upstream.
// Returns false when the circuit is Open and the recovery timeout has not yet elapsed,
// or when HalfOpen but the maximum probe count has been reached.
func (cb *CircuitBreaker) Allow() bool {
	switch State(cb.state.Load()) {
	case StateClosed:
		return true

	case StateOpen:
		elapsed := time.Since(time.Unix(0, cb.openedAt.Load()))
		if elapsed < cb.cfg.RecoveryTimeout {
			return false // still in open window; fast-fail
		}
		// Recovery timeout elapsed — try to transition to HalfOpen.
		// Only the first goroutine that wins the CAS gets to probe.
		if cb.state.CompareAndSwap(int32(StateOpen), int32(StateHalfOpen)) {
			cb.halfOpenCount.Store(0)
		}
		// Whether we won the CAS or not, allow this request through as the probe.
		return true

	case StateHalfOpen:
		maxReqs := cb.cfg.HalfOpenMaxRequests
		if maxReqs <= 0 {
			maxReqs = 1
		}
		return cb.halfOpenCount.Load() < int32(maxReqs)

	default:
		return true
	}
}

// RecordSuccess notifies the breaker that the last upstream call succeeded.
func (cb *CircuitBreaker) RecordSuccess() {
	switch State(cb.state.Load()) {
	case StateHalfOpen:
		maxReqs := cb.cfg.HalfOpenMaxRequests
		if maxReqs <= 0 {
			maxReqs = 1
		}
		n := cb.halfOpenCount.Add(1)
		if n >= int32(maxReqs) {
			// All probes succeeded — close the circuit.
			if cb.state.CompareAndSwap(int32(StateHalfOpen), int32(StateClosed)) {
				cb.failures.Store(0)
				cb.halfOpenCount.Store(0)
			}
		}
	case StateClosed, StateOpen:
		// Closed: reset consecutive failure counter on any success.
		// Open: no-op (Allow() handles the Open→HalfOpen transition).
		cb.failures.Store(0)
	}
}

// RecordFailure notifies the breaker that the last upstream call failed.
func (cb *CircuitBreaker) RecordFailure() {
	switch State(cb.state.Load()) {
	case StateHalfOpen:
		// A single probe failure sends the circuit back to Open.
		if cb.state.CompareAndSwap(int32(StateHalfOpen), int32(StateOpen)) {
			cb.openedAt.Store(time.Now().UnixNano())
			cb.halfOpenCount.Store(0)
		}
	case StateClosed, StateOpen:
		// Closed: accumulate failures; open when threshold is reached.
		// Open: already open; re-record the openedAt time to extend the window.
		threshold := cb.cfg.FailureThreshold
		if threshold <= 0 {
			threshold = 5
		}
		n := cb.failures.Add(1)
		if n >= int32(threshold) {
			if cb.state.CompareAndSwap(int32(StateClosed), int32(StateOpen)) {
				cb.openedAt.Store(time.Now().UnixNano())
			}
		}
	}
}

// RecoveryRemaining returns how long until the circuit may transition to HalfOpen.
// Returns zero when the circuit is not Open or the recovery timeout has already elapsed.
func (cb *CircuitBreaker) RecoveryRemaining() time.Duration {
	if State(cb.state.Load()) != StateOpen {
		return 0
	}
	rem := cb.cfg.RecoveryTimeout - time.Since(time.Unix(0, cb.openedAt.Load()))
	if rem < 0 {
		return 0
	}
	return rem
}
