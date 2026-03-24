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
	validator func(string) error // optional external validator run on save
	errMsg    string             // validation error shown inline
}

// TextareaEditorOption is a functional option for configuring a TextareaEditorModel.
type TextareaEditorOption func(*TextareaEditorModel)

// WithTextareaValidator sets a validation function called when the user saves
// (Ctrl+S). If the function returns an error, the editor displays it
// and does not confirm.
func WithTextareaValidator(fn func(string) error) TextareaEditorOption {
	return func(m *TextareaEditorModel) {
		m.validator = fn
	}
}

// NewTextareaEditor creates a multiline text editor with the given label and initial value.
func NewTextareaEditor(label string, value string, opts ...TextareaEditorOption) TextareaEditorModel {
	ta := textarea.New()
	ta.SetValue(value)
	ta.Focus()
	ta.ShowLineNumbers = false
	ta.CharLimit = 0

	// Default width — resize handler sets actual width from terminal size.
	ta.SetWidth(80)

	// Height: content + breathing room, min 5, cap at 30.
	lines := strings.Count(value, "\n") + 3
	if lines < 5 {
		lines = 5
	}
	if lines > 30 {
		lines = 30
	}
	ta.SetHeight(lines)

	te := TextareaEditorModel{
		label: label,
		ta:    ta,
	}
	for _, opt := range opts {
		opt(&te)
	}
	return te
}

// Init returns the textarea blink command.
func (m TextareaEditorModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update handles key messages: ctrl+s saves, esc cancels, everything else delegates.
func (m TextareaEditorModel) Update(msg tea.Msg) (TextareaEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		w := msg.Width - 4
		if w < 1 {
			w = 1
		}
		m.ta.SetWidth(w)
		maxH := msg.Height - 6
		if maxH < 5 {
			maxH = 5
		}
		if maxH > 30 {
			maxH = 30
		}
		m.ta.SetHeight(maxH)
		return m, nil

	case tea.KeyMsg:
		// Any key clears the previous error so it doesn't linger.
		m.errMsg = ""

		switch {
		case msg.String() == "ctrl+s":
			// Run external validator before confirming.
			if m.validator != nil {
				if err := m.validator(m.ta.Value()); err != nil {
					m.errMsg = err.Error()
					return m, nil
				}
			}
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
	b.WriteString("\n")

	// Validation error
	if m.errMsg != "" {
		b.WriteString("\n  ")
		b.WriteString(renderValidationError(m.errMsg))
	}

	b.WriteString("\n  ")
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

// Err returns the current validation error message, or empty string if none.
func (m TextareaEditorModel) Err() string { return m.errMsg }
