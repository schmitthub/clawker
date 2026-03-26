package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// WizardValues is a map of step ID to string value.
type WizardValues map[string]string

// WizardResult is returned by RunWizard.
type WizardResult struct {
	Values    WizardValues
	Submitted bool
}

// WizardStep defines a single step in the wizard. Each step wraps a
// pre-constructed WizardPage — the wizard is a generic page sequencer
// that delegates rendering and input to the page model.
type WizardStep struct {
	ID     string
	Title  string // StepperBar label
	Page   WizardPage
	SkipIf func(WizardValues) bool
	// HelpKeys provides custom help bar entries for this step.
	// If nil, no help bar is shown for this step.
	HelpKeys []string // alternating key, description pairs for QuickHelp
}

// ---------------------------------------------------------------------------
// wizardModel — generic page-sequencing BubbleTea model
// ---------------------------------------------------------------------------

type wizardModel struct {
	steps       []WizardStep
	currentStep int
	values      WizardValues
	submitted   bool
	cancelled   bool
	width       int
	height      int
}

func newWizardModel(steps []WizardStep) wizardModel {
	m := wizardModel{
		steps:  steps,
		values: make(WizardValues),
	}

	// Validate step definitions (programming errors → panic).
	seen := make(map[string]bool, len(steps))
	for _, s := range steps {
		if s.ID == "" {
			panic("WizardStep.ID must not be empty")
		}
		if seen[s.ID] {
			panic(fmt.Sprintf("duplicate WizardStep.ID: %q", s.ID))
		}
		seen[s.ID] = true
		if s.Page == nil {
			panic(fmt.Sprintf("WizardStep %q: Page must not be nil", s.ID))
		}
	}

	// Skip to first non-skipped step.
	first := m.nextVisibleStep(-1)
	if first >= 0 {
		m.currentStep = first
	} else {
		m.submitted = true
	}

	return m
}

// ---------------------------------------------------------------------------
// tea.Model implementation
// ---------------------------------------------------------------------------

func (m *wizardModel) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.WindowSize()}
	if idx := m.currentStep; idx >= 0 && idx < len(m.steps) {
		if cmd := m.steps[idx].Page.Init(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (m *wizardModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.propagateSize()
		return m, nil

	case tea.KeyMsg:
		switch {
		case msg.String() == "ctrl+c":
			m.cancelled = true
			return m, tea.Quit
		case IsEscape(msg):
			// If the current page handles Esc internally (e.g. exiting edit
			// mode in a FieldBrowser), delegate instead of navigating back.
			if eh, ok := m.currentPage().(EscapeHandler); ok && eh.HandlesEscape() {
				return m, m.delegateToPage(msg)
			}
			return m, m.goBack()
		}
	}

	// Delegate to current page.
	return m, m.delegateToPage(msg)
}

func (m *wizardModel) View() string {
	var b strings.Builder

	steps := m.buildStepperSteps()
	bar := RenderStepperBar(steps, m.width)
	b.WriteString("  ")
	b.WriteString(bar)
	b.WriteString("\n\n")

	if idx := m.currentStep; idx >= 0 && idx < len(m.steps) {
		b.WriteString(m.steps[idx].Page.View())
	}
	b.WriteString("\n\n")

	b.WriteString("  ")
	b.WriteString(m.helpBar())

	return b.String()
}

// ---------------------------------------------------------------------------
// Navigation
// ---------------------------------------------------------------------------

func (m *wizardModel) nextVisibleStep(from int) int {
	for i := from + 1; i < len(m.steps); i++ {
		if !m.isStepSkipped(i) {
			return i
		}
	}
	return -1
}

func (m *wizardModel) prevVisibleStep(from int) int {
	for i := from - 1; i >= 0; i-- {
		if !m.isStepSkipped(i) {
			return i
		}
	}
	return -1
}

func (m *wizardModel) isStepSkipped(idx int) bool {
	if idx < 0 || idx >= len(m.steps) {
		return false
	}
	s := m.steps[idx]
	if s.SkipIf == nil {
		return false
	}
	return s.SkipIf(m.values)
}

// activateStep moves to step idx. When going backward, resets the page
// via the Resetter interface so it becomes editable again.
func (m *wizardModel) activateStep(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.steps) {
		return nil
	}
	if idx < m.currentStep {
		if r, ok := m.steps[idx].Page.(Resetter); ok {
			r.Reset()
		}
	}
	m.currentStep = idx
	return m.steps[idx].Page.Init()
}

func (m *wizardModel) goBack() tea.Cmd {
	prev := m.prevVisibleStep(m.currentStep)
	if prev < 0 {
		m.cancelled = true
		return tea.Quit
	}
	return m.activateStep(prev)
}

// ---------------------------------------------------------------------------
// Page delegation
// ---------------------------------------------------------------------------

func (m *wizardModel) currentPage() WizardPage {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.steps) {
		return nil
	}
	return m.steps[idx].Page
}

func (m *wizardModel) delegateToPage(msg tea.Msg) tea.Cmd {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.steps) {
		return nil
	}

	page := m.steps[idx].Page
	newModel, cmd := page.Update(msg)
	if p, ok := newModel.(WizardPage); ok {
		m.steps[idx].Page = p
	}

	// Check if the page signalled cancellation.
	if cp, ok := m.steps[idx].Page.(CancelledPage); ok && cp.IsCancelled() {
		m.cancelled = true
		return tea.Quit
	}

	// Check if the page completed after this update.
	if m.steps[idx].Page.IsComplete() {
		return m.completeAndAdvance(idx)
	}

	return filterQuit(cmd)
}

func (m *wizardModel) completeAndAdvance(idx int) tea.Cmd {
	m.values[m.steps[idx].ID] = m.steps[idx].Page.Value()

	next := m.nextVisibleStep(idx)
	if next < 0 {
		m.submitted = true
		return tea.Quit
	}
	return m.activateStep(next)
}

// ---------------------------------------------------------------------------
// Size propagation
// ---------------------------------------------------------------------------

func (m *wizardModel) propagateSize() {
	fieldHeight := m.height - 4
	if fieldHeight < 3 {
		fieldHeight = 3
	}
	for i := range m.steps {
		if sp, ok := m.steps[i].Page.(interface{ SetSize(int, int) }); ok {
			sp.SetSize(m.width, fieldHeight)
		}
	}
}

// ---------------------------------------------------------------------------
// Stepper bar
// ---------------------------------------------------------------------------

func (m *wizardModel) buildStepperSteps() []Step {
	steps := make([]Step, len(m.steps))
	for i, s := range m.steps {
		step := Step{Title: s.Title}
		switch {
		case m.isStepSkipped(i):
			step.State = StepSkippedState
		case i < m.currentStep:
			step.State = StepCompleteState
			step.Value = m.values[s.ID]
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

func (m *wizardModel) helpBar() string {
	idx := m.currentStep
	if idx < 0 || idx >= len(m.steps) {
		return ""
	}
	s := m.steps[idx]
	if len(s.HelpKeys) > 0 {
		return QuickHelp(s.HelpKeys...)
	}
	// Default: just esc/quit.
	return QuickHelp("esc", "back", "ctrl+c", "quit")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Resetter is an optional interface for pages that support being reset
// when the user navigates backward in the wizard.
type Resetter interface {
	Reset()
}

// CancelledPage is an optional interface for pages that can signal
// cancellation (e.g. a browser page with a configured cancel key).
// When IsCancelled returns true, the wizard treats it as a full cancel.
type CancelledPage interface {
	IsCancelled() bool
}

// filterQuit intercepts tea.Quit from pages to prevent them from
// terminating the wizard. The wizard manages its own quit lifecycle.
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

var _ tea.Model = (*wizardModel)(nil)
