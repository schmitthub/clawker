package shared

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRateLimiter_Basic(t *testing.T) {
	limiter := NewRateLimiter(3)

	// First 3 calls should be allowed
	assert.True(t, limiter.Allow(), "first call should be allowed")
	assert.True(t, limiter.Allow(), "second call should be allowed")
	assert.True(t, limiter.Allow(), "third call should be allowed")

	// Fourth call should be blocked
	assert.False(t, limiter.Allow(), "fourth call should be blocked")

	// Check remaining
	assert.Equal(t, 0, limiter.Remaining())
	assert.Equal(t, 3, limiter.CallCount())
}

func TestRateLimiter_Disabled(t *testing.T) {
	// Zero limit disables rate limiting
	limiter := NewRateLimiter(0)

	assert.False(t, limiter.IsEnabled())
	assert.Equal(t, -1, limiter.Remaining()) // -1 means unlimited

	// Should always allow
	for i := 0; i < 1000; i++ {
		assert.True(t, limiter.Allow())
	}
}

func TestRateLimiter_NegativeLimit(t *testing.T) {
	// Negative limit also disables rate limiting
	limiter := NewRateLimiter(-1)

	assert.False(t, limiter.IsEnabled())
	assert.True(t, limiter.Allow())
}

func TestRateLimiter_Record(t *testing.T) {
	limiter := NewRateLimiter(3)

	// Record without checking
	limiter.Record()
	limiter.Record()
	assert.Equal(t, 2, limiter.CallCount())

	// Should still allow one more
	assert.True(t, limiter.Allow())
	assert.False(t, limiter.Allow())
}

func TestRateLimiter_State(t *testing.T) {
	limiter := NewRateLimiter(10)
	limiter.Record()
	limiter.Record()
	limiter.Record()

	state := limiter.State()
	assert.Equal(t, 3, state.Calls)
	assert.False(t, state.WindowStart.IsZero())

	// Create new limiter and restore
	limiter2 := NewRateLimiter(10)
	restored := limiter2.RestoreState(state)

	assert.True(t, restored, "RestoreState should return true for valid state")
	assert.Equal(t, 3, limiter2.CallCount())
	assert.Equal(t, 7, limiter2.Remaining())
}

func TestRateLimiter_StateExpired(t *testing.T) {
	limiter := NewRateLimiter(10)

	// Create an old state
	oldState := RateLimitState{
		Calls:       5,
		WindowStart: time.Now().Add(-2 * time.Hour), // 2 hours ago
	}

	restored := limiter.RestoreState(oldState)

	// Should not restore expired state
	assert.False(t, restored, "RestoreState should return false for expired state")
	assert.Equal(t, 0, limiter.CallCount())
	assert.Equal(t, 10, limiter.Remaining())
}

func TestRateLimiter_StateNegativeCalls(t *testing.T) {
	limiter := NewRateLimiter(10)

	// Create state with negative calls (invalid)
	invalidState := RateLimitState{
		Calls:       -5,
		WindowStart: time.Now(),
	}

	restored := limiter.RestoreState(invalidState)

	// Should not restore invalid state
	assert.False(t, restored, "RestoreState should return false for negative calls")
	assert.Equal(t, 0, limiter.CallCount())
	assert.Equal(t, 10, limiter.Remaining())
}

func TestRateLimiter_ResetTime(t *testing.T) {
	limiter := NewRateLimiter(10)

	resetTime := limiter.ResetTime()
	require.False(t, resetTime.IsZero())

	// Reset time should be approximately 1 hour from now
	expected := time.Now().Add(time.Hour)
	diff := resetTime.Sub(expected)
	assert.True(t, diff < time.Second && diff > -time.Second,
		"reset time should be approximately 1 hour from now")
}

func TestRateLimiter_Concurrent(t *testing.T) {
	limiter := NewRateLimiter(100)

	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 20; j++ {
				limiter.Allow()
			}
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}

	// All 100 calls should be recorded, subsequent blocked
	assert.Equal(t, 100, limiter.CallCount())
	assert.Equal(t, 0, limiter.Remaining())
	assert.False(t, limiter.Allow())
}
