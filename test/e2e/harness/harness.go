package harness

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/testenv"
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

// SetupResult holds the resolved paths from NewIsolatedFS.
type SetupResult struct {
	BaseDir    string
	ProjectDir string
	ConfigDir  string
	DataDir    string
	StateDir   string
	CacheDir   string
}

// FSOptions allows overriding the project directory name.
type FSOptions struct {
	ProjectDir string // subdirectory name under base (default: "testproject")
}

// NewIsolatedFS creates an isolated test environment.
//
// Delegates XDG directory setup to testenv.New, then adds a project directory
// and chdirs into it (restored on cleanup).
func (h *Harness) NewIsolatedFS(opts *FSOptions) *SetupResult {
	h.T.Helper()

	if opts == nil {
		opts = &FSOptions{}
	}
	if opts.ProjectDir == "" {
		opts.ProjectDir = "testproject"
	}

	env := testenv.New(h.T)

	projectDir := filepath.Join(env.Dirs.Base, opts.ProjectDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		h.T.Fatalf("harness: creating project dir %s: %v", projectDir, err)
	}

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
		BaseDir:    env.Dirs.Base,
		ProjectDir: projectDir,
		ConfigDir:  env.Dirs.Config,
		DataDir:    env.Dirs.Data,
		StateDir:   env.Dirs.State,
		CacheDir:   env.Dirs.Cache,
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
