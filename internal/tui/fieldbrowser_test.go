package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testBrowserFields() []BrowserField {
	return []BrowserField{
		{Path: "build.image", Label: "image", Kind: BrowserText, Value: "ubuntu:22.04", Order: 0},
		{Path: "build.packages", Label: "packages", Kind: BrowserStringSlice, Value: "git, curl", Order: 1},
		{Path: "security.docker_socket", Label: "docker_socket", Kind: BrowserBool, Value: "false", Order: 2},
		{Path: "security.git_credentials.forward_ssh", Label: "forward_ssh", Kind: BrowserTriState, Value: "<unset>", Options: []string{"true", "false", "<unset>"}, Order: 3},
		{Path: "build.instructions", Label: "instructions", Kind: BrowserComplex, Value: "{}", ReadOnly: true, Order: 4},
	}
}

func testBrowserSaveTargets() []BrowserSaveTarget {
	return []BrowserSaveTarget{
		{Label: "Original locations", Description: "Save each value to the file it came from"},
		{Label: "Project local", Description: ".clawker.yaml"},
	}
}

func TestFieldBrowser_BuildsTabs(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})

	require.Len(t, m.tabs, 2, "should have 2 tabs: build, security")
	assert.Equal(t, "Build", m.tabs[0].name)
	assert.Equal(t, "Security", m.tabs[1].name)
}

func TestFieldBrowser_TabRowStructure(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})

	// Build tab: image, packages, instructions (no sub-sections)
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
	assert.True(t, len(secTab.rows) >= 3)
	assert.False(t, secTab.rows[0].isHeading)
	assert.Equal(t, "docker_socket", secTab.rows[0].field.Label)
	assert.True(t, secTab.rows[1].isHeading)
	assert.Equal(t, "Git Credentials", secTab.rows[1].heading)
	assert.False(t, secTab.rows[2].isHeading)
	assert.Equal(t, "forward_ssh", secTab.rows[2].field.Label)
}

func TestFieldBrowser_InitialState(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test Editor", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})

	assert.Equal(t, bsStateBrowse, m.state)
	assert.Equal(t, 0, m.activeTab)
	assert.False(t, m.saved)
	assert.False(t, m.cancelled)
	assert.Empty(t, m.modified)
}

func TestFieldBrowser_TabSwitching(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})

	assert.Equal(t, 0, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 1, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 0, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	assert.Equal(t, 1, m.activeTab)
}

func TestFieldBrowser_UpDownSkipsHeadings(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	require.Equal(t, 1, m.activeTab)

	secTab := m.tabs[1]
	assert.False(t, secTab.rows[m.activeRow].isHeading)

	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	assert.False(t, secTab.rows[m.activeRow].isHeading)
	assert.Equal(t, "forward_ssh", secTab.rows[m.activeRow].field.Label)
}

func TestFieldBrowser_CancelKeys(t *testing.T) {
	keys := []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	}
	for _, key := range keys {
		t.Run(key.String(), func(t *testing.T) {
			m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
			updated, cmd := m.Update(key)
			result := updated.(*FieldBrowserModel)
			assert.True(t, result.cancelled)
			assert.NotNil(t, cmd)
		})
	}
}

func TestFieldBrowser_EnterOnReadOnlyStaysInBrowse(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateBrowse, result.state)
}

func TestFieldBrowser_EnterTransitionsToEdit(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateEdit, result.state)
	assert.Equal(t, 0, result.editIdx)
}

func TestFieldBrowser_EscFromEditReturnsToBrowse(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateBrowse, result.state)
	assert.Empty(t, result.modified)
}

func TestFieldBrowser_SaveWithNoModificationsIgnored(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateBrowse, result.state)
}

func TestFieldBrowser_SaveShowsDialogWithMultipleTargets(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.modified["build.image"] = "alpine:latest"

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateSave, result.state)

	// Confirm selection → saves.
	updated, cmd := result.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result = updated.(*FieldBrowserModel)
	assert.True(t, result.saved)
	assert.NotNil(t, cmd)
}

func TestFieldBrowser_SaveAutoWithSingleTarget(t *testing.T) {
	targets := []BrowserSaveTarget{{Label: "Local", Description: ".clawker.yaml"}}
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: targets})
	m.modified["build.image"] = "alpine:latest"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*FieldBrowserModel)
	assert.True(t, result.saved)
	assert.NotNil(t, cmd)
}

func TestFieldBrowser_SaveDirectly(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields()})
	m.modified["build.image"] = "alpine:latest"

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	result := updated.(*FieldBrowserModel)
	assert.True(t, result.saved)
	assert.NotNil(t, cmd)
}

func TestFieldBrowser_ViewRendersTabBar(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.width = 80
	m.height = 30
	view := m.View()
	assert.Contains(t, view, "Build")
	assert.Contains(t, view, "Security")
}

func TestFieldBrowser_ViewShowsSectionHeadings(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.width = 80
	m.height = 30
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	view := m.View()
	assert.Contains(t, view, "Git Credentials")
}

func TestFieldBrowser_ModifiedFieldShowsAsterisk(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.modified["build.image"] = "alpine:latest"
	m.width = 80
	m.height = 30
	view := m.View()
	assert.Contains(t, view, "* image")
}

func TestFieldBrowser_LayerBreakdown(t *testing.T) {
	layers := []BrowserLayer{
		{Label: "project/clawker.yaml", Data: map[string]any{
			"build": map[string]any{"image": "alpine:3.19"},
		}},
		{Label: "~/.config/clawker/clawker.yaml", Data: map[string]any{
			"build": map[string]any{"image": "ubuntu:22.04"},
		}},
	}
	m := NewFieldBrowser(BrowserConfig{
		Title:  "Test",
		Fields: testBrowserFields(),
		Layers: layers,
	})
	m.width = 80
	m.height = 40
	view := m.View()

	// Should show layer breakdown with both layer values for build.image.
	assert.Contains(t, view, "layers")
	assert.Contains(t, view, "project/clawker.yaml")
	assert.Contains(t, view, "alpine:3.19")
	assert.Contains(t, view, "~/.config/clawker/clawker.yaml")
	assert.Contains(t, view, "ubuntu:22.04")
}

func TestFieldBrowser_LayerBreakdownSkipsLayersWithoutField(t *testing.T) {
	layers := []BrowserLayer{
		{Label: "project/clawker.yaml", Data: map[string]any{
			"build": map[string]any{"image": "alpine:3.19"},
		}},
		{Label: "~/.config/clawker/clawker.yaml", Data: map[string]any{
			"security": map[string]any{"docker_socket": true},
		}},
	}
	m := NewFieldBrowser(BrowserConfig{
		Title:  "Test",
		Fields: testBrowserFields(),
		Layers: layers,
	})
	m.width = 80
	m.height = 40
	view := m.View()

	// build.image is selected by default — only the layer that has it should show.
	assert.Contains(t, view, "project/clawker.yaml")
	assert.Contains(t, view, "alpine:3.19")
	// The other layer doesn't have build.image, so it shouldn't appear in breakdown.
	assert.NotContains(t, view, "~/.config/clawker/clawker.yaml")
}

func TestLookupMapPath(t *testing.T) {
	data := map[string]any{
		"build": map[string]any{
			"image":    "ubuntu",
			"packages": []any{"git", "curl"},
		},
		"name": "test",
	}
	assert.Equal(t, "ubuntu", lookupMapPath(data, []string{"build", "image"}))
	assert.Equal(t, "[git curl]", lookupMapPath(data, []string{"build", "packages"}))
	assert.Equal(t, "test", lookupMapPath(data, []string{"name"}))
	assert.Equal(t, "", lookupMapPath(data, []string{"missing"}))
	assert.Equal(t, "", lookupMapPath(data, []string{"build", "missing"}))
}

func TestFbFormatHeading(t *testing.T) {
	assert.Equal(t, "Git Credentials", fbFormatHeading("git_credentials"))
	assert.Equal(t, "Otel", fbFormatHeading("otel"))
	assert.Equal(t, "Host Proxy", fbFormatHeading("host_proxy"))
}

func TestFbFormatTabName(t *testing.T) {
	assert.Equal(t, "Build", fbFormatTabName("build"))
	assert.Equal(t, "Security", fbFormatTabName("security"))
	assert.Equal(t, "", fbFormatTabName(""))
}

func TestFbFilterQuit_FiltersQuitMsg(t *testing.T) {
	cmd := fbFilterQuit(tea.Quit)
	msg := cmd()
	assert.Nil(t, msg)
}

func TestFbFilterQuit_PassesThroughOtherMsgs(t *testing.T) {
	expected := tea.KeyMsg{Type: tea.KeyEnter}
	original := func() tea.Msg { return expected }
	cmd := fbFilterQuit(original)
	msg := cmd()
	assert.Equal(t, expected, msg)
}

func TestFieldBrowser_Result(t *testing.T) {
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: testBrowserFields(), SaveTargets: testBrowserSaveTargets()})
	m.modified["build.image"] = "alpine:latest"

	// Not saved yet.
	r := m.Result()
	assert.False(t, r.Saved)
	assert.Equal(t, -1, r.SaveTargetIndex)

	// Trigger save → dialog → confirm.
	m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	r = m.Result()
	assert.True(t, r.Saved)
	assert.Equal(t, 0, r.SaveTargetIndex)
	assert.Equal(t, "alpine:latest", r.Modified["build.image"])
}
