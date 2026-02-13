package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

func newTestDashModel(ch <-chan LoopDashEvent) loopDashboardModel {
	ios := iostreams.NewTestIOStreams()
	cfg := LoopDashboardConfig{
		AgentName: "loop-brave-turing",
		Project:   "myapp",
		MaxLoops:  50,
	}
	return newLoopDashboardModel(ios.IOStreams, cfg, ch)
}

func TestLoopDash_Init(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	cmd := m.Init()

	// Init should return a command (waitForLoopEvent)
	require.NotNil(t, cmd)
}

func TestLoopDash_Update_Detach_Q(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.True(t, model.detached, "q should detach, not interrupt")
	assert.False(t, model.interrupted, "q should not set interrupted")
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestLoopDash_Update_Detach_Esc(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := tea.KeyMsg{Type: tea.KeyEsc}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.True(t, model.detached, "Esc should detach, not interrupt")
	assert.False(t, model.interrupted, "Esc should not set interrupted")
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestLoopDash_Update_Interrupt_CtrlC(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.True(t, model.interrupted, "Ctrl+C should interrupt")
	assert.False(t, model.detached, "Ctrl+C should not set detached")
	assert.True(t, model.finished)
	require.NotNil(t, cmd)
}

func TestLoopDash_Update_WindowSize(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.Equal(t, 120, model.width)
	assert.Nil(t, cmd)
}

func TestLoopDash_Update_OtherKeys(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.False(t, model.interrupted)
	assert.False(t, model.finished)
	assert.Nil(t, cmd)
}

func TestLoopDash_Update_Event(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	ev := LoopDashEvent{
		Kind:          LoopDashEventIterStart,
		Iteration:     1,
		MaxIterations: 50,
	}
	msg := loopDashEventMsg(ev)
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.Equal(t, 1, model.currentIter)
	// Should return a command to wait for next event
	require.NotNil(t, cmd)
}

func TestLoopDash_Update_ChannelClosed(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	msg := loopDashChannelClosedMsg{}
	updated, cmd := m.Update(msg)
	model := updated.(loopDashboardModel)

	assert.True(t, model.finished)
	require.NotNil(t, cmd) // tea.Quit
}

func TestLoopDash_ProcessEvent_Start(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.processEvent(LoopDashEvent{
		Kind:          LoopDashEventStart,
		AgentName:     "loop-new-name",
		Project:       "otherproj",
		MaxIterations: 100,
	})

	assert.Equal(t, "loop-new-name", m.agentName)
	assert.Equal(t, "otherproj", m.project)
	assert.Equal(t, 100, m.maxIter)
}

func TestLoopDash_ProcessEvent_IterStart(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 3,
	})

	assert.Equal(t, 3, m.currentIter)
	require.Len(t, m.activity, 1)
	assert.True(t, m.activity[0].running)
	assert.Equal(t, 3, m.activity[0].iteration)
}

func TestLoopDash_ProcessEvent_IterEnd(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	// First add an IterStart
	m.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 1,
	})
	// Then the IterEnd
	m.processEvent(LoopDashEvent{
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
	})

	assert.Equal(t, 1, m.currentIter)
	assert.Equal(t, "IN_PROGRESS", m.statusText)
	assert.Equal(t, 3, m.totalTasks)
	assert.Equal(t, 5, m.totalFiles)
	assert.Equal(t, "PASSING", m.testsStatus)
	assert.Equal(t, 1, m.circuitProgress)
	assert.Equal(t, 3, m.circuitThreshold)
	assert.Equal(t, 97, m.rateRemaining)
	assert.Equal(t, 100, m.rateLimit)

	// Activity should be updated (not running anymore)
	require.Len(t, m.activity, 1)
	assert.False(t, m.activity[0].running)
	assert.Equal(t, "IN_PROGRESS", m.activity[0].status)
	assert.Equal(t, 3, m.activity[0].tasks)
	assert.Equal(t, 5, m.activity[0].files)
}

func TestLoopDash_ProcessEvent_Complete(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.processEvent(LoopDashEvent{
		Kind:       LoopDashEventComplete,
		ExitReason: "agent signaled completion",
		TotalTasks: 10,
		TotalFiles: 25,
	})

	assert.Equal(t, "agent signaled completion", m.exitReason)
	assert.Equal(t, 10, m.totalTasks)
	assert.Equal(t, 25, m.totalFiles)
}

func TestLoopDash_ProcessEvent_CompleteWithError(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	testErr := errors.New("circuit breaker tripped")
	m.processEvent(LoopDashEvent{
		Kind:       LoopDashEventComplete,
		ExitReason: "stagnation: no progress",
		Error:      testErr,
	})

	assert.Equal(t, "stagnation: no progress", m.exitReason)
	assert.Equal(t, testErr, m.exitError)
}

func TestLoopDash_ProcessEvent_RateLimit(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.processEvent(LoopDashEvent{
		Kind:          LoopDashEventRateLimit,
		RateRemaining: 0,
		RateLimit:     100,
	})

	assert.Equal(t, 0, m.rateRemaining)
	assert.Equal(t, 100, m.rateLimit)
}

func TestLoopDash_ActivityRingBuffer(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)

	// Add maxActivityEntries + 2 entries to test overflow
	for i := 1; i <= maxActivityEntries+2; i++ {
		m.processEvent(LoopDashEvent{
			Kind:      LoopDashEventIterStart,
			Iteration: i,
		})
		m.processEvent(LoopDashEvent{
			Kind:       LoopDashEventIterEnd,
			Iteration:  i,
			StatusText: "IN_PROGRESS",
		})
	}

	assert.Len(t, m.activity, maxActivityEntries)
	// Oldest should be iteration 3 (1 and 2 were evicted)
	assert.Equal(t, 3, m.activity[0].iteration)
	// Newest should be maxActivityEntries + 2
	assert.Equal(t, maxActivityEntries+2, m.activity[maxActivityEntries-1].iteration)
}

func TestLoopDash_View_InitialState(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	view := m.View()

	assert.Contains(t, view, "Loop Dashboard")
	assert.Contains(t, view, "loop-brave-turing")
	assert.Contains(t, view, "myapp")
	assert.Contains(t, view, "Iteration: 0/50")
	assert.Contains(t, view, "Status")
	assert.Contains(t, view, "Activity")
	assert.Contains(t, view, "q detach")
	assert.Contains(t, view, "ctrl+c stop")
	assert.Contains(t, view, "Waiting for first iteration")
}

func TestLoopDash_View_WithActivity(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)

	// Simulate iteration 1 complete
	m.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 1,
	})
	m.processEvent(LoopDashEvent{
		Kind:           LoopDashEventIterEnd,
		Iteration:      1,
		StatusText:     "IN_PROGRESS",
		TasksCompleted: 3,
		FilesModified:  8,
		TotalTasks:     3,
		TotalFiles:     8,
		IterDuration:   72 * time.Second,
	})

	// Simulate iteration 2 running
	m.processEvent(LoopDashEvent{
		Kind:      LoopDashEventIterStart,
		Iteration: 2,
	})

	view := m.View()

	assert.Contains(t, view, "Iteration: 2/50")
	assert.Contains(t, view, "IN_PROGRESS")
	assert.Contains(t, view, "[Loop 2] Running...")
	assert.Contains(t, view, "[Loop 1] IN_PROGRESS")
	assert.Contains(t, view, "3 tasks, 8 files")
}

func TestLoopDash_View_CircuitTripped(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.circuitTripped = true
	view := m.View()

	assert.Contains(t, view, "TRIPPED")
}

func TestLoopDash_View_WithRate(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.rateLimit = 100
	m.rateRemaining = 97
	view := m.View()

	assert.Contains(t, view, "Rate: 97/100")
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
			// Non-empty result for all cases
			assert.NotEmpty(t, result)
		})
	}
}

func TestLoopDash_UpdateRunningActivity_NotFound(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)

	// Update without prior start — should add entry
	m.updateRunningActivity(activityEntry{
		iteration: 1,
		status:    "IN_PROGRESS",
	})
	assert.Len(t, m.activity, 1)
	assert.Equal(t, "IN_PROGRESS", m.activity[0].status)
}

func TestLoopDash_HighWaterMark(t *testing.T) {
	ch := make(chan LoopDashEvent, 1)
	defer close(ch)

	m := newTestDashModel(ch)
	m.width = 80

	// Render once to set initial high water
	view1 := m.View()
	lines1 := len(strings.Split(view1, "\n"))

	// Add activity to increase height
	m.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 1})
	m.processEvent(LoopDashEvent{Kind: LoopDashEventIterEnd, Iteration: 1, StatusText: "IN_PROGRESS"})
	m.processEvent(LoopDashEvent{Kind: LoopDashEventIterStart, Iteration: 2})
	m.processEvent(LoopDashEvent{Kind: LoopDashEventIterEnd, Iteration: 2, StatusText: "IN_PROGRESS"})

	view2 := m.View()
	lines2 := len(strings.Split(view2, "\n"))

	// Second view should be >= first (high-water mark prevents shrinkage)
	assert.GreaterOrEqual(t, lines2, lines1)

	// High-water should be set
	assert.Greater(t, *m.highWater, 0)
}

