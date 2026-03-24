package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKVEditor_ParsesYAMLInput(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar\nBAZ: qux")
	require.Len(t, m.pairs, 2)
	// Sorted alphabetically.
	assert.Equal(t, "BAZ", m.pairs[0].Key)
	assert.Equal(t, "qux", m.pairs[0].Value)
	assert.Equal(t, "FOO", m.pairs[1].Key)
	assert.Equal(t, "bar", m.pairs[1].Value)
}

func TestKVEditor_EmptyInput(t *testing.T) {
	m := NewKVEditor("env", "")
	assert.Empty(t, m.pairs)
	assert.Equal(t, kvBrowsing, m.state)
}

func TestKVEditor_EnterConfirms(t *testing.T) {
	m := NewKVEditor("env", "KEY: val")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv := updated.(KVEditorModel)
	assert.True(t, kv.IsConfirmed())
	assert.False(t, kv.IsCancelled())
}

func TestKVEditor_EscCancels(t *testing.T) {
	m := NewKVEditor("env", "KEY: val")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	kv := updated.(KVEditorModel)
	assert.True(t, kv.IsCancelled())
	assert.False(t, kv.IsConfirmed())
}

func TestKVEditor_DeletePair(t *testing.T) {
	m := NewKVEditor("env", "A: 1\nB: 2\nC: 3")
	require.Len(t, m.pairs, 3)

	// Delete first pair (cursor at 0).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	kv := updated.(KVEditorModel)
	require.Len(t, kv.pairs, 2)
	assert.Equal(t, "B", kv.pairs[0].Key)
}

func TestKVEditor_AddPair(t *testing.T) {
	m := NewKVEditor("env", "")

	// Press 'a' to start adding.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	kv := updated.(KVEditorModel)
	assert.Equal(t, kvAddingKey, kv.state)

	// Type key name and confirm.
	kv.input.SetValue("NEW_KEY")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvAddingVal, kv.state)
	assert.Equal(t, "NEW_KEY", kv.pendingKey)

	// Type value and confirm.
	kv.input.SetValue("new_val")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvBrowsing, kv.state)
	require.Len(t, kv.pairs, 1)
	assert.Equal(t, "NEW_KEY", kv.pairs[0].Key)
	assert.Equal(t, "new_val", kv.pairs[0].Value)
}

func TestKVEditor_ValueReturnsYAML(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar\nBAZ: qux")
	val := m.Value()
	assert.Contains(t, val, "BAZ: qux")
	assert.Contains(t, val, "FOO: bar")
}

func TestKVEditor_ValueEmptyReturnsEmpty(t *testing.T) {
	m := NewKVEditor("env", "")
	assert.Equal(t, "", m.Value())
}

func TestKVEditor_EditValue(t *testing.T) {
	m := NewKVEditor("env", "KEY: old")
	require.Len(t, m.pairs, 1)

	// Press 'e' to edit value.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	kv := updated.(KVEditorModel)
	assert.Equal(t, kvEditingVal, kv.state)

	// Change value and confirm.
	kv.input.SetValue("new")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvBrowsing, kv.state)
	assert.Equal(t, "new", kv.pairs[0].Value)
}

func TestKVEditor_EditKey(t *testing.T) {
	m := NewKVEditor("env", "OLD_KEY: val")

	// Press 'E' to edit key.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	kv := updated.(KVEditorModel)
	assert.Equal(t, kvEditingKey, kv.state)

	// Change key and confirm.
	kv.input.SetValue("NEW_KEY")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvBrowsing, kv.state)
	assert.Equal(t, "NEW_KEY", kv.pairs[0].Key)
}

func TestKVEditor_WindowSizeMsg(t *testing.T) {
	m := NewKVEditor("env", "KEY: val")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	kv := updated.(KVEditorModel)
	assert.Equal(t, 120, kv.width)
	assert.Equal(t, 40, kv.height)
}

func TestKVEditor_ViewShowsPairs(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar\nBAZ: qux")
	view := m.View()
	assert.Contains(t, view, "FOO")
	assert.Contains(t, view, "bar")
	assert.Contains(t, view, "BAZ")
	assert.Contains(t, view, "qux")
	assert.Contains(t, view, "key-value editor")
}

func TestKVEditor_ViewShowsEmptyState(t *testing.T) {
	m := NewKVEditor("env", "")
	view := m.View()
	assert.Contains(t, view, "(empty)")
}

func TestKVEditor_EscFromEditReturnsToBrowsing(t *testing.T) {
	m := NewKVEditor("env", "KEY: val")

	// Enter edit mode.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	kv := updated.(KVEditorModel)
	require.Equal(t, kvEditingVal, kv.state)

	// Escape from edit.
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEsc})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvBrowsing, kv.state)
	assert.False(t, kv.IsCancelled(), "esc from inline edit should not cancel the whole editor")
}
