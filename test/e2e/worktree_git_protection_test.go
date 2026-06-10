package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/controlplane/cpboot"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/testenv"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// gitInDir runs the host git binary in dir as test fixture setup (creating
// the repo a worktree container hangs off). Clawker behavior under test is
// still exercised exclusively through h.Run.
func gitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}

// TestWorktreeGitProtection_E2E pins the worktree container .git contract:
// the main repo's .git is mounted RW at its host absolute path so worktree
// git ops work, but .git/hooks and .git/config are masked read-only (both
// are host-code-execution vectors — a hook or core.hooksPath/fsmonitor
// planted from the container would run on the host's next git op in the
// main checkout), and GOFLAGS=-buildvcs=false is set (Go's VCS walk skips
// the worktree's .git file, lands on the mounted main .git, and fails or
// stamps the wrong revision).
func TestWorktreeGitProtection_E2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary required for worktree fixture setup")
	}

	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
			ControlPlane: func(cfg config.Config, log *logger.Logger) cpboot.Manager {
				return cpboot.NewManager(
					func(ctx context.Context) (*docker.Client, error) {
						return docker.NewClient(ctx, cfg, log)
					},
					func() (config.Config, error) { return cfg, nil },
					func() (*logger.Logger, error) { return log, nil },
				)
			},
			UseRealAdminClient: true,
		},
	}
	setup := h.NewIsolatedFS(&harness.FSOptions{ProjectDir: "wt-protect"})

	// The project root must be a real git repo with a commit for worktree
	// creation to have something to branch from.
	gitInDir(t, setup.ProjectDir, "init", "-b", "main")
	gitInDir(t, setup.ProjectDir, "config", "user.email", "e2e@clawker.test")
	gitInDir(t, setup.ProjectDir, "config", "user.name", "clawker e2e")
	require.NoError(t, os.WriteFile(filepath.Join(setup.ProjectDir, "README.md"), []byte("worktree e2e\n"), 0o644))
	gitInDir(t, setup.ProjectDir, "add", "README.md")
	gitInDir(t, setup.ProjectDir, "commit", "-m", "init")

	initRes := h.Run("project", "init", "wt-protect", "--yes", "--preset", "Bare")
	require.NoError(t, initRes.Err, "init failed\nstdout: %s\nstderr: %s",
		initRes.Stdout, initRes.Stderr)

	// use_host_auth: false because the test env has no Claude credentials.
	setup.WriteYAML(t, testenv.ProjectConfigLocal, setup.ProjectDir, `
agent:
  claude_code:
    use_host_auth: false
`)

	buildRes := h.Run("build", "--progress=none")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	runRes := h.Run("container", "run", "--detach", "--agent", "wtprobe",
		"--worktree", "e2e/probe", "@", "sleep", "infinity")
	require.NoError(t, runRes.Err, "container run --worktree failed\nstdout: %s\nstderr: %s",
		runRes.Stdout, runRes.Stderr)
	t.Cleanup(func() {
		if res := h.Run("container", "stop", "--agent", "wtprobe"); res.Err != nil {
			t.Logf("cleanup: container stop failed: %v\nstdout: %s\nstderr: %s",
				res.Err, res.Stdout, res.Stderr)
		}
	})

	// Go VCS stamping is disabled by default; user env can override.
	goflagsRes := h.ExecInContainer("wtprobe", "sh", "-c", `printf %s "$GOFLAGS"`)
	require.NoError(t, goflagsRes.Err, "GOFLAGS probe failed\nstdout: %s\nstderr: %s",
		goflagsRes.Stdout, goflagsRes.Stderr)
	assert.Contains(t, goflagsRes.Stdout, "-buildvcs=false",
		"worktree containers must default GOFLAGS=-buildvcs=false")

	// Everyday worktree git ops must work against the RW .git mount.
	gitOpsRes := h.ExecInContainer("wtprobe", "sh", "-c",
		"git status --porcelain && git -c user.email=e2e@clawker.test -c user.name=e2e commit --allow-empty -m e2e-probe")
	require.NoError(t, gitOpsRes.Err, "worktree git status/commit must work\nstdout: %s\nstderr: %s",
		gitOpsRes.Stdout, gitOpsRes.Stderr)

	// Host-exec vector 1: planting a hook in the main repo's .git must fail.
	hookRes := h.ExecInContainer("wtprobe", "sh", "-c",
		`touch "$(git rev-parse --git-common-dir)/hooks/e2e-planted-hook"`)
	assert.Error(t, hookRes.Err,
		"writing to main .git/hooks must fail (read-only mask)\nstdout: %s\nstderr: %s",
		hookRes.Stdout, hookRes.Stderr)

	// Host-exec vector 2: writing the shared .git/config must fail. From a
	// worktree, `git config --local` targets the MAIN repo's config file.
	configRes := h.ExecInContainer("wtprobe", "sh", "-c", "git config --local clawker.e2eprobe 1")
	assert.Error(t, configRes.Err,
		"git config --local must fail (main .git/config is read-only)\nstdout: %s\nstderr: %s",
		configRes.Stdout, configRes.Stderr)

	// Nothing leaked onto the host side of the mounts.
	hostHook := filepath.Join(setup.ProjectDir, ".git", "hooks", "e2e-planted-hook")
	_, statErr := os.Stat(hostHook)
	assert.True(t, os.IsNotExist(statErr), "planted hook must not exist on host at %s", hostHook)
	hostConfig, err := os.ReadFile(filepath.Join(setup.ProjectDir, ".git", "config"))
	require.NoError(t, err)
	assert.NotContains(t, string(hostConfig), "e2eprobe", "probe key must not land in host .git/config")
}
