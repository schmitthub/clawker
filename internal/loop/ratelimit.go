package loop

import (
	"sync"
	"time"
)

// RateLimiter tracks API calls per hour and enforces a configurable limit.
type RateLimiter struct {
	mu          sync.Mutex
	limit       int
	calls       int
	windowStart time.Time
}

// NewRateLimiter creates a new rate limiter with the given limit per hour.
// A limit of 0 or less disables rate limiting.
func NewRateLimiter(limit int) *RateLimiter {
	return &RateLimiter{
		limit:       limit,
		windowStart: time.Now(),
	}
}

// Allow checks if a call is allowed and records it if so.
// Returns true if the call is allowed, false if rate limited.
func (r *RateLimiter) Allow() bool {
	if r.limit <= 0 {
		return true // Rate limiting disabled
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset window if an hour has passed
	if time.Since(r.windowStart) >= time.Hour {
		r.calls = 0
		r.windowStart = time.Now()
	}

	if r.calls >= r.limit {
		return false
	}

	r.calls++
	return true
}

// Record manually records a call without checking the limit.
// Useful when a call has already been made.
func (r *RateLimiter) Record() {
	if r.limit <= 0 {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset window if an hour has passed
	if time.Since(r.windowStart) >= time.Hour {
		r.calls = 0
		r.windowStart = time.Now()
	}

	r.calls++
}

// Remaining returns the number of calls remaining in the current window.
func (r *RateLimiter) Remaining() int {
	if r.limit <= 0 {
		return -1 // Unlimited
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Reset window if an hour has passed
	if time.Since(r.windowStart) >= time.Hour {
		return r.limit
	}

	remaining := r.limit - r.calls
	if remaining < 0 {
		return 0
	}
	return remaining
}

// ResetTime returns when the current rate limit window will reset.
func (r *RateLimiter) ResetTime() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.windowStart.Add(time.Hour)
}

// CallCount returns the current number of calls in this window.
func (r *RateLimiter) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// Limit returns the configured limit.
func (r *RateLimiter) Limit() int {
	return r.limit
}

// IsEnabled returns true if rate limiting is enabled.
func (r *RateLimiter) IsEnabled() bool {
	return r.limit > 0
}

// RateLimitState represents the persistent state of the rate limiter.
type RateLimitState struct {
	Calls       int       `json:"calls"`
	WindowStart time.Time `json:"window_start"`
}

// State returns the current state for persistence.
func (r *RateLimiter) State() RateLimitState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RateLimitState{
		Calls:       r.calls,
		WindowStart: r.windowStart,
	}
}

// RestoreState restores the rate limiter from a persisted state.
// Returns true if the state was actually restored, false if it was
// discarded due to expiration or invalid values.
func (r *RateLimiter) RestoreState(state RateLimitState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Validate state - reject negative call counts
	if state.Calls < 0 {
		return false
	}

	// Only restore if the window is still valid
	if time.Since(state.WindowStart) < time.Hour {
		r.calls = state.Calls
		r.windowStart = state.WindowStart
		return true
	}
	return false
}
