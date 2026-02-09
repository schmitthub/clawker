package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// SelectField tests
// ---------------------------------------------------------------------------

func TestSelectField_Navigation(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "Option A"},
		{Label: "B", Description: "Option B"},
		{Label: "C", Description: "Option C"},
	}, 0)

	assert.Equal(t, 0, f.SelectedIndex())

	// Move down through the list
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, f.SelectedIndex())

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 2, f.SelectedIndex())

	// Move back up
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 1, f.SelectedIndex())

	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 0, f.SelectedIndex())

	// Wrap enabled by default: going up from 0 should wrap to last
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyUp})
	assert.Equal(t, 2, f.SelectedIndex())

	// Wrap: going down from last should wrap to first
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 0, f.SelectedIndex())
}

func TestSelectField_Navigation_VimKeys(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "Option A"},
		{Label: "B", Description: "Option B"},
	}, 0)

	// 'j' moves down (vim binding)
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	assert.Equal(t, 1, f.SelectedIndex())

	// 'k' moves up (vim binding)
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	assert.Equal(t, 0, f.SelectedIndex())
}

func TestSelectField_Confirm(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "Option A"},
		{Label: "B", Description: "Option B"},
		{Label: "C", Description: "Option C"},
	}, 0)

	// Not confirmed initially
	assert.False(t, f.IsConfirmed())

	// Move to B and confirm
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, 1, f.SelectedIndex())

	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, f.IsConfirmed())
	assert.Equal(t, "B", f.Value())
	require.NotNil(t, cmd, "Enter should return tea.Quit cmd")
}

func TestSelectField_Value(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "Alpha", Description: "First"},
		{Label: "Beta", Description: "Second"},
		{Label: "Gamma", Description: "Third"},
	}, 0)

	// Initial value is the first option's label
	assert.Equal(t, "Alpha", f.Value())

	// Navigate to second option
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, "Beta", f.Value())

	// Navigate to third option
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, "Gamma", f.Value())
}

func TestSelectField_DefaultIndex(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "First"},
		{Label: "B", Description: "Second"},
		{Label: "C", Description: "Third"},
	}, 2)

	assert.Equal(t, 2, f.SelectedIndex())
	assert.Equal(t, "C", f.Value())
}

func TestSelectField_View(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "Alpha", Description: "First letter"},
		{Label: "Beta", Description: "Second letter"},
	}, 0)

	view := f.View()

	// View should contain the prompt and all option labels
	assert.Contains(t, view, "Pick one")
	assert.Contains(t, view, "Alpha")
	assert.Contains(t, view, "Beta")
	assert.Contains(t, view, "First letter")
	assert.Contains(t, view, "Second letter")
}

func TestSelectField_CtrlC(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "Option A"},
	}, 0)

	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.False(t, f.IsConfirmed(), "Ctrl+C should not confirm")
	require.NotNil(t, cmd, "Ctrl+C should return tea.Quit cmd")
}

// ---------------------------------------------------------------------------
// TextField tests
// ---------------------------------------------------------------------------

func TestTextField_Input(t *testing.T) {
	f := NewTextField("name", "Enter name")

	// Send individual rune key messages to type "hello"
	for _, r := range "hello" {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	assert.Equal(t, "hello", f.Value())
}

func TestTextField_Input_WithDefault(t *testing.T) {
	f := NewTextField("name", "Enter name", WithDefault("preset"))

	assert.Equal(t, "preset", f.Value())
}

func TestTextField_Validation(t *testing.T) {
	validator := func(s string) error {
		if len(s) < 3 {
			return fmt.Errorf("must be at least 3 characters")
		}
		return nil
	}

	f := NewTextField("name", "Enter name", WithValidator(validator))

	// Type "ab" (too short)
	for _, r := range "ab" {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	// Try to confirm — should fail validation
	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, f.IsConfirmed(), "should not confirm with invalid input")
	assert.Equal(t, "must be at least 3 characters", f.Err())
	assert.Nil(t, cmd, "should return nil cmd on validation failure")

	// View should show the error
	view := f.View()
	assert.Contains(t, view, "must be at least 3 characters")

	// Type one more character to make it valid ("abc")
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})

	// Now confirm should succeed
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, f.IsConfirmed())
	assert.Equal(t, "", f.Err(), "error should be cleared on successful confirm")
	assert.Equal(t, "abc", f.Value())
	require.NotNil(t, cmd, "Enter should return tea.Quit cmd on success")
}

func TestTextField_Required(t *testing.T) {
	f := NewTextField("name", "Enter name", WithRequired())

	// Try to confirm with empty input
	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, f.IsConfirmed(), "should not confirm empty required field")
	assert.Equal(t, "This field is required", f.Err())
	assert.Nil(t, cmd, "should return nil cmd on required validation failure")

	// Whitespace-only should also fail
	for _, r := range "   " {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, f.IsConfirmed(), "whitespace-only should not pass required check")
	assert.Equal(t, "This field is required", f.Err())

	// Clear and type a real value
	// Use backspace to clear whitespace
	for i := 0; i < 3; i++ {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	}
	for _, r := range "valid" {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}

	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, f.IsConfirmed())
	assert.Equal(t, "", f.Err())
	assert.Equal(t, "valid", f.Value())
	require.NotNil(t, cmd)
}

func TestTextField_Required_WithValidator(t *testing.T) {
	// Required check runs before custom validator
	f := NewTextField("name", "Enter name",
		WithRequired(),
		WithValidator(func(s string) error {
			if s == "bad" {
				return fmt.Errorf("invalid value")
			}
			return nil
		}),
	)

	// Empty input hits required check first
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "This field is required", f.Err())

	// Type "bad" — passes required, fails validator
	for _, r := range "bad" {
		f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, "invalid value", f.Err())
	assert.False(t, f.IsConfirmed())
}

func TestTextField_View(t *testing.T) {
	f := NewTextField("name", "Enter your name", WithPlaceholder("type here..."))

	view := f.View()
	assert.Contains(t, view, "Enter your name")
}

func TestTextField_CtrlC(t *testing.T) {
	f := NewTextField("name", "Enter name")

	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.False(t, f.IsConfirmed(), "Ctrl+C should not confirm")
	require.NotNil(t, cmd, "Ctrl+C should return tea.Quit cmd")
}

// ---------------------------------------------------------------------------
// ConfirmField tests
// ---------------------------------------------------------------------------

func TestConfirmField_Toggle(t *testing.T) {
	f := NewConfirmField("confirm", "Are you sure?", true)

	// Starts at yes
	assert.True(t, f.BoolValue())
	assert.Equal(t, "yes", f.Value())

	// Left key toggles to no
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyLeft})
	assert.False(t, f.BoolValue())
	assert.Equal(t, "no", f.Value())

	// Right key toggles back to yes
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.True(t, f.BoolValue())
	assert.Equal(t, "yes", f.Value())

	// Tab also toggles
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyTab})
	assert.False(t, f.BoolValue())

	// 'y' key sets to true directly
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	assert.True(t, f.BoolValue())

	// 'n' key sets to false directly
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	assert.False(t, f.BoolValue())

	// 'Y' (uppercase) also works
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'Y'}})
	assert.True(t, f.BoolValue())

	// 'N' (uppercase) also works
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'N'}})
	assert.False(t, f.BoolValue())
}

func TestConfirmField_Default(t *testing.T) {
	t.Run("default yes", func(t *testing.T) {
		f := NewConfirmField("confirm", "Enable feature?", true)
		assert.True(t, f.BoolValue())
		assert.Equal(t, "yes", f.Value())
	})

	t.Run("default no", func(t *testing.T) {
		f := NewConfirmField("confirm", "Enable feature?", false)
		assert.False(t, f.BoolValue())
		assert.Equal(t, "no", f.Value())
	})
}

func TestConfirmField_Confirm(t *testing.T) {
	f := NewConfirmField("confirm", "Are you sure?", false)

	// Not confirmed initially
	assert.False(t, f.IsConfirmed())

	// Toggle to yes
	f, _ = f.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	assert.True(t, f.BoolValue())
	assert.False(t, f.IsConfirmed(), "toggling should not confirm")

	// Press Enter to confirm
	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.True(t, f.IsConfirmed())
	assert.True(t, f.BoolValue())
	require.NotNil(t, cmd, "Enter should return tea.Quit cmd")
}

func TestConfirmField_View(t *testing.T) {
	f := NewConfirmField("confirm", "Continue?", true)

	view := f.View()
	assert.Contains(t, view, "Continue?")
	assert.Contains(t, view, "Yes")
	assert.Contains(t, view, "No")
}

func TestConfirmField_CtrlC(t *testing.T) {
	f := NewConfirmField("confirm", "Are you sure?", true)

	var cmd tea.Cmd
	f, cmd = f.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	assert.False(t, f.IsConfirmed(), "Ctrl+C should not confirm")
	require.NotNil(t, cmd, "Ctrl+C should return tea.Quit cmd")
}

// ---------------------------------------------------------------------------
// SetSize tests
// ---------------------------------------------------------------------------

func TestTextField_SetSize_SmallWidth(t *testing.T) {
	f := NewTextField("name", "Enter name")

	// Width 0 should clamp input width to 1.
	f = f.SetSize(0, 10)
	assert.Equal(t, "Enter name", f.Prompt, "prompt should be unchanged")

	// Width 3 results in inputWidth = 3-4 = -1, clamped to 1.
	f = f.SetSize(3, 10)

	// Width 10 results in inputWidth = 10-4 = 6.
	f = f.SetSize(10, 10)
}

func TestSelectField_SetSize(t *testing.T) {
	f := NewSelectField("test", "Pick one", []FieldOption{
		{Label: "A", Description: "Option A"},
		{Label: "B", Description: "Option B"},
	}, 0)

	f = f.SetSize(60, 20)

	// Value should still be accessible after resize.
	assert.Equal(t, "A", f.Value())
	assert.Equal(t, 0, f.SelectedIndex())
}

func TestConfirmField_SetSize(t *testing.T) {
	f := NewConfirmField("confirm", "Are you sure?", true)

	// SetSize is a no-op but should not panic.
	f = f.SetSize(80, 24)
	f = f.SetSize(0, 0)

	// Value should be unchanged.
	assert.True(t, f.BoolValue())
	assert.Equal(t, "yes", f.Value())
}
