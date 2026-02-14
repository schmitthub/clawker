package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
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

	// LoopDashEventOutput is sent with raw output chunks (for future verbose feed).
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

	// Output (for future verbose feed)
	OutputChunk string
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
// BubbleTea messages
// ---------------------------------------------------------------------------

type loopDashEventMsg LoopDashEvent

type loopDashChannelClosedMsg struct{}

func waitForLoopEvent(ch <-chan LoopDashEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return loopDashChannelClosedMsg{}
		}
		return loopDashEventMsg(ev)
	}
}

// ---------------------------------------------------------------------------
// Activity log entry
// ---------------------------------------------------------------------------

type activityEntry struct {
	iteration int
	status    string // "Running", "IN_PROGRESS", "COMPLETE", "BLOCKED", or error text
	tasks     int
	files     int
	costUSD   float64
	tokens    int
	turns     int
	duration  time.Duration
	isError   bool
	running   bool
}

const maxActivityEntries = 10

// ---------------------------------------------------------------------------
// BubbleTea model
// ---------------------------------------------------------------------------

type loopDashboardModel struct {
	ios *iostreams.IOStreams
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

	// Terminal
	finished    bool
	detached    bool // user pressed q/Esc — TUI exits, loop continues
	interrupted bool // user pressed Ctrl+C — stop the loop
	width       int

	// High-water mark for stable frame height (pointer for View value receiver)
	highWater *int

	eventCh <-chan LoopDashEvent
}

func newLoopDashboardModel(ios *iostreams.IOStreams, cfg LoopDashboardConfig, eventCh <-chan LoopDashEvent) loopDashboardModel {
	return loopDashboardModel{
		ios:              ios,
		cs:               ios.ColorScheme(),
		cfg:              cfg,
		agentName:        cfg.AgentName,
		project:          cfg.Project,
		maxIter:          cfg.MaxLoops,
		startTime:        time.Now(),
		circuitThreshold: 3, // default, updated by events
		highWater:        new(int),
		width:            ios.TerminalWidth(),
		eventCh:          eventCh,
	}
}

func (m loopDashboardModel) Init() tea.Cmd {
	return waitForLoopEvent(m.eventCh)
}

func (m loopDashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC:
			// Ctrl+C = interrupt (stop the loop)
			m.interrupted = true
			m.finished = true
			return m, tea.Quit
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "q",
			msg.Type == tea.KeyEsc:
			// q/Esc = detach (exit TUI, loop continues with minimal output)
			m.detached = true
			m.finished = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case loopDashEventMsg:
		m.processEvent(LoopDashEvent(msg))
		return m, waitForLoopEvent(m.eventCh)

	case loopDashChannelClosedMsg:
		m.finished = true
		return m, tea.Quit
	}

	return m, nil
}

func (m *loopDashboardModel) processEvent(ev LoopDashEvent) {
	switch ev.Kind {
	case LoopDashEventStart:
		m.agentName = ev.AgentName
		m.project = ev.Project
		m.maxIter = ev.MaxIterations

	case LoopDashEventIterStart:
		m.currentIter = ev.Iteration
		m.iterStartTime = time.Now()
		// Add "Running..." entry
		m.addActivity(activityEntry{
			iteration: ev.Iteration,
			status:    "Running",
			running:   true,
		})

	case LoopDashEventIterEnd:
		m.currentIter = ev.Iteration
		m.statusText = ev.StatusText
		m.totalTasks = ev.TotalTasks
		m.totalFiles = ev.TotalFiles
		m.testsStatus = ev.TestsStatus
		m.circuitProgress = ev.CircuitProgress
		m.circuitThreshold = ev.CircuitThreshold
		m.circuitTripped = ev.CircuitTripped
		m.rateRemaining = ev.RateRemaining
		m.rateLimit = ev.RateLimit
		m.totalCostUSD += ev.IterCostUSD
		m.totalTokens += ev.IterTokens
		m.totalTurns += ev.IterTurns
		// Update the running entry to completed
		m.updateRunningActivity(activityEntry{
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

	case LoopDashEventRateLimit:
		m.rateRemaining = ev.RateRemaining
		m.rateLimit = ev.RateLimit

	case LoopDashEventComplete:
		m.exitReason = ev.ExitReason
		m.exitError = ev.Error
		m.totalTasks = ev.TotalTasks
		m.totalFiles = ev.TotalFiles
	}
}

func (m *loopDashboardModel) addActivity(entry activityEntry) {
	if len(m.activity) >= maxActivityEntries {
		m.activity = m.activity[1:]
	}
	m.activity = append(m.activity, entry)
}

func (m *loopDashboardModel) updateRunningActivity(entry activityEntry) {
	// Find and update the latest running entry for this iteration
	for i := len(m.activity) - 1; i >= 0; i-- {
		if m.activity[i].running && m.activity[i].iteration == entry.iteration {
			m.activity[i] = entry
			return
		}
	}
	// Not found (shouldn't happen), just add it
	m.addActivity(entry)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m loopDashboardModel) View() string {
	cs := m.cs
	width := m.width
	if width < 40 {
		width = 40
	}

	var buf strings.Builder
	lines := 0

	// Header bar
	renderLoopDashHeader(&buf, cs, m.agentName, width)
	lines++
	buf.WriteByte('\n')
	lines++

	// Info line: Agent / ProjectCfg / Elapsed
	elapsed := time.Since(m.startTime)
	infoLine := fmt.Sprintf("  Agent: %s    ProjectCfg: %s    Elapsed: %s",
		m.agentName, m.project, formatElapsed(elapsed))
	buf.WriteString(infoLine)
	buf.WriteByte('\n')
	lines++

	// Counters line: Iteration / Circuit / Rate
	iterStr := fmt.Sprintf("%d/%d", m.currentIter, m.maxIter)
	circuitStr := fmt.Sprintf("%d/%d", m.circuitProgress, m.circuitThreshold)
	if m.circuitTripped {
		circuitStr = cs.Error("TRIPPED")
	}
	rateStr := ""
	if m.rateLimit > 0 {
		rateStr = fmt.Sprintf("      Rate: %d/%d", m.rateRemaining, m.rateLimit)
	}
	countersLine := fmt.Sprintf("  Iteration: %s             Circuit: %s%s",
		iterStr, circuitStr, rateStr)
	buf.WriteString(countersLine)
	buf.WriteByte('\n')
	lines++

	// Cost/token line (only shown after first iteration completes)
	if m.totalTokens > 0 || m.totalCostUSD > 0 {
		costLine := fmt.Sprintf("  Cost: %s  Tokens: %s  Turns: %d",
			formatCostUSD(m.totalCostUSD), formatTokenCount(m.totalTokens), m.totalTurns)
		buf.WriteString(cs.Muted(costLine))
		buf.WriteByte('\n')
		lines++
	}

	buf.WriteByte('\n')
	lines++

	// Status section
	renderLoopDashStatusSection(&buf, cs, m.statusText, m.totalTasks, m.totalFiles, m.testsStatus, width)
	lines += 3 // divider + status line + blank
	buf.WriteByte('\n')
	lines++

	// Activity section
	activityLines := renderLoopDashActivitySection(&buf, cs, m.activity, width)
	lines += 1 + activityLines // divider + entries

	buf.WriteByte('\n')
	lines++

	// Help
	helpLine := cs.Muted("  q detach  ctrl+c stop")
	buf.WriteString(helpLine)
	buf.WriteByte('\n')
	lines++

	// Pad to high-water mark
	if lines > *m.highWater {
		*m.highWater = lines
	}
	for range *m.highWater - lines {
		buf.WriteByte('\n')
	}

	return buf.String()
}

// ---------------------------------------------------------------------------
// Render helpers
// ---------------------------------------------------------------------------

func renderLoopDashHeader(buf *strings.Builder, cs *iostreams.ColorScheme, agentName string, width int) {
	title := fmt.Sprintf("  ━━ Loop Dashboard ")
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
	divFill := width - text.CountVisibleWidth(divLabel) - 4 // "  ─── " prefix
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

func renderLoopDashActivitySection(buf *strings.Builder, cs *iostreams.ColorScheme, activity []activityEntry, width int) int {
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
		return 2
	}

	lines := 0
	// Render newest first
	for i := len(activity) - 1; i >= 0; i-- {
		entry := activity[i]
		renderActivityEntry(buf, cs, entry)
		lines++
	}
	return lines + 1 // +1 for divider
}

func renderActivityEntry(buf *strings.Builder, cs *iostreams.ColorScheme, entry activityEntry) {
	if entry.running {
		buf.WriteString(fmt.Sprintf("  %s [Loop %d] Running...\n",
			cs.Muted("●"), entry.iteration))
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
// Entry point — package-level (called by TUI factory method)
// ---------------------------------------------------------------------------

// RunLoopDashboard runs the loop dashboard display, consuming events from ch
// until the channel is closed. Returns when the BubbleTea program exits.
func RunLoopDashboard(ios *iostreams.IOStreams, cfg LoopDashboardConfig, ch <-chan LoopDashEvent) LoopDashboardResult {
	model := newLoopDashboardModel(ios, cfg, ch)
	finalModel, err := RunProgram(ios, model)
	if err != nil {
		return LoopDashboardResult{Err: fmt.Errorf("display error: %w", err)}
	}

	m, ok := finalModel.(loopDashboardModel)
	if !ok {
		return LoopDashboardResult{Err: fmt.Errorf("unexpected model type")}
	}

	if m.detached {
		return LoopDashboardResult{Detached: true}
	}
	if m.interrupted {
		return LoopDashboardResult{Interrupted: true}
	}

	return LoopDashboardResult{}
}
