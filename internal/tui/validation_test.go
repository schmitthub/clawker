package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// KVEditor — no editor-level duplicate key blocking
// The editor shows merged store state. Duplicate key validation belongs at
// the write boundary (per-layer), not in the editor. The same key in
// different layers is inheritance, not duplication.
// ---------------------------------------------------------------------------

func TestKVEditor_AddKeyMatchingMergedEntry(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar")
	require.Len(t, m.pairs, 1)

	// Add FOO again — the editor must not block this. The user may intend
	// to save this value to a different layer than the existing FOO.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	kv := updated.(KVEditorModel)
	kv.input.SetValue("FOO")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)

	assert.Equal(t, kvAddingVal, kv.state, "should advance to value entry")
	assert.Empty(t, kv.Err())

	kv.input.SetValue("new-value")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)
	assert.Equal(t, kvBrowsing, kv.state)
	assert.Len(t, kv.pairs, 2)
}

func TestKVEditor_RenameKeyToExistingName(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar\nBAZ: qux")
	require.Len(t, m.pairs, 2)

	// Rename BAZ to FOO — editor must not block this.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'E'}})
	kv := updated.(KVEditorModel)
	kv.input.SetValue("FOO")
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv = updated.(KVEditorModel)

	assert.Equal(t, kvBrowsing, kv.state)
	assert.Equal(t, "FOO", kv.pairs[0].Key)
	assert.Empty(t, kv.Err())
}

// ---------------------------------------------------------------------------
// KVEditor — parse error surfacing
// ---------------------------------------------------------------------------

func TestKVEditor_MalformedYAMLShowsParseError(t *testing.T) {
	m := NewKVEditor("env", "not: valid: yaml: {{")
	// Should show parse error rather than silently dropping data.
	assert.NotEmpty(t, m.Err())
	assert.Contains(t, m.Err(), "could not parse")
	assert.Contains(t, m.View(), "could not parse")
}

func TestKVEditor_ValidYAMLNoParseError(t *testing.T) {
	m := NewKVEditor("env", "FOO: bar")
	assert.Empty(t, m.Err())
}

// ---------------------------------------------------------------------------
// KVEditor — external validator
// ---------------------------------------------------------------------------

func TestKVEditor_ExternalValidatorBlocksConfirm(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("map is invalid")
	}
	m := NewKVEditor("env", "FOO: bar", WithKVValidator(validator))

	// Try to confirm (Enter in browsing state).
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv := updated.(KVEditorModel)

	assert.False(t, kv.IsConfirmed())
	assert.Equal(t, "map is invalid", kv.Err())
	assert.Contains(t, kv.View(), "map is invalid")
}

func TestKVEditor_ErrorClearedOnNextKey(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("nope")
	}
	m := NewKVEditor("env", "FOO: bar", WithKVValidator(validator))
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	kv := updated.(KVEditorModel)
	require.NotEmpty(t, kv.Err())

	// Any key clears the error.
	updated, _ = kv.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	kv = updated.(KVEditorModel)
	assert.Equal(t, "", kv.Err())
}

// ---------------------------------------------------------------------------
// ListEditor — external validator
// ---------------------------------------------------------------------------

func TestListEditor_ExternalValidatorBlocksConfirm(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("list is invalid")
	}
	m := NewListEditor("packages", "git", WithListValidator(validator))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.False(t, m.IsConfirmed())
	assert.Equal(t, "list is invalid", m.Err())
	assert.Contains(t, m.View(), "list is invalid")
}

func TestListEditor_ErrorClearedOnNextKey(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("nope")
	}
	m := NewListEditor("packages", "git", WithListValidator(validator))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.NotEmpty(t, m.Err())

	// Any browsing key clears the error.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.Equal(t, "", m.Err())
}

// ---------------------------------------------------------------------------
// TextareaEditor — external validator
// ---------------------------------------------------------------------------

func TestTextareaEditor_ExternalValidatorBlocksConfirm(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("content is invalid")
	}
	m := NewTextareaEditor("script", "echo hello", WithTextareaValidator(validator))

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	assert.False(t, m.IsConfirmed())
	assert.Equal(t, "content is invalid", m.Err())
	assert.Contains(t, m.View(), "content is invalid")
}

func TestTextareaEditor_ErrorClearedOnNextKey(t *testing.T) {
	validator := func(s string) error {
		return fmt.Errorf("nope")
	}
	m := NewTextareaEditor("script", "echo hello", WithTextareaValidator(validator))
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	require.NotEmpty(t, m.Err())

	// Type any character — error should clear.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	assert.Equal(t, "", m.Err())
}

// ---------------------------------------------------------------------------
// FieldBrowser — validator wiring to all editor types
// ---------------------------------------------------------------------------

func TestFieldBrowser_ValidatorWiredToListEditor(t *testing.T) {
	validatorCalled := false
	fields := []BrowserField{
		{
			Path:  "build.packages",
			Label: "packages",
			Kind:  BrowserStringSlice,
			Value: "git",
			Validator: func(s string) error {
				validatorCalled = true
				return fmt.Errorf("rejected")
			},
		},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	// Enter edit on the list field.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	require.Equal(t, ekList, m.editKind)

	// Try to confirm the list — validator should fire.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, validatorCalled, "validator should be called on list confirm")
	assert.False(t, m.listEditor.IsConfirmed(), "list should not confirm when validator rejects")
	assert.Equal(t, "rejected", m.listEditor.Err())
}

func TestFieldBrowser_ValidatorWiredToKVEditor(t *testing.T) {
	validatorCalled := false
	fields := []BrowserField{
		{
			Path:      "agent.env",
			Label:     "env",
			Kind:      BrowserMap,
			Value:     "1 entry",
			EditValue: "FOO: bar",
			Validator: func(s string) error {
				validatorCalled = true
				return fmt.Errorf("rejected")
			},
		},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	// Enter edit.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	require.Equal(t, ekKV, m.editKind)

	// Try to confirm — validator should fire.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, validatorCalled, "validator should be called on KV confirm")
	assert.False(t, m.kvEditor.IsConfirmed(), "KV editor should not confirm when validator rejects")
	assert.Equal(t, "rejected", m.kvEditor.Err())
}

func TestFieldBrowser_ValidatorWiredToTextareaEditor(t *testing.T) {
	validatorCalled := false
	fields := []BrowserField{
		{
			Path:  "agent.post_init",
			Label: "post_init",
			Kind:  BrowserText,
			Value: "echo hello",
			Validator: func(s string) error {
				validatorCalled = true
				return fmt.Errorf("rejected")
			},
		},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	// Enter edit.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	require.Equal(t, ekTextarea, m.editKind)

	// Try to save with Ctrl+S — validator should fire.
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})

	assert.True(t, validatorCalled, "validator should be called on textarea save")
	assert.False(t, m.taEditor.IsConfirmed(), "textarea should not confirm when validator rejects")
	assert.Equal(t, "rejected", m.taEditor.Err())
}

func TestFieldBrowser_ValidatorWiredToTextField(t *testing.T) {
	validatorCalled := false
	fields := []BrowserField{
		{
			Path:  "build.timeout",
			Label: "timeout",
			Kind:  BrowserDuration,
			Value: "5m",
			Validator: func(s string) error {
				validatorCalled = true
				return fmt.Errorf("rejected")
			},
		},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	// Enter edit.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	require.Equal(t, ekText, m.editKind)

	// Try to confirm — validator should fire.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, validatorCalled, "validator should be called on text field confirm")
	assert.False(t, m.textField.IsConfirmed(), "text field should not confirm when validator rejects")
	assert.Equal(t, "rejected", m.textField.Err())
}

func TestFieldBrowser_ValidatorWiredToSelectEditor(t *testing.T) {
	validatorCalled := false
	fields := []BrowserField{
		{
			Path:  "security.docker_socket",
			Label: "docker_socket",
			Kind:  BrowserBool,
			Value: "false",
			Validator: func(s string) error {
				validatorCalled = true
				return fmt.Errorf("rejected")
			},
		},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	// Enter edit on the bool field (uses SelectField).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	require.Equal(t, ekSelect, m.editKind)

	// Confirm selection — validator should fire at FieldBrowser level.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.True(t, validatorCalled, "validator should be called on select confirm")
	// Validator rejection returns to browse with error shown.
	assert.Equal(t, bsStateBrowse, m.state)
	assert.Equal(t, "rejected", m.lastSaveError)
}
