package storeui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
	"github.com/schmitthub/clawker/internal/tui"
)

// listEditorState tracks what the user is doing in the list editor.
type listEditorState int

const (
	listBrowsing listEditorState = iota
	listEditing                          // editing an existing item inline
	listAdding                           // adding a new item at the bottom
)

// listEditorModel lets the user manage a []string field by navigating,
// editing, deleting, and adding individual items.
type listEditorModel struct {
	label     string
	items     []string
	cursor    int
	state     listEditorState
	input     textinput.Model
	confirmed bool // user pressed Enter to accept the list
	cancelled bool // user pressed Esc to discard
	width     int
	height    int
}

func newListEditor(label string, value string) listEditorModel {
	var items []string
	if value != "" {
		for _, s := range strings.Split(value, ",") {
			trimmed := strings.TrimSpace(s)
			if trimmed != "" {
				items = append(items, trimmed)
			}
		}
	}

	ti := textinput.New()
	ti.Focus()
	ti.Width = 60

	return listEditorModel{
		label: label,
		items: items,
		state: listBrowsing,
		input: ti,
		width: 80,
	}
}

func (m listEditorModel) Init() tea.Cmd {
	return nil
}

func (m listEditorModel) Update(msg tea.Msg) (listEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.input.Width = msg.Width - 8
		return m, nil
	}

	switch m.state {
	case listBrowsing:
		return m.updateBrowsing(msg)
	case listEditing, listAdding:
		return m.updateEditing(msg)
	}
	return m, nil
}

func (m listEditorModel) updateBrowsing(msg tea.Msg) (listEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case tui.IsEnter(msg):
			// Accept the list and return to the field browser.
			m.confirmed = true
			return m, nil

		case tui.IsEscape(msg):
			m.cancelled = true
			return m, nil

		case tui.IsUp(msg):
			if m.cursor > 0 {
				m.cursor--
			}

		case tui.IsDown(msg):
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}

		case msg.String() == "e":
			if len(m.items) > 0 {
				m.state = listEditing
				m.input.SetValue(m.items[m.cursor])
				m.input.CursorEnd()
				return m, textinput.Blink
			}

		case msg.String() == "a":
			m.state = listAdding
			m.input.SetValue("")
			return m, textinput.Blink

		case msg.String() == "d", msg.String() == "backspace":
			if len(m.items) > 0 {
				m.items = append(m.items[:m.cursor], m.items[m.cursor+1:]...)
				if m.cursor >= len(m.items) && m.cursor > 0 {
					m.cursor--
				}
			}
		}
	}
	return m, nil
}

func (m listEditorModel) updateEditing(msg tea.Msg) (listEditorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case tui.IsEnter(msg):
			val := strings.TrimSpace(m.input.Value())
			if val != "" {
				if m.state == listAdding {
					m.items = append(m.items, val)
					m.cursor = len(m.items) - 1
				} else {
					m.items[m.cursor] = val
				}
			}
			m.state = listBrowsing
			return m, nil

		case tui.IsEscape(msg):
			m.state = listBrowsing
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m listEditorModel) View() string {
	promptStyle := iostreams.PanelTitleStyle
	selectedStyle := iostreams.ListItemSelectedStyle
	mutedStyle := iostreams.MutedStyle
	helpKeyStyle := iostreams.HelpKeyStyle
	helpDescStyle := iostreams.HelpDescStyle

	var b strings.Builder

	// Header
	b.WriteString("  ")
	b.WriteString(promptStyle.Render(m.label))
	b.WriteString("  ")
	b.WriteString(mutedStyle.Render("(list editor)"))
	b.WriteString("\n\n")

	if len(m.items) == 0 && m.state == listBrowsing {
		b.WriteString("    ")
		b.WriteString(mutedStyle.Render("(empty list)"))
		b.WriteString("\n")
	}

	for i, item := range m.items {
		selected := i == m.cursor

		if m.state == listEditing && selected {
			// Show inline text input for the item being edited.
			b.WriteString("  > ")
			b.WriteString(m.input.View())
			b.WriteString("\n")
			continue
		}

		label := text.PadRight(item, 40)
		if selected {
			b.WriteString("  > ")
			b.WriteString(selectedStyle.Render(label))
		} else {
			b.WriteString("    ")
			b.WriteString(label)
		}
		b.WriteString("\n")
	}

	if m.state == listAdding {
		b.WriteString("  + ")
		b.WriteString(m.input.View())
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString("\n  ")
	switch m.state {
	case listBrowsing:
		b.WriteString(helpKeyStyle.Render("a"))
		b.WriteString(helpDescStyle.Render(" add"))
		b.WriteString("  ")
		if len(m.items) > 0 {
			b.WriteString(helpKeyStyle.Render("e"))
			b.WriteString(helpDescStyle.Render(" edit"))
			b.WriteString("  ")
			b.WriteString(helpKeyStyle.Render("d"))
			b.WriteString(helpDescStyle.Render(" delete"))
			b.WriteString("  ")
		}
		b.WriteString(helpKeyStyle.Render("enter"))
		b.WriteString(helpDescStyle.Render(" done"))
		b.WriteString("  ")
		b.WriteString(helpKeyStyle.Render("esc"))
		b.WriteString(helpDescStyle.Render(" cancel"))
	case listEditing, listAdding:
		b.WriteString(helpKeyStyle.Render("enter"))
		b.WriteString(helpDescStyle.Render(" confirm"))
		b.WriteString("  ")
		b.WriteString(helpKeyStyle.Render("esc"))
		b.WriteString(helpDescStyle.Render(" cancel"))
	}

	return b.String()
}

// Value returns the current items as a comma-separated string.
func (m listEditorModel) Value() string {
	return strings.Join(m.items, ", ")
}

func (m listEditorModel) IsConfirmed() bool { return m.confirmed }
func (m listEditorModel) IsCancelled() bool { return m.cancelled }
