package overseer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestInit_WithStep covers the StepIndex clamp and the (name, index)
// pair contract subscribers rely on.
func TestInit_WithStep(t *testing.T) {
	cases := []struct {
		name        string
		stepCount   int
		idx         int
		wantIdx     int
		description string
	}{
		{"negative_clamps_to_zero", 5, -1, 0, "stepIndex < 0 → 0"},
		{"out_of_range_clamps_to_max", 5, 99, 4, "stepIndex >= stepCount → stepCount-1"},
		{"zero_count_no_clamp", 0, 3, 3, "stepCount==0 disables clamp (unknown bound)"},
		{"in_range_unchanged", 5, 3, 3, "valid stepIndex stays put"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i := InitRunning(tc.stepCount, time.Now()).WithStep("git", tc.idx)
			assert.Equal(t, tc.wantIdx, i.StepIndex(), tc.description)
			assert.Equal(t, "git", i.StepName(),
				"StepName must always be the caller-supplied label — clamp affects only StepIndex")
		})
	}
}

// TestInitRunning_NegativeStepCountClamped pins the negative-count
// clamp at construction so a stale-event projection can't display
// "step N of -3" to subscribers.
func TestInitRunning_NegativeStepCountClamped(t *testing.T) {
	i := InitRunning(-3, time.Now())
	assert.Equal(t, 0, i.StepCount())
}

// TestInit_Complete_ClearsLastError pins the success-snapshot
// contract: once Status is Completed, LastError must be empty.
// Subscribers switching on Status alone would otherwise display a
// stale failure message on a successful init.
func TestInit_Complete_ClearsLastError(t *testing.T) {
	start := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	i := InitRunning(3, start).WithStepError("stale error from a previous step").Complete(end)
	assert.Equal(t, InitStatusCompleted, i.Status())
	assert.Equal(t, end, i.CompletedAt())
	assert.Empty(t, i.LastError())
}

// TestInit_TerminalAtFloors verifies both Complete and Fail floor
// CompletedAt at StartedAt — the (CompletedAt < StartedAt) projection
// is unrepresentable regardless of which terminal transition fires.
func TestInit_TerminalAtFloors(t *testing.T) {
	start := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	earlier := start.Add(-time.Hour)
	t.Run("complete", func(t *testing.T) {
		i := InitRunning(3, start).Complete(earlier)
		assert.Equal(t, start, i.CompletedAt())
	})
	t.Run("fail", func(t *testing.T) {
		i := InitRunning(3, start).Fail(earlier, "boom")
		assert.Equal(t, start, i.CompletedAt())
	})
}

// TestInit_Fail pins the failed-state shape: Status, CompletedAt, and
// LastError all transition together. The combined assertion is the
// invariant — any one alone would let a regression silently break
// subscribers that branch on a different field.
func TestInit_Fail(t *testing.T) {
	start := time.Date(2026, 5, 8, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Second)
	i := InitRunning(3, start).Fail(end, "transport error")
	assert.Equal(t, InitStatusFailed, i.Status())
	assert.Equal(t, end, i.CompletedAt())
	assert.Equal(t, "transport error", i.LastError())
}
