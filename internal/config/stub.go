package config

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// NewBlankConfig returns a Config seeded with default values.
func NewBlankConfig() *Config {
	return NewFromString(defaultConfigStr)
}

// NewFromString returns a Config parsed from the given YAML string.
func NewFromString(cfgStr string) *Config {
	return ReadFromString(cfgStr)
}

// NewIsolatedTestConfig creates a blank config, swaps Read for test isolation,
// and stubs the write path to a temp directory. Returns the config and a
// function that reads any data written to disk.
func NewIsolatedTestConfig(t *testing.T) (*Config, func(io.Writer, io.Writer)) {
	t.Helper()
	c := ReadFromString("")

	// Swap Read to return this config (test isolation).
	Read = func(_ *Config) (*Config, error) {
		return c, nil
	}

	readConfigs := StubWriteConfig(t)
	return c, readConfigs
}

// StubWriteConfig stubs the filesystem where config files are written.
// It returns a function that reads written config files into io.Writers.
// Automatically cleans up environment variables and written files.
func StubWriteConfig(t *testing.T) func(io.Writer, io.Writer) {
	t.Helper()
	tempDir := t.TempDir()
	t.Setenv("CLAWKER_HOME", tempDir)
	return func(wc io.Writer, wh io.Writer) {
		configData, err := os.ReadFile(filepath.Join(tempDir, "clawker.yaml"))
		if err == nil {
			_, _ = wc.Write(configData)
		}

		settingsData, err := os.ReadFile(filepath.Join(tempDir, "settings.yaml"))
		if err == nil {
			_, _ = wh.Write(settingsData)
		}
	}
}
