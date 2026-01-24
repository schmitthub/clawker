package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/schmitthub/clawker/internal/tui"
)

// Model is the Ralph TUI dashboard model.
type Model struct {
	width    int
	height   int
	project  string
	quitting bool
	err      error
}

// NewModel creates a new Ralph TUI model.
func NewModel(project string) Model {
	return Model{
		project: project,
	}
}

// Init initializes the model.
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if tui.IsQuit(msg) {
			m.quitting = true
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.width = max(msg.Width, 40)   // Minimum usable width
		m.height = max(msg.Height, 10) // Minimum usable height

	case errMsg:
		m.err = msg.err
	}

	return m, nil
}

// View renders the TUI.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder

	// Header
	header := tui.TitleStyle.Render("RALPH DASHBOARD")
	b.WriteString(header)
	b.WriteString("\n\n")

	// Project info
	projectLine := fmt.Sprintf("Project: %s", m.project)
	b.WriteString(tui.MutedStyle.Render(projectLine))
	b.WriteString("\n\n")

	// Error display
	if m.err != nil {
		b.WriteString(tui.ErrorStyle.Render(fmt.Sprintf("Error: %v", m.err)))
		b.WriteString("\n\n")
	}

	// Placeholder for agent list (future phases)
	placeholder := lipgloss.NewStyle().
		Foreground(tui.ColorMuted).
		Italic(true).
		Render("No agents discovered yet. Agent list will appear here.")
	b.WriteString(placeholder)
	b.WriteString("\n\n")

	// Help text
	help := tui.MutedStyle.Render("Press 'q' to quit")
	b.WriteString(help)

	return b.String()
}
