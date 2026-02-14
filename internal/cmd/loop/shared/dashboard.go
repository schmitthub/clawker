package shared

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/cmdutil"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/logger"
)

// RunLoopConfig holds all inputs for RunLoop.
type RunLoopConfig struct {
	Runner      *Runner
	RunnerOpts  Options
	TUI         interface{} // unused after refactor, kept for API compat (TODO: remove)
	IOStreams   *iostreams.IOStreams
	Setup       *LoopContainerResult
	Format      *cmdutil.FormatFlags
	Verbose     bool
	CommandName string // "iterate" or "tasks"
}

// RunLoop executes the loop with either the TUI dashboard (TTY default) or the
// text monitor (verbose/json/quiet/non-TTY). Returns the result and any error.
// This consolidates the output mode selection shared by iterate and tasks.
func RunLoop(ctx context.Context, cfg RunLoopConfig) (*Result, error) {
	ios := cfg.IOStreams
	cs := ios.ColorScheme()
	useTUI := ios.IsStderrTTY() && !cfg.Verbose && !cfg.Format.Quiet && !cfg.Format.IsJSON()

	var result *Result
	runnerOpts := cfg.RunnerOpts

	if useTUI {
		runCtx, runCancel := context.WithCancel(ctx)
		defer runCancel()

		ch := make(chan LoopDashEvent, 16)
		WireLoopDashboard(&runnerOpts, ch, cfg.Setup, runnerOpts.MaxLoops)

		var runErr error
		go func() {
			defer close(ch)
			result, runErr = cfg.Runner.Run(runCtx, runnerOpts)
		}()

		dashResult := RunLoopDashboard(ios, LoopDashboardConfig{
			AgentName: cfg.Setup.AgentName,
			Project:   cfg.Setup.ProjectCfg.Project,
			MaxLoops:  runnerOpts.MaxLoops,
		}, ch)
		if dashResult.Err != nil {
			runCancel()
			for range ch {
			}
			return nil, fmt.Errorf("dashboard error: %w", dashResult.Err)
		}

		if dashResult.Detached {
			fmt.Fprintf(ios.ErrOut, "%s Detached from dashboard — loop continues...\n", cs.InfoIcon())
			drainLoopEventsAsText(ios.ErrOut, cs, ch)
			if runErr != nil {
				return nil, runErr
			}
			return result, nil
		}

		if dashResult.Interrupted {
			runCancel()
			for range ch {
			}
			return nil, cmdutil.SilentError
		}

		if runErr != nil {
			return nil, runErr
		}
	} else {
		showProgress := !cfg.Format.Quiet && !cfg.Format.IsJSON()

		if showProgress {
			monitor := NewMonitor(MonitorOptions{
				Writer:   ios.ErrOut,
				MaxLoops: runnerOpts.MaxLoops,
				Verbose:  cfg.Verbose,
			})
			runnerOpts.Monitor = monitor

			fmt.Fprintf(ios.ErrOut, "%s Starting loop %s for %s.%s (%d max loops)\n",
				cs.InfoIcon(), cfg.CommandName, cfg.Setup.ProjectCfg.Project, cfg.Setup.AgentName, runnerOpts.MaxLoops)
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
// and renders them as minimal text output after TUI detach.
func drainLoopEventsAsText(w io.Writer, cs *iostreams.ColorScheme, ch <-chan LoopDashEvent) {
	for ev := range ch {
		switch ev.Kind {
		case LoopDashEventIterStart:
			fmt.Fprintf(w, "%s [Loop %d] Running...\n", cs.InfoIcon(), ev.Iteration)

		case LoopDashEventIterEnd:
			icon := cs.SuccessIcon()
			if ev.Error != nil {
				icon = cs.FailureIcon()
			}

			var parts []string
			if ev.TasksCompleted > 0 || ev.FilesModified > 0 {
				parts = append(parts, fmt.Sprintf("%d tasks, %d files", ev.TasksCompleted, ev.FilesModified))
			}
			if ev.IterCostUSD > 0 {
				parts = append(parts, fmt.Sprintf("$%.4f", ev.IterCostUSD))
			}

			detail := ""
			if len(parts) > 0 {
				detail = " — " + strings.Join(parts, ", ")
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

		case LoopDashEventRateLimit:
			fmt.Fprintf(w, "%s Rate limit: %d/%d remaining\n", cs.WarningIcon(), ev.RateRemaining, ev.RateLimit)

		case LoopDashEventComplete:
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
func WireLoopDashboard(opts *Options, ch chan<- LoopDashEvent, setup *LoopContainerResult, maxLoops int) {
	ch <- LoopDashEvent{
		Kind:          LoopDashEventStart,
		AgentName:     setup.AgentName,
		Project:       setup.ProjectCfg.Project,
		MaxIterations: maxLoops,
	}

	var iterStart time.Time
	var totalTasks, totalFiles int

	opts.OnLoopStart = func(loopNum int) {
		iterStart = time.Now()
		sendEvent(ch, LoopDashEvent{
			Kind:      LoopDashEventIterStart,
			Iteration: loopNum,
		})
	}

	opts.OnLoopEnd = func(loopNum int, status *Status, resultEvent *ResultEvent, err error) {
		iterDuration := time.Since(iterStart)

		ev := LoopDashEvent{
			Kind:         LoopDashEventIterEnd,
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

		if resultEvent != nil {
			ev.IterCostUSD = resultEvent.TotalCostUSD
			ev.IterTurns = resultEvent.NumTurns
			if resultEvent.Usage != nil {
				ev.IterTokens = resultEvent.Usage.Total()
			}
		}

		ev.TotalTasks = totalTasks
		ev.TotalFiles = totalFiles

		sendEvent(ch, ev)
	}

	opts.OnStreamEvent = func(e *StreamDeltaEvent) {
		if name := e.ToolName(); name != "" {
			sendEvent(ch, LoopDashEvent{
				Kind:        LoopDashEventOutput,
				OutputKind:  OutputToolStart,
				OutputChunk: fmt.Sprintf("[Using %s...]", name),
			})
		}
		if text := e.TextDelta(); text != "" {
			sendEvent(ch, LoopDashEvent{
				Kind:        LoopDashEventOutput,
				OutputKind:  OutputText,
				OutputChunk: text,
			})
		}
	}

	opts.Monitor = nil
}

// sendEvent sends an event on the channel without blocking.
func sendEvent(ch chan<- LoopDashEvent, ev LoopDashEvent) {
	select {
	case ch <- ev:
	default:
		logger.Warn().Str("event_kind", ev.Kind.String()).Msg("dashboard event dropped: channel full")
	}
}
