package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// Compile-time check: ItemListEditorModel satisfies FieldEditor.
var _ FieldEditor = ItemListEditorModel{}

// ilState tracks what the user is doing in the item list editor.
type ilState int

const (
	ilBrowsing ilState = iota
	ilEditing          // editing an existing item via FormEditorModel
	ilAdding           // adding a new item via FormEditorModel
)

// ItemListEditorModel lets the user manage a list of structs by navigating,
// editing, deleting, and adding items. Each item is edited via a [FormEditorModel].
//
// Input: a label, a YAML-formatted string value, and field definitions.
// Output: Value() returns the edited YAML string.
//
// This is a reusable building block for domain adapters that want a structured
// list editor instead of the default YAML textarea. Wire it via the Editor factory
// on [storeui.Override]. Currently unused — available for future domain customization.
type ItemListEditorModel struct {
	label     string
	fieldDefs []StructFieldDef
	items     []map[string]string // each item is a map of key→value
	cursor    int
	state     ilState
	form      FormEditorModel
	confirmed bool
	cancelled bool
	width     int
	height    int
}

// NewItemListEditor creates an item list editor from a label, YAML value, and field defs.
func NewItemListEditor(label string, value string, fields []StructFieldDef) ItemListEditorModel {
	items := parseYAMLItems(value, fields)

	return ItemListEditorModel{
		label:     label,
		fieldDefs: fields,
		items:     items,
		state:     ilBrowsing,
		width:     80,
	}
}

// parseYAMLItems parses a YAML list-of-maps into []map[string]string.
// Handles both typed YAML and plain map representations.
//
// TODO: This silently returns nil on YAML unmarshal errors, which would make the
// editor display an empty list. If a user confirms (Enter), the empty value would
// overwrite existing data with no feedback. When wiring this editor into a domain
// adapter, this MUST be fixed: capture the parse error into an errMsg field (like
// KVEditorModel does at kveditor.go:77-83), surface it via Err(), and render it
// inline via renderValidationError(). The current Err() at line 312 is hardcoded
// to "" — it needs to return the captured parse error. See KVEditorModel for the
// correct pattern. Do NOT wire this editor without fixing this first.
func parseYAMLItems(value string, fields []StructFieldDef) []map[string]string {
	if value == "" {
		return nil
	}

	var raw []map[string]any
	if err := yaml.Unmarshal([]byte(value), &raw); err != nil {
		return nil
	}

	items := make([]map[string]string, len(raw))
	for i, entry := range raw {
		m := make(map[string]string, len(fields))
		for _, fd := range fields {
			if v, ok := entry[fd.Key]; ok {
				m[fd.Key] = fmt.Sprintf("%v", v)
			}
		}
		items[i] = m
	}
	return items
}

func (m ItemListEditorModel) Init() tea.Cmd { return nil }

func (m ItemListEditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	switch m.state {
	case ilBrowsing:
		return m.updateBrowsing(msg)
	case ilEditing, ilAdding:
		return m.updateForm(msg)
	}
	return m, nil
}

func (m ItemListEditorModel) updateBrowsing(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsEnter(msg):
			m.confirmed = true
			return m, nil

		case IsEscape(msg), msg.String() == "ctrl+c":
			m.cancelled = true
			return m, nil

		case IsUp(msg):
			if m.cursor > 0 {
				m.cursor--
			}

		case IsDown(msg):
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}

		case msg.String() == "e":
			if len(m.items) > 0 {
				m.state = ilEditing
				m.form = NewFormEditor(m.fieldDefs, m.items[m.cursor])
				return m, m.form.Init()
			}

		case msg.String() == "a":
			m.state = ilAdding
			m.form = NewFormEditor(m.fieldDefs, nil)
			return m, m.form.Init()

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

func (m ItemListEditorModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.form, cmd = m.form.Update(msg)

	if m.form.IsConfirmed() {
		vals := m.form.Values()
		if m.state == ilAdding {
			// Only add if at least one field has a value.
			hasValue := false
			for _, v := range vals {
				if v != "" {
					hasValue = true
					break
				}
			}
			if hasValue {
				m.items = append(m.items, vals)
				m.cursor = len(m.items) - 1
			}
		} else {
			m.items[m.cursor] = vals
		}
		m.state = ilBrowsing
		return m, nil
	}

	if m.form.IsCancelled() {
		m.state = ilBrowsing
		return m, nil
	}

	return m, cmd
}

func (m ItemListEditorModel) View() string {
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
	b.WriteString(mutedStyle.Render("(item list editor)"))
	b.WriteString("\n\n")

	// Show form when editing/adding.
	if m.state == ilEditing || m.state == ilAdding {
		if m.state == ilAdding {
			b.WriteString("  ")
			b.WriteString(mutedStyle.Render("New item:"))
			b.WriteString("\n")
		} else {
			b.WriteString("  ")
			b.WriteString(mutedStyle.Render(fmt.Sprintf("Editing item %d:", m.cursor+1)))
			b.WriteString("\n")
		}
		b.WriteString(m.form.View())
		return b.String()
	}

	// Browse mode: show item list.
	if len(m.items) == 0 {
		b.WriteString("    ")
		b.WriteString(mutedStyle.Render("(empty list)"))
		b.WriteString("\n")
	}

	for i, item := range m.items {
		selected := i == m.cursor
		summary := m.itemSummary(item, i)

		if selected {
			b.WriteString("  > ")
			b.WriteString(selectedStyle.Render(summary))
		} else {
			b.WriteString("    ")
			b.WriteString(summary)
		}
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString("\n  ")
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

	return b.String()
}

// itemSummary produces a one-line summary of an item for the browse list.
// Uses the first non-empty field value, or "(item N)".
func (m ItemListEditorModel) itemSummary(item map[string]string, idx int) string {
	maxWidth := m.width - 8
	if maxWidth < 20 {
		maxWidth = 20
	}

	// Try showing key=value pairs for non-empty fields.
	var parts []string
	for _, fd := range m.fieldDefs {
		if v := item[fd.Key]; v != "" {
			parts = append(parts, fd.Key+": "+v)
		}
	}
	if len(parts) > 0 {
		summary := strings.Join(parts, ", ")
		return text.Truncate(summary, maxWidth)
	}

	return fmt.Sprintf("(item %d)", idx+1)
}

// Value returns the current items as a YAML-formatted string.
func (m ItemListEditorModel) Value() string {
	if len(m.items) == 0 {
		return ""
	}

	// Convert []map[string]string to []map[string]any for clean YAML output.
	// Only include non-empty values to match omitempty behavior.
	out := make([]map[string]any, len(m.items))
	for i, item := range m.items {
		entry := make(map[string]any, len(m.fieldDefs))
		for _, fd := range m.fieldDefs {
			if v := item[fd.Key]; v != "" {
				entry[fd.Key] = v
			}
		}
		out[i] = entry
	}
	data, _ := yaml.Marshal(out)
	return strings.TrimSpace(string(data))
}

// IsConfirmed returns true if the user accepted the list.
func (m ItemListEditorModel) IsConfirmed() bool { return m.confirmed }

// IsCancelled returns true if the user cancelled editing.
func (m ItemListEditorModel) IsCancelled() bool { return m.cancelled }

// Err returns the current validation error message, or empty string if none.
// ItemListEditorModel does not support validation yet — this always returns "".
func (m ItemListEditorModel) Err() string { return "" }
