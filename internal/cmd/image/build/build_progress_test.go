package build

import (
	"bytes"
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildProgress_Pipeline exercises the full progress pipeline:
// fake BuildKit events → OnProgress callback → channel → RunProgress (plain mode) → stderr output.
//
// Each scenario uses whailtest pre-built event sequences that mirror real BuildKit output patterns.
func TestBuildProgress_Pipeline(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			t.Setenv("DOCKER_BUILDKIT", "1")

			fake := dockertest.NewFakeClient()
			fake.SetupBuildKitWithProgress(scenario.Events)

			tio := iostreams.NewTestIOStreams()
			f := &cmdutil.Factory{
				IOStreams: tio.IOStreams,
				TUI:      tui.NewTUI(tio.IOStreams),
				Client: func(_ context.Context) (*docker.Client, error) {
					return fake.Client, nil
				},
				Config: func() *config.Config {
					return config.NewConfigForTest(testBuildConfig(t), config.DefaultSettings())
				},
			}

			cmd := NewCmdBuild(f, nil) // nil runF → real buildRun
			cmd.SetArgs([]string{"--progress", "plain"})
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(tio.OutBuf)
			cmd.SetErr(tio.ErrBuf)

			err := cmd.Execute()

			// Error scenarios produce a build error (the last step has StepError).
			if scenario.Name == "error" {
				// The build itself succeeds (fake returns nil), but the progress
				// display should still render the error step. The build command
				// succeeds because the fake builder returns nil error.
				require.NoError(t, err)
			} else {
				require.NoError(t, err)
			}

			output := tio.ErrBuf.String()

			// Verify all visible step names appear in plain output.
			for _, event := range scenario.Events {
				if event.StepName == "" || whail.IsInternalStep(event.StepName) {
					continue
				}
				// Only check status events (not log lines) for step name presence.
				if event.Status == 0 && event.LogLine != "" {
					continue
				}
				assert.Contains(t, output, event.StepName,
					"plain output should contain visible step name")
			}
		})
	}
}

// TestBuildProgress_SimplePipeline validates specific output content for the simple scenario.
func TestBuildProgress_SimplePipeline(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "1")

	fake := dockertest.NewFakeClient()
	fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(testBuildConfig(t), config.DefaultSettings())
		},
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--progress", "plain"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	output := tio.ErrBuf.String()

	// Visible steps should appear.
	assert.Contains(t, output, "FROM node:20-slim")
	assert.Contains(t, output, "RUN apt-get update")
	assert.Contains(t, output, "COPY . /app")

	// Internal steps should NOT appear.
	assert.NotContains(t, output, "[internal]")
}

// TestBuildProgress_Suppressed verifies that --quiet suppresses progress output.
func TestBuildProgress_Suppressed(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "1")

	fake := dockertest.NewFakeClient()
	fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(testBuildConfig(t), config.DefaultSettings())
		},
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--quiet"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	// Progress output should not contain step names.
	output := tio.ErrBuf.String()
	assert.NotContains(t, output, "FROM node:20-slim")
	assert.NotContains(t, output, "RUN apt-get update")
}

// TestBuildProgress_CaptureCallCount verifies the fake builder is invoked exactly once.
func TestBuildProgress_CaptureCallCount(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "1")

	fake := dockertest.NewFakeClient()
	capture := fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio := iostreams.NewTestIOStreams()
	f := &cmdutil.Factory{
		IOStreams: tio.IOStreams,
		TUI:      tui.NewTUI(tio.IOStreams),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() *config.Config {
			return config.NewConfigForTest(testBuildConfig(t), config.DefaultSettings())
		},
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--progress", "plain"})
	cmd.SetIn(&bytes.Buffer{})
	cmd.SetOut(tio.OutBuf)
	cmd.SetErr(tio.ErrBuf)

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, 1, capture.CallCount, "BuildKit builder should be called exactly once")
	assert.NotEmpty(t, capture.Opts.Tags, "build should pass tags")
	assert.NotEmpty(t, capture.Opts.ContextDir, "build should pass context dir")
}

// testBuildConfig returns a minimal config.Project suitable for the build pipeline test.
// Uses Build.Image so the bundler can generate a Dockerfile without external dependencies.
func testBuildConfig(t *testing.T) *config.Project {
	t.Helper()
	return &config.Project{
		Version: "1",
		Project: "test-project",
		Build: config.BuildConfig{
			Image: "node:20-slim",
		},
		Workspace: config.WorkspaceConfig{
			RemotePath:  "/workspace",
			DefaultMode: "bind",
		},
		Security: config.SecurityConfig{
			Firewall: &config.FirewallConfig{
				Enable: false,
			},
		},
	}
}
