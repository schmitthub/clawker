// Package ralph provides autonomous loop execution for Claude Code agents.
package ralph

import "time"

// Config holds Ralph-specific configuration.
type Config struct {
	MaxLoops            int  `yaml:"max_loops" mapstructure:"max_loops"`
	StagnationThreshold int  `yaml:"stagnation_threshold" mapstructure:"stagnation_threshold"`
	TimeoutMinutes      int  `yaml:"timeout_minutes" mapstructure:"timeout_minutes"`
	AutoConfirm         bool `yaml:"auto_confirm" mapstructure:"auto_confirm"`
}

// DefaultConfig returns the default Ralph configuration.
func DefaultConfig() Config {
	return Config{
		MaxLoops:            50,
		StagnationThreshold: 3,
		TimeoutMinutes:      15,
		AutoConfirm:         false,
	}
}

// Timeout returns the per-loop timeout duration.
func (c Config) Timeout() time.Duration {
	if c.TimeoutMinutes <= 0 {
		return 15 * time.Minute
	}
	return time.Duration(c.TimeoutMinutes) * time.Minute
}

// GetMaxLoops returns the max loops with default fallback.
func (c Config) GetMaxLoops() int {
	if c.MaxLoops <= 0 {
		return 50
	}
	return c.MaxLoops
}

// GetStagnationThreshold returns the stagnation threshold with default fallback.
func (c Config) GetStagnationThreshold() int {
	if c.StagnationThreshold <= 0 {
		return 3
	}
	return c.StagnationThreshold
}
