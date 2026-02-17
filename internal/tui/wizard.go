package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// ---------------------------------------------------------------------------
// WizardFieldKind — identifies the type of field in a wizard step
// ---------------------------------------------------------------------------

// WizardFieldKind identifies the type of field in a wizard step.
type WizardFieldKind int

const (
	// FieldSelect is an arrow-key selection field.
	FieldSelect WizardFieldKind = iota
	// FieldText is a text input field.
	FieldText
	// FieldConfirm is a yes/no confirmation field.
	FieldConfirm
)

// ---------------------------------------------------------------------------
// WizardField — defines a single step in the wizard
// ---------------------------------------------------------------------------

// WizardField defines a single step in the wizard.
type WizardField struct {
	ID     string
	Title  string // StepperBar label
	Prompt string // Question text
	Kind   WizardFieldKind

	// Select-specific fields.
	Options    []FieldOption
	DefaultIdx int

	// Text-specific fields.
	Placeholder string
	Default     string
	Validator   func(string) error
	Required    bool

	// Confirm-specific fields.
	DefaultYes bool

	// Conditional: skip this step when predicate returns true.
	SkipIf func(WizardValues) bool
}

// WizardValues is a map of field ID to string value.
type WizardValues map[string]string

// WizardResult is returned by RunWizard.
type WizardResult struct {
	Values    WizardValues
	Submitted bool
}

// ---------------------------------------------------------------------------
// wizardModel — internal BubbleTea model
// ---------------------------------------------------------------------------

// wizardModel implements tea.Model for the multi-step wizard.
// It uses pointer receivers because it mutates maps internally.
type wizardModel struct {
	fields      []WizardField
	currentStep int
	values      WizardValues

	// Active field instances (one per field, created from WizardField defs).
	selectFields  map[int]SelectField
	textFields    map[int]TextField
	confirmFields map[int]ConfirmField

	submitted bool
	cancelled bool
	width     int
	height    int
}

// newWizardModel creates a new wizard model from the given field definitions.
func newWizardModel(fields []WizardField) wizardModel {
	m := wizardModel{
		fields:        fields,
		values:        make(WizardValues),
		selectFields:  make(map[int]SelectField),
		textFields:    make(map[int]TextField),
		confirmFields: make(map[int]ConfirmField),
	}

	// Validate field definitions (programming errors → panic).
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		if f.ID == "" {
			panic("WizardField.ID must not be empty")
		}
		if seen[f.ID] {
			panic(fmt.Sprintf("duplicate WizardField.ID: %q", f.ID))
		}
		seen[f.ID] = true
		if f.Kind == FieldSelect && len(f.Options) == 0 {
			panic(fmt.Sprintf("WizardField %q: FieldSelect requires at least one option", f.ID))
		}
	}

	// Create field instances for each WizardField.
	for i, f := range fields {
		m.createFieldInstance(i, f)
	}

	// Skip to first non-skipped step.
	first := m.nextVisibleStep(-1)
	if first >= 0 {
		m.currentStep = first
	} else {
		// All steps skipped — auto-complete with empty values.
		m.submitted = true
	}

	return m
}

// createFieldInstance creates the appropriate field type for the given index.
func (m *wizardModel) createFieldInstance(idx int, f WizardField) {
	switch f.Kind {
	case FieldSelect:
		m.selectFields[idx] = NewSelectField(f.ID, f.Prompt, f.Options, f.DefaultIdx)
	case FieldText:
		var opts []TextFieldOption
		if f.Placeholder != "" {
			opts = append(opts, WithPlaceholder(f.Placeholder))
		}
		if f.Default != "" {
			opts = append(opts, WithDefault(f.Default))
		}
		if f.Validator != nil {
			opts = append(opts, WithValidator(f.Validator))
		}
		if f.Required {
			opts = append(opts, WithRequired())
		}
		m.textFields[idx] = NewTextField(f.ID, f.Prompt, opts...)
	case FieldConfirm:
		m.confirmFields[idx] = NewConfirmField(f.ID, f.Prompt, f.DefaultYes)
	default:
		panic(fmt.Sprintf("unsupported WizardFieldKind: %d", f.Kind))
	}
}

// ---------------------------------------------------------------------------
// tea.Model implementation (pointer receivers)
// ---------------------------------------------------------------------------

// Init returns a WindowSize command to get terminal dimensions, plus the
// Init cmd for the first active field (e.g., textinput.Blink for TextFields).
func (m *wizardModel) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.WindowSize()}

	// Also init the current field (needed for cursor blink in text fields).
	if cmd := m.currentFieldInit(); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

// Update handles messages for the wizard model.
func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updateAllFieldSizes()
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c":
			m.cancelled = true
			return m, tea.Quit

		case IsEscape(msg):
			return m, m.goBack()

		default:
			return m, m.delegateToCurrentField(msg)
		}
	}

	// Delegate non-key messages (e.g., blink ticks) to current field.
	return m, m.delegateNonKeyToCurrentField(msg)
}

// View renders the wizard: stepper bar, current field, and help bar.
func (m *wizardModel) View() string {
	var b strings.Builder

	// Stepper bar at the top.
	steps := m.buildStepperSteps()
	bar := RenderStepperBar(steps, m.width)
	b.WriteString("  ")
	b.WriteString(bar)
	b.WriteString("\n\n")

	// Current field view.
	b.WriteString(m.currentFieldView())
	b.WriteString("\n\n")

	// Help bar at the bottom.
	b.WriteString("  ")
	b.WriteString(m.helpBar())

	return b.String()
}

// ---------------------------------------------------------------------------
// Navigation helpers
// ---------------------------------------------------------------------------

// nextVisibleStep returns the next non-skipped step index after from, or -1.
func (m *wizardModel) nextVisibleStep(from int) int {
	for i := from + 1; i < len(m.fields); i++ {
		if !m.isStepSkipped(i) {
			return i
		}
	}
	return -1
}

// prevVisibleStep returns the previous non-skipped step index before from, or -1.
func (m *wizardModel) prevVisibleStep(from int) int {
	for i := from - 1; i >= 0; i-- {
		if !m.isStepSkipped(i) {
			return i
		}
	}
	return -1
}

// isStepSkipped checks the SkipIf predicate with current values.
func (m *wizardModel) isStepSkipped(idx int) bool {
	if idx < 0 || idx >= len(m.fields) {
		return false
	}
	f := m.fields[idx]
	if f.SkipIf == nil {
		return false
	}
	return f.SkipIf(m.values)
}

// activateStep moves to step idx, resetting the field if going backwards.
func (m *wizardModel) activateStep(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.fields) {
		return nil
	}

	// If going backward (to a previously completed step), recreate the field
	// so it becomes unconfirmed and editable again.
	if idx < m.currentStep {
		m.createFieldInstance(idx, m.fields[idx])
	}

	m.currentStep = idx
	return m.currentFieldInit()
}

// goBack returns to the previous visible step, or cancels if on the first step.
func (m *wizardModel) goBack() tea.Cmd {
	prev := m.prevVisibleStep(m.currentStep)
	if prev < 0 {
		// On first step, Esc cancels the wizard.
		m.cancelled = true
		return tea.Quit
	}
	return m.activateStep(prev)
}

// ---------------------------------------------------------------------------
// Field delegation
// ---------------------------------------------------------------------------

// delegateToCurrentField sends a key message to the active field and handles
// the result. If the field confirms, the wizard stores the value and advances.
func (m *wizardModel) delegateToCurrentField(msg tea.KeyMsg) tea.Cmd {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.fields) {
		return nil
	}

	f := m.fields[idx]
	switch f.Kind {
	case FieldSelect:
		sf := m.selectFields[idx]
		sf, _ = sf.Update(msg)
		m.selectFields[idx] = sf
		if sf.IsConfirmed() {
			return m.confirmAndAdvance(idx)
		}
		return nil

	case FieldText:
		tf := m.textFields[idx]
		tf, cmd := tf.Update(msg)
		m.textFields[idx] = tf
		if tf.IsConfirmed() {
			return m.confirmAndAdvance(idx)
		}
		// Return cmd for text field (e.g., cursor blink), but filter tea.Quit.
		return filterQuit(cmd)

	case FieldConfirm:
		cf := m.confirmFields[idx]
		cf, _ = cf.Update(msg)
		m.confirmFields[idx] = cf
		if cf.IsConfirmed() {
			return m.confirmAndAdvance(idx)
		}
		return nil
	}
	return nil
}

// delegateNonKeyToCurrentField sends non-key messages (like blink ticks) to
// the current field.
func (m *wizardModel) delegateNonKeyToCurrentField(msg tea.Msg) tea.Cmd {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.fields) {
		return nil
	}

	f := m.fields[idx]
	if f.Kind == FieldText {
		tf := m.textFields[idx]
		tf, cmd := tf.Update(msg)
		m.textFields[idx] = tf
		return filterQuit(cmd)
	}
	return nil
}

// confirmAndAdvance stores the current field's value and moves to the next step.
// If this is the last step, sets submitted=true and returns tea.Quit.
func (m *wizardModel) confirmAndAdvance(idx int) tea.Cmd {
	// Store the value.
	m.values[m.fields[idx].ID] = m.currentFieldValue(idx)

	// Find next visible step.
	next := m.nextVisibleStep(idx)
	if next < 0 {
		// No more steps — wizard is complete.
		m.submitted = true
		return tea.Quit
	}

	return m.activateStep(next)
}

// currentFieldValue returns the string value of the field at the given index.
func (m *wizardModel) currentFieldValue(idx int) string {
	f := m.fields[idx]
	switch f.Kind {
	case FieldSelect:
		return m.selectFields[idx].Value()
	case FieldText:
		return m.textFields[idx].Value()
	case FieldConfirm:
		return m.confirmFields[idx].Value()
	default:
		return ""
	}
}

// currentFieldInit returns the Init cmd for the current step's field.
func (m *wizardModel) currentFieldInit() tea.Cmd {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.fields) {
		return nil
	}

	f := m.fields[idx]
	switch f.Kind {
	case FieldSelect:
		return m.selectFields[idx].Init()
	case FieldText:
		return m.textFields[idx].Init()
	case FieldConfirm:
		return m.confirmFields[idx].Init()
	}
	return nil
}

// currentFieldView returns the View string for the current step's field.
func (m *wizardModel) currentFieldView() string {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.fields) {
		return ""
	}

	f := m.fields[idx]
	switch f.Kind {
	case FieldSelect:
		return m.selectFields[idx].View()
	case FieldText:
		return m.textFields[idx].View()
	case FieldConfirm:
		return m.confirmFields[idx].View()
	}
	return ""
}

// ---------------------------------------------------------------------------
// Size management
// ---------------------------------------------------------------------------

// updateAllFieldSizes propagates the terminal dimensions to all field instances.
// Reserves lines for: stepper bar (1) + gap after stepper (1) + gap before help (1) + help bar (1) = 4.
func (m *wizardModel) updateAllFieldSizes() {
	fieldHeight := m.height - 4
	if fieldHeight < 3 {
		fieldHeight = 3
	}

	for i, f := range m.fields {
		switch f.Kind {
		case FieldSelect:
			sf := m.selectFields[i]
			m.selectFields[i] = sf.SetSize(m.width, fieldHeight)
		case FieldText:
			tf := m.textFields[i]
			m.textFields[i] = tf.SetSize(m.width, fieldHeight)
		case FieldConfirm:
			cf := m.confirmFields[i]
			m.confirmFields[i] = cf.SetSize(m.width, fieldHeight)
		}
	}
}

// ---------------------------------------------------------------------------
// Stepper bar construction
// ---------------------------------------------------------------------------

// buildStepperSteps creates Step entries from the wizard field definitions
// based on current wizard state.
func (m *wizardModel) buildStepperSteps() []Step {
	steps := make([]Step, len(m.fields))

	for i, f := range m.fields {
		step := Step{
			Title: f.Title,
		}

		switch {
		case m.isStepSkipped(i):
			step.State = StepSkippedState
		case i < m.currentStep:
			step.State = StepCompleteState
			step.Value = m.values[f.ID]
		case i == m.currentStep:
			step.State = StepActiveState
		default:
			step.State = StepPendingState
		}

		steps[i] = step
	}

	return steps
}

// ---------------------------------------------------------------------------
// Help bar
// ---------------------------------------------------------------------------

// helpBar returns contextual help text based on the current field kind.
func (m *wizardModel) helpBar() string {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.fields) {
		return ""
	}

	f := m.fields[idx]
	switch f.Kind {
	case FieldSelect:
		return QuickHelp(
			"\u2191\u2193", "select",
			"enter", "confirm",
			"esc", "back",
			"ctrl+c", "quit",
		)
	case FieldText:
		return QuickHelp(
			"enter", "confirm",
			"esc", "back",
			"ctrl+c", "quit",
		)
	case FieldConfirm:
		return QuickHelp(
			"←→", "toggle",
			"y/n", "set",
			"enter", "confirm",
			"esc", "back",
			"ctrl+c", "quit",
		)
	}
	return ""
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// filterQuit returns nil if cmd would produce a tea.QuitMsg, otherwise returns
// cmd unchanged. This prevents individual fields from quitting the wizard.
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

// String returns a debug-friendly name for WizardFieldKind.
func (k WizardFieldKind) String() string {
	switch k {
	case FieldSelect:
		return "select"
	case FieldText:
		return "text"
	case FieldConfirm:
		return "confirm"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// Ensure wizardModel satisfies tea.Model at compile time.
var _ tea.Model = (*wizardModel)(nil)
