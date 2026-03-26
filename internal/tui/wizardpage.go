package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// WizardPage is the interface that wizard steps must satisfy.
// Any tea.Model that can report completion and extract a value works as a page.
type WizardPage interface {
	tea.Model
	// IsComplete returns true when the page is done and the wizard should advance.
	IsComplete() bool
	// Value returns the collected value as a string. Empty for pages that
	// persist their own state (e.g. a FieldBrowserModel with per-field save).
	Value() string
}

// EscapeHandler is an optional interface for pages that handle Esc internally
// (e.g. exiting edit mode in a FieldBrowserModel). When HandlesEscape returns
// true, the wizard delegates Esc to the page instead of navigating back.
type EscapeHandler interface {
	HandlesEscape() bool
}

// ---------------------------------------------------------------------------
// selectPage — wraps SelectField (value semantics) for WizardPage
// ---------------------------------------------------------------------------

type selectPage struct {
	field      SelectField
	defaultIdx int
}

// NewSelectPage wraps a SelectField as a WizardPage.
func NewSelectPage(id, prompt string, options []FieldOption, defaultIdx int) WizardPage {
	return &selectPage{
		field:      NewSelectField(id, prompt, options, defaultIdx),
		defaultIdx: defaultIdx,
	}
}

func (p *selectPage) Init() tea.Cmd    { return p.field.Init() }
func (p *selectPage) View() string     { return p.field.View() }
func (p *selectPage) IsComplete() bool { return p.field.IsConfirmed() }
func (p *selectPage) Value() string    { return p.field.Value() }
func (p *selectPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, cmd := p.field.Update(msg)
	p.field = f
	return p, cmd
}

func (p *selectPage) SetSize(w, h int) { p.field = p.field.SetSize(w, h) }

// Reset recreates the underlying field so the page can be re-entered (going back).
func (p *selectPage) Reset() {
	p.field = NewSelectField(p.field.ID, p.field.Prompt, p.field.Options, p.defaultIdx)
}

// ---------------------------------------------------------------------------
// textPage — wraps TextField (value semantics) for WizardPage
// ---------------------------------------------------------------------------

type textPage struct {
	field  TextField
	opts   []TextFieldOption // kept for Reset
	id     string
	prompt string
}

// NewTextPage wraps a TextField as a WizardPage.
func NewTextPage(id, prompt string, opts ...TextFieldOption) WizardPage {
	return &textPage{
		field:  NewTextField(id, prompt, opts...),
		opts:   opts,
		id:     id,
		prompt: prompt,
	}
}

func (p *textPage) Init() tea.Cmd    { return p.field.Init() }
func (p *textPage) View() string     { return p.field.View() }
func (p *textPage) IsComplete() bool { return p.field.IsConfirmed() }
func (p *textPage) Value() string    { return p.field.Value() }
func (p *textPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, cmd := p.field.Update(msg)
	p.field = f
	return p, cmd
}

func (p *textPage) SetSize(w, h int) { p.field = p.field.SetSize(w, h) }

func (p *textPage) Reset() {
	p.field = NewTextField(p.id, p.prompt, p.opts...)
}

// ---------------------------------------------------------------------------
// confirmPage — wraps ConfirmField (value semantics) for WizardPage
// ---------------------------------------------------------------------------

type confirmPage struct {
	field  ConfirmField
	id     string
	prompt string
	defYes bool
}

// NewConfirmPage wraps a ConfirmField as a WizardPage.
func NewConfirmPage(id, prompt string, defaultYes bool) WizardPage {
	return &confirmPage{
		field:  NewConfirmField(id, prompt, defaultYes),
		id:     id,
		prompt: prompt,
		defYes: defaultYes,
	}
}

func (p *confirmPage) Init() tea.Cmd    { return p.field.Init() }
func (p *confirmPage) View() string     { return p.field.View() }
func (p *confirmPage) IsComplete() bool { return p.field.IsConfirmed() }
func (p *confirmPage) Value() string    { return p.field.Value() }
func (p *confirmPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	f, cmd := p.field.Update(msg)
	p.field = f
	return p, cmd
}

func (p *confirmPage) SetSize(w, h int) { p.field = p.field.SetSize(w, h) }

func (p *confirmPage) Reset() {
	p.field = NewConfirmField(p.id, p.prompt, p.defYes)
}

// ---------------------------------------------------------------------------
// browserPage — wraps FieldBrowserModel for WizardPage
// ---------------------------------------------------------------------------

type browserPage struct {
	browser *FieldBrowserModel
	done    bool
}

// NewBrowserPage wraps a FieldBrowserModel as a WizardPage.
// The browser handles per-field saves via its OnFieldSaved callback.
// The page is "complete" when the user quits the browser (q in browse mode).
// Esc in browse mode triggers wizard back-navigation; Esc in edit/picker mode
// is delegated to the browser via the EscapeHandler interface.
func NewBrowserPage(browser *FieldBrowserModel) WizardPage {
	return &browserPage{browser: browser}
}

func (p *browserPage) Init() tea.Cmd    { return p.browser.Init() }
func (p *browserPage) View() string     { return p.browser.View() }
func (p *browserPage) IsComplete() bool { return p.done }
func (p *browserPage) Value() string    { return "" }

func (p *browserPage) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Intercept q in browse mode as "done, advance."
	if km, ok := msg.(tea.KeyMsg); ok && p.browser.InBaseState() {
		if km.String() == "q" {
			p.done = true
			return p, nil
		}
	}

	newModel, cmd := p.browser.Update(msg)
	if bm, ok := newModel.(*FieldBrowserModel); ok {
		p.browser = bm
	}

	return p, filterQuit(cmd)
}

func (p *browserPage) SetSize(w, h int) {
	// FieldBrowserModel handles WindowSizeMsg internally via Update.
	p.browser.Update(tea.WindowSizeMsg{Width: w, Height: h}) //nolint:errcheck
}

// HandlesEscape implements EscapeHandler. Returns true when the browser is
// in an internal state (editing, picking a layer) so the wizard delegates
// Esc to the browser instead of navigating back.
func (p *browserPage) HandlesEscape() bool {
	return !p.browser.InBaseState()
}

func (p *browserPage) Reset() {
	p.done = false
}
