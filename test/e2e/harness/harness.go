package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/testenv"
)

// Harness provides an isolated filesystem environment for integration tests.
// Each Run() creates a fresh Factory from Opts — mirroring a real CLI process.
type Harness struct {
	T    *testing.T
	Opts *FactoryOptions
}

// RunResult holds the outcome of a CLI command execution.
type RunResult struct {
	ExitCode int
	Err      error
	Stdout   string
	Stderr   string
	Factory  *cmdutil.Factory
}

// SetupResult holds the resolved paths from NewIsolatedFS.
type SetupResult struct {
	*testenv.Env
	ProjectDir string
}

// FSOptions allows overriding the project directory name.
type FSOptions struct {
	ProjectDir string // subdirectory name under base (default: "testproject")
}

// NewIsolatedFS creates an isolated test environment.
func (h *Harness) NewIsolatedFS(opts *FSOptions) *SetupResult {
	h.T.Helper()

	if opts == nil {
		opts = &FSOptions{}
	}
	if opts.ProjectDir == "" {
		opts.ProjectDir = "testproject"
	}

	// Build the clawker binary so the hostproxy daemon can spawn it.
	ensureClawkerBinary(h.T)

	env := testenv.New(h.T)

	projectDir := filepath.Join(env.Dirs.Base, opts.ProjectDir)
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		h.T.Fatalf("harness: creating project dir %s: %v", projectDir, err)
	}

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

	// Dump log file on test failure (registered before Docker cleanup so it runs after).
	logDir := filepath.Join(env.Dirs.State, "logs")
	h.T.Cleanup(func() {
		if !h.T.Failed() {
			return
		}
		for _, name := range []string{"clawker.log", "firewall.log"} {
			data, err := os.ReadFile(filepath.Join(logDir, name))
			if err != nil {
				continue
			}
			h.T.Logf("=== %s ===\n%s", name, string(data))
		}
	})

	// Single cleanup: daemons, firewall infra, then test-labeled resources.
	h.T.Cleanup(func() {
		cleanupTestEnvironment(h.T, h)
	})

	return &SetupResult{
		Env:        env,
		ProjectDir: projectDir,
	}
}

// Chdir changes the working directory and registers a cleanup to restore it.
func (r *SetupResult) Chdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("harness: chdir to %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(r.ProjectDir) })
}

// clawkerBinaryOnce ensures the clawker binary is built exactly once per test process.
var clawkerBinaryOnce sync.Once

// ensureClawkerBinary builds the clawker binary and sets CLAWKER_EXECUTABLE
// so the hostproxy daemon can spawn it. Built once per test process.
func ensureClawkerBinary(t *testing.T) {
	t.Helper()
	clawkerBinaryOnce.Do(func() {
		// Find the repo root (test/e2e/harness → ../../..).
		_, thisFile, _, _ := runtime.Caller(0)
		repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")

		binPath := filepath.Join(repoRoot, "bin", "clawker")
		cmd := exec.CommandContext(context.Background(), "go", "build", "-o", binPath, "./cmd/clawker")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("harness: building clawker binary: %s (%v)", string(out), err)
		}
	})

	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	t.Setenv("CLAWKER_EXECUTABLE", filepath.Join(repoRoot, "bin", "clawker"))
}

// cleanupTestEnvironment is the single cleanup entrypoint for all e2e tests.
// Order: stop daemons → remove firewall infra → remove test-labeled resources.
func cleanupTestEnvironment(t *testing.T, h *Harness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Stop daemons (via CLI so they use the test's isolated env vars).
	h.Run("firewall", "down")
	h.Run("host-proxy", "stop")

	// 2. Remove shared firewall infrastructure containers (not test-labeled).
	// Build the label key=value from config accessors so we stay consistent
	// with how production code (internal/firewall/manager.go) stamps labels.
	cfg := resolveCleanupConfig(t, h)
	firewallLabel := fmt.Sprintf("%s=%s", cfg.LabelPurpose(), cfg.PurposeFirewall())
	if ids, err := dockerListByLabel(ctx, "container", firewallLabel); err != nil {
		t.Logf("cleanup: docker ps firewall label=%s: %v (firewall containers may leak)", firewallLabel, err)
	} else if len(ids) > 0 {
		//nolint:gosec // label is derived from config accessors, not user input
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Logf("cleanup: docker rm firewall: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}

	// 3. Remove test-labeled containers, volumes, networks.
	label := fmt.Sprintf("%s.test.name=%s", cfg.LabelDomain(), t.Name())

	if ids, err := dockerListByLabel(ctx, "container", label); err != nil {
		t.Logf("cleanup: docker ps label=%s: %v (containers may leak)", label, err)
	} else if len(ids) > 0 {
		//nolint:gosec // label is derived from t.Name(), not user input
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Logf("cleanup: docker rm: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}

	if ids, err := dockerListByLabel(ctx, "volume", label); err != nil {
		t.Logf("cleanup: docker volume ls label=%s: %v (volumes may leak)", label, err)
	} else if len(ids) > 0 {
		//nolint:gosec // label is derived from t.Name(), not user input
		args := append([]string{"volume", "rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Logf("cleanup: docker volume rm: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}

	if ids, err := dockerListByLabel(ctx, "network", label); err != nil {
		t.Logf("cleanup: docker network ls label=%s: %v (networks may leak)", label, err)
	} else if len(ids) > 0 {
		for _, id := range ids {
			//nolint:gosec // id comes from docker ls output
			if out, err := exec.CommandContext(ctx, "docker", "network", "rm", id).CombinedOutput(); err != nil {
				t.Logf("cleanup: docker network rm %s: %s (%v)", id, strings.TrimSpace(string(out)), err)
			}
		}
	}
}

// resolveCleanupConfig returns a config.Config for label accessors during
// cleanup. It uses the harness' own Config constructor when set so labels
// match the test's isolated env exactly; otherwise it falls back to a blank
// config, which still exposes the canonical label/purpose constants. Any
// failure to construct the configured Config is non-fatal for cleanup —
// we log and fall back to the blank config.
func resolveCleanupConfig(t *testing.T, h *Harness) config.Config {
	t.Helper()
	if h != nil && h.Opts != nil && h.Opts.Config != nil {
		if cfg, err := h.Opts.Config(); err == nil && cfg != nil {
			return cfg
		} else if err != nil {
			t.Logf("cleanup: resolving harness config: %v (falling back to blank config)", err)
		}
	}
	return configmocks.NewBlankConfig()
}

// dockerListByLabel returns IDs of Docker resources matching a label filter.
// Returns an error when `docker` fails to execute so callers can surface the
// underlying reason (daemon down, permission denied) instead of silently
// assuming "nothing to clean", which would leak resources into the next test.
func dockerListByLabel(ctx context.Context, resourceType, label string) ([]string, error) {
	var cmd *exec.Cmd
	switch resourceType {
	case "container":
		cmd = exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label="+label)
	case "volume":
		cmd = exec.CommandContext(ctx, "docker", "volume", "ls", "-q", "--filter", "label="+label)
	case "network":
		cmd = exec.CommandContext(ctx, "docker", "network", "ls", "-q", "--filter", "label="+label)
	default:
		return nil, fmt.Errorf("dockerListByLabel: unsupported resource type %q", resourceType)
	}

	out, err := cmd.Output()
	if err != nil {
		// Include stderr from docker on ExitError so the failure mode is
		// visible in test logs (e.g. "Cannot connect to the Docker daemon").
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("docker %s: %w: %s", resourceType, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("docker %s: %w", resourceType, err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var ids []string
	for _, line := range lines {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// Run creates a fresh Factory and executes a CLI command through the full
// root.NewCmdRoot Cobra pipeline — each call is a fresh process.
func (h *Harness) Run(args ...string) *RunResult {
	h.T.Helper()

	f, _, out, errOut := NewFactory(h.T, h.Opts)

	rootCmd, err := root.NewCmdRoot(f, "test", "test")
	if err != nil {
		return &RunResult{ExitCode: 1, Err: err, Stderr: err.Error()}
	}

	rootCmd.SilenceErrors = true
	rootCmd.SetArgs(args)

	cmd, err := rootCmd.ExecuteC()

	exitCode := 0
	if err != nil {
		if errors.Is(err, cmdutil.SilentError) {
			// Already displayed
		} else {
			cs := f.IOStreams.ColorScheme()
			fmt.Fprintf(f.IOStreams.ErrOut, "%s %s\n", cs.FailureIcon(), err)
			if cmd != nil {
				var flagErr *cmdutil.FlagError
				if errors.As(err, &flagErr) {
					fmt.Fprintln(f.IOStreams.ErrOut, cmd.UsageString())
				}
			}
		}

		var exitErr *cmdutil.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.Code
		} else {
			exitCode = 1
		}
	}

	return &RunResult{
		ExitCode: exitCode,
		Err:      err,
		Stdout:   out.String(),
		Stderr:   errOut.String(),
		Factory:  f,
	}
}

// RunInContainer runs a command inside a fresh container and returns the result.
// The container starts, runs the command, and is automatically removed.
func (h *Harness) RunInContainer(agent string, cmd ...string) *RunResult {
	h.T.Helper()
	args := []string{"container", "run", "--rm", "--agent", agent, "@"}
	args = append(args, cmd...)
	return h.Run(args...)
}

// ExecInContainer runs a command inside an existing container as the container user (claude).
func (h *Harness) ExecInContainer(agent string, cmd ...string) *RunResult {
	h.T.Helper()
	args := []string{"container", "exec", "--user", "claude", "--agent", agent}
	args = append(args, cmd...)
	return h.Run(args...)
}

// ExecInContainerAsRoot runs a command inside an existing container as root.
func (h *Harness) ExecInContainerAsRoot(agent string, cmd ...string) *RunResult {
	h.T.Helper()
	args := []string{"container", "exec", "--agent", agent}
	args = append(args, cmd...)
	return h.Run(args...)
}
