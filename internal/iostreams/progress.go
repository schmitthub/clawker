package iostreams

import (
	"fmt"
	"strings"
	"sync"
)

// ProgressBar provides deterministic percentage progress for operations with known totals.
// TTY mode uses \r for an animated bar display. Non-TTY mode prints periodic line updates.
type ProgressBar struct {
	ios      *IOStreams
	total    int
	current  int
	label    string
	finished bool
	writeErr bool // circuit-breaker: skip renders after first write failure
	mu       sync.Mutex

	// Non-TTY threshold tracking: only print at 25% intervals
	lastPrintedPct int
}

// NewProgressBar creates a progress bar for an operation with a known total.
// The bar writes to ios.ErrOut (progress is status output, not data).
func (s *IOStreams) NewProgressBar(total int, label string) *ProgressBar {
	return &ProgressBar{
		ios:            s,
		total:          total,
		label:          label,
		lastPrintedPct: -1,
	}
}

// canUpdate reports whether the progress bar should accept updates.
// Caller must hold pb.mu.
func (pb *ProgressBar) canUpdate() bool {
	return !pb.finished && pb.ios.progressIndicatorEnabled
}

// Set updates the progress bar to the given value.
// Values are clamped to [0, total].
func (pb *ProgressBar) Set(current int) {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if !pb.canUpdate() {
		return
	}

	// Clamp
	if current < 0 {
		current = 0
	}
	if pb.total > 0 && current > pb.total {
		current = pb.total
	}

	pb.current = current
	pb.render()
}

// Increment advances the progress bar by one.
func (pb *ProgressBar) Increment() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if !pb.canUpdate() {
		return
	}

	pb.current++
	if pb.total > 0 && pb.current > pb.total {
		pb.current = pb.total
	}
	pb.render()
}

// Finish completes the progress bar at 100%.
func (pb *ProgressBar) Finish() {
	pb.mu.Lock()
	defer pb.mu.Unlock()

	if pb.finished {
		return
	}
	if !pb.ios.progressIndicatorEnabled {
		pb.finished = true
		return
	}

	pb.current = pb.total
	pb.finished = true

	if pb.ios.IsStderrTTY() {
		pb.renderTTY()
		fmt.Fprintln(pb.ios.ErrOut) // newline after final bar
	} else {
		pb.renderNonTTY(true)
	}
}

// render dispatches to TTY or non-TTY rendering.
// Caller must hold pb.mu.
func (pb *ProgressBar) render() {
	if pb.writeErr {
		return
	}
	if pb.ios.IsStderrTTY() {
		pb.renderTTY()
	} else {
		pb.renderNonTTY(false)
	}
}

// renderTTY renders an animated progress bar using \r to overwrite the line.
// Format: Label [====----] 45% (9/20)
func (pb *ProgressBar) renderTTY() {
	pct := pb.percentage()
	barWidth := 20
	filled := barWidth * pct / 100
	if filled > barWidth {
		filled = barWidth
	}

	bar := strings.Repeat("=", filled) + strings.Repeat("-", barWidth-filled)

	var err error
	if pb.total > 0 {
		_, err = fmt.Fprintf(pb.ios.ErrOut, "\r\033[K%s [%s] %d%% (%d/%d)", pb.label, bar, pct, pb.current, pb.total)
	} else {
		_, err = fmt.Fprintf(pb.ios.ErrOut, "\r\033[K%s [%s] %d%%", pb.label, bar, pct)
	}
	if err != nil {
		pb.writeErr = true
	}
}

// renderNonTTY prints periodic line updates at 25% intervals.
func (pb *ProgressBar) renderNonTTY(force bool) {
	pct := pb.percentage()

	// Print at 25% thresholds or when forced
	threshold := (pct / 25) * 25
	if !force && threshold == pb.lastPrintedPct {
		return
	}

	pb.lastPrintedPct = threshold
	if _, err := fmt.Fprintf(pb.ios.ErrOut, "%s... %d%%\n", pb.label, pct); err != nil {
		pb.writeErr = true
	}
}

// percentage calculates the current percentage.
func (pb *ProgressBar) percentage() int {
	if pb.total <= 0 {
		return 0
	}
	pct := pb.current * 100 / pb.total
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	return pct
}
