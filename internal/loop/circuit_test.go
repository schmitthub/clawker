package loop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewCircuitBreaker(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
		expected  int
	}{
		{"positive threshold", 5, 5},
		{"zero threshold defaults to 3", 0, 3},
		{"negative threshold defaults to 3", -1, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := NewCircuitBreaker(tt.threshold)
			assert.Equal(t, tt.expected, cb.Threshold())
		})
	}
}

func TestCircuitBreaker_Update_WithProgress(t *testing.T) {
	cb := NewCircuitBreaker(3)

	// Status with progress should reset counter
	status := &Status{TasksCompleted: 1}
	tripped, reason := cb.Update(status)

	assert.False(t, tripped)
	assert.Empty(t, reason)
	assert.Equal(t, 0, cb.NoProgressCount())
}

func TestCircuitBreaker_Update_NoProgress(t *testing.T) {
	cb := NewCircuitBreaker(3)

	// First loop without progress
	status := &Status{Status: StatusPending}
	tripped, _ := cb.Update(status)
	assert.False(t, tripped)
	assert.Equal(t, 1, cb.NoProgressCount())

	// Second loop without progress
	tripped, _ = cb.Update(status)
	assert.False(t, tripped)
	assert.Equal(t, 2, cb.NoProgressCount())

	// Third loop without progress - should trip
	tripped, reason := cb.Update(status)
	assert.True(t, tripped)
	assert.Contains(t, reason, "no progress for 3 consecutive loops")
	assert.True(t, cb.IsTripped())
}

func TestCircuitBreaker_Update_NilStatus(t *testing.T) {
	cb := NewCircuitBreaker(2)

	// First nil status
	tripped, _ := cb.Update(nil)
	assert.False(t, tripped)
	assert.Equal(t, 1, cb.NoProgressCount())

	// Second nil status - should trip
	tripped, reason := cb.Update(nil)
	assert.True(t, tripped)
	assert.Contains(t, reason, "no LOOP_STATUS block")
}

func TestCircuitBreaker_Update_Blocked(t *testing.T) {
	cb := NewCircuitBreaker(5)

	// Blocked status should trip immediately
	status := &Status{Status: StatusBlocked, Recommendation: "Need help"}
	tripped, reason := cb.Update(status)

	assert.True(t, tripped)
	assert.Contains(t, reason, "BLOCKED")
	assert.Contains(t, reason, "Need help")
}

func TestCircuitBreaker_Update_ProgressResetsCounter(t *testing.T) {
	cb := NewCircuitBreaker(3)

	// Two loops without progress
	noProgress := &Status{Status: StatusPending}
	cb.Update(noProgress)
	cb.Update(noProgress)
	assert.Equal(t, 2, cb.NoProgressCount())

	// Loop with progress resets counter
	withProgress := &Status{TasksCompleted: 1}
	tripped, _ := cb.Update(withProgress)
	assert.False(t, tripped)
	assert.Equal(t, 0, cb.NoProgressCount())

	// Can have more loops without progress now
	cb.Update(noProgress)
	assert.Equal(t, 1, cb.NoProgressCount())
	assert.False(t, cb.IsTripped())
}

func TestCircuitBreaker_Check(t *testing.T) {
	cb := NewCircuitBreaker(2)

	// Initially open (not tripped)
	assert.True(t, cb.Check())

	// Trip it
	cb.Update(nil)
	cb.Update(nil)

	// Now closed (tripped)
	assert.False(t, cb.Check())
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(2)

	// Trip it
	cb.Update(nil)
	cb.Update(nil)
	require.True(t, cb.IsTripped())

	// Reset
	cb.Reset()

	assert.False(t, cb.IsTripped())
	assert.Equal(t, 0, cb.NoProgressCount())
	assert.Empty(t, cb.TripReason())
	assert.True(t, cb.Check())
}

func TestCircuitBreaker_AlreadyTripped(t *testing.T) {
	cb := NewCircuitBreaker(2)

	// Trip it
	cb.Update(nil)
	cb.Update(nil)
	require.True(t, cb.IsTripped())

	originalReason := cb.TripReason()

	// Additional updates should return tripped state
	tripped, reason := cb.Update(&Status{TasksCompleted: 100})
	assert.True(t, tripped)
	assert.Equal(t, originalReason, reason)
}

func TestCircuitBreaker_ConcurrentAccess(t *testing.T) {
	cb := NewCircuitBreaker(100)
	done := make(chan bool)

	// Concurrent updates
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				cb.Update(&Status{Status: StatusPending})
				cb.Check()
				cb.NoProgressCount()
			}
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should have tripped at some point (500 updates, threshold 100)
	// Just verify no race conditions occurred
	_ = cb.IsTripped()
}

func TestCircuitBreaker_SafetyCompletionThreshold(t *testing.T) {
	// Configure with safety threshold of 3
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold:       10, // High so it doesn't trip on no-progress
		SafetyCompletionThreshold: 3,
	})

	// Status with completion indicators but no EXIT_SIGNAL
	status := &Status{
		Status:               StatusPending,
		CompletionIndicators: 2, // Has completion text but no exit signal
	}

	// First loop - not tripped yet
	result := cb.UpdateWithAnalysis(status, nil)
	assert.False(t, result.Tripped)
	assert.False(t, result.IsComplete)

	// Second loop - still not tripped
	result = cb.UpdateWithAnalysis(status, nil)
	assert.False(t, result.Tripped)

	// Third loop - should trip due to safety threshold
	result = cb.UpdateWithAnalysis(status, nil)
	assert.True(t, result.Tripped)
	assert.Contains(t, result.Reason, "safety")
	assert.Contains(t, result.Reason, "consecutive loops with completion indicators")
}

func TestCircuitBreaker_SafetyCompletionResetsOnNoIndicators(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold:       10,
		SafetyCompletionThreshold: 3,
	})

	// Two loops with completion indicators
	withCompletion := &Status{
		Status:               StatusPending,
		CompletionIndicators: 1,
		TasksCompleted:       1, // Has progress so no-progress counter doesn't trip
	}
	cb.UpdateWithAnalysis(withCompletion, nil)
	cb.UpdateWithAnalysis(withCompletion, nil)

	// One loop without completion indicators - should reset counter
	noCompletion := &Status{
		Status:               StatusPending,
		CompletionIndicators: 0,
		TasksCompleted:       1,
	}
	result := cb.UpdateWithAnalysis(noCompletion, nil)
	assert.False(t, result.Tripped)

	// Now two more with completion - should NOT trip (counter was reset)
	cb.UpdateWithAnalysis(withCompletion, nil)
	result = cb.UpdateWithAnalysis(withCompletion, nil)
	assert.False(t, result.Tripped)
}

func TestCircuitBreaker_SafetyCompletionDoesNotBlockNormalCompletion(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold:       10,
		SafetyCompletionThreshold: 5, // Set higher than completion threshold
		CompletionThreshold:       2,
	})

	// Status with EXIT_SIGNAL and enough completion indicators should complete normally
	// before safety threshold is reached
	status := &Status{
		Status:               StatusComplete,
		ExitSignal:           true,
		CompletionIndicators: 3,
	}

	result := cb.UpdateWithAnalysis(status, nil)
	assert.True(t, result.IsComplete) // Normal completion
	assert.False(t, result.Tripped)   // Not tripped as safety
}

func TestCircuitBreaker_StateIncludesCompletionCount(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		SafetyCompletionThreshold: 10,
	})

	// Add some completion indicators
	status := &Status{
		Status:               StatusPending,
		CompletionIndicators: 2,
		TasksCompleted:       1,
	}
	cb.UpdateWithAnalysis(status, nil)
	cb.UpdateWithAnalysis(status, nil)

	// State should include the count
	state := cb.State()
	assert.Equal(t, 2, state.ConsecutiveCompletionCount)
}

func TestCircuitBreaker_RestoreStateWithCompletionCount(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		SafetyCompletionThreshold: 5,
	})

	// Restore state with existing completion count
	state := CircuitBreakerState{
		ConsecutiveCompletionCount: 4,
	}
	cb.RestoreState(state)

	// One more loop with completion indicators should trip
	status := &Status{
		Status:               StatusPending,
		CompletionIndicators: 1,
		TasksCompleted:       1,
	}
	result := cb.UpdateWithAnalysis(status, nil)
	assert.True(t, result.Tripped)
	assert.Contains(t, result.Reason, "safety")
}

// ---------------------------------------------------------------------------
// Granular edge case tests
// ---------------------------------------------------------------------------

func TestCircuitBreaker_ExactlyAtThreshold(t *testing.T) {
	threshold := 3
	cb := NewCircuitBreaker(threshold)

	noProgress := &Status{Status: StatusPending}

	// threshold-1 loops should NOT trip
	for i := 0; i < threshold-1; i++ {
		tripped, _ := cb.Update(noProgress)
		assert.False(t, tripped, "should not trip at count %d", i+1)
	}
	assert.Equal(t, threshold-1, cb.NoProgressCount())

	// The next one should trip at exactly threshold
	tripped, reason := cb.Update(noProgress)
	assert.True(t, tripped, "should trip at exactly the threshold")
	assert.Contains(t, reason, fmt.Sprintf("%d consecutive loops", threshold))
}

func TestCircuitBreaker_AlternatingProgressNoProgress(t *testing.T) {
	cb := NewCircuitBreaker(3)

	noProgress := &Status{Status: StatusPending}
	withProgress := &Status{TasksCompleted: 1}

	// Alternate: progress → no-progress → progress → no-progress
	// Counter should never accumulate past 1
	for i := 0; i < 10; i++ {
		cb.Update(withProgress)
		assert.Equal(t, 0, cb.NoProgressCount())

		tripped, _ := cb.Update(noProgress)
		assert.False(t, tripped, "alternating pattern should never trip")
		assert.Equal(t, 1, cb.NoProgressCount())
	}
}

func TestCircuitBreaker_SameErrorResetOnDifferentError(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold: 100, // High to avoid stagnation trip
		SameErrorThreshold:  4,
	})

	statusA := &Status{Status: StatusPending, TasksCompleted: 1}
	statusB := &Status{Status: StatusPending, TasksCompleted: 1}
	analysisA := &AnalysisResult{ErrorSignature: "error-A", OutputSize: 100}
	analysisB := &AnalysisResult{ErrorSignature: "error-B", OutputSize: 100}

	// Error A × 2
	cb.UpdateWithAnalysis(statusA, analysisA)
	cb.UpdateWithAnalysis(statusA, analysisA)
	assert.Equal(t, 2, cb.SameErrorCount())

	// Different error B resets the counter to 1
	cb.UpdateWithAnalysis(statusB, analysisB)
	assert.Equal(t, 1, cb.SameErrorCount())

	// Error A again starts fresh at 1
	cb.UpdateWithAnalysis(statusA, analysisA)
	assert.Equal(t, 1, cb.SameErrorCount())

	// Should NOT have tripped despite 4 total same-error loops (A,A,B,A)
	assert.False(t, cb.IsTripped())
}

func TestCircuitBreaker_FirstTripConditionWins(t *testing.T) {
	// Configure so stagnation threshold is 3 and same-error threshold is 3
	// Both approach threshold simultaneously — whichever check runs first wins
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold: 3,
		SameErrorThreshold:  3,
	})

	status := &Status{Status: StatusPending} // no progress
	analysis := &AnalysisResult{ErrorSignature: "repeated-err", OutputSize: 100}

	// Two loops: no progress AND same error
	cb.UpdateWithAnalysis(status, analysis)
	cb.UpdateWithAnalysis(status, analysis)
	assert.False(t, cb.IsTripped())

	// Third loop: both conditions met — one reason should appear
	result := cb.UpdateWithAnalysis(status, analysis)
	assert.True(t, result.Tripped)
	// The reason should be one of the two trip conditions
	assert.True(t,
		strings.Contains(result.Reason, "same error") || strings.Contains(result.Reason, "no progress"),
		"trip reason should mention one of the conditions, got: %s", result.Reason)
}

func TestCircuitBreaker_SafetyCompletionAlternating(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		StagnationThreshold:       100,
		SafetyCompletionThreshold: 3,
	})

	withCompletion := &Status{
		Status:               StatusPending,
		CompletionIndicators: 2,
		TasksCompleted:       1, // Has progress
	}
	noCompletion := &Status{
		Status:               StatusPending,
		CompletionIndicators: 0,
		TasksCompleted:       1,
	}

	// Alternate: with → without → with → without → with → without
	// Counter resets each time, should never reach 3
	for i := 0; i < 10; i++ {
		result := cb.UpdateWithAnalysis(withCompletion, nil)
		assert.False(t, result.Tripped, "alternating safety completion should not trip at iteration %d", i)

		result = cb.UpdateWithAnalysis(noCompletion, nil)
		assert.False(t, result.Tripped)
	}
}

func TestCircuitBreaker_ResetClearsCompletionCount(t *testing.T) {
	cb := NewCircuitBreakerWithConfig(CircuitBreakerConfig{
		SafetyCompletionThreshold: 10,
	})

	// Add some completion indicators
	status := &Status{
		Status:               StatusPending,
		CompletionIndicators: 2,
		TasksCompleted:       1,
	}
	cb.UpdateWithAnalysis(status, nil)
	cb.UpdateWithAnalysis(status, nil)

	// Reset should clear the count
	cb.Reset()
	state := cb.State()
	assert.Equal(t, 0, state.ConsecutiveCompletionCount)
}
