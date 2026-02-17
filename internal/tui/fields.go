package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// ---------------------------------------------------------------------------
// FieldOption — shared option type for select fields and wizard definitions
// ---------------------------------------------------------------------------

// FieldOption represents a selectable option with a label and description.
type FieldOption struct {
	Label       string
	Description string
}

// ---------------------------------------------------------------------------
// SelectField — arrow-key selection wrapping ListModel
// ---------------------------------------------------------------------------

// SelectField is a standalone BubbleTea model for arrow-key selection.
// It wraps ListModel for navigation state but renders its own compact view
// with label + description on a single line.
type SelectField struct {
	ID        string
	Prompt    string
	Options   []FieldOption
	list      ListModel
	confirmed bool
}

// NewSelectField creates a new SelectField with the given options.
// defaultIdx sets the initially selected option (clamped to valid range).
func NewSelectField(id, prompt string, options []FieldOption, defaultIdx int) SelectField {
	items := make([]ListItem, len(options))
	for i, opt := range options {
		items[i] = SimpleListItem{
			ItemTitle:       opt.Label,
			ItemDescription: opt.Description,
		}
	}

	cfg := DefaultListConfig()
	cfg.ShowDescriptions = false
	cfg.Wrap = true

	list := NewList(cfg)
	list = list.SetItems(items)
	if defaultIdx >= 0 && defaultIdx < len(options) {
		list = list.Select(defaultIdx)
	}

	return SelectField{
		ID:      id,
		Prompt:  prompt,
		Options: options,
		list:    list,
	}
}

// Init returns nil — no initial command is needed.
func (f SelectField) Init() tea.Cmd {
	return nil
}

// Update handles key messages. Up/Down delegate to the internal list.
// Enter confirms the selection and sends tea.Quit for standalone use.
func (f SelectField) Update(msg tea.Msg) (SelectField, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsEnter(msg):
			f.confirmed = true
			return f, tea.Quit
		case msg.String() == "ctrl+c":
			return f, tea.Quit
		default:
			var cmd tea.Cmd
			f.list, cmd = f.list.Update(msg)
			return f, cmd
		}
	}
	return f, nil
}

// View renders the select field with prompt and compact option list.
// Each option shows label and description on one line:
//
//	> bookworm     Debian stable (Recommended)
//	  trixie       Debian testing
func (f SelectField) View() string {
	promptStyle := iostreams.PanelTitleStyle
	selectedStyle := iostreams.ListItemSelectedStyle
	dimStyle := iostreams.ListItemDimStyle

	var b strings.Builder

	// Prompt line
	b.WriteString("  ")
	b.WriteString(promptStyle.Render(f.Prompt))
	b.WriteString("\n\n")

	// Calculate max label width for alignment
	maxLabelWidth := 0
	for _, opt := range f.Options {
		if len(opt.Label) > maxLabelWidth {
			maxLabelWidth = len(opt.Label)
		}
	}

	selectedIdx := f.list.SelectedIndex()
	for i, opt := range f.Options {
		selected := i == selectedIdx

		// Prefix: "> " for selected, "  " for unselected
		if selected {
			b.WriteString("  > ")
		} else {
			b.WriteString("    ")
		}

		// Label (padded for alignment)
		label := text.PadRight(opt.Label, maxLabelWidth)
		if selected {
			b.WriteString(selectedStyle.Render(label))
		} else {
			b.WriteString(label)
		}

		// Description (dimmed)
		if opt.Description != "" {
			b.WriteString("  ")
			b.WriteString(dimStyle.Render(opt.Description))
		}

		if i < len(f.Options)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// Value returns the label of the currently selected option.
func (f SelectField) Value() string {
	item := f.list.SelectedItem()
	if item == nil {
		return ""
	}
	return item.Title()
}

// SelectedIndex returns the index of the currently selected option.
func (f SelectField) SelectedIndex() int {
	return f.list.SelectedIndex()
}

// IsConfirmed returns true if the user has confirmed their selection.
func (f SelectField) IsConfirmed() bool {
	return f.confirmed
}

// SetSize sets the width and height available for rendering.
func (f SelectField) SetSize(w, h int) SelectField {
	f.list = f.list.SetWidth(w).SetHeight(h)
	return f
}

// ---------------------------------------------------------------------------
// TextField — text input wrapping bubbles/textinput
// ---------------------------------------------------------------------------

// TextField is a standalone BubbleTea model for text input.
// It wraps the bubbles textinput component with validation support.
type TextField struct {
	ID        string
	Prompt    string
	input     textinput.Model
	validator func(string) error
	required  bool
	confirmed bool
	errMsg    string
}

// TextFieldOption is a functional option for configuring a TextField.
type TextFieldOption func(*TextField)

// WithPlaceholder sets the placeholder text shown when the input is empty.
func WithPlaceholder(s string) TextFieldOption {
	return func(f *TextField) {
		f.input.Placeholder = s
	}
}

// WithDefault sets the initial value of the text input.
func WithDefault(s string) TextFieldOption {
	return func(f *TextField) {
		f.input.SetValue(s)
	}
}

// WithValidator sets a validation function called on Enter.
// If the function returns an error, the field displays it and does not confirm.
func WithValidator(fn func(string) error) TextFieldOption {
	return func(f *TextField) {
		f.validator = fn
	}
}

// WithRequired marks the field as required — an empty value is rejected on Enter.
func WithRequired() TextFieldOption {
	return func(f *TextField) {
		f.required = true
	}
}

// NewTextField creates a new TextField with the given options.
func NewTextField(id, prompt string, opts ...TextFieldOption) TextField {
	ti := textinput.New()
	ti.Focus()

	f := TextField{
		ID:     id,
		Prompt: prompt,
		input:  ti,
	}
	for _, opt := range opts {
		opt(&f)
	}
	return f
}

// Init returns the textinput blink command to start cursor blinking.
func (f TextField) Init() tea.Cmd {
	return textinput.Blink
}

// Update handles key messages. Enter validates and confirms.
// All other keys are delegated to the underlying textinput.
func (f TextField) Update(msg tea.Msg) (TextField, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsEnter(msg):
			// Clear any previous error
			f.errMsg = ""

			val := f.input.Value()

			// Validate required
			if f.required && strings.TrimSpace(val) == "" {
				f.errMsg = "This field is required"
				return f, nil
			}

			// Run custom validator
			if f.validator != nil {
				if err := f.validator(val); err != nil {
					f.errMsg = err.Error()
					return f, nil
				}
			}

			f.confirmed = true
			return f, tea.Quit

		case msg.String() == "ctrl+c":
			return f, tea.Quit
		}
	}

	// Delegate all other messages to textinput
	var cmd tea.Cmd
	f.input, cmd = f.input.Update(msg)
	return f, cmd
}

// View renders the text field with prompt, input, and optional error message.
func (f TextField) View() string {
	promptStyle := iostreams.PanelTitleStyle
	errStyle := iostreams.ErrorStyle

	var b strings.Builder

	// Prompt line
	b.WriteString("  ")
	b.WriteString(promptStyle.Render(f.Prompt))
	b.WriteString("\n\n")

	// Text input
	b.WriteString("  ")
	b.WriteString(f.input.View())

	// Error message
	if f.errMsg != "" {
		b.WriteString("\n  ")
		b.WriteString(errStyle.Render(fmt.Sprintf("! %s", f.errMsg)))
	}

	return b.String()
}

// Value returns the current text input value.
func (f TextField) Value() string {
	return f.input.Value()
}

// IsConfirmed returns true if the user has confirmed the input.
func (f TextField) IsConfirmed() bool {
	return f.confirmed
}

// Err returns the current validation error message, or empty string if none.
func (f TextField) Err() string {
	return f.errMsg
}

// SetSize sets the width available for the text input.
func (f TextField) SetSize(w, h int) TextField {
	inputWidth := w - 4 // Account for indentation
	if inputWidth < 1 {
		inputWidth = 1
	}
	f.input.Width = inputWidth
	return f
}

// ---------------------------------------------------------------------------
// ConfirmField — yes/no toggle
// ---------------------------------------------------------------------------

// ConfirmField is a standalone BubbleTea model for yes/no confirmation.
type ConfirmField struct {
	ID        string
	Prompt    string
	value     bool
	confirmed bool
}

// NewConfirmField creates a new ConfirmField with the given default value.
func NewConfirmField(id, prompt string, defaultYes bool) ConfirmField {
	return ConfirmField{
		ID:     id,
		Prompt: prompt,
		value:  defaultYes,
	}
}

// Init returns nil — no initial command is needed.
func (f ConfirmField) Init() tea.Cmd {
	return nil
}

// Update handles key messages. Left/Right/Tab toggle the value.
// Enter confirms. 'y' sets true, 'n' sets false.
func (f ConfirmField) Update(msg tea.Msg) (ConfirmField, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch {
		case IsEnter(msg):
			f.confirmed = true
			return f, tea.Quit
		case msg.String() == "ctrl+c":
			return f, tea.Quit
		case IsLeft(msg), IsRight(msg), IsTab(msg):
			f.value = !f.value
			return f, nil
		case msg.String() == "y" || msg.String() == "Y":
			f.value = true
			return f, nil
		case msg.String() == "n" || msg.String() == "N":
			f.value = false
			return f, nil
		}
	}
	return f, nil
}

// View renders the confirm field with prompt and [ Yes ] / [ No ] toggle.
func (f ConfirmField) View() string {
	promptStyle := iostreams.PanelTitleStyle
	activeStyle := iostreams.ListItemSelectedStyle
	inactiveStyle := iostreams.MutedStyle

	var b strings.Builder

	// Prompt line
	b.WriteString("  ")
	b.WriteString(promptStyle.Render(f.Prompt))
	b.WriteString("\n\n")

	// Yes/No options side by side
	b.WriteString("  ")
	if f.value {
		b.WriteString(activeStyle.Render("[ Yes ]"))
		b.WriteString("  ")
		b.WriteString(inactiveStyle.Render("[ No ]"))
	} else {
		b.WriteString(inactiveStyle.Render("[ Yes ]"))
		b.WriteString("  ")
		b.WriteString(activeStyle.Render("[ No ]"))
	}

	return b.String()
}

// Value returns "yes" or "no" based on the current toggle state.
func (f ConfirmField) Value() string {
	if f.value {
		return "yes"
	}
	return "no"
}

// BoolValue returns the boolean value of the toggle.
func (f ConfirmField) BoolValue() bool {
	return f.value
}

// IsConfirmed returns true if the user has confirmed their choice.
func (f ConfirmField) IsConfirmed() bool {
	return f.confirmed
}

// SetSize satisfies the wizard's updateAllFieldSizes contract.
// ConfirmField renders at a fixed layout, so width/height are unused.
func (f ConfirmField) SetSize(w, h int) ConfirmField {
	return f
}
