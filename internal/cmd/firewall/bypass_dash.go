package firewall

import (
	"fmt"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// ---------------------------------------------------------------------------
// Dashboard event types
// ---------------------------------------------------------------------------

type bypassTickEvent struct {
	remaining time.Duration
}

type bypassLogEvent struct {
	line string
}

// ---------------------------------------------------------------------------
// Dashboard renderer (implements tui.DashboardRenderer)
// ---------------------------------------------------------------------------

const (
	maxLogLines     = 200
	visibleLogLines = 15
)

type bypassDashRenderer struct {
	agent     string
	duration  time.Duration
	remaining time.Duration
	startTime time.Time
	logLines  []string
}

func newBypassDashRenderer(agent string, duration time.Duration) *bypassDashRenderer {
	return &bypassDashRenderer{
		agent:     agent,
		duration:  duration,
		remaining: duration,
		startTime: time.Now(),
	}
}

func (r *bypassDashRenderer) ProcessEvent(ev any) {
	switch e := ev.(type) {
	case bypassTickEvent:
		r.remaining = e.remaining
	case bypassLogEvent:
		r.logLines = append(r.logLines, e.line)
		if len(r.logLines) > maxLogLines {
			r.logLines = r.logLines[len(r.logLines)-maxLogLines:]
		}
	}
}

func (r *bypassDashRenderer) View(cs *iostreams.ColorScheme, width int) string {
	var buf strings.Builder

	// Header bar
	buf.WriteString(tui.RenderDashHeader(cs, tui.DashHeaderConfig{
		Title:    "Firewall Bypass",
		Subtitle: r.agent,
		Width:    width,
	}))
	buf.WriteByte('\n')

	// Info line
	elapsed := formatBypassElapsed(time.Since(r.startTime))
	fmt.Fprintf(&buf, "  %s    %s    %s\n",
		tui.TimerIndicator("Remaining", formatBypassRemaining(cs, r.remaining)),
		tui.TimerIndicator("Elapsed", elapsed),
		tui.TimerIndicator("Duration", r.duration.String()),
	)

	buf.WriteByte('\n')

	// Proxy log panel — auto-scroll to bottom
	offset := 0
	if len(r.logLines) > visibleLogLines {
		offset = len(r.logLines) - visibleLogLines
	}
	buf.WriteString(tui.RenderScrollablePanel("Proxy Log", r.logLines, offset, visibleLogLines, width-4))
	buf.WriteByte('\n')

	return buf.String()
}

func formatBypassRemaining(cs *iostreams.ColorScheme, d time.Duration) string {
	d = d.Truncate(time.Second)
	s := d.String()
	if d <= 0 {
		return cs.Error("0s")
	}
	if d <= 30*time.Second {
		return cs.Warning(s)
	}
	return s
}

func formatBypassElapsed(d time.Duration) string {
	d = d.Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", m, s)
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// BypassDashboardConfig holds the configuration for the bypass dashboard.
type BypassDashboardConfig struct {
	Agent    string
	Duration time.Duration
}

// BypassDashboardResult is returned when the dashboard exits.
type BypassDashboardResult struct {
	Err         error
	Detached    bool // user pressed q/Esc (switch to non-interactive)
	Interrupted bool // user pressed Ctrl+C (stop bypass)
}

// RunBypassDashboard runs the interactive bypass dashboard.
func RunBypassDashboard(ios *iostreams.IOStreams, cfg BypassDashboardConfig, ch <-chan any) BypassDashboardResult {
	renderer := newBypassDashRenderer(cfg.Agent, cfg.Duration)

	result := tui.RunDashboard(ios, renderer, tui.DashboardConfig{
		HelpText: "q detach  ctrl+c stop",
	}, ch)

	return BypassDashboardResult{
		Err:         result.Err,
		Detached:    result.Detached,
		Interrupted: result.Interrupted,
	}
}
