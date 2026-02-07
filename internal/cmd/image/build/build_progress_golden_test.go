package build

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/config"
	"github.com/schmitthub/clawker/internal/docker"
	"github.com/schmitthub/clawker/internal/docker/dockertest"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/schmitthub/clawker/test/harness"
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

			cmd := NewCmdBuild(f, nil)
			cmd.SetArgs([]string{"--progress", "plain"})
			cmd.SetIn(&bytes.Buffer{})
			cmd.SetOut(tio.OutBuf)
			cmd.SetErr(tio.ErrBuf)

			err := cmd.Execute()
			require.NoError(t, err)

			// Scrub durations for deterministic golden comparison.
			output := scrubDurations(tio.ErrBuf.String())
			harness.CompareGoldenString(t, scenario.Name, output)
		})
	}
}

// scrubDurations replaces all duration patterns with "0.0s" for deterministic output.
func scrubDurations(s string) string {
	return durationRE.ReplaceAllString(s, "0.0s")
}
