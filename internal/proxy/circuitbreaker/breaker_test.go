package circuitbreaker_test

import (
	"sync"
	"testing"
	"time"

	"github.com/K3y5tr0ke/mcpgate/internal/config"
	"github.com/K3y5tr0ke/mcpgate/internal/proxy/circuitbreaker"
)

func cfg(threshold int, recovery time.Duration, halfOpen int) config.CircuitBreakerConfig {
	return config.CircuitBreakerConfig{
		FailureThreshold:    threshold,
		RecoveryTimeout:     recovery,
		HalfOpenMaxRequests: halfOpen,
	}
}

func TestBreaker_StartsClosedAndAllows(t *testing.T) {
	cb := circuitbreaker.New(cfg(3, time.Second, 1))
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("initial state = %v, want Closed", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("Allow() = false, want true when Closed")
	}
}

func TestBreaker_OpensAfterThreshold(t *testing.T) {
	cb := circuitbreaker.New(cfg(3, time.Second, 1))
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("state = %v after 3 failures, want Open", cb.State())
	}
	if cb.Allow() {
		t.Fatal("Allow() = true when Open, want false")
	}
}

func TestBreaker_RecoveryTimeout_TransitionsToHalfOpen(t *testing.T) {
	cb := circuitbreaker.New(cfg(1, 50*time.Millisecond, 2))
	cb.RecordFailure()
	if cb.State() != circuitbreaker.StateOpen {
		t.Fatal("expected Open after 1 failure with threshold=1")
	}

	time.Sleep(60 * time.Millisecond)

	// First Allow() after timeout should succeed (probe).
	if !cb.Allow() {
		t.Fatal("Allow() = false after recovery timeout, want true")
	}
	if cb.State() != circuitbreaker.StateHalfOpen {
		t.Fatalf("state = %v after recovery timeout, want HalfOpen", cb.State())
	}
}

func TestBreaker_HalfOpen_SuccessesCloseCircuit(t *testing.T) {
	cb := circuitbreaker.New(cfg(1, 10*time.Millisecond, 2))
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // triggers transition to HalfOpen

	cb.RecordSuccess()
	cb.RecordSuccess()

	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("state = %v after 2 probe successes (halfOpenMax=2), want Closed", cb.State())
	}
}

func TestBreaker_HalfOpen_FailureReopens(t *testing.T) {
	cb := circuitbreaker.New(cfg(1, 10*time.Millisecond, 2))
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // triggers transition to HalfOpen

	cb.RecordFailure()

	if cb.State() != circuitbreaker.StateOpen {
		t.Fatalf("state = %v after probe failure, want Open", cb.State())
	}
}

func TestBreaker_SuccessResetFailureCount(t *testing.T) {
	cb := circuitbreaker.New(cfg(3, time.Second, 1))
	cb.RecordFailure()
	cb.RecordFailure()
	cb.RecordSuccess() // resets counter
	cb.RecordFailure() // only 1 failure after reset
	cb.RecordFailure() // 2 failures

	// Still not at threshold; should still be Closed.
	if cb.State() != circuitbreaker.StateClosed {
		t.Fatalf("state = %v, want Closed (2 failures after reset, threshold=3)", cb.State())
	}
}

func TestBreaker_RecoveryRemaining(t *testing.T) {
	cb := circuitbreaker.New(cfg(1, 200*time.Millisecond, 1))
	if cb.RecoveryRemaining() != 0 {
		t.Error("RecoveryRemaining should be 0 when Closed")
	}
	cb.RecordFailure()
	rem := cb.RecoveryRemaining()
	if rem <= 0 || rem > 200*time.Millisecond {
		t.Errorf("RecoveryRemaining = %v, want (0, 200ms]", rem)
	}
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	cb := circuitbreaker.New(cfg(10, 50*time.Millisecond, 5))
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if cb.Allow() {
				cb.RecordFailure()
			}
		}()
	}
	wg.Wait()
	// After 50 goroutines recording failures (threshold=10), circuit must be Open.
	if cb.State() == circuitbreaker.StateClosed {
		t.Error("expected circuit to open under concurrent failures")
	}
}
