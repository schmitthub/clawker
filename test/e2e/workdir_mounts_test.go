package e2e

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func TestWorkdirOverride(t *testing.T) {
	// `container run` boots the CP and dispatches FirewallInit through the
	// admin client (firewall.enable defaults true) — real CP + admin
	// wiring is required, same shape as the firewall E2E tests.
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:              config.NewConfig,
			Client:              docker.NewClient,
			ProjectManager:      project.NewProjectManager,
			UseRealControlPlane: true,
			UseRealAdminClient:  true,
		},
	}
	h.NewIsolatedFS(nil)
	harness.EnsureNoControlPlane(t, 30*time.Second)

	// Initialize project via CLI.
	initRes := h.Run("project", "init", "--yes")
	require.NoError(t, initRes.Err, "init failed\nstdout: %s\nstderr: %s",
		initRes.Stdout, initRes.Stderr)

	// Build the image (suppress progress output for clean test logs).
	buildRes := h.Run("build", "--progress=none")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	// Run with --workdir to override the container's working directory.
	runRes := h.Run("container", "run", "--detach", "--agent", "dev", "--workdir", "/tmp", "@")
	require.NoError(t, runRes.Err, "run failed\nstdout: %s\nstderr: %s",
		runRes.Stdout, runRes.Stderr)
	t.Cleanup(func() { h.Run("container", "stop", "--agent", "dev") })

	// Inspect the container and verify WorkingDir.
	inspectRes := h.Run("container", "inspect", "--agent", "dev", "--format", "{{.Config.WorkingDir}}")
	require.NoError(t, inspectRes.Err, "inspect failed\nstdout: %s\nstderr: %s",
		inspectRes.Stdout, inspectRes.Stderr)
	assert.Equal(t, "/tmp", strings.TrimSpace(inspectRes.Stdout))
}
