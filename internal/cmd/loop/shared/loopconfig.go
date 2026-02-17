package shared

import "time"

// Default configuration values.
const (
	DefaultMaxLoops                  = 50
	DefaultStagnationThreshold       = 3
	DefaultTimeoutMinutes            = 15
	DefaultCallsPerHour              = 100
	DefaultCompletionThreshold       = 2
	DefaultSessionExpirationHours    = 24
	DefaultSameErrorThreshold        = 5
	DefaultOutputDeclineThreshold    = 70
	DefaultMaxConsecutiveTestLoops   = 3
	DefaultSafetyCompletionThreshold = 5 // Force exit after N consecutive loops with completion indicators
	DefaultLoopDelaySeconds          = 3 // Seconds to wait between loop iterations
)

// Config holds loop-specific configuration.
type Config struct {
	MaxLoops                  int  `yaml:"max_loops" mapstructure:"max_loops"`
	StagnationThreshold       int  `yaml:"stagnation_threshold" mapstructure:"stagnation_threshold"`
	TimeoutMinutes            int  `yaml:"timeout_minutes" mapstructure:"timeout_minutes"`
	AutoConfirm               bool `yaml:"auto_confirm" mapstructure:"auto_confirm"`
	CallsPerHour              int  `yaml:"calls_per_hour" mapstructure:"calls_per_hour"`
	CompletionThreshold       int  `yaml:"completion_threshold" mapstructure:"completion_threshold"`
	SessionExpirationHours    int  `yaml:"session_expiration_hours" mapstructure:"session_expiration_hours"`
	SameErrorThreshold        int  `yaml:"same_error_threshold" mapstructure:"same_error_threshold"`
	OutputDeclineThreshold    int  `yaml:"output_decline_threshold" mapstructure:"output_decline_threshold"`
	MaxConsecutiveTestLoops   int  `yaml:"max_consecutive_test_loops" mapstructure:"max_consecutive_test_loops"`
	LoopDelaySeconds          int  `yaml:"loop_delay_seconds" mapstructure:"loop_delay_seconds"`
	SafetyCompletionThreshold int  `yaml:"safety_completion_threshold" mapstructure:"safety_completion_threshold"`
}

// DefaultConfig returns the default loop configuration.
func DefaultConfig() Config {
	return Config{
		MaxLoops:                  DefaultMaxLoops,
		StagnationThreshold:       DefaultStagnationThreshold,
		TimeoutMinutes:            DefaultTimeoutMinutes,
		AutoConfirm:               false,
		CallsPerHour:              DefaultCallsPerHour,
		CompletionThreshold:       DefaultCompletionThreshold,
		SessionExpirationHours:    DefaultSessionExpirationHours,
		SameErrorThreshold:        DefaultSameErrorThreshold,
		OutputDeclineThreshold:    DefaultOutputDeclineThreshold,
		MaxConsecutiveTestLoops:   DefaultMaxConsecutiveTestLoops,
		LoopDelaySeconds:          DefaultLoopDelaySeconds,
		SafetyCompletionThreshold: DefaultSafetyCompletionThreshold,
	}
}

// Timeout returns the per-loop timeout duration.
func (c Config) Timeout() time.Duration {
	if c.TimeoutMinutes <= 0 {
		return time.Duration(DefaultTimeoutMinutes) * time.Minute
	}
	return time.Duration(c.TimeoutMinutes) * time.Minute
}

// GetMaxLoops returns the max loops with default fallback.
func (c Config) GetMaxLoops() int {
	if c.MaxLoops <= 0 {
		return DefaultMaxLoops
	}
	return c.MaxLoops
}

// GetStagnationThreshold returns the stagnation threshold with default fallback.
func (c Config) GetStagnationThreshold() int {
	if c.StagnationThreshold <= 0 {
		return DefaultStagnationThreshold
	}
	return c.StagnationThreshold
}

// GetCallsPerHour returns the calls per hour limit with default fallback.
// Returns 0 to disable rate limiting.
func (c Config) GetCallsPerHour() int {
	if c.CallsPerHour < 0 {
		return 0 // Disabled
	}
	if c.CallsPerHour == 0 {
		return DefaultCallsPerHour
	}
	return c.CallsPerHour
}

// GetCompletionThreshold returns the completion indicator threshold with default fallback.
func (c Config) GetCompletionThreshold() int {
	if c.CompletionThreshold <= 0 {
		return DefaultCompletionThreshold
	}
	return c.CompletionThreshold
}

// GetSessionExpirationHours returns the session expiration hours with default fallback.
func (c Config) GetSessionExpirationHours() int {
	if c.SessionExpirationHours <= 0 {
		return DefaultSessionExpirationHours
	}
	return c.SessionExpirationHours
}

// GetSameErrorThreshold returns the same error threshold with default fallback.
func (c Config) GetSameErrorThreshold() int {
	if c.SameErrorThreshold <= 0 {
		return DefaultSameErrorThreshold
	}
	return c.SameErrorThreshold
}

// GetOutputDeclineThreshold returns the output decline threshold percentage with default fallback.
func (c Config) GetOutputDeclineThreshold() int {
	if c.OutputDeclineThreshold <= 0 {
		return DefaultOutputDeclineThreshold
	}
	return c.OutputDeclineThreshold
}

// GetMaxConsecutiveTestLoops returns the max consecutive test loops with default fallback.
func (c Config) GetMaxConsecutiveTestLoops() int {
	if c.MaxConsecutiveTestLoops <= 0 {
		return DefaultMaxConsecutiveTestLoops
	}
	return c.MaxConsecutiveTestLoops
}

// GetLoopDelaySeconds returns the loop delay in seconds with default fallback.
func (c Config) GetLoopDelaySeconds() int {
	if c.LoopDelaySeconds <= 0 {
		return DefaultLoopDelaySeconds
	}
	return c.LoopDelaySeconds
}

// GetSafetyCompletionThreshold returns the safety completion threshold with default fallback.
func (c Config) GetSafetyCompletionThreshold() int {
	if c.SafetyCompletionThreshold <= 0 {
		return DefaultSafetyCompletionThreshold
	}
	return c.SafetyCompletionThreshold
}
