package storeui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/tui"
)

// editorState tracks the current phase of the editor.
type editorState int

const (
	stateBrowse editorState = iota
	stateEdit
	stateSave
)

// editorModel is the BubbleTea model for the store field editor.
var _ tea.Model = (*editorModel)(nil)

type editorModel struct {
	title    string
	fields   []Field
	layers   []string // layer filenames for save target
	modified map[string]string
	state    editorState

	// Browse state
	list tui.ListModel

	// Edit state
	editIdx   int
	textField tui.TextField
	selField  tui.SelectField

	// Save state
	saveField tui.SelectField

	// Result
	saved     bool
	cancelled bool
	width     int
	height    int
}

func newEditorModel(title string, fields []Field, layers []string) *editorModel {
	m := &editorModel{
		title:    title,
		fields:   fields,
		layers:   layers,
		modified: make(map[string]string),
		state:    stateBrowse,
	}
	m.list = m.buildFieldList()
	return m
}

func (m *editorModel) buildFieldList() tui.ListModel {
	items := make([]tui.ListItem, len(m.fields))
	for i, f := range m.fields {
		label := f.Label
		value := f.Value
		if v, ok := m.modified[f.Path]; ok {
			value = v
			label = "* " + label
		}
		if f.ReadOnly {
			label = label + " (read-only)"
		}
		items[i] = tui.SimpleListItem{
			ItemTitle:       label,
			ItemDescription: value,
		}
	}

	cfg := tui.DefaultListConfig()
	cfg.ShowDescriptions = true
	cfg.Wrap = false
	if m.height > 6 {
		cfg.Height = m.height - 6
	}
	if m.width > 4 {
		cfg.Width = m.width - 4
	}

	list := tui.NewList(cfg)
	list = list.SetItems(items)
	if m.list.Len() > 0 {
		idx := m.list.SelectedIndex()
		if idx >= len(items) {
			idx = len(items) - 1
		}
		list = list.Select(idx)
	}
	return list
}

func (m *editorModel) Init() tea.Cmd {
	return nil
}

func (m *editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list = m.buildFieldList()
		return m, nil
	}

	switch m.state {
	case stateBrowse:
		return m.updateBrowse(msg)
	case stateEdit:
		return m.updateEdit(msg)
	case stateSave:
		return m.updateSave(msg)
	}
	return m, nil
}

func (m *editorModel) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case tui.IsQuit(msg), tui.IsEscape(msg):
			m.cancelled = true
			return m, tea.Quit

		case msg.String() == "s":
			if len(m.modified) == 0 {
				return m, nil
			}
			return m, m.enterSaveState()

		case tui.IsEnter(msg):
			idx := m.list.SelectedIndex()
			if idx < 0 || idx >= len(m.fields) {
				return m, nil
			}
			f := m.fields[idx]
			if f.ReadOnly {
				return m, nil
			}
			return m, m.enterEditState(idx)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m *editorModel) enterEditState(idx int) tea.Cmd {
	m.state = stateEdit
	m.editIdx = idx
	f := m.fields[idx]

	currentVal := f.Value
	if v, ok := m.modified[f.Path]; ok {
		currentVal = v
	}

	switch f.Kind {
	case KindBool:
		options := []tui.FieldOption{
			{Label: "true"},
			{Label: "false"},
		}
		defaultIdx := 1
		if currentVal == "true" {
			defaultIdx = 0
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)

	case KindTriState:
		options := []tui.FieldOption{
			{Label: "true"},
			{Label: "false"},
			{Label: "<unset>"},
		}
		defaultIdx := 2
		switch currentVal {
		case "true":
			defaultIdx = 0
		case "false":
			defaultIdx = 1
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)

	case KindSelect:
		if len(f.Options) == 0 {
			// No options — cannot edit. Return to browse.
			m.state = stateBrowse
			return nil
		}
		options := make([]tui.FieldOption, len(f.Options))
		defaultIdx := 0
		for i, opt := range f.Options {
			options[i] = tui.FieldOption{Label: opt}
			if opt == currentVal {
				defaultIdx = i
			}
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)

	default:
		// Text, Int, Duration, StringSlice all use TextField.
		opts := []tui.TextFieldOption{
			tui.WithDefault(currentVal),
		}
		if f.Validator != nil {
			opts = append(opts, tui.WithValidator(f.Validator))
		}
		m.textField = tui.NewTextField("edit", f.Label, opts...)
		return m.textField.Init()
	}
	return nil
}

func (m *editorModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		m.state = stateBrowse
		return m, nil
	}
	f := m.fields[m.editIdx]

	if msg, ok := msg.(tea.KeyMsg); ok && tui.IsEscape(msg) {
		m.state = stateBrowse
		return m, nil
	}

	switch f.Kind {
	case KindBool, KindTriState, KindSelect:
		var cmd tea.Cmd
		m.selField, cmd = m.selField.Update(msg)
		if m.selField.IsConfirmed() {
			m.modified[f.Path] = m.selField.Value()
			m.state = stateBrowse
			m.list = m.buildFieldList()
			return m, nil
		}
		return m, filterQuit(cmd)

	default:
		var cmd tea.Cmd
		m.textField, cmd = m.textField.Update(msg)
		if m.textField.IsConfirmed() {
			m.modified[f.Path] = m.textField.Value()
			m.state = stateBrowse
			m.list = m.buildFieldList()
			return m, nil
		}
		return m, filterQuit(cmd)
	}
}

func (m *editorModel) enterSaveState() tea.Cmd {
	if len(m.layers) == 0 {
		// No layers to save to — stay in browse.
		return nil
	}
	if len(m.layers) == 1 {
		// Auto-select the only layer.
		m.saved = true
		return tea.Quit
	}

	m.state = stateSave
	options := make([]tui.FieldOption, len(m.layers))
	for i, l := range m.layers {
		options[i] = tui.FieldOption{Label: l}
	}
	m.saveField = tui.NewSelectField("save", "Save to which config file?", options, 0)
	return nil
}

func (m *editorModel) updateSave(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok && tui.IsEscape(msg) {
		m.state = stateBrowse
		return m, nil
	}

	var cmd tea.Cmd
	m.saveField, cmd = m.saveField.Update(msg)
	if m.saveField.IsConfirmed() {
		m.saved = true
		return m, tea.Quit
	}
	return m, filterQuit(cmd)
}

func (m *editorModel) View() string {
	var b strings.Builder

	b.WriteString("  ")
	b.WriteString(iostreams.TitleStyle.Render(m.title))
	b.WriteString("\n\n")

	switch m.state {
	case stateBrowse:
		b.WriteString(m.list.View())
		b.WriteString("\n\n")

		modified := len(m.modified)
		if modified > 0 {
			b.WriteString("  ")
			b.WriteString(iostreams.MutedStyle.Render(fmt.Sprintf("%d modified", modified)))
			b.WriteString("\n")
		}
		b.WriteString("  ")
		b.WriteString(iostreams.HelpKeyStyle.Render("enter"))
		b.WriteString(iostreams.HelpDescStyle.Render(" edit"))
		b.WriteString("  ")
		if modified > 0 {
			b.WriteString(iostreams.HelpKeyStyle.Render("s"))
			b.WriteString(iostreams.HelpDescStyle.Render(" save"))
			b.WriteString("  ")
		}
		b.WriteString(iostreams.HelpKeyStyle.Render("esc"))
		b.WriteString(iostreams.HelpDescStyle.Render(" cancel"))

	case stateEdit:
		b.WriteString(m.editView())

	case stateSave:
		b.WriteString(m.saveField.View())
	}

	return b.String()
}

func (m *editorModel) editView() string {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		return ""
	}
	f := m.fields[m.editIdx]
	switch f.Kind {
	case KindBool, KindTriState, KindSelect:
		return m.selField.View()
	default:
		return m.textField.View()
	}
}

// selectedLayer returns the filename of the layer the user selected to save to.
func (m *editorModel) selectedLayer() string {
	if len(m.layers) == 1 {
		return m.layers[0]
	}
	return m.saveField.Value()
}

// filterQuit filters out tea.Quit commands from child widgets to prevent
// them from terminating the parent editor.
func filterQuit(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if _, ok := msg.(tea.QuitMsg); ok {
			return nil
		}
		return msg
	}
}
