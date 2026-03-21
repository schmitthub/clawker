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
		{Path: "security.git_credentials.forward_ssh", Label: "forward_ssh", Kind: KindTriState, Value: "<unset>", Options: []string{"true", "false", "<unset>"}, Order: 3},
		{Path: "build.instructions", Label: "instructions", Kind: KindComplex, Value: "{}", ReadOnly: true, Order: 4},
	}
}

func testLayers() []string {
	return []string{"clawker.yaml", "clawker.local.yaml"}
}

func TestEditorModel_BuildsTabs(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	require.Len(t, m.tabs, 2, "should have 2 tabs: build, security")
	assert.Equal(t, "Build", m.tabs[0].name)
	assert.Equal(t, "Security", m.tabs[1].name)
}

func TestEditorModel_TabRowStructure(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	// Build tab: image, packages, instructions (no sub-sections, all direct children)
	buildTab := m.tabs[0]
	fieldCount := 0
	for _, r := range buildTab.rows {
		if !r.isHeading {
			fieldCount++
		}
	}
	assert.Equal(t, 3, fieldCount, "build tab should have 3 fields")

	// Security tab: docker_socket (direct) + Git Credentials heading + forward_ssh
	secTab := m.tabs[1]
	assert.True(t, len(secTab.rows) >= 3, "security tab should have heading + fields")

	// First non-heading should be docker_socket (direct child of security)
	assert.False(t, secTab.rows[0].isHeading)
	assert.Equal(t, "docker_socket", secTab.rows[0].field.Label)

	// Second row should be the "Git Credentials" heading
	assert.True(t, secTab.rows[1].isHeading)
	assert.Equal(t, "Git Credentials", secTab.rows[1].heading)

	// Third should be forward_ssh under that heading
	assert.False(t, secTab.rows[2].isHeading)
	assert.Equal(t, "forward_ssh", secTab.rows[2].field.Label)
}

func TestEditorModel_InitialState(t *testing.T) {
	m := newEditorModel("Test Editor", testFields(), testLayers())

	assert.Equal(t, stateBrowse, m.state)
	assert.Equal(t, 0, m.activeTab)
	assert.False(t, m.saved)
	assert.False(t, m.cancelled)
	assert.Empty(t, m.modified)
}

func TestEditorModel_TabSwitching(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	assert.Equal(t, 0, m.activeTab)

	// Right arrow → next tab
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 1, m.activeTab)

	// Right again → wraps to first tab
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 0, m.activeTab)

	// Left arrow → wraps to last tab
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	assert.Equal(t, 1, m.activeTab)
}

func TestEditorModel_UpDownSkipsHeadings(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	// Switch to security tab which has a heading
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	require.Equal(t, 1, m.activeTab)

	// Should start on first field (docker_socket), not the heading
	secTab := m.tabs[1]
	assert.False(t, secTab.rows[m.activeRow].isHeading)

	// Navigate down — should skip heading and land on forward_ssh
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.False(t, secTab.rows[m.activeRow].isHeading)
	assert.Equal(t, "forward_ssh", secTab.rows[m.activeRow].field.Label)
}

func TestEditorModel_CancelKeys(t *testing.T) {
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

	// Navigate to the read-only field (instructions, 3rd field in build tab)
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
}

func TestEditorModel_EnterTransitionsToEdit(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())

	// First field in first tab is "image" (KindText)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*editorModel)

	assert.Equal(t, stateEdit, result.state)
}

func TestEditorModel_EscFromEditReturnsToBrowse(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
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

func TestEditorModel_ZeroLayersSaveIgnored(t *testing.T) {
	m := newEditorModel("Test", testFields(), nil)
	m.modified["build.image"] = "alpine:latest"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*editorModel)

	assert.Equal(t, stateBrowse, result.state)
	assert.False(t, result.saved)
}

func TestEditorModel_ViewRendersTabBar(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.width = 80
	m.height = 30
	view := m.View()

	assert.Contains(t, view, "Build")
	assert.Contains(t, view, "Security")
}

func TestEditorModel_ViewShowsSectionHeadings(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.width = 80
	m.height = 30

	// Switch to security tab
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	view := m.View()

	assert.Contains(t, view, "Git Credentials")
}

func TestEditorModel_ModifiedFieldShowsAsterisk(t *testing.T) {
	m := newEditorModel("Test", testFields(), testLayers())
	m.modified["build.image"] = "alpine:latest"
	m.width = 80
	m.height = 30

	view := m.View()
	assert.Contains(t, view, "* image")
}

func TestFormatHeading(t *testing.T) {
	assert.Equal(t, "Git Credentials", formatHeading("git_credentials"))
	assert.Equal(t, "Otel", formatHeading("otel"))
	assert.Equal(t, "Host Proxy", formatHeading("host_proxy"))
}

func TestFormatTabName(t *testing.T) {
	assert.Equal(t, "Build", formatTabName("build"))
	assert.Equal(t, "Security", formatTabName("security"))
	assert.Equal(t, "", formatTabName(""))
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
