package config

import (
	"github.com/spf13/viper"
)

// FakeConfigOptions defines dependency overrides for fake config construction.
type FakeConfigOptions struct {
	Viper *viper.Viper
}

// NewMockConfig returns an in-memory config with defaults and env support.
// It does not load any files and uses a mem filesystem for safe test usage.
func NewMockConfig() Config {
	return NewFakeConfig(FakeConfigOptions{})
}

// NewFakeConfig returns a config with injected dependencies for testing.
// Nil fields fall back to production defaults.
func NewFakeConfig(opts FakeConfigOptions) Config {
	v := opts.Viper
	if v == nil {
		v = newViperConfig()
	}

	return newConfig(v)
}

// NewConfigFromString creates a config from YAML content.
func NewConfigFromString(str string) (Config, error) {
	return ReadFromString(str)
}
