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
	ta.SetWidth(76)
	ta.ShowLineNumbers = false
	ta.CharLimit = 0 // no limit

	// Size to content — min 3 lines, grows with content.
	lines := strings.Count(value, "\n") + 1
	if lines < 3 {
		lines = 3
	}
	if lines > 20 {
		lines = 20
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
		m.ta.SetWidth(msg.Width - 6)
		// Let height follow content, capped by terminal.
		maxH := msg.Height - 8
		if maxH < 3 {
			maxH = 3
		}
		lines := strings.Count(m.ta.Value(), "\n") + 2
		if lines < 3 {
			lines = 3
		}
		if lines > maxH {
			lines = maxH
		}
		m.ta.SetHeight(lines)
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

	b.WriteString("  ")
	b.WriteString(m.ta.View())
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
