package tui

import (
	"fmt"
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
		{Path: "security.git_credentials.forward_ssh", Label: "forward_ssh", Kind: BrowserBool, Value: "false", Order: 3},
		{Path: "build.instructions.env", Label: "env", Kind: BrowserMap, Value: "2 entries", ReadOnly: true, Order: 4},
		{Path: "agent.env", Label: "env", Kind: BrowserMap, Value: "1 entry", EditValue: "FOO: bar", Order: 5},
	}
}

func testLayerTargets() []BrowserLayerTarget {
	return []BrowserLayerTarget{
		{Label: "Local", Description: ".clawker.yaml"},
		{Label: "User", Description: "~/.config/clawker/clawker.yaml"},
	}
}

func testBrowserConfig() BrowserConfig {
	return BrowserConfig{
		Title:        "Test",
		Fields:       testBrowserFields(),
		LayerTargets: testLayerTargets(),
	}
}

func TestFieldBrowser_BuildsTabs(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())

	require.Len(t, m.tabs, 3, "should have 3 tabs: build, security, agent")
	assert.Equal(t, "Build", m.tabs[0].name)
	assert.Equal(t, "Security", m.tabs[1].name)
	assert.Equal(t, "Agent", m.tabs[2].name)
}

func TestFieldBrowser_TabRowStructure(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())

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

func TestFieldBrowser_TabSwitching(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())

	assert.Equal(t, 0, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 1, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 2, m.activeTab)
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	assert.Equal(t, 0, m.activeTab, "should wrap around to first tab")
	m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	assert.Equal(t, 2, m.activeTab, "should wrap around to last tab")
}

func TestFieldBrowser_UpDownSkipsHeadings(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
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
			m := NewFieldBrowser(testBrowserConfig())
			updated, cmd := m.Update(key)
			result := updated.(*FieldBrowserModel)
			assert.True(t, result.cancelled)
			assert.NotNil(t, cmd)
		})
	}
}

func TestFieldBrowser_EnterOnReadOnlyStaysInBrowse(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyDown})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateBrowse, result.state)
}

func TestFieldBrowser_EnterTransitionsToEdit(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateEdit, result.state)
	assert.Equal(t, 0, result.editIdx)
}

func TestFieldBrowser_EscFromEditReturnsToBrowse(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	result := updated.(*FieldBrowserModel)
	assert.Equal(t, bsStateBrowse, result.state)
}

func TestFieldBrowser_EditConfirmShowsLayerPicker(t *testing.T) {
	var savedPath, savedValue string
	var savedIdx int
	cfg := testBrowserConfig()
	cfg.OnFieldSaved = func(fieldPath, value string, targetIdx int) error {
		savedPath = fieldPath
		savedValue = value
		savedIdx = targetIdx
		return nil
	}
	m := NewFieldBrowser(cfg)

	// Enter edit on build.image (text field → textarea editor).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)

	// Save the textarea value with Ctrl+S — should show layer picker.
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	require.Equal(t, bsStatePickLayer, m.state)

	// Confirm the default layer (index 0 = Local).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, bsStateBrowse, m.state)
	assert.True(t, m.saved)
	assert.Equal(t, "build.image", savedPath)
	assert.Equal(t, 0, savedIdx)
	assert.NotEmpty(t, savedValue)
}

func TestFieldBrowser_EscFromLayerPickerDiscardsEdit(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())

	// Enter edit → save textarea with Ctrl+S → layer picker.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	require.Equal(t, bsStatePickLayer, m.state)

	// Esc from layer picker → discard, back to browse.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, bsStateBrowse, m.state)
	assert.False(t, m.saved)
}

func TestFieldBrowser_ViewRendersTabBar(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
	m.width = 80
	m.height = 30
	view := m.View()
	assert.Contains(t, view, "Build")
	assert.Contains(t, view, "Security")
}

func TestFieldBrowser_ViewShowsSectionHeadings(t *testing.T) {
	m := NewFieldBrowser(testBrowserConfig())
	m.width = 80
	m.height = 30
	m.Update(tea.KeyMsg{Type: tea.KeyRight})
	view := m.View()
	assert.Contains(t, view, "Git Credentials")
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
			"context":  "",
		},
		"name":  "test",
		"empty": nil,
	}

	val, found := lookupMapPath(data, []string{"build", "image"})
	assert.True(t, found)
	assert.Equal(t, "ubuntu", val)

	val, found = lookupMapPath(data, []string{"build", "packages"})
	assert.True(t, found)
	assert.Equal(t, "[git curl]", val)

	val, found = lookupMapPath(data, []string{"name"})
	assert.True(t, found)
	assert.Equal(t, "test", val)

	// Missing keys return found=false.
	val, found = lookupMapPath(data, []string{"missing"})
	assert.False(t, found)
	assert.Equal(t, "", val)

	val, found = lookupMapPath(data, []string{"build", "missing"})
	assert.False(t, found)
	assert.Equal(t, "", val)

	// Explicitly set empty string returns found=true with empty value.
	val, found = lookupMapPath(data, []string{"build", "context"})
	assert.True(t, found)
	assert.Equal(t, "", val)

	// Explicitly set nil returns found=true with empty value.
	val, found = lookupMapPath(data, []string{"empty"})
	assert.True(t, found)
	assert.Equal(t, "", val)
}

func TestFbFormatHeading(t *testing.T) {
	// fbFormatTabName delegates to fbFormatHeading, so both are tested here.
	assert.Equal(t, "Git Credentials", fbFormatHeading("git_credentials"))
	assert.Equal(t, "Otel", fbFormatHeading("otel"))
	assert.Equal(t, "Host Proxy", fbFormatHeading("host_proxy"))
	assert.Equal(t, "Build", fbFormatHeading("build"))
	assert.Equal(t, "Security", fbFormatHeading("security"))
	assert.Equal(t, "", fbFormatHeading(""))
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

func TestRowLines(t *testing.T) {
	heading := browserRow{isHeading: true, heading: "Section"}
	fieldNoDesc := browserRow{field: &BrowserField{Path: "a.b", Label: "B"}}
	fieldWithDesc := browserRow{field: &BrowserField{Path: "a.c", Label: "C", Description: "Help text"}}

	assert.Equal(t, 1, rowLines(heading), "heading should be 1 line")
	assert.Equal(t, 1, rowLines(fieldNoDesc), "field without description should be 1 line")
	assert.Equal(t, 2, rowLines(fieldWithDesc), "field with description should be 2 lines")
}

func TestVisibleRows_AccountsForDescriptionHeight(t *testing.T) {
	// All fields have descriptions (2 lines each).
	fields := []BrowserField{
		{Path: "build.a", Label: "A", Description: "desc a"},
		{Path: "build.b", Label: "B", Description: "desc b"},
		{Path: "build.c", Label: "C", Description: "desc c"},
		{Path: "build.d", Label: "D", Description: "desc d"},
		{Path: "build.e", Label: "E", Description: "desc e"},
	}
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: fields})
	m.width = 80
	m.height = 20

	visible := m.visibleRows()
	// With chrome=7, visibleLines = 20-7 = 13. Each row is 2 lines.
	// 13 / 2 = 6, but only 5 rows exist, so visible = 5.
	assert.Equal(t, 5, visible, "should fit all 5 rows in 13 available lines")

	// Shrink terminal: visibleLines = max(11-7, 5) = 5 (min-clamped).
	// Only 2 two-line rows fit in 5 lines, so visibleRows clamps to minimum of 3.
	m.height = 11
	visible = m.visibleRows()
	assert.Equal(t, 3, visible, "should clamp to minimum of 3")
}

func TestVisibleRows_MixedHeightRows(t *testing.T) {
	fields := []BrowserField{
		{Path: "build.a", Label: "A"},                               // 1 line
		{Path: "build.b", Label: "B", Description: "desc b"},        // 2 lines
		{Path: "build.c", Label: "C"},                               // 1 line
		{Path: "security.d", Label: "D", Description: "desc d"},     // 2 lines
		{Path: "security.e", Label: "E"},                            // 1 line
		{Path: "security.git.f", Label: "F", Description: "desc f"}, // 2 lines
	}
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: fields})
	m.width = 80
	m.height = 18 // 18-7 = 11 available lines

	visible := m.visibleRows()
	// Build tab has: a(1) + b(2) + c(1) = 4 lines, 3 rows. All fit in 11 lines.
	assert.GreaterOrEqual(t, visible, 3, "should fit all build tab rows")
}

func TestFieldBrowser_ViewShowsDescriptions(t *testing.T) {
	fields := []BrowserField{
		{Path: "build.image", Label: "image", Kind: BrowserText, Value: "ubuntu:22.04", Description: "Docker base image"},
	}
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: fields})
	m.width = 80
	m.height = 30
	view := m.View()
	assert.Contains(t, view, "Docker base image", "should render field description in view")
}

func TestEnsureVisible_ConvergesWithVariableHeightRows(t *testing.T) {
	// Create enough 2-line rows that scrolling is needed.
	var fields []BrowserField
	for i := 0; i < 20; i++ {
		fields = append(fields, BrowserField{
			Path:        fmt.Sprintf("build.f%d", i),
			Label:       fmt.Sprintf("Field %d", i),
			Description: fmt.Sprintf("Description for field %d", i),
		})
	}
	m := NewFieldBrowser(BrowserConfig{Title: "Test", Fields: fields})
	m.width = 80
	m.height = 20

	// Navigate to the last field.
	for i := 0; i < 19; i++ {
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}

	// Active row should be visible after ensureVisible converges.
	visible := m.visibleRows()
	assert.GreaterOrEqual(t, m.activeRow, m.scrollOff, "active row should be at or after scroll offset")
	assert.Less(t, m.activeRow, m.scrollOff+visible, "active row should be within visible window")
}

func TestFieldBrowser_Result(t *testing.T) {
	cfg := testBrowserConfig()
	cfg.OnFieldSaved = func(fieldPath, value string, targetIdx int) error { return nil }
	m := NewFieldBrowser(cfg)

	// Not saved yet.
	r := m.Result()
	assert.False(t, r.Saved)

	// Navigate to docker_socket (bool field, currently "false").
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // switch to Security tab
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // enter edit on docker_socket (bool select, default idx=1=false)

	require.Equal(t, bsStateEdit, m.state)

	// Select "true" (move up to index 0) then confirm.
	m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm → layer picker
	require.Equal(t, bsStatePickLayer, m.state)

	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // confirm layer

	r = m.Result()
	assert.True(t, r.Saved)
	assert.Equal(t, 1, r.SavedCount)
	// The field's base value should reflect the saved value.
	assert.Equal(t, "true", m.fields[2].Value) // security.docker_socket
}

func TestFieldBrowser_MapFieldUsesKVEditor(t *testing.T) {
	cfg := testBrowserConfig()
	m := NewFieldBrowser(cfg)

	// Navigate to Agent tab (3rd tab) where agent.env lives.
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Security
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Agent

	// Enter edit on agent.env (the only field in Agent tab).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	assert.Equal(t, ekKV, m.editKind, "map field should dispatch to KV editor")
}

func TestFieldBrowser_MapFieldConfirmFlowsToLayerPicker(t *testing.T) {
	var savedPath string
	cfg := testBrowserConfig()
	cfg.OnFieldSaved = func(fieldPath, value string, targetIdx int) error {
		savedPath = fieldPath
		return nil
	}
	m := NewFieldBrowser(cfg)

	// Navigate to Agent tab → agent.env.
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Security
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Agent
	m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // edit
	require.Equal(t, ekKV, m.editKind)

	// Confirm the KV editor (Enter = done).
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStatePickLayer, m.state)

	// Confirm layer.
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	assert.Equal(t, bsStateBrowse, m.state)
	assert.Equal(t, "agent.env", savedPath)
}

func TestFieldBrowser_MapFieldCancelReturnsToBrowse(t *testing.T) {
	cfg := testBrowserConfig()
	m := NewFieldBrowser(cfg)

	// Navigate to Agent tab → agent.env → edit.
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Security
	m.Update(tea.KeyMsg{Type: tea.KeyRight}) // Agent
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)

	// Cancel from KV editor.
	m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assert.Equal(t, bsStateBrowse, m.state)
}

func TestFieldBrowser_StructSliceStillUsesTextarea(t *testing.T) {
	fields := []BrowserField{
		{Path: "build.items", Label: "items", Kind: BrowserStructSlice, Value: "3 items", EditValue: "- cmd: echo\n"},
	}
	cfg := BrowserConfig{
		Title:        "Test",
		Fields:       fields,
		LayerTargets: testLayerTargets(),
	}
	m := NewFieldBrowser(cfg)

	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	require.Equal(t, bsStateEdit, m.state)
	assert.Equal(t, ekTextarea, m.editKind, "struct slice should still use textarea editor")
}
