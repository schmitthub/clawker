package e2e

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

// TestBindMountUID_E2E pins the host-UID-derivation contract end-to-end:
// CLI process os.Getuid() → bundler bakes image's claude user at that UID
// → CLI emits CLAWKER_HOST_UID on the CP container → CP's userStage
// drops to consts.HostUID() → a file written from inside the container
// to ~/.claude/projects lands with the host invoker's UID on the host
// bind mount.
//
// Skipped on darwin because Docker Desktop's virtiofs translates ownership
// at the bind boundary — host always sees its own UID regardless of what
// the container's claude user is baked at, so the test would false-pass
// the very regression it exists to catch.
func TestBindMountUID_E2E(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("UID-mismatch regression is Linux-specific; macOS virtiofs translates ownership")
	}
	if os.Getuid() <= 0 {
		t.Skip("test invoker UID must be > 0 (test runs as the host user being plumbed through to userStage)")
	}

	// Isolated CLAUDE_CONFIG_DIR so the bind mount points at a path this
	// test owns end-to-end. Pre-create projects/ so SetupMounts wires the
	// bind (the missing-projects path is a silent skip — would short-circuit
	// the contract this test is asserting).
	claudeDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(claudeDir, "projects"), 0o755))
	t.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

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
	setup := h.NewIsolatedFS(&harness.FSOptions{ProjectDir: "uid-bind"})

	initRes := h.Run("project", "init", "uid-bind", "--yes", "--preset", "Bare")
	require.NoError(t, initRes.Err, "init failed\nstdout: %s\nstderr: %s",
		initRes.Stdout, initRes.Stderr)

	// use_host_auth: false because the test env has no Claude credentials
	// to forward; mount_projects: true is the default and the very thing
	// we're asserting on.
	setup.WriteYAML(t, testenv.ProjectConfigLocal, setup.ProjectDir, `
agent:
  claude_code:
    use_host_auth: false
`)

	buildRes := h.Run("build", "--progress=none")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	runRes := h.Run("container", "run", "--detach", "--agent", "uidtest", "@", "sleep", "infinity")
	require.NoError(t, runRes.Err, "container run failed\nstdout: %s\nstderr: %s",
		runRes.Stdout, runRes.Stderr)
	t.Cleanup(func() {
		if res := h.Run("container", "stop", "--agent", "uidtest"); res.Err != nil {
			t.Logf("cleanup: container stop failed: %v\nstdout: %s\nstderr: %s",
				res.Err, res.Stdout, res.Stderr)
		}
	})

	writeRes := h.ExecInContainer("uidtest", "sh", "-c",
		"set -e; printf %s probe > ~/.claude/projects/probe.txt")
	require.NoError(t, writeRes.Err, "probe write failed\nstdout: %s\nstderr: %s",
		writeRes.Stdout, writeRes.Stderr)

	probePath := filepath.Join(claudeDir, "projects", "probe.txt")
	info, err := os.Stat(probePath)
	require.NoError(t, err, "probe file must land on the host bind mount at %s", probePath)
	st, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok, "stat sys must be *syscall.Stat_t on linux")
	assert.Equal(t, uint32(os.Getuid()), st.Uid,
		"bind-mount write must land at the host invoker's UID (got uid=%d, want %d) — host-UID-derivation contract broke somewhere between CLI bundler, CP env, and userStage",
		st.Uid, os.Getuid())
}
