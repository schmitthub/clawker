package shared

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// MonitorOptions configures the monitor output.
type MonitorOptions struct {
	// Writer is where monitor output goes (typically os.Stderr).
	Writer io.Writer

	// MaxLoops is the total max loops for progress calculation.
	MaxLoops int

	// ShowRateLimit shows rate limiter status if enabled.
	ShowRateLimit bool

	// RateLimiter to query for status.
	RateLimiter *RateLimiter

	// Verbose enables detailed output.
	Verbose bool
}

// Monitor provides real-time progress output for loop iterations.
type Monitor struct {
	opts      MonitorOptions
	startTime time.Time
}

// NewMonitor creates a new monitor with the given options.
func NewMonitor(opts MonitorOptions) *Monitor {
	return &Monitor{
		opts:      opts,
		startTime: time.Now(),
	}
}

// FormatLoopStart returns the loop start message.
func (m *Monitor) FormatLoopStart(loopNum int) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("[Loop %d/%d]", loopNum, m.opts.MaxLoops))
	parts = append(parts, "starting...")

	if m.opts.Verbose {
		elapsed := time.Since(m.startTime).Round(time.Second)
		parts = append(parts, fmt.Sprintf("(elapsed: %s)", elapsed))
	}

	return strings.Join(parts, " ")
}

// FormatLoopProgress returns the progress line for monitor mode.
// Format: [Loop 3/50] IN_PROGRESS | Tasks: 2 | Files: 5 | Rate: 97/100 | Circuit: 0/3
func (m *Monitor) FormatLoopProgress(loopNum int, status *Status, circuit *CircuitBreaker) string {
	var parts []string

	// Loop counter
	parts = append(parts, fmt.Sprintf("[Loop %d/%d]", loopNum, m.opts.MaxLoops))

	// Status
	if status != nil {
		parts = append(parts, status.Status)
	} else {
		parts = append(parts, "NO_STATUS")
	}

	// Tasks and files
	if status != nil {
		parts = append(parts, fmt.Sprintf("Tasks: %d", status.TasksCompleted))
		parts = append(parts, fmt.Sprintf("Files: %d", status.FilesModified))

		if status.TestsStatus != "" {
			parts = append(parts, fmt.Sprintf("Tests: %s", status.TestsStatus))
		}
	}

	// Rate limiter
	if m.opts.ShowRateLimit && m.opts.RateLimiter != nil && m.opts.RateLimiter.IsEnabled() {
		remaining := m.opts.RateLimiter.Remaining()
		limit := m.opts.RateLimiter.Limit()
		parts = append(parts, fmt.Sprintf("Rate: %d/%d", remaining, limit))
	}

	// Circuit state
	if circuit != nil {
		if circuit.IsTripped() {
			parts = append(parts, "Circuit: TRIPPED")
		} else {
			noProgress := circuit.NoProgressCount()
			threshold := circuit.Threshold()
			parts = append(parts, fmt.Sprintf("Circuit: %d/%d", noProgress, threshold))
		}
	}

	return strings.Join(parts, " | ")
}

// FormatLoopEnd returns the loop end message.
func (m *Monitor) FormatLoopEnd(loopNum int, status *Status, err error, outputSize int, elapsed time.Duration) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("[Loop %d]", loopNum))

	if err != nil {
		parts = append(parts, fmt.Sprintf("ERROR: %v", err))
	} else if status != nil {
		parts = append(parts, status.String())
	} else {
		parts = append(parts, "no status block")
	}

	if m.opts.Verbose {
		parts = append(parts, fmt.Sprintf("(%s, %d bytes)", elapsed.Round(time.Millisecond), outputSize))
	}

	return strings.Join(parts, " ")
}

// FormatResult returns the final result summary.
func (m *Monitor) FormatResult(result *Result) string {
	var b strings.Builder

	b.WriteString("\n=== Loop Complete ===\n")

	fmt.Fprintf(&b, "Loops: %d\n", result.LoopsCompleted)
	fmt.Fprintf(&b, "Exit:  %s\n", result.ExitReason)

	if result.Session != nil {
		fmt.Fprintf(&b, "Tasks: %d total\n", result.Session.TotalTasksCompleted)
		fmt.Fprintf(&b, "Files: %d total\n", result.Session.TotalFilesModified)
	}

	if result.FinalStatus != nil && result.FinalStatus.Recommendation != "" {
		fmt.Fprintf(&b, "Tip:   %s\n", result.FinalStatus.Recommendation)
	}

	totalElapsed := time.Since(m.startTime).Round(time.Second)
	fmt.Fprintf(&b, "Time:  %s\n", totalElapsed)

	if result.Error != nil {
		fmt.Fprintf(&b, "Error: %v\n", result.Error)
	}

	return b.String()
}

// FormatRateLimitWait returns the rate limit wait message.
func (m *Monitor) FormatRateLimitWait(resetTime time.Time) string {
	waitDuration := time.Until(resetTime).Round(time.Minute)
	return fmt.Sprintf("Rate limit hit. Resets in %s (at %s)",
		waitDuration, resetTime.Format("15:04"))
}

// FormatAPILimitError returns the API limit error message.
func (m *Monitor) FormatAPILimitError(isInteractive bool) string {
	msg := "Claude's API rate limit hit (likely 5-hour limit).\n"
	if isInteractive {
		msg += "Options:\n"
		msg += "  1. Wait ~60 minutes for limit to reset\n"
		msg += "  2. Exit and try again later\n"
	} else {
		msg += "Non-interactive mode: exiting. Retry in ~60 minutes."
	}
	return msg
}

// PrintLoopStart writes the loop start message.
func (m *Monitor) PrintLoopStart(loopNum int) {
	if m.opts.Writer != nil {
		fmt.Fprintln(m.opts.Writer, m.FormatLoopStart(loopNum))
	}
}

// PrintLoopProgress writes the progress line.
func (m *Monitor) PrintLoopProgress(loopNum int, status *Status, circuit *CircuitBreaker) {
	if m.opts.Writer != nil {
		fmt.Fprintln(m.opts.Writer, m.FormatLoopProgress(loopNum, status, circuit))
	}
}

// PrintLoopEnd writes the loop end message.
func (m *Monitor) PrintLoopEnd(loopNum int, status *Status, err error, outputSize int, elapsed time.Duration) {
	if m.opts.Writer != nil {
		fmt.Fprintln(m.opts.Writer, m.FormatLoopEnd(loopNum, status, err, outputSize, elapsed))
	}
}

// PrintResult writes the final result summary.
func (m *Monitor) PrintResult(result *Result) {
	if m.opts.Writer != nil {
		fmt.Fprint(m.opts.Writer, m.FormatResult(result))
	}
}
