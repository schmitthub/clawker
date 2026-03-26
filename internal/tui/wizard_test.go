package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Wizard step navigation tests
// ---------------------------------------------------------------------------

func TestWizard_StepNavigation(t *testing.T) {
	steps := []WizardStep{
		{ID: "step1", Title: "Step 1", Page: NewConfirmPage("step1", "Confirm step 1?", true)},
		{ID: "step2", Title: "Step 2", Page: NewSelectPage("step2", "Pick one", []FieldOption{
			{Label: "Alpha", Description: "First"},
			{Label: "Beta", Description: "Second"},
		}, 0)},
		{ID: "step3", Title: "Step 3", Page: NewConfirmPage("step3", "Confirm step 3?", false)},
	}

	m := newWizardModel(steps)
	model := &m

	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	assert.Equal(t, 0, model.currentStep)

	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep, "should advance to step 1 after confirming step 0")

	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, model.currentStep, "should advance to step 2 after confirming step 1")

	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 1, model.currentStep, "should go back to step 1 on Esc")

	model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, 0, model.currentStep, "should go back to step 0 on Esc")
}

// ---------------------------------------------------------------------------
// Wizard conditional skip tests
// ---------------------------------------------------------------------------

func TestWizard_ConditionalSkip(t *testing.T) {
	steps := []WizardStep{
		{ID: "build", Title: "Build", Page: NewConfirmPage("build", "Build image?", false)},
		{ID: "flavor", Title: "Flavor", Page: NewSelectPage("flavor", "Pick flavor", []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
		}, 0), SkipIf: func(vals WizardValues) bool {
			return vals["build"] == "no"
		}},
		{ID: "submit", Title: "Submit", Page: NewConfirmPage("submit", "Submit?", true)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Step 0: ConfirmField with defaultYes=false, so value is "no".
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
	steps := []WizardStep{
		{ID: "step1", Title: "Step 1", Page: NewConfirmPage("step1", "First?", true)},
		{ID: "step2", Title: "Step 2", Page: NewConfirmPage("step2", "Second?", true)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// On the first step, press Esc — should cancel the wizard.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, model.cancelled, "Esc on first step should cancel")
	assert.False(t, model.submitted, "cancelled wizard should not be submitted")

	require.NotNil(t, cmd, "Esc on first step should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")
}

// ---------------------------------------------------------------------------
// Wizard Ctrl+C tests
// ---------------------------------------------------------------------------

func TestWizard_CtrlC(t *testing.T) {
	steps := []WizardStep{
		{ID: "step1", Title: "Step 1", Page: NewConfirmPage("step1", "First?", true)},
		{ID: "step2", Title: "Step 2", Page: NewConfirmPage("step2", "Second?", true)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Advance to step 1 first.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep)

	// Press Ctrl+C.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.True(t, model.cancelled, "Ctrl+C should cancel")
	assert.False(t, model.submitted, "Ctrl+C should not submit")

	require.NotNil(t, cmd, "Ctrl+C should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")
}

// ---------------------------------------------------------------------------
// Wizard submit and values collection tests
// ---------------------------------------------------------------------------

func TestWizard_SubmitCollectsValues(t *testing.T) {
	steps := []WizardStep{
		{ID: "build", Title: "Build", Page: NewConfirmPage("build", "Build image?", true)},
		{ID: "flavor", Title: "Flavor", Page: NewSelectPage("flavor", "Pick flavor", []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
			{Label: "Strawberry", Description: "Fruity"},
		}, 0)},
		{ID: "submit", Title: "Submit", Page: NewConfirmPage("submit", "Submit?", true)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Step 0: Confirm build (defaultYes=true, so value is "yes").
	assert.Equal(t, 0, model.currentStep)
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep)

	// Step 1: Select flavor — navigate down to "Chocolate" and confirm.
	model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 2, model.currentStep)

	// Step 2: Confirm submit — last step, submitting completes the wizard.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, model.submitted, "wizard should be submitted after confirming last step")
	assert.False(t, model.cancelled, "submitted wizard should not be cancelled")

	require.NotNil(t, cmd, "final confirmation should return tea.Quit cmd")
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cmd should produce tea.QuitMsg")

	assert.Equal(t, "yes", model.values["build"], "build should be 'yes'")
	assert.Equal(t, "Chocolate", model.values["flavor"], "flavor should be 'Chocolate'")
	assert.Equal(t, "yes", model.values["submit"], "submit should be 'yes'")
}

// ---------------------------------------------------------------------------
// Wizard stepper bar state tests
// ---------------------------------------------------------------------------

func TestWizard_NavBarUpdates(t *testing.T) {
	steps := []WizardStep{
		{ID: "step1", Title: "First Step", Page: NewConfirmPage("step1", "Do first?", true)},
		{ID: "step2", Title: "Second Step", Page: NewConfirmPage("step2", "Do second?", false)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	steps2 := model.buildStepperSteps()
	assert.Equal(t, StepActiveState, steps2[0].State, "step 0 should be active initially")
	assert.Equal(t, StepPendingState, steps2[1].State, "step 1 should be pending initially")

	model.Update(tea.KeyMsg{Type: tea.KeyEnter})

	steps2 = model.buildStepperSteps()
	assert.Equal(t, StepCompleteState, steps2[0].State, "step 0 should be complete after confirming")
	assert.Equal(t, "yes", steps2[0].Value, "step 0 value should be 'yes'")
	assert.Equal(t, StepActiveState, steps2[1].State, "step 1 should be active")
}

// ---------------------------------------------------------------------------
// Wizard View rendering tests
// ---------------------------------------------------------------------------

func TestWizard_View(t *testing.T) {
	steps := []WizardStep{
		{
			ID:       "build",
			Title:    "Build",
			Page:     NewConfirmPage("build", "Build image?", true),
			HelpKeys: []string{"←→", "toggle", "enter", "confirm", "esc", "back"},
		},
		{
			ID:       "flavor",
			Title:    "Flavor",
			Page:     NewSelectPage("flavor", "Pick flavor", []FieldOption{{Label: "Vanilla", Description: "Plain"}}, 0),
			HelpKeys: []string{"↑↓", "select", "enter", "confirm", "esc", "back"},
		},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	view := model.View()

	assert.Contains(t, view, "Build", "view should contain stepper bar step title")
	assert.Contains(t, view, "Flavor", "view should contain stepper bar step title")
	assert.Contains(t, view, "Build image?", "view should contain the current field prompt")
	assert.Contains(t, view, "enter", "view should contain help bar key bindings")
	assert.Contains(t, view, "confirm", "view should contain help bar descriptions")
	assert.Contains(t, view, "esc", "view should contain esc in help bar")
}

// ---------------------------------------------------------------------------
// Wizard window size tests
// ---------------------------------------------------------------------------

func TestWizard_WindowSize(t *testing.T) {
	steps := []WizardStep{
		{ID: "step1", Title: "Step 1", Page: NewConfirmPage("step1", "First?", true)},
	}

	m := newWizardModel(steps)
	model := &m

	assert.Equal(t, 0, model.width)
	assert.Equal(t, 0, model.height)

	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	assert.Equal(t, 100, model.width, "width should be updated to 100")
	assert.Equal(t, 30, model.height, "height should be updated to 30")

	model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	assert.Equal(t, 120, model.width, "width should be updated to 120")
	assert.Equal(t, 40, model.height, "height should be updated to 40")
}

// ---------------------------------------------------------------------------
// Wizard SkipIf re-evaluation on back-navigation
// ---------------------------------------------------------------------------

func TestWizard_SkipIfReevaluation(t *testing.T) {
	steps := []WizardStep{
		{ID: "build", Title: "Build", Page: NewSelectPage("build", "Build?", []FieldOption{
			{Label: "Yes", Description: "Build it"},
			{Label: "No", Description: "Skip it"},
		}, 1)}, // Default to "No"
		{ID: "flavor", Title: "Flavor", Page: NewSelectPage("flavor", "Pick flavor", []FieldOption{
			{Label: "Vanilla", Description: "Plain"},
			{Label: "Chocolate", Description: "Rich"},
		}, 0), SkipIf: func(vals WizardValues) bool {
			return vals["build"] == "No"
		}},
		{ID: "submit", Title: "Submit", Page: NewConfirmPage("submit", "Submit?", true)},
	}

	m := newWizardModel(steps)
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
// Wizard with text field step
// ---------------------------------------------------------------------------

func TestWizard_TextFieldInWizard(t *testing.T) {
	steps := []WizardStep{
		{ID: "name", Title: "Name", Page: NewTextPage("name", "Enter project name", WithDefault("my-project"))},
		{ID: "submit", Title: "Submit", Page: NewConfirmPage("submit", "Submit?", true)},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	model.Init()

	assert.Equal(t, 0, model.currentStep)

	// Press Enter to confirm the text field with default value.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep, "should advance to step 1 after confirming text field")

	assert.Equal(t, "my-project", model.values["name"], "text field should have default value")

	// Confirm submit.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, model.submitted, "wizard should be submitted")
	require.NotNil(t, cmd)
}

// ---------------------------------------------------------------------------
// Wizard with empty steps slice
// ---------------------------------------------------------------------------

func TestWizard_EmptySteps(t *testing.T) {
	ios, _, _, _ := iostreams.Test()
	tui := NewTUI(ios)

	result, err := tui.RunWizard(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wizard requires at least one step")
	assert.False(t, result.Submitted)

	result, err = tui.RunWizard([]WizardStep{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wizard requires at least one step")
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
// BrowserPage key configuration tests
// ---------------------------------------------------------------------------

func TestBrowserPage_DoneKey(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	page := NewBrowserPage(browser, WithDoneKey("s"))

	page.Init()
	bp := page.(*browserPage)

	// "s" should complete the page.
	page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.True(t, bp.done, "done key 's' should mark the page as complete")
	assert.True(t, page.IsComplete(), "IsComplete should return true after done key")
}

func TestBrowserPage_CancelKey(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	page := NewBrowserPage(browser, WithCancelKey("q"))

	page.Init()
	bp := page.(*browserPage)

	// "q" should cancel the page.
	page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.True(t, bp.cancelled, "cancel key 'q' should mark the page as cancelled")
	assert.False(t, page.IsComplete(), "cancelled page should not be complete")

	// Should satisfy CancelledPage interface.
	cp, ok := page.(CancelledPage)
	require.True(t, ok, "browserPage should implement CancelledPage")
	assert.True(t, cp.IsCancelled(), "IsCancelled should return true after cancel key")
}

func TestBrowserPage_NoKeysConfigured(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	page := NewBrowserPage(browser)

	page.Init()
	bp := page.(*browserPage)

	// Neither "s" nor "q" should trigger done/cancel when no keys configured.
	page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.False(t, bp.done, "'s' should not complete page when no done key configured")

	page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.False(t, bp.cancelled, "'q' should not cancel page when no cancel key configured")
}

func TestBrowserPage_Reset(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	page := NewBrowserPage(browser, WithDoneKey("s"), WithCancelKey("q"))

	page.Init()
	bp := page.(*browserPage)

	page.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.True(t, bp.done)

	// Reset should clear both done and cancelled.
	page.(Resetter).Reset()
	assert.False(t, bp.done, "done should be cleared after reset")
	assert.False(t, bp.cancelled, "cancelled should be cleared after reset")
}

func TestWizard_BrowserPageCancelKey(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	steps := []WizardStep{
		{ID: "confirm", Title: "Confirm", Page: NewConfirmPage("confirm", "Ready?", true)},
		{
			ID:    "browse",
			Title: "Browse",
			Page:  NewBrowserPage(browser, WithDoneKey("s"), WithCancelKey("q")),
		},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Advance past confirm step.
	model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, 1, model.currentStep)

	// Press "q" — should cancel the wizard.
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	assert.True(t, model.cancelled, "cancel key on browser page should cancel wizard")
	assert.False(t, model.submitted, "cancelled wizard should not be submitted")
	require.NotNil(t, cmd)
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "cancel should produce tea.QuitMsg")
}

func TestWizard_BrowserPageDoneKey(t *testing.T) {
	browser := NewFieldBrowser(testBrowserConfig())
	steps := []WizardStep{
		{
			ID:    "browse",
			Title: "Browse",
			Page:  NewBrowserPage(browser, WithDoneKey("s"), WithCancelKey("q")),
		},
	}

	m := newWizardModel(steps)
	model := &m
	model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	// Press "s" — should complete the page and submit (last step).
	_, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	assert.True(t, model.submitted, "done key should submit wizard when on last step")
	assert.False(t, model.cancelled, "done key should not cancel wizard")
	require.NotNil(t, cmd)
	msg := cmd()
	_, isQuit := msg.(tea.QuitMsg)
	assert.True(t, isQuit, "submit should produce tea.QuitMsg")
}

// ---------------------------------------------------------------------------
// Wizard validation panic tests
// ---------------------------------------------------------------------------

func TestWizard_ValidationPanics(t *testing.T) {
	t.Run("empty step ID", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardStep{
				{ID: "", Title: "Bad", Page: NewConfirmPage("bad", "Bad?", false)},
			})
		}, "empty step ID should panic")
	})

	t.Run("duplicate step IDs", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardStep{
				{ID: "dup", Title: "First", Page: NewConfirmPage("dup1", "First?", false)},
				{ID: "dup", Title: "Second", Page: NewConfirmPage("dup2", "Second?", false)},
			})
		}, "duplicate step IDs should panic")
	})

	t.Run("nil page", func(t *testing.T) {
		assert.Panics(t, func() {
			newWizardModel([]WizardStep{
				{ID: "bad", Title: "Bad", Page: nil},
			})
		}, "nil Page should panic")
	})
}
