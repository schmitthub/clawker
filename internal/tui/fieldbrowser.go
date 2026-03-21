package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// BrowserFieldKind identifies how a field should be edited.
type BrowserFieldKind int

const (
	BrowserText        BrowserFieldKind = iota // Single-line text input
	BrowserBool                                // true/false select
	BrowserTriState                            // Deprecated: mapped to BrowserBool. Retained for iota stability.
	BrowserSelect                              // Bounded enum with Options
	BrowserInt                                 // Integer text input
	BrowserStringSlice                         // List editor (comma-separated)
	BrowserDuration                            // Duration text input
	BrowserComplex                             // Unsupported — always read-only
)

// BrowserField represents a single field in the field browser.
type BrowserField struct {
	Path        string             // Dotted path used as key (e.g. "build.image")
	Label       string             // Human-readable label
	Description string             // Help text
	Kind        BrowserFieldKind   // Widget type for editing
	Value       string             // Formatted current value
	Default     string             // Shown when Value is empty or "<unset>"
	Source      string             // Where this value came from (e.g. "~/.config/clawker/clawker.yaml")
	Options     []string           // For Select fields
	Validator   func(string) error // Optional input validation
	Required    bool               // Whether the field must have a value
	ReadOnly    bool               // Whether the field is not editable
	Order       int                // Sort order (lower = first)
}

// BrowserLayerTarget represents a save destination for a single field.
type BrowserLayerTarget struct {
	Label       string // "Original", "Local", "User"
	Description string // Shortened path for display
}

// BrowserResult holds the outcome of a field browser session.
type BrowserResult struct {
	Saved      bool // True if any field was persisted
	Cancelled  bool // True if the user cancelled
	SavedCount int  // Number of fields successfully saved
}

// BrowserLayer represents a discovered configuration layer with its raw data.
// Used to show per-layer value breakdowns for the selected field.
type BrowserLayer struct {
	Label string         // Human-readable label (e.g. "~/.config/clawker/clawker.yaml")
	Data  map[string]any // Raw YAML data from this layer
}

// BrowserConfig configures the field browser.
type BrowserConfig struct {
	Title        string               // Title displayed at the top
	Fields       []BrowserField       // Fields to display
	LayerTargets []BrowserLayerTarget // Per-field save destinations (Local, User, etc.)
	Layers       []BrowserLayer       // Discovered layers for per-field provenance display

	// OnFieldSaved is called when the user saves a single field to a layer.
	// fieldPath is the dotted path, value is the new string value,
	// targetIdx is the index into LayerTargets. Return error to show to user.
	OnFieldSaved func(fieldPath, value string, targetIdx int) error
}

// ---------------------------------------------------------------------------
// browserState
// ---------------------------------------------------------------------------

type browserState int

const (
	bsStateBrowse browserState = iota
	bsStateEdit
	bsStatePickLayer // layer picker after field edit confirmation
)

// editKind identifies which sub-editor is active during bsStateEdit.
type editKind int

const (
	ekSelect   editKind = iota // SelectField (bool, tristate, enum)
	ekText                     // TextField (simple string, int, duration)
	ekList                     // ListEditorModel ([]string)
	ekTextarea                 // TextareaEditorModel (multiline string)
)

// ---------------------------------------------------------------------------
// tab grouping types
// ---------------------------------------------------------------------------

type browserRow struct {
	isHeading bool
	heading   string        // set when isHeading
	field     *BrowserField // set when !isHeading
	fieldIdx  int           // index into fields slice
}

type browserTab struct {
	name string
	rows []browserRow
}

// ---------------------------------------------------------------------------
// FieldBrowserModel — the BubbleTea model
// ---------------------------------------------------------------------------

var _ tea.Model = (*FieldBrowserModel)(nil)

// FieldBrowserModel is a generic tabbed field browser/editor.
// It knows nothing about stores, reflection, or config schemas.
type FieldBrowserModel struct {
	title        string
	fields       []BrowserField
	layerTargets []BrowserLayerTarget
	layers       []BrowserLayer
	onFieldSaved func(fieldPath, value string, targetIdx int) error
	savedCount   int
	state        browserState

	// Tab navigation
	tabs      []browserTab
	activeTab int
	activeRow int // index into current tab's rows (skips headings)
	scrollOff int // scroll offset for visible area

	// Edit state
	editIdx    int
	editKind   editKind
	textField  TextField
	selField   SelectField
	listEditor ListEditorModel
	taEditor   TextareaEditorModel

	// Layer picker state (after edit confirmation)
	layerField    SelectField
	pendingPath   string // field path being saved
	pendingValue  string // value being saved
	lastSaveError string // error from OnFieldSaved, shown briefly

	// Result
	saved     bool // true if any field was persisted
	cancelled bool
	width     int
	height    int
}

// NewFieldBrowser creates a field browser from the given config.
func NewFieldBrowser(cfg BrowserConfig) *FieldBrowserModel {
	m := &FieldBrowserModel{
		title:        cfg.Title,
		fields:       cfg.Fields,
		layerTargets: cfg.LayerTargets,
		layers:       cfg.Layers,
		onFieldSaved: cfg.OnFieldSaved,
		state:        bsStateBrowse,
		width:        80,
		height:       24,
	}
	m.tabs = m.buildTabs()
	if len(m.tabs) > 0 {
		m.activeRow = m.firstFieldRow(0)
	}
	return m
}

// Result returns the browser result after the program exits.
func (m *FieldBrowserModel) Result() BrowserResult {
	return BrowserResult{
		Saved:      m.saved,
		Cancelled:  m.cancelled,
		SavedCount: m.savedCount,
	}
}

// ---------------------------------------------------------------------------
// Tab building
// ---------------------------------------------------------------------------

type fbFieldEntry struct {
	field    *BrowserField
	fieldIdx int
}

type fbSectionEntry struct {
	section string
	fields  []fbFieldEntry
}

// buildTabs groups fields into tabbed pages by top-level path key.
// Fields with 3+ path segments are grouped into sub-sections within the tab.
func (m *FieldBrowserModel) buildTabs() []browserTab {
	tabSections := make(map[string][]fbSectionEntry)
	sectionPos := make(map[string]map[string]int) // tab → section → slice index
	var tabOrder []string

	for i := range m.fields {
		f := &m.fields[i]
		parts := strings.SplitN(f.Path, ".", 3)
		tabName := parts[0]

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
			tabSections[tabName] = append(tabSections[tabName], fbSectionEntry{section: section})
		}

		tabSections[tabName][si].fields = append(tabSections[tabName][si].fields, fbFieldEntry{field: f, fieldIdx: i})
	}

	tabs := make([]browserTab, 0, len(tabOrder))
	for _, tabName := range tabOrder {
		var rows []browserRow
		for _, sec := range tabSections[tabName] {
			if sec.section != "" {
				rows = append(rows, browserRow{isHeading: true, heading: fbFormatHeading(sec.section)})
			}
			for _, e := range sec.fields {
				rows = append(rows, browserRow{field: e.field, fieldIdx: e.fieldIdx})
			}
		}
		tabs = append(tabs, browserTab{name: fbFormatTabName(tabName), rows: rows})
	}

	return tabs
}

func (m *FieldBrowserModel) firstFieldRow(tabIdx int) int {
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

func (m *FieldBrowserModel) adjacentFieldRow(dir int) int {
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

func (m *FieldBrowserModel) nextFieldRow() int { return m.adjacentFieldRow(+1) }
func (m *FieldBrowserModel) prevFieldRow() int { return m.adjacentFieldRow(-1) }

// ---------------------------------------------------------------------------
// BubbleTea interface
// ---------------------------------------------------------------------------

func (m *FieldBrowserModel) Init() tea.Cmd {
	return nil
}

func (m *FieldBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Forward resize to active editor so it can adjust its dimensions.
		if m.state == bsStateEdit {
			return m.updateEdit(msg)
		}
		return m, nil
	}

	switch m.state {
	case bsStateBrowse:
		return m.updateBrowse(msg)
	case bsStateEdit:
		return m.updateEdit(msg)
	case bsStatePickLayer:
		return m.updatePickLayer(msg)
	}
	return m, nil
}

func (m *FieldBrowserModel) updateBrowse(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsQuit(msg), IsEscape(msg):
			m.cancelled = true
			return m, tea.Quit

		case IsEnter(msg):
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

		case IsLeft(msg):
			if len(m.tabs) > 1 {
				m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
				m.activeRow = m.firstFieldRow(m.activeTab)
				m.scrollOff = 0
			}
			return m, nil

		case IsRight(msg), IsTab(msg):
			if len(m.tabs) > 1 {
				m.activeTab = (m.activeTab + 1) % len(m.tabs)
				m.activeRow = m.firstFieldRow(m.activeTab)
				m.scrollOff = 0
			}
			return m, nil

		case IsDown(msg):
			m.activeRow = m.nextFieldRow()
			m.ensureVisible()
			return m, nil

		case IsUp(msg):
			m.activeRow = m.prevFieldRow()
			m.ensureVisible()
			return m, nil
		}
	}
	return m, nil
}

func (m *FieldBrowserModel) enterEditState(idx int) tea.Cmd {
	m.state = bsStateEdit
	m.editIdx = idx
	f := m.fields[idx]

	currentVal := f.Value

	switch f.Kind {
	case BrowserBool, BrowserTriState:
		m.editKind = ekSelect
		options := []FieldOption{{Label: "true"}, {Label: "false"}}
		defaultIdx := 1
		if currentVal == "true" {
			defaultIdx = 0
		}
		m.selField = NewSelectField("edit", f.Label, options, defaultIdx)
		return nil

	case BrowserSelect:
		if len(f.Options) == 0 {
			m.state = bsStateBrowse
			return nil
		}
		m.editKind = ekSelect
		options := make([]FieldOption, len(f.Options))
		defaultIdx := 0
		for i, opt := range f.Options {
			options[i] = FieldOption{Label: opt}
			if opt == currentVal {
				defaultIdx = i
			}
		}
		m.selField = NewSelectField("edit", f.Label, options, defaultIdx)
		return nil

	case BrowserStringSlice:
		m.editKind = ekList
		m.listEditor = NewListEditor(f.Label, currentVal)
		if m.width > 0 && m.height > 0 {
			m.listEditor, _ = m.listEditor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
		}
		return m.listEditor.Init()

	default:
		// Text, Int, Duration — check if multiline.
		if strings.Contains(currentVal, "\n") {
			m.editKind = ekTextarea
			m.taEditor = NewTextareaEditor(f.Label, currentVal)
			// Size to the browser's known dimensions so wrapping works immediately.
			if m.width > 0 && m.height > 0 {
				m.taEditor, _ = m.taEditor.Update(tea.WindowSizeMsg{Width: m.width, Height: m.height})
			}
			return m.taEditor.Init()
		}
		m.editKind = ekText
		opts := []TextFieldOption{WithDefault(currentVal)}
		if f.Validator != nil {
			opts = append(opts, WithValidator(f.Validator))
		}
		m.textField = NewTextField("edit", f.Label, opts...)
		return m.textField.Init()
	}
}

func (m *FieldBrowserModel) updateEdit(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		m.state = bsStateBrowse
		return m, nil
	}
	f := m.fields[m.editIdx]

	switch m.editKind {
	case ekSelect:
		if msg, ok := msg.(tea.KeyMsg); ok && IsEscape(msg) {
			m.state = bsStateBrowse
			return m, nil
		}
		var cmd tea.Cmd
		m.selField, cmd = m.selField.Update(msg)
		if m.selField.IsConfirmed() {
			return m, m.enterPickLayer(f.Path, m.selField.Value())
		}
		return m, fbFilterQuit(cmd)

	case ekText:
		if msg, ok := msg.(tea.KeyMsg); ok && IsEscape(msg) {
			m.state = bsStateBrowse
			return m, nil
		}
		var cmd tea.Cmd
		m.textField, cmd = m.textField.Update(msg)
		if m.textField.IsConfirmed() {
			return m, m.enterPickLayer(f.Path, m.textField.Value())
		}
		return m, fbFilterQuit(cmd)

	case ekList:
		var cmd tea.Cmd
		m.listEditor, cmd = m.listEditor.Update(msg)
		if m.listEditor.IsConfirmed() {
			return m, m.enterPickLayer(f.Path, m.listEditor.Value())
		}
		if m.listEditor.IsCancelled() {
			m.state = bsStateBrowse
			return m, nil
		}
		return m, cmd

	case ekTextarea:
		var cmd tea.Cmd
		m.taEditor, cmd = m.taEditor.Update(msg)
		if m.taEditor.IsConfirmed() {
			return m, m.enterPickLayer(f.Path, m.taEditor.Value())
		}
		if m.taEditor.IsCancelled() {
			m.state = bsStateBrowse
			return m, nil
		}
		return m, cmd
	}

	return m, nil
}

// enterPickLayer transitions to the layer picker after a field edit is confirmed.
func (m *FieldBrowserModel) enterPickLayer(fieldPath, value string) tea.Cmd {
	if len(m.layerTargets) == 0 {
		m.lastSaveError = "no save destinations available"
		m.state = bsStateBrowse
		return nil
	}

	m.pendingPath = fieldPath
	m.pendingValue = value
	m.state = bsStatePickLayer

	options := make([]FieldOption, len(m.layerTargets))
	for i, t := range m.layerTargets {
		options[i] = FieldOption{Label: t.Label, Description: t.Description}
	}
	m.layerField = NewSelectField("layer", "Save to:", options, 0)
	return nil
}

func (m *FieldBrowserModel) updatePickLayer(msg tea.Msg) (tea.Model, tea.Cmd) {
	if msg, ok := msg.(tea.KeyMsg); ok && IsEscape(msg) {
		// Discard the edit, return to browse.
		m.pendingPath = ""
		m.pendingValue = ""
		m.state = bsStateBrowse
		return m, nil
	}

	var cmd tea.Cmd
	m.layerField, cmd = m.layerField.Update(msg)
	if m.layerField.IsConfirmed() {
		idx := m.layerField.SelectedIndex()
		// Persist via callback.
		if m.onFieldSaved == nil {
			m.lastSaveError = "save not available (no save handler configured)"
			m.state = bsStateBrowse
			return m, nil
		}
		if idx >= 0 && idx < len(m.layerTargets) {
			if err := m.onFieldSaved(m.pendingPath, m.pendingValue, idx); err != nil {
				m.lastSaveError = err.Error()
				m.state = bsStateBrowse
				return m, nil
			}
		}
		// Update the field's displayed value.
		for i := range m.fields {
			if m.fields[i].Path == m.pendingPath {
				m.fields[i].Value = m.pendingValue
				break
			}
		}
		m.lastSaveError = "" // Clear any previous error on success.
		m.saved = true
		m.savedCount++
		m.pendingPath = ""
		m.pendingValue = ""
		m.state = bsStateBrowse
		return m, nil
	}
	return m, fbFilterQuit(cmd)
}

// ---------------------------------------------------------------------------
// Scroll
// ---------------------------------------------------------------------------

func (m *FieldBrowserModel) ensureVisible() {
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

func (m *FieldBrowserModel) visibleRows() int {
	// Chrome: title(2) + tabbar(2) + status/help(3: newline + optional error + help bar).
	chrome := 7
	if len(m.layers) > 0 {
		chrome += len(m.layers) + 2 // divider + entries
	}
	return max(m.height-chrome, 3)
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

func (m *FieldBrowserModel) View() string {
	var b strings.Builder

	// Title
	b.WriteString("  ")
	b.WriteString(iostreams.TitleStyle.Render(m.title))
	b.WriteString("\n\n")

	switch m.state {
	case bsStateBrowse:
		m.viewBrowse(&b)
	case bsStateEdit:
		m.viewEdit(&b)
	case bsStatePickLayer:
		b.WriteString(m.layerField.View())
	}

	return b.String()
}

func (m *FieldBrowserModel) viewBrowse(b *strings.Builder) {
	// Tab bar
	m.renderTabBar(b)
	b.WriteString("\n")

	// Field list for active tab
	if m.activeTab < len(m.tabs) {
		tab := m.tabs[m.activeTab]
		visible := m.visibleRows()
		end := min(m.scrollOff+visible, len(tab.rows))

		for i := m.scrollOff; i < end; i++ {
			row := tab.rows[i]
			if row.isHeading {
				b.WriteString("  ")
				w := m.width - 4
				if w < 10 {
					w = 40
				}
				b.WriteString(RenderLabeledDivider(row.heading, w))
				b.WriteString("\n")
				continue
			}

			selected := i == m.activeRow
			m.renderFieldRow(b, row, selected)
			b.WriteString("\n")
		}

		// Scroll indicator (count only field rows, not section headings)
		if len(tab.rows) > visible {
			fieldIdx, fieldTotal := 0, 0
			for i, r := range tab.rows {
				if !r.isHeading {
					fieldTotal++
					if i <= m.activeRow {
						fieldIdx++
					}
				}
			}
			b.WriteString("  ")
			b.WriteString(iostreams.MutedStyle.Render(
				fmt.Sprintf("[%d/%d]", fieldIdx, fieldTotal)))
			b.WriteString("\n")
		}
	}

	// Layer breakdown for selected field
	if len(m.layers) > 0 && m.activeTab < len(m.tabs) {
		rows := m.tabs[m.activeTab].rows
		if m.activeRow < len(rows) && !rows[m.activeRow].isHeading {
			m.renderLayerBreakdown(b, rows[m.activeRow].field)
		}
	}

	// Status line
	b.WriteString("\n")
	if m.lastSaveError != "" {
		b.WriteString("  ")
		b.WriteString(iostreams.ErrorStyle.Render("Error: " + m.lastSaveError))
		b.WriteString("\n")
	}

	// Help bar
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("←/→"))
	b.WriteString(iostreams.HelpDescStyle.Render(" tab"))
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("↑/↓"))
	b.WriteString(iostreams.HelpDescStyle.Render(" navigate"))
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("enter"))
	b.WriteString(iostreams.HelpDescStyle.Render(" edit"))
	b.WriteString("  ")
	b.WriteString(iostreams.HelpKeyStyle.Render("esc"))
	b.WriteString(iostreams.HelpDescStyle.Render(" quit"))
}

func (m *FieldBrowserModel) renderTabBar(b *strings.Builder) {
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

func (m *FieldBrowserModel) renderFieldRow(b *strings.Builder, row browserRow, selected bool) {
	f := row.field
	label := f.Label
	value := f.Value
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
		displayVal = strings.SplitN(displayVal, "\n", 2)[0] + "..."
	}
	displayVal = text.Truncate(displayVal, maxVal)

	b.WriteString("  ")
	if f.ReadOnly {
		b.WriteString(iostreams.MutedStyle.Render(displayVal))
	} else {
		b.WriteString(displayVal)
	}

	// Show source provenance when selected.
	if selected && f.Source != "" {
		b.WriteString("  ")
		b.WriteString(iostreams.MutedStyle.Render("← " + f.Source))
	}
}

// renderLayerBreakdown shows what each layer has for the given field.
// Walks the field's dotted path through each layer's raw data map.
func (m *FieldBrowserModel) renderLayerBreakdown(b *strings.Builder, f *BrowserField) {
	segments := strings.Split(f.Path, ".")

	type layerValue struct {
		label string
		value string
	}
	var entries []layerValue

	for _, layer := range m.layers {
		val, found := lookupMapPath(layer.Data, segments)
		if !found {
			continue
		}
		if val == "" {
			val = `""`
		}
		entries = append(entries, layerValue{label: layer.Label, value: val})
	}

	if len(entries) == 0 {
		return
	}

	b.WriteString("\n")
	w := m.width - 4
	if w < 10 {
		w = 40
	}
	b.WriteString("  ")
	b.WriteString(RenderLabeledDivider("layers", w))
	b.WriteString("\n")

	// Find max label width for alignment.
	maxLabel := 0
	for _, e := range entries {
		if len(e.label) > maxLabel {
			maxLabel = len(e.label)
		}
	}

	for i, e := range entries {
		isWinner := i == 0 // first layer with a value = highest priority = winner
		b.WriteString("    ")
		label := text.PadRight(e.label, maxLabel)
		if isWinner {
			b.WriteString(iostreams.ListItemSelectedStyle.Render(label))
			b.WriteString("  ")
			b.WriteString(e.value)
		} else {
			b.WriteString(iostreams.MutedStyle.Render(label))
			b.WriteString("  ")
			b.WriteString(iostreams.MutedStyle.Render(e.value))
		}
		b.WriteString("\n")
	}
}

// lookupMapPath walks a dotted path through nested map[string]any.
// Returns the formatted value at the leaf and whether the path was found.
// A found path with an empty-string or nil value still returns found=true
// so callers can distinguish "key absent" from "key present but empty".
func lookupMapPath(data map[string]any, segments []string) (string, bool) {
	current := any(data)
	for _, seg := range segments {
		m, ok := current.(map[string]any)
		if !ok {
			return "", false
		}
		current, ok = m[seg]
		if !ok {
			return "", false
		}
	}
	if current == nil {
		return "", true
	}
	return fmt.Sprintf("%v", current), true
}

func (m *FieldBrowserModel) viewEdit(b *strings.Builder) {
	if m.editIdx < 0 || m.editIdx >= len(m.fields) {
		return
	}
	switch m.editKind {
	case ekSelect:
		b.WriteString(m.selField.View())
	case ekText:
		b.WriteString(m.textField.View())
	case ekList:
		b.WriteString(m.listEditor.View())
	case ekTextarea:
		b.WriteString(m.taEditor.View())
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// fbFormatTabName formats a yaml key for tab display (e.g. "host_proxy" → "Host Proxy").
func fbFormatTabName(key string) string {
	return fbFormatHeading(key)
}

// fbFormatHeading turns a yaml key like "git_credentials" into "Git Credentials".
func fbFormatHeading(key string) string {
	parts := strings.Split(key, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// fbFilterQuit filters out tea.Quit commands from child widgets to prevent
// them from terminating the parent browser.
func fbFilterQuit(cmd tea.Cmd) tea.Cmd {
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
