package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
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
			tio := iostreams.NewTestIOStreams()
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
			compareGolden(t, scenario.Name, output)
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

// ---------------------------------------------------------------------------
// Inline golden file helper — avoids importing test/harness and its heavy
// transitive dependencies (Docker SDK, whail, config, yaml).
// ---------------------------------------------------------------------------

func compareGolden(t *testing.T, name, got string) {
	t.Helper()

	testDir := sanitizeGoldenName(t.Name())
	path := filepath.Join("testdata", testDir, name+".golden")
	updateMode := os.Getenv("GOLDEN_UPDATE") == "1"

	want, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if updateMode {
			writeGoldenFile(t, path, got)
			t.Logf("Created golden file: %s", path)
			return
		}
		t.Fatalf("golden file not found: %s\n\nTo create, run:\n  GOLDEN_UPDATE=1 go test ./internal/tui/... -run %s", path, t.Name())
	}
	require.NoError(t, err)

	if got != string(want) {
		if updateMode {
			writeGoldenFile(t, path, got)
			t.Logf("Updated golden file: %s", path)
			return
		}
		t.Errorf("output does not match golden file: %s\n\nTo update:\n  GOLDEN_UPDATE=1 go test ./internal/tui/... -run %s\n\nGot:\n%s\nWant:\n%s",
			path, t.Name(), got, string(want))
	}
}

func writeGoldenFile(t *testing.T, path, content string) {
	t.Helper()
	dir := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func sanitizeGoldenName(name string) string {
	result := make([]byte, len(name))
	for i := range len(name) {
		c := name[i]
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			result[i] = '_'
		default:
			result[i] = c
		}
	}
	return string(result)
}

