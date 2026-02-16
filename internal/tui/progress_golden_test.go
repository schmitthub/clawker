package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams/iostreamstest"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/schmitthub/clawker/test/harness/golden"
	"github.com/stretchr/testify/require"
)

// TestProgressPlain_Golden runs each recorded scenario through RunProgress in
// plain mode and compares stderr output against golden files.
//
// Generate/update golden files with:
//
//	GOLDEN_UPDATE=1 go test ./internal/tui/... -run TestProgressPlain_Golden -v
func TestProgressPlain_Golden(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			tio := iostreamstest.New()
			ch := make(chan ProgressStep, 64)

			go func() {
				for _, e := range scenario.Events {
					ch <- ProgressStep{
						ID:      e.StepID,
						Name:    e.StepName,
						Status:  progressStatus(e.Status),
						Cached:  e.Cached,
						Error:   e.Error,
						LogLine: e.LogLine,
					}
				}
				close(ch)
			}()

			cfg := goldenDisplayConfig()
			result := RunProgress(tio.IOStreams, "plain", cfg, ch)
			require.NoError(t, result.Err)

			output := tio.ErrBuf.String()
			golden.CompareGoldenString(t, scenario.Name, output)
		})
	}
}

// goldenDisplayConfig returns a deterministic ProgressDisplayConfig for golden tests.
// Uses fixed duration formatting to avoid timing variance.
func goldenDisplayConfig() ProgressDisplayConfig {
	return ProgressDisplayConfig{
		Title:          "Building test-project",
		Subtitle:       "test-project:latest",
		CompletionVerb: "Built",
		IsInternal: func(name string) bool {
			return strings.HasPrefix(name, "[internal]")
		},
		CleanName: whail.CleanStepName,
		ParseGroup: func(name string) string {
			return whail.ParseBuildStage(name)
		},
		FormatDuration: func(_ time.Duration) string {
			return "0.0s"
		},
	}
}

// progressStatus converts whail.BuildStepStatus to tui.ProgressStepStatus.
// Mirrors the conversion in internal/cmd/image/build/build.go.
func progressStatus(s whail.BuildStepStatus) ProgressStepStatus {
	switch s {
	case whail.BuildStepPending:
		return StepPending
	case whail.BuildStepRunning:
		return StepRunning
	case whail.BuildStepComplete:
		return StepComplete
	case whail.BuildStepCached:
		return StepCached
	case whail.BuildStepError:
		return StepError
	default:
		return StepPending
	}
}

