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

	"github.com/schmitthub/clawker/controlplane/manager"
	"github.com/schmitthub/clawker/internal/cmd/root"
	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/testenv"
)

// EnsureNoControlPlane stops and removes any pre-existing CP container so the
// test's own bring-up creates a CP whose certs chain to THIS test env's
// freshly minted CLI CA (manager.EnsureRunning seeds via
// auth.EnsureAuthMaterial before create). Reusing a foreign CP passes the
// mTLS-less healthz reuse probe but fails every admin RPC — its certs chain
// to another environment's CA. Call it after NewIsolatedFS, before the first
// command that bootstraps the CP. Best-effort: failures are logged, never
// fatal.
func EnsureNoControlPlane(t *testing.T, timeout time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cfg, err := config.NewConfig()
	if err != nil {
		t.Logf("harness: cp pre-clean: config: %v (skipping)", err)
		return
	}
	dc, err := docker.NewClient(ctx, cfg, logger.Nop())
	if err != nil {
		t.Logf("harness: cp pre-clean: docker client: %v (skipping)", err)
		return
	}
	defer func() { _ = dc.Close() }()
	if stopErr := manager.Stop(ctx, dc); stopErr != nil {
		t.Logf("harness: cp pre-clean: manager.Stop: %v (ignoring)", stopErr)
	}
}

// CleanupReport captures what infrastructure was running when cleanup ran.
// Tests can inspect this to fail if expected services were never started.
type CleanupReport struct {
	FirewallContainers int // count of purpose=firewall containers found
	CPContainers       int // count of purpose=controlplane containers found
	AgentContainers    int // count of test-labeled containers found
}

// Harness provides an isolated filesystem environment for integration tests.
// Each Run() creates a fresh Factory from Opts — mirroring a real CLI process.
type Harness struct {
	T    *testing.T
	Opts *FactoryOptions

	// Cleanup stores the cleanup report after the test environment is torn down.
	// Firewall tests can check this to fail if the stack was never running.
	Cleanup *CleanupReport
}

// New builds a Harness whose FactoryOptions start at the zero baseline —
// every dependency mocked, no real CP or admin client. Each mutator opts
// one dependency into its real production wiring. Cleanup is populated by
// the harness at teardown.
func New(t *testing.T, mutators ...func(*FactoryOptions)) *Harness {
	t.Helper()
	opts := &FactoryOptions{
		Config:              nil,
		Client:              nil,
		ProjectManager:      nil,
		GitManager:          nil,
		HostProxy:           nil,
		SocketBridge:        nil,
		UseRealAdminClient:  false,
		UseRealControlPlane: false,
	}
	for _, mutate := range mutators {
		mutate(opts)
	}
	return &Harness{T: t, Opts: opts, Cleanup: nil}
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

	// Docker CLI plugin discovery (`docker compose`) and registry auth live
	// under the real user's ~/.docker. testenv re-points HOME at the temp
	// base, which would silently strip the compose plugin from the monitor
	// verbs — pin DOCKER_CONFIG to the real location first, unless the
	// caller already set one.
	if os.Getenv("DOCKER_CONFIG") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			h.T.Setenv("DOCKER_CONFIG", filepath.Join(home, ".docker"))
		}
	}

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
		// consts.CPBootLogFile is intentionally absent: nothing writes it
		// yet (host-side manager logs land in the CLI log) — dumping it
		// would always ENOENT-skip and imply coverage that isn't there.
		for _, name := range []string{logger.DefaultLogFileName, consts.HostProxyLogFile, consts.ControlPlaneLogFile} {
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
	t.Setenv(consts.EnvExecutable, filepath.Join(repoRoot, "bin", "clawker"))
}

// cleanupTestEnvironment is the single cleanup entrypoint for all e2e tests.
// Order: snapshot what's running → stop daemons → remove firewall infra → remove test-labeled resources.
// The snapshot is stored on h.Cleanup so tests can inspect it post-cleanup.
func cleanupTestEnvironment(t *testing.T, h *Harness) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report := &CleanupReport{}

	// Snapshot infrastructure state BEFORE tearing anything down.
	firewallLabel := fmt.Sprintf("%s=%s", consts.LabelPurpose, consts.PurposeFirewall)
	cpLabel := fmt.Sprintf("%s=%s", consts.LabelPurpose, consts.PurposeControlPlane)
	testLabel := fmt.Sprintf("%s=%s", consts.LabelTestName, t.Name())

	if ids, err := dockerListByLabel(ctx, "container", firewallLabel); err == nil {
		report.FirewallContainers = len(ids)
	}
	if ids, err := dockerListByLabel(ctx, "container", cpLabel); err == nil {
		report.CPContainers = len(ids)
	}
	if ids, err := dockerListByLabel(ctx, "container", testLabel); err == nil {
		report.AgentContainers = len(ids)
	}
	h.Cleanup = report

	// 1. Stop daemons (via CLI so they use the test's isolated env vars).
	h.Run("firewall", "down")
	h.Run("host-proxy", "stop")

	// 2. Remove shared firewall infrastructure containers (not test-labeled).
	if ids, err := dockerListByLabel(ctx, "container", firewallLabel); err != nil {
		t.Logf("cleanup: docker ps firewall label=%s: %v (firewall containers may leak)", firewallLabel, err)
	} else if len(ids) > 0 {
		//nolint:gosec // label is derived from config accessors, not user input
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Logf("cleanup: docker rm firewall: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}

	// 3. Remove control plane container if left running (not test-labeled).
	if ids, err := dockerListByLabel(ctx, "container", cpLabel); err != nil {
		t.Logf("cleanup: docker ps control plane label=%s: %v (control plane containers may leak)", cpLabel, err)
	} else if len(ids) > 0 {
		//nolint:gosec // label is derived from config accessors, not user input
		args := append([]string{"rm", "-f"}, ids...)
		if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
			t.Logf("cleanup: docker rm control plane: %s (%v)", strings.TrimSpace(string(out)), err)
		}
	}

	// 4. Remove test-labeled containers, volumes, networks.
	label := testLabel

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

	// 5. Remove test-labeled images (built through the harness client, which
	// stamps LabelConfig.Default onto images too). Last: image removal fails
	// while a container still references the image.
	if ids, err := dockerListByLabel(ctx, "image", label); err != nil {
		t.Logf("cleanup: docker images label=%s: %v (images may leak)", label, err)
	} else if len(ids) > 0 {
		args := append([]string{"rmi", "-f"}, ids...)
		out, rmiErr := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if rmiErr != nil {
			t.Logf("cleanup: docker rmi: %s (%v)", strings.TrimSpace(string(out)), rmiErr)
		}
	}
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
	case "image":
		//nolint:gosec // label is derived from t.Name(), not user input
		cmd = exec.CommandContext(ctx, "docker", "images", "-q", "--filter", "label="+label)
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
	seen := map[string]bool{} // docker images -q repeats an ID per tag
	for _, line := range lines {
		if id := strings.TrimSpace(line); id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// RequireServicesWereRunning fails the test if the cleanup report shows
// that the specified services were not running. Callers specify which
// services they expect — tests using fakes don't call this at all.
//
// Valid service names: "firewall", "controlplane".
func (h *Harness) RequireServicesWereRunning(t *testing.T, services ...string) {
	t.Helper()
	if h.Cleanup == nil {
		t.Fatal("RequireServicesWereRunning called but cleanup has not run yet")
	}
	for _, svc := range services {
		switch svc {
		case "firewall":
			if h.Cleanup.FirewallContainers == 0 {
				t.Fatal(
					"firewall stack was not running during this test (no firewall containers found at cleanup) — test results are invalid",
				)
			}
		case "controlplane":
			if h.Cleanup.CPContainers == 0 {
				t.Fatal(
					"control plane was not running during this test (no CP container found at cleanup) — test results are invalid",
				)
			}
		default:
			t.Fatalf("RequireServicesWereRunning: unknown service %q", svc)
		}
	}
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

// ExecInContainer runs a command inside an existing container as the
// unprivileged container user (consts.ContainerUser).
func (h *Harness) ExecInContainer(agent string, cmd ...string) *RunResult {
	h.T.Helper()
	base := []string{"container", "exec", "--user", consts.ContainerUser, "--agent", agent}
	args := make([]string, 0, len(base)+len(cmd))
	args = append(args, base...)
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
