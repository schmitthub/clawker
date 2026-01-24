package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	"github.com/stretchr/testify/assert"
)

func TestDefaultHelpConfig(t *testing.T) {
	cfg := DefaultHelpConfig()
	assert.Equal(t, 80, cfg.Width)
	assert.False(t, cfg.ShowAll)
	assert.NotEmpty(t, cfg.Separator)
}

func TestNewHelp(t *testing.T) {
	cfg := HelpConfig{
		Width:     100,
		ShowAll:   true,
		Separator: " | ",
	}

	h := NewHelp(cfg)
	assert.Equal(t, 100, h.width)
	assert.True(t, h.showAll)
	assert.Equal(t, " | ", h.separator)
}

func TestHelpModel_SetBindings(t *testing.T) {
	h := NewHelp(DefaultHelpConfig())
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}

	h = h.SetBindings(bindings)
	assert.Equal(t, bindings, h.Bindings())
}

func TestHelpModel_SetWidth(t *testing.T) {
	h := NewHelp(DefaultHelpConfig())
	h = h.SetWidth(120)
	assert.Equal(t, 120, h.width)
}

func TestHelpModel_SetShowAll(t *testing.T) {
	h := NewHelp(DefaultHelpConfig())
	h = h.SetShowAll(true)
	assert.True(t, h.showAll)
}

func TestHelpModel_SetSeparator(t *testing.T) {
	h := NewHelp(DefaultHelpConfig())
	h = h.SetSeparator(" | ")
	assert.Equal(t, " | ", h.separator)
}

func TestHelpModel_View(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	}

	h := NewHelp(DefaultHelpConfig()).SetBindings(bindings)
	view := h.View()

	assert.Contains(t, view, "q")
	assert.Contains(t, view, "quit")
}

func TestHelpModel_View_Empty(t *testing.T) {
	h := NewHelp(DefaultHelpConfig())
	view := h.View()
	assert.Empty(t, view)
}

func TestHelpModel_ShortHelp(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	}

	h := NewHelp(DefaultHelpConfig()).SetBindings(bindings)
	result := h.ShortHelp()

	assert.Contains(t, result, "q")
	assert.Contains(t, result, "quit")
}

func TestHelpModel_FullHelp(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "select")),
	}

	h := NewHelp(DefaultHelpConfig()).SetBindings(bindings)
	result := h.FullHelp()

	assert.Contains(t, result, "q")
	assert.Contains(t, result, "quit")
	assert.Contains(t, result, "?")
	assert.Contains(t, result, "help")
	assert.Contains(t, result, "enter")
	assert.Contains(t, result, "select")
}

func TestRenderHelpBar(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	}

	result := RenderHelpBar(bindings, 80)
	assert.Contains(t, result, "q")
	assert.Contains(t, result, "quit")
}

func TestRenderHelpBar_Empty(t *testing.T) {
	result := RenderHelpBar([]key.Binding{}, 80)
	assert.Empty(t, result)
}

func TestRenderHelpBar_DisabledBinding(t *testing.T) {
	disabledBinding := key.NewBinding(
		key.WithKeys("x"),
		key.WithHelp("x", "disabled"),
		key.WithDisabled(),
	)
	enabledBinding := key.NewBinding(
		key.WithKeys("y"),
		key.WithHelp("y", "enabled"),
	)

	result := RenderHelpBar([]key.Binding{disabledBinding, enabledBinding}, 80)
	assert.NotContains(t, result, "disabled")
	assert.Contains(t, result, "enabled")
}

func TestRenderHelpGrid(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "action a")),
		key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "action b")),
		key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "action c")),
		key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "action d")),
	}

	result := RenderHelpGrid(bindings, 2, 80)
	assert.Contains(t, result, "a")
	assert.Contains(t, result, "action a")
	assert.Contains(t, result, "b")
	// Should have newlines for rows
	assert.Contains(t, result, "\n")
}

func TestRenderHelpGrid_Empty(t *testing.T) {
	result := RenderHelpGrid([]key.Binding{}, 2, 80)
	assert.Empty(t, result)
}

func TestRenderHelpGrid_ZeroColumns(t *testing.T) {
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "action")),
	}
	result := RenderHelpGrid(bindings, 0, 80)
	assert.Empty(t, result)
}

func TestNavigationBindings(t *testing.T) {
	bindings := NavigationBindings()
	assert.NotEmpty(t, bindings)

	// Should have up, down, enter, escape
	var keys []string
	for _, b := range bindings {
		keys = append(keys, b.Help().Key)
	}

	assert.Contains(t, keys, "up/k")
	assert.Contains(t, keys, "down/j")
	assert.Contains(t, keys, "enter")
	assert.Contains(t, keys, "esc")
}

func TestQuitBindings(t *testing.T) {
	bindings := QuitBindings()
	assert.NotEmpty(t, bindings)
	assert.Equal(t, 1, len(bindings))
	assert.Equal(t, "q", bindings[0].Help().Key)
}

func TestAllBindings(t *testing.T) {
	bindings := AllBindings()
	assert.NotEmpty(t, bindings)
	// Should have all default bindings
	assert.GreaterOrEqual(t, len(bindings), 5)
}

func TestHelpBinding(t *testing.T) {
	result := HelpBinding("ctrl+c", "quit")
	assert.Contains(t, result, "ctrl+c")
	assert.Contains(t, result, "quit")
}

func TestQuickHelp(t *testing.T) {
	result := QuickHelp("q", "quit", "?", "help", "enter", "select")
	assert.Contains(t, result, "q")
	assert.Contains(t, result, "quit")
	assert.Contains(t, result, "?")
	assert.Contains(t, result, "help")
	assert.Contains(t, result, "enter")
	assert.Contains(t, result, "select")
}

func TestQuickHelp_OddArgs(t *testing.T) {
	// Should handle odd number of args gracefully
	result := QuickHelp("q", "quit", "extra")
	assert.Contains(t, result, "q")
	assert.Contains(t, result, "quit")
	assert.NotContains(t, result, "extra")
}

func TestQuickHelp_Empty(t *testing.T) {
	result := QuickHelp()
	assert.Empty(t, result)
}
