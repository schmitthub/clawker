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
	ta.SetHeight(15)
	ta.ShowLineNumbers = true

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
		h := msg.Height - 8
		if h < 5 {
			h = 5
		}
		m.ta.SetHeight(h)
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
