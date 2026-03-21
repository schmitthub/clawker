package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// TextareaEditorModel provides multiline text editing with ctrl+s save and esc cancel.
type TextareaEditorModel struct {
	label     string
	ta        textarea.Model
	confirmed bool
	cancelled bool
}

// NewTextareaEditor creates a multiline text editor with the given label and initial value.
func NewTextareaEditor(label string, value string) TextareaEditorModel {
	ta := textarea.New()
	ta.SetValue(value)
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0

	// Full width — resize handler sets actual width.
	ta.SetWidth(200)

	// Height: content + breathing room, min 5, cap at 30.
	lines := strings.Count(value, "\n") + 3
	if lines < 5 {
		lines = 5
	}
	if lines > 30 {
		lines = 30
	}
	ta.SetHeight(lines)

	return TextareaEditorModel{
		label: label,
		ta:    ta,
	}
}

// Init returns the textarea blink command.
func (m TextareaEditorModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update handles key messages: ctrl+s saves, esc cancels, everything else delegates.
func (m TextareaEditorModel) Update(msg tea.Msg) (TextareaEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.ta.SetWidth(msg.Width - 4)
		maxH := msg.Height - 6
		if maxH < 5 {
			maxH = 5
		}
		m.ta.SetHeight(maxH)
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+s":
			m.confirmed = true
			return m, nil
		case msg.String() == "esc":
			m.cancelled = true
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

// View renders the textarea editor with label, content area, and help bar.
func (m TextareaEditorModel) View() string {
	promptStyle := iostreams.PanelTitleStyle
	mutedStyle := iostreams.MutedStyle
	helpKeyStyle := iostreams.HelpKeyStyle
	helpDescStyle := iostreams.HelpDescStyle

	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(promptStyle.Render(m.label))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("(multiline editor)"))
	b.WriteString("\n\n")

	for i, line := range strings.Split(m.ta.View(), "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(line)
	}
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(helpKeyStyle.Render("ctrl+s"))
	b.WriteString(helpDescStyle.Render(" save"))
	b.WriteString("  ")
	b.WriteString(helpKeyStyle.Render("esc"))
	b.WriteString(helpDescStyle.Render(" cancel"))

	return b.String()
}

// Value returns the current textarea content.
func (m TextareaEditorModel) Value() string { return m.ta.Value() }

// IsConfirmed returns true if the user saved.
func (m TextareaEditorModel) IsConfirmed() bool { return m.confirmed }

// IsCancelled returns true if the user cancelled.
func (m TextareaEditorModel) IsCancelled() bool { return m.cancelled }
