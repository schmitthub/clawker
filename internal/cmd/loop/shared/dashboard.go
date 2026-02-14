package shared

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
	"github.com/schmitthub/clawker/internal/loop"
	"github.com/schmitthub/clawker/internal/tui"
)

// RunLoopConfig holds all inputs for RunLoop.
type RunLoopConfig struct {
	Runner      *loop.Runner
	RunnerOpts  loop.Options
	TUI         *tui.TUI
	IOStreams    *iostreams.IOStreams
	Setup       *LoopContainerResult
	Format      *cmdutil.FormatFlags
	Verbose     bool
	CommandName string // "iterate" or "tasks"
}

// RunLoop executes the loop with either the TUI dashboard (TTY default) or the
// text monitor (verbose/json/quiet/non-TTY). Returns the result and any error.
// This consolidates the output mode selection shared by iterate and tasks.
//
// When running in TUI mode, the user can:
//   - Press q/Esc to detach — the TUI exits and output switches to minimal text mode
//     while the loop continues running in the foreground.
//   - Press Ctrl+C to interrupt — the loop is cancelled and the process exits.
func RunLoop(ctx context.Context, cfg RunLoopConfig) (*loop.Result, error) {
	ios := cfg.IOStreams
	cs := ios.ColorScheme()
	useTUI := ios.IsStderrTTY() && !cfg.Verbose && !cfg.Format.Quiet && !cfg.Format.IsJSON()

	var result *loop.Result
	runnerOpts := cfg.RunnerOpts

	if useTUI {
		// Wrap context so we can cancel the runner on Ctrl+C
		runCtx, runCancel := context.WithCancel(ctx)
		defer runCancel()

		ch := make(chan tui.LoopDashEvent, 16)
		WireLoopDashboard(&runnerOpts, ch, cfg.Setup, runnerOpts.MaxLoops)

		var runErr error
		go func() {
			defer close(ch)
			result, runErr = cfg.Runner.Run(runCtx, runnerOpts)
		}()

		dashResult := cfg.TUI.RunLoopDashboard(tui.LoopDashboardConfig{
			AgentName: cfg.Setup.AgentName,
			Project:   cfg.Setup.Project,
			MaxLoops:  runnerOpts.MaxLoops,
		}, ch)
		if dashResult.Err != nil {
			runCancel()
			for range ch {
			}
			return nil, fmt.Errorf("dashboard error: %w", dashResult.Err)
		}

		if dashResult.Detached {
			// TUI exited but loop continues — switch to minimal text output
			fmt.Fprintf(ios.ErrOut, "%s Detached from dashboard — loop continues...\n", cs.InfoIcon())
			drainLoopEventsAsText(ios.ErrOut, cs, ch)
			// Runner goroutine has finished (channel closed)
			if runErr != nil {
				return nil, runErr
			}
			return result, nil
		}

		if dashResult.Interrupted {
			// Cancel the runner and drain remaining events
			runCancel()
			for range ch {
				// Drain to unblock the runner goroutine
			}
			return nil, cmdutil.SilentError
		}

		// Normal completion (channel closed, runner done)
		if runErr != nil {
			return nil, runErr
		}
	} else {
		// Quiet and JSON modes suppress progress output — only final result matters.
		showProgress := !cfg.Format.Quiet && !cfg.Format.IsJSON()

		if showProgress {
			monitor := loop.NewMonitor(loop.MonitorOptions{
				Writer:   ios.ErrOut,
				MaxLoops: runnerOpts.MaxLoops,
				Verbose:  cfg.Verbose,
			})
			runnerOpts.Monitor = monitor

			fmt.Fprintf(ios.ErrOut, "%s Starting loop %s for %s.%s (%d max loops)\n",
				cs.InfoIcon(), cfg.CommandName, cfg.Setup.Project, cfg.Setup.AgentName, runnerOpts.MaxLoops)
		}

		if cfg.Verbose {
			runnerOpts.OnOutput = func(chunk []byte) {
				_, _ = io.WriteString(ios.ErrOut, string(chunk))
			}
		}

		var runErr error
		result, runErr = cfg.Runner.Run(ctx, runnerOpts)
		if runErr != nil {
			return nil, runErr
		}
	}

	return result, nil
}

// drainLoopEventsAsText consumes remaining events from the dashboard channel
// and renders them as minimal text output. This is used when the user detaches
// from the TUI dashboard — the loop continues and events are displayed as
// simple status lines on stderr.
func drainLoopEventsAsText(w io.Writer, cs *iostreams.ColorScheme, ch <-chan tui.LoopDashEvent) {
	for ev := range ch {
		switch ev.Kind {
		case tui.LoopDashEventIterStart:
			fmt.Fprintf(w, "%s [Loop %d] Running...\n", cs.InfoIcon(), ev.Iteration)

		case tui.LoopDashEventIterEnd:
			icon := cs.SuccessIcon()
			if ev.Error != nil {
				icon = cs.FailureIcon()
			}

			detail := ""
			if ev.TasksCompleted > 0 || ev.FilesModified > 0 {
				detail = fmt.Sprintf(" — %d tasks, %d files", ev.TasksCompleted, ev.FilesModified)
			}

			durStr := ""
			if ev.IterDuration > 0 {
				durStr = fmt.Sprintf(" (%s)", formatMinimalDuration(ev.IterDuration))
			}

			statusText := ev.StatusText
			if statusText == "" {
				statusText = "done"
			}

			fmt.Fprintf(w, "%s [Loop %d] %s%s%s\n", icon, ev.Iteration, statusText, detail, durStr)

		case tui.LoopDashEventRateLimit:
			fmt.Fprintf(w, "%s Rate limit: %d/%d remaining\n", cs.WarningIcon(), ev.RateRemaining, ev.RateLimit)

		case tui.LoopDashEventComplete:
			if ev.Error != nil {
				fmt.Fprintf(w, "%s Loop ended: %s (%s)\n", cs.FailureIcon(), ev.ExitReason, ev.Error)
			} else if ev.ExitReason != "" {
				fmt.Fprintf(w, "%s Loop completed: %s\n", cs.InfoIcon(), ev.ExitReason)
			}
		}
	}
}

// formatMinimalDuration formats a duration for minimal text output.
func formatMinimalDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		return fmt.Sprintf("%dm %ds", secs/60, secs%60)
	default:
		return fmt.Sprintf("%dh %dm", secs/3600, (secs%3600)/60)
	}
}

// WireLoopDashboard sets Runner callback options to send events on the dashboard channel.
// It configures OnLoopStart, OnLoopEnd, and OnOutput callbacks. It does NOT close the
// channel — the goroutine wrapping runner.Run() is responsible for that.
func WireLoopDashboard(opts *loop.Options, ch chan<- tui.LoopDashEvent, setup *LoopContainerResult, maxLoops int) {
	// Send initial session start event
	ch <- tui.LoopDashEvent{
		Kind:          tui.LoopDashEventStart,
		AgentName:     setup.AgentName,
		Project:       setup.Project,
		MaxIterations: maxLoops,
	}

	// Track per-iteration start time for duration calculation
	var iterStart time.Time

	// Track session totals (accumulated across iterations)
	var totalTasks, totalFiles int

	opts.OnLoopStart = func(loopNum int) {
		iterStart = time.Now()
		sendEvent(ch, tui.LoopDashEvent{
			Kind:      tui.LoopDashEventIterStart,
			Iteration: loopNum,
		})
	}

	opts.OnLoopEnd = func(loopNum int, status *loop.Status, err error) {
		iterDuration := time.Since(iterStart)

		ev := tui.LoopDashEvent{
			Kind:         tui.LoopDashEventIterEnd,
			Iteration:    loopNum,
			IterDuration: iterDuration,
			Error:        err,
		}

		if status != nil {
			ev.StatusText = status.Status
			ev.TasksCompleted = status.TasksCompleted
			ev.FilesModified = status.FilesModified
			ev.TestsStatus = status.TestsStatus
			ev.ExitSignal = status.ExitSignal

			totalTasks += status.TasksCompleted
			totalFiles += status.FilesModified
		}

		ev.TotalTasks = totalTasks
		ev.TotalFiles = totalFiles

		sendEvent(ch, ev)
	}

	opts.OnOutput = func(chunk []byte) {
		sendEvent(ch, tui.LoopDashEvent{
			Kind:        tui.LoopDashEventOutput,
			OutputChunk: string(chunk),
		})
	}

	// Disable the text monitor — the dashboard replaces it
	opts.Monitor = nil
}

// sendEvent sends an event on the channel without blocking. If the channel is
// full, the event is dropped to prevent deadlocking the runner goroutine.
// Dropped events are logged as warnings with the event kind name for observability.
func sendEvent(ch chan<- tui.LoopDashEvent, ev tui.LoopDashEvent) {
	select {
	case ch <- ev:
	default:
		logger.Warn().Str("event_kind", ev.Kind.String()).Msg("dashboard event dropped: channel full")
	}
}
