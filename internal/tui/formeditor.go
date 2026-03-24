package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// FormEditorModel presents a vertical multi-field form for editing a
// single struct's fields. Each field is a labeled textarea that supports
// multiline YAML values.
//
// Navigation: Tab/Shift+Tab between fields. Ctrl+S saves, Esc cancels.
//
// Used internally by [ItemListEditorModel] to edit individual items.
// Not a standalone [FieldEditor] — the item list owns the lifecycle.
//
// This is a reusable building block for domain adapters. Currently unused —
// available for future domain customization via [storeui.Override] Editor factories.
type FormEditorModel struct {
	fields    []formField
	cursor    int
	confirmed bool
	cancelled bool
	width     int
	height    int
}

type formField struct {
	key   string // YAML key
	label string // Display label
	input textarea.Model
}

// formFieldHeight is the default textarea height per field.
// Compact enough for single-line values, expands visually for multiline.
const formFieldHeight = 2

// NewFormEditor creates a form from field definitions and initial values.
// values maps YAML keys to their current string values.
func NewFormEditor(fields []StructFieldDef, values map[string]string) FormEditorModel {
	ff := make([]formField, len(fields))
	for i, def := range fields {
		ta := textarea.New()
		ta.SetWidth(60)
		ta.SetHeight(formFieldHeight)
		ta.ShowLineNumbers = false
		ta.SetValue(values[def.Key])
		ta.CharLimit = 0 // no limit
		if i == 0 {
			ta.Focus()
		} else {
			ta.Blur()
		}
		ff[i] = formField{key: def.Key, label: def.Label, input: ta}
	}
	return FormEditorModel{fields: ff, width: 80}
}

func (m FormEditorModel) Init() tea.Cmd {
	if len(m.fields) > 0 {
		return textarea.Blink
	}
	return nil
}

func (m FormEditorModel) Update(msg tea.Msg) (FormEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputWidth := msg.Width - 20
		if inputWidth < 1 {
			inputWidth = 1
		}
		for i := range m.fields {
			m.fields[i].input.SetWidth(inputWidth)
		}
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+s":
			m.confirmed = true
			return m, nil

		case IsEscape(msg), msg.String() == "ctrl+c":
			m.cancelled = true
			return m, nil

		case IsTab(msg):
			// Tab moves to next field.
			if m.cursor < len(m.fields)-1 {
				m.fields[m.cursor].input.Blur()
				m.cursor++
				m.fields[m.cursor].input.Focus()
				return m, textarea.Blink
			}
			// Tab on last field wraps to first.
			m.fields[m.cursor].input.Blur()
			m.cursor = 0
			m.fields[m.cursor].input.Focus()
			return m, textarea.Blink

		case msg.String() == "shift+tab":
			// Shift+Tab moves to previous field.
			if m.cursor > 0 {
				m.fields[m.cursor].input.Blur()
				m.cursor--
				m.fields[m.cursor].input.Focus()
				return m, textarea.Blink
			}
			// Shift+Tab on first field wraps to last.
			m.fields[m.cursor].input.Blur()
			m.cursor = len(m.fields) - 1
			m.fields[m.cursor].input.Focus()
			return m, textarea.Blink
		}
	}

	// Delegate to active textarea.
	if m.cursor >= 0 && m.cursor < len(m.fields) {
		var cmd tea.Cmd
		m.fields[m.cursor].input, cmd = m.fields[m.cursor].input.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m FormEditorModel) View() string {
	mutedStyle := iostreams.MutedStyle
	selectedStyle := iostreams.ListItemSelectedStyle
	helpKeyStyle := iostreams.HelpKeyStyle
	helpDescStyle := iostreams.HelpDescStyle

	var b strings.Builder

	// Find max label width for alignment.
	maxLabelLen := 0
	for _, f := range m.fields {
		if len(f.label) > maxLabelLen {
			maxLabelLen = len(f.label)
		}
	}

	for i, f := range m.fields {
		label := text.PadRight(f.label+":", maxLabelLen+1)
		if i == m.cursor {
			b.WriteString("  > ")
			b.WriteString(selectedStyle.Render(label))
		} else {
			b.WriteString("    ")
			b.WriteString(mutedStyle.Render(label))
		}
		b.WriteString("\n")
		// Indent the textarea content.
		for _, line := range strings.Split(f.input.View(), "\n") {
			b.WriteString("      ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	// Help
	b.WriteString("\n  ")
	b.WriteString(helpKeyStyle.Render("tab"))
	b.WriteString(helpDescStyle.Render(" next field"))
	b.WriteString("  ")
	b.WriteString(helpKeyStyle.Render("shift+tab"))
	b.WriteString(helpDescStyle.Render(" prev field"))
	b.WriteString("  ")
	b.WriteString(helpKeyStyle.Render("ctrl+s"))
	b.WriteString(helpDescStyle.Render(" save"))
	b.WriteString("  ")
	b.WriteString(helpKeyStyle.Render("esc"))
	b.WriteString(helpDescStyle.Render(" cancel"))

	return b.String()
}

// Values returns the edited field values as a map of key → value.
func (m FormEditorModel) Values() map[string]string {
	out := make(map[string]string, len(m.fields))
	for _, f := range m.fields {
		out[f.key] = f.input.Value()
	}
	return out
}

// IsConfirmed returns true if the user accepted the form.
func (m FormEditorModel) IsConfirmed() bool { return m.confirmed }

// IsCancelled returns true if the user cancelled the form.
func (m FormEditorModel) IsCancelled() bool { return m.cancelled }
