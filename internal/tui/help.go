package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"

	"github.com/schmitthub/clawker/internal/iostreams"
	"github.com/schmitthub/clawker/internal/text"
)

// HelpConfig configures the help bar component.
type HelpConfig struct {
	Width     int
	ShowAll   bool // Show all bindings vs short help
	Separator string
}

// DefaultHelpConfig returns sensible defaults for a help bar.
func DefaultHelpConfig() HelpConfig {
	return HelpConfig{
		Width:     80,
		ShowAll:   false,
		Separator: " \u2022 ", // bullet separator
	}
}

// HelpModel represents a help bar showing key bindings.
type HelpModel struct {
	bindings  []key.Binding
	width     int
	showAll   bool
	separator string
}

// NewHelp creates a new help bar with the given configuration.
func NewHelp(cfg HelpConfig) HelpModel {
	return HelpModel{
		width:     cfg.Width,
		showAll:   cfg.ShowAll,
		separator: cfg.Separator,
	}
}

// SetBindings sets the key bindings to display.
func (m HelpModel) SetBindings(bindings []key.Binding) HelpModel {
	m.bindings = bindings
	return m
}

// SetWidth sets the help bar width.
func (m HelpModel) SetWidth(width int) HelpModel {
	m.width = width
	return m
}

// SetShowAll sets whether to show all bindings.
func (m HelpModel) SetShowAll(showAll bool) HelpModel {
	m.showAll = showAll
	return m
}

// SetSeparator sets the separator between bindings.
func (m HelpModel) SetSeparator(sep string) HelpModel {
	m.separator = sep
	return m
}

// View renders the help bar.
func (m HelpModel) View() string {
	if len(m.bindings) == 0 {
		return ""
	}

	return RenderHelpBar(m.bindings, m.width)
}

// ShortHelp returns a compact help string.
func (m HelpModel) ShortHelp() string {
	if len(m.bindings) == 0 {
		return ""
	}

	var parts []string
	availableWidth := m.width
	sepWidth := text.CountVisibleWidth(m.separator)

	for _, b := range m.bindings {
		if !b.Enabled() {
			continue
		}

		keys := b.Help().Key
		desc := b.Help().Desc
		part := iostreams.HelpKeyStyle.Render(keys) + " " + iostreams.HelpDescStyle.Render(desc)

		partWidth := text.CountVisibleWidth(keys) + 1 + text.CountVisibleWidth(desc)
		if len(parts) > 0 {
			partWidth += sepWidth
		}

		// Check if this binding fits
		if availableWidth-partWidth < 0 && len(parts) > 0 {
			break
		}

		parts = append(parts, part)
		availableWidth -= partWidth
	}

	return strings.Join(parts, m.separator)
}

// FullHelp returns all bindings.
func (m HelpModel) FullHelp() string {
	if len(m.bindings) == 0 {
		return ""
	}

	var parts []string
	for _, b := range m.bindings {
		if !b.Enabled() {
			continue
		}

		keys := b.Help().Key
		desc := b.Help().Desc
		part := iostreams.HelpKeyStyle.Render(keys) + " " + iostreams.HelpDescStyle.Render(desc)
		parts = append(parts, part)
	}

	return strings.Join(parts, m.separator)
}

// Bindings returns the current key bindings.
func (m HelpModel) Bindings() []key.Binding {
	return m.bindings
}

// RenderHelpBar renders a help bar with the given bindings.
func RenderHelpBar(bindings []key.Binding, width int) string {
	if len(bindings) == 0 {
		return ""
	}

	separator := " \u2022 " // bullet
	sepWidth := text.CountVisibleWidth(separator)

	var parts []string
	availableWidth := width

	for _, b := range bindings {
		if !b.Enabled() {
			continue
		}

		keys := b.Help().Key
		desc := b.Help().Desc
		part := iostreams.HelpKeyStyle.Render(keys) + " " + iostreams.HelpDescStyle.Render(desc)

		partWidth := text.CountVisibleWidth(keys) + 1 + text.CountVisibleWidth(desc)
		if len(parts) > 0 {
			partWidth += sepWidth
		}

		// Check if this binding fits
		if availableWidth-partWidth < 0 && len(parts) > 0 {
			break
		}

		parts = append(parts, part)
		availableWidth -= partWidth
	}

	return strings.Join(parts, separator)
}

// RenderHelpGrid renders bindings in a grid layout.
func RenderHelpGrid(bindings []key.Binding, columns, width int) string {
	if len(bindings) == 0 || columns <= 0 {
		return ""
	}

	colWidth := width / columns

	var rows []string
	var currentRow []string

	for i, b := range bindings {
		if !b.Enabled() {
			continue
		}

		keys := b.Help().Key
		desc := b.Help().Desc
		part := iostreams.HelpKeyStyle.Render(keys) + " " + iostreams.HelpDescStyle.Render(desc)

		// Pad to column width
		part = text.PadRight(part, colWidth)
		currentRow = append(currentRow, part)

		if len(currentRow) >= columns || i == len(bindings)-1 {
			rows = append(rows, strings.Join(currentRow, ""))
			currentRow = nil
		}
	}

	return strings.Join(rows, "\n")
}

// NavigationBindings returns common navigation key bindings.
func NavigationBindings() []key.Binding {
	km := DefaultKeyMap()
	return []key.Binding{
		km.Up,
		km.Down,
		km.Enter,
		km.Escape,
	}
}

// QuitBindings returns quit-related key bindings.
func QuitBindings() []key.Binding {
	km := DefaultKeyMap()
	return []key.Binding{
		km.Quit,
	}
}

// AllBindings returns all default key bindings.
func AllBindings() []key.Binding {
	km := DefaultKeyMap()
	return []key.Binding{
		km.Up,
		km.Down,
		km.Left,
		km.Right,
		km.Enter,
		km.Escape,
		km.Tab,
		km.Help,
		km.Quit,
	}
}

// HelpBinding creates a single help binding display.
func HelpBinding(keys, desc string) string {
	return iostreams.HelpKeyStyle.Render(keys) + " " + iostreams.HelpDescStyle.Render(desc)
}

// QuickHelp creates a quick help string from key-description pairs.
func QuickHelp(pairs ...string) string {
	if len(pairs)%2 != 0 {
		// Ensure even number of arguments
		pairs = pairs[:len(pairs)-1]
	}

	var parts []string
	for i := 0; i < len(pairs); i += 2 {
		keys := pairs[i]
		desc := pairs[i+1]
		parts = append(parts, HelpBinding(keys, desc))
	}

	return strings.Join(parts, " \u2022 ")
}
