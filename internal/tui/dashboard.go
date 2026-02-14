package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

// DashboardRenderer controls the content of a channel-driven dashboard.
// Consumer packages implement this interface with domain-specific logic.
type DashboardRenderer interface {
	// ProcessEvent handles a domain event received from the channel.
	ProcessEvent(ev any)
	// View renders the dashboard content for the given terminal width.
	// Should NOT include the help line — the framework handles that.
	View(cs *iostreams.ColorScheme, width int) string
}

// DashboardConfig configures the generic dashboard.
type DashboardConfig struct {
	HelpText string // e.g., "q detach  ctrl+c stop"
}

// DashboardResult is returned when the dashboard exits.
type DashboardResult struct {
	Err         error // display error only
	Detached    bool  // user pressed q/Esc
	Interrupted bool  // user pressed Ctrl+C
}

// ---------------------------------------------------------------------------
// BubbleTea messages
// ---------------------------------------------------------------------------

type dashEventMsg struct{ ev any }

type dashChannelClosedMsg struct{}

func waitForDashEvent(ch <-chan any) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return dashChannelClosedMsg{}
		}
		return dashEventMsg{ev}
	}
}

// ---------------------------------------------------------------------------
// BubbleTea model
// ---------------------------------------------------------------------------

type dashboardModel struct {
	ios      *iostreams.IOStreams
	cs       *iostreams.ColorScheme
	renderer DashboardRenderer
	cfg      DashboardConfig
	eventCh  <-chan any

	// Terminal state
	finished    bool
	detached    bool
	interrupted bool
	width       int

	// High-water mark for stable frame height (pointer for View value receiver)
	highWater *int
}

func newDashboardModel(ios *iostreams.IOStreams, renderer DashboardRenderer, cfg DashboardConfig, ch <-chan any) dashboardModel {
	return dashboardModel{
		ios:       ios,
		cs:        ios.ColorScheme(),
		renderer:  renderer,
		cfg:       cfg,
		eventCh:   ch,
		width:     ios.TerminalWidth(),
		highWater: new(int),
	}
}

func (m dashboardModel) Init() tea.Cmd {
	return waitForDashEvent(m.eventCh)
}

func (m dashboardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case msg.Type == tea.KeyCtrlC:
			m.interrupted = true
			m.finished = true
			return m, tea.Quit
		case msg.Type == tea.KeyRunes && string(msg.Runes) == "q",
			msg.Type == tea.KeyEsc:
			m.detached = true
			m.finished = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		return m, nil

	case dashEventMsg:
		m.renderer.ProcessEvent(msg.ev)
		return m, waitForDashEvent(m.eventCh)

	case dashChannelClosedMsg:
		m.finished = true
		return m, tea.Quit
	}

	return m, nil
}

func (m dashboardModel) View() string {
	cs := m.cs
	width := m.width
	if width < 40 {
		width = 40
	}

	var buf strings.Builder

	// Renderer content
	content := m.renderer.View(cs, width)
	buf.WriteString(content)

	// Count content lines
	lines := strings.Count(content, "\n")
	if len(content) > 0 && content[len(content)-1] != '\n' {
		lines++
	}

	// Help line
	if m.cfg.HelpText != "" {
		helpLine := cs.Muted("  " + m.cfg.HelpText)
		buf.WriteString(helpLine)
		buf.WriteByte('\n')
		lines++
	}

	// Pad to high-water mark
	*m.highWater = max(*m.highWater, lines)
	for range *m.highWater - lines {
		buf.WriteByte('\n')
	}

	return buf.String()
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

// RunDashboard runs a generic channel-driven dashboard.
// Events are read from ch and dispatched to renderer.ProcessEvent().
// Returns when the channel is closed or the user presses q/Esc/Ctrl+C.
func RunDashboard(ios *iostreams.IOStreams, renderer DashboardRenderer, cfg DashboardConfig, ch <-chan any) DashboardResult {
	model := newDashboardModel(ios, renderer, cfg, ch)
	finalModel, err := RunProgram(ios, model)
	if err != nil {
		return DashboardResult{Err: err}
	}

	m, ok := finalModel.(dashboardModel)
	if !ok {
		return DashboardResult{Err: err}
	}

	if m.detached {
		return DashboardResult{Detached: true}
	}
	if m.interrupted {
		return DashboardResult{Interrupted: true}
	}

	return DashboardResult{}
}
