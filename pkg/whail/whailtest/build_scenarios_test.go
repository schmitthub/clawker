package whailtest_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schmitthub/clawker/pkg/whail"
	"github.com/schmitthub/clawker/pkg/whail/whailtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStepDigest(t *testing.T) {
	d := whailtest.StepDigest(1)
	assert.Equal(t, "sha256:0000000000000000000000000000000000000000000000000000000000000001", d)
	assert.Len(t, d, 7+64) // "sha256:" + 64 hex chars
}

func TestStepDigest_Unique(t *testing.T) {
	seen := make(map[string]bool)
	for i := range 100 {
		d := whailtest.StepDigest(i)
		assert.False(t, seen[d], "duplicate digest for step %d", i)
		seen[d] = true
	}
}

func TestAllBuildScenarios_Complete(t *testing.T) {
	scenarios := whailtest.AllBuildScenarios()
	require.Len(t, scenarios, 7, "expected 7 scenarios")

	names := make(map[string]bool)
	for _, s := range scenarios {
		assert.NotEmpty(t, s.Name)
		assert.False(t, names[s.Name], "duplicate scenario name: %s", s.Name)
		names[s.Name] = true
		assert.NotEmpty(t, s.Events, "scenario %q has no events", s.Name)
	}
}

func TestAllBuildScenarios_ValidEvents(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			for i, e := range scenario.Events {
				assert.NotEmpty(t, e.StepID, "event %d has empty StepID", i)
				// Every event must have a StepName (used for display and filtering).
				assert.NotEmpty(t, e.StepName, "event %d has empty StepName", i)
			}
		})
	}
}

func TestAllBuildScenarios_NoConsecutiveDuplicateStatus(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			// Track last status per step ID.
			lastStatus := make(map[string]whail.BuildStepStatus)
			for i, e := range scenario.Events {
				if e.LogLine != "" && e.Status == whail.BuildStepPending {
					// Log-only events (status=0/pending) are fine to repeat.
					continue
				}
				prev, seen := lastStatus[e.StepID]
				if seen && e.Status == prev {
					t.Errorf("event %d: step %s has consecutive duplicate status %d",
						i, e.StepID, e.Status)
				}
				if e.Status != whail.BuildStepPending {
					lastStatus[e.StepID] = e.Status
				}
			}
		})
	}
}

func TestSimpleBuildEvents(t *testing.T) {
	events := whailtest.SimpleBuildEvents()
	visible, internal := countStepTypes(events)
	assert.Equal(t, 3, visible, "expected 3 visible steps")
	assert.Equal(t, 2, internal, "expected 2 internal steps")
}

func TestCachedBuildEvents(t *testing.T) {
	events := whailtest.CachedBuildEvents()
	visible, _ := countStepTypes(events)
	assert.Equal(t, 5, visible, "expected 5 visible steps")

	cached := 0
	seen := make(map[string]bool)
	for _, e := range events {
		if e.Cached && !seen[e.StepID] {
			cached++
			seen[e.StepID] = true
		}
	}
	assert.Equal(t, 4, cached, "expected 4 cached steps")
}

func TestMultiStageBuildEvents(t *testing.T) {
	events := whailtest.MultiStageBuildEvents()
	visible, _ := countStepTypes(events)
	assert.Equal(t, 8, visible, "expected 8 visible steps")

	stages := countStages(events)
	assert.Equal(t, 3, stages, "expected 3 stages")
}

func TestErrorBuildEvents(t *testing.T) {
	events := whailtest.ErrorBuildEvents()
	hasError := false
	for _, e := range events {
		if e.Status == whail.BuildStepError {
			hasError = true
			assert.NotEmpty(t, e.Error, "error step should have Error message")
		}
	}
	assert.True(t, hasError, "expected at least one error step")
}

func TestLargeLogOutputEvents(t *testing.T) {
	events := whailtest.LargeLogOutputEvents()
	logLines := 0
	for _, e := range events {
		if e.LogLine != "" {
			logLines++
		}
	}
	assert.Equal(t, 50, logLines, "expected 50 log lines")
}

func TestManyStepsBuildEvents(t *testing.T) {
	events := whailtest.ManyStepsBuildEvents()
	visible, _ := countStepTypes(events)
	assert.Equal(t, 10, visible, "expected 10 visible steps")
}

func TestInternalOnlyEvents(t *testing.T) {
	events := whailtest.InternalOnlyEvents()
	visible, internal := countStepTypes(events)
	assert.Equal(t, 0, visible, "expected 0 visible steps")
	assert.Equal(t, 3, internal, "expected 3 internal steps")
}

// countStepTypes counts unique visible and internal steps.
func countStepTypes(events []whail.BuildProgressEvent) (visible, internal int) {
	seen := make(map[string]bool)
	for _, e := range events {
		if seen[e.StepID] {
			continue
		}
		seen[e.StepID] = true
		if whail.IsInternalStep(e.StepName) {
			internal++
		} else {
			visible++
		}
	}
	return
}

// countStages counts unique stage names from visible steps.
func countStages(events []whail.BuildProgressEvent) int {
	stages := make(map[string]bool)
	for _, e := range events {
		if whail.IsInternalStep(e.StepName) {
			continue
		}
		stage := whail.ParseBuildStage(e.StepName)
		if stage != "" {
			stages[stage] = true
		}
	}
	return len(stages)
}

// ---------------------------------------------------------------------------
// Recorded JSON scenario seeding and validation
// ---------------------------------------------------------------------------

// scenarioDescriptions maps scenario names to their descriptions for JSON files.
var scenarioDescriptions = map[string]string{
	"simple":        "Basic 3-visible-step build: FROM + RUN + COPY",
	"cached":        "5-step build with 4 cached steps, typical incremental rebuild",
	"multi-stage":   "8-step build across 3 named stages (builder, assets, runtime)",
	"error":         "3-step build where the last step fails with npm error",
	"large-log":     "Single-step build emitting 50 log lines",
	"many-steps":    "10-step build exercising per-stage child window display",
	"internal-only": "3 internal steps with zero visible output",
}

// TestSeedRecordedScenarios generates JSON testdata files from Go scenarios.
// Run with GOLDEN_UPDATE=1 to create or update the files:
//
//	GOLDEN_UPDATE=1 go test ./pkg/whail/whailtest/... -run TestSeedRecordedScenarios -v
func TestSeedRecordedScenarios(t *testing.T) {
	if os.Getenv("GOLDEN_UPDATE") != "1" {
		t.Skip("set GOLDEN_UPDATE=1 to regenerate JSON testdata")
	}

	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			desc := scenarioDescriptions[scenario.Name]
			recorded := whailtest.RecordedScenarioFromEventsWithTiming(
				scenario.Name, desc, scenario.Events,
				10*time.Millisecond,  // internal steps
				50*time.Millisecond,  // running status
				20*time.Millisecond,  // log lines
				100*time.Millisecond, // complete/cached/error
			)

			path := filepath.Join("testdata", scenario.Name+".json")
			err := whailtest.SaveRecordedScenario(path, recorded)
			require.NoError(t, err)
			t.Logf("Wrote %s (%d events)", path, len(recorded.Events))
		})
	}
}

// TestRecordedScenarios_MatchGoScenarios validates that JSON testdata files
// contain the same events as the Go scenario functions. Catches drift between
// the two representations.
func TestRecordedScenarios_MatchGoScenarios(t *testing.T) {
	for _, scenario := range whailtest.AllBuildScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			path := filepath.Join("testdata", scenario.Name+".json")
			recorded, err := whailtest.LoadRecordedScenario(path)
			if os.IsNotExist(err) {
				t.Skipf("JSON file not found: %s (run GOLDEN_UPDATE=1 to generate)", path)
				return
			}
			require.NoError(t, err)

			flat := recorded.FlatEvents()
			require.Len(t, flat, len(scenario.Events),
				"event count mismatch for %s", scenario.Name)

			for i, want := range scenario.Events {
				got := flat[i]
				assert.Equal(t, want.StepID, got.StepID, "event %d StepID", i)
				assert.Equal(t, want.StepName, got.StepName, "event %d StepName", i)
				assert.Equal(t, want.Status, got.Status, "event %d Status", i)
				assert.Equal(t, want.LogLine, got.LogLine, "event %d LogLine", i)
				assert.Equal(t, want.Error, got.Error, "event %d Error", i)
				assert.Equal(t, want.Cached, got.Cached, "event %d Cached", i)
			}
		})
	}
}
