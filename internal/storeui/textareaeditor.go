package storeui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// textareaEditorModel provides multiline text editing for fields like post_init.
type textareaEditorModel struct {
	label     string
	ta        textarea.Model
	confirmed bool
	cancelled bool
}

func newTextareaEditor(label string, value string) textareaEditorModel {
	ta := textarea.New()
	ta.SetValue(value)
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0

	// Full width, no artificial constraint — resize handler sets actual width.
	ta.SetWidth(200)

	// Height: content + 2 breathing room, min 5, expand up to 30 before scrolling.
	lines := strings.Count(value, "\n") + 3
	if lines < 5 {
		lines = 5
	}
	if lines > 30 {
		lines = 30
	}
	ta.SetHeight(lines)

	return textareaEditorModel{
		label: label,
		ta:    ta,
	}
}

func (m textareaEditorModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m textareaEditorModel) Update(msg tea.Msg) (textareaEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Full width minus indent.
		m.ta.SetWidth(msg.Width - 4)
		// Use most of the terminal height, leave room for header + help bar.
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
		case msg.String() == "ctrl+c":
			m.cancelled = true
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.ta, cmd = m.ta.Update(msg)
	return m, cmd
}

func (m textareaEditorModel) View() string {
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

	// Indent every line of the textarea view.
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
	b.WriteString(helpKeyStyle.Render("ctrl+c"))
	b.WriteString(helpDescStyle.Render(" cancel"))

	return b.String()
}

func (m textareaEditorModel) Value() string        { return m.ta.Value() }
func (m textareaEditorModel) IsConfirmed() bool     { return m.confirmed }
func (m textareaEditorModel) IsCancelled() bool     { return m.cancelled }
