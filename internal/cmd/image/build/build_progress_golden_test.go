package build

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	configmocks "github.com/schmitthub/clawker/internal/config/mocks"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/internal/project"
	projectmocks "github.com/schmitthub/clawker/internal/project/mocks"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/schmitthub/clawker/test/harness/golden"
	"github.com/stretchr/testify/require"
)

// durationRE matches duration patterns like "0.0s", "1.2s", "1m 12s" in build output.
var durationRE = regexp.MustCompile(`\d+\.?\d*s|\d+m \d+\.?\d*s|\d+h \d+m`)

// TestBuildProgress_Golden exercises the full build pipeline for each scenario
// and compares stderr output against golden files. Durations are scrubbed to
// "(0.0s)" for deterministic comparison since buildRun uses whail.FormatBuildDuration.
//
// Generate/update golden files with:
//
//	GOLDEN_UPDATE=1 go test ./internal/cmd/image/build/... -run TestBuildProgress_Golden -v
func TestBuildProgress_Golden(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			t.Setenv("DOCKER_BUILDKIT", "1")

			testCfg := configmocks.NewFromString(`
build: { image: "node:20-slim" }
workspace: { default_mode: "bind" }
security: { firewall: { enable: false } }
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
			fake := dockertest.NewFakeClient(testCfg)
			fake.SetupBuildKitWithProgress(scenario.Events)

			// Wire a ProjectManager that returns "test-project" so buildRun
			// resolves project name via ProjectManager.CurrentProject(ctx).
			mockPM := projectmocks.NewMockProjectManager()
			mockPM.CurrentProjectFunc = func(_ context.Context) (project.Project, error) {
				return projectmocks.NewMockProject("test-project", "/fake/repo"), nil
			}

			tio := iostreamstest.New()
			f := &cmdutil.Factory{
				IOStreams: tio.IOStreams,
				TUI:       tui.NewTUI(tio.IOStreams),
				Client: func(_ context.Context) (*docker.Client, error) {
					return fake.Client, nil
				},
				Config: func() (config.Config, error) {
					return testCfg, nil
				},
				ProjectManager: func() (project.ProjectManager, error) {
					return mockPM, nil
				},
			}

			cmd := NewCmdBuild(f, nil)
			cmd.SetArgs([]string{"--progress", "plain"})
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(tio.OutBuf)
			cmd.SetErr(tio.ErrBuf)

			err := cmd.Execute()
			require.NoError(t, err)

			// Scrub durations for deterministic golden comparison.
			output := scrubDurations(tio.ErrBuf.String())
			golden.CompareGoldenString(t, scenario.Name, output)
		})
	}
}

// scrubDurations replaces all duration patterns with "0.0s" for deterministic output.
func scrubDurations(s string) string {
	return durationRE.ReplaceAllString(s, "0.0s")
}
