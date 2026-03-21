package storeui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testFields() []Field {
	return []Field{
		{Path: "build.image", Label: "image", Kind: KindText, Value: "ubuntu:22.04", Order: 0},
		{Path: "build.packages", Label: "packages", Kind: KindStringSlice, Value: "git, curl", Order: 1},
		{Path: "security.docker_socket", Label: "docker_socket", Kind: KindBool, Value: "false", Order: 2},
		{Path: "logging.file_enabled", Label: "file_enabled", Kind: KindTriState, Value: "<unset>", Options: []string{"true", "false", "<unset>"}, Order: 3},
		{Path: "build.instructions", Label: "instructions", Kind: KindComplex, Value: "{}", ReadOnly: true, Order: 4},
	}
}

func testLayers() []string {
	return []string{"clawker.yaml", "clawker.local.yaml"}
}

func TestEditorModel_InitialState(t *testing.T) {
	m := newEditorModel("Test Editor", testFields(), testLayers())

	assert.Equal(t, stateBrowse, m.state)
	assert.False(t, m.saved)
	assert.False(t, m.cancelled)
	assert.Empty(t, m.modified)
}

func TestEditorModel_BrowseCancelKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m := newEditorModel("Test", testFields(), testLayers())
			updated, cmd := m.Update(key)
			result := updated.(*editorModel)

			assert.True(t, result.cancelled)
			assert.NotNil(t, cmd)
		})
	}
}

func TestEditorModel_EnterOnReadOnlyStaysInBrowse(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	// Navigate to the read-only field (index 4).
	for i := 0; i < 4; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
}

func TestEditorModel_EnterTransitionsToEdit(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	// First field (index 0) is KindText — enter edit.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*editorModel)

	assert.Equal(t, stateEdit, result.state)
	assert.Equal(t, 0, result.editIdx)
}

func TestEditorModel_EscFromEditReturnsToBrowse(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // Enter edit

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
	assert.Empty(t, result.modified)
}

func TestEditorModel_SaveWithNoModificationsIgnored(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
}

func TestEditorModel_SingleLayerAutoSaves(t *testing.T) {
	m := newEditorModel("Test", testFields(), []string{"clawker.yaml"})
	m.modified["build.image"] = "alpine:latest"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*editorModel)

	assert.True(t, result.saved)
	assert.Equal(t, "clawker.yaml", result.selectedLayer())
	assert.NotNil(t, cmd)
}

func TestEditorModel_MultipleLayersShowsSaveSelect(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.modified["build.image"] = "alpine:latest"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*editorModel)

	assert.Equal(t, stateSave, result.state)
}

func TestEditorModel_SaveSelectEscReturnsToBrowse(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.modified["build.image"] = "alpine:latest"
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
	assert.False(t, result.saved)
}

func TestEditorModel_ZeroLayersSaveIgnored(t *testing.T) {
	m := newEditorModel("Test", testFields(), nil)
	m.modified["build.image"] = "alpine:latest"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
	assert.False(t, result.saved)
}

func TestEditorModel_ViewBrowseRendersTitle(t *testing.T) {
	m := newEditorModel("Settings Editor", testFields(), testLayers())
	view := m.View()

	assert.Contains(t, view, "Settings Editor")
}

func TestEditorModel_ViewBrowseShowsModifiedCount(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.modified["build.image"] = "alpine:latest"
	m.list = m.buildFieldList()

	view := m.View()
	assert.Contains(t, view, "1 modified")
}

func TestEditorModel_ModifiedFieldShowsAsterisk(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.modified["build.image"] = "alpine:latest"
	m.list = m.buildFieldList()

	item := m.list.Items()[0]
	assert.Contains(t, item.Title(), "* ")
}

func TestEditorModel_SelectFieldConfirmSetsModified(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	// Navigate to bool field (index 2) and enter edit.
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	require.Equal(t, stateEdit, m.state)

	// Confirm selection.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	assert.Equal(t, stateBrowse, m.state)
	assert.Contains(t, m.modified, "security.docker_socket")
}

func TestEditorModel_EmptyOptionsSelectStaysInBrowse(t *testing.T) {
	fields := []Field{
		{Path: "mode", Label: "mode", Kind: KindSelect, Value: "", Options: nil, Order: 0},
	}
	m := newEditorModel("Test", fields, testLayers())

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
}

func TestFilterQuit_FiltersQuitMsg(t *testing.T) {
	cmd := filterQuit(tea.Quit)
	msg := cmd()
	assert.Nil(t, msg)
}

func TestFilterQuit_PassesThroughOtherMsgs(t *testing.T) {
	expected := tea.KeyMsg{Type: tea.KeyEnter}
	original := func() tea.Msg { return expected }

	cmd := filterQuit(original)
	msg := cmd()

	assert.Equal(t, expected, msg)
}
