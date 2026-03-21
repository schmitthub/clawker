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

// SaveTarget represents a location where changes can be persisted.
type SaveTarget struct {
	Label       string // Display label (e.g. "User settings", "Project local")
	Description string // Short description shown in the save dialog
	Path        string // Full filesystem path for store.WriteTo(), or "" for provenance routing
}

// editFieldKind identifies which sub-editor is active during stateEdit.
type editFieldKind int

const (
	editSelect   editFieldKind = iota // tui.SelectField (bool, tristate, enum)
	editText                          // tui.TextField (simple string, int, duration)
	editList                          // listEditorModel ([]string)
	editTextarea                      // textareaEditorModel (multiline string)
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
	title       string
	fields      []Field
	saveTargets []SaveTarget
	modified    map[string]string
	state       editorState

	// Tab navigation
	tabs      []tabPage
	activeTab int
	activeRow int // index into current tab's rows (only stops on non-heading rows)
	scrollOff int // scroll offset for visible area

	// Edit state — which sub-editor is active depends on field kind.
	editIdx    int
	editKind   editFieldKind
	textField  tui.TextField
	selField   tui.SelectField
	listEditor listEditorModel
	taEditor   textareaEditorModel

	// Save state
	saveField tui.SelectField

	// Result
	saved     bool
	cancelled bool
	width     int
	height    int
}

func newEditorModel(title string, fields []Field, saveTargets []SaveTarget) *editorModel {
	m := &editorModel{
		title:       title,
		fields:      fields,
		saveTargets: saveTargets,
		modified:    make(map[string]string),
		state:       stateBrowse,
		width:       80,
		height:      24,
	}
	m.tabs = m.buildTabs()
	if len(m.tabs) > 0 {
		m.activeRow = m.firstFieldRow(0)
	}
	return m
}

// fieldEntry holds a pointer into m.fields plus its original index.
type fieldEntry struct {
	field    *Field
	fieldIdx int
}

// sectionEntry groups fields under a named sub-key within a tab.
type sectionEntry struct {
	section string
	fields  []fieldEntry
}

// buildTabs groups fields into tabbed pages by top-level path key.
// Fields with 3+ path segments (e.g. "security.git_credentials.forward_ssh") are
// grouped into a sub-section named after the 2nd segment within the tab.
func (m *editorModel) buildTabs() []tabPage {
	// tabSections preserves insertion order per tab.
	tabSections := make(map[string][]sectionEntry)
	// sectionPos tracks each section's index within its tab for O(1) appends.
	sectionPos := make(map[string]map[string]int) // tab → section → slice index
	var tabOrder []string

	for i := range m.fields {
		f := &m.fields[i]
		parts := strings.SplitN(f.Path, ".", 3)
		tabName := parts[0]

		// Use the 2nd path segment as a sub-section header when there are 3+ segments.
		section := ""
		if len(parts) == 3 {
			section = parts[1]
		}

		if _, exists := sectionPos[tabName]; !exists {
			sectionPos[tabName] = make(map[string]int)
			tabOrder = append(tabOrder, tabName)
		}

		si, exists := sectionPos[tabName][section]
		if !exists {
			si = len(tabSections[tabName])
			sectionPos[tabName][section] = si
			tabSections[tabName] = append(tabSections[tabName], sectionEntry{section: section})
		}

		tabSections[tabName][si].fields = append(tabSections[tabName][si].fields, fieldEntry{field: f, fieldIdx: i})
	}

	tabs := make([]tabPage, 0, len(tabOrder))
	for _, tabName := range tabOrder {
		var rows []tabRow
		for _, sec := range tabSections[tabName] {
			if sec.section != "" {
				rows = append(rows, tabRow{isHeading: true, heading: formatHeading(sec.section)})
			}
			for _, e := range sec.fields {
				rows = append(rows, tabRow{field: e.field, fieldIdx: e.fieldIdx})
			}
		}
		tabs = append(tabs, tabPage{name: formatTabName(tabName), rows: rows})
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

// nextFieldRow finds the next non-heading row after the current one, wrapping.
func (m *editorModel) nextFieldRow() int {
	return m.adjacentFieldRow(+1)
}

// prevFieldRow finds the previous non-heading row before the current one, wrapping.
func (m *editorModel) prevFieldRow() int {
	return m.adjacentFieldRow(-1)
}

// adjacentFieldRow walks rows in the given direction (+1 or -1), wrapping around,
// and returns the index of the first non-heading row found. Returns activeRow if none found.
func (m *editorModel) adjacentFieldRow(dir int) int {
	if m.activeTab >= len(m.tabs) {
		return 0
	}
	rows := m.tabs[m.activeTab].rows
	n := len(rows)
	for i := 1; i < n; i++ {
		idx := (m.activeRow + dir*i + n) % n
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
		m.editKind = editSelect
		options := []tui.FieldOption{{Label: "true"}, {Label: "false"}}
		defaultIdx := 1
		if currentVal == "true" {
			defaultIdx = 0
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)
		return nil

	case KindTriState:
		m.editKind = editSelect
		options := []tui.FieldOption{{Label: "true"}, {Label: "false"}, {Label: "<unset>"}}
		defaultIdx := 2
		switch currentVal {
		case "true":
			defaultIdx = 0
		case "false":
			defaultIdx = 1
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)
		return nil

	case KindSelect:
		if len(f.Options) == 0 {
			m.state = stateBrowse
			return nil
		}
		m.editKind = editSelect
		options := make([]tui.FieldOption, len(f.Options))
		defaultIdx := 0
		for i, opt := range f.Options {
			options[i] = tui.FieldOption{Label: opt}
			if opt == currentVal {
				defaultIdx = i
			}
		}
		m.selField = tui.NewSelectField("edit", f.Label, options, defaultIdx)
		return nil

	case KindStringSlice:
		m.editKind = editList
		m.listEditor = newListEditor(f.Label, currentVal)
		return m.listEditor.Init()

	default:
		// Text, Int, Duration — check if multiline.
		if strings.Contains(currentVal, "\n") {
			m.editKind = editTextarea
			m.taEditor = newTextareaEditor(f.Label, currentVal)
			return m.taEditor.Init()
		}
		m.editKind = editText
		opts := []tui.TextFieldOption{tui.WithDefault(currentVal)}
		if f.Validator != nil {
			opts = append(opts, tui.WithValidator(f.Validator))
		}
		m.textField = tui.NewTextField("edit", f.Label, opts...)
		return m.textField.Init()
	}
}

func (m *editorModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		m.state = stateBrowse
		return m, nil
	}
	f := m.fields[m.editIdx]

	switch m.editKind {
	case editSelect:
		// Esc handled by the caller; select field uses Enter to confirm.
		if msg, ok := msg.(tea.KeyMsg); ok && tui.IsEscape(msg) {
			m.state = stateBrowse
			return m, nil
		}
		var cmd tea.Cmd
		m.selField, cmd = m.selField.Update(msg)
		if m.selField.IsConfirmed() {
			m.trackModified(f.Path, m.selField.Value())
			m.state = stateBrowse
			return m, nil
		}
		return m, filterQuit(cmd)

	case editText:
		if msg, ok := msg.(tea.KeyMsg); ok && tui.IsEscape(msg) {
			m.state = stateBrowse
			return m, nil
		}
		var cmd tea.Cmd
		m.textField, cmd = m.textField.Update(msg)
		if m.textField.IsConfirmed() {
			m.trackModified(f.Path, m.textField.Value())
			m.state = stateBrowse
			return m, nil
		}
		return m, filterQuit(cmd)

	case editList:
		var cmd tea.Cmd
		m.listEditor, cmd = m.listEditor.Update(msg)
		if m.listEditor.IsConfirmed() {
			m.trackModified(f.Path, m.listEditor.Value())
			m.state = stateBrowse
			return m, nil
		}
		if m.listEditor.IsCancelled() {
			m.state = stateBrowse
			return m, nil
		}
		return m, cmd

	case editTextarea:
		var cmd tea.Cmd
		m.taEditor, cmd = m.taEditor.Update(msg)
		if m.taEditor.IsConfirmed() {
			m.trackModified(f.Path, m.taEditor.Value())
			m.state = stateBrowse
			return m, nil
		}
		if m.taEditor.IsCancelled() {
			m.state = stateBrowse
			return m, nil
		}
		return m, cmd
	}

	return m, nil
}

func (m *editorModel) enterSaveState() tea.Cmd {
	if len(m.saveTargets) <= 1 {
		// Single or no target — save directly.
		m.saved = true
		return tea.Quit
	}

	// Multiple targets — ask the user.
	m.state = stateSave
	options := make([]tui.FieldOption, len(m.saveTargets))
	for i, t := range m.saveTargets {
		options[i] = tui.FieldOption{Label: t.Label, Description: t.Description}
	}
	m.saveField = tui.NewSelectField("save", "Save changes to:", options, 0)
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
	// Show effective default when value is unset.
	if (value == "<unset>" || value == "") && f.Default != "" {
		value = f.Default + " (default)"
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
	// Truncate long values to fit the terminal width.
	maxVal := m.width - maxLabel - 8
	if maxVal < 10 {
		maxVal = 40
	}
	displayVal := value
	if strings.Contains(displayVal, "\n") {
		// Show first line only for multiline values.
		displayVal = strings.SplitN(displayVal, "\n", 2)[0] + "..."
	}
	displayVal = text.Truncate(displayVal, maxVal)

	b.WriteString("  ")
	if f.ReadOnly {
		b.WriteString(iostreams.MutedStyle.Render(displayVal))
	} else {
		b.WriteString(displayVal)
	}
}

func (m *editorModel) viewEdit(b *strings.Builder) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		return
	}
	switch m.editKind {
	case editSelect:
		b.WriteString(m.selField.View())
	case editText:
		b.WriteString(m.textField.View())
	case editList:
		b.WriteString(m.listEditor.View())
	case editTextarea:
		b.WriteString(m.taEditor.View())
	}
}

// trackModified records a field change only if the value actually differs from the original.
func (m *editorModel) trackModified(path string, newVal string) {
	// Find the original value from the field.
	for i := range m.fields {
		if m.fields[i].Path == path {
			if newVal == m.fields[i].Value {
				// Value unchanged — remove from modified if it was there.
				delete(m.modified, path)
			} else {
				m.modified[path] = newVal
			}
			return
		}
	}
	m.modified[path] = newVal
}

// selectedTarget returns the SaveTarget the user chose (or the only available target).
func (m *editorModel) selectedTarget() SaveTarget {
	idx := m.saveField.SelectedIndex()
	if idx >= 0 && idx < len(m.saveTargets) {
		return m.saveTargets[idx]
	}
	if len(m.saveTargets) == 1 {
		return m.saveTargets[0]
	}
	return SaveTarget{}
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
