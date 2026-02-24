package container_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/project"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/test/harness"
)

func TestContainerRun_NoArgs(t *testing.T) {
	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio.IOStreams,
	}

	h := harness.New(t, f)
	res := h.Run("container", "run")

	require.Error(t, res.Err)
	assert.Contains(t, tio.ErrBuf.String()+res.Err.Error(), "requires at least 1 arg")
}

func TestContainerRun_Detach(t *testing.T) {
	requireDocker(t)

	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Client: func(ctx context.Context) (*docker.Client, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			c, err := docker.NewClient(ctx, cfg,
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
			return project.NewProjectManager(cfg, nil)
		},
	}

	h := harness.New(t, f)

	// Create config after harness sets env vars, set build image, persist to disk.
	cfg, err := config.NewConfig()
	require.NoError(t, err)
	cfg.ProjectStore().Set(func(p *config.Project) {
		p.Build.Image = "buildpack-deps:bookworm-scm"
	})
	h.WriteConfig(cfg)

	// Build the clawker image — BuildKit caches make this fast after first run.
	// All tests share "testrepo" project name so image name is deterministic.
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		tio.OutBuf.String(), tio.ErrBuf.String())
	tio.OutBuf.Reset()
	tio.ErrBuf.Reset()

	// Run a detached container using @ (resolves to the project's built image).
	res := h.Run("container", "run", "--detach", "--agent", "dev", "@")
	require.NoError(t, res.Err, "stdout: %s\nstderr: %s", tio.OutBuf.String(), tio.ErrBuf.String())
}

func TestContainerRun_WorkdirFlag(t *testing.T) {
	requireDocker(t)

	tio := iostreamstest.New()
	f := &cmdutil.Factory{
		Version:   "test",
		IOStreams: tio.IOStreams,
		TUI:       tui.NewTUI(tio.IOStreams),
		Config: func() (config.Config, error) {
			return config.NewConfig()
		},
		Client: func(ctx context.Context) (*docker.Client, error) {
			cfg, err := config.NewConfig()
			if err != nil {
				return nil, err
			}
			c, err := docker.NewClient(ctx, cfg,
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
			return project.NewProjectManager(cfg, nil)
		},
	}

	h := harness.New(t, f)

	cfg, err := config.NewConfig()
	require.NoError(t, err)
	cfg.ProjectStore().Set(func(p *config.Project) {
		p.Build.Image = "buildpack-deps:bookworm-scm"
	})
	h.WriteConfig(cfg)

	// Build the image (cached if TestContainerRun_Detach already ran).
	buildRes := h.Run("build")
	require.NoError(t, buildRes.Err, "build failed\nstdout: %s\nstderr: %s",
		tio.OutBuf.String(), tio.ErrBuf.String())
	tio.OutBuf.Reset()
	tio.ErrBuf.Reset()

	// Run with --workdir to override the container's working directory.
	res := h.Run("container", "run", "--detach", "--agent", "dev", "--workdir", "/tmp", "@")
	require.NoError(t, res.Err, "run failed\nstdout: %s\nstderr: %s",
		tio.OutBuf.String(), tio.ErrBuf.String())
	tio.OutBuf.Reset()
	tio.ErrBuf.Reset()

	// Inspect the container and verify WorkingDir via the production inspect command.
	inspectRes := h.Run("container", "inspect", "--agent", "dev", "--format", "{{.Config.WorkingDir}}")
	require.NoError(t, inspectRes.Err, "inspect failed\nstdout: %s\nstderr: %s",
		tio.OutBuf.String(), tio.ErrBuf.String())
	assert.Equal(t, "/tmp\n", tio.OutBuf.String())
}
