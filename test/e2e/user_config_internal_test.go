package e2e

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/consts"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

// TestUserLevelConfig_E2E covers a project directory without its own config:
// commands refuse to run until the directory is registered, registration
// needs the user-level clawker.yaml, and the build produces the shared
// global image instead of a per-project one.
func TestUserLevelConfig_E2E(t *testing.T) {
	opts := new(harness.FactoryOptions)
	opts.Config = config.NewConfig
	opts.Client = docker.NewClient
	opts.ProjectManager = project.NewProjectManager
	h := new(harness.Harness)
	h.T = t
	h.Opts = opts
	h.NewIsolatedFS(nil)

	// Not registered: the project gate refuses the command.
	res := h.Run("container", "list")
	require.Error(t, res.Err, "container list must fail outside a registered project")
	require.Contains(t, res.Stderr, "clawker project register")

	// No project file and no user-level file: registration refuses.
	res = h.Run("project", "register", "--yes")
	require.Error(t, res.Err, "register must fail without any config file")

	userPath, err := consts.UserProjectConfigFilePath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(userPath, []byte("{}\n"), 0o644))

	res = h.Run("project", "register", "--yes")
	require.NoError(t, res.Err, "register failed\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)
	require.Contains(t, res.Stderr, userPath, "register names the user-level config it applies")

	res = h.Run("build", "--progress=none")
	require.NoError(t, res.Err, "build failed\nstdout: %s\nstderr: %s", res.Stdout, res.Stderr)

	ctx := context.Background()
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	dc, err := docker.NewClient(ctx, cfg, logger.Nop())
	require.NoError(t, err)
	t.Cleanup(func() {
		if closeErr := dc.Close(); closeErr != nil {
			t.Logf("closing docker client: %v", closeErr)
		}
	})

	_, err = dc.ImageInspect(ctx, docker.BaseImageTag(""))
	require.NoError(t, err, "global base image must exist")
	_, err = dc.ImageInspect(ctx, docker.DefaultAliasImageTag(""))
	require.NoError(t, err, "global :default image must exist")
	_, err = dc.ImageInspect(ctx, docker.DefaultAliasImageTag("testproject"))
	require.Error(t, err, "no per-project image for a global-scope project")
}
