package shared

import (
	"fmt"
	"sync"
)

// CircuitBreaker detects stagnation in loop iterations and trips when
// unhealthy patterns persist. Trip conditions (any one triggers a trip):
//   - Stagnation: N consecutive loops without progress (threshold)
//   - No LOOP_STATUS: N consecutive loops where status block is missing (threshold)
//   - Same error: N identical error signatures in a row (sameErrorThreshold)
//   - Output decline: Output size shrinks >= threshold% for 2 consecutive loops
//   - Test-only loops: N consecutive TESTING-only work type loops (maxConsecutiveTestLoops)
//   - Safety completion: N consecutive loops with completion indicators but no EXIT_SIGNAL (safetyCompletionThreshold)
//   - Blocked status: Trips immediately when agent reports BLOCKED
type CircuitBreaker struct {
	mu              sync.Mutex
	threshold       int
	noProgressCount int
	tripped         bool
	tripReason      string

	// Same-error tracking
	sameErrorThreshold int
	lastErrorSignature string
	sameErrorCount     int

	// Output decline tracking
	outputDeclineThreshold int // Percentage (0-100)
	lastOutputSize         int
	declineCount           int

	// Test loop tracking
	maxConsecutiveTestLoops int
	consecutiveTestLoops    int

	// Completion tracking (for strict mode)
	completionThreshold int

	// Safety completion tracking - force exit after N consecutive loops with completion indicators
	// This catches cases where EXIT_SIGNAL parsing fails but Claude outputs completion-like text
	safetyCompletionThreshold  int
	consecutiveCompletionCount int
}

// CircuitBreakerConfig holds configuration for the circuit breaker.
type CircuitBreakerConfig struct {
	StagnationThreshold       int
	SameErrorThreshold        int
	OutputDeclineThreshold    int
	MaxConsecutiveTestLoops   int
	CompletionThreshold       int
	SafetyCompletionThreshold int
}

// DefaultCircuitBreakerConfig returns default configuration.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		StagnationThreshold:       DefaultStagnationThreshold,
		SameErrorThreshold:        DefaultSameErrorThreshold,
		OutputDeclineThreshold:    DefaultOutputDeclineThreshold,
		MaxConsecutiveTestLoops:   DefaultMaxConsecutiveTestLoops,
		CompletionThreshold:       DefaultCompletionThreshold,
		SafetyCompletionThreshold: DefaultSafetyCompletionThreshold,
	}
}

// NewCircuitBreaker creates a new circuit breaker with the given threshold.
func NewCircuitBreaker(threshold int) *CircuitBreaker {
	if threshold <= 0 {
		threshold = DefaultStagnationThreshold
	}
	return &CircuitBreaker{
		threshold:                 threshold,
		sameErrorThreshold:        DefaultSameErrorThreshold,
		outputDeclineThreshold:    DefaultOutputDeclineThreshold,
		maxConsecutiveTestLoops:   DefaultMaxConsecutiveTestLoops,
		completionThreshold:       DefaultCompletionThreshold,
		safetyCompletionThreshold: DefaultSafetyCompletionThreshold,
	}
}

// NewCircuitBreakerWithConfig creates a circuit breaker with full configuration.
func NewCircuitBreakerWithConfig(cfg CircuitBreakerConfig) *CircuitBreaker {
	cb := &CircuitBreaker{
		threshold:                 cfg.StagnationThreshold,
		sameErrorThreshold:        cfg.SameErrorThreshold,
		outputDeclineThreshold:    cfg.OutputDeclineThreshold,
		maxConsecutiveTestLoops:   cfg.MaxConsecutiveTestLoops,
		completionThreshold:       cfg.CompletionThreshold,
		safetyCompletionThreshold: cfg.SafetyCompletionThreshold,
	}

	// Apply defaults for zero values
	if cb.threshold <= 0 {
		cb.threshold = DefaultStagnationThreshold
	}
	if cb.sameErrorThreshold <= 0 {
		cb.sameErrorThreshold = DefaultSameErrorThreshold
	}
	if cb.outputDeclineThreshold <= 0 {
		cb.outputDeclineThreshold = DefaultOutputDeclineThreshold
	}
	if cb.maxConsecutiveTestLoops <= 0 {
		cb.maxConsecutiveTestLoops = DefaultMaxConsecutiveTestLoops
	}
	if cb.completionThreshold <= 0 {
		cb.completionThreshold = DefaultCompletionThreshold
	}
	if cb.safetyCompletionThreshold <= 0 {
		cb.safetyCompletionThreshold = DefaultSafetyCompletionThreshold
	}

	return cb
}

// UpdateResult contains the result of a circuit breaker update.
type UpdateResult struct {
	Tripped       bool
	Reason        string
	IsComplete    bool // True if strict completion criteria met
	CompletionMsg string
}

// Update evaluates the status and updates the circuit breaker state.
// Returns true and a reason if the circuit has tripped.
func (cb *CircuitBreaker) Update(status *Status) (tripped bool, reason string) {
	result := cb.UpdateWithAnalysis(status, nil)
	return result.Tripped, result.Reason
}

// UpdateWithAnalysis evaluates the full analysis and updates circuit state.
// It checks all trip conditions in order: safety completion, strict completion
// (success path), nil status, blocked status, same-error sequence, output
// decline, test-only loops, and finally no-progress stagnation. Returns an
// UpdateResult indicating whether the circuit tripped, the reason, or whether
// strict completion criteria were met.
func (cb *CircuitBreaker) UpdateWithAnalysis(status *Status, analysis *AnalysisResult) UpdateResult {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if cb.tripped {
		return UpdateResult{Tripped: true, Reason: cb.tripReason}
	}

	// Extract output size and error signature from analysis if available
	var outputSize int
	var errorSignature string
	if analysis != nil {
		outputSize = analysis.OutputSize
		errorSignature = analysis.ErrorSignature
	}

	// Safety circuit breaker: track consecutive loops with completion indicators.
	// This catches cases where EXIT_SIGNAL parsing fails but Claude outputs completion-like text.
	if status != nil && status.CompletionIndicators > 0 {
		cb.consecutiveCompletionCount++
		if cb.consecutiveCompletionCount >= cb.safetyCompletionThreshold {
			cb.tripped = true
			cb.tripReason = fmt.Sprintf("safety: %d consecutive loops with completion indicators (EXIT_SIGNAL may not be parsing correctly)", cb.consecutiveCompletionCount)
			return UpdateResult{Tripped: true, Reason: cb.tripReason}
		}
	} else {
		cb.consecutiveCompletionCount = 0
	}

	// Check for strict completion
	if status != nil && status.ExitSignal && status.CompletionIndicators >= cb.completionThreshold {
		return UpdateResult{
			IsComplete: true,
			CompletionMsg: fmt.Sprintf("completion criteria met: EXIT_SIGNAL=true, indicators=%d (threshold=%d)",
				status.CompletionIndicators, cb.completionThreshold),
		}
	}

	// No status block found - count as no progress
	if status == nil {
		cb.noProgressCount++
		if cb.noProgressCount >= cb.threshold {
			cb.tripped = true
			cb.tripReason = fmt.Sprintf("no LOOP_STATUS block for %d consecutive loops", cb.noProgressCount)
			return UpdateResult{Tripped: true, Reason: cb.tripReason}
		}
		return UpdateResult{}
	}

	// Blocked status trips immediately
	if status.IsBlocked() {
		cb.tripped = true
		cb.tripReason = fmt.Sprintf("agent reported BLOCKED status: %s", status.Recommendation)
		return UpdateResult{Tripped: true, Reason: cb.tripReason}
	}

	// Check for same-error sequence
	if errorSignature != "" {
		if errorSignature == cb.lastErrorSignature {
			cb.sameErrorCount++
			if cb.sameErrorCount >= cb.sameErrorThreshold {
				cb.tripped = true
				cb.tripReason = fmt.Sprintf("same error repeated %d times", cb.sameErrorCount)
				return UpdateResult{Tripped: true, Reason: cb.tripReason}
			}
		} else {
			cb.sameErrorCount = 1
			cb.lastErrorSignature = errorSignature
		}
	} else {
		cb.sameErrorCount = 0
		cb.lastErrorSignature = ""
	}

	// Check for output decline
	if outputSize > 0 && cb.lastOutputSize > 0 && cb.outputDeclineThreshold > 0 {
		decline := float64(cb.lastOutputSize-outputSize) / float64(cb.lastOutputSize) * 100
		if decline >= float64(cb.outputDeclineThreshold) {
			cb.declineCount++
			if cb.declineCount >= 2 { // Require 2 consecutive declines
				cb.tripped = true
				cb.tripReason = fmt.Sprintf("output declined by %.0f%% for %d consecutive loops", decline, cb.declineCount)
				return UpdateResult{Tripped: true, Reason: cb.tripReason}
			}
		} else {
			cb.declineCount = 0
		}
	}
	cb.lastOutputSize = outputSize

	// Check for consecutive test-only loops
	if status.IsTestOnly() {
		cb.consecutiveTestLoops++
		if cb.consecutiveTestLoops >= cb.maxConsecutiveTestLoops {
			cb.tripped = true
			cb.tripReason = fmt.Sprintf("test-only work for %d consecutive loops", cb.consecutiveTestLoops)
			return UpdateResult{Tripped: true, Reason: cb.tripReason}
		}
	} else {
		cb.consecutiveTestLoops = 0
	}

	// Check for progress
	if status.HasProgress() {
		cb.noProgressCount = 0
		return UpdateResult{}
	}

	// No progress - increment counter
	cb.noProgressCount++
	if cb.noProgressCount >= cb.threshold {
		cb.tripped = true
		cb.tripReason = fmt.Sprintf("no progress for %d consecutive loops", cb.noProgressCount)
		return UpdateResult{Tripped: true, Reason: cb.tripReason}
	}

	return UpdateResult{}
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
	cb.sameErrorCount = 0
	cb.lastErrorSignature = ""
	cb.declineCount = 0
	cb.lastOutputSize = 0
	cb.consecutiveTestLoops = 0
	cb.consecutiveCompletionCount = 0
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

// SameErrorCount returns the current count of same-error loops.
func (cb *CircuitBreaker) SameErrorCount() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.sameErrorCount
}

// ConsecutiveTestLoops returns the count of consecutive test-only loops.
func (cb *CircuitBreaker) ConsecutiveTestLoops() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.consecutiveTestLoops
}

// State returns the current circuit breaker state for persistence.
func (cb *CircuitBreaker) State() CircuitBreakerState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return CircuitBreakerState{
		NoProgressCount:            cb.noProgressCount,
		Tripped:                    cb.tripped,
		TripReason:                 cb.tripReason,
		SameErrorCount:             cb.sameErrorCount,
		LastErrorSignature:         cb.lastErrorSignature,
		DeclineCount:               cb.declineCount,
		LastOutputSize:             cb.lastOutputSize,
		ConsecutiveTestLoops:       cb.consecutiveTestLoops,
		ConsecutiveCompletionCount: cb.consecutiveCompletionCount,
	}
}

// RestoreState restores circuit breaker state from persistence.
func (cb *CircuitBreaker) RestoreState(state CircuitBreakerState) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.noProgressCount = state.NoProgressCount
	cb.tripped = state.Tripped
	cb.tripReason = state.TripReason
	cb.sameErrorCount = state.SameErrorCount
	cb.lastErrorSignature = state.LastErrorSignature
	cb.declineCount = state.DeclineCount
	cb.lastOutputSize = state.LastOutputSize
	cb.consecutiveTestLoops = state.ConsecutiveTestLoops
	cb.consecutiveCompletionCount = state.ConsecutiveCompletionCount
}

// CircuitBreakerState holds the state for persistence.
type CircuitBreakerState struct {
	NoProgressCount            int    `json:"no_progress_count"`
	Tripped                    bool   `json:"tripped"`
	TripReason                 string `json:"trip_reason,omitempty"`
	SameErrorCount             int    `json:"same_error_count"`
	LastErrorSignature         string `json:"last_error_signature,omitempty"`
	DeclineCount               int    `json:"decline_count"`
	LastOutputSize             int    `json:"last_output_size"`
	ConsecutiveTestLoops       int    `json:"consecutive_test_loops"`
	ConsecutiveCompletionCount int    `json:"consecutive_completion_count"`
}
