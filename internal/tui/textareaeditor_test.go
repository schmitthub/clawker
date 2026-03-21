package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
)

func TestTextareaEditor_InitialValue(t *testing.T) {
	m := NewTextareaEditor("script", "#!/bin/bash\necho hello")
	assert.Equal(t, "#!/bin/bash\necho hello", m.Value())
}

func TestTextareaEditor_CtrlSSaves(t *testing.T) {
	m := NewTextareaEditor("script", "hello")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	assert.True(t, m.IsConfirmed())
	assert.False(t, m.IsCancelled())
}

func TestTextareaEditor_EscCancels(t *testing.T) {
	m := NewTextareaEditor("script", "hello")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.True(t, m.IsCancelled())
	assert.False(t, m.IsConfirmed())
}

func TestTextareaEditor_ViewShowsLabel(t *testing.T) {
	m := NewTextareaEditor("Post-Init Script", "echo hello")
	view := m.View()
	assert.Contains(t, view, "Post-Init Script")
	assert.Contains(t, view, "multiline editor")
	assert.Contains(t, view, "ctrl+s")
}
