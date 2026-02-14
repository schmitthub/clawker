package shared

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

func TestLoopDashEventKind_String(t *testing.T) {
	tests := []struct {
		kind LoopDashEventKind
		want string
	}{
		{LoopDashEventStart, "Start"},
		{LoopDashEventIterStart, "IterStart"},
		{LoopDashEventIterEnd, "IterEnd"},
		{LoopDashEventOutput, "Output"},
		{LoopDashEventRateLimit, "RateLimit"},
		{LoopDashEventComplete, "Complete"},
		{LoopDashEventKind(99), "Unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.kind.String())
		})
	}
}

func newTestRenderer() (*loopDashRenderer, *iostreams.IOStreams) {
	ios := iostreams.NewTestIOStreams()
	cfg := LoopDashboardConfig{
		AgentName: "loop-brave-turing",
		Project:   "myapp",
		MaxLoops:  50,
	}
	return newLoopDashRenderer(ios.IOStreams, cfg), ios.IOStreams
}

func TestLoopDashRenderer_ProcessEvent_Start(t *testing.T) {
	r, _ := newTestRenderer()
	r.processEvent(LoopDashEvent{
		Kind:          LoopDashEventStart,
		AgentName:     "loop-new-name",
		Project:       "otherproj",
		MaxIterations: 100,
	})

	assert.Equal(t, "loop-new-name", r.agentName)
	assert.Equal(t, "otherproj", r.project)
	assert.Equal(t, 100, r.maxIter)
}

func TestLoopDashRenderer_ProcessEvent_IterStart(t *testing.T) {
	r, _ := newTestRenderer()
	r.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 3,
	})

	assert.Equal(t, 3, r.currentIter)
	require.Len(t, r.activity, 1)
	assert.True(t, r.activity[0].running)
	assert.Equal(t, 3, r.activity[0].iteration)
}

func TestLoopDashRenderer_ProcessEvent_IterEnd(t *testing.T) {
	r, _ := newTestRenderer()
	r.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 1,
	})
	r.processEvent(LoopDashEvent{
		Kind:             LoopDashEventIterEnd,
		Iteration:        1,
		StatusText:       "IN_PROGRESS",
		TasksCompleted:   3,
		FilesModified:    5,
		TestsStatus:      "PASSING",
		CircuitProgress:  1,
		CircuitThreshold: 3,
		RateRemaining:    97,
		RateLimit:        100,
		IterDuration:     45 * time.Second,
		TotalTasks:       3,
		TotalFiles:       5,
		IterCostUSD:      0.0523,
		IterTokens:       15000,
		IterTurns:        4,
	})

	assert.Equal(t, 1, r.currentIter)
	assert.Equal(t, "IN_PROGRESS", r.statusText)
	assert.Equal(t, 3, r.totalTasks)
	assert.Equal(t, 5, r.totalFiles)
	assert.Equal(t, "PASSING", r.testsStatus)
	assert.Equal(t, 1, r.circuitProgress)
	assert.Equal(t, 3, r.circuitThreshold)
	assert.Equal(t, 97, r.rateRemaining)
	assert.Equal(t, 100, r.rateLimit)

	// Cost/token accumulation
	assert.InDelta(t, 0.0523, r.totalCostUSD, 0.0001)
	assert.Equal(t, 15000, r.totalTokens)
	assert.Equal(t, 4, r.totalTurns)

	// Activity should be updated (not running anymore)
	require.Len(t, r.activity, 1)
	assert.False(t, r.activity[0].running)
	assert.Equal(t, "IN_PROGRESS", r.activity[0].status)
	assert.Equal(t, 3, r.activity[0].tasks)
	assert.Equal(t, 5, r.activity[0].files)
	assert.InDelta(t, 0.0523, r.activity[0].costUSD, 0.0001)
	assert.Equal(t, 15000, r.activity[0].tokens)
	assert.Equal(t, 4, r.activity[0].turns)
}

func TestLoopDashRenderer_ProcessEvent_Complete(t *testing.T) {
	r, _ := newTestRenderer()
	r.processEvent(LoopDashEvent{
		Kind:       LoopDashEventComplete,
		ExitReason: "agent signaled completion",
		TotalTasks: 10,
		TotalFiles: 25,
	})

	assert.Equal(t, "agent signaled completion", r.exitReason)
	assert.Equal(t, 10, r.totalTasks)
	assert.Equal(t, 25, r.totalFiles)
}

func TestLoopDashRenderer_ProcessEvent_CompleteWithError(t *testing.T) {
	r, _ := newTestRenderer()
	testErr := errors.New("circuit breaker tripped")
	r.processEvent(LoopDashEvent{
		Kind:       LoopDashEventComplete,
		ExitReason: "stagnation: no progress",
		Error:      testErr,
	})

	assert.Equal(t, "stagnation: no progress", r.exitReason)
	assert.Equal(t, testErr, r.exitError)
}

func TestLoopDashRenderer_ProcessEvent_RateLimit(t *testing.T) {
	r, _ := newTestRenderer()
	r.processEvent(LoopDashEvent{
		Kind:          LoopDashEventRateLimit,
		RateRemaining: 0,
		RateLimit:     100,
	})

	assert.Equal(t, 0, r.rateRemaining)
	assert.Equal(t, 100, r.rateLimit)
}

func TestLoopDashRenderer_ActivityRingBuffer(t *testing.T) {
	r, _ := newTestRenderer()

	for i := 1; i <= maxActivityEntries+2; i++ {
		r.processEvent(LoopDashEvent{
			Kind:      LoopDashEventIterStart,
			Iteration: i,
		})
		r.processEvent(LoopDashEvent{
			Kind:       LoopDashEventIterEnd,
			Iteration:  i,
			StatusText: "IN_PROGRESS",
		})
	}

	assert.Len(t, r.activity, maxActivityEntries)
	assert.Equal(t, 3, r.activity[0].iteration)
	assert.Equal(t, maxActivityEntries+2, r.activity[maxActivityEntries-1].iteration)
}

func TestLoopDashRenderer_View_InitialState(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()
	view := r.View(cs, 80)

	assert.Contains(t, view, "Loop Dashboard")
	assert.Contains(t, view, "loop-brave-turing")
	assert.Contains(t, view, "myapp")
	assert.Contains(t, view, "Iteration: 0/50")
	assert.Contains(t, view, "Status")
	assert.Contains(t, view, "Activity")
	assert.Contains(t, view, "Waiting for first iteration")
}

func TestLoopDashRenderer_View_WithActivity(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	r.processEvent(LoopDashEvent{
		Kind:           LoopDashEventIterEnd,
		Iteration:      1,
		StatusText:     "IN_PROGRESS",
		TasksCompleted: 3,
		FilesModified:  8,
		TotalTasks:     3,
		TotalFiles:     8,
		IterDuration:   72 * time.Second,
	})
	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 2})

	view := r.View(cs, 80)

	assert.Contains(t, view, "Iteration: 2/50")
	assert.Contains(t, view, "IN_PROGRESS")
	assert.Contains(t, view, "[Loop 2] Running...")
	assert.Contains(t, view, "[Loop 1] IN_PROGRESS")
	assert.Contains(t, view, "3 tasks, 8 files")
}

func TestLoopDashRenderer_View_CircuitTripped(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()
	r.circuitTripped = true
	view := r.View(cs, 80)

	assert.Contains(t, view, "TRIPPED")
}

func TestLoopDashRenderer_View_WithRate(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()
	r.rateLimit = 100
	r.rateRemaining = 97
	view := r.View(cs, 80)

	assert.Contains(t, view, "Rate: 97/100")
}

func TestLoopDashRenderer_ProcessEvent_CostTokenAccumulation(t *testing.T) {
	r, _ := newTestRenderer()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventIterEnd,
		Iteration:   1,
		IterCostUSD: 0.05,
		IterTokens:  10000,
		IterTurns:   3,
	})

	assert.InDelta(t, 0.05, r.totalCostUSD, 0.0001)
	assert.Equal(t, 10000, r.totalTokens)
	assert.Equal(t, 3, r.totalTurns)

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 2})
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventIterEnd,
		Iteration:   2,
		IterCostUSD: 0.08,
		IterTokens:  25000,
		IterTurns:   5,
	})

	assert.InDelta(t, 0.13, r.totalCostUSD, 0.0001)
	assert.Equal(t, 35000, r.totalTokens)
	assert.Equal(t, 8, r.totalTurns)
}

func TestLoopDashRenderer_View_WithCostTokens(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	r.processEvent(LoopDashEvent{
		Kind:           LoopDashEventIterEnd,
		Iteration:      1,
		StatusText:     "IN_PROGRESS",
		TasksCompleted: 2,
		FilesModified:  4,
		TotalTasks:     2,
		TotalFiles:     4,
		IterCostUSD:    0.0523,
		IterTokens:     15000,
		IterTurns:      4,
		IterDuration:   30 * time.Second,
	})

	view := r.View(cs, 80)
	assert.Contains(t, view, "Cost:")
	assert.Contains(t, view, "$0.05")
	assert.Contains(t, view, "Tokens:")
	assert.Contains(t, view, "15.0k")
	assert.Contains(t, view, "Turns: 4")
}

func TestLoopDashRenderer_View_NoCostTokensBeforeFirstIteration(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()
	view := r.View(cs, 80)

	assert.NotContains(t, view, "Cost:")
	assert.NotContains(t, view, "Tokens:")
}

func TestFormatCostUSD(t *testing.T) {
	tests := []struct {
		cost float64
		want string
	}{
		{0.0, "$0.0000"},
		{0.0001, "$0.0001"},
		{0.005, "$0.0050"},
		{0.0523, "$0.05"},
		{0.15, "$0.15"},
		{1.50, "$1.50"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, formatCostUSD(tt.cost))
		})
	}
}

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		tokens int
		want   string
	}{
		{0, "0"},
		{500, "500"},
		{1500, "1.5k"},
		{15000, "15.0k"},
		{150000, "150.0k"},
		{1500000, "1.5M"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, formatTokenCount(tt.tokens))
		})
	}
}

func TestLoopDash_FormatElapsed(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{30 * time.Second, "30s"},
		{90 * time.Second, "1m 30s"},
		{5*time.Minute + 32*time.Second, "5m 32s"},
		{1*time.Hour + 5*time.Minute, "1h 5m"},
		{-1 * time.Second, "0s"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatElapsed(tt.d)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoopDash_FormatStatusText(t *testing.T) {
	cs := iostreams.NewTestIOStreams().IOStreams.ColorScheme()

	tests := []struct {
		status string
		desc   string
	}{
		{"COMPLETE", "success color"},
		{"BLOCKED", "error color"},
		{"IN_PROGRESS", "warning color"},
		{"", "muted PENDING"},
		{"UNKNOWN", "passthrough"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			result := formatStatusText(cs, tt.status)
			assert.NotEmpty(t, result)
		})
	}
}

func TestLoopDashRenderer_UpdateRunningActivity_NotFound(t *testing.T) {
	r, _ := newTestRenderer()

	r.updateRunningActivity(activityEntry{
		iteration: 1,
		status:    "IN_PROGRESS",
	})
	assert.Len(t, r.activity, 1)
	assert.Equal(t, "IN_PROGRESS", r.activity[0].status)
}

// ---------------------------------------------------------------------------
// Streaming output tests
// ---------------------------------------------------------------------------

func TestLoopDashRenderer_ProcessOutputEvent_Text(t *testing.T) {
	r, _ := newTestRenderer()

	// Start a running entry
	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})

	// Send text with newlines
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputText,
		OutputChunk: "I'll examine the failing tests\n",
	})

	require.Len(t, r.activity, 1)
	assert.Len(t, r.activity[0].outputLines, 1)
	assert.Equal(t, "I'll examine the failing tests", r.activity[0].outputLines[0])
}

func TestLoopDashRenderer_ProcessOutputEvent_ToolStart(t *testing.T) {
	r, _ := newTestRenderer()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})

	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputToolStart,
		OutputChunk: "[Using Bash...]",
	})

	require.Len(t, r.activity, 1)
	assert.Len(t, r.activity[0].outputLines, 1)
	assert.Equal(t, "[Using Bash...]", r.activity[0].outputLines[0])
}

func TestLoopDashRenderer_ProcessOutputEvent_MaxLines(t *testing.T) {
	r, _ := newTestRenderer()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})

	// Send more than maxOutputLines
	for i := 0; i < maxOutputLines+3; i++ {
		r.processEvent(LoopDashEvent{
			Kind:        LoopDashEventOutput,
			OutputKind:  OutputToolStart,
			OutputChunk: fmt.Sprintf("line %d", i),
		})
	}

	require.Len(t, r.activity, 1)
	assert.Len(t, r.activity[0].outputLines, maxOutputLines)
	// Should keep the most recent lines
	assert.Equal(t, "line 3", r.activity[0].outputLines[0])
}

func TestLoopDashRenderer_ProcessOutputEvent_ClearedOnIterEnd(t *testing.T) {
	r, _ := newTestRenderer()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputText,
		OutputChunk: "some output\n",
	})

	// Iteration ends — output cleared in completed entry
	r.processEvent(LoopDashEvent{
		Kind:       LoopDashEventIterEnd,
		Iteration:  1,
		StatusText: "IN_PROGRESS",
	})

	require.Len(t, r.activity, 1)
	assert.Empty(t, r.activity[0].outputLines)
}

func TestLoopDashRenderer_ProcessOutputEvent_NoRunningEntry(t *testing.T) {
	r, _ := newTestRenderer()

	// Output with no running entry should be silently ignored
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputText,
		OutputChunk: "orphaned output\n",
	})

	assert.Empty(t, r.activity)
}

func TestLoopDashRenderer_ProcessOutputEvent_BufferResetOnIterStart(t *testing.T) {
	r, _ := newTestRenderer()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	// Partial line (no newline)
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputText,
		OutputChunk: "partial",
	})
	assert.Equal(t, "partial", r.outputLineBuf.String())

	// New iteration starts — buffer should be reset
	r.processEvent(LoopDashEvent{
		Kind:       LoopDashEventIterEnd,
		Iteration:  1,
		StatusText: "IN_PROGRESS",
	})
	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 2})
	assert.Equal(t, "", r.outputLineBuf.String())
}

func TestLoopDashRenderer_View_WithOutputLines(t *testing.T) {
	r, ios := newTestRenderer()
	cs := ios.ColorScheme()

	r.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputText,
		OutputChunk: "I'll examine the failing tests\n",
	})
	r.processEvent(LoopDashEvent{
		Kind:        LoopDashEventOutput,
		OutputKind:  OutputToolStart,
		OutputChunk: "[Using Bash...]",
	})

	view := r.View(cs, 80)
	assert.Contains(t, view, "[Loop 1] Running...")
	assert.Contains(t, view, "I'll examine the failing tests")
	assert.Contains(t, view, "[Using Bash...]")
	assert.Contains(t, view, "⎿")
}
