package shared

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/internal/tui"
)

func TestWireLoopDashboard_SendsStartEvent(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{
		AgentName: "loop-test-agent",
		Project:   "testproj",
	}

	WireLoopDashboard(opts, ch, setup, 50)

	// Should have sent a start event
	ev := <-ch
	assert.Equal(t, tui.LoopDashEventStart, ev.Kind)
	assert.Equal(t, "loop-test-agent", ev.AgentName)
	assert.Equal(t, "testproj", ev.Project)
	assert.Equal(t, 50, ev.MaxIterations)
}

func TestWireLoopDashboard_SetsCallbacks(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{
		AgentName: "loop-test-agent",
		Project:   "testproj",
	}

	WireLoopDashboard(opts, ch, setup, 50)

	// Drain start event
	<-ch

	assert.NotNil(t, opts.OnLoopStart)
	assert.NotNil(t, opts.OnLoopEnd)
	assert.NotNil(t, opts.OnOutput)
	assert.Nil(t, opts.Monitor, "Monitor should be nil when dashboard is wired")
}

func TestWireLoopDashboard_OnLoopStart(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	opts.OnLoopStart(3)

	ev := <-ch
	assert.Equal(t, tui.LoopDashEventIterStart, ev.Kind)
	assert.Equal(t, 3, ev.Iteration)
}

func TestWireLoopDashboard_OnLoopEnd(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	// Simulate start then end
	opts.OnLoopStart(1)
	<-ch // drain iter start

	status := &loop.Status{
		Status:         "IN_PROGRESS",
		TasksCompleted: 3,
		FilesModified:  5,
		TestsStatus:    "PASSING",
		ExitSignal:     false,
	}
	opts.OnLoopEnd(1, status, nil)

	ev := <-ch
	assert.Equal(t, tui.LoopDashEventIterEnd, ev.Kind)
	assert.Equal(t, 1, ev.Iteration)
	assert.Equal(t, "IN_PROGRESS", ev.StatusText)
	assert.Equal(t, 3, ev.TasksCompleted)
	assert.Equal(t, 5, ev.FilesModified)
	assert.Equal(t, "PASSING", ev.TestsStatus)
	assert.False(t, ev.ExitSignal)
	assert.Nil(t, ev.Error)
	assert.Equal(t, 3, ev.TotalTasks)
	assert.Equal(t, 5, ev.TotalFiles)
	assert.Greater(t, ev.IterDuration, time.Duration(0))
}

func TestWireLoopDashboard_OnLoopEnd_NilStatus(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	opts.OnLoopStart(1)
	<-ch // drain iter start

	opts.OnLoopEnd(1, nil, nil)

	ev := <-ch
	assert.Equal(t, tui.LoopDashEventIterEnd, ev.Kind)
	assert.Equal(t, "", ev.StatusText)
	assert.Equal(t, 0, ev.TasksCompleted)
}

func TestWireLoopDashboard_OnLoopEnd_AccumulatesTotals(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	// Iteration 1
	opts.OnLoopStart(1)
	<-ch
	opts.OnLoopEnd(1, &loop.Status{TasksCompleted: 2, FilesModified: 3}, nil)
	ev1 := <-ch
	assert.Equal(t, 2, ev1.TotalTasks)
	assert.Equal(t, 3, ev1.TotalFiles)

	// Iteration 2 — totals should accumulate
	opts.OnLoopStart(2)
	<-ch
	opts.OnLoopEnd(2, &loop.Status{TasksCompleted: 1, FilesModified: 4}, nil)
	ev2 := <-ch
	assert.Equal(t, 3, ev2.TotalTasks)
	assert.Equal(t, 7, ev2.TotalFiles)
}

func TestWireLoopDashboard_OnOutput(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	opts.OnOutput([]byte("hello world"))

	ev := <-ch
	assert.Equal(t, tui.LoopDashEventOutput, ev.Kind)
	assert.Equal(t, "hello world", ev.OutputChunk)
}

func TestWireLoopDashboard_OnLoopEnd_WithError(t *testing.T) {
	opts := &loop.Options{}
	ch := make(chan tui.LoopDashEvent, 16)
	setup := &LoopContainerResult{AgentName: "test", Project: "proj"}

	WireLoopDashboard(opts, ch, setup, 10)
	<-ch // drain start

	opts.OnLoopStart(1)
	<-ch

	testErr := assert.AnError
	opts.OnLoopEnd(1, nil, testErr)

	ev := <-ch
	require.Error(t, ev.Error)
	assert.Equal(t, testErr, ev.Error)
}

func TestSendEvent_FullChannel(t *testing.T) {
	// Create a channel with no buffer
	ch := make(chan tui.LoopDashEvent)

	// Sending to a full (unbuffered, no receiver) channel should not block
	done := make(chan struct{})
	go func() {
		sendEvent(ch, tui.LoopDashEvent{Kind: tui.LoopDashEventOutput})
		close(done)
	}()

	select {
	case <-done:
		// sendEvent returned without blocking — correct
	case <-time.After(100 * time.Millisecond):
		t.Fatal("sendEvent blocked on full channel")
	}
}

// ---------------------------------------------------------------------------
// drainLoopEventsAsText tests
// ---------------------------------------------------------------------------

func TestDrainLoopEventsAsText_IterStart(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{Kind: tui.LoopDashEventIterStart, Iteration: 3}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	assert.Contains(t, buf.String(), "[Loop 3] Running...")
}

func TestDrainLoopEventsAsText_IterEnd(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:           tui.LoopDashEventIterEnd,
		Iteration:      2,
		StatusText:     "IN_PROGRESS",
		TasksCompleted: 3,
		FilesModified:  5,
		IterDuration:   90 * time.Second,
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	output := buf.String()
	assert.Contains(t, output, "[Loop 2] IN_PROGRESS")
	assert.Contains(t, output, "3 tasks, 5 files")
	assert.Contains(t, output, "1m 30s")
}

func TestDrainLoopEventsAsText_IterEnd_WithError(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:       tui.LoopDashEventIterEnd,
		Iteration:  1,
		StatusText: "BLOCKED",
		Error:      errors.New("test error"),
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	output := buf.String()
	assert.Contains(t, output, "[Loop 1] BLOCKED")
}

func TestDrainLoopEventsAsText_IterEnd_NoStatus(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:      tui.LoopDashEventIterEnd,
		Iteration: 1,
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	assert.Contains(t, buf.String(), "[Loop 1] done")
}

func TestDrainLoopEventsAsText_RateLimit(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:          tui.LoopDashEventRateLimit,
		RateRemaining: 5,
		RateLimit:     100,
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	assert.Contains(t, buf.String(), "5/100")
}

func TestDrainLoopEventsAsText_Complete(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:       tui.LoopDashEventComplete,
		ExitReason: "agent signaled completion",
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	assert.Contains(t, buf.String(), "Loop completed: agent signaled completion")
}

func TestDrainLoopEventsAsText_CompleteWithError(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{
		Kind:       tui.LoopDashEventComplete,
		ExitReason: "circuit breaker tripped",
		Error:      errors.New("stagnation"),
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	output := buf.String()
	assert.Contains(t, output, "Loop ended: circuit breaker tripped")
	assert.Contains(t, output, "stagnation")
}

func TestDrainLoopEventsAsText_MultipleEvents(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 8)
	ch <- tui.LoopDashEvent{Kind: tui.LoopDashEventIterStart, Iteration: 1}
	ch <- tui.LoopDashEvent{
		Kind: tui.LoopDashEventIterEnd, Iteration: 1,
		StatusText: "IN_PROGRESS", TasksCompleted: 2, FilesModified: 3,
		IterDuration: 30 * time.Second,
	}
	ch <- tui.LoopDashEvent{Kind: tui.LoopDashEventIterStart, Iteration: 2}
	ch <- tui.LoopDashEvent{
		Kind: tui.LoopDashEventIterEnd, Iteration: 2,
		StatusText: "COMPLETE", ExitSignal: true,
		IterDuration: 45 * time.Second,
	}
	ch <- tui.LoopDashEvent{
		Kind:       tui.LoopDashEventComplete,
		ExitReason: "agent signaled completion",
	}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	output := buf.String()
	assert.Contains(t, output, "[Loop 1] Running...")
	assert.Contains(t, output, "[Loop 1] IN_PROGRESS")
	assert.Contains(t, output, "[Loop 2] Running...")
	assert.Contains(t, output, "[Loop 2] COMPLETE")
	assert.Contains(t, output, "Loop completed: agent signaled completion")
}

func TestDrainLoopEventsAsText_IgnoresOutputEvents(t *testing.T) {
	var buf bytes.Buffer
	tio := iostreams.NewTestIOStreams()
	cs := tio.IOStreams.ColorScheme()

	ch := make(chan tui.LoopDashEvent, 4)
	ch <- tui.LoopDashEvent{Kind: tui.LoopDashEventOutput, OutputChunk: "some output"}
	ch <- tui.LoopDashEvent{Kind: tui.LoopDashEventStart, AgentName: "test"}
	close(ch)

	drainLoopEventsAsText(&buf, cs, ch)

	// Output and Start events should be silently ignored in minimal mode
	assert.Empty(t, buf.String())
}

// ---------------------------------------------------------------------------
// formatMinimalDuration tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// RunLoop mode selection tests
// ---------------------------------------------------------------------------

// TestRunLoop_ModeSelection verifies that the correct output mode is selected
// for each combination of TTY status and format flags. It uses MaxLoops=0 so
// the runner returns immediately without needing Docker.
//
// TTY default (TUI mode) is NOT tested here — it enters the BubbleTea branch
// which requires a real terminal. TUI behavior is covered by dashboard model tests.
func TestRunLoop_ModeSelection(t *testing.T) {
	tests := []struct {
		name         string
		tty          bool
		verbose      bool
		json         bool
		quiet        bool
		wantStartMsg bool // "Starting loop" printed to stderr
	}{
		// TTY modes (non-TUI paths — TUI default skipped)
		{"TTY verbose", true, true, false, false, true},
		{"TTY json", true, false, true, false, false},
		{"TTY quiet", true, false, false, true, false},
		// Non-TTY modes
		{"non-TTY default", false, false, false, false, true},
		{"non-TTY json", false, false, true, false, false},
		{"non-TTY quiet", false, false, false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp stores for the Runner.
			// Use a pre-cancelled context so Run exits immediately on the first
			// loop iteration without needing Docker.
			tmpDir := t.TempDir()
			store := loop.NewSessionStore(filepath.Join(tmpDir, "sessions"))
			history := loop.NewHistoryStore(filepath.Join(tmpDir, "history"))
			runner := loop.NewRunnerWith(nil, store, history)

			ctx, cancel := context.WithCancel(context.Background())
			cancel() // pre-cancel so Run exits immediately

			tio := iostreams.NewTestIOStreams()
			tio.SetInteractive(tt.tty)

			format := &cmdutil.FormatFlags{Quiet: tt.quiet}
			if tt.json {
				format.Format, _ = cmdutil.ParseFormat("json")
			}

			cfg := RunLoopConfig{
				Ctx:    ctx,
				Runner: runner,
				RunnerOpts: loop.Options{
					MaxLoops:      1,
					Project:       "testproj",
					Agent:         "testagent",
					ContainerName: "clawker.testproj.testagent",
				},
				TUI:         tui.NewTUI(tio.IOStreams),
				IOStreams:    tio.IOStreams,
				Setup:       &LoopContainerResult{AgentName: "testagent", Project: "testproj"},
				Format:      format,
				Verbose:     tt.verbose,
				CommandName: "iterate",
			}

			result, err := RunLoop(cfg)
			require.NoError(t, err)
			require.NotNil(t, result)

			stderr := tio.ErrBuf.String()
			if tt.wantStartMsg {
				assert.Contains(t, stderr, "Starting loop", "expected start message in stderr")
			} else {
				assert.NotContains(t, stderr, "Starting loop", "expected no start message in stderr")
			}
		})
	}
}

func TestFormatMinimalDuration(t *testing.T) {
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
			got := formatMinimalDuration(tt.d)
			assert.Equal(t, tt.want, got)
		})
	}
}
