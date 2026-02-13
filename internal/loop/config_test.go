package loop

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 50, cfg.MaxLoops)
	assert.Equal(t, 3, cfg.StagnationThreshold)
	assert.Equal(t, 15, cfg.TimeoutMinutes)
	assert.False(t, cfg.AutoConfirm)
}

func TestConfig_Timeout(t *testing.T) {
	tests := []struct {
		name           string
		timeoutMinutes int
		expected       time.Duration
	}{
		{
			name:           "default when zero",
			timeoutMinutes: 0,
			expected:       15 * time.Minute,
		},
		{
			name:           "default when negative",
			timeoutMinutes: -5,
			expected:       15 * time.Minute,
		},
		{
			name:           "custom value",
			timeoutMinutes: 30,
			expected:       30 * time.Minute,
		},
		{
			name:           "one minute",
			timeoutMinutes: 1,
			expected:       1 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{TimeoutMinutes: tt.timeoutMinutes}
			assert.Equal(t, tt.expected, cfg.Timeout())
		})
	}
}

func TestConfig_GetMaxLoops(t *testing.T) {
	tests := []struct {
		name     string
		maxLoops int
		expected int
	}{
		{
			name:     "default when zero",
			maxLoops: 0,
			expected: 50,
		},
		{
			name:     "default when negative",
			maxLoops: -10,
			expected: 50,
		},
		{
			name:     "custom value",
			maxLoops: 100,
			expected: 100,
		},
		{
			name:     "small value",
			maxLoops: 5,
			expected: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{MaxLoops: tt.maxLoops}
			assert.Equal(t, tt.expected, cfg.GetMaxLoops())
		})
	}
}

func TestConfig_GetStagnationThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold int
		expected  int
	}{
		{
			name:      "default when zero",
			threshold: 0,
			expected:  3,
		},
		{
			name:      "default when negative",
			threshold: -1,
			expected:  3,
		},
		{
			name:      "custom value",
			threshold: 10,
			expected:  10,
		},
		{
			name:      "small value",
			threshold: 1,
			expected:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{StagnationThreshold: tt.threshold}
			assert.Equal(t, tt.expected, cfg.GetStagnationThreshold())
		})
	}
}
