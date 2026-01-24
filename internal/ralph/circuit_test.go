package ralph

import (
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
	assert.Contains(t, reason, "no RALPH_STATUS block")
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
