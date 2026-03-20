package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func TestWorkdirOverride(t *testing.T) {
	h := &harness.Harness{
		T: t,
		Opts: &harness.FactoryOptions{
			Config:         config.NewConfig,
			Client:         docker.NewClient,
			ProjectManager: project.NewProjectManager,
		},
	}
	h.NewIsolatedFS(nil)

	// Initialize project via CLI.
	initRes := h.Run("project", "init", "--yes")
	require.NoError(t, initRes.Err, "init failed\nstdout: %s\nstderr: %s",
		initRes.Stdout, initRes.Stderr)

	// Build the image.
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		buildRes.Stdout, buildRes.Stderr)

	// Run with --workdir to override the container's working directory.
	runRes := h.Run("container", "run", "--detach", "--agent", "dev", "--workdir", "/tmp", "@")
	require.NoError(t, runRes.Err, "run failed\nstdout: %s\nstderr: %s",
		runRes.Stdout, runRes.Stderr)

	// Inspect the container and verify WorkingDir.
	inspectRes := h.Run("container", "inspect", "--agent", "dev", "--format", "{{.Config.WorkingDir}}")
	require.NoError(t, inspectRes.Err, "inspect failed\nstdout: %s\nstderr: %s",
		inspectRes.Stdout, inspectRes.Stderr)
	assert.Equal(t, "/tmp\n", inspectRes.Stdout)
}
