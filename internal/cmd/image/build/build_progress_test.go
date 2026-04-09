package build

import (
	"context"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/mock"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
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
	for _, scenario := range whailtest.AllBuildScenarios() { // TODO: This should not be importing whail test doubles its suposed to test from the docker packages
		t.Run(scenario.Name, func(t *testing.T) {
			t.Setenv("DOCKER_BUILDKIT", "1")

			testCfg := configmocks.NewFromString(`
version: "1"
name: test-project
build: { image: "node:20-slim" }
workspace: { default_mode: "bind" }
security: {}
`, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`)
			fake := mock.NewFakeClient(testCfg)
			fake.SetupBuildKitWithProgress(scenario.Events)

			tio, in, out, errOut := iostreams.Test()
			f := &cmdutil.Factory{
				IOStreams: tio,
				TUI:       tui.NewTUI(tio),
				Client: func(_ context.Context) (*docker.Client, error) {
					return fake.Client, nil
				},
				Config: func() (config.Config, error) {
					return testCfg, nil
				},
				Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
			}

			cmd := NewCmdBuild(f, nil) // nil runF → real buildRun
			cmd.SetArgs([]string{"--progress", "plain"})
			cmd.SetIn(in)
			cmd.SetOut(out)
			cmd.SetErr(errOut)

			err := cmd.Execute()

			// The build itself succeeds (fake returns nil), but the progress
			// display should still render the error step.
			require.NoError(t, err)

			output := errOut.String()

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

	testCfg := configmocks.NewFromString(`
version: "1"
name: test-project
build: { image: "node:20-slim" }
workspace: { default_mode: "bind" }
security: {}
`, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`)
	fake := mock.NewFakeClient(testCfg)
	fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio, in, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return testCfg, nil
		},
		Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--progress", "plain"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	output := errOut.String()

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

	testCfg := configmocks.NewFromString(`
version: "1"
name: test-project
build: { image: "node:20-slim" }
workspace: { default_mode: "bind" }
security: {}
`, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`)
	fake := mock.NewFakeClient(testCfg)
	fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio, in, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return testCfg, nil
		},
		Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--quiet"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	// Progress output should not contain step names.
	output := errOut.String()
	assert.NotContains(t, output, "FROM node:20-slim")
	assert.NotContains(t, output, "RUN apt-get update")
}

// TestBuildProgress_CaptureCallCount verifies the fake builder is invoked exactly once.
func TestBuildProgress_CaptureCallCount(t *testing.T) {
	t.Setenv("DOCKER_BUILDKIT", "1")

	testCfg := configmocks.NewFromString(`
version: "1"
name: test-project
build: { image: "node:20-slim" }
workspace: { default_mode: "bind" }
security: {}
`, `
monitoring:
  otel_collector_port: 4318
  otel_grpc_port: 4317
  telemetry:
    metrics_path: "/v1/metrics"
    logs_path: "/v1/logs"
    log_tool_details: true
    log_user_prompts: true
    include_account_uuid: true
    include_session_id: true
`)
	fake := mock.NewFakeClient(testCfg)
	capture := fake.SetupBuildKitWithProgress(whailtest.SimpleBuildEvents())

	tio, in, out, errOut := iostreams.Test()
	f := &cmdutil.Factory{
		IOStreams: tio,
		TUI:       tui.NewTUI(tio),
		Client: func(_ context.Context) (*docker.Client, error) {
			return fake.Client, nil
		},
		Config: func() (config.Config, error) {
			return testCfg, nil
		},
		Logger: func() (*logger.Logger, error) { return logger.Nop(), nil },
	}

	cmd := NewCmdBuild(f, nil)
	cmd.SetArgs([]string{"--progress", "plain"})
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	require.NoError(t, err)

	assert.Equal(t, 1, capture.CallCount, "BuildKit builder should be called exactly once")
	assert.NotEmpty(t, capture.Opts.Tags, "build should pass tags")
	assert.NotEmpty(t, capture.Opts.ContextDir, "build should pass context dir")
}
