package e2e

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/test/e2e/harness"
)

func TestWorkdirOverride(t *testing.T) {
	tio, _, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio,
		Logger:    func() (*logger.Logger, error) { return logger.Nop(), nil },
		TUI:       tui.NewTUI(tio),
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Client: func(ctx context.Context) (*docker.Client, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			c, err := docker.NewClient(ctx, cfg, nil,
				docker.WithLabels(docker.TestLabelConfig(cfg, t.Name())))
			if err != nil {
				return nil, err
			}
			docker.WireBuildKit(c)
			return c, nil
		},
		ProjectManager: func() (project.ProjectManager, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			return project.NewProjectManager(cfg, logger.Nop(), nil)
		},
	}
	h := &harness.Harness{
		T:       t,
		Factory: f,
	}

	result := h.NewIsolatedFS(nil)

	// Register the project so @ resolves to the built image.
	pm, err := f.ProjectManager()
	require.NoError(t, err)
	_, err = pm.Register(context.Background(), "testproject", result.ProjectDir)
	require.NoError(t, err)

	cfg, err := f.Config()
	require.NoError(t, err)
	cfg.ProjectStore().Set(func(p *config.Project) {
		p.Build.Image = "buildpack-deps:bookworm-scm"
	})
	require.NoError(t, cfg.ProjectStore().Write())

	// Build the image.
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	out.Reset()
	errOut.Reset()

	// Run with --workdir to override the container's working directory.
	res := h.Run("container", "run", "--detach", "--agent", "dev", "--workdir", "/tmp", "@")
	require.NoError(t, res.Err, "run failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	out.Reset()
	errOut.Reset()

	// Inspect the container and verify WorkingDir.
	inspectRes := h.Run("container", "inspect", "--agent", "dev", "--format", "{{.Config.WorkingDir}}")
	require.NoError(t, inspectRes.Err, "inspect failed\nstdout: %s\nstderr: %s",
		out.String(), errOut.String())
	assert.Equal(t, "/tmp\n", out.String())
}
