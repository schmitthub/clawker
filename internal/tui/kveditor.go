package tui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"gopkg.in/yaml.v3"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// Compile-time check: KVEditorModel satisfies FieldEditor.
var _ FieldEditor = KVEditorModel{}

// kvEditorState tracks what the user is doing in the KV editor.
type kvEditorState int

const (
	kvBrowsing   kvEditorState = iota
	kvEditingKey               // editing an existing key inline
	kvEditingVal               // editing an existing value inline
	kvAddingKey                // adding a new pair — entering the key
	kvAddingVal                // adding a new pair — entering the value
)

type kvPair struct {
	Key   string
	Value string
}

// KVEditorModel lets the user manage a map[string]string by navigating,
// editing, deleting, and adding key-value pairs.
//
// Input: a label and a YAML-formatted string value (marshaled map[string]string).
// Output: Value() returns the edited YAML string.
//
// This is a reusable building block for domain adapters that want a structured
// map editor instead of the default YAML textarea. Wire it via the Editor factory
// on [storeui.Override]. Currently unused — available for future domain customization.
type KVEditorModel struct {
	label      string
	pairs      []kvPair
	cursor     int
	state      kvEditorState
	input      textinput.Model
	pendingKey string // key being added (held between kvAddingKey → kvAddingVal)
	confirmed  bool
	cancelled  bool
	width      int
	height     int
}

// NewKVEditor creates a KV editor from a label and YAML-formatted map string.
func NewKVEditor(label string, value string) KVEditorModel {
	var m map[string]string
	if value != "" {
		_ = yaml.Unmarshal([]byte(value), &m)
	}

	// Sort keys for stable display order.
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]kvPair, len(keys))
	for i, k := range keys {
		pairs[i] = kvPair{Key: k, Value: m[k]}
	}

	ti := textinput.New()
	ti.Focus()
	ti.Width = 60

	return KVEditorModel{
		label: label,
		pairs: pairs,
		state: kvBrowsing,
		input: ti,
		width: 80,
	}
}

func (m KVEditorModel) Init() tea.Cmd { return nil }

func (m KVEditorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		inputWidth := msg.Width - 8
		if inputWidth < 1 {
			inputWidth = 1
		}
		m.input.Width = inputWidth
		return m, nil
	}

	switch m.state {
	case kvBrowsing:
		return m.updateBrowsing(msg)
	default:
		return m.updateEditing(msg)
	}
}

func (m KVEditorModel) updateBrowsing(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			if m.cursor < len(m.pairs)-1 {
				m.cursor++
			}

		case msg.String() == "e":
			// Edit value of selected pair.
			if len(m.pairs) > 0 {
				m.state = kvEditingVal
				m.input.SetValue(m.pairs[m.cursor].Value)
				m.input.CursorEnd()
				return m, textinput.Blink
			}

		case msg.String() == "E":
			// Edit key of selected pair.
			if len(m.pairs) > 0 {
				m.state = kvEditingKey
				m.input.SetValue(m.pairs[m.cursor].Key)
				m.input.CursorEnd()
				return m, textinput.Blink
			}

		case msg.String() == "a":
			m.state = kvAddingKey
			m.pendingKey = ""
			m.input.SetValue("")
			m.input.Placeholder = "key"
			return m, textinput.Blink

		case msg.String() == "d", msg.String() == "backspace":
			if len(m.pairs) > 0 {
				m.pairs = append(m.pairs[:m.cursor], m.pairs[m.cursor+1:]...)
				if m.cursor >= len(m.pairs) && m.cursor > 0 {
					m.cursor--
				}
			}
		}
	}
	return m, nil
}

func (m KVEditorModel) updateEditing(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsEnter(msg):
			val := strings.TrimSpace(m.input.Value())
			switch m.state {
			case kvEditingKey:
				if val != "" {
					m.pairs[m.cursor].Key = val
				}
				m.state = kvBrowsing
			case kvEditingVal:
				m.pairs[m.cursor].Value = val
				m.state = kvBrowsing
			case kvAddingKey:
				if val == "" {
					m.state = kvBrowsing
					return m, nil
				}
				m.pendingKey = val
				m.state = kvAddingVal
				m.input.SetValue("")
				m.input.Placeholder = "value"
				return m, textinput.Blink
			case kvAddingVal:
				m.pairs = append(m.pairs, kvPair{Key: m.pendingKey, Value: val})
				m.cursor = len(m.pairs) - 1
				m.pendingKey = ""
				m.state = kvBrowsing
			}
			return m, nil

		case IsEscape(msg):
			m.pendingKey = ""
			m.state = kvBrowsing
			return m, nil
		}
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m KVEditorModel) View() string {
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
	b.WriteString(mutedStyle.Render("(key-value editor)"))
	b.WriteString("\n\n")

	if len(m.pairs) == 0 && m.state == kvBrowsing {
		b.WriteString("    ")
		b.WriteString(mutedStyle.Render("(empty)"))
		b.WriteString("\n")
	}

	// Find max key width for alignment.
	maxKeyLen := 0
	for _, p := range m.pairs {
		if len(p.Key) > maxKeyLen {
			maxKeyLen = len(p.Key)
		}
	}
	if maxKeyLen < 4 {
		maxKeyLen = 4
	}

	for i, p := range m.pairs {
		selected := i == m.cursor

		if selected && m.state == kvEditingKey {
			b.WriteString("  > ")
			b.WriteString(m.input.View())
			b.WriteString(mutedStyle.Render(" = " + p.Value))
			b.WriteString("\n")
			continue
		}

		if selected && m.state == kvEditingVal {
			b.WriteString("  > ")
			b.WriteString(text.PadRight(p.Key, maxKeyLen))
			b.WriteString(" = ")
			b.WriteString(m.input.View())
			b.WriteString("\n")
			continue
		}

		line := text.PadRight(p.Key, maxKeyLen) + " = " + p.Value
		if selected {
			b.WriteString("  > ")
			b.WriteString(selectedStyle.Render(line))
		} else {
			b.WriteString("    ")
			b.WriteString(line)
		}
		b.WriteString("\n")
	}

	// Adding state
	if m.state == kvAddingKey {
		b.WriteString("  + ")
		b.WriteString(m.input.View())
		b.WriteString(mutedStyle.Render(" = ..."))
		b.WriteString("\n")
	}
	if m.state == kvAddingVal {
		b.WriteString("  + ")
		b.WriteString(text.PadRight(m.pendingKey, maxKeyLen))
		b.WriteString(" = ")
		b.WriteString(m.input.View())
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString("\n  ")
	switch m.state {
	case kvBrowsing:
		b.WriteString(helpKeyStyle.Render("a"))
		b.WriteString(helpDescStyle.Render(" add"))
		b.WriteString("  ")
		if len(m.pairs) > 0 {
			b.WriteString(helpKeyStyle.Render("e"))
			b.WriteString(helpDescStyle.Render(" edit value"))
			b.WriteString("  ")
			b.WriteString(helpKeyStyle.Render("E"))
			b.WriteString(helpDescStyle.Render(" edit key"))
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
	default:
		b.WriteString(helpKeyStyle.Render("enter"))
		b.WriteString(helpDescStyle.Render(" confirm"))
		b.WriteString("  ")
		b.WriteString(helpKeyStyle.Render("esc"))
		b.WriteString(helpDescStyle.Render(" cancel"))
	}

	return b.String()
}

// Value returns the current pairs as a YAML-formatted string.
func (m KVEditorModel) Value() string {
	if len(m.pairs) == 0 {
		return ""
	}
	out := make(map[string]string, len(m.pairs))
	for _, p := range m.pairs {
		out[p.Key] = p.Value
	}
	data, _ := yaml.Marshal(out)
	return strings.TrimSpace(string(data))
}

// IsConfirmed returns true if the user accepted the map.
func (m KVEditorModel) IsConfirmed() bool { return m.confirmed }

// IsCancelled returns true if the user cancelled editing.
func (m KVEditorModel) IsCancelled() bool { return m.cancelled }
