package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
)

// Harness provides an isolated filesystem environment for integration tests.
// It creates temp directories, sets XDG env vars, registers a project, and
// optionally persists config — all driven by what the caller wired into the Factory.
type Harness struct {
	T       *testing.T
	Factory *cmdutil.Factory
}

// RunResult holds the outcome of a CLI command execution.
type RunResult struct {
	ExitCode int
	Err      error
}

type SetupResult struct {
	BaseDir    string
	ProjectDir string
	ConfigDir  string
	DataDir    string
	StateDir   string
}

type FSOptions struct {
	ProjectDir string
	ConfigDir  string
	DataDir    string
	StateDir   string
}

// New creates an isolated test environment and returns a Harness.
//
// The caller passes a pre-built *cmdutil.Factory. The harness:
//  1. Creates temp dirs for config, data, state, and project
//  2. Sets CLAWKER_CONFIG_DIR, CLAWKER_DATA_DIR, CLAWKER_STATE_DIR via t.Setenv
//  3. Chdirs to projectDir (restored on cleanup)
func (h *Harness) NewIsolatedFS(opts *FSOptions) *SetupResult {
	h.T.Helper()

	if opts == nil {
		opts = &FSOptions{}
	}

	if opts.ProjectDir == "" {
		opts.ProjectDir = "testproject"
	}
	if opts.ConfigDir == "" {
		opts.ConfigDir = "config"
	}
	if opts.DataDir == "" {
		opts.DataDir = "data"
	}
	if opts.StateDir == "" {
		opts.StateDir = "state"
	}

	// Resolve symlinks on the base temp dir so registry paths match
	// os.Getwd() after chdir (macOS: /var → /private/var).
	base, err := filepath.EvalSymlinks(h.T.TempDir())
	if err != nil {
		h.T.Fatalf("harness: resolving temp dir symlinks: %v", err)
	}
	configDir := filepath.Join(base, opts.ConfigDir)
	dataDir := filepath.Join(base, opts.DataDir)
	stateDir := filepath.Join(base, opts.StateDir)
	projectDir := filepath.Join(base, opts.ProjectDir)

	for _, dir := range []string{configDir, dataDir, stateDir, projectDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			h.T.Fatalf("harness: creating dir %s: %v", dir, err)
		}
	}

	h.T.Setenv("CLAWKER_CONFIG_DIR", configDir)
	h.T.Setenv("CLAWKER_DATA_DIR", dataDir)
	h.T.Setenv("CLAWKER_STATE_DIR", stateDir)

	// Chdir to project directory so config discovery works from CWD.
	prevDir, err := os.Getwd()
	if err != nil {
		h.T.Fatalf("harness: getting cwd: %v", err)
	}
	if err := os.Chdir(projectDir); err != nil {
		h.T.Fatalf("harness: chdir to project dir: %v", err)
	}
	h.T.Cleanup(func() {
		_ = os.Chdir(prevDir)
	})

	return &SetupResult{
		BaseDir:    base,
		ProjectDir: projectDir,
		ConfigDir:  configDir,
		DataDir:    dataDir,
		StateDir:   stateDir,
	}
}

// Chdir changes the working directory and registers a cleanup to restore it
// to ProjectDir when the test ends.
func (r *SetupResult) Chdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("harness: chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(r.ProjectDir) })
}

// Run executes a CLI command through the full root.NewCmdRoot Cobra pipeline
// using the stored factory. IO is whatever the caller wired into the factory.
func (h *Harness) Run(args ...string) *RunResult {
	h.T.Helper()

	cmd, err := root.NewCmdRoot(h.Factory, "test", "test")
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
