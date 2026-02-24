package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
)

const projectName = "testrepo"

// Harness provides an isolated filesystem environment for integration tests.
// It creates temp directories, sets XDG env vars, registers a project, and
// optionally persists config — all driven by what the caller wired into the Factory.
type Harness struct {
	t          *testing.T
	factory    *cmdutil.Factory
	projectDir string
	configDir  string
	dataDir    string
	stateDir   string
	prevDir    string
}

// RunResult holds the outcome of a CLI command execution.
type RunResult struct {
	ExitCode int
	Err      error
}

// Option configures harness behavior.
type Option func(*options)

type options struct {
	skipProjectRegistration bool
	cfg                     config.Config
}

// WithoutProjectRegistered skips automatic project registration.
// Use this for testing init/project-init commands that do their own registration.
func WithoutProjectRegistered() Option {
	return func(o *options) { o.skipProjectRegistration = true }
}

// WithConfig persists a config.Config to disk via its store Write() methods.
// The Config must be created AFTER harness.New() sets env vars — pass it via
// a closure or create it inside the test after New() returns... but that's
// too late. Instead, create Config lazily: the factory's Config closure will
// resolve after env vars are set. Use WriteConfig() on the harness after New().
func WithConfig(cfg config.Config) Option {
	return func(o *options) { o.cfg = cfg }
}

// New creates an isolated test environment and returns a Harness.
//
// The caller passes a pre-built *cmdutil.Factory. The harness:
//  1. Creates temp dirs for config, data, state, and project
//  2. Sets CLAWKER_CONFIG_DIR, CLAWKER_DATA_DIR, CLAWKER_STATE_DIR via t.Setenv
//  3. If f.ProjectManager is set (and not opted out): registers "testrepo" at projectDir
//  4. Chdirs to projectDir (restored on cleanup)
func New(t *testing.T, f *cmdutil.Factory, opts ...Option) *Harness {
	t.Helper()

	var o options
	for _, opt := range opts {
		opt(&o)
	}

	// Resolve symlinks on the base temp dir so registry paths match
	// os.Getwd() after chdir (macOS: /var → /private/var).
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("harness: resolving temp dir symlinks: %v", err)
	}
	configDir := filepath.Join(base, "config")
	dataDir := filepath.Join(base, "data")
	stateDir := filepath.Join(base, "state")
	projectDir := filepath.Join(base, "project")

	for _, dir := range []string{configDir, dataDir, stateDir, projectDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("harness: creating dir %s: %v", dir, err)
		}
	}

	t.Setenv("CLAWKER_CONFIG_DIR", configDir)
	t.Setenv("CLAWKER_DATA_DIR", dataDir)
	t.Setenv("CLAWKER_STATE_DIR", stateDir)

	// Register project by default if the factory has a ProjectManager wired.
	if f.ProjectManager != nil && !o.skipProjectRegistration {
		pm, err := f.ProjectManager()
		if err != nil {
			t.Fatalf("harness: getting project manager: %v", err)
		}
		if _, err := pm.Register(context.Background(), projectName, projectDir); err != nil {
			t.Fatalf("harness: registering project: %v", err)
		}
	}

	// Chdir to project directory so config discovery works from CWD.
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("harness: getting cwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		t.Fatalf("harness: chdir to project dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	h := &Harness{
		t:          t,
		factory:    f,
		projectDir: projectDir,
		configDir:  configDir,
		dataDir:    dataDir,
		stateDir:   stateDir,
		prevDir:    prevDir,
	}

	// Persist config to disk if provided via WithConfig.
	if o.cfg != nil {
		h.WriteConfig(o.cfg)
	}

	return h
}

// WriteConfig persists a config.Config to disk by calling Write() on its
// project and settings stores. Call this after New() to persist config that
// was created using the harness's env vars.
func (h *Harness) WriteConfig(cfg config.Config) {
	h.t.Helper()
	if err := cfg.ProjectStore().Write(); err != nil {
		h.t.Fatalf("harness: writing project config: %v", err)
	}
	if err := cfg.SettingsStore().Write(); err != nil {
		h.t.Fatalf("harness: writing settings config: %v", err)
	}
}

// Run executes a CLI command through the full root.NewCmdRoot Cobra pipeline
// using the stored factory. IO is whatever the caller wired into the factory.
func (h *Harness) Run(args ...string) *RunResult {
	h.t.Helper()

	cmd, err := root.NewCmdRoot(h.factory, "test", "test")
	if err != nil {
		return &RunResult{ExitCode: 1, Err: err}
	}

	cmd.SetArgs(args)

	err = cmd.Execute()

	exitCode := 0
	if err != nil {
		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.Code
		} else {
			exitCode = 1
		}
	}

	return &RunResult{ExitCode: exitCode, Err: err}
}

// ProjectDir returns the path to the isolated project directory.
func (h *Harness) ProjectDir() string {
	return h.projectDir
}
