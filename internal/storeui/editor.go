package storeui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
	"github.com/schmitthub/clawker/internal/tui"
)

// editorState tracks the current phase of the editor.
type editorState int

const (
	stateBrowse editorState = iota
	stateEdit
	stateSave
)

// tabRow is either a section heading or an editable field within a tab.
type tabRow struct {
	isHeading bool
	heading   string // set when isHeading
	field     *Field // set when !isHeading
	fieldIdx  int    // index into m.fields
}

// tabPage groups fields under a top-level config key.
type tabPage struct {
	name string
	rows []tabRow
}

// editorModel is the BubbleTea model for the tabbed store field editor.
var _ tea.Model = (*editorModel)(nil)

type editorModel struct {
	title    string
	fields   []Field
	layers   []string
	modified map[string]string
	state    editorState

	// Tab navigation
	tabs      []tabPage
	activeTab int
	activeRow int // index into current tab's rows (only stops on non-heading rows)
	scrollOff int // scroll offset for visible area

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
	m.tabs = m.buildTabs()
	if len(m.tabs) > 0 {
		m.activeRow = m.firstFieldRow(0)
	}
	return m
}

// buildTabs groups fields into tabbed pages by top-level path key.
func (m *editorModel) buildTabs() []tabPage {
	type sectionEntry struct {
		section string
		fields  []struct {
			field    *Field
			fieldIdx int
		}
	}

	// Group fields by top-level key → section key.
	tabMap := make(map[string][]sectionEntry)
	var tabOrder []string
	sectionIdx := make(map[string]map[string]int) // tab → section → index in slice

	for i := range m.fields {
		f := &m.fields[i]
		parts := strings.SplitN(f.Path, ".", 3)
		tabName := parts[0]

		// Determine section name from 2nd path segment if field has 3+ segments.
		section := ""
		if len(parts) >= 3 {
			section = parts[1]
		}

		if _, exists := sectionIdx[tabName]; !exists {
			sectionIdx[tabName] = make(map[string]int)
			tabOrder = append(tabOrder, tabName)
		}

		si, exists := sectionIdx[tabName][section]
		if !exists {
			si = len(tabMap[tabName])
			sectionIdx[tabName][section] = si
			tabMap[tabName] = append(tabMap[tabName], sectionEntry{section: section})
		}

		tabMap[tabName][si].fields = append(tabMap[tabName][si].fields, struct {
			field    *Field
			fieldIdx int
		}{field: f, fieldIdx: i})
	}

	// Build tab pages with rows.
	tabs := make([]tabPage, 0, len(tabOrder))
	for _, tabName := range tabOrder {
		sections := tabMap[tabName]
		var rows []tabRow

		for _, sec := range sections {
			if sec.section != "" {
				rows = append(rows, tabRow{
					isHeading: true,
					heading:   formatHeading(sec.section),
				})
			}
			for _, entry := range sec.fields {
				rows = append(rows, tabRow{
					field:    entry.field,
					fieldIdx: entry.fieldIdx,
				})
			}
		}

		tabs = append(tabs, tabPage{
			name: formatTabName(tabName),
			rows: rows,
		})
	}

	return tabs
}

// firstFieldRow returns the index of the first non-heading row in a tab.
func (m *editorModel) firstFieldRow(tabIdx int) int {
	if tabIdx >= len(m.tabs) {
		return 0
	}
	for i, r := range m.tabs[tabIdx].rows {
		if !r.isHeading {
			return i
		}
	}
	return 0
}

// nextFieldRow finds the next non-heading row after current, wrapping.
func (m *editorModel) nextFieldRow() int {
	if m.activeTab >= len(m.tabs) {
		return 0
	}
	rows := m.tabs[m.activeTab].rows
	for i := 1; i < len(rows); i++ {
		idx := (m.activeRow + i) % len(rows)
		if !rows[idx].isHeading {
			return idx
		}
	}
	return m.activeRow
}

// prevFieldRow finds the previous non-heading row before current, wrapping.
func (m *editorModel) prevFieldRow() int {
	if m.activeTab >= len(m.tabs) {
		return 0
	}
	rows := m.tabs[m.activeTab].rows
	for i := 1; i < len(rows); i++ {
		idx := (m.activeRow - i + len(rows)) % len(rows)
		if !rows[idx].isHeading {
			return idx
		}
	}
	return m.activeRow
}

func (m *editorModel) Init() tea.Cmd {
	return nil
}

func (m *editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
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
			if m.activeTab >= len(m.tabs) {
				return m, nil
			}
			rows := m.tabs[m.activeTab].rows
			if m.activeRow >= len(rows) || rows[m.activeRow].isHeading {
				return m, nil
			}
			f := rows[m.activeRow].field
			if f.ReadOnly {
				return m, nil
			}
			return m, m.enterEditState(rows[m.activeRow].fieldIdx)

		case tui.IsLeft(msg):
			if len(m.tabs) > 1 {
				m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
				m.activeRow = m.firstFieldRow(m.activeTab)
				m.scrollOff = 0
			}
			return m, nil

		case tui.IsRight(msg), tui.IsTab(msg):
			if len(m.tabs) > 1 {
				m.activeTab = (m.activeTab + 1) % len(m.tabs)
				m.activeRow = m.firstFieldRow(m.activeTab)
				m.scrollOff = 0
			}
			return m, nil

		case tui.IsDown(msg):
			m.activeRow = m.nextFieldRow()
			m.ensureVisible()
			return m, nil

		case tui.IsUp(msg):
			m.activeRow = m.prevFieldRow()
			m.ensureVisible()
			return m, nil
		}
	}
	return m, nil
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
			return m, nil
		}
		return m, filterQuit(cmd)

	default:
		var cmd tea.Cmd
		m.textField, cmd = m.textField.Update(msg)
		if m.textField.IsConfirmed() {
			m.modified[f.Path] = m.textField.Value()
			m.state = stateBrowse
			return m, nil
		}
		return m, filterQuit(cmd)
	}
}

func (m *editorModel) enterSaveState() tea.Cmd {
	if len(m.layers) == 0 {
		return nil
	}
	if len(m.layers) == 1 {
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

// ensureVisible adjusts scrollOff so activeRow is visible.
func (m *editorModel) ensureVisible() {
	visible := m.visibleRows()
	if visible <= 0 {
		return
	}
	if m.activeRow < m.scrollOff {
		m.scrollOff = m.activeRow
	}
	if m.activeRow >= m.scrollOff+visible {
		m.scrollOff = m.activeRow - visible + 1
	}
}

// visibleRows returns how many rows fit in the content area.
func (m *editorModel) visibleRows() int {
	// Title(2) + tabbar(2) + help(3) = 7 lines of chrome.
	available := m.height - 7
	if available < 3 {
		available = 3
	}
	return available
}

func (m *editorModel) View() string {
	var b strings.Builder

	// Title
	b.WriteString("  ")
	b.WriteString(iostreams.TitleStyle.Render(m.title))
	b.WriteString("\n\n")

	switch m.state {
	case stateBrowse:
		m.viewBrowse(&b)
	case stateEdit:
		m.viewEdit(&b)
	case stateSave:
		b.WriteString(m.saveField.View())
	}

	return b.String()
}

func (m *editorModel) viewBrowse(b *strings.Builder) {
	// Tab bar
	m.renderTabBar(b)
	b.WriteString("\n")

	// Field list for active tab
	if m.activeTab < len(m.tabs) {
		tab := m.tabs[m.activeTab]
		visible := m.visibleRows()
		end := m.scrollOff + visible
		if end > len(tab.rows) {
			end = len(tab.rows)
		}

		for i := m.scrollOff; i < end; i++ {
			row := tab.rows[i]
			if row.isHeading {
				b.WriteString("  ")
				w := m.width - 4
				if w < 10 {
					w = 40
				}
				b.WriteString(tui.RenderLabeledDivider(row.heading, w))
				b.WriteString("\n")
				continue
			}

			selected := i == m.activeRow
			m.renderFieldRow(b, row, selected)
			b.WriteString("\n")
		}

		// Scroll indicator
		if len(tab.rows) > visible {
			b.WriteString("  ")
			b.WriteString(iostreams.MutedStyle.Render(
				fmt.Sprintf("[%d/%d]", m.activeRow+1, len(tab.rows))))
			b.WriteString("\n")
		}
	}

	// Help bar
	b.WriteString("\n")
	modified := len(m.modified)
	if modified > 0 {
		b.WriteString("  ")
		b.WriteString(iostreams.MutedStyle.Render(fmt.Sprintf("%d modified", modified)))
		b.WriteString("\n")
	}
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("←/→"))
	b.WriteString(iostreams.HelpDescStyle.Render(" tab"))
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("↑/↓"))
	b.WriteString(iostreams.HelpDescStyle.Render(" navigate"))
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("enter"))
	b.WriteString(iostreams.HelpDescStyle.Render(" edit"))
	if modified > 0 {
		b.WriteString("  ")
		b.WriteString(iostreams.HelpKeyStyle.Render("s"))
		b.WriteString(iostreams.HelpDescStyle.Render(" save"))
	}
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("esc"))
	b.WriteString(iostreams.HelpDescStyle.Render(" quit"))
}

func (m *editorModel) renderTabBar(b *strings.Builder) {
	b.WriteString("  ")
	for i, tab := range m.tabs {
		if i > 0 {
			b.WriteString(iostreams.MutedStyle.Render(" │ "))
		}
		label := tab.name
		if i == m.activeTab {
			b.WriteString(iostreams.ListItemSelectedStyle.Render(label))
		} else {
			b.WriteString(iostreams.MutedStyle.Render(label))
		}
	}
	b.WriteString("\n")
}

func (m *editorModel) renderFieldRow(b *strings.Builder, row tabRow, selected bool) {
	f := row.field
	label := f.Label
	value := f.Value
	if v, ok := m.modified[f.Path]; ok {
		value = v
		label = "* " + label
	}
	if f.ReadOnly {
		label = label + " (read-only)"
	}

	// Pad label to fixed width for alignment.
	maxLabel := 30
	if m.width > 60 {
		maxLabel = m.width / 3
	}
	paddedLabel := text.PadRight(label, maxLabel)

	if selected {
		b.WriteString("  > ")
		b.WriteString(iostreams.ListItemSelectedStyle.Render(paddedLabel))
	} else {
		b.WriteString("    ")
		b.WriteString(paddedLabel)
	}
	b.WriteString("  ")
	if f.ReadOnly {
		b.WriteString(iostreams.MutedStyle.Render(value))
	} else {
		b.WriteString(value)
	}
}

func (m *editorModel) viewEdit(b *strings.Builder) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		return
	}
	f := m.fields[m.editIdx]
	switch f.Kind {
	case KindBool, KindTriState, KindSelect:
		b.WriteString(m.selField.View())
	default:
		b.WriteString(m.textField.View())
	}
}

// selectedLayer returns the filename of the layer the user selected to save to.
func (m *editorModel) selectedLayer() string {
	if len(m.layers) == 1 {
		return m.layers[0]
	}
	return m.saveField.Value()
}

// formatTabName capitalizes a yaml key for tab display.
func formatTabName(key string) string {
	if len(key) == 0 {
		return key
	}
	return strings.ToUpper(key[:1]) + key[1:]
}

// formatHeading turns a yaml key like "git_credentials" into "Git Credentials".
func formatHeading(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
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
