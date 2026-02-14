package shared

import (
	"fmt"
	"strings"
	"time"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
	"github.com/schmitthub/clawker/internal/tui"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// LoopDashEventKind discriminates dashboard events.
type LoopDashEventKind int

const (
	// LoopDashEventStart is sent once at the beginning of a loop session.
	LoopDashEventStart LoopDashEventKind = iota

	// LoopDashEventIterStart is sent when an iteration begins.
	LoopDashEventIterStart

	// LoopDashEventIterEnd is sent when an iteration completes.
	LoopDashEventIterEnd

	// LoopDashEventOutput is sent with output chunks for the streaming feed.
	LoopDashEventOutput

	// LoopDashEventRateLimit is sent when the rate limiter triggers a wait.
	LoopDashEventRateLimit

	// LoopDashEventComplete is sent when the loop session finishes.
	LoopDashEventComplete
)

// String returns a human-readable name for the event kind.
func (k LoopDashEventKind) String() string {
	switch k {
	case LoopDashEventStart:
		return "Start"
	case LoopDashEventIterStart:
		return "IterStart"
	case LoopDashEventIterEnd:
		return "IterEnd"
	case LoopDashEventOutput:
		return "Output"
	case LoopDashEventRateLimit:
		return "RateLimit"
	case LoopDashEventComplete:
		return "Complete"
	default:
		return fmt.Sprintf("Unknown(%d)", int(k))
	}
}

// OutputKind distinguishes output chunk types.
type OutputKind int

const (
	// OutputText is a raw text chunk from the assistant.
	OutputText OutputKind = iota

	// OutputToolStart indicates a tool invocation began.
	OutputToolStart
)

// LoopDashEvent is sent on the channel to update the dashboard.
type LoopDashEvent struct {
	Kind          LoopDashEventKind
	Iteration     int
	MaxIterations int
	AgentName     string
	Project       string

	// Status (populated on IterEnd)
	StatusText     string
	TasksCompleted int
	FilesModified  int
	TestsStatus    string
	ExitSignal     bool

	// Circuit breaker
	CircuitProgress  int
	CircuitThreshold int
	CircuitTripped   bool

	// Rate limiter
	RateRemaining int
	RateLimit     int

	// Timing
	IterDuration time.Duration

	// Completion
	ExitReason string
	Error      error

	// Session totals
	TotalTasks int
	TotalFiles int

	// Cost/token data (populated on IterEnd from ResultEvent)
	IterCostUSD float64
	IterTokens  int
	IterTurns   int

	// Output
	OutputChunk string
	OutputKind  OutputKind
}

// LoopDashboardConfig configures the dashboard.
type LoopDashboardConfig struct {
	AgentName string
	Project   string
	MaxLoops  int
}

// LoopDashboardResult is returned when the dashboard exits.
type LoopDashboardResult struct {
	Err         error // display error only
	Detached    bool  // user pressed q/Esc — loop continues, switch to minimal output
	Interrupted bool  // user pressed Ctrl+C — stop the loop
}

// ---------------------------------------------------------------------------
// Activity log entry
// ---------------------------------------------------------------------------

type activityEntry struct {
	iteration   int
	status      string // "Running", "IN_PROGRESS", "COMPLETE", "BLOCKED", or error text
	tasks       int
	files       int
	costUSD     float64
	tokens      int
	turns       int
	duration    time.Duration
	isError     bool
	running     bool
	outputLines []string // streaming output lines (only for running entries)
}

const (
	maxActivityEntries = 10
	maxOutputLines     = 5
)

// ---------------------------------------------------------------------------
// Loop dashboard renderer (implements tui.DashboardRenderer)
// ---------------------------------------------------------------------------

type loopDashRenderer struct {
	cs  *iostreams.ColorScheme
	cfg LoopDashboardConfig

	// State
	currentIter   int
	maxIter       int
	agentName     string
	project       string
	startTime     time.Time
	iterStartTime time.Time

	// Latest status
	statusText  string
	totalTasks  int
	totalFiles  int
	testsStatus string

	// Cost/token accumulation
	totalCostUSD float64
	totalTokens  int
	totalTurns   int

	// Circuit breaker
	circuitProgress  int
	circuitThreshold int
	circuitTripped   bool

	// Rate limiter
	rateRemaining int
	rateLimit     int

	// Activity log (ring buffer)
	activity []activityEntry

	// Completion
	exitReason string
	exitError  error

	// Streaming output line buffer
	outputLineBuf strings.Builder
}

func newLoopDashRenderer(ios *iostreams.IOStreams, cfg LoopDashboardConfig) *loopDashRenderer {
	return &loopDashRenderer{
		cs:               ios.ColorScheme(),
		cfg:              cfg,
		agentName:        cfg.AgentName,
		project:          cfg.Project,
		maxIter:          cfg.MaxLoops,
		startTime:        time.Now(),
		circuitThreshold: 3, // default, updated by events
	}
}

// ProcessEvent implements tui.DashboardRenderer.
func (r *loopDashRenderer) ProcessEvent(ev any) {
	e := ev.(LoopDashEvent)
	r.processEvent(e)
}

// View implements tui.DashboardRenderer.
func (r *loopDashRenderer) View(cs *iostreams.ColorScheme, width int) string {
	var buf strings.Builder

	// Header bar
	renderLoopDashHeader(&buf, cs, r.agentName, width)
	buf.WriteByte('\n')

	// Info line: Agent / ProjectCfg / Elapsed
	elapsed := time.Since(r.startTime)
	infoLine := fmt.Sprintf("  Agent: %s    ProjectCfg: %s    Elapsed: %s",
		r.agentName, r.project, formatElapsed(elapsed))
	buf.WriteString(infoLine)
	buf.WriteByte('\n')

	// Counters line: Iteration / Circuit / Rate
	iterStr := fmt.Sprintf("%d/%d", r.currentIter, r.maxIter)
	circuitStr := fmt.Sprintf("%d/%d", r.circuitProgress, r.circuitThreshold)
	if r.circuitTripped {
		circuitStr = cs.Error("TRIPPED")
	}
	rateStr := ""
	if r.rateLimit > 0 {
		rateStr = fmt.Sprintf("      Rate: %d/%d", r.rateRemaining, r.rateLimit)
	}
	countersLine := fmt.Sprintf("  Iteration: %s             Circuit: %s%s",
		iterStr, circuitStr, rateStr)
	buf.WriteString(countersLine)
	buf.WriteByte('\n')

	// Cost/token line (only shown after first iteration completes)
	if r.totalTokens > 0 || r.totalCostUSD > 0 {
		costLine := fmt.Sprintf("  Cost: %s  Tokens: %s  Turns: %d",
			formatCostUSD(r.totalCostUSD), formatTokenCount(r.totalTokens), r.totalTurns)
		buf.WriteString(cs.Muted(costLine))
		buf.WriteByte('\n')
	}

	buf.WriteByte('\n')

	// Status section
	renderLoopDashStatusSection(&buf, cs, r.statusText, r.totalTasks, r.totalFiles, r.testsStatus, width)
	buf.WriteByte('\n')

	// Activity section
	renderLoopDashActivitySection(&buf, cs, r.activity, width)

	buf.WriteByte('\n')

	return buf.String()
}

func (r *loopDashRenderer) processEvent(ev LoopDashEvent) {
	switch ev.Kind {
	case LoopDashEventStart:
		r.agentName = ev.AgentName
		r.project = ev.Project
		r.maxIter = ev.MaxIterations

	case LoopDashEventIterStart:
		r.currentIter = ev.Iteration
		r.iterStartTime = time.Now()
		r.outputLineBuf.Reset()
		// Add "Running..." entry
		r.addActivity(activityEntry{
			iteration: ev.Iteration,
			status:    "Running",
			running:   true,
		})

	case LoopDashEventIterEnd:
		r.currentIter = ev.Iteration
		r.statusText = ev.StatusText
		r.totalTasks = ev.TotalTasks
		r.totalFiles = ev.TotalFiles
		r.testsStatus = ev.TestsStatus
		r.circuitProgress = ev.CircuitProgress
		r.circuitThreshold = ev.CircuitThreshold
		r.circuitTripped = ev.CircuitTripped
		r.rateRemaining = ev.RateRemaining
		r.rateLimit = ev.RateLimit
		r.totalCostUSD += ev.IterCostUSD
		r.totalTokens += ev.IterTokens
		r.totalTurns += ev.IterTurns
		// Update the running entry to completed (output lines cleared)
		r.updateRunningActivity(activityEntry{
			iteration: ev.Iteration,
			status:    ev.StatusText,
			tasks:     ev.TasksCompleted,
			files:     ev.FilesModified,
			costUSD:   ev.IterCostUSD,
			tokens:    ev.IterTokens,
			turns:     ev.IterTurns,
			duration:  ev.IterDuration,
			isError:   ev.Error != nil,
		})

	case LoopDashEventOutput:
		r.processOutputEvent(ev)

	case LoopDashEventRateLimit:
		r.rateRemaining = ev.RateRemaining
		r.rateLimit = ev.RateLimit

	case LoopDashEventComplete:
		r.exitReason = ev.ExitReason
		r.exitError = ev.Error
		r.totalTasks = ev.TotalTasks
		r.totalFiles = ev.TotalFiles
	}
}

func (r *loopDashRenderer) processOutputEvent(ev LoopDashEvent) {
	// Find the current running entry
	idx := r.findRunningEntry()
	if idx < 0 {
		return
	}

	switch ev.OutputKind {
	case OutputToolStart:
		// Push tool indicator line directly
		r.pushOutputLine(idx, ev.OutputChunk)

	case OutputText:
		// Accumulate in line buffer, push complete lines on \n
		for _, ch := range ev.OutputChunk {
			if ch == '\n' {
				line := strings.TrimSpace(r.outputLineBuf.String())
				if line != "" {
					r.pushOutputLine(idx, line)
				}
				r.outputLineBuf.Reset()
			} else {
				r.outputLineBuf.WriteRune(ch)
			}
		}
	}
}

func (r *loopDashRenderer) findRunningEntry() int {
	for i := len(r.activity) - 1; i >= 0; i-- {
		if r.activity[i].running {
			return i
		}
	}
	return -1
}

func (r *loopDashRenderer) pushOutputLine(idx int, line string) {
	entry := &r.activity[idx]
	entry.outputLines = append(entry.outputLines, line)
	if len(entry.outputLines) > maxOutputLines {
		entry.outputLines = entry.outputLines[len(entry.outputLines)-maxOutputLines:]
	}
}

func (r *loopDashRenderer) addActivity(entry activityEntry) {
	if len(r.activity) >= maxActivityEntries {
		r.activity = r.activity[1:]
	}
	r.activity = append(r.activity, entry)
}

func (r *loopDashRenderer) updateRunningActivity(entry activityEntry) {
	for i := len(r.activity) - 1; i >= 0; i-- {
		if r.activity[i].running && r.activity[i].iteration == entry.iteration {
			r.activity[i] = entry
			return
		}
	}
	r.addActivity(entry)
}

// ---------------------------------------------------------------------------
// Render helpers
// ---------------------------------------------------------------------------

func renderLoopDashHeader(buf *strings.Builder, cs *iostreams.ColorScheme, agentName string, width int) {
	title := "  ━━ Loop Dashboard "
	subtitle := fmt.Sprintf(" %s ━━", agentName)

	titleRendered := cs.Bold(cs.Primary(title))
	subtitleRendered := cs.Muted(subtitle)

	titleWidth := text.CountVisibleWidth(titleRendered)
	subtitleWidth := text.CountVisibleWidth(subtitleRendered)
	fillWidth := width - titleWidth - subtitleWidth
	if fillWidth < 3 {
		fillWidth = 3
	}
	fill := cs.Muted(strings.Repeat("━", fillWidth))

	buf.WriteString(titleRendered)
	buf.WriteString(fill)
	buf.WriteString(subtitleRendered)
	buf.WriteByte('\n')
}

func renderLoopDashStatusSection(buf *strings.Builder, cs *iostreams.ColorScheme, statusText string, totalTasks, totalFiles int, testsStatus string, width int) {
	// Divider
	divLabel := " Status "
	divFill := width - text.CountVisibleWidth(divLabel) - 4
	if divFill < 3 {
		divFill = 3
	}
	buf.WriteString("  ")
	buf.WriteString(cs.Muted("───" + divLabel + strings.Repeat("─", divFill)))
	buf.WriteByte('\n')

	// Status line
	statusColored := formatStatusText(cs, statusText)
	parts := []string{"  " + statusColored}
	if totalTasks > 0 || totalFiles > 0 {
		parts = append(parts, fmt.Sprintf("Tasks: %d", totalTasks))
		parts = append(parts, fmt.Sprintf("Files: %d", totalFiles))
	}
	if testsStatus != "" {
		parts = append(parts, fmt.Sprintf("Tests: %s", testsStatus))
	}
	buf.WriteString(strings.Join(parts, "  "))
	buf.WriteByte('\n')
}

func renderLoopDashActivitySection(buf *strings.Builder, cs *iostreams.ColorScheme, activity []activityEntry, width int) {
	// Divider
	divLabel := " Activity "
	divFill := width - text.CountVisibleWidth(divLabel) - 4
	if divFill < 3 {
		divFill = 3
	}
	buf.WriteString("  ")
	buf.WriteString(cs.Muted("───" + divLabel + strings.Repeat("─", divFill)))
	buf.WriteByte('\n')

	if len(activity) == 0 {
		buf.WriteString(cs.Muted("  Waiting for first iteration..."))
		buf.WriteByte('\n')
		return
	}

	// Render newest first
	for i := len(activity) - 1; i >= 0; i-- {
		entry := activity[i]
		renderActivityEntry(buf, cs, entry, width)
	}
}

func renderActivityEntry(buf *strings.Builder, cs *iostreams.ColorScheme, entry activityEntry, width int) {
	if entry.running {
		fmt.Fprintf(buf, "  %s [Loop %d] Running...\n",
			cs.Muted("●"), entry.iteration)
		// Render streaming output lines under running entry
		for _, line := range entry.outputLines {
			maxWidth := width - 8 // "    ⎿ " prefix
			if maxWidth < 10 {
				maxWidth = 10
			}
			truncated := text.Truncate(line, maxWidth)
			fmt.Fprintf(buf, "    %s %s\n",
				cs.Muted("⎿"), cs.Muted(truncated))
		}
		return
	}

	icon := cs.Green("✓")
	if entry.isError {
		icon = cs.Error("✗")
	}

	var parts []string
	if entry.tasks > 0 || entry.files > 0 {
		parts = append(parts, fmt.Sprintf("%d tasks, %d files", entry.tasks, entry.files))
	}
	if entry.costUSD > 0 {
		parts = append(parts, formatCostUSD(entry.costUSD))
	}

	detail := ""
	if len(parts) > 0 {
		detail = " — " + strings.Join(parts, ", ")
	}

	durStr := ""
	if entry.duration > 0 {
		durStr = fmt.Sprintf(" (%s)", formatElapsed(entry.duration))
	}

	fmt.Fprintf(buf, "  %s [Loop %d] %s%s%s\n",
		icon, entry.iteration, entry.status, detail, durStr)
}

func formatStatusText(cs *iostreams.ColorScheme, status string) string {
	switch status {
	case "COMPLETE":
		return cs.Success(status)
	case "BLOCKED":
		return cs.Error(status)
	case "IN_PROGRESS":
		return cs.Warning(status)
	case "":
		return cs.Muted("PENDING")
	default:
		return status
	}
}

func formatCostUSD(cost float64) string {
	if cost < 0.01 {
		return fmt.Sprintf("$%.4f", cost)
	}
	return fmt.Sprintf("$%.2f", cost)
}

func formatTokenCount(tokens int) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d.Seconds())
	switch {
	case secs < 60:
		return fmt.Sprintf("%ds", secs)
	case secs < 3600:
		m := secs / 60
		s := secs % 60
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		h := secs / 3600
		m := (secs % 3600) / 60
		return fmt.Sprintf("%dh %dm", h, m)
	}
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunLoopDashboard runs the loop dashboard display, consuming events from ch
// until the channel is closed. Returns when the BubbleTea program exits.
func RunLoopDashboard(ios *iostreams.IOStreams, cfg LoopDashboardConfig, ch <-chan LoopDashEvent) LoopDashboardResult {
	renderer := newLoopDashRenderer(ios, cfg)

	// Bridge typed channel to generic any channel
	anyCh := make(chan any, 16)
	go func() {
		defer close(anyCh)
		for ev := range ch {
			anyCh <- ev
		}
	}()

	result := tui.RunDashboard(ios, renderer, tui.DashboardConfig{
		HelpText: "q detach  ctrl+c stop",
	}, anyCh)

	return LoopDashboardResult{
		Err:         result.Err,
		Detached:    result.Detached,
		Interrupted: result.Interrupted,
	}
}
