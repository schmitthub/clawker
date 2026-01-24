package ralph

import (
	"fmt"
	"sync"
)

// CircuitBreaker detects stagnation in Ralph loops.
// It tracks consecutive loops without progress and trips when
// the threshold is exceeded.
type CircuitBreaker struct {
	mu              sync.Mutex
	threshold       int
	noProgressCount int
	tripped         bool
	tripReason      string
}

// NewCircuitBreaker creates a new circuit breaker with the given threshold.
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	if threshold <= 0 {
		threshold = 3
	}
	return &CircuitBreaker{
		threshold: threshold,
	}
}

// Update evaluates the status and updates the circuit breaker state.
// Returns true and a reason if the circuit has tripped.
func (cb *CircuitBreaker) Update(status *Status) (tripped bool, reason string) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.tripped {
		return true, cb.tripReason
	}

	// No status block found - count as no progress
	if status == nil {
		cb.noProgressCount++
		if cb.noProgressCount >= cb.threshold {
			cb.tripped = true
			cb.tripReason = fmt.Sprintf("no RALPH_STATUS block for %d consecutive loops", cb.noProgressCount)
			return true, cb.tripReason
		}
		return false, ""
	}

	// Blocked status trips immediately
	if status.IsBlocked() {
		cb.tripped = true
		cb.tripReason = fmt.Sprintf("agent reported BLOCKED status: %s", status.Recommendation)
		return true, cb.tripReason
	}

	// Check for progress
	if status.HasProgress() {
		cb.noProgressCount = 0
		return false, ""
	}

	// No progress - increment counter
	cb.noProgressCount++
	if cb.noProgressCount >= cb.threshold {
		cb.tripped = true
		cb.tripReason = fmt.Sprintf("no progress for %d consecutive loops", cb.noProgressCount)
		return true, cb.tripReason
	}

	return false, ""
}

// Check returns true if the circuit is still open (not tripped).
func (cb *CircuitBreaker) Check() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return !cb.tripped
}

// IsTripped returns true if the circuit has tripped.
func (cb *CircuitBreaker) IsTripped() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripped
}

// TripReason returns the reason the circuit tripped, or empty if not tripped.
func (cb *CircuitBreaker) TripReason() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.tripReason
}

// Reset clears the circuit breaker state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.noProgressCount = 0
	cb.tripped = false
	cb.tripReason = ""
}

// NoProgressCount returns the current count of loops without progress.
func (cb *CircuitBreaker) NoProgressCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.noProgressCount
}

// Threshold returns the configured stagnation threshold.
func (cb *CircuitBreaker) Threshold() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.threshold
}
