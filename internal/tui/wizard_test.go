package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/schmitthub/clawker/internal/iostreams"
)

// ---------------------------------------------------------------------------
// Wizard step navigation tests
// ---------------------------------------------------------------------------

func TestWizard_StepNavigation(t *testing.T) {
	fields := []WizardField{
		{ID: "step1", Title: "Step 1", Prompt: "Confirm step 1?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "step2", Title: "Step 2", Prompt: "Pick one", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Alpha", Description: "First"},
			{Label: "Beta", Description: "Second"},
		}},
		{ID: "step3", Title: "Step 3", Prompt: "Confirm step 3?", Kind: FieldConfirm, DefaultYes: false},
	}

	m := newWizardModel(fields)
	model := &m

	// Send initial window size for realistic sizing.
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Should start at step 0.
	assert.Equal(t, 0, model.currentStep)

	// Confirm step 0 (Enter on ConfirmField).
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep, "should advance to step 1 after confirming step 0")

	// Confirm step 1 (Enter on SelectField with default selection).
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, model.currentStep, "should advance to step 2 after confirming step 1")

	// Go back with Esc.
	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 1, model.currentStep, "should go back to step 1 on Esc")

	// Go back again.
	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, model.currentStep, "should go back to step 0 on Esc")
}

// ---------------------------------------------------------------------------
// Wizard conditional skip tests
// ---------------------------------------------------------------------------

func TestWizard_ConditionalSkip(t *testing.T) {
	fields := []WizardField{
		{ID: "build", Title: "Build", Prompt: "Build image?", Kind: FieldConfirm, DefaultYes: false},
		{ID: "flavor", Title: "Flavor", Prompt: "Pick flavor", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
		}, SkipIf: func(vals WizardValues) bool {
			return vals["build"] == "no"
		}},
		{ID: "submit", Title: "Submit", Prompt: "Submit?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Step 0: ConfirmField with DefaultYes=false, so default value is "no".
	// Confirm it without toggling — value stays "no".
	assert.Equal(t, 0, model.currentStep)
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Step 1 should be skipped (build == "no"), so we jump to step 2.
	assert.Equal(t, 2, model.currentStep, "step 1 should be skipped when build=no")

	// Go back with Esc — should skip step 1 going backwards too.
	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, model.currentStep, "should skip step 1 and go back to step 0")
}

// ---------------------------------------------------------------------------
// Wizard cancel tests
// ---------------------------------------------------------------------------

func TestWizard_Cancel(t *testing.T) {
	fields := []WizardField{
		{ID: "step1", Title: "Step 1", Prompt: "First?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "step2", Title: "Step 2", Prompt: "Second?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// On the first step, press Esc — should cancel the wizard.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, model.cancelled, "Esc on first step should cancel")
	assert.False(t, model.submitted, "cancelled wizard should not be submitted")

	// Verify tea.Quit was returned.
	require.NotNil(t, cmd, "Esc on first step should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")
}

// ---------------------------------------------------------------------------
// Wizard Ctrl+C tests
// ---------------------------------------------------------------------------

func TestWizard_CtrlC(t *testing.T) {
	fields := []WizardField{
		{ID: "step1", Title: "Step 1", Prompt: "First?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "step2", Title: "Step 2", Prompt: "Second?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Advance to step 1 first to test Ctrl+C at a non-first step.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep)

	// Press Ctrl+C.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.True(t, model.cancelled, "Ctrl+C should cancel")
	assert.False(t, model.submitted, "Ctrl+C should not submit")

	// Verify tea.Quit was returned.
	require.NotNil(t, cmd, "Ctrl+C should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")
}

// ---------------------------------------------------------------------------
// Wizard submit and values collection tests
// ---------------------------------------------------------------------------

func TestWizard_SubmitCollectsValues(t *testing.T) {
	fields := []WizardField{
		{ID: "build", Title: "Build", Prompt: "Build image?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "flavor", Title: "Flavor", Prompt: "Pick flavor", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
			{Label: "Strawberry", Description: "Fruity"},
		}, DefaultIdx: 0},
		{ID: "submit", Title: "Submit", Prompt: "Submit?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Step 0: Confirm build (DefaultYes=true, so value is "yes").
	assert.Equal(t, 0, model.currentStep)
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep)

	// Step 1: Select flavor — navigate down to "Chocolate" and confirm.
	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, model.currentStep)

	// Step 2: Confirm submit (DefaultYes=true, so value is "yes").
	// This is the last step — submitting should complete the wizard.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, model.submitted, "wizard should be submitted after confirming last step")
	assert.False(t, model.cancelled, "submitted wizard should not be cancelled")

	// Verify tea.Quit was returned.
	require.NotNil(t, cmd, "final confirmation should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")

	// Verify collected values.
	assert.Equal(t, "yes", model.values["build"], "build should be 'yes'")
	assert.Equal(t, "Chocolate", model.values["flavor"], "flavor should be 'Chocolate'")
	assert.Equal(t, "yes", model.values["submit"], "submit should be 'yes'")
}

// ---------------------------------------------------------------------------
// Wizard stepper bar state tests
// ---------------------------------------------------------------------------

func TestWizard_NavBarUpdates(t *testing.T) {
	fields := []WizardField{
		{ID: "step1", Title: "First Step", Prompt: "Do first?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "step2", Title: "Second Step", Prompt: "Do second?", Kind: FieldConfirm, DefaultYes: false},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Before confirming anything — step 0 active, step 1 pending.
	steps := model.buildStepperSteps()
	assert.Equal(t, StepActiveState, steps[0].State, "step 0 should be active initially")
	assert.Equal(t, StepPendingState, steps[1].State, "step 1 should be pending initially")

	// Confirm step 0.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Now step 0 should be complete with value, step 1 active.
	steps = model.buildStepperSteps()
	assert.Equal(t, StepCompleteState, steps[0].State, "step 0 should be complete after confirming")
	assert.Equal(t, "yes", steps[0].Value, "step 0 value should be 'yes'")
	assert.Equal(t, StepActiveState, steps[1].State, "step 1 should be active")
}

// ---------------------------------------------------------------------------
// Wizard View rendering tests
// ---------------------------------------------------------------------------

func TestWizard_View(t *testing.T) {
	fields := []WizardField{
		{ID: "build", Title: "Build", Prompt: "Build image?", Kind: FieldConfirm, DefaultYes: true},
		{ID: "flavor", Title: "Flavor", Prompt: "Pick flavor", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
		}},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := model.View()

	// View should contain elements from the stepper bar (step titles).
	assert.Contains(t, view, "Build", "view should contain stepper bar step title")
	assert.Contains(t, view, "Flavor", "view should contain stepper bar step title")

	// View should contain the current field's prompt.
	assert.Contains(t, view, "Build image?", "view should contain the current field prompt")

	// View should contain help bar elements.
	assert.Contains(t, view, "enter", "view should contain help bar key bindings")
	assert.Contains(t, view, "confirm", "view should contain help bar descriptions")
	assert.Contains(t, view, "esc", "view should contain esc in help bar")
}

// ---------------------------------------------------------------------------
// Wizard window size tests
// ---------------------------------------------------------------------------

func TestWizard_WindowSize(t *testing.T) {
	fields := []WizardField{
		{ID: "step1", Title: "Step 1", Prompt: "First?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m

	// Initial dimensions should be zero.
	assert.Equal(t, 0, model.width)
	assert.Equal(t, 0, model.height)

	// Send WindowSizeMsg.
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Equal(t, 100, model.width, "width should be updated to 100")
	assert.Equal(t, 30, model.height, "height should be updated to 30")

	// Send another WindowSizeMsg to verify update.
	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.Equal(t, 120, model.width, "width should be updated to 120")
	assert.Equal(t, 40, model.height, "height should be updated to 40")
}

// ---------------------------------------------------------------------------
// Wizard SkipIf re-evaluation on back-navigation
// ---------------------------------------------------------------------------

func TestWizard_SkipIfReevaluation(t *testing.T) {
	fields := []WizardField{
		{ID: "build", Title: "Build", Prompt: "Build image?", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Yes", Description: "Build it"},
			{Label: "No", Description: "Skip it"},
		}, DefaultIdx: 1}, // Default to "No"
		{ID: "flavor", Title: "Flavor", Prompt: "Pick flavor", Kind: FieldSelect, Options: []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
		}, SkipIf: func(vals WizardValues) bool {
			return vals["build"] == "No"
		}},
		{ID: "submit", Title: "Submit", Prompt: "Submit?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Step 0: Default is "No" (index 1). Confirm without changing.
	assert.Equal(t, 0, model.currentStep)
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Step 1 should be skipped (build == "No"), land on step 2.
	assert.Equal(t, 2, model.currentStep, "step 1 should be skipped when build=No")

	// Go back to step 0 (skip step 1 backwards).
	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, model.currentStep, "should go back to step 0")

	// Change to "Yes" (navigate up to index 0).
	model.Update(tea.KeyMsg{Type: tea.KeyUp})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	// Now step 1 should NOT be skipped (build == "Yes"), so we land on step 1.
	assert.Equal(t, 1, model.currentStep, "step 1 should be visible after changing build to Yes")

	// Confirm step 1.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, model.currentStep, "should advance to step 2")
}

// ---------------------------------------------------------------------------
// Wizard with FieldText step
// ---------------------------------------------------------------------------

func TestWizard_TextFieldInWizard(t *testing.T) {
	fields := []WizardField{
		{ID: "name", Title: "Name", Prompt: "Enter project name", Kind: FieldText, Default: "my-project"},
		{ID: "submit", Title: "Submit", Prompt: "Submit?", Kind: FieldConfirm, DefaultYes: true},
	}

	m := newWizardModel(fields)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Initialize the text field (activates blink cursor).
	model.Init()

	// Step 0 should be a TextField. Type some characters to replace default.
	assert.Equal(t, 0, model.currentStep)

	// Clear the default value first: select all and delete (ctrl+a then delete won't work in test,
	// but we can just verify the default gets passed through).
	// Press Enter to confirm the text field with default value.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep, "should advance to step 1 after confirming text field")

	// Verify the value was collected.
	assert.Equal(t, "my-project", model.values["name"], "text field should have default value")

	// Confirm submit.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, model.submitted, "wizard should be submitted")
	require.NotNil(t, cmd)
}

// ---------------------------------------------------------------------------
// Wizard with empty fields slice
// ---------------------------------------------------------------------------

func TestWizard_EmptyFields(t *testing.T) {
	ios := iostreams.NewTestIOStreams()
	tui := NewTUI(ios.IOStreams)

	result, err := tui.RunWizard(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wizard requires at least one field")
	assert.False(t, result.Submitted)

	result, err = tui.RunWizard([]WizardField{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wizard requires at least one field")
	assert.False(t, result.Submitted)
}

// ---------------------------------------------------------------------------
// filterQuit tests
// ---------------------------------------------------------------------------

func TestFilterQuit(t *testing.T) {
	t.Run("nil cmd returns nil", func(t *testing.T) {
		result := filterQuit(nil)
		assert.Nil(t, result, "filterQuit(nil) should return nil")
	})

	t.Run("quit cmd is filtered", func(t *testing.T) {
		quitCmd := tea.Quit
		result := filterQuit(quitCmd)
		require.NotNil(t, result, "filterQuit should return a non-nil cmd for quit")

		// The returned cmd should produce nil (quit suppressed).
		msg := result()
		assert.Nil(t, msg, "filtered quit cmd should produce nil msg")
	})

	t.Run("non-quit cmd passes through", func(t *testing.T) {
		type customMsg struct{ value string }
		original := func() tea.Msg { return customMsg{value: "hello"} }

		result := filterQuit(original)
		require.NotNil(t, result, "filterQuit should return a non-nil cmd")

		msg := result()
		custom, ok := msg.(customMsg)
		require.True(t, ok, "message should be customMsg type")
		assert.Equal(t, "hello", custom.value, "message should pass through unchanged")
	})
}

// ---------------------------------------------------------------------------
// Wizard validation panic tests
// ---------------------------------------------------------------------------

func TestWizard_ValidationPanics(t *testing.T) {
	t.Run("empty field ID", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardField{
				{ID: "", Title: "Bad", Prompt: "Bad?", Kind: FieldConfirm},
			})
		}, "empty field ID should panic")
	})

	t.Run("duplicate field IDs", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardField{
				{ID: "dup", Title: "First", Prompt: "First?", Kind: FieldConfirm},
				{ID: "dup", Title: "Second", Prompt: "Second?", Kind: FieldConfirm},
			})
		}, "duplicate field IDs should panic")
	})

	t.Run("select with empty options", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardField{
				{ID: "sel", Title: "Pick", Prompt: "Pick one", Kind: FieldSelect, Options: []FieldOption{}},
			})
		}, "FieldSelect with empty Options should panic")
	})
}
