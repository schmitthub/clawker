package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/git"
	"github.com/schmitthub/clawker/internal/project"
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

// TestWorktreeGitAccess_E2E checks Git writes through the shared mount.
func TestWorktreeGitAccess_E2E(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary required for worktree fixture setup")
	}

	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:              config.NewConfig,
			Client:              docker.NewClient,
			ProjectManager:      project.NewProjectManager,
			GitManager:          git.NewGitManager,
			HostProxy:           nil,
			SocketBridge:        nil,
			UseRealControlPlane: true,
			UseRealAdminClient:  true,
		},
		Cleanup: nil,
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
	gitOpsRes := h.ExecInContainer(
		"wtprobe",
		"sh",
		"-c",
		"git status --porcelain && git -c user.email=e2e@clawker.test -c user.name=e2e commit --allow-empty -m e2e-probe",
	)
	require.NoError(t, gitOpsRes.Err, "worktree git status/commit must work\nstdout: %s\nstderr: %s",
		gitOpsRes.Stdout, gitOpsRes.Stderr)

	// Hook files and local Git config must remain writable.
	hookRes := h.ExecInContainer("wtprobe", "sh", "-c",
		`touch "$(git rev-parse --git-common-dir)/hooks/e2e-hook"`)
	require.NoError(t, hookRes.Err, "write hook: %s", hookRes.Stderr)

	configRes := h.ExecInContainer("wtprobe", "git", "config", "--local", "clawker.e2eprobe", "1")
	require.NoError(t, configRes.Err, "write local Git config: %s", configRes.Stderr)

	// Both writes must reach the shared repository.
	require.FileExists(t, filepath.Join(setup.ProjectDir, ".git", "hooks", "e2e-hook"))
	hostConfig, err := os.ReadFile(filepath.Join(setup.ProjectDir, ".git", "config"))
	require.NoError(t, err)
	assert.Contains(t, string(hostConfig), "e2eprobe")
}
