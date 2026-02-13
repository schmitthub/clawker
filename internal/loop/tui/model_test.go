package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewModel(t *testing.T) {
	model := NewModel("test-project")

	assert.Equal(t, "test-project", model.project)
	assert.Equal(t, 0, model.width)
	assert.Equal(t, 0, model.height)
	assert.False(t, model.quitting)
	assert.Nil(t, model.err)
}

func TestModel_Init(t *testing.T) {
	model := NewModel("test-project")
	cmd := model.Init()

	// Phase 1 Init returns nil
	assert.Nil(t, cmd)
}

func TestModel_Update_Quit(t *testing.T) {
	model := NewModel("test-project")

	// Test q key quits
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	assert.True(t, m.quitting)
	require.NotNil(t, cmd)
}

func TestModel_Update_CtrlC(t *testing.T) {
	model := NewModel("test-project")

	// Test Ctrl+C quits
	msg := tea.KeyMsg{Type: tea.KeyCtrlC}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	assert.True(t, m.quitting)
	require.NotNil(t, cmd)
}

func TestModel_Update_WindowSize(t *testing.T) {
	model := NewModel("test-project")

	msg := tea.WindowSizeMsg{Width: 120, Height: 40}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	assert.Equal(t, 120, m.width)
	assert.Equal(t, 40, m.height)
	assert.Nil(t, cmd)
}

func TestModel_Update_Error(t *testing.T) {
	model := NewModel("test-project")

	testErr := errors.New("test error")
	msg := errMsg{err: testErr}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	assert.Equal(t, testErr, m.err)
	assert.Nil(t, cmd)
}

func TestModel_Update_OtherKeys(t *testing.T) {
	model := NewModel("test-project")

	// Other keys should not quit
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}
	newModel, cmd := model.Update(msg)
	m := newModel.(Model)

	assert.False(t, m.quitting)
	assert.Nil(t, cmd)
}

func TestModel_View_Normal(t *testing.T) {
	model := NewModel("test-project")
	view := model.View()

	// Check that view contains expected elements
	assert.Contains(t, view, "LOOP DASHBOARD")
	assert.Contains(t, view, "test-project")
	assert.Contains(t, view, "Press 'q' to quit")
}

func TestModel_View_Quitting(t *testing.T) {
	model := NewModel("test-project")
	model.quitting = true
	view := model.View()

	// When quitting, view should be empty
	assert.Empty(t, view)
}

func TestModel_View_WithError(t *testing.T) {
	model := NewModel("test-project")
	model.err = errors.New("test error message")
	view := model.View()

	// Error should be displayed
	assert.Contains(t, view, "Error:")
	assert.Contains(t, view, "test error message")
}

func TestErrMsg_Error(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"with error", errors.New("test error"), "test error"},
		{"nil error", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := errMsg{err: tt.err}
			assert.Equal(t, tt.want, msg.Error())
		})
	}
}
