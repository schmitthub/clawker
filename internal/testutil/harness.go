package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/config"
	"gopkg.in/yaml.v3"
)

// Harness provides an isolated test environment for clawker tests.
// It manages:
// - Temporary project directory with clawker.yaml
// - Isolated config directory (~/.local/clawker/)
// - Environment variable backup and restoration
// - Automatic cleanup via t.Cleanup()
type Harness struct {
	T              *testing.T
	ProjectDir     string            // Temp dir with clawker.yaml
	ConfigDir      string            // Isolated ~/.local/clawker/
	OriginalEnv    map[string]string // For restoration
	OriginalDir    string            // Original working directory
	Config         *config.Config    // The test config
	Project        string            // Project name
	envKeys        []string          // Keys we've set for cleanup
	changedDir     bool              // Whether we changed directory
}

// HarnessOption configures a Harness.
type HarnessOption func(*Harness)

// WithProject sets the project name.
// Note: This should be used before WithConfig/WithConfigBuilder, or the config's
// project name will override this setting.
func WithProject(name string) HarnessOption {
	return func(h *Harness) {
		h.Project = name
	}
}

// WithConfig sets the config directly.
func WithConfig(cfg *config.Config) HarnessOption {
	return func(h *Harness) {
		h.Config = cfg
		if cfg != nil {
			h.Project = cfg.Project
		}
	}
}

// WithConfigBuilder sets the config from a ConfigBuilder.
func WithConfigBuilder(cb *ConfigBuilder) HarnessOption {
	return func(h *Harness) {
		cfg := cb.Build()
		h.Config = cfg
		if cfg != nil {
			h.Project = cfg.Project
		}
	}
}

// NewHarness creates a new test harness with isolation.
// The harness automatically cleans up all resources when the test completes.
func NewHarness(t *testing.T, opts ...HarnessOption) *Harness {
	t.Helper()

	// Create temp directories
	projectDir := t.TempDir() // Auto-cleaned by testing framework
	configDir := t.TempDir()  // Auto-cleaned by testing framework

	// Resolve symlinks for consistent path comparisons (e.g., /var -> /private/var on macOS)
	projectDir, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		t.Fatalf("failed to resolve project directory symlinks: %v", err)
	}
	configDir, err = filepath.EvalSymlinks(configDir)
	if err != nil {
		t.Fatalf("failed to resolve config directory symlinks: %v", err)
	}

	// Save original working directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get current directory: %v", err)
	}

	h := &Harness{
		T:           t,
		ProjectDir:  projectDir,
		ConfigDir:   configDir,
		OriginalDir: origDir,
		OriginalEnv: make(map[string]string),
		envKeys:     make([]string, 0),
	}

	// Apply options
	for _, opt := range opts {
		opt(h)
	}

	// If no config provided, use minimal valid config
	if h.Config == nil {
		h.Config = MinimalValidConfig().Build()
	}

	// Sync project name between harness and config
	// - If project was explicitly set via WithProject, update config
	// - Otherwise, use project name from config
	if h.Project != "" {
		h.Config.Project = h.Project
	} else {
		h.Project = h.Config.Project
	}

	// Write clawker.yaml to project directory
	if err := h.writeConfig(); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// Set up cleanup
	t.Cleanup(h.cleanup)

	return h
}

// writeConfig writes the Config to clawker.yaml in the project directory.
func (h *Harness) writeConfig() error {
	data, err := yaml.Marshal(h.Config)
	if err != nil {
		return err
	}

	configPath := filepath.Join(h.ProjectDir, "clawker.yaml")
	return os.WriteFile(configPath, data, 0644)
}

// cleanup restores the original state.
func (h *Harness) cleanup() {
	// Restore working directory if changed
	if h.changedDir {
		if err := os.Chdir(h.OriginalDir); err != nil {
			h.T.Errorf("failed to restore working directory: %v", err)
		}
	}

	// Restore environment variables
	for _, key := range h.envKeys {
		original, existed := h.OriginalEnv[key]
		if existed {
			os.Setenv(key, original)
		} else {
			os.Unsetenv(key)
		}
	}
}

// ----------------------------------------------------------------
// Environment Management
// ----------------------------------------------------------------

// SetEnv sets an environment variable and registers it for cleanup.
// The original value is restored when the test completes.
func (h *Harness) SetEnv(key, value string) {
	// Save original value if not already saved
	if _, exists := h.OriginalEnv[key]; !exists {
		h.OriginalEnv[key] = os.Getenv(key)
		h.envKeys = append(h.envKeys, key)
	}

	if err := os.Setenv(key, value); err != nil {
		h.T.Fatalf("failed to set env %s: %v", key, err)
	}
}

// UnsetEnv unsets an environment variable and registers it for cleanup.
func (h *Harness) UnsetEnv(key string) {
	// Save original value if not already saved
	if _, exists := h.OriginalEnv[key]; !exists {
		h.OriginalEnv[key] = os.Getenv(key)
		h.envKeys = append(h.envKeys, key)
	}

	if err := os.Unsetenv(key); err != nil {
		h.T.Fatalf("failed to unset env %s: %v", key, err)
	}
}

// Chdir changes to the project directory and registers restoration for cleanup.
func (h *Harness) Chdir() {
	h.T.Helper()
	if err := os.Chdir(h.ProjectDir); err != nil {
		h.T.Fatalf("failed to change to project directory: %v", err)
	}
	h.changedDir = true
}

// ----------------------------------------------------------------
// Resource Name Generation
// ----------------------------------------------------------------

const (
	// NamePrefix is the standard prefix for clawker resources
	NamePrefix = "clawker"
)

// ContainerName generates the expected container name for an agent.
// Format: clawker.<project>.<agent>
func (h *Harness) ContainerName(agent string) string {
	return NamePrefix + "." + h.Project + "." + agent
}

// ImageName returns the expected image name.
// Returns DefaultImage if set, otherwise clawker.<project>:latest
func (h *Harness) ImageName() string {
	if h.Config != nil && h.Config.DefaultImage != "" {
		return h.Config.DefaultImage
	}
	return NamePrefix + "-" + h.Project + ":latest"
}

// VolumeName generates the expected volume name for an agent and purpose.
// Format: clawker.<project>.<agent>-<purpose>
func (h *Harness) VolumeName(agent, purpose string) string {
	return NamePrefix + "." + h.Project + "." + agent + "-" + purpose
}

// NetworkName returns the clawker network name.
func (h *Harness) NetworkName() string {
	return NamePrefix + "-net"
}

// ----------------------------------------------------------------
// Utilities
// ----------------------------------------------------------------

// ConfigPath returns the path to the clawker.yaml file.
func (h *Harness) ConfigPath() string {
	return filepath.Join(h.ProjectDir, "clawker.yaml")
}

// WriteFile writes a file to the project directory.
func (h *Harness) WriteFile(relPath, content string) {
	h.T.Helper()
	fullPath := filepath.Join(h.ProjectDir, relPath)

	// Create parent directories if needed
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		h.T.Fatalf("failed to create directory %s: %v", dir, err)
	}

	if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
		h.T.Fatalf("failed to write file %s: %v", fullPath, err)
	}
}

// ReadFile reads a file from the project directory.
func (h *Harness) ReadFile(relPath string) string {
	h.T.Helper()
	fullPath := filepath.Join(h.ProjectDir, relPath)

	data, err := os.ReadFile(fullPath)
	if err != nil {
		h.T.Fatalf("failed to read file %s: %v", fullPath, err)
	}

	return string(data)
}

// FileExists checks if a file exists in the project directory.
func (h *Harness) FileExists(relPath string) bool {
	fullPath := filepath.Join(h.ProjectDir, relPath)
	_, err := os.Stat(fullPath)
	return err == nil
}

// UpdateConfig updates the config and rewrites clawker.yaml.
func (h *Harness) UpdateConfig(fn func(*config.Config)) {
	h.T.Helper()
	fn(h.Config)
	if err := h.writeConfig(); err != nil {
		h.T.Fatalf("failed to update config: %v", err)
	}
}
